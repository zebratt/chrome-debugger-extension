export async function ensureProfileId(): Promise<string> {
  const stored = await chrome.storage.local.get("profileId");
  if (typeof stored.profileId === "string" && stored.profileId.length > 0) return stored.profileId;
  const profileId = crypto.randomUUID();
  await chrome.storage.local.set({ profileId });
  return profileId;
}
