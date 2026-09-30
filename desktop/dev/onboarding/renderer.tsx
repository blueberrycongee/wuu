import { createRoot } from "react-dom/client";
import { FirstRunOnboarding } from "../../src/renderer/FirstRunOnboarding";
import { WuuMascotRuntimeProvider } from "../../src/renderer/WuuMascot";
import { I18nProvider } from "../../src/renderer/i18n";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { applyPlatformStamp } from "../../src/renderer/platform";
import type { CatalogProviderSummary, EngineListResult, WuuDesktopApi } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";

async function rejectPersistence(): Promise<never> {
  throw new Error("Onboarding preview must not persist changes");
}

const params = new URLSearchParams(window.location.search);
const availableEngines = new Set(params.getAll("availableEngine"));
const engines: EngineListResult = {
  engines: ["wuu", "codex", "claude"].map((id) => ({
    id,
    enabled: id === "wuu" || availableEngines.has(id),
    binary_ok: id === "wuu" || availableEngines.has(id),
  })),
};

// A synthetic model catalog, so the provider step offers its services
// without the app server. Connecting in the preview saves nothing.
const catalogProviders: CatalogProviderSummary[] = [
  ["deepseek", "DeepSeek", "openai-compatible", "https://api.deepseek.com", 4, "deepseek-v4-pro"],
  ["moonshotai-cn", "Moonshot AI (China)", "openai-compatible", "https://api.moonshot.cn/v1", 4, "kimi-k3"],
  ["zhipuai", "Zhipu AI", "openai-compatible", "https://open.bigmodel.cn/api/paas/v4", 16, "glm-5.3"],
  ["alibaba-cn", "Alibaba (China)", "openai-compatible", "https://dashscope.aliyuncs.com/compatible-mode/v1", 90, "qwen3.8-max"],
  ["openrouter", "OpenRouter", "openai-compatible", "https://openrouter.ai/api/v1", 372, "z-ai/glm-5.3"],
  ["openai", "OpenAI", "openai", "https://api.openai.com/v1", 49, "gpt-6-astra"],
  ["anthropic", "Anthropic", "anthropic", "https://api.anthropic.com", 16, "claude-fable-5-1"],
  ["xai", "xAI", "openai-compatible", "https://api.x.ai/v1", 10, "grok-4.6"],
].map(([id, name, type, base_url, model_count, default_model]) => ({ id, name, type, base_url, model_count, default_model } as CatalogProviderSummary));

window.wuu = {
  ...(params.has("lang") ? { initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN" } : {}),
  listCatalogProviders: async (provider?: string) => ({
    providers: provider
      ? catalogProviders.filter((item) => item.id === provider).map((item) => ({
        ...item,
        models: [item.default_model, `${item.default_model}-mini`].map((id) => ({ id, tool_call: true })),
      }))
      : catalogProviders,
  }),
} as unknown as WuuDesktopApi;

applyPlatformStamp();
startFocusModality();
createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuMascotRuntimeProvider>
      <FirstRunOnboarding
        preview
        engines={engines}
        onDismissPreview={() => window.close()}
        onUpdateExtensionPackage={rejectPersistence}
        onSaveProvider={rejectPersistence}
        onUpdateEngines={rejectPersistence}
        onComplete={rejectPersistence}
      />
    </WuuMascotRuntimeProvider>
  </I18nProvider>,
);
