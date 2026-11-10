import * as React from "react";
import { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { RuntimePicker } from "../../src/renderer/ComposerRuntimeMenus";
import type { CodexRuntimeMenu } from "../../src/renderer/ComposerTypes";
import { I18nProvider, setActiveLocale, useI18n } from "../../src/renderer/i18n";
import { EngineIcon } from "../../src/renderer/EngineIcons";
import type { EngineInfo, InitializeResult } from "../../src/shared/protocol";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { ToastViewport } from "../../src/renderer/Toast";
import "../../src/renderer/styles.css";

// Visual acceptance page for the flat runtime picker. Open with
//   npx vite --config dev/runtime-picker/vite.config.ts
// Scenes: ?scene=wuu|engine|rail|locked|noeffort&theme=light|dark&lang=en-US|zh-CN

const params = new URLSearchParams(location.search);
const scene = params.get("scene") ?? "wuu";
if (params.get("theme")) document.documentElement.dataset.theme = params.get("theme")!;
if (params.get("lang")) setActiveLocale(params.get("lang") as "en-US" | "zh-CN");

const PROVIDERS: NonNullable<InitializeResult["providers"]> = [
  {
    name: "work",
    type: "anthropic",
    model: "claude-sonnet",
    catalog_id: "anthropic",
    models: [
      { id: "claude-opus", display_name: "Claude Opus 4.6", supported_efforts: ["low", "medium", "high", "xhigh", "max"], default_effort: "high", fast_mode: true },
      { id: "claude-sonnet", display_name: "Claude Sonnet 4.6", supported_efforts: ["low", "medium", "high", "xhigh"], default_effort: "high", fast_mode: true, default_speed: "standard" },
      { id: "claude-haiku", display_name: "Claude Haiku 4.5", supported_efforts: ["low", "medium"], default_effort: "low" },
    ],
  },
  {
    name: "tokenhub",
    type: "openai-compatible",
    model: "gpt-5.6-sol",
    models: [
      { id: "gpt-5.6-sol", display_name: "GPT-5.6 Sol", supported_efforts: ["minimal", "low", "medium", "high"], default_effort: "medium" },
      { id: "gpt-5.6-terra", display_name: "GPT-5.6 Terra", supported_efforts: ["low", "medium", "high"], fast_mode: true },
      { id: "gpt-5.6-luna", display_name: "GPT-5.6 Luna" },
    ],
  },
];

const ENGINES: EngineInfo[] = [
  { id: "wuu", enabled: true, binary_ok: true },
  {
    id: "codex",
    enabled: true,
    binary_ok: true,
    models: [
      {
        id: "gpt-5.4",
        display_name: "GPT-5.4",
        supported_efforts: ["minimal", "low", "medium", "high", "xhigh"],
        default_effort: "medium",
        fast_mode: true,
        is_default: true,
        options: [
          {
            id: "sandbox",
            label: "Sandbox",
            type: "select",
            default_value: "workspace-write",
            choices: [
              { value: "read-only", label: "Read only" },
              { value: "workspace-write", label: "Workspace write" },
              { value: "danger-full-access", label: "Full access" },
            ],
          },
          {
            id: "web",
            label: "Web search",
            type: "boolean",
            default_value: "off",
            choices: [
              { value: "on", label: "On" },
              { value: "off", label: "Off" },
            ],
          },
        ],
      },
      { id: "gpt-5.4-mini", display_name: "GPT-5.4 mini", supported_efforts: ["low", "medium", "high"], fast_mode: true },
      { id: "codex-mini-latest", display_name: "Codex Mini", supported_efforts: ["low", "medium", "high"] },
      { id: "o4", display_name: "o4", supported_efforts: ["low", "medium", "high", "xhigh"] },
    ],
  },
  {
    id: "claude",
    enabled: true,
    binary_ok: true,
    models: [
      { id: "claude-code-sonnet", display_name: "Sonnet 4.6", supported_efforts: ["low", "medium", "high"], is_default: true },
      { id: "claude-code-opus", display_name: "Opus 4.6", supported_efforts: ["low", "medium", "high", "max"] },
    ],
  },
  { id: "grok", enabled: false, binary_ok: false },
];

function sceneInitialized(): InitializeResult {
  const base: InitializeResult = {
    protocol_version: "wuu-app-server/v0.1",
    provider: "work",
    model: "claude-sonnet",
    variant: "high",
    speed: "standard",
    workspace_root: "/tmp/project",
    permissions: { mode: "standard" },
    providers: PROVIDERS,
  };
  if (scene === "noeffort") return { ...base, model: "gpt-5.6-luna", provider: "tokenhub", variant: "" };
  return base;
}

function Picker({ fromRail }: { fromRail?: boolean }): JSX.Element {
  const [openMenu, setOpenMenu] = useState<CodexRuntimeMenu>("model");
  const [engine, setEngine] = useState(params.get("engine") ?? (scene === "engine" || scene === "locked" ? "codex" : "wuu"));
  const [engineModel, setEngineModel] = useState("gpt-5.4");
  const [engineEffort, setEngineEffort] = useState("high");
  const [engineSpeed, setEngineSpeed] = useState("standard");
  const [engineOptions, setEngineOptions] = useState<Record<string, string>>({});
  const [variant, setVariant] = useState(sceneInitialized().variant ?? "");
  const [model, setModel] = useState(sceneInitialized().model);
  const [provider, setProvider] = useState(sceneInitialized().provider);
  const [speed, setSpeed] = useState("standard");
  const anchorRef = useRef<HTMLDivElement>(null);
  const hostEngine = engine === "wuu";
  const initialized = { ...sceneInitialized(), provider, model, variant, speed };
  return (
    <RuntimePicker
      inToolRail={fromRail}
      initialized={initialized}
      state={{ provider, loading: false, error: "", models: [] }}
      openMenu={openMenu}
      anchorRef={anchorRef}
      running={false}
      engines={ENGINES}
      activeEngine={engine}
      engineLocked={scene === "locked"}
      engineModel={engineModel}
      engineEffort={engineEffort}
      engineSpeed={engineSpeed}
      engineModelOptions={engineOptions}
      onToggleMenu={(menu) => setOpenMenu((current) => (current === menu ? null : menu))}
      onSelectEngine={(id) => setEngine(id)}
      onSelectEngineModel={async (next, effort) => { setEngineModel(next); setEngineEffort(effort); return true; }}
      onSelectEngineEffort={async (effort) => { setEngineEffort(effort); return true; }}
      onSelectEngineModelOptions={async (options) => { setEngineOptions(options); return true; }}
      onSelectSpeed={async (next) => { if (hostEngine) setSpeed(next); else setEngineSpeed(next); return true; }}
      onSelectModel={async (nextProvider, nextModel, nextVariant) => { setProvider(nextProvider); setModel(nextModel); setVariant(nextVariant ?? ""); return true; }}
      onSelectEffort={async (next) => { setVariant(next); return true; }}
    />
  );
}

function Shell(): JSX.Element {
  const { t } = useI18n();
  return (
    <>
      {scene === "rail" ? (
        <div className="dev-rail" style={{ position: "fixed", top: 0, right: 0, bottom: 0, width: 44, display: "flex", flexDirection: "column", alignItems: "center", gap: 4, paddingTop: 80, borderLeft: "1px solid var(--hairline-soft)", background: "var(--surface-1, Canvas)" }}>
          <EngineIcon engine="wuu" />
          <Picker fromRail />
        </div>
      ) : (
        <div style={{ position: "absolute", left: "50%", bottom: 24, transform: "translateX(-50%)", width: 680, border: "1px solid var(--hairline-soft)", borderRadius: 12, padding: 8, display: "flex", alignItems: "center", gap: 8 }}>
          <Picker />
          <span style={{ flex: 1, color: "var(--ink-muted)", fontSize: 13 }}>{t("runtime.selectModel")}</span>
        </div>
      )}
      <ToastViewport />
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuUIRoot>
      <Shell />
    </WuuUIRoot>
  </I18nProvider>,
);
