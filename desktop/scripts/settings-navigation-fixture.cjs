module.exports = api => ({
  ...api,
  initialLanguagePreference: "zh-CN",
  listAllThreads: () => api.listThreads(),
  getSessionOrganization: async () => ({ organization: { folders: [] } }),
});
