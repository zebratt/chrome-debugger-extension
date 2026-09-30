import { sendCDPCommand, withDebugger } from "./debugger.ts";
import { getTabInfo } from "./tabs.ts";
import { type AXNode, describeInput, inputRoles, nodeContext, property } from "./metadata.ts";

import { signature, pageState, controlDetails, assertGuard, hitPoint, selectObserved, type SelectOption } from "./guards.ts";

type Snapshot = { newTabLinks?: Set<string>; documentKey?: string; guards?: Map<string,string>; options?: Map<string,SelectOption[]>; id: string; tabId: number; url: string; references: Map<string, number>; createdAt: number };
const snapshots = new Map<string, Snapshot>();
const snapshotTTL = 60_000;
const maxSnapshotsPerTab = 16;

function pruneSnapshots(tabId: number) {
  const now = Date.now();
  for (const [id, snapshot] of snapshots) {
    if (now - snapshot.createdAt >= snapshotTTL) snapshots.delete(id);
  }
  const existing = [...snapshots].filter(([, snapshot]) => snapshot.tabId === tabId);
  for (const [id] of existing.slice(0, Math.max(0, existing.length - maxSnapshotsPerTab + 1))) {
    snapshots.delete(id);
  }
}

function invalidateTabSnapshots(tabId: number) {
  for (const [id, snapshot] of snapshots) {
    if (snapshot.tabId === tabId) snapshots.delete(id);
  }
}

export async function snapshotPage(tabId: number, compact = false, detailed = false) {
  const info = await getTabInfo(tabId);
  const snapshotId = crypto.randomUUID();
  const references = new Map<string, number>();
  const guards = new Map<string,string>();
  const options = new Map<string,SelectOption[]>();
  const newTabLinks = new Set<string>();
  let state: Record<string,unknown> | undefined;
  const nodes = await withDebugger(tabId, async (send) => {
    if (detailed) {state = await pageState(send);if(state.documentURL!==info.url)throw new Error("STALE_SNAPSHOT: tab and document URL differ while navigating");}
    const result = await send("Accessibility.getFullAXTree", {}) as { nodes?: AXNode[] };
    const all = new Map((result.nodes || []).map((node) => [node.nodeId || "", node]));
    const output: Record<string, unknown>[] = [];
    let inputCount = 0;
    for (const node of result.nodes || []) {
      if (node.backendDOMNodeId === undefined) continue;
      const role = String(node.role?.value || "");
      const name = String(node.name?.value || "");
      if (compact && (node.ignored || role === "InlineTextBox" || !name.trim() && !inputRoles.has(role))) continue;
      const nodeRef = crypto.randomUUID();
      references.set(nodeRef, node.backendDOMNodeId);
      if (detailed) guards.set(nodeRef, signature(node));
      const item: Record<string, unknown> = { nodeRef, role, name, ignored: Boolean(node.ignored), disabled: property(node, "disabled") === true };
      if (detailed && ["textbox","searchbox","combobox","button","link","checkbox","radio","switch","tab","menuitem","option","gridcell"].includes(role)) {
        if(inputCount++ >= 64) { item.visible=false; item.editable=false; item.metadataUnavailable=true; }
        else {
          try {
            const details = await controlDetails(send,node.backendDOMNodeId);
            Object.assign(item,details);
            if(details.editable&&typeof node.value?.value === "string")item.value=node.value.value;
            if(details.options)options.set(nodeRef,details.options);
            if(details.opensNewTab)newTabLinks.add(nodeRef);
          } catch { item.visible=false; item.editable=false; item.metadataUnavailable=true; }
        }

      } else if (inputRoles.has(role)) {
        const meta = inputCount++ < 64 ? await describeInput(send, node.backendDOMNodeId) : { inputType: "unknown", editable: false, disabled: false };
        item.inputType = meta.inputType;
        item.disabled = item.disabled || meta.disabled;
        item.editable = meta.editable && !item.disabled && !node.ignored && property(node, "readonly") !== true;
        if (item.editable && typeof node.value?.value === "string") item.value = node.value.value;
      }
      for (const key of ["checked","selected","expanded"]) if (property(node,key)!==undefined) item[key]=property(node,key);
      if (role === "link" && typeof property(node, "url") === "string") item.href = property(node, "url");
      if (inputRoles.has(role) || role === "link" || role === "button") item.context = nodeContext(node, all);
      output.push(item);
    }
    if(detailed) { const after=await pageState(send,true); if(after.documentKey!==state?.documentKey)throw new Error("STALE_SNAPSHOT: document reloaded during observation"); state=after; }
    return output;
  }, 10000, detailed);
  if ((await getTabInfo(tabId)).url !== info.url) throw new Error("STALE_SNAPSHOT: tab navigated while reading snapshot");
  pruneSnapshots(tabId);
  snapshots.set(snapshotId, { id: snapshotId, tabId, url: info.url, references, guards, options, newTabLinks, documentKey:state?.documentKey as string|undefined, createdAt: Date.now() });
  return { snapshotId, documentId: snapshotId, metadataVersion: detailed ? 2 : 1, url: info.url, nodes, ...(state || {}) };
}

