import { ipcRenderer } from "electron";

// No desktop bridge, input forwarding or arbitrary IPC is exposed here.
ipcRenderer.once("wuu-linux-preview-port", (event) => {
  window.postMessage("wuu-linux-preview-port", "*", event.ports);
});
