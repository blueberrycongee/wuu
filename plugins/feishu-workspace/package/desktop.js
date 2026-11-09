/** @param {import("@wuu/plugin-sdk").PluginGenerationApi} api */
export function activate(api) {
  const React = api.react, h = React.createElement;
  const { WebPage, ErrorState, LoadingState } = api.ui;
  api.registerLocale({ id: "feishu-en", locale: "en-US", entries: {
    "feishu.title": "Feishu", "feishu.loading": "Opening Feishu…",
    "feishu.invalid": "Set an HTTPS Feishu workspace URL in plugin settings.",
    "feishu.unavailable": "This Wuu version does not support embedded website views.",
    "feishu.failed": "Could not read Feishu workspace settings.",
  } });
  api.registerLocale({ id: "feishu-zh", locale: "zh-CN", entries: {
    "feishu.title": "飞书", "feishu.loading": "正在打开飞书…",
    "feishu.invalid": "请在插件设置中填写 HTTPS 飞书工作区网址。",
    "feishu.unavailable": "当前 Wuu 版本不支持内嵌网页视图。",
    "feishu.failed": "无法读取飞书工作区设置。",
  } });
  function FeishuView({ host, translate: tr }) {
    const [state, setState] = React.useState({ url: "", error: "" });
    React.useEffect(() => {
      let active = true;
      async function load() {
        try {
          const value = await host.getSetting("feishu-workspace.workspace-url");
          const url = new URL(String(value));
          if (url.protocol !== "https:" || url.username || url.password || url.port || !(url.hostname === "feishu.cn" || url.hostname.endsWith(".feishu.cn"))) throw new Error("feishu.invalid");
          if (active) setState({ url: url.href, error: "" });
        } catch (error) {
          if (active) setState({ url: "", error: error.message === "feishu.invalid" || error instanceof TypeError ? "feishu.invalid" : "feishu.failed" });
        }
      }
      void load();
      return () => { active = false; };
    }, [host]);
    if (state.error) return h(ErrorState, { title: tr("feishu.title"), description: tr(state.error) });
    if (typeof WebPage !== "function") return h(ErrorState, { title: tr("feishu.title"), description: tr("feishu.unavailable") });
    return state.url ? h(WebPage, { url: state.url, title: tr("feishu.title") }) : h(LoadingState, { title: tr("feishu.loading") });
  }
  api.registerViewType({ id: "feishu.web", title: "Feishu / 飞书", icon: "file-text", defaultRegion: "primary", persistence: "durable", render: FeishuView });
}
