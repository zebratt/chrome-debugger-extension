import { connectNativeBridge } from "./native.ts";
import { ensureProfileId } from "./profile.ts";

const reconnectAlarm = "native-reconnect";
let port: chrome.runtime.Port | undefined;
let connecting = false;

async function connect() {
  if (port || connecting) return;
  connecting = true;
  try {
    const profileId = await ensureProfileId();
    const nextPort = connectNativeBridge(profileId, chrome.runtime.getManifest().version, () => {
      if (port !== nextPort) return;
      port = undefined;
      const detail = chrome.runtime.lastError?.message || "Native host disconnected";
      void chrome.storage.local.set({ connectionStatus: "disconnected", connectionDetail: detail });
      chrome.alarms.create(reconnectAlarm, { delayInMinutes: 0.5 });
    });
    port = nextPort;
    await chrome.storage.local.set({ connectionStatus: "connected", connectionDetail: "Native host port open", profileId });
  } catch (error) {
    await chrome.storage.local.set({ connectionStatus: "disconnected", connectionDetail: String(error) });
    chrome.alarms.create(reconnectAlarm, { delayInMinutes: 0.5 });
  } finally {
    connecting = false;
  }
}

chrome.runtime.onInstalled.addListener(() => { void connect(); });
chrome.runtime.onStartup.addListener(() => { void connect(); });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === reconnectAlarm) void connect();
});
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (message?.type !== "retry") return;
  port?.disconnect();
  port = undefined;
  void connect().then(() => reply({ started: true }));
  return true;
});

void connect();
