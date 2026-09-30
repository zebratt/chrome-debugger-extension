import assert from "node:assert/strict";
import test from "node:test";
import { handleMessage } from "./dispatch.ts";

test("dispatcher preserves the request ID across a tab list result", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { query: async () => [{ id: 3, title: "Example", url: "https://example.com", incognito: false }] },
  };
  try {
    const response = await handleMessage({ jsonrpc: "2.0", id: "req-1", method: "tab.list", params: { profileId: "p1" } });
    assert.deepEqual(response, { jsonrpc: "2.0", id: "req-1", result: { tabs: [{ tabId: 3, title: "Example", url: "https://example.com" }] } });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("dispatcher reports malformed and unknown methods", async () => {
  const malformed = await handleMessage({ jsonrpc: "2.0", method: "tab.list" });
  assert.equal(malformed.error?.data.kind, "INVALID_REQUEST");
  const unknown = await handleMessage({ jsonrpc: "2.0", id: 3, method: "unknown.method" });
  assert.equal(unknown.id, 3);
  assert.equal(unknown.error?.data.kind, "METHOD_NOT_FOUND");
});

test("guarded keys cannot silently fall back to the current focus", async () => {
  for (const params of [{ tabId: 7, key: "Enter", guarded: true }, { tabId: 7, key: "Enter", guarded: "true" }]) {
    const response = await handleMessage({ jsonrpc: "2.0", id: "key", method: "tab.key", params });
    assert.equal(response.error?.data.kind, "INVALID_PARAMS");
  }
});

test("dispatcher checks the live URL of a target tab", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, title: "Page", url: "https://example.com", incognito: false }) },
  };
  try {
    const response = await handleMessage({ jsonrpc: "2.0", id: "req-2", method: "tab.info", params: { tabId: 7 } });
    assert.deepEqual(response.result, { tabId: 7, title: "Page", url: "https://example.com" });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("dispatcher exposes a screenshot as a JSON-RPC result", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://example.com", incognito: false }) },
    debugger: { attach: async () => {}, detach: async () => {}, sendCommand: async () => ({ data: "aGVsbG8=" }) },
  };
  try {
    const response = await handleMessage({ jsonrpc: "2.0", id: "shot", method: "tab.screenshot", params: { tabId: 7 } });
    assert.deepEqual(response.result, { mimeType: "image/png", dataBase64: "aGVsbG8=", size: 5 });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("dispatcher accepts a node reference from its own snapshot", async () => {
  const original = globalThis.chrome;
  const events: string[] = [];
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, title: "Example", url: "https://example.com", incognito: false }) },
    debugger: {
      attach: async () => {}, detach: async () => {},
      sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
        if (method === "Accessibility.getFullAXTree") return { nodes: [{ backendDOMNodeId: 12, role: { value: "button" }, name: { value: "Go" } }] };
        if (method === "DOM.getBoxModel") return { model: { content: [0, 0, 20, 0, 20, 20, 0, 20] } };
        if (method === "Input.dispatchMouseEvent") events.push(String(params.type));
        return {};
      },
    },
  };
  try {
    const snapshot = await handleMessage({ jsonrpc: "2.0", id: "snap", method: "tab.snapshot", params: { tabId: 7 } });
    const result = snapshot.result as { snapshotId: string; nodes: { nodeRef: string }[] };
    const clicked = await handleMessage({ jsonrpc: "2.0", id: "click", method: "tab.click", params: { tabId: 7, snapshotId: result.snapshotId, nodeRef: result.nodes[0].nodeRef } });
    assert.deepEqual(clicked.result, { clicked: true });
    assert.deepEqual(events, ["mousePressed", "mouseReleased"]);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("page request rejects a tab that changed URL after broker validation", async () => {
  const original = globalThis.chrome;
  let attached = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://blocked.example.com", incognito: false }) },
    debugger: { attach: async () => { attached = true; }, detach: async () => {}, sendCommand: async () => ({ data: "aGVsbG8=" }) },
  };
  try {
    const response = await handleMessage({ jsonrpc: "2.0", id: "changed", method: "tab.screenshot", params: { tabId: 7, expectedUrl: "https://allowed.example.com" } });
    assert.equal(response.error?.data.kind, "STALE_SNAPSHOT");
    assert.equal(attached, false);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("extension reload acknowledges before scheduling the restart", async () => {
  const original = globalThis.chrome;
  let reloaded = false;
  (globalThis as unknown as { chrome: unknown }).chrome = { runtime: { reload: () => { reloaded = true; } } };
  try {
    const response = await handleMessage({ jsonrpc: "2.0", id: "reload", method: "extension.reload" });
    assert.deepEqual(response.result, { reloading: true });
    assert.equal(reloaded, false);
    await new Promise((resolve) => setTimeout(resolve, 5));
    assert.equal(reloaded, true);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
