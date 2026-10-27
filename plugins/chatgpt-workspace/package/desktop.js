export function activate(api) {
  api.registerViewType({
    id: "website",
    title: "ChatGPT",
    icon: { light: "icon-light.svg", dark: "icon-dark.svg" },
    defaultRegion: "primary",
    persistence: "durable",
    render: () => api.react.createElement(api.ui.WebPage, {
      url: "https://chatgpt.com/",
      title: "ChatGPT",
    }),
  });
}
