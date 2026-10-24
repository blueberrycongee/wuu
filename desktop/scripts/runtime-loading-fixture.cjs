// Keep startup pending until the isolated renderer journey resolves it.
module.exports = api => {
  let complete;
  let fail;
  const pending = new Promise((resolve, reject) => { complete = resolve; fail = reject; });
  return {
    ...api,
    initialLanguagePreference: "zh-CN",
    initialize: async () => { await pending; return api.initialize(); },
    testStartup: outcome => outcome === "ready" ? complete() : fail(new Error(outcome)),
  };
};
