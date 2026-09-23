import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { EngineListResult, ProviderSummary } from "../../src/shared/protocol";
import { SubscriptionDashboard } from "../../src/renderer/SubscriptionDashboard";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { I18nProvider } from "../../src/renderer/i18n";
import "../../src/renderer/styles.css";

// Isolated visual fixture: no Go process, account discovery, or real credentials.
const params = new URLSearchParams(location.search);
startFocusModality();
document.documentElement.dataset.theme = params.get("theme") || "light";
document.body.style.background = "var(--paper)";
document.documentElement.style.setProperty("--conversation-message-font-size", `${params.get("size") || 14}px`);
const usage = { input_tokens: 146800, output_tokens: 24100, cache_creation_tokens: 0, cache_read_tokens: 31000, reported_turns: 12 };
const inventory: EngineListResult = { engines: params.has("empty") ? [] : [
  { id: "codex", display_name: "Codex", enabled: true, binary_ok: true, models: [{ id: "gpt-5.4", display_name: "GPT-5.4", is_default: true }], local_usage: usage,
    quota: { status: "available", checked_at: new Date().toISOString(), windows: [
      { id: "short", used_percent: 36, window_minutes: 300, resets_at: new Date(Date.now() + 7200000).toISOString() },
      { id: "weekly", used_percent: 71, window_minutes: 10080, resets_at: new Date(Date.now() + 172800000).toISOString() },
    ] } },
  { id: "grok", display_name: params.has("long") ? "Grok — subscription-with-a-long-account-name@example.test" : "Grok", protocol: "acp", enabled: true, binary_ok: true, models: [{ id: "grok-4.7", display_name: "Grok 4.7", is_default: true }], local_usage: usage },
  { id: "claude", display_name: "Claude Code", enabled: true, binary_ok: true, models: [{ id: "sonnet", display_name: "Claude Sonnet", is_default: true }] },
  { id: "cursor", display_name: "Cursor", protocol: "acp", enabled: false, binary_ok: true },
] };
const providers: ProviderSummary[] = params.has("empty") ? [] : [{ name: "SuperGrok", type: "xai-subscription", model: "grok-4.7", models: [{ id: "grok-4.7", display_name: "Grok 4.7" }, { id: "grok-4-fast", display_name: "Grok 4 Fast" }], api_key_configured: true, local_usage: usage }];
if (params.has("states") && inventory.engines.length) {
  inventory.engines[0].quota!.windows![0].used_percent = 100;
  inventory.engines[0].quota!.windows![1].resets_at = new Date(Date.now() - 60_000).toISOString();
  inventory.engines[1].models = [];
  inventory.engines[1].models_error = "Synthetic catalog failure";
  inventory.engines[2].quota = { status: "unavailable", checked_at: new Date().toISOString() };
  inventory.engines[3].models = [{ id: "auto", display_name: "Auto" }];
}
if (params.has("failures")) {
  inventory.engines[0].quota = undefined;
  inventory.engines[0].latest_request = { status: "interrupted", error: "context canceled" };
  inventory.engines.splice(3, 0,
    { id: "devin", display_name: "Devin", protocol: "acp", enabled: true, binary_ok: true },
    { id: "hermes", display_name: "Hermes", protocol: "acp", enabled: true, binary_ok: true,
      models_error: "session/new: engine RPC error -32603: Internal error\n" + "[WARNING] synthetic agent stderr /example/private/config\n".repeat(30) },
    { id: "opencode", display_name: "OpenCode", protocol: "acp", enabled: true, binary_ok: true },
  );
  providers.push({ name: "openai-codex", type: "openai", reuse_codex_credentials: true, model: "gpt-5.4", api_key_configured: false });
}
window.wuu = {
  ...window.wuu,
  initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN",
  listEngines: async () => structuredClone(inventory),
  listEngineAuthMethods: async () => ({ methods: [{ id: "browser", name: "Browser" }], authenticated: false }),
  authenticateEngine: async (id) => {
    const engine = inventory.engines.find((item) => item.id === id)!;
    engine.models_error = undefined;
    engine.models = [{ id: "auto", display_name: "Auto", is_default: true }];
    return { methods: [], authenticated: true };
  },
  cancelEngineAuth: async () => ({ ok: true }),
};

function Preview() {
  const [sources, setSources] = useState(providers);
  return <main style={{ height: "100vh", overflow: "auto" }}><div className="settings-page">
    <SubscriptionDashboard inventory={inventory} providers={sources} onSelectBuiltinModel={async (name, model) => setSources((current) => current.map((source) => source.name === name ? { ...source, model } : source))} />
  </div></main>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><Preview /></I18nProvider>);
