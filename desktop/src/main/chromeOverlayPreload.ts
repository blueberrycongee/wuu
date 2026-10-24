import { contextBridge, ipcRenderer } from "electron";
import type { ChromeOverlayRendererApi, ChromeOverlayRequest } from "../shared/chromeOverlay";

// This preload is never used by website tabs. No general IPC or Wuu API escapes.
const bridge: ChromeOverlayRendererApi = {
  ready: () => ipcRenderer.invoke("wuu:chrome-overlay-ready"),
  onState: listener => {
    const receive = (_event: Electron.IpcRendererEvent, state: ChromeOverlayRequest | null) => listener(state);
    ipcRenderer.on("wuu:chrome-overlay-state", receive);
    return () => ipcRenderer.removeListener("wuu:chrome-overlay-state", receive);
  },
  painted: (token, generation) => ipcRenderer.send("wuu:chrome-overlay-painted", token, generation),
  command: event => ipcRenderer.send("wuu:chrome-overlay-command", event),
};
contextBridge.exposeInMainWorld("wuuChromeOverlay", bridge);
