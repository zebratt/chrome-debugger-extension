document.getElementById("extension-id").textContent = chrome.runtime.id;

async function render() {
  const state = await chrome.storage.local.get(["profileId", "connectionStatus", "connectionDetail"]);
  document.getElementById("profile-id").textContent = state.profileId || "pending";
  document.getElementById("status").textContent = state.connectionStatus || "pending";
  document.getElementById("detail").textContent = state.connectionDetail || "";
}

document.getElementById("retry").addEventListener("click", async () => {
  await chrome.runtime.sendMessage({ type: "retry" });
  await render();
});

chrome.storage.onChanged.addListener(render);
void render();
