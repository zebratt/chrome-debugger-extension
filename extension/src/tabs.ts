export function isWebURL(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (parsed.protocol === "http:" || parsed.protocol === "https:") && Boolean(parsed.hostname);
  } catch {
    return false;
  }
}

export async function listTabs() {
  const tabs = await chrome.tabs.query({});
  return {
    tabs: tabs
      .filter((tab) => tab.id !== undefined && tab.url !== undefined && !tab.incognito && isWebURL(tab.url))
      .map((tab) => ({ tabId: tab.id as number, title: tab.title || "", url: tab.url as string })),
  };
}

export async function openTab(url: string) {
  if (!isWebURL(url)) throw new Error("POLICY_DENIED: URL must use http or https");
  const tab = await chrome.tabs.create({ url });
  if (tab.id === undefined) throw new Error("TAB_GONE: Chrome did not return a tab id");
  return { tabId: tab.id };
}

export async function navigateTab(tabId: number, url: string) {
  if (!isWebURL(url)) throw new Error("POLICY_DENIED: URL must use http or https");
  await getTabInfo(tabId);
  await chrome.tabs.update(tabId, { url });
  return { tabId };
}

export async function getTabInfo(tabId: number) {
  const tab = await chrome.tabs.get(tabId);
  if (tab.incognito || !tab.url || !isWebURL(tab.url)) {
    throw new Error("POLICY_DENIED: tab is not a regular web page");
  }
  return { tabId, title: tab.title || "", url: tab.url };
}
