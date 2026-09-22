import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { AutoModelSettings } from "../../src/renderer/AutoModelSettings";
import { AutoModelStatus } from "../../src/renderer/AutoModelStatus";
import { I18nProvider } from "../../src/renderer/i18n";
import type {
  AutoModelConfig,
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
  const [value, setValue] = useState<AutoModelConfig>({
    enabled: true,
    default: true,
    classifier: { provider: "Example service", model: "fast" },
    simple: { provider: "Example service", model: "fast" },
    medium: { provider: "Example service", model: "balanced" },
    complex: { provider: "Example service", model: "capable" },
  });
  const turn = {
    id: "example",
    items: [],
    items_view: "full",
    status: "completed",
    auto_model: {
      tier: "complex",
      selection: value.complex,
      classifier: value.classifier,
      duration_ms: 720,
      reason: "classifier_failed",
      usage: { InputTokens: 120, OutputTokens: 8 },
    },
  } as Turn;
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
      <AutoModelSettings
        value={value}
        providers={params.has("empty") ? [] : providers}
        disabled={false}
        onSave={async (update) => {
          if (params.has("error"))
            throw new Error("The selected model is no longer available.");
          if (update.auto_model) setValue(update.auto_model);
        }}
      />
      <AutoModelStatus turn={turn} />
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
