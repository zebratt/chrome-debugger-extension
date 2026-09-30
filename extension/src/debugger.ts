export const SUPPORTED_CDP_DOMAINS = [
  "Accessibility", "Audits", "CacheStorage", "Console", "CSS", "Database", "Debugger",
  "DOM", "DOMDebugger", "DOMSnapshot", "Emulation", "Fetch", "IO", "Input",
  "Inspector", "Log", "Network", "Overlay", "Page", "Performance", "Profiler",
  "Runtime", "Storage", "Target", "Tracing", "WebAudio", "WebAuthn",
] as const;

const tabQueues = new Map<number, Promise<void>>();

async function acquireTab(tabId: number): Promise<() => void> {
  const previous = tabQueues.get(tabId) || Promise.resolve();
  let unlock!: () => void;
  const current = new Promise<void>((resolve) => { unlock = resolve; });
  const tail = previous.then(() => current);
  tabQueues.set(tabId, tail);
  await previous;
  return () => {
    unlock();
    if (tabQueues.get(tabId) === tail) tabQueues.delete(tabId);
  };
}

export async function withDebugger<T>(tabId: number, action: (send: (method: string, params: Record<string, unknown>) => Promise<unknown>) => Promise<T>, timeoutMs = 10000, emulateFocus = false): Promise<T> {
  const releaseTab = await acquireTab(tabId);
  const target = { tabId };
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("OUTCOME_UNKNOWN: Chrome debugger command timed out")), timeoutMs);
  });
  let shouldDetach = false;
  let focusStarted = false;
  let closed = false;
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
      const send = async (method: string, params: Record<string, unknown>) => {
        if (closed) throw new Error("OUTCOME_UNKNOWN: debugger session has ended");
        assertSupported(method);
        return chrome.debugger.sendCommand(target, method, params);
      };
      return await Promise.race([(async () => {
        if (emulateFocus) {
          focusStarted = true;
          await send("Emulation.setFocusEmulationEnabled", { enabled: true });
        }
        return action(send);
      })(), timeout]);
    } catch (error) {
      if (/detached/i.test(String(error))) throw new Error("TARGET_DETACHED: Chrome debugger target was detached");
      throw error;
    }
  } finally {
    closed = true;
    if (timer !== undefined) clearTimeout(timer);
    if (focusStarted) {
      // Keep background pages responsive only while this attachment is owned.
      // Restore emulation even after action failure, then detach as usual.
      await boundedCleanup(chrome.debugger.sendCommand(target, "Emulation.setFocusEmulationEnabled", { enabled: false }));
    }
    if (shouldDetach) {
      await boundedCleanup(chrome.debugger.detach(target));
    }
    releaseTab();
  }
}

async function boundedCleanup(command: Promise<unknown>): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      command,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("debugger cleanup timed out")), 1000); }),
    ]);
  } catch {
    // The tab may already have detached or closed.
  } finally {
    if (timer !== undefined) clearTimeout(timer);
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
