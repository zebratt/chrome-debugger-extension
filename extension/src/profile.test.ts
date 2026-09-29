import assert from "node:assert/strict";
import test from "node:test";
import { ensureProfileId } from "./profile.ts";

test("profile ID persists across service worker starts", async () => {
  const original = globalThis.chrome;
  const store: Record<string, unknown> = {};
  (globalThis as unknown as { chrome: unknown }).chrome = {
    storage: { local: {
      get: async () => ({ profileId: store.profileId }),
      set: async (value: Record<string, unknown>) => Object.assign(store, value),
    } },
  };
  try {
    const first = await ensureProfileId();
    const second = await ensureProfileId();
    assert.match(first, /^[0-9a-f-]{36}$/);
    assert.equal(first, second);
    assert.equal(store.profileId, first);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
