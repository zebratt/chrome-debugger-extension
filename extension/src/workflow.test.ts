import assert from "node:assert/strict";
import test from "node:test";
import { snapshotPage, typeIntoNode } from "./page.ts";

function fixture(options: { type?: string; disabled?: boolean; readonly?: boolean; missingValue?: boolean; wrongValue?: boolean } = {}) {
  const original = globalThis.chrome;
  let value = "old";
  const events: { method: string; params: Record<string, unknown> }[] = [];
  const input = () => ({ nodeId: "i", parentId: "group", backendDOMNodeId: 7, role: { value: "textbox" }, name: { value: "Name" }, value: { value }, properties: [] });
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async () => ({ id: 90, url: "https://example.test", incognito: false }) },
    debugger: {
      attach: async () => {}, detach: async () => {},
      sendCommand: async (_: unknown, method: string, params: Record<string, unknown>) => {
        events.push({ method, params });
        if (method === "Accessibility.getFullAXTree") return { nodes: [
          { nodeId: "group", role: { value: "group" }, name: { value: "Profile details" }, childIds: ["i"] },
          input(),
          { backendDOMNodeId: 8, role: { value: "none" }, name: { value: "" }, ignored: true },
          { backendDOMNodeId: 9, role: { value: "InlineTextBox" }, name: { value: "duplicate" } },
          { backendDOMNodeId: 10, role: { value: "link" }, name: { value: "Guide" }, properties: [{ name: "url", value: { value: "https://example.test/guide" } }] },
        ] };
        if (method === "DOM.describeNode") return { node: { nodeName: "INPUT", attributes: ["type", options.type || "text", ...(options.disabled ? ["disabled", ""] : []), ...(options.readonly ? ["readonly", ""] : [])] } };
        if (method === "Accessibility.getPartialAXTree") return { nodes: [{ ...input(), value: options.missingValue ? undefined : { value: options.wrongValue ? "wrong" : value } }] };
        if (method === "Input.insertText") value = String(params.text);
        if (method === "Input.dispatchKeyEvent" && params.key === "Backspace") value = "";
        return {};
      },
    },
  };
  return { events, value: () => value, restore() { (globalThis as unknown as { chrome: unknown }).chrome = original; } };
}

test("compact snapshot preserves usable control metadata and omits duplicate or ignored nodes", async () => {
  const f = fixture();
  try {
    const snap = await snapshotPage(90, true);
    assert.equal(snap.metadataVersion, 1);
    assert.equal(snap.url, "https://example.test");
    assert.equal(snap.nodes.length, 2);
    assert.equal(snap.nodes[0].editable, true);
    assert.equal(snap.nodes[0].value, "old");
    assert.equal(snap.nodes[0].context, "Profile details");
    assert.equal(snap.nodes[1].href, "https://example.test/guide");
  } finally { f.restore(); }
});

test("password, file, disabled and read-only input values are not exported", async () => {
  for (const options of [{ type: "password" }, { type: "file" }, { disabled: true }, { readonly: true }]) {
    const f = fixture(options);
    try {
      const snap = await snapshotPage(90, true);
      assert.equal(snap.nodes[0].editable, false);
      assert.equal(snap.nodes[0].value, undefined);
      await assert.rejects(typeIntoNode(90, snap.snapshotId, String(snap.nodes[0].nodeRef), "new", true), /INVALID_TARGET/);
      assert.equal(f.value(), "old");
    } finally { f.restore(); }
  }
});

test("replacement uses native editing and verifies the actual target value", async () => {
  const f = fixture();
  try {
    const snap = await snapshotPage(90, true);
    const result = await typeIntoNode(90, snap.snapshotId, String(snap.nodes[0].nodeRef), "Matt", true);
    assert.deepEqual(result, { verified: true });
    assert.equal(f.value(), "Matt");
    const selection = f.events.findIndex((e) => e.method === "Input.dispatchKeyEvent" && Array.isArray(e.params.commands));
    const insertion = f.events.findIndex((e) => e.method === "Input.insertText");
    assert.ok(selection >= 0 && insertion > selection);
    assert.equal(f.events.at(-1)?.method, "Accessibility.getPartialAXTree");
  } finally { f.restore(); }
});

test("replacement never reports verification for missing or incorrect values", async () => {
  for (const options of [{ wrongValue: true }, { missingValue: true }]) {
    const f = fixture(options);
    try {
      const snap = await snapshotPage(90, true);
      const result = await typeIntoNode(90, snap.snapshotId, String(snap.nodes[0].nodeRef), "", true);
      assert.deepEqual(result, { verified: false });
      assert.equal(f.value(), "");
    } finally { f.restore(); }
  }
});

test("empty replacement is verified from the DOM when Chrome omits its AX value", async () => {
  const f = fixture({ missingValue: true });
  const debuggerAPI = chrome.debugger;
  const originalSend = debuggerAPI.sendCommand;
  (debuggerAPI as unknown as { sendCommand: unknown }).sendCommand = async (target: unknown, method: string, params: Record<string, unknown>) => {
    if (method === "DOM.resolveNode") return { object: { objectId: "input-object" } };
    if (method === "Runtime.callFunctionOn") return { result: { value: f.value() } };
    return (originalSend as unknown as (target: unknown, method: string, params: Record<string, unknown>) => Promise<unknown>)(target, method, params);
  };
  try {
    const snap = await snapshotPage(90, true);
    assert.deepEqual(await typeIntoNode(90, snap.snapshotId, String(snap.nodes[0].nodeRef), "", true), { verified: true });
  } finally { f.restore(); }
});

test("named regions retain nearby explanatory text without reading control values", async () => {
  const { nodeContext } = await import('./metadata.ts');
  const link = { nodeId: 'link', parentId: 'region', role: { value: 'link' }, name: { value: 'Open docs' } };
  const region = { nodeId: 'region', role: { value: 'region' }, name: { value: 'Reports' }, childIds: ['paragraph', 'input', 'link'] };
  const paragraph = { nodeId: 'paragraph', role: { value: 'paragraph' }, childIds: ['text'] };
  const text = { nodeId: 'text', role: { value: 'StaticText' }, name: { value: 'Export reports and resolve amount errors.' } };
  const input = { nodeId: 'input', role: { value: 'textbox' }, name: { value: 'Token' }, value: { value: 'must-not-appear' } };
  const context = nodeContext(link, new Map([link, region, paragraph, text, input].map(n => [n.nodeId, n])));
  assert.equal(context, 'Reports / Export reports and resolve amount errors.');
});
