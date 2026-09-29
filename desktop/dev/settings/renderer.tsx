import { useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type {
  CatalogProviderSummary,
  CodexPetsSnapshot,
  EngineListResult,
  InitializeResult,
  MCPServerStatus,
  ProviderSummary,
  RuntimeConnectionUpdate,
  SettingsUsageDay,
  SettingsUsageResponse,
  WuuDesktopApi,
} from "../../src/shared/protocol";
import { SettingsView, type ArchivedSessionView, type SettingsPage } from "../../src/renderer/SettingsView";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

// Isolated visual fixture: synthetic providers, agents, MCP servers, usage and
// archive rows. No Go process, saved desktop preferences, or real credentials.
const params = new URLSearchParams(location.search);
const empty = params.has("empty");
const long = params.has("long");
const date = "2026-09-17T08:30:00Z";

const gptModels = ["gpt-5.5", "gpt-5.5-fast", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra", "gpt-6-astra-fast", "gpt-6-luna", "gpt-6-sol"];
const providers: ProviderSummary[] = empty ? [] : [
  {
    name: "openai-codex", type: "openai-codex", model: "gpt-6-astra", base_url: "https://chatgpt.com/backend-api/codex",
    api_key_configured: true, connection_locked: true, reuse_codex_credentials: true, codex_credential_source: "codex-cli",
    models: gptModels.map((id) => ({ id, display_name: id.replace(/^gpt-/, "GPT-").replace(/-(\w)/g, (_, c: string) => ` ${c.toUpperCase()}`), supported_efforts: ["low", "medium", "high", "xhigh"], capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true, context_window: 1_050_000 } })),
  },
  {
    name: "grok-build", type: "grok-build", model: "grok-4.5", base_url: "https://cli-chat-proxy.grok.com/v1", api_key_configured: true, connection_locked: true,
    models: ["grok-4.5", "grok-4.6", "grok-build-0.1"].map((id) => ({ id })),
  },
  {
    name: "deepseek", type: "openai-compatible", model: "deepseek-v4-pro", base_url: "https://api.deepseek.com", api_key_configured: true,
    catalog_id: "deepseek", catalog_name: "DeepSeek", hidden_models: ["deepseek-v4-flash-vision-exp"],
    models: [
      { id: "deepseek-v4-pro", display_name: "DeepSeek V4 Pro", capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true, context_window: 1_000_000 } },
      { id: "deepseek-v4-flash", display_name: "DeepSeek V4 Flash", capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true, context_window: 1_000_000 } },
      { id: "deepseek-flash", display_name: "DeepSeek V4.1 Flash", capabilities: { chat: true, tools: true, structured_output: true, streaming: true, system_role: true, reasoning: true, context_window: 1_000_000 } },
    ],
  },
  {
    name: "openrouter", type: "openai-compatible", model: long ? "provider/an-exceptionally-long-model-identifier-for-truncation-review" : "deepseek/deepseek-v4",
    base_url: "https://openrouter.ai/api/v1", api_key_configured: false, catalog_id: "openrouter", catalog_name: "OpenRouter",
    models: [{ id: "deepseek/deepseek-v4" }, { id: "qwen/qwen3-coder" }],
  },
];

