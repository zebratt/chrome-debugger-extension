import { handleMessage } from "./dispatch.ts";
import { makeHello } from "./protocol.ts";

export function connectNativeBridge(profileId: string, extensionVersion: string, onDisconnect: () => void): chrome.runtime.Port {
  const port = chrome.runtime.connectNative("com.chromeconnector.bridge");
  port.onMessage.addListener(async (message: unknown) => {
    const response = await handleMessage(message);
    port.postMessage(response);
  });
  port.onDisconnect.addListener(onDisconnect);
  port.postMessage(makeHello(profileId, extensionVersion));
  return port;
}
