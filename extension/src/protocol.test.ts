import assert from "node:assert/strict";
import test from "node:test";
import { makeHello, parseRequest, errorResponse } from "./protocol.ts";

test("hello names the profile and protocol version", () => {
  const hello = makeHello("profile-1", "0.1.0") as Record<string, unknown>;
  assert.equal(hello.kind, "hello");
  assert.equal(hello.profileId, "profile-1");
  assert.equal(hello.protocolVersion, "1.0");
  assert.equal(hello.extensionVersion, "0.1.0");
  assert.ok((hello.supportedCdpDomains as string[]).includes("Network"));
});

test("request parser requires a JSON-RPC method and id", () => {
  assert.equal(parseRequest({ jsonrpc: "2.0", id: "r1", method: "tab.list", params: { profileId: "p1" } })?.method, "tab.list");
  assert.equal(parseRequest({ jsonrpc: "2.0", method: "tab.list" }), null);
  assert.equal(parseRequest({ id: "r1", method: "tab.list" }), null);
});

test("error response includes a stable error kind", () => {
  assert.deepEqual(errorResponse("r1", "BROWSER_OFFLINE", "Chrome is unavailable", true), {
    jsonrpc: "2.0",
    id: "r1",
    error: {
      code: -32000,
      message: "Chrome is unavailable",
      data: { kind: "BROWSER_OFFLINE", retryable: true },
    },
  });
});