const catalogProviders: CatalogProviderSummary[] = [
  ["deepseek", "DeepSeek", "openai-compatible", "https://api.deepseek.com", 4, "deepseek-v4-pro"],
  ["moonshotai-cn", "Moonshot AI (China)", "openai-compatible", "https://api.moonshot.cn/v1", 4, "kimi-k3"],
  ["zhipuai", "Zhipu AI", "openai-compatible", "https://open.bigmodel.cn/api/paas/v4", 16, "glm-5.3"],
  ["alibaba-cn", "Alibaba (China)", "openai-compatible", "https://dashscope.aliyuncs.com/compatible-mode/v1", 90, "qwen3.8-max"],
  ["volcengine", "Volcengine Ark", "openai-compatible", "https://ark.cn-beijing.volces.com/api/v3", 16, "deepseek-v4-pro-ga-260813"],
  ["siliconflow-cn", "SiliconFlow (China)", "openai-compatible", "https://api.siliconflow.cn/v1", 47, "zai-org/GLM-5.2"],
  ["minimax-cn", "MiniMax (minimaxi.com)", "anthropic", "https://api.minimaxi.com/anthropic/v1", 7, "MiniMax-M3"],
  ["openrouter", "OpenRouter", "openai-compatible", "https://openrouter.ai/api/v1", 372, "z-ai/glm-5.3"],
  ["openai", "OpenAI", "openai", "https://api.openai.com/v1", 49, "gpt-6-astra"],
  ["anthropic", "Anthropic", "anthropic", "https://api.anthropic.com", 16, "claude-fable-5-1"],
  ["xai", "xAI", "openai-compatible", "https://api.x.ai/v1", 10, "grok-4.6"],
  ["lmstudio", "LMStudio", "openai-compatible", "http://127.0.0.1:1234/v1", 3, "qwen3-32b"],
].map(([id, name, type, base_url, model_count, default_model]) => ({ id, name, type, base_url, model_count, default_model } as CatalogProviderSummary));

const engines: EngineListResult = {
  settings: { default_engine: "wuu", claude: { enabled: false } },
  engines: empty ? [] : [
    { id: "codex", display_name: "Codex", enabled: true, binary_ok: true, binary_path: "/usr/local/bin/codex" },
    { id: "claude", display_name: "Claude Code", enabled: false, binary_ok: true, binary_path: "/usr/local/bin/claude" },
    { id: "cursor", display_name: "Cursor", protocol: "acp", enabled: true, binary_ok: false, error: "Cursor executable \"cursor-agent\" not found; install it or configure its executable path (https://cursor.com/docs/cli/acp)", install_url: "https://cursor.com/cli" },
    { id: "opencode", display_name: "OpenCode", protocol: "acp", enabled: true, binary_ok: false, install_url: "https://opencode.ai" },
    { id: "hermes", display_name: "Hermes", protocol: "acp", enabled: true, binary_ok: false },
  ],
};

const mcpServers: MCPServerStatus[] = empty ? [] : [
  { name: "github", state: "connected", connected: true, tool_count: 26, auth_status: "oauth" },
  { name: "linear", state: "needs_auth", connected: false, tool_count: 0, auth_status: "not_logged_in" },
  { name: "postgres", state: "error", connected: false, tool_count: 0, error: "connect ECONNREFUSED 127.0.0.1:5432" },
];

const pets: CodexPetsSnapshot = {
  enabled: !empty, selected_id: "mochi", home: "~/.wuu/pets", errors: [],
  pets: empty ? [] : [{ id: "mochi", display_name: "Mochi", description: "", manifest_path: "", spritesheet_path: "", spritesheet_url: "" }],
};

