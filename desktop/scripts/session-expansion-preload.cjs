const { contextBridge, ipcRenderer } = require("electron");
contextBridge.exposeInMainWorld("inspectorNative", params => ipcRenderer.invoke("test:session-expansion", params));
