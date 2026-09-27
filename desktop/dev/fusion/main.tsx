import React, { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { FusionSettings } from "../../src/renderer/FusionSettings";
import { EnvironmentPanel } from "../../src/renderer/EnvironmentPanel";
import { AssistantTurnShell } from "../../src/renderer/AssistantTurnShell";
import { buildAssistantTurnDisplay } from "../../src/renderer/AssistantTurnDisplay";
import { RuntimePicker } from "../../src/renderer/ComposerRuntimeMenus";
import type { CodexRuntimeMenu } from "../../src/renderer/ComposerTypes";
import { I18nProvider } from "../../src/renderer/i18n";
import type {
  FusionConfig,
  ProviderSummary,
  Turn,
} from "../../src/shared/protocol";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.style.setProperty(
  "--conversation-message-font-size",
  `${Number(params.get("font")) || 14}px`,
);
document.documentElement.dataset.platform = "mac";
const providers: ProviderSummary[] = [
  {
    name: "Example service",
    type: "openai-compatible",
    model: "balanced",
    base_url: "https://example.invalid",
    api_key_configured: true,
    models: [
      { id: "fast", display_name: "Fast" },
      { id: "balanced", display_name: "Balanced" },
      {
        id: "capable",
        display_name: params.has("long")
          ? "A model with a very long descriptive display name for layout testing"
          : "Capable",
        supported_efforts: ["low", "high"],
      },
    ],
  },
  {
    name: "Second service",
    type: "anthropic",
    model: "alternative",
    base_url: "https://example.invalid",
    api_key_configured: true,
    models: [{ id: "alternative" }],
  },
];
function Preview() {
  const anchor = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const [openMenu, setOpenMenu] = useState<CodexRuntimeMenu>(null);
  const [selected, setSelected] = useState({ provider: providers[0].name, model: "wuu/fusion", variant: "" });
  const [value, setValue] = useState<FusionConfig>({
    enabled: !params.has("scroll"),
    default: true,
    lead: { provider: "Example service", model: "capable" },
    sidekick: { provider: "Example service", model: "fast" },
  });
  const turn = {
    id: "example",
    items: [
      ...(params.has("solo") ? [] : [{ id: "delegate", type: "tool_call", name: "fusion_delegate", status: "completed", result: JSON.stringify({ outcome: params.get("outcome") || "completed", input_tokens: 1200, output_tokens: 180, cache_read_tokens: 400 }) }]),
      { id: "answer", type: "agent_message", role: "assistant", status: "completed", terminal: true, text: "这是 Fusion 的示例回复。" },
    ],
    items_view: "full",
    status: "completed",
    duration_ms: 88_000,
    fusion: {
      lead: params.has("long")
        ? { provider: "A service with a long provider name", model: "a-model-with-a-very-long-identifier-for-layout-testing" }
        : value.lead,
      sidekick: value.sidekick,
    },
  } as Turn;
  if (params.has("info")) {
    return (
      <div className="conversation-pane" style={{ position: "relative", minHeight: "100vh" }}>
        <EnvironmentPanel
          panelRef={panelRef}
          motionState="open"
          initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/example", ...selected }}
          fusion={turn.fusion}
          activeMenu={null}
          running={false}
          pullRequestDisabledReason=""
          onSetActiveMenu={() => {}}
          onClose={() => {}}
          onSelectBranch={() => {}}
          onCreateBranch={async () => {}}
          onOpenReview={() => {}}
          onOpenCommit={() => {}}
          onOpenPullRequest={() => {}}
        />
      </div>
    );
  }
  return (
    <main
      className="settings-page"
      style={{
        maxWidth: 800,
        padding: 24,
        margin: "0 auto",
        height: "100vh",
        overflow: "auto",
      }}
    >
      {params.has("scroll") && (
        <section style={{ minHeight: "72vh", flexShrink: 0 }}>
          <h2 className="settings-section-title">Connected services</h2>
          {providers.map((provider) => (
            <div className="settings-row" key={provider.name}>{provider.name}</div>
          ))}
        </section>
      )}
      <FusionSettings
        value={value}
        providers={params.has("empty") ? [] : providers}
        disabled={false}
        onSave={async (update) => {
          if (params.has("error"))
            throw new Error("The selected model is no longer available.");
          if (update.fusion) setValue(update.fusion);
        }}
      />
      <AssistantTurnShell turn={turn} display={buildAssistantTurnDisplay(turn, undefined)!} onStreamFrame={() => {}} />
      <div style={{ marginTop: 240 }}>
        <RuntimePicker
          initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/example", ...selected, providers, advanced_settings: { fusion: value, max_steps: 0, max_context_tokens: 0, temperature: 0, disable_auto_compact: false } }}
          state={{ loading: false, error: "", models: [] }}
          openMenu={openMenu} anchorRef={anchor} running={false}
          onToggleMenu={(menu) => setOpenMenu((current) => current === menu ? null : menu)}
          onSelectModel={(provider, model, variant) => setSelected({ provider, model, variant: variant || "" })}
          onSelectEffort={(variant) => setSelected((current) => ({ ...current, variant }))}
        />
      </div>
    </main>
  );
}
createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuUIRoot>
      <Preview />
    </WuuUIRoot>
  </I18nProvider>,
);
