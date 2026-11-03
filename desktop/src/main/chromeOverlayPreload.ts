import { contextBridge, ipcRenderer } from "electron";
import type { ChromeOverlayRendererApi, ChromeOverlayRendererRequest } from "../shared/chromeOverlay";

// This preload is never used by website tabs. No general IPC or Wuu API escapes.
const bridge: ChromeOverlayRendererApi = {
  ready: () => ipcRenderer.invoke("wuu:chrome-overlay-ready"),
  onState: listener => {
    const receive = (_event: Electron.IpcRendererEvent, state: ChromeOverlayRendererRequest | null) => listener(state);
    ipcRenderer.on("wuu:chrome-overlay-state", receive);
    return () => ipcRenderer.removeListener("wuu:chrome-overlay-state", receive);
  },
  prepared: (token, generation) => ipcRenderer.send("wuu:chrome-overlay-prepared", token, generation),
  painted: (token, generation, viewport) => ipcRenderer.send("wuu:chrome-overlay-painted", token, generation, viewport),
  command: event => ipcRenderer.send("wuu:chrome-overlay-command", event),
};
contextBridge.exposeInMainWorld("wuuChromeOverlay", bridge);
