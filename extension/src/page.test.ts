import assert from "node:assert/strict";
import test from "node:test";
import { clickNode, pressKey, screenshotPage, scrollPage, snapshotPage, typeIntoNode, waitForText } from "./page.ts";

test("snapshot node can be clicked through a fresh backend DOM reference", async () => {
  const original = globalThis.chrome;
  const inputEvents: Record<string, unknown>[] = [];
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, title: "Example", url: "https://example.com", incognito: false }) },
    debugger: {
      attach: async () => {},
      detach: async () => {},
      sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
        if (method === "Accessibility.getFullAXTree") return { nodes: [{ backendDOMNodeId: 99, role: { value: "button" }, name: { value: "Submit" } }] };
        if (method === "DOM.getBoxModel") return { model: { content: [0, 0, 100, 0, 100, 40, 0, 40] } };
        if (method === "Input.dispatchMouseEvent") inputEvents.push(params);
        return {};
      },
    },
  };
  try {
    const snapshot = await snapshotPage(7);
    assert.equal(snapshot.nodes.length, 1);
    assert.equal(snapshot.nodes[0].role, "button");
    assert.equal(snapshot.nodes[0].name, "Submit");
    await clickNode(7, snapshot.snapshotId, snapshot.nodes[0].nodeRef);
    assert.deepEqual(inputEvents.map((event) => event.type), ["mousePressed", "mouseReleased"]);
    assert.deepEqual(inputEvents.map((event) => [event.x, event.y]), [[50, 20], [50, 20]]);
    await assert.rejects(clickNode(7, "old-snapshot", snapshot.nodes[0].nodeRef), /STALE_SNAPSHOT/);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("screenshot returns bounded PNG bytes", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://example.com", incognito: false }) },
    storage: { local: { set: async () => {} } },
    debugger: {
      attach: async () => {},
      detach: async () => {},
      sendCommand: async () => ({ data: "aGVsbG8=" }),
    },
  };
  try {
    assert.deepEqual(await screenshotPage(7), { mimeType: "image/png", dataBase64: "aGVsbG8=", size: 5 });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("typing focuses the referenced input and inserts text", async () => {
  const original = globalThis.chrome;
  const methods: string[] = [];
  let inserted = "";
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://example.com", incognito: false }) },
    debugger: {
      attach: async () => {}, detach: async () => {},
      sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
        methods.push(method);
        if (method === "Accessibility.getFullAXTree") return { nodes: [{ backendDOMNodeId: 5, role: { value: "textbox" }, name: { value: "Name" } }] };
        if (method === "DOM.getBoxModel") return { model: { content: [0, 0, 100, 0, 100, 20, 0, 20] } };
        if (method === "Input.insertText") inserted = String(params.text);
        return {};
      },
    },
  };
  try {
    const snap = await snapshotPage(7);
    await typeIntoNode(7, snap.snapshotId, snap.nodes[0].nodeRef, "Matt");
    assert.equal(inserted, "Matt");
    assert.deepEqual(methods.slice(-4), ["DOM.getBoxModel", "Input.dispatchMouseEvent", "Input.dispatchMouseEvent", "Input.insertText"]);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("keyboard and page scroll use bounded commands", async () => {
  const original = globalThis.chrome;
  const inputs: { method: string; params: Record<string, unknown> }[] = [];
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://example.com", incognito: false }) },
    storage: { local: { set: async () => {} } },
    debugger: {
      attach: async () => {}, detach: async () => {},
      sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
        inputs.push({ method, params });
        return {};
      },
    },
  };
  try {
    await pressKey(7, "Enter");
    await scrollPage(7, 200);
    assert.deepEqual(inputs.slice(0, 2).map((item) => item.params.type), ["keyDown", "keyUp"]);
    assert.equal(inputs[0].params.key, "Enter");
    assert.equal(inputs[2].method, "Runtime.evaluate");
    assert.equal(inputs[2].params.expression, "window.scrollBy(0, 200)");
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("wait for text observes page state until the text appears", async () => {
  const original = globalThis.chrome;
  let checks = 0;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, url: "https://example.com", incognito: false }) },
    debugger: {
      attach: async () => {}, detach: async () => {},
      sendCommand: async (_target: unknown, method: string) => {
        if (method === "Runtime.evaluate") return { result: { value: ++checks >= 2 } };
        return {};
      },
    },
  };
  try {
    assert.deepEqual(await waitForText(7, "Ready", 500), { found: true });
    assert.equal(checks, 2);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