export async function clickNode(tabId: number, snapshotId: string, nodeRef: string, guarded = false): Promise<void> {
	const backendNodeId = await resolveNode(tabId, snapshotId, nodeRef);
	await withDebugger(tabId, async (send) => {
    if (guarded) {
      // Chrome may attribute a background click's popup to the active tab.
      // Activate the known source before a target=_blank click so openerTabId
      // remains reliable; this click will open an active tab anyway.
      if(snapshots.get(snapshotId)?.newTabLinks?.has(nodeRef))await chrome.tabs.update(tabId,{active:true});
      await validateObserved(send,snapshotId,nodeRef,backendNodeId);
      const {x,y}=await hitPoint(send,backendNodeId);
      await send("Input.dispatchMouseEvent",{type:"mousePressed",x,y,button:"left",clickCount:1});
      await send("Input.dispatchMouseEvent",{type:"mouseReleased",x,y,button:"left",clickCount:1});
    } else await clickBackendNode(send,backendNodeId);
  }, 10000, guarded);
}

async function resolveNode(tabId: number, snapshotId: string, nodeRef: string): Promise<number> {
  const snapshot = snapshots.get(snapshotId);
  const backendNodeId = snapshot?.references.get(nodeRef);
  if (!snapshot || snapshot.tabId !== tabId || Date.now() - snapshot.createdAt >= snapshotTTL || backendNodeId === undefined) {
    throw new Error("STALE_SNAPSHOT: node reference is no longer valid");
  }
  const info = await getTabInfo(tabId);
  if (info.url !== snapshot.url) {
    invalidateTabSnapshots(tabId);
    throw new Error("STALE_SNAPSHOT: tab URL changed");
  }
  return backendNodeId;
}

async function clickBackendNode(send: (method: string, params: Record<string, unknown>) => Promise<unknown>, backendNodeId: number): Promise<void> {
    let model: { model?: { content?: number[] } };
    try {
      await send("DOM.scrollIntoViewIfNeeded", { backendNodeId });
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

export async function typeIntoNode(tabId: number, snapshotId: string, nodeRef: string, text: string, replace = false, guarded = false): Promise<{ verified?: boolean }> {
  const backendNodeId = await resolveNode(tabId, snapshotId, nodeRef);
  return withDebugger(tabId, async (send) => {
    if(guarded){ await validateObserved(send,snapshotId,nodeRef,backendNodeId); await hitPoint(send,backendNodeId); }
    if (!replace) {
      await clickBackendNode(send, backendNodeId);
      await send("Input.insertText", { text });
      return {};
    }
    const meta = await describeInput(send, backendNodeId);
    if (!meta.editable) throw new Error("INVALID_TARGET: replacement requires an enabled text input or textarea");
    const before = await send("Accessibility.getPartialAXTree", { backendNodeId, fetchRelatives: false }) as { nodes?: AXNode[] };
    const target = before.nodes?.find((node) => node.backendDOMNodeId === backendNodeId);
    if (!target || target.ignored || property(target, "disabled") === true || property(target, "readonly") === true) throw new Error("INVALID_TARGET: input is no longer editable");
    await send("DOM.focus", { backendNodeId });
    await send("Input.dispatchKeyEvent", { type: "keyDown", key: "a", code: "KeyA", modifiers: 4, commands: ["selectAll"] });
    await send("Input.dispatchKeyEvent", { type: "keyUp", key: "a", code: "KeyA", modifiers: 4 });
    if (text) await send("Input.insertText", { text });
    else {
      await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 });
      await send("Input.dispatchKeyEvent", { type: "keyUp", key: "Backspace", code: "Backspace", windowsVirtualKeyCode: 8 });
    }
    try {
      const result = await send("Accessibility.getPartialAXTree", { backendNodeId, fetchRelatives: false }) as { nodes?: AXNode[] };
      const after = result.nodes?.find((node) => node.backendDOMNodeId === backendNodeId);
      if (!after || after.ignored) return { verified: false };
      if (typeof after.value?.value === "string") return { verified: after.value.value === text };
      // Chrome omits AX value for empty fields. Read the actual DOM property
      // rather than treating missing accessibility data as an empty value.
      const resolved = await send("DOM.resolveNode", { backendNodeId }) as { object?: { objectId?: string } };
      const objectId = resolved.object?.objectId;
      if (!objectId) return { verified: false };
      try {
        const read = await send("Runtime.callFunctionOn", { objectId, functionDeclaration: "function () { return typeof this.value === 'string' ? this.value : null; }", returnByValue: true, silent: true }) as { result?: { value?: unknown }; exceptionDetails?: unknown };
        return { verified: !read.exceptionDetails && read.result?.value === text };
      } finally { await send("Runtime.releaseObject", { objectId }).catch(() => {}); }
    } catch { return { verified: false }; }
  }, 10000, guarded);
}

