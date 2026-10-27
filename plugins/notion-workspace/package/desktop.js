export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { WebPage, LoadingState, ErrorState, Button } = api.ui;
  api.registerLocale({ id: "notion-en", locale: "en-US", entries: {
    "notion.loading": "Opening Notion…",
    "notion.error": "Cannot open Notion",
    "notion.invalidURL": "Choose an HTTPS notion.so or notion.com URL in the plugin settings.",
    "notion.unavailable": "This Wuu version does not support website views.",
    "notion.retry": "Retry",
  } });
  api.registerLocale({ id: "notion-zh", locale: "zh-CN", entries: {
    "notion.loading": "正在打开 Notion…",
    "notion.error": "无法打开 Notion",
    "notion.invalidURL": "请在插件设置中填写 notion.so 或 notion.com 的 HTTPS 地址。",
    "notion.unavailable": "当前 Wuu 版本不支持网站视图。",
    "notion.retry": "重试",
  } });
  function Website({ host, translate: tr }) {
    const [state, setState] = React.useState({ url: "", error: "" });
    const [revision, setRevision] = React.useState(0);
    React.useEffect(() => {
      let active = true;
      setState({ url: "", error: "" });
      void host.getSetting("website-url").then((value) => {
        let url;
        try {
          url = new URL(typeof value === "string" && value.trim() ? value.trim() : "https://www.notion.so/");
          if (url.protocol !== "https:" || url.username || url.password || url.port
            || !["notion.so", "notion.com"].some((domain) => url.hostname === domain || url.hostname.endsWith(`.${domain}`))) throw new Error();
        } catch { throw new Error(tr("notion.invalidURL")); }
        if (active) setState({ url: url.href, error: "" });
      }).catch((error) => { if (active) setState({ url: "", error: String(error.message || error) }); });
      return () => { active = false; };
    }, [host, tr, revision]);
    if (state.error || typeof WebPage !== "function") return h(ErrorState, {
      title: tr("notion.error"), description: state.error || tr("notion.unavailable"),
      actions: h(Button, { onClick: () => setRevision((value) => value + 1) }, tr("notion.retry")),
    });
    return state.url ? h(WebPage, { url: state.url, title: "Notion" }) : h(LoadingState, { title: tr("notion.loading") });
  }
  api.registerViewType({
    id: "website", title: "Notion", icon: "file-text", defaultRegion: "primary", persistence: "durable",
    render: (props) => h(Website, props),
  });
}
