// Explicit composition for synthetic backends exercising the shipped default UI.
// Keep this function self-contained so sandboxed/browser fixtures can inline it.
module.exports = function withDefaultShell(api) {
  const activationReports = [];
  const shell = {
    id: "builtin:wuu.default-shell", kind: "plugin", name: "Default shell fixture",
    provenance: { kind: "plugin", source: "builtin", scope: "user", plugin_id: "wuu.default-shell" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    graph_generation: "default-shell-e2e-graph",
    fingerprint: "default-shell-e2e", desktop: { entry: "desktop.js", services: [{ name: "ui.shell", version: "1.0.0" }] },
  };
  const surfaces = ["navigation", "conversation", "composer", "settings", "preview"].map(name => ({
    ...shell, id: `builtin:wuu.default-${name}`, name: `Default ${name} fixture`,
    provenance: { ...shell.provenance, plugin_id: `wuu.default-${name}` },
    fingerprint: `default-${name}-e2e`,
    desktop: { entry: "desktop.js", services: [{ name: `ui.${name}`, version: "1.0.0" }] },
  }));
  return {
    ...api,
    reportPluginDesktopActivation: async report => { activationReports.push(report); },
    testDesktopActivations: async () => activationReports,
    initialize: async (...args) => {
      const initialized = await api.initialize(...args);
      return { ...initialized, application_shell: { plugin_id: shell.id, generation: shell.fingerprint, provider_id: "ui.shell" },
        application_surfaces: Object.fromEntries(surfaces.map(surface => [surface.desktop.services[0].name, {
          plugin_id: surface.id, generation: surface.fingerprint, provider_id: surface.desktop.services[0].name,
        }])),
        extension_inventory: [shell, ...surfaces, ...(initialized.extension_inventory ?? [])] };
    },
    getPluginConflictPreferences: api.getPluginConflictPreferences ?? (async () => ({})),
    loadPluginDesktopModule: async (request) => {
      if (request.id === shell.id && request.fingerprint === shell.fingerprint) {
        return { ...request, digest: "0".repeat(64), url: "wuu-builtin:default-shell" };
      }
      const surface = surfaces.find(surface => surface.id === request.id && surface.fingerprint === request.fingerprint);
      if (surface) return { ...request, digest: "0".repeat(64), url: `wuu-builtin:default-${surface.desktop.services[0].name.slice(3)}` };
      if (!api.loadPluginDesktopModule) throw new Error(`Unknown fixture module: ${request.id}`);
      return api.loadPluginDesktopModule(request);
    },
  };
};
