import { errorResponse, parseRequest } from "./protocol.ts";
import { getTabInfo, listTabs, navigateTab, openTab } from "./tabs.ts";
import { clickNode, pressKey, screenshotPage, scrollPage, snapshotPage, typeIntoNode, waitForText } from "./page.ts";
import { sendCDPCommand } from "./debugger.ts";

export async function handleMessage(value: unknown) {
  const request = parseRequest(value);
  if (!request) return errorResponse(null, "INVALID_REQUEST", "Invalid JSON-RPC request", false);
  const params = request.params || {};
  try {
    if (params.expectedUrl !== undefined) {
      if (typeof params.tabId !== "number" || typeof params.expectedUrl !== "string") {
        throw new Error("INVALID_PARAMS: expectedUrl requires tabId");
      }
      const current = await getTabInfo(params.tabId);
      if (current.url !== params.expectedUrl) throw new Error("POLICY_DENIED: tab URL changed after broker check");
    }
    let result: unknown;
    switch (request.method) {
      case "tab.list":
        if (typeof params.profileId !== "string") throw new Error("INVALID_PARAMS: profileId is required");
        result = await listTabs();
        break;
      case "tab.open":
        if (typeof params.url !== "string") throw new Error("INVALID_PARAMS: url is required");
        result = await openTab(params.url);
        break;
      case "tab.navigate":
        if (typeof params.tabId !== "number" || typeof params.url !== "string") throw new Error("INVALID_PARAMS: tabId and url are required");
        result = await navigateTab(params.tabId, params.url);
        break;
      case "tab.info":
        if (typeof params.tabId !== "number") throw new Error("INVALID_PARAMS: tabId is required");
        result = await getTabInfo(params.tabId);
        break;
      case "tab.screenshot":
        if (typeof params.tabId !== "number") throw new Error("INVALID_PARAMS: tabId is required");
        result = await screenshotPage(params.tabId);
        break;
      case "tab.snapshot":
        if (typeof params.tabId !== "number") throw new Error("INVALID_PARAMS: tabId is required");
        result = await snapshotPage(params.tabId);
        break;
      case "tab.click":
        if (typeof params.tabId !== "number" || typeof params.snapshotId !== "string" || typeof params.nodeRef !== "string") {
          throw new Error("INVALID_PARAMS: tabId, snapshotId and nodeRef are required");
        }
        await clickNode(params.tabId, params.snapshotId, params.nodeRef);
        result = { clicked: true };
        break;
      case "tab.type":
        if (typeof params.tabId !== "number" || typeof params.snapshotId !== "string" || typeof params.nodeRef !== "string" || typeof params.text !== "string") {
          throw new Error("INVALID_PARAMS: tabId, snapshotId, nodeRef and text are required");
        }
        await typeIntoNode(params.tabId, params.snapshotId, params.nodeRef, params.text);
        result = { typed: true };
        break;
      case "tab.key":
        if (typeof params.tabId !== "number" || typeof params.key !== "string") throw new Error("INVALID_PARAMS: tabId and key are required");
        await pressKey(params.tabId, params.key);
        result = { pressed: true };
        break;
      case "tab.scroll":
        if (typeof params.tabId !== "number" || typeof params.deltaY !== "number" || !Number.isFinite(params.deltaY)) {
          throw new Error("INVALID_PARAMS: tabId and deltaY are required");
        }
        await scrollPage(params.tabId, params.deltaY);
        result = { scrolled: true };
        break;
      case "tab.wait":
        if (typeof params.tabId !== "number" || typeof params.text !== "string") throw new Error("INVALID_PARAMS: tabId and text are required");
        result = await waitForText(params.tabId, params.text, typeof params.timeoutMs === "number" ? params.timeoutMs : 5000);
        break;
      case "cdp.send":
        if (typeof params.tabId !== "number" || typeof params.method !== "string") throw new Error("INVALID_PARAMS: tabId and method are required");
        await getTabInfo(params.tabId);
        result = await sendCDPCommand(params.tabId, params.method, typeof params.commandParams === "object" && params.commandParams !== null ? params.commandParams as Record<string, unknown> : {});
        break;
      case "extension.reload":
        setTimeout(() => chrome.runtime.reload(), 0);
        result = { reloading: true };
        break;
      default:
        return errorResponse(request.id, "METHOD_NOT_FOUND", "Method is not available", false);
    }
    return { jsonrpc: "2.0" as const, id: request.id, result };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    const kind = /^([A-Z_]+):/.exec(message)?.[1] || "CHROME_ERROR";
    return errorResponse(request.id, kind, message, kind === "CHROME_ERROR");
  }
}
