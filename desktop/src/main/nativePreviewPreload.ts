import { ipcRenderer } from "electron";
import { startNativePreview } from "../nativePreview";

// The decoder ships with this sandbox preload, independent of the renderer
// dev server. Its private port exposes no desktop bridge or input capability.
// Main transfers the port only after the local canvas document has loaded.
ipcRenderer.once("wuu-native-preview-port", (event) => {
  startNativePreview(event.ports[0]);
});
