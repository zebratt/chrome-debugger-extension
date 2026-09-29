export const SUPPORTED_CDP_DOMAINS = [
  "Accessibility", "Audits", "CacheStorage", "Console", "CSS", "Database", "Debugger",
  "DOM", "DOMDebugger", "DOMSnapshot", "Emulation", "Fetch", "IO", "Input",
  "Inspector", "Log", "Network", "Overlay", "Page", "Performance", "Profiler",
  "Runtime", "Storage", "Target", "Tracing", "WebAudio", "WebAuthn",
] as const;

export async function withDebugger<T>(tabId: number, action: (send: (method: string, params: Record<string, unknown>) => Promise<unknown>) => Promise<T>, timeoutMs = 10000): Promise<T> {
  const target = { tabId };
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("OUTCOME_UNKNOWN: Chrome debugger command timed out")), timeoutMs);
  });
  let shouldDetach = false;
  try {
    try {
      await Promise.race([chrome.debugger.attach(target, "1.3"), timeout]);
      shouldDetach = true;
    } catch (error) {
      const message = String(error);
      if (message.includes("OUTCOME_UNKNOWN")) shouldDetach = true;
      if (message.includes("Another debugger is already attached")) throw new Error("TAB_BUSY: another debugger controls this tab");
      throw error;
    }
    try {
      return await Promise.race([action(async (method, params) => {
        assertSupported(method);
        return chrome.debugger.sendCommand(target, method, params);
      }), timeout]);
    } catch (error) {
      if (/detached/i.test(String(error))) throw new Error("TARGET_DETACHED: Chrome debugger target was detached");
      throw error;
    }
  } finally {
    if (timer !== undefined) clearTimeout(timer);
    if (shouldDetach) {
      let detachTimer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          chrome.debugger.detach(target),
          new Promise((_, reject) => { detachTimer = setTimeout(() => reject(new Error("debugger detach timed out")), 1000); }),
        ]);
      } catch {
        // The tab may already have detached or closed.
      } finally {
        if (detachTimer !== undefined) clearTimeout(detachTimer);
      }
    }
  }
}

export async function sendCDPCommand(tabId: number, method: string, params: Record<string, unknown>): Promise<unknown> {
  assertSupported(method);
  return withDebugger(tabId, (send) => send(method, params));
}

function assertSupported(method: string) {
  const [domain, command] = method.split(".", 2);
  if (!domain || !command || !SUPPORTED_CDP_DOMAINS.includes(domain as typeof SUPPORTED_CDP_DOMAINS[number])) {
    throw new Error("UNSUPPORTED_CDP_DOMAIN: method is unavailable through chrome.debugger");
  }
}
