import { useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type {
  CodexPetsSnapshot,
  EngineListResult,
  InitializeResult,
  MCPServerStatus,
  ProviderSummary,
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

const providers: ProviderSummary[] = empty ? [] : [
  {
    name: "anthropic", type: "anthropic", model: "claude-sonnet-5", base_url: "https://api.anthropic.com/v1", api_key_configured: true,
    models: ["claude-sonnet-5", "claude-opus-5-5", "claude-haiku-4-5"].map((id) => ({ id, supported_efforts: ["low", "medium", "high"] })),
  },
  {
    name: "openrouter", type: "openai-compatible", model: long ? "provider/an-exceptionally-long-model-identifier-for-truncation-review" : "deepseek/deepseek-v4",
    base_url: "https://openrouter.ai/api/v1", api_key_configured: false,
    models: [{ id: "deepseek/deepseek-v4" }, { id: "qwen/qwen3-coder" }],
  },
  { name: "local", type: "openai-compatible", model: "qwen3-32b", base_url: "http://localhost:11434/v1", api_key_configured: true },
];

const engines: EngineListResult = {
  settings: { default_engine: "wuu" },
  engines: empty ? [] : [
    { id: "codex", display_name: "Codex", enabled: true, binary_ok: true, binary_path: "/usr/local/bin/codex" },
    { id: "claude", display_name: "Claude Code", enabled: true, binary_ok: true, binary_path: "/usr/local/bin/claude" },
    { id: "cursor", display_name: "Cursor", protocol: "acp", enabled: true, binary_ok: false, error: "cursor-agent not found in PATH", install_url: "https://cursor.com/cli" },
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
  general_settings: { git_attribution_enabled: true, mcp_server_enabled: Object.fromEntries(mcpServers.map((server) => [server.name, true])) },
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
  const save = async (provider: string, model: string) => {
    setState((current) => ({
      ...current,
      provider,
      model,
      providers: current.providers?.map((item) => (item.name === provider ? { ...item, model } : item)),
    }));
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
        onRemoveProvider={async () => undefined}
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
        archivedRooms={[]}
        onUnarchiveThread={() => undefined}
        sidebarCollapsed={collapsed}
        sidebarAnimating={false}
        onToggleSidebar={() => setCollapsed((value) => !value)}
      />
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><Fixture /></WuuUIRoot></I18nProvider>);
