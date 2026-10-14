import { contextBridge, ipcRenderer } from "electron";
import type { SystemPermissionGuideAPI } from "../shared/systemPermissionGuide";

const api: SystemPermissionGuideAPI = {
  config: () => ipcRenderer.invoke("wuu:permission-guide-config"),
  drag: () => ipcRenderer.send("wuu:permission-guide-action", "drag"),
  reveal: () => ipcRenderer.send("wuu:permission-guide-action", "reveal"),
  resize: height => ipcRenderer.send("wuu:permission-guide-action", "resize", height),
  close: () => ipcRenderer.send("wuu:permission-guide-action", "close"),
  onError: handler => {
    const listener = () => handler();
    ipcRenderer.on("wuu:permission-guide-error", listener);
    return () => ipcRenderer.removeListener("wuu:permission-guide-error", listener);
  },
};
contextBridge.exposeInMainWorld("wuuPermissionGuide", api);
