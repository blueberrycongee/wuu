import { ipcRenderer } from "electron";

// This sandbox preload transfers only the private preview port. It exposes no
// desktop bridge, filesystem access, navigation, or input capabilities.
ipcRenderer.once("wuu-native-preview-port", (event) => {
  window.postMessage("wuu-native-preview-port", "*", event.ports);
});
