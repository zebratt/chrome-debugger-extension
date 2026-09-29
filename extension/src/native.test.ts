import assert from "node:assert/strict";
import test from "node:test";
import { connectNativeBridge } from "./native.ts";

test("native bridge sends hello and replies to broker requests", async () => {
  const original = globalThis.chrome;
  const sent: unknown[] = [];
  let onMessage: ((message: unknown) => Promise<void>) | undefined;
  let hostName = "";
  (globalThis as unknown as { chrome: unknown }).chrome = {
    runtime: {
      connectNative: (name: string) => {
        hostName = name;
        return {
          postMessage: (message: unknown) => sent.push(message),
          onMessage: { addListener: (handler: typeof onMessage) => { onMessage = handler; } },
          onDisconnect: { addListener: () => {} },
        };
      },
    },
    tabs: { query: async () => [{ id: 7, title: "Example", url: "https://example.com" }] },
  };
  try {
    connectNativeBridge("profile-a", "0.1.0", () => {});
    assert.equal(hostName, "com.chromeconnector.bridge");
    const hello = sent[0] as Record<string, unknown>;
    assert.equal(hello.kind, "hello");
    assert.equal(hello.profileId, "profile-a");
    assert.equal(hello.protocolVersion, "1.0");
    assert.equal(hello.extensionVersion, "0.1.0");
    assert.ok((hello.supportedCdpDomains as string[]).includes("Network"));
    assert.ok(onMessage);
    await onMessage({ jsonrpc: "2.0", id: "b-1", method: "tab.list", params: { profileId: "profile-a" } });
    assert.deepEqual(sent[1], { jsonrpc: "2.0", id: "b-1", result: { tabs: [{ tabId: 7, title: "Example", url: "https://example.com" }] } });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
