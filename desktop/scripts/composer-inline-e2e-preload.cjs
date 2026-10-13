// Extension for resize-e2e-preload.cjs. Backend inventory and module bytes are
// synthetic; DesktopPluginRuntime, PluginHost and the composer are production.
const { ipcRenderer } = require("electron");

module.exports = (api) => {
  let enabled = false;
  let generation = 1;
  const listeners = new Set();
  const cwd = process.env.WUU_RESIZE_E2E_CWD || process.cwd();
  const inventory = () => enabled ? [{
    id: "user:inline-completion-e2e",
    name: "Inline completion acceptance fixture",
    kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "inline-completion-e2e" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    fingerprint: `inline-completion-e2e-${generation}`,
    desktop: { entry: "desktop.js" },
  }] : [];
  ipcRenderer.on("test:inline-inventory", (_event, change) => {
    enabled = change.enabled;
    generation = change.generation ?? generation;
    for (const listener of listeners) listener({
      kind: "notification", workdir: cwd,
      message: { method: "plugin/inventory/changed", params: { epoch: generation, extension_inventory: inventory(), skills: [] } },
    });
  });
  return {
    ...api,
    initialize: async () => ({ ...await api.initialize(), extension_inventory: inventory() }),
    startTurn: async (...args) => {
      const response = await api.startTurn(...args);
      ipcRenderer.send("test:inline-submitted", { threadId: args[0], text: args[1], response });
      return response;
    },
    getPluginConflictPreferences: async () => ({}),
    requestPluginRuntime: (params) => ipcRenderer.invoke("test:inline-runtime", params),
    cancelPluginRuntime: (params) => ipcRenderer.invoke("test:inline-runtime-cancel", params),
    loadPluginDesktopModule: async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "0".repeat(64), url: `wuu-plugin://inline-completion/${fingerprint}/desktop.js`,
    }),
    onServerEvent: (listener) => {
      listeners.add(listener);
      const dispose = api.onServerEvent?.(listener);
      return () => { listeners.delete(listener); dispose?.(); };
    },
  };
};
