import { createRoot } from "react-dom/client";
import { RuntimeLoading } from "../../src/renderer/LoadingViews";
import { I18nProvider, useI18n, type LanguagePreferenceStore } from "../../src/renderer/i18n";
import "../../src/renderer/styles.css";

// Launch and recovery states of RuntimeLoading, without the product bridge.
// Query parameters: state=starting|recovering|failed (default failed),
// actions=retry|recovery|both (default recovery), theme=dark, font=20,
// lang=en, long (a raw multi-line failure detail).
const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") ?? "light";
document.documentElement.style.setProperty("--conversation-message-font-size", `${params.get("font") ?? 15}px`);
const state = params.get("state") ?? "failed";
const actions = params.get("actions") ?? "recovery";
const locale = params.get("lang") === "en" ? "en-US" : "zh-CN";
const languageStore: LanguagePreferenceStore = {
  get: () => locale,
  set: async () => undefined,
  subscribe: () => () => {},
};
const noop = () => {};
const longStatus = [
  "Error: application provider \"ui.application\" exited during activation",
  "    at DesktopPluginRuntime.prepare (provider-runtime.ts:418:11)",
  "    at async DesktopBootstrap.initialize (bootstrap.ts:203:5)",
  "caused by: ECONNREFUSED 127.0.0.1:58123 — the app-server socket closed before the activation report",
].join("\n");

function Scene(): JSX.Element {
  const { t } = useI18n();
  const recovery = actions !== "retry" ? noop : undefined;
  const retry = actions !== "recovery" ? noop : undefined;
  if (state === "starting") return <RuntimeLoading fullscreen status="connecting" />;
  if (state === "recovering") return <RuntimeLoading fullscreen recovering status="connecting" onRecovery={recovery} onRetry={retry} />;
  return <RuntimeLoading fullscreen status={params.has("long") ? longStatus : t("plugins.connectionUnavailable")} onRecovery={recovery} onRetry={retry} />;
}

createRoot(document.getElementById("root")!).render(<I18nProvider preferenceStore={languageStore}><Scene /></I18nProvider>);
