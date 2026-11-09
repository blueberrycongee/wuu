// Keep startup pending until the isolated renderer journey resolves it.
module.exports = api => {
  let complete;
  let fail;
  const pending = new Promise((resolve, reject) => { complete = resolve; fail = reject; });
  let completeUI;
  let failUI;
  let moduleRequests = 0;
  const pendingUI = new Promise((resolve, reject) => { completeUI = resolve; failUI = reject; });
  return {
    ...api,
    initialLanguagePreference: "zh-CN",
    initialize: async () => { await pending; return api.initialize(); },
    testStartup: outcome => outcome === "ready" ? complete() : fail(new Error(outcome)),
    loadPluginDesktopModule: async request => { moduleRequests++; await pendingUI; return api.loadPluginDesktopModule(request); },
    testModuleRequests: () => moduleRequests,
    testUIStartup: outcome => outcome === "ready" ? completeUI() : failUI(new Error(outcome)),
    listAllThreads: () => api.listThreads(),
    getSessionOrganization: async () => ({ organization: { folders: null } }),
  };
};
