import assert from "node:assert/strict";
import test from "node:test";
import { listTabs, openTab, navigateTab, getTabInfo } from "./tabs.ts";

test("tab list omits restricted and incognito pages", async () => {
  const original = globalThis.chrome;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: {
      query: async () => [
        { id: 1, title: "Example", url: "https://example.com", incognito: false },
        { id: 2, title: "Extensions", url: "chrome://extensions", incognito: false },
        { id: 3, title: "Private", url: "https://private.example.com", incognito: true },
      ],
    },
  };
  try {
    assert.deepEqual(await listTabs(), { tabs: [{ tabId: 1, title: "Example", url: "https://example.com" }] });
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("tab open rejects internal URLs before calling Chrome", async () => {
  const original = globalThis.chrome;
  let created = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { create: async () => { created = true; return { id: 4 }; } },
  };
  try {
    await assert.rejects(openTab("chrome://extensions"), /POLICY_DENIED/);
    assert.equal(created, false);
    assert.deepEqual(await openTab("https://example.com"), { tabId: 4 });
    assert.equal(created, true);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("navigation uses the requested tab id", async () => {
  const original = globalThis.chrome;
  let updated: unknown;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: {
      get: async (id: number) => ({ id, title: "Example", url: "https://example.com", incognito: false }),
      update: async (id: number, change: unknown) => { updated = [id, change]; return { id }; },
    },
  };
  try {
    assert.deepEqual(await navigateTab(7, "https://example.com/path"), { tabId: 7 });
    assert.deepEqual(updated, [7, { url: "https://example.com/path" }]);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("navigation refuses an incognito target tab", async () => {
  const original = globalThis.chrome;
  let updated = false;
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: {
      get: async (id: number) => ({ id, url: "https://example.com", incognito: true }),
      update: async () => { updated = true; },
    },
  };
  try {
    await assert.rejects(navigateTab(7, "https://example.com/path"), /POLICY_DENIED/);
    assert.equal(updated, false);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});

test("tab info refuses a tab after it navigates to an internal page", async () => {
  const original = globalThis.chrome;
  let url = "https://example.com";
  (globalThis as unknown as { chrome: unknown }).chrome = {
    tabs: { get: async (id: number) => ({ id, title: "Page", url, incognito: false }) },
  };
  try {
    assert.deepEqual(await getTabInfo(7), { tabId: 7, title: "Page", url: "https://example.com" });
    url = "chrome://settings";
    await assert.rejects(getTabInfo(7), /POLICY_DENIED/);
  } finally {
    (globalThis as unknown as { chrome: unknown }).chrome = original;
  }
});
