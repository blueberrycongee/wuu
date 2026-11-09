module.exports = api => ({
  ...api,
  initialize: async (...args) => {
    const initialized = await api.initialize(...args);
    return { ...initialized, extension_inventory: [
      ...initialized.extension_inventory,
      ...Array.from({ length: 40 }, (_, index) => ({
        id: `user:scroll-fixture-${index}`, kind: "plugin", name: `Scroll fixture ${index} with a long navigation label`,
        state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
        provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: `scroll-fixture-${index}` },
      })),
    ] };
  },
  initialLanguagePreference: "zh-CN",
  listAllThreads: () => api.listThreads(),
  getSessionOrganization: async () => ({ organization: { folders: [] } }),
});
