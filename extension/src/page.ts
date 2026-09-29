import { sendCDPCommand, withDebugger } from "./debugger.ts";
import { getTabInfo } from "./tabs.ts";

type Snapshot = { id: string; url: string; references: Map<string, number> };
const snapshots = new Map<number, Snapshot>();

export async function snapshotPage(tabId: number) {
  const info = await getTabInfo(tabId);
  const result = await sendCDPCommand(tabId, "Accessibility.getFullAXTree", {}) as {
    nodes?: { backendDOMNodeId?: number; role?: { value?: unknown }; name?: { value?: unknown } }[];
  };
  const snapshotId = crypto.randomUUID();
  const references = new Map<string, number>();
  const nodes = (result.nodes || []).flatMap((node) => {
    if (node.backendDOMNodeId === undefined) return [];
    const nodeRef = crypto.randomUUID();
    references.set(nodeRef, node.backendDOMNodeId);
    return [{ nodeRef, role: String(node.role?.value || ""), name: String(node.name?.value || "") }];
  });
  snapshots.set(tabId, { id: snapshotId, url: info.url, references });
  return { snapshotId, documentId: snapshotId, nodes };
}

export async function clickNode(tabId: number, snapshotId: string, nodeRef: string): Promise<void> {
	const backendNodeId = await resolveNode(tabId, snapshotId, nodeRef);
	await withDebugger(tabId, (send) => clickBackendNode(send, backendNodeId));
}

async function resolveNode(tabId: number, snapshotId: string, nodeRef: string): Promise<number> {
  const snapshot = snapshots.get(tabId);
  const backendNodeId = snapshot?.references.get(nodeRef);
  if (!snapshot || snapshot.id !== snapshotId || backendNodeId === undefined) {
    throw new Error("STALE_SNAPSHOT: node reference is no longer valid");
  }
  const info = await getTabInfo(tabId);
  if (info.url !== snapshot.url) {
    snapshots.delete(tabId);
    throw new Error("STALE_SNAPSHOT: tab URL changed");
  }
  return backendNodeId;
}

async function clickBackendNode(send: (method: string, params: Record<string, unknown>) => Promise<unknown>, backendNodeId: number): Promise<void> {
    let model: { model?: { content?: number[] } };
    try {
      model = await send("DOM.getBoxModel", { backendNodeId }) as typeof model;
    } catch {
      throw new Error("STALE_SNAPSHOT: node was removed");
    }
    const content = model.model?.content;
    if (!content || content.length !== 8) throw new Error("STALE_SNAPSHOT: node has no box");
    const x = (content[0] + content[2] + content[4] + content[6]) / 4;
    const y = (content[1] + content[3] + content[5] + content[7]) / 4;
    await send("Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
    await send("Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
}

export async function screenshotPage(tabId: number) {
  await getTabInfo(tabId);
  const result = await sendCDPCommand(tabId, "Page.captureScreenshot", { format: "png" }) as { data?: string };
  if (!result.data) throw new Error("CHROME_ERROR: screenshot has no image data");
  const size = atob(result.data).length;
  if (size > 8 * 1024 * 1024) throw new Error("PAYLOAD_TOO_LARGE: screenshot exceeds 8 MiB");
  return { mimeType: "image/png", dataBase64: result.data, size };
}

export async function typeIntoNode(tabId: number, snapshotId: string, nodeRef: string, text: string): Promise<void> {
  const backendNodeId = await resolveNode(tabId, snapshotId, nodeRef);
  await withDebugger(tabId, async (send) => {
    await clickBackendNode(send, backendNodeId);
    await send("Input.insertText", { text });
  });
}

export async function pressKey(tabId: number, key: string): Promise<void> {
  await getTabInfo(tabId);
  const codes: Record<string, number> = { Enter: 13, Tab: 9, Escape: 27, Backspace: 8, ArrowUp: 38, ArrowDown: 40, ArrowLeft: 37, ArrowRight: 39 };
  const code = codes[key];
  if (code === undefined) throw new Error("INVALID_PARAMS: unsupported key");
  await withDebugger(tabId, async (send) => {
    await send("Input.dispatchKeyEvent", { type: "keyDown", key, code: key, windowsVirtualKeyCode: code });
    await send("Input.dispatchKeyEvent", { type: "keyUp", key, code: key, windowsVirtualKeyCode: code });
  });
}

export async function scrollPage(tabId: number, deltaY: number): Promise<void> {
  if (!Number.isFinite(deltaY)) throw new Error("INVALID_PARAMS: deltaY must be finite");
  await getTabInfo(tabId);
  await sendCDPCommand(tabId, "Runtime.evaluate", { expression: `window.scrollBy(0, ${deltaY})` });
}

export async function waitForText(tabId: number, text: string, timeoutMs: number): Promise<{ found: boolean }> {
  if (!text || timeoutMs <= 0 || timeoutMs > 10000) throw new Error("INVALID_PARAMS: text and timeoutMs are required");
  await getTabInfo(tabId);
  const deadline = Date.now() + timeoutMs;
  const expression = `Boolean(document.body && document.body.innerText.includes(${JSON.stringify(text)}))`;
  return withDebugger(tabId, async (send) => {
    for (;;) {
      const response = await send("Runtime.evaluate", { expression, returnByValue: true }) as { result?: { value?: unknown } };
      if (response.result?.value === true) return { found: true };
      if (Date.now() >= deadline) throw new Error("TIMEOUT: text did not appear");
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  });
}
