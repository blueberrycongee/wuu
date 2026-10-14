// Synthetic transport observations; the core RPC tests cover the real pin store.
const { ipcRenderer } = require('electron');
module.exports = api => ({
  ...api,
  getThreadExecutionEnvironment: threadID => ipcRenderer.invoke('test:execution-environment', threadID),
});
