const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('__scopedFixture', { call: (method,params) => ipcRenderer.invoke('fixture:scope',method,params) });
