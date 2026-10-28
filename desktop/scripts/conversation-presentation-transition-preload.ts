import { contextBridge, ipcRenderer } from 'electron';
contextBridge.exposeInMainWorld('wuu', { capturePresentationFrame: () => ipcRenderer.invoke('fixture:presentation-frame') });
