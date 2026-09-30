import assert from "node:assert/strict";
import test from "node:test";
import { sendCDPCommand, withDebugger } from "./debugger.ts";

test("CDP command attaches to the target and detaches after completion", async () => {
  const original = globalThis.chrome;
  const calls: string[] = [];
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async (target: { tabId: number }, version: string) => { calls.push(`attach:${target.tabId}:${version}`); },
      sendCommand: async (target: { tabId: number }, method: string) => { calls.push(`send:${target.tabId}:${method}`); return { data: "image" }; },
      detach: async (target: { tabId: number }) => { calls.push(`detach:${target.tabId}`); },
    },
  };
  try {
    assert.deepEqual(await sendCDPCommand(7, "Page.captureScreenshot", {}), { data: "image" });
    assert.deepEqual(calls, ["attach:7:1.3", "send:7:Page.captureScreenshot", "detach:7"]);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("unsupported CDP domain is rejected before debugger attachment", async () => {
  const original = globalThis.chrome;
  let attached = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: { attach: async () => { attached = true; } },
  };
  try {
    await assert.rejects(sendCDPCommand(7, "Browser.getVersion", {}), /UNSUPPORTED_CDP_DOMAIN/);
    assert.equal(attached, false);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("one debugger attachment can send a sequence of input commands", async () => {
  const original = globalThis.chrome;
  const calls: string[] = [];
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async () => { calls.push("attach"); },
      sendCommand: async (_target: unknown, method: string) => { calls.push(method); return {}; },
      detach: async () => { calls.push("detach"); },
    },
  };
  try {
    await withDebugger(7, async (send) => {
      await send("Input.dispatchMouseEvent", { type: "mousePressed" });
      await send("Input.dispatchMouseEvent", { type: "mouseReleased" });
    });
    assert.deepEqual(calls, ["attach", "Input.dispatchMouseEvent", "Input.dispatchMouseEvent", "detach"]);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("stuck CDP command releases its debugger attachment", async () => {
  const original = globalThis.chrome;
  let detached = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async () => {},
      sendCommand: async () => new Promise(() => {}),
      detach: async () => { detached = true; },
    },
  };
  try {
    const outcome = await Promise.race([
      withDebugger(7, async (send) => send("Input.dispatchMouseEvent", { type: "mouseWheel" }), 20)
        .then(() => "resolved", (error) => String(error).includes("OUTCOME_UNKNOWN") ? "rejected" : "wrong-error"),
      new Promise((resolve) => setTimeout(() => resolve("hung"), 100)),
    ]);
    assert.equal(outcome, "rejected");
    assert.equal(detached, true);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("existing debugger is reported as busy without detaching it", async () => {
  const original = globalThis.chrome;
  let detached = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async () => { throw new Error("Another debugger is already attached to the tab"); },
      detach: async () => { detached = true; },
    },
  };
  try {
    await assert.rejects(sendCDPCommand(7, "Page.captureScreenshot", {}), /TAB_BUSY/);
    assert.equal(detached, false);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("detached target has a stable error kind", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async () => {},
      sendCommand: async () => { throw new Error("Detached while handling command"); },
      detach: async () => {},
    },
  };
  try {
    await assert.rejects(sendCDPCommand(7, "Page.captureScreenshot", {}), /TARGET_DETACHED/);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("two readers of one tab are serialized instead of fighting for debugger attachment", async () => {
  const original = globalThis.chrome;
  let attached = false;
  let attachments = 0;
  let releaseFirst!: () => void;
  let markStarted!: () => void;
  const firstStarted = new Promise<void>((resolve) => { markStarted = resolve; });
  const firstGate = new Promise<void>((resolve) => { releaseFirst = resolve; });
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: {
      attach: async () => {
        if (attached) throw new Error("Another debugger is already attached to the tab");
        attached = true;
        attachments++;
      },
      sendCommand: async () => {
        if (attachments === 1) { markStarted(); await firstGate; }
        return { data: "ok" };
      },
      detach: async () => { attached = false; },
    },
  };
  try {
    const first = sendCDPCommand(7, "Page.captureScreenshot", {});
    await firstStarted;
    const second = sendCDPCommand(7, "Page.captureScreenshot", {});
    await new Promise((resolve) => setTimeout(resolve, 5));
    releaseFirst();
    const results = await Promise.allSettled([first, second]);
    assert.deepEqual(results.map((result) => result.status), ["fulfilled", "fulfilled"]);
    assert.equal(attachments, 2);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("agent focus emulation is restored on success, failure and timeout", async () => {
  const original = globalThis.chrome;
  try {
    for (const outcome of ["success", "failure", "timeout"]) {
      const calls: string[] = [];
      (globalThis as unknown as { chrome: unknown }).chrome = {
        debugger: {
          attach: async () => { calls.push("attach"); },
          sendCommand: async (_target: unknown, method: string, params: Record<string, unknown>) => {
            calls.push(method === "Emulation.setFocusEmulationEnabled" ? `focus:${params.enabled}` : method);
            return {};
          },
          detach: async () => { calls.push("detach"); },
        },
      };
      const task = withDebugger(7, async (send) => {
        await send("DOM.focus", { backendNodeId: 1 });
        if (outcome === "failure") throw new Error("action failed");
        if (outcome === "timeout") await new Promise(() => {});
      }, 30, true);
      if (outcome === "success") await task;
      else await assert.rejects(task, outcome === "failure" ? /action failed/ : /OUTCOME_UNKNOWN/);
      assert.deepEqual(calls, ["attach", "focus:true", "DOM.focus", "focus:false", "detach"]);
    }
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("a timed-out action cannot send another command after its attachment ends", async () => {
  const original = globalThis.chrome;
  let lateSend: ((method: string, params: Record<string, unknown>) => Promise<unknown>) | undefined;
  let sent = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    debugger: { attach: async () => {}, detach: async () => {}, sendCommand: async () => { sent = true; } },
  };
  try {
    await assert.rejects(withDebugger(7, async (send) => {
      lateSend = send;
      await new Promise(() => {});
    }, 20), /OUTCOME_UNKNOWN/);
    await assert.rejects(lateSend!("Input.insertText", { text: "must not run" }), /session has ended/);
    assert.equal(sent, false);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