export async function pressKey(tabId: number, key: string, snapshotId?: string, nodeRef?: string, guarded = false): Promise<void> {
  await getTabInfo(tabId);
  const codes: Record<string, number> = { Enter: 13, Tab: 9, Escape: 27, Backspace: 8, ArrowUp: 38, ArrowDown: 40, ArrowLeft: 37, ArrowRight: 39 };
  const backendNodeId = snapshotId && nodeRef ? await resolveNode(tabId, snapshotId, nodeRef) : undefined;
  const code = codes[key];
  if (code === undefined) throw new Error("INVALID_PARAMS: unsupported key");
  await withDebugger(tabId, async (send) => {
    if (backendNodeId !== undefined) {
      if(guarded&&snapshotId&&nodeRef){
        await chrome.tabs.update(tabId,{active:true});
        await validateObserved(send,snapshotId,nodeRef,backendNodeId);
        await hitPoint(send,backendNodeId);
      }
      const meta = await describeInput(send, backendNodeId);
      if (!meta.editable) throw new Error("INVALID_TARGET: targeted key requires an editable input");
      await send("DOM.focus", { backendNodeId });
    }
    await send("Input.dispatchKeyEvent", { type: "keyDown", key, code: key, windowsVirtualKeyCode: code, ...(key === "Enter" ? { text: "\r", unmodifiedText: "\r" } : {}) });
    await send("Input.dispatchKeyEvent", { type: "keyUp", key, code: key, windowsVirtualKeyCode: code });
  }, 10000, guarded);
}

export async function scrollPage(tabId: number, deltaY: number, snapshotId?: string): Promise<void> {
  if (!Number.isFinite(deltaY)) throw new Error("INVALID_PARAMS: deltaY must be finite");
  await getTabInfo(tabId);
  if (snapshotId) {
    const snap=snapshots.get(snapshotId);
    if(!snap?.documentKey||snap.tabId!==tabId||Date.now()-snap.createdAt>=snapshotTTL)throw new Error("STALE_SNAPSHOT: scroll observation expired");
    await withDebugger(tabId,async send=>{
      if((await pageState(send)).documentKey!==snap.documentKey)throw new Error("STALE_SNAPSHOT: document changed before scroll");
      await send("Runtime.evaluate",{expression:`window.scrollBy(0, ${deltaY})`});
    }, 10000, true);
  } else await sendCDPCommand(tabId, "Runtime.evaluate", { expression: `window.scrollBy(0, ${deltaY})` });
}

export async function waitForText(tabId: number, text: string, timeoutMs: number): Promise<{ found: boolean }> {
  if (!text || timeoutMs <= 0 || timeoutMs > 10000) throw new Error("INVALID_PARAMS: text and timeoutMs are required");
  await getTabInfo(tabId);
  const deadline = Date.now() + timeoutMs;
  const expression = `Boolean(document.body && document.body.innerText.includes(${JSON.stringify(text)}))`;
  for (;;) {
    const response = await sendCDPCommand(tabId, "Runtime.evaluate", { expression, returnByValue: true }) as { result?: { value?: unknown } };
    if (response.result?.value === true) return { found: true };
    if (Date.now() >= deadline) throw new Error("TIMEOUT: text did not appear");
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

async function validateObserved(send: (method:string,params:Record<string,unknown>)=>Promise<unknown>, snapshotId:string,nodeRef:string,backendNodeId:number) {
 const snap=snapshots.get(snapshotId), expected=snap?.guards?.get(nodeRef);
 if(!snap?.documentKey||!expected)throw new Error("STALE_SNAPSHOT: guarded action requires an detailed snapshot");
 await assertGuard(send,backendNodeId,snap.documentKey,expected);
}
export async function selectNode(tabId:number,snapshotId:string,nodeRef:string,optionIndex:number) {
 const id=await resolveNode(tabId,snapshotId,nodeRef);
 const option=snapshots.get(snapshotId)?.options?.get(nodeRef)?.find(o=>o.index===optionIndex&&!o.disabled);
 if(!option)throw new Error("STALE_SNAPSHOT: no matching observed dropdown option");
 return withDebugger(tabId,async send=>{await validateObserved(send,snapshotId,nodeRef,id);return selectObserved(send,id,option);}, 10000, true);
}
