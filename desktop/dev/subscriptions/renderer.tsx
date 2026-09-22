import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { EngineListResult, ProviderSummary } from "../../src/shared/protocol";
import { SubscriptionDashboard } from "../../src/renderer/SubscriptionDashboard";
import "../../src/renderer/styles.css";

// Isolated visual fixture: no Go process, account discovery, or real credentials.
const params = new URLSearchParams(location.search);
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
const providers: ProviderSummary[] = params.has("empty") ? [] : [{ name: "SuperGrok", type: "xai-subscription", model: "grok-4.7", api_key_configured: true, local_usage: usage }];
window.wuu = { ...window.wuu, listEngines: async () => inventory, listEngineAuthMethods: async () => ({ engine_id: "grok", methods: [], authenticated: false }) };

function Preview() {
  const [sources, setSources] = useState(providers);
  return <main style={{ maxWidth: 880, margin: "auto", padding: 24, height: "100vh", overflow: "auto" }}>
    <p style={{ color: "var(--ink-muted)", marginBottom: 24 }}>UI 预览 · 示例数据</p>
    <SubscriptionDashboard inventory={inventory} providers={sources} onSelectBuiltinModel={async (name, model) => setSources((current) => current.map((source) => source.name === name ? { ...source, model } : source))} />
  </main>;
}
createRoot(document.getElementById("root")!).render(<Preview />);