function isoDay(back: number): string {
  const day = new Date(); day.setDate(day.getDate() - back);
  return `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
}

function sampleUsage(): SettingsUsageResponse {
  const days: SettingsUsageDay[] = Array.from({ length: 300 }, (_, back) => {
    const tokens = back % 7 === 5 || back % 11 === 3 ? 0 : ((back * 37) % 23 + 1) * 9_000;
    return { date: isoDay(back), input_tokens: tokens, output_tokens: tokens / 4, cache_creation_tokens: 0, cache_read_tokens: tokens * 3, cache_hit_rate: 0.75, turns: tokens ? 4 : 0, agents: 0 };
  }).filter((day) => day.turns > 0).reverse();
  const sum = (key: "input_tokens" | "output_tokens" | "cache_read_tokens") => days.reduce((total, day) => total + day[key], 0);
  const input = sum("input_tokens"), output = sum("output_tokens"), cacheRead = sum("cache_read_tokens");
  return {
    total_sessions: 128, generated_at: date, days,
    metrics: {
      prompt_tokens: input + cacheRead, context_tokens: input + cacheRead + output, input_tokens: input, output_tokens: output,
      cache_read_tokens: cacheRead, cache_creation_tokens: 0, cache_hit_rate: 0.75, turns: days.length * 4, agents: 0,
      date_range: [days[0]?.date ?? "", days.at(-1)?.date ?? ""], active_days: days.length,
    },
    model_breakdowns: [
      { provider: "anthropic", model: "claude-sonnet-5", input_tokens: input * 0.6, output_tokens: output * 0.6, cache_creation_tokens: 0, cache_read_tokens: cacheRead * 0.6, sessions: 80 },
      { provider: "openrouter", model: "deepseek/deepseek-v4", input_tokens: input * 0.3, output_tokens: output * 0.3, cache_creation_tokens: 0, cache_read_tokens: cacheRead * 0.3, sessions: 36 },
      { provider: "local", model: "qwen3-32b", input_tokens: input * 0.1, output_tokens: output * 0.1, cache_creation_tokens: 0, cache_read_tokens: cacheRead * 0.1, sessions: 12 },
    ],
    skill_usage: [{ name: "code-review", count: 42 }, { name: "frontend-design", count: 18 }, { name: "release-notes", count: 7 }],
  };
}

const archivedThreads: ArchivedSessionView[] = empty ? [] : [
  { id: "a1", title: "优化设置页的信息结构", updated_at: date, archive_project_id: "wuu", archive_project_name: "wuu" },
  { id: "a2", title: long ? "一个非常长的归档会话标题，用来检查省略号、时间与恢复按钮之间是否留出足够的阅读空间" : "修复侧栏折叠动画", updated_at: "2026-09-12T10:00:00Z", archive_project_id: "wuu", archive_project_name: "wuu" },
  { id: "a3", title: "整理发布说明", updated_at: "2026-08-30T10:00:00Z" },
];

const initialized: InitializeResult = {
  status: "ready", protocol_version: "1", workspace_root: "/preview",
  core: { version: "2026.9.25" },
  provider: providers[0]?.name ?? "", model: providers[0]?.model ?? "",
  providers,
  advanced_settings: { max_steps: 0, max_context_tokens: 0, temperature: 0, disable_auto_compact: false, context_window_tokens: 200_000, context_window_source: "provider_model_limit" },
  general_settings: {
    git_attribution_enabled: true,
    mcp_server_enabled: Object.fromEntries(mcpServers.map((server) => [server.name, true])),
    execution_environments: empty ? {} : {
      default: "isolated",
      profiles: {
        isolated: { backend: "docker", image: "wuu-execution:local", workspace: "/workspace", network: "none", cpus: 2, memory_mb: 2048 },
        [long ? "remote-build-machine-with-an-unusually-long-profile-name" : "build-box"]: { backend: "ssh", host: "build.example.test", workspace: "/srv/wuu" },
      },
    },
  },
};

window.wuu = {
  initialLanguagePreference: params.get("lang") === "en" ? "en-US" : "zh-CN",
  initialThemePreference: params.get("theme") === "dark" ? "dark" : "light",
  getThemePreference: async () => (params.get("theme") === "dark" ? "dark" : "light"),
  setThemePreference: async () => undefined,
  onThemePreferenceChange: () => () => undefined,
  getMessageFlowFontSize: async () => Number(params.get("size")) || 14.5,
  setMessageFlowFontSize: async () => undefined,
  getBuildInfo: async () => ({ core: initialized.core, desktop: { version: "2026.9.25", date: "2026-09-25T08:00:00Z" } }),
  listMCPServers: async () => ({ servers: structuredClone(mcpServers) }),
  connectMCPServer: async (name: string) => ({ status: { ...mcpServers.find((server) => server.name === name)!, state: "connected", connected: true } }),
  disconnectMCPServer: async (name: string) => ({ status: { ...mcpServers.find((server) => server.name === name)!, state: "configured", connected: false } }),
  refreshMCPServer: async (name: string) => ({ status: mcpServers.find((server) => server.name === name)! }),
  listEngines: async () => structuredClone(engines),
  listCatalogProviders: async (provider?: string) => ({
    providers: provider
      ? catalogProviders.filter((item) => item.id === provider).map((item) => ({
        ...item,
        models: [item.default_model, `${item.default_model}-fast`, `${item.default_model}-mini`].map((id, index) => ({ id, tool_call: true, context_window: 1_000_000 / (index + 1) })),
      }))
      : catalogProviders,
  }),
  listEngineAuthMethods: async () => ({ methods: [], authenticated: true }),
  openExternal: async () => undefined,
  getRemoteControlSnapshot: async () => ({ status: { fingerprint: "AB12-CD34", store: "", devices: [] }, host_running: false }),
  onRemoteControlEvent: () => () => undefined,
  unsupportedMethods: [],
} as unknown as WuuDesktopApi;

startFocusModality();

function Fixture(): JSX.Element {
  const shellRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState(initialized);
  const [collapsed, setCollapsed] = useState(params.has("collapsed"));
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = params.get("theme") || "light";
    document.documentElement.dataset.platform = "darwin";
    applyMessageFlowFontSize(Number(params.get("size")) || 14.5);
  }, []);
  // Mirrors the core's settings contract closely enough to review flows:
  // keep_selection edits a service without choosing it.
  const save = async (provider: string, model: string, _effort?: string, connection?: RuntimeConnectionUpdate, variant?: string) => {
    setState((current) => {
      const keep = connection?.keep_selection === true;
      let list = current.providers ?? [];
      if (connection?.create_provider) {
        const entry = catalogProviders.find((item) => item.base_url === connection.base_url);
        list = [...list, {
          name: provider, type: connection.type ?? "openai-compatible", model, base_url: connection.base_url,
          api_key_configured: Boolean(connection.api_key) || connection.type === "grok-build" || connection.type === "xai-subscription",
          connection_locked: connection.type === "grok-build" || connection.type === "xai-subscription",
          catalog_id: entry?.id, catalog_name: entry?.name, models: [{ id: model }],
        }];
      }
      list = list.map((item) => {
        if (item.name !== provider) return item;
        let models = item.models ?? [];
        let hidden = item.hidden_models ?? [];
        if (connection?.remove_model) {
          models = models.filter((entry) => entry.id !== connection.remove_model);
          hidden = [...hidden, connection.remove_model].sort();
        }
        if (connection?.add_model && !models.some((entry) => entry.id === connection.add_model)) {
          models = [...models, { id: connection.add_model }];
          hidden = hidden.filter((entry) => entry !== connection.add_model);
        }
        return {
          ...item, model, models, hidden_models: hidden,
          ...(connection?.base_url ? { base_url: connection.base_url } : {}),
          ...(connection?.api_key ? { api_key_configured: true } : {}),
        };
      });
      return {
        ...current,
        providers: list,
        ...(keep ? {} : { provider, model, variant: variant ?? current.variant }),
      };
    });
  };
  return (
    <div style={{ height: "100vh" }}>
      <SettingsView
        initialized={state}
        initialPage={(params.get("page") as SettingsPage | null) ?? undefined}
        running={false}
        usage={empty ? undefined : sampleUsage()}
        engineInventory={engines}
        codexPets={pets}
        codexPetsLoading={false}
        codexPetsError=""
        sidebarWidth={Number(params.get("rail")) || 260}
        sidebarMinWidth={200}
        sidebarMaxWidth={420}
        resizingSidebar={false}
        shellRef={shellRef}
        onBack={() => undefined}
        onSave={save}
        onRemoveProvider={async (name) => setState((current) => ({ ...current, providers: current.providers?.filter((item) => item.name !== name) }))}
        onRefreshModelCatalog={async () => undefined}
        onRefreshEngineInventory={async () => engines}
        onUpdateEngineInventory={async () => engines}
        onAdvancedSave={async () => undefined}
        onGeneralSave={async () => undefined}
        onCodexPetsRefresh={async () => pets}
        onCodexPetsUpdate={async () => pets}
        onSidebarResizeStart={() => undefined}
        onSidebarSeparatorKey={() => undefined}
        archivedThreads={archivedThreads}
        onUnarchiveThread={() => undefined}
        sidebarCollapsed={collapsed}
        sidebarAnimating={false}
        onToggleSidebar={() => setCollapsed((value) => !value)}
      />
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
