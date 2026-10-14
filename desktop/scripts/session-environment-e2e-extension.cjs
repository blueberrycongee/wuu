// Synthetic transport observations; the core RPC tests cover the real pin store.
const { ipcRenderer } = require('electron');
module.exports = api => ({
  ...api,
  listWorkspaceDirectory: (path, root, target) => ipcRenderer.invoke("test:workspace-directory", { path, root, target }),
  readWorkspaceFile: (path, root, target) => ipcRenderer.invoke("test:workspace-file", { path, root, target }),
  getThreadExecutionEnvironment: threadID => ipcRenderer.invoke('test:execution-environment', threadID),
});
