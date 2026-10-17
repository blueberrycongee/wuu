import * as React from "react";
import { createRoot } from "react-dom/client";
import { createPluginUIKit } from "../../src/shared/workbench";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { activate } from "../../../internal/plugin/bundled/next-message/desktop.js";
import "../../src/renderer/styles.css";
import "./style.css";

// The production plugin and UI kit render against disposable settings, with no
// model calls or access to the installed profile.
const params = new URLSearchParams(location.search);
const locale = params.get("locale") === "en" ? "en-US" : "zh-CN";
document.documentElement.lang = locale;
document.documentElement.dataset.theme = params.get("theme") === "dark" ? "dark" : "light";
applyMessageFlowFontSize(Number(params.get("font")) || 15);
startFocusModality();
const workspace = { id: "preview", root: "/preview", available: true };
const snapshot = { activeWorkspaceId: workspace.id, activeRoot: workspace.root, workspaces: [workspace] };
const entries: Record<string, string> = {};
let view: { render: (props: unknown) => React.ReactNode };
let settings = params.has("empty") ? {} : { provider: "OpenAI", model: "gpt-5-mini", variant: "low" };
await activate({
  react: React,
  ui: createPluginUIKit(React),
  signal: new AbortController().signal,
  listWorkspaces: async () => snapshot,
  onHostEvent: () => {},
  registerComposerInlineCompletionProvider: () => ({ refresh() {} }),
  registerCleanup: () => {},
  registerLocale: (value) => { if (value.locale === locale) Object.assign(entries, value.entries); },
  registerStyle: ({ css }) => { const style = document.createElement("style"); style.textContent = css; document.head.append(style); },
  registerViewType: (value) => { view = value; },
  invokeRuntime: async (method, input) => {
    if (method === "next-message.update") settings = input;
    return { settings };
  },
});
const models = params.has("empty") ? [] : [
  { provider: "OpenAI", model: "gpt-5-mini", label: "GPT-5 mini", variants: ["minimal", "low", "medium", "high"] },
  { provider: "OpenAI", model: "gpt-5", label: params.has("long") ? "A very long custom model name for a workspace provider" : "GPT-5", variants: ["low", "medium", "high"] },
  { provider: "Anthropic", model: "claude-haiku", label: "Claude Haiku", variants: [] },
];
const host = { settings: {
  listAvailableModels: () => models,
  getValue: () => params.has("empty") ? {} : { "Fast suggestions": { provider: "OpenAI", model: "gpt-5-mini", variant: "low" } },
} };
const tr = (key: string) => entries[key] ?? key;
function Preview() {
  return <main className="settings-main preview-main">
    <div className="settings-titlebar">
      <span className="settings-crumb-root">{locale === "zh-CN" ? "设置" : "Settings"}</span>
      <span className="settings-crumb-separator">/</span>
      <div className="settings-titlebar-slot"><h1 className="settings-page-title">{tr("nextMessage.title")}</h1></div>
      <button className="preview-theme" aria-label="Toggle theme" onClick={() => {
        const root = document.documentElement;
        root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark";
      }}>◐</button>
    </div>
    <div className="settings-scroll"><div className="settings-page">
      {view.render({ host, translate: tr })}
    </div></div>
  </main>;
}
createRoot(document.getElementById("root")!).render(<Preview />);
