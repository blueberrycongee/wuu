// Explicit composition for synthetic backends exercising the shipped default UI.
// Keep this function self-contained so sandboxed/browser fixtures can inline it.
module.exports = function withDefaultShell(api) {
  const shell = {
    id: "builtin:wuu.default-shell", kind: "plugin", name: "Default shell fixture",
    provenance: { kind: "plugin", source: "builtin", scope: "user", plugin_id: "wuu.default-shell" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    fingerprint: "default-shell-e2e", desktop: { entry: "desktop.js", services: [{ name: "ui.shell", version: "1.0.0" }] },
  };
  return {
    ...api,
    initialize: async (...args) => {
      const initialized = await api.initialize(...args);
      return { ...initialized, application_shell: { plugin_id: shell.id, generation: shell.fingerprint, provider_id: "ui.shell" },
        extension_inventory: [shell, ...(initialized.extension_inventory ?? [])] };
    },
    getPluginConflictPreferences: api.getPluginConflictPreferences ?? (async () => ({})),
    loadPluginDesktopModule: async (request) => {
      if (request.id === shell.id && request.fingerprint === shell.fingerprint) {
        return { ...request, digest: "0".repeat(64), url: "wuu-builtin:default-shell" };
      }
      if (!api.loadPluginDesktopModule) throw new Error(`Unknown fixture module: ${request.id}`);
      return api.loadPluginDesktopModule(request);
    },
  };
};
