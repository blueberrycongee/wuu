import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  SettingsView,
  type ArchivedSessionView,
  type SettingsPage,
} from "./SettingsView";
import type {
  BuildInfoResult,
  CodexPetsSnapshot,
  EngineListResult,
  EngineUpdateParams,
  InitializeResult,
  RuntimeAdvancedSettingsUpdate,
  CodexPetSettingsUpdate,
  RuntimeConnectionUpdate,
  RuntimeGeneralSettingsUpdate,
  SettingsUsageResponse,
  WuuDesktopApi
} from "../shared/protocol";
import { I18nProvider } from "./i18n";
import type { PageHistoryControlsProps } from "./PageHistoryControls";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";

type GlobalWindow = typeof window & { wuu: WuuDesktopApi };

let container: HTMLDivElement;
let root: Root | null = null;

function noopResizeStart(): void {}
function noopResizeKey(): void {}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  unhoverTooltip();
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  // Drop the stub so each test installs its own.
  delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  delete (window as { wuu?: WuuDesktopApi }).wuu;
  Reflect.deleteProperty(document, "elementFromPoint");
  vi.restoreAllMocks();
  vi.useRealTimers();
});

function installBuildInfoStub(info: BuildInfoResult): void {
  const stub: Partial<WuuDesktopApi> = {
    listEngines: vi.fn().mockResolvedValue({ engines: [] }),
    getBuildInfo: vi.fn().mockResolvedValue(info),
    listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
    connectMCPServer: vi.fn(),
    disconnectMCPServer: vi.fn(),
    refreshMCPServer: vi.fn(),
    startMCPAuth: vi.fn(),
    getMCPAuthStatus: vi.fn(),
    finishMCPAuth: vi.fn(),
    removeMCPAuth: vi.fn(),
    startXAILogin: vi.fn(),
    pollXAILogin: vi.fn(),
    cancelXAILogin: vi.fn(),
    openExternal: vi.fn(),
    listCodexPets: vi.fn().mockResolvedValue(emptyCodexPetsSnapshot()),
    updateCodexPetSettings: vi.fn().mockResolvedValue(emptyCodexPetsSnapshot()),
  };
  (globalThis as { wuu?: WuuDesktopApi }).wuu = stub as WuuDesktopApi;
  (window as unknown as GlobalWindow).wuu = stub as WuuDesktopApi;
}

function setInputValue(input: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  const prototype = input instanceof HTMLTextAreaElement
    ? HTMLTextAreaElement.prototype
    : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

function baseInitialized(overrides: Partial<InitializeResult> = {}): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: "/tmp/project",
    ...overrides,
  };
}

function emptyCodexPetsSnapshot(overrides: Partial<CodexPetsSnapshot> = {}): CodexPetsSnapshot {
  return {
    home: "/Users/test/.wuu/pets",
    enabled: false,
    selected_id: "",
    pets: [],
    errors: [],
    ...overrides,
  };
}

// Protect the user-facing save boundary: changing one role preserves the other
// and cannot change the lead conversation selection.
it("saves technical lead selection without changing executor or coordinator", async () => {
  installBuildInfoStub({ core: {}, desktop: {} } as BuildInfoResult);
  const onAdvancedSave = vi.fn().mockResolvedValue(undefined);
  renderSettings({
    initialized: baseInitialized({
      features: { project_agent: true },
      project_models: { technical_lead: { provider: "fake", model: "side-model", effort: "high" }, executor: { provider: "fake", model: "worker-model" } },
      providers: [{ name: "fake", type: "openai-compatible", model: "fake-model", models: [{ id: "side-model" }, { id: "next-side" }, { id: "worker-model" }] }],
    }),
    initialPage: "advanced", onAdvancedSave, locale: "en-US",
  });
  await act(async () => { container.querySelector<HTMLButtonElement>('[data-testid="project-technical_lead-model"]')?.click(); });
  const option = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')).find((item) => item.textContent?.includes("side-model"));
  expect(option).toBeDefined();
  await act(async () => { option?.click(); });
  expect(onAdvancedSave).not.toHaveBeenCalled();
  await act(async () => { container.querySelector<HTMLButtonElement>('[data-testid="project-technical_lead-model"]')?.click(); });
  const replacement = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')).find((item) => item.textContent?.includes("next-side"));
  expect(replacement).toBeDefined();
  await act(async () => { replacement?.click(); });
  expect(onAdvancedSave).toHaveBeenCalledWith({ project_models: { technical_lead: { provider: "fake", model: "next-side" }, executor: { provider: "fake", model: "worker-model" } } });
});

function readyEngineInventory(): EngineListResult {
  return {
    engines: [
      { id: "wuu", enabled: true, binary_ok: true },
      { id: "codex", enabled: true, binary_ok: true },
      { id: "claude", enabled: true, binary_ok: true },
    ],
    settings: { default_engine: "wuu" },
  };
}

function renderSettings(props: {
  initialized: InitializeResult | undefined;
  running?: boolean;
  usage?: SettingsUsageResponse;
  usageLoading?: boolean;
  usageError?: string;
  engineInventory?: EngineListResult;
  engineInventoryError?: string;
  onRefreshEngineInventory?: () => Promise<EngineListResult | undefined>;
  onUpdateEngineInventory?: (params: EngineUpdateParams) => Promise<EngineListResult>;
  initialPage?: SettingsPage;
  page?: SettingsPage;
  onPageChange?: (page: SettingsPage) => void;
  pageHistory?: Omit<PageHistoryControlsProps, "children">;
  runningProviderNames?: string[];
  codexPets?: CodexPetsSnapshot;
  codexPetsLoading?: boolean;
  codexPetsError?: string;
  onCodexPetsUpdate?: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
  onSave?: (provider: string, model: string, effort?: string, connection?: RuntimeConnectionUpdate, variant?: string) => Promise<void>;
  onRemoveProvider?: (provider: string) => Promise<void>;
  onRefreshModelCatalog?: () => Promise<void>;
  onAdvancedSave?: (settings: RuntimeAdvancedSettingsUpdate) => Promise<void>;
  onGeneralSave?: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onToggleSidebar?: () => void;
  sidebarCollapsed?: boolean;
  // ArchivedSessionView 是结构子集（id/title?/updated_at），这里
  // 直接传对象字面量，避免引入 ThreadSummary（它要求 turns/turn_count
  // 等计算字段，测试场景下冗余）。
  archivedThreads?: readonly ArchivedSessionView[];
  onUnarchiveThread?: (thread: ArchivedSessionView) => void;
  locale?: "zh-CN" | "en-US";
}): { about: Element | null; text: () => string; rootText: () => string } {
  if (props.locale) {
    window.wuu.initialLanguagePreference = props.locale;
    window.wuu.initialSystemLocale = props.locale;
  }
  const view = (
    <SettingsView
        initialized={props.initialized}
        initialPage={props.initialPage ?? "general"}
        page={props.page}
        onPageChange={props.onPageChange}
        pageHistory={props.pageHistory ?? { canGoBack: false, canGoForward: false, onBack: () => {}, onForward: () => {} }}
        running={props.running ?? false}
        usage={props.usage}
        usageLoading={props.usageLoading}
        usageError={props.usageError}
        engineInventory={props.engineInventory ?? readyEngineInventory()}
        engineInventoryError={props.engineInventoryError}
        onRefreshEngineInventory={props.onRefreshEngineInventory ?? (async () => readyEngineInventory())}
        onUpdateEngineInventory={props.onUpdateEngineInventory ?? (async () => readyEngineInventory())}
        runningProviderNames={props.runningProviderNames}
        codexPets={props.codexPets ?? emptyCodexPetsSnapshot()}
        codexPetsLoading={props.codexPetsLoading ?? false}
        codexPetsError={props.codexPetsError ?? ""}
        onCodexPetsRefresh={async () => props.codexPets ?? emptyCodexPetsSnapshot()}
        onCodexPetsUpdate={props.onCodexPetsUpdate ?? (async () => props.codexPets ?? emptyCodexPetsSnapshot())}
        sidebarWidth={320}
        sidebarMinWidth={240}
        sidebarMaxWidth={480}
        resizingSidebar={false}
        // Mirror the App-level collapse/hover wiring so existing render
        // assertions still produce a sensible non-collapsed shell by default.
        sidebarCollapsed={props.sidebarCollapsed ?? false}
        sidebarAnimating={false}
        onToggleSidebar={props.onToggleSidebar ?? (() => {})}
        onBack={() => {}}
        onSave={props.onSave ?? (async () => {})}
        onRemoveProvider={props.onRemoveProvider ?? (async () => {})}
        onRefreshModelCatalog={props.onRefreshModelCatalog ?? (async () => {})}
        onAdvancedSave={props.onAdvancedSave ?? (async () => {})}
        onGeneralSave={props.onGeneralSave ?? (async () => {})}
        onSidebarResizeStart={noopResizeStart}
        onSidebarSeparatorKey={noopResizeKey}
        archivedThreads={props.archivedThreads ?? []}
        onUnarchiveThread={props.onUnarchiveThread ?? (() => {})}
    />
  );
  act(() => {
    root ??= createRoot(container);
    root!.render(props.locale ? <I18nProvider>{view}</I18nProvider> : view);
  });
  const about = container.querySelector("[data-testid=\"settings-about\"]");
  return {
    about,
    text: () => about?.textContent ?? "",
    rootText: () => container.textContent ?? "",
  };
}

describe("SettingsView shell", () => {
  it("reports settings navigation once and restores a history page without recording it again", () => {
    installBuildInfoStub({ core: {}, desktop: {} } as BuildInfoResult);
    const onPageChange = vi.fn();
    const props = { initialized: baseInitialized(), page: "general" as const, onPageChange };
    renderSettings(props);
    const appearance = container.querySelector<HTMLButtonElement>('[data-wuu-page="appearance"]');
    expect(appearance).not.toBeNull();
    act(() => { appearance?.click(); });
    expect(onPageChange).toHaveBeenCalledExactlyOnceWith("appearance");
    renderSettings({ ...props, page: "appearance" });
    expect(container.querySelector('.settings-page')?.getAttribute('data-wuu-page')).toBe("appearance");
    act(() => { appearance?.click(); });
    expect(onPageChange).toHaveBeenCalledTimes(1);

    renderSettings({ ...props, page: "usage" });
    expect(container.querySelector('.settings-page')?.getAttribute('data-wuu-page')).toBe("usage");
    expect(onPageChange).toHaveBeenCalledTimes(1);
  });

  it("keeps page history controls reachable across settings pages and sidebar collapse", () => {
    installBuildInfoStub({ core: {}, desktop: {} } as BuildInfoResult);
    const onBack = vi.fn();
    const onForward = vi.fn();
    const props = {
      initialized: baseInitialized(),
      pageHistory: { canGoBack: false, canGoForward: true, onBack, onForward },
    };
    renderSettings(props);
    const back = container.querySelector<HTMLButtonElement>('[data-wuu-component="page-history-back"]');
    const forward = container.querySelector<HTMLButtonElement>('[data-wuu-component="page-history-forward"]');
    expect(back?.disabled).toBe(true);
    expect(forward?.disabled).toBe(false);
    act(() => { back?.click(); forward?.click(); });
    expect(onBack).not.toHaveBeenCalled();
    expect(onForward).toHaveBeenCalledTimes(1);

    renderSettings({ ...props, initialPage: "appearance", sidebarCollapsed: true, pageHistory: { ...props.pageHistory, canGoBack: true, canGoForward: false } });
    expect(container.querySelector('[data-wuu-component="page-history-back"]')).toBe(back);
    expect(container.querySelector('[data-wuu-component="page-history-forward"]')).toBe(forward);
    expect(back?.closest('[inert]')).toBeNull();
    expect(back?.disabled).toBe(false);
    expect(forward?.disabled).toBe(true);
    act(() => { back?.click(); forward?.click(); });
    expect(onBack).toHaveBeenCalledTimes(1);
    expect(onForward).toHaveBeenCalledTimes(1);
  });

  it("omits native settings and does not request desktop build info on a browser host", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "1970-01-01" } });
    window.wuu.unsupportedMethods = ["getBuildInfo", "listCodexPets", "getRemoteControlSnapshot"];
    renderSettings({ initialized: baseInitialized(), initialPage: "general" });
    await act(async () => { await Promise.resolve(); });
    expect(window.wuu.getBuildInfo).not.toHaveBeenCalled();
    expect(container.querySelector('[data-testid="settings-general"]')).not.toBeNull();
    const appearanceButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".settings-nav-item"),
    ).find((button) => button.textContent?.includes("外观"));
    act(() => {
      appearanceButton?.click();
    });
    expect(container.querySelector('[data-testid="settings-appearance"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="settings-pet"]')).toBeNull();
  });

  it("hides phone access without development opt-in", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "today" } });
    window.wuu.getRemoteControlSnapshot = vi.fn().mockResolvedValue({ status: null, host_running: false, pair_uri: null });
    window.wuu.onRemoteControlEvent = vi.fn(() => () => {});
    renderSettings({ initialized: baseInitialized(), initialPage: "remote" });
    await act(async () => { await Promise.resolve(); });
    expect(container.querySelector('[data-testid="settings-remote-page"]')).toBeNull();
    expect(window.wuu.getRemoteControlSnapshot).not.toHaveBeenCalled();
  });

  it.each([false, true])("exposes the sidebar state and invokes the toggle action (collapsed=%s)", (sidebarCollapsed) => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onToggleSidebar = vi.fn();
    renderSettings({ initialized: baseInitialized(), onToggleSidebar, sidebarCollapsed });

    const toggle = container.querySelector<HTMLButtonElement>(
      ".settings-sidebar-toggle",
    );
    expect(toggle).not.toBeNull();
    expect(container.querySelectorAll(".settings-sidebar-toggle")).toHaveLength(1);
    expect(toggle?.getAttribute("aria-pressed")).toBe(String(!sidebarCollapsed));

    act(() => {
      toggle?.click();
    });
    expect(onToggleSidebar).toHaveBeenCalledTimes(1);
  });

  it("does not reveal navigation when the independent window toggle is hovered", () => {
    vi.useFakeTimers();
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "today" } });
    renderSettings({ initialized: baseInitialized(), sidebarCollapsed: true });
    const toggle = container.querySelector<HTMLElement>(".settings-sidebar-toggle");
    expect(toggle).not.toBeNull();
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: vi.fn(() => toggle) });
    act(() => {
      toggle?.dispatchEvent(new MouseEvent("pointerover", { bubbles: true }));
      vi.advanceTimersByTime(480);
    });
    expect(container.querySelector(".sidebar-drawer-open")).toBeNull();
  });

  it("reveals navigation at the lower edge and closes it when the pointer enters blank window chrome", () => {
    vi.useFakeTimers();
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "today" } });
    renderSettings({ initialized: baseInitialized(), sidebarCollapsed: true });
    const shell = container.querySelector<HTMLElement>(".settings-shell");
    const edge = container.querySelector<HTMLElement>(".sidebar-hover-zone");
    const chrome = container.querySelector<HTMLElement>('[data-wuu-component="window-chrome"]');
    expect(edge).not.toBeNull();
    expect(chrome).not.toBeNull();
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: vi.fn(() => edge) });
    act(() => {
      edge?.dispatchEvent(new MouseEvent("pointerover", { bubbles: true, clientX: 1, clientY: 80 }));
      vi.advanceTimersByTime(240);
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: vi.fn(() => chrome) });
    act(() => {
      window.dispatchEvent(new MouseEvent("pointermove", { bubbles: true, clientX: 600, clientY: 24 }));
      vi.advanceTimersByTime(1);
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(shell?.classList.contains("sidebar-drawer-closing")).toBe(true);
  });

  it("starts each settings page at the top and replaces the content surface", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({ initialized: baseInitialized(), initialPage: "providers" });

    const scroll = container.querySelector<HTMLElement>(".settings-scroll")!;
    const providersPage = container.querySelector<HTMLElement>(".settings-page")!;
    scroll.scrollTop = 420;
    const runtimeButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".settings-nav-item"),
    ).find((button) => button.textContent?.includes("内置 Agent"));

    act(() => {
      runtimeButton?.click();
    });

    expect(scroll.scrollTop).toBe(0);
    expect(container.querySelector(".settings-page")).not.toBe(providersPage);
    expect(container.querySelector(".settings-page-title")?.textContent).toBe("内置 Agent");
  });
});

function servicesInitialized(): InitializeResult {
  return baseInitialized({
    provider: "openai-codex",
    model: "gpt-6-astra",
    variant: "low",
    providers: [
      {
        name: "openai-codex", type: "openai-codex", model: "gpt-6-astra", base_url: "https://chatgpt.com/backend-api/codex",
        api_key_configured: true, connection_locked: true,
        models: [{ id: "gpt-6-astra", display_name: "GPT-6 Astra", supported_efforts: ["low", "high"] }, { id: "gpt-6-sol", display_name: "GPT-6 Sol" }],
      },
      {
        name: "deepseek", type: "openai-compatible", model: "deepseek-v4-pro", base_url: "https://api.deepseek.com",
        api_key_configured: true, catalog_id: "deepseek", catalog_name: "DeepSeek", hidden_models: ["deepseek-old"],
        models: [{ id: "deepseek-v4-pro", display_name: "DeepSeek V4 Pro" }, { id: "deepseek-v4-flash", display_name: "DeepSeek V4 Flash" }],
      },
      {
        name: "openrouter", type: "openai-compatible", model: "qwen/qwen3-coder", base_url: "https://openrouter.ai/api/v1",
        api_key_configured: false, catalog_id: "openrouter", catalog_name: "OpenRouter", models: [{ id: "qwen/qwen3-coder" }],
      },
    ],
  });
}

const catalogProviders = [
  { id: "moonshotai-cn", name: "Moonshot AI (China)", type: "openai-compatible", base_url: "https://api.moonshot.cn/v1", model_count: 2, default_model: "kimi-k3" },
  { id: "deepseek", name: "DeepSeek", type: "openai-compatible", base_url: "https://api.deepseek.com", model_count: 4, default_model: "deepseek-v4-pro" },
];

function installServicesStub(): WuuDesktopApi {
  installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "today" } });
  const listCatalogProviders = vi.fn(async (provider?: string) => ({
    providers: provider
      ? catalogProviders.filter((item) => item.id === provider).map((item) => ({ ...item, models: [{ id: "kimi-k3", name: "Kimi K3", tool_call: true }, { id: "kimi-k2.7-code", name: "Kimi K2.7 Code", tool_call: true }] }))
      : catalogProviders,
  }));
  Object.assign(window.wuu, { listCatalogProviders });
  return window.wuu;
}

async function flush(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function click(element: Element | null | undefined): void {
  if (!element) throw new Error("missing element");
  act(() => {
    (element as HTMLElement).click();
  });
}

async function openProviderCatalog(): Promise<void> {
  click(document.querySelector('[data-testid="settings-connection-add"]'));
  await flush();
}

describe("SettingsView model services", () => {
  it.each([true, false])("offers one ChatGPT entry and switches methods without auth or mutation, existing local=%s", async (existingLocal) => {
    await act(async () => { await import("./ChatGPTConnectionDialog"); await import("./ChatGPTAccountRows"); });
    const api = installServicesStub();
    api.listChatGPTAccounts = vi.fn().mockResolvedValue({ accounts: [] });
    api.startChatGPTLogin = vi.fn();
    api.useCodexCredentials = vi.fn();
    const onSave = vi.fn();
    const initialized = servicesInitialized();
    Object.assign(initialized.providers![0], { reuse_codex_credentials: true, codex_credential_source: "codex-cli" });
    if (!existingLocal) initialized.providers = initialized.providers?.filter(provider => provider.type !== "openai-codex");
    const original = structuredClone(initialized);
    renderSettings({ initialized, initialPage: "providers", onSave });
    await flush();
    await openProviderCatalog();
    expect(document.querySelectorAll('[data-connection="chatgpt"]')).toHaveLength(1);
    expect(document.querySelector('[data-subscription="openai-codex"]')).toBeNull();
    click(document.querySelector('[data-connection="chatgpt"]'));
    await flush();
    expect(document.querySelector('[data-testid="settings-auth-method-login"]')?.getAttribute("aria-pressed")).toBe("true");
    click(document.querySelector('[data-testid="settings-auth-method-local"]'));
    await flush();
    expect(document.querySelector('[data-testid="settings-auth-method-local"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(api.startChatGPTLogin).not.toHaveBeenCalled();
    expect(api.useCodexCredentials).not.toHaveBeenCalled();
    expect(onSave).not.toHaveBeenCalled();
    click(document.querySelector('[data-testid="settings-auth-method-login"]'));
    await flush();
    expect(document.querySelector('[data-testid="chatgpt-login-start"]')).not.toBeNull();
    expect(api.startChatGPTLogin).not.toHaveBeenCalled();
    expect(onSave).not.toHaveBeenCalled();
    expect(initialized).toEqual(original);
  });

  it("returns from an alternative account method form to its original bound connection without saving", async () => {
    await act(async () => { await import("./ChatGPTConnectionDialog"); await import("./ChatGPTAccountRows"); });
    const api = installServicesStub();
    api.listChatGPTAccounts = vi.fn().mockResolvedValue({ accounts: [{ id: "official-account", status: "connected" }] });
    api.listChatGPTModels = vi.fn().mockResolvedValue({ models: [{ id: "official-model", name: "Official model" }] });
    api.startChatGPTLogin = vi.fn();
    api.useCodexCredentials = vi.fn();
    const initialized = servicesInitialized();
    initialized.provider = "official";
    initialized.model = "official-model";
    initialized.providers = [{ name: "official", type: "chatgpt", model: "official-model", chatgpt_account_id: "official-account", auth_status: "connected", api_key_configured: true }];
    const original = structuredClone(initialized);
    const onSave = vi.fn();
    renderSettings({ initialized, initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="official"]'));
    await flush();
    click(document.querySelector('[data-testid="settings-auth-method-local"]'));
    await flush();
    expect(document.querySelector('[data-testid="settings-auth-method-local"]')?.getAttribute("aria-pressed")).toBe("true");
    click(document.querySelector('.model-connect-dialog [data-testid="settings-connection-back"]'));
    await flush();
    expect(document.querySelector('[data-testid="settings-provider-back"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="settings-auth-method-login"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(document.querySelector('[data-testid="settings-provider-model"][data-model="official-model"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(api.startChatGPTLogin).not.toHaveBeenCalled();
    expect(api.useCodexCredentials).not.toHaveBeenCalled();
    expect(onSave).not.toHaveBeenCalled();
    expect(initialized).toEqual(original);
  });

  it("locks account method navigation during explicit account login and unlocks after cancel", async () => {
    await act(async () => { await import("./ChatGPTAccountRows"); });
    const api = installServicesStub();
    api.listChatGPTAccounts = vi.fn().mockResolvedValue({ accounts: [{ id: "official-account", status: "reauth_required" }] });
    api.startChatGPTLogin = vi.fn().mockResolvedValue({ login_id: "method-login", interval_ms: 60000 });
    api.cancelChatGPTLogin = vi.fn().mockResolvedValue(undefined);
    const initialized = servicesInitialized();
    initialized.providers!.push({ name: "official", type: "chatgpt", model: "official-model", chatgpt_account_id: "official-account", auth_status: "reauth_required" });
    const onSave = vi.fn();
    renderSettings({ initialized, initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="official"]'));
    await flush();
    click(document.querySelector('[data-testid="chatgpt-login-start"]'));
    await flush();
    expect(api.startChatGPTLogin).toHaveBeenCalledExactlyOnceWith("official-account", false);
    const local = document.querySelector<HTMLButtonElement>('[data-testid="settings-auth-method-local"]')!;
    expect(local.disabled).toBe(true);
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-back"]')!.disabled).toBe(true);
    click(local);
    expect(document.querySelector('[data-testid="chatgpt-login-cancel"]')).not.toBeNull();
    click(document.querySelector('[data-testid="chatgpt-login-cancel"]'));
    await flush();
    expect(api.cancelChatGPTLogin).toHaveBeenCalledExactlyOnceWith("method-login");
    expect(local.disabled).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-back"]')!.disabled).toBe(false);
    expect(onSave).not.toHaveBeenCalled();
  });

  it("finds consolidated engine and subscription controls through settings search", async () => {
    installServicesStub();
    renderSettings({ initialized: servicesInitialized(), initialPage: "general", locale: "en-US" });
    await flush();
    const search = container.querySelector<HTMLInputElement>('.settings-search input')!;
    for (const query of ["codex", "quota"]) {
      act(() => setInputValue(search, query));
      const connection = container.querySelector('.settings-nav [data-wuu-page="providers"]');
      expect(connection).not.toBeNull();
      expect(container.querySelector('.settings-nav [data-wuu-page="agents"], .settings-nav [data-wuu-page="subscriptions"]')).toBeNull();
      click(connection);
      expect(container.querySelector('.settings-page')?.getAttribute("data-wuu-page")).toBe("providers");
    }
  });

  it("keeps connection actions available while the catalog loads and reports failure", async () => {
    const api = installServicesStub();
    let rejectCatalog!: (reason: Error) => void;
    api.listCatalogProviders = vi.fn(() => new Promise<Awaited<ReturnType<WuuDesktopApi["listCatalogProviders"]>>>((_resolve, reject) => { rejectCatalog = reject; }));
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers" });
    await flush();

    await openProviderCatalog();
    click(document.querySelector('[data-testid="settings-provider-custom"]'));
    expect(document.querySelector('[data-testid="settings-provider-connect-base-url"]')).not.toBeNull();

    await act(async () => { rejectCatalog(new Error("catalog unavailable")); });
    click(document.querySelector('.model-connect-dialog [data-testid="settings-connection-back"]'));
    expect(document.querySelector('[data-testid="settings-provider-custom"]')).not.toBeNull();
  });

  it("returns from connection configuration to the same catalog search without saving", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    await openProviderCatalog();
    const search = document.querySelector<HTMLInputElement>('.model-browse-dialog input[type="search"]')!;
    act(() => setInputValue(search, "deepseek"));
    click(document.querySelector('[data-catalog="deepseek"]'));
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.querySelector('[data-testid="settings-provider-card"]')).toBeNull();
    click(document.querySelector('.model-connect-dialog [data-testid="settings-connection-back"]'));
    expect(document.querySelector<HTMLInputElement>('.model-browse-dialog input[type="search"]')?.value).toBe("deepseek");
    expect(document.querySelector('[data-catalog="moonshotai-cn"]')).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("gathers conversation defaults on General", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "general", locale: "en-US", onSave });
    await flush();

    const group = document.querySelector('[data-testid="settings-new-conversations"]');
    expect(group?.querySelector('[data-testid="settings-default-engine"]')).not.toBeNull();
    expect(group?.querySelector('[data-testid="settings-default-model-select"]')?.textContent).toContain("GPT-6 Astra");
    expect(group?.textContent).toContain("ChatGPT");
    expect(group?.querySelector('[aria-label="Default for new conversations"]')).not.toBeNull();
  });

  it("sends General to Model providers when no service is connected", async () => {
    installServicesStub();
    renderSettings({ initialized: baseInitialized({ provider: "", model: "", providers: [] }), initialPage: "general" });
    await flush();

    click(document.querySelector('[data-testid="settings-providers-empty"]'));
    await flush();
    await openProviderCatalog();
    expect(document.querySelector('[data-testid="settings-provider-custom"]')).not.toBeNull();
  });

  it("names connected services by provider identity", async () => {
    installServicesStub();
    const initialized = servicesInitialized();
    const beforeBrowsing = structuredClone(initialized);
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized, initialPage: "providers", locale: "en-US", onSave });
    await flush();

    const cards = [...document.querySelectorAll('[data-testid="settings-provider-card"]')];
    expect(cards.map((card) => card.getAttribute("aria-label"))).toEqual(["ChatGPT", "DeepSeek", "OpenRouter"]);
    expect(cards[0]?.textContent).toContain("Ready");
    expect(cards[2]?.textContent).toContain("API key missing");
    expect(document.querySelector('[data-catalog="deepseek"]')).toBeNull();
    await openProviderCatalog();
    // Connected vendors stay discoverable and can add a separate connection.
    const connectedTile = document.querySelector<HTMLButtonElement>('[data-catalog="deepseek"]');
    expect(connectedTile?.querySelector('[role="img"][data-ready]')?.getAttribute("aria-label")).toBeTruthy();
    expect(connectedTile?.disabled).toBe(false);
    expect(document.querySelector('[data-testid="settings-provider-custom"]')).not.toBeNull();

    click(connectedTile);
    await flush();
    const dialog = document.querySelector('.model-connect-dialog')!;
    const name = dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-name"]')!.value;
    expect(name).not.toBe("");
    expect(initialized.providers?.map((provider) => provider.name)).not.toContain(name);
    expect(dialog.querySelector('[data-testid="settings-provider-connect-default"]')?.getAttribute("aria-checked")).toBe("false");
    expect(onSave).not.toHaveBeenCalled();
    act(() => setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-key"]')!, "sk-another-connection"));
    click(document.querySelector('.model-connect-dialog [data-testid="settings-connection-back"]'));

    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
    expect(initialized).toEqual(beforeBrowsing);
  });

  it("loads one allowance snapshot without replacing live connection configuration", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    const inventory = readyEngineInventory();
    const observedAt = new Date().toISOString();
    const quota = { status: "available" as const, checked_at: observedAt,
      windows: [{ id: "short", window_minutes: 300, used_percent: 25 }] };
    vi.mocked(api.listEngines).mockResolvedValue({
      engines: [{ id: "codex", enabled: false, binary_ok: false, quota }],
      subscription_providers: [{ ...initialized.providers![0], api_key_configured: false, model: "obsolete-model", quota }],
    });
    renderSettings({ initialized, engineInventory: inventory, initialPage: "providers", locale: "en-US" });
    await flush();
    expect(api.listEngines).toHaveBeenCalledTimes(1);
    expect(api.listEngines).toHaveBeenCalledWith({ include_quota: true });
    const provider = document.querySelector('[data-provider="openai-codex"]')!;
    expect(provider.querySelector("meter")?.value).toBe(75);
    const engine = document.querySelector('[data-testid="settings-engine-codex-status"]')!;
    expect(engine.querySelector("meter")?.value).toBe(75);
    for (const row of [provider, engine]) {
      const quota = row.querySelector(".connection-usage-summary")!;
      expect(quota.id).toBeTruthy();
      expect(quota.hasAttribute("aria-label")).toBe(false);
      expect(row.getAttribute("aria-describedby")?.split(" ")).toContain(quota.id);
    }
    click(engine);
    await flush();
    expect(document.querySelector('[data-testid="settings-engine-codex-enabled"]')?.getAttribute("aria-checked")).toBe("true");
    expect(initialized.providers![0].api_key_configured).toBe(true);
    expect(initialized.providers![0].model).toBe("gpt-6-astra");
    expect(inventory.engines.find(item => item.id === "codex")?.enabled).toBe(true);
  });

  it("keeps a plain API account balance available when opening its connection details", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    const provider = { name: "balance-api", type: "openai", model: "model", api_key_configured: true, base_url: "https://api.deepseek.com" };
    initialized.providers = [provider];
    vi.mocked(api.listEngines).mockResolvedValue({ engines: [], subscription_providers: [{ ...provider,
      quota: { status: "available", checked_at: new Date().toISOString(), balances: [{ currency: "USD", amount: "12.3400" }] },
    }] });
    renderSettings({ initialized, initialPage: "providers", locale: "en-US" });
    await flush();
    click(document.querySelector('[data-provider="balance-api"]'));
    await flush();
    expect(document.querySelector('[data-testid="settings-subscriptions"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="subscription-builtin-balance-api"]')?.textContent).toContain("12.3400");
    expect(initialized.providers[0]).not.toHaveProperty("quota");
  });

  it("does not attach an allowance from a different configured engine executable", async () => {
    const api = installServicesStub();
    const inventory = readyEngineInventory();
    inventory.settings = { default_engine: "wuu", codex: { binary_path: "/new/codex" } };
    vi.mocked(api.listEngines).mockResolvedValue({
      engines: [{ id: "codex", enabled: true, binary_ok: true,
        quota: { status: "available", checked_at: new Date().toISOString(), windows: [{ id: "short", used_percent: 25 }] } }],
      settings: { default_engine: "wuu", codex: { binary_path: "/old/codex" } },
    });
    renderSettings({ initialized: servicesInitialized(), engineInventory: inventory, initialPage: "providers" });
    await flush();
    expect(document.querySelector('[data-testid="settings-engine-codex-status"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="settings-engine-codex-status"] meter')).toBeNull();
  });

  it.each([
    { type: "openai-compatible" },
    { base_url: "https://different.example.test/v1" },
    { chatgpt_account_id: "different-account" },
  ])("does not attach another provider identity's allowance (%j)", async changedIdentity => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    vi.mocked(api.listEngines).mockResolvedValue({
      engines: [], subscription_providers: [{ ...initialized.providers![0], ...changedIdentity,
        quota: { status: "available", checked_at: new Date().toISOString(), windows: [{ id: "short", used_percent: 25 }] },
      }],
    });
    renderSettings({ initialized, initialPage: "providers" });
    await flush();
    expect(document.querySelector('[data-provider="openai-codex"]')).not.toBeNull();
    expect(document.querySelector('[data-provider="openai-codex"] meter')).toBeNull();
  });

  it("does not show a previously available allowance for a disconnected account", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    const provider = { name: "disconnected", type: "chatgpt", model: "model", chatgpt_account_id: "account", auth_status: "reauth_required" as const };
    initialized.providers = [provider];
    vi.mocked(api.listEngines).mockResolvedValue({ engines: [], subscription_providers: [{ ...provider,
      quota: { status: "available", checked_at: new Date().toISOString(), windows: [{ id: "short", used_percent: 25 }] },
    }] });
    renderSettings({ initialized, initialPage: "providers" });
    await flush();
    const row = document.querySelector('[data-provider="disconnected"]')!;
    expect(row.querySelector(".connection-summary-status")?.hasAttribute("data-attention")).toBe(true);
    expect(row.querySelector(".connection-usage-summary")).toBeNull();
  });

  it("keeps unavailable quota out of the list without hiding connection status", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    vi.mocked(api.listEngines).mockResolvedValue({ engines: [], subscription_providers: [{
      ...initialized.providers![0], quota: { status: "unsupported", checked_at: new Date().toISOString() },
    }] });
    renderSettings({ initialized, engineInventory: readyEngineInventory(), initialPage: "providers" });
    await flush();
    const row = document.querySelector('[data-provider="openai-codex"]')!;
    expect(row.querySelector(".connection-usage-summary")).toBeNull();
    expect(row.querySelector(".connection-summary-status")?.textContent).toBeTruthy();
    expect(document.querySelector('[data-testid="settings-model-catalog-refresh"]')).toBeNull();
    expect(document.querySelector('[data-testid="settings-engine-refresh"]')).toBeNull();
  });

  it("shows one authentication warning instead of ready or default for a rejected quota login", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    vi.mocked(api.listEngines).mockResolvedValue({
      engines: [], subscription_providers: [{ ...initialized.providers![0],
        quota: { status: "sign_in", checked_at: new Date().toISOString() },
      }],
    });
    renderSettings({ initialized, initialPage: "providers", locale: "en-US" });
    await flush();
    const row = document.querySelector('[data-provider="openai-codex"]')!;
    const warnings = row.querySelectorAll('[data-attention]');
    expect(warnings).toHaveLength(1);
    const warning = warnings[0].textContent!;
    expect(warning.length).toBeGreaterThan(0);
    // The row has only its service name and one actionable authentication
    // state, rather than a default/ready badge or duplicate usage warning.
    expect(row.querySelector(".connection-summary-status")?.textContent).toBe(warning);
    expect(row.querySelector(".connection-usage-summary")).toBeNull();
    expect(row.querySelector("meter")).toBeNull();
  });

  it("keeps configured connections with broken authentication visible without opening the catalog", async () => {
    installServicesStub();
    const initialized = servicesInitialized();
    initialized.providers!.push({ name: "chatgpt-expired", type: "chatgpt", model: "gpt-6-astra", chatgpt_account_id: "expired-account", auth_status: "reauth_required" });
    renderSettings({ initialized, initialPage: "providers" });
    await flush();
    expect(document.querySelector('[data-provider="chatgpt-expired"]')).not.toBeNull();
    expect(document.querySelector('[data-provider="openrouter"]')).not.toBeNull();
    expect(document.querySelector('[data-catalog]')).toBeNull();
  });

  it("replaces the overview with configuration and returns keyboard focus on Back", async () => {
    installServicesStub();
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers" });
    await flush();
    const opener = document.querySelector<HTMLButtonElement>('[data-provider="deepseek"]')!;
    opener.focus();
    click(opener);
    expect(document.querySelector('[data-testid="settings-provider-back"]')).not.toBeNull();
    expect(document.querySelector('[data-provider="openrouter"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    click(document.querySelector('[data-testid="settings-provider-back"]'));
    expect(document.querySelector('[data-testid="settings-provider-back"]')).toBeNull();
    expect(document.activeElement).toBe(document.querySelector('[data-provider="deepseek"]'));
  });

  it("dismisses a model removal confirmation without leaving its connection details", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="deepseek"]'));
    const modelActions = document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-model-actions"][data-model="deepseek-v4-flash"]')!;
    expect(modelActions.getAttribute("aria-haspopup")).toBe("menu");
    expect(modelActions.getAttribute("aria-label")?.trim()).toBeTruthy();
    modelActions.focus();
    click(modelActions);
    expect(onSave).not.toHaveBeenCalled();
    click(document.querySelector('[role="menuitem"][data-tone="danger"]'));
    expect(document.querySelector('.model-remove-dialog')).not.toBeNull();
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('.model-remove-dialog')).toBeNull();
    expect(document.querySelector('[data-testid="settings-provider-back"]')).not.toBeNull();
    expect(onSave).not.toHaveBeenCalled();
  });

  it("saves another service's key without changing the default", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();

    click(document.querySelector('[data-provider="deepseek"]'));
    click(document.querySelector('[data-testid="settings-provider-key-edit"]'));
    const input = document.querySelector<HTMLInputElement>('[data-testid="settings-provider-key-input"]')!;
    expect(input.type).toBe("password");
    act(() => setInputValue(input, "sk-rotated"));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-key-save"]')!.click();
    });

    expect(onSave).toHaveBeenCalledWith("deepseek", "deepseek-v4-pro", undefined, {
      base_url: "https://api.deepseek.com",
      api_key: "sk-rotated",
      keep_selection: true,
    });
    expect(document.querySelector('[data-testid="settings-provider-key-input"]')).toBeNull();
  });

  it("keeps a pending credential save open and retains a failed edit for retry", async () => {
    installServicesStub();
    let rejectSave!: (error: Error) => void;
    const onSave = vi.fn(() => new Promise<void>((_resolve, reject) => { rejectSave = reject; }));
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="deepseek"]'));
    click(document.querySelector('[data-testid="settings-provider-key-edit"]'));
    act(() => setInputValue(document.querySelector<HTMLInputElement>('[data-testid="settings-provider-key-input"]')!, "sk-retry"));
    click(document.querySelector('[data-testid="settings-provider-key-save"]'));
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-back"]')!.disabled).toBe(true);
    click(document.querySelector('[data-testid="settings-provider-back"]'));
    expect(document.querySelector('[data-testid="settings-provider-back"]')).not.toBeNull();
    await act(async () => rejectSave(new Error("Credential storage unavailable")));
    expect(document.querySelector('[data-testid="settings-providers"]')?.textContent).toContain("Credential storage unavailable");
    expect(document.querySelector<HTMLInputElement>('[data-testid="settings-provider-key-input"]')!.value).toBe("sk-retry");
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-key-save"]')!.disabled).toBe(false);
    onSave.mockImplementationOnce(async () => {});
    await act(async () => document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-key-save"]')!.click());
    expect(onSave).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[data-testid="settings-provider-key-input"]')).toBeNull();
  });

  it("chooses a service model in place and makes the service default only on request", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="deepseek"]'));

    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-model="deepseek-v4-flash"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-flash", undefined, { keep_selection: true });

    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-make-default"]')!.click();
    });
    // The default carries the model and a variant the model accepts.
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, undefined, "");
  });

  it("removes and restores model choices with an explicit replacement for selected models", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    const initialized = servicesInitialized();
    renderSettings({ initialized, initialPage: "providers", onSave });
    await flush();
    click(document.querySelector('[data-provider="deepseek"]'));

    const modelActions = document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-model-actions"][data-model="deepseek-v4-flash"]')!;
    expect(modelActions.getAttribute("aria-haspopup")).toBe("menu");
    expect(modelActions.getAttribute("aria-label")?.trim()).toBeTruthy();
    modelActions.focus();
    click(modelActions);
    expect(onSave).not.toHaveBeenCalled();
    click(document.querySelector('[role="menuitem"][data-tone="danger"]'));
    expect(onSave).not.toHaveBeenCalled();
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, { remove_model: "deepseek-v4-flash", keep_selection: true }, undefined);

    click(document.querySelector('[data-testid="settings-provider-model-actions"][data-model="deepseek-v4-pro"]'));
    click(document.querySelector('[role="menuitem"][data-tone="danger"]'));
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.disabled).toBe(true);
    click(document.querySelector('[data-testid="settings-provider-replacement-model"]'));
    click(document.querySelector('[data-value="deepseek-v4-flash"]'));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-flash", undefined, { remove_model: "deepseek-v4-pro", keep_selection: true }, undefined);

    // Removing the workspace default model atomically saves the chosen replacement.
    renderSettings({ initialized: { ...initialized, provider: "deepseek", model: "deepseek-v4-pro" }, initialPage: "providers", onSave });
    click(document.querySelector('[data-testid="settings-provider-model-actions"][data-model="deepseek-v4-pro"]'));
    click(document.querySelector('[role="menuitem"][data-tone="danger"]'));
    click(document.querySelector('[data-testid="settings-provider-replacement-model"]'));
    click(document.querySelector('[data-value="deepseek-v4-flash"]'));
    onSave.mockRejectedValueOnce(new Error("Unable to save model choices"));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.click();
    });
    expect(document.querySelector('.model-remove-dialog [role="alert"]')?.textContent).toBe("Unable to save model choices");
    expect(document.querySelector('[data-testid="settings-provider-replacement-model"]')?.textContent).toContain("DeepSeek V4 Flash");
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-flash", undefined, { remove_model: "deepseek-v4-pro", keep_selection: false }, "");

    await act(async () => {
      document.querySelector<HTMLButtonElement>('[aria-label="恢复 deepseek-old"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, { add_model: "deepseek-old", keep_selection: true });

    // The last model cannot disappear while the provider still needs a selection.
    const sole = initialized.providers!.find((provider) => provider.name === "deepseek")!;
    renderSettings({ initialized: { ...initialized, provider: "deepseek", model: sole.model, providers: [{ ...sole, models: [{ id: sole.model }] }] }, initialPage: "providers", onSave });
    click(document.querySelector('[data-testid="settings-provider-model-actions"]'));
    click(document.querySelector('[role="menuitem"][data-tone="danger"]'));
    expect(document.querySelector('[data-testid="settings-provider-replacement-model"]')).toBeNull();
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-model-confirm"]')!.disabled).toBe(true);
  });

  it("connects a catalog provider with its endpoint and suggested model", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();

    await openProviderCatalog();
    click(document.querySelector('[data-catalog="moonshotai-cn"]'));
    await flush();
    const dialog = document.querySelector('.model-connect-dialog')!;
    const connect = dialog.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!;
    expect(connect.disabled).toBe(true);
    // The current default works, so connecting another service keeps it.
    expect(dialog.querySelector('[data-testid="settings-provider-connect-default"]')?.getAttribute("aria-checked")).toBe("false");
    act(() => setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-key"]')!, "sk-kimi"));
    expect(connect.disabled).toBe(false);
    await act(async () => {
      connect.click();
    });

    expect(onSave).toHaveBeenCalledWith("moonshotai-cn", "kimi-k3", undefined, {
      type: "openai-compatible",
      create_provider: true,
      keep_selection: true,
      base_url: "https://api.moonshot.cn/v1",
      api_key: "sk-kimi",
    });
  });

  it("makes the first working connection the default", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: baseInitialized({ provider: "", model: "", providers: [] }), initialPage: "providers", onSave });
    await flush();

    await openProviderCatalog();
    click(document.querySelector('[data-testid="settings-provider-custom"]'));
    const dialog = document.querySelector('.model-connect-dialog')!;
    expect(dialog.querySelector('[data-testid="settings-provider-connect-default"]')?.getAttribute("aria-checked")).toBe("true");
    act(() => {
      setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-base-url"]')!, "https://gateway.example.test/v1");
      setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-key"]')!, "sk-gw");
      setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-model"]')!, "team-model");
    });
    await act(async () => {
      dialog.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!.click();
    });

    expect(onSave).toHaveBeenCalledWith("custom", "team-model", undefined, {
      type: "openai-compatible",
      create_provider: true,
      keep_selection: false,
      base_url: "https://gateway.example.test/v1",
      api_key: "sk-gw",
    });
  });

  it("refuses to remove a service a running turn uses, and confirms other removals", async () => {
    installServicesStub();
    const onRemoveProvider = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onRemoveProvider, runningProviderNames: ["deepseek"] });
    await flush();
    click(document.querySelector('[data-provider="deepseek"]'));
    click(document.querySelector('[data-testid="settings-provider-remove"]'));
    expect(document.body.textContent).toContain("这个模型服务正在被运行中的对话使用");
    expect(onRemoveProvider).not.toHaveBeenCalled();

    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onRemoveProvider, runningProviderNames: [] });
    click(document.querySelector('[data-testid="settings-provider-remove"]'));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.click();
    });
    expect(onRemoveProvider).toHaveBeenCalledWith("deepseek", undefined);

    const initialized = { ...servicesInitialized(), provider: "deepseek", model: "deepseek-v4-pro" };
    renderSettings({ initialized, initialPage: "providers", onRemoveProvider });
    click(document.querySelector('[data-provider="deepseek"]'));
    click(document.querySelector('[data-testid="settings-provider-remove"]'));
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.disabled).toBe(true);
    click(document.querySelector('[data-testid="settings-provider-removal-fallback"]'));
    click(document.querySelector('[data-value="openai-codex"]'));
    onRemoveProvider.mockRejectedValueOnce(new Error("Could not remove provider"));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.click();
    });
    expect(document.querySelector('.model-service-remove-dialog [role="alert"]')?.textContent).toBe("Could not remove provider");
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.click();
    });
    expect(onRemoveProvider).toHaveBeenLastCalledWith("deepseek", { fallbackProvider: "openai-codex", fallbackModel: "gpt-6-astra" });

    renderSettings({ initialized: { ...initialized, providers: initialized.providers!.filter((provider) => provider.name === "deepseek") }, initialPage: "providers", onRemoveProvider });
    click(document.querySelector('[data-provider="deepseek"]'));
    click(document.querySelector('[data-testid="settings-provider-remove"]'));
    expect(document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.disabled).toBe(true);
  });

  it("allows repeated provider display names with observed subscription plans", async () => {
    const api = installServicesStub();
    const initialized = servicesInitialized();
    initialized.providers = ["personal", "work"].map(name => ({ name, type: "openai-codex", api_key_configured: true, model: "gpt-6-astra" }));
    vi.mocked(api.listEngines).mockResolvedValue({ engines: [], subscription_providers: [{ ...initialized.providers[0],
      quota: { status: "available", plan: "Plus", checked_at: new Date().toISOString(), windows: [{ id: "weekly", window_minutes: 10080, used_percent: 32 }] },
    }] });
    renderSettings({ initialized, initialPage: "providers", locale: "en-US" });
    await flush();
    const row = document.querySelector('[data-provider="personal"]')!;
    expect([...document.querySelectorAll('[data-testid="settings-provider-card"]')].map(card => card.getAttribute("aria-label"))).toEqual(["ChatGPT", "ChatGPT"]);
    expect(row.querySelector(".connection-summary-meta")?.textContent).toContain("Plus");
    expect(row.querySelectorAll("meter")).toHaveLength(1);
    click(row);
    await flush();
    expect(document.querySelector('[data-testid="settings-provider-back"]')).not.toBeNull();
  });
});

describe("SettingsView advanced settings", () => {
  it("renders compaction controls and saves each field on commit", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onAdvancedSave = vi.fn().mockResolvedValue(undefined);
    const { rootText } = renderSettings({
      initialPage: "advanced",
      initialized: baseInitialized({
        provider: "openrouter",
        model: "openai/gpt-5.5",
        advanced_settings: {
          max_steps: 0,
          max_context_tokens: 0,
          temperature: 0,
          disable_auto_compact: false,
          compact_keep_recent_tokens: 20000,
          context_window_tokens: 400000,
          context_window_source: "provider_input_limit",
          output_reserve_tokens: 128000,
          compact_threshold_tokens: 272000,
        },
      }),
      onAdvancedSave,
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(container.querySelector("[data-testid=\"settings-advanced\"]")).not.toBeNull();
    expect(rootText()).toContain("压缩触发阈值");
    expect(rootText()).toContain("保留最近上下文");
    expect(rootText()).toContain("当前服务上下文上限");
    expect(rootText()).toContain("来自当前通道输入上限");
    expect(rootText()).toContain("400,000");
    // Instant-apply: no draft form, no Save button inside the numeric advanced card.
    expect(
      Array.from(container.querySelectorAll("[data-testid=\"settings-advanced\"] button")).some((button) =>
        button.textContent?.includes("保存"),
      ),
    ).toBe(false);

    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>(".settings-page input"));
    expect(inputs.length).toBeGreaterThanOrEqual(4);
    const [compactThreshold, compactKeepRecent, providerContextWindow, maxContextTokens] = inputs;
    const commit = async (input: Element) => {
      await act(async () => {
        input.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
        await Promise.resolve();
      });
    };

    await act(async () => {
      setInputValue(compactThreshold, "50");
      compactThreshold.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      await Promise.resolve();
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ compact_threshold_pct: 0.5 });

    await act(async () => {
      setInputValue(compactKeepRecent, "30000");
    });
    await commit(compactKeepRecent);
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ compact_keep_recent_tokens: 30000 });

    await act(async () => {
      setInputValue(providerContextWindow, "512000");
    });
    await commit(providerContextWindow);
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ provider_context_window: 512000 });

    await act(async () => {
      setInputValue(maxContextTokens, "256000");
    });
    await commit(maxContextTokens);
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ max_context_tokens: 256000 });

    // Blurring an untouched field does not round-trip the same value.
    const callsBefore = onAdvancedSave.mock.calls.length;
    await commit(maxContextTokens);
    expect(onAdvancedSave.mock.calls.length).toBe(callsBefore);

    const switchButton = container.querySelector(".settings-switch") as HTMLButtonElement;
    await act(async () => {
      switchButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ disable_auto_compact: true });
  });
});

describe("SettingsView general settings", () => {
  it("shows a rejected attribution save locally and allows retry without changing the switch", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const reason = "settings write rejected";
    const failure = new Error(`Error invoking remote method 'wuu:config-general-update': Error: ${reason}`);
    let rejectSave!: (error: Error) => void;
    const pendingSave = new Promise<void>((_resolve, reject) => { rejectSave = reject; });
    const onGeneralSave = vi.fn()
      .mockReturnValueOnce(pendingSave)
      .mockResolvedValue(undefined);
    renderSettings({
      initialized: baseInitialized({
        general_settings: { git_attribution_enabled: true, mcp_server_enabled: {} },
      }),
      initialPage: "advanced",
      onGeneralSave,
    });
    const attributionSwitch = container.querySelector<HTMLButtonElement>(
      '[data-testid="settings-git-attribution"]',
    )!;

    await act(async () => { attributionSwitch.click(); });
    expect(attributionSwitch.disabled).toBe(true);
    await act(async () => { rejectSave(failure); });

    const generalSection = container.querySelector('[data-testid="settings-git"]')!;
    expect(generalSection.querySelector('[role="alert"]')?.textContent).toBe(reason);
    expect(container.textContent).not.toContain("wuu:config-general-update");
    expect(attributionSwitch.getAttribute("aria-checked")).toBe("true");
    expect(attributionSwitch.disabled).toBe(false);

    await act(async () => { attributionSwitch.click(); });
    expect(generalSection.querySelector('[role="alert"]')).toBeNull();
    expect(onGeneralSave).toHaveBeenCalledTimes(2);
    expect(onGeneralSave).toHaveBeenLastCalledWith({ git_attribution_enabled: false });
  });

  it.each([false, true])("loads and toggles WUU Agent commit attribution while running=%s", async (running) => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onGeneralSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      locale: "en-US",
      running,
      initialized: baseInitialized({
        general_settings: {
          git_attribution_enabled: true,
          mcp_server_enabled: {},
        },
      }),
      initialPage: "advanced",
      onGeneralSave,
    });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const attributionSwitch = container.querySelector<HTMLButtonElement>(
      '[data-testid="settings-git-attribution"]',
    );
    expect(attributionSwitch?.getAttribute("aria-checked")).toBe("true");
    expect(container.textContent).toContain("Agent commit attribution");
    expect(container.textContent).toContain("wuu-agent[bot]");

    await act(async () => {
      attributionSwitch?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(onGeneralSave).toHaveBeenCalledWith({
      git_attribution_enabled: false,
    });
  });

  it("renders and saves MCP toggles", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onGeneralSave = vi.fn().mockResolvedValue(undefined);
    const { rootText } = renderSettings({
      initialPage: "mcp",
      initialized: baseInitialized({
        general_settings: {
          mcp_server_enabled: {
            docs: true,
            search: false,
          },
        },
      }),
      onGeneralSave,
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(container.querySelector("[data-testid=\"settings-mcp\"]")).not.toBeNull();
    expect(rootText()).toContain("docs");
    expect(rootText()).toContain("search");

    // MCP toggles now save immediately on switch, sending only the toggle map.
    const docsSwitch = container.querySelector("[data-testid=\"settings-mcp-enabled-docs\"]") as HTMLButtonElement | null;
    expect(docsSwitch).not.toBeNull();
    await act(async () => {
      docsSwitch?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onGeneralSave).toHaveBeenCalledWith({
      mcp_enabled_toggles: {
        docs: false,
        search: false,
      },
    });

    // Instant-apply: no draft form, no Save button on the page.
    expect(
      Array.from(container.querySelectorAll("button")).some((button) =>
        button.textContent?.includes("保存"),
      ),
    ).toBe(false);
  });

  it("renders Codex Pets controls and saves pet selection", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onCodexPetsUpdate = vi.fn().mockImplementation(async (settings: CodexPetSettingsUpdate) =>
      emptyCodexPetsSnapshot({
        enabled: settings.enabled ?? true,
        selected_id: settings.selected_id ?? "alpha",
        pets: [
          {
            id: "alpha",
            display_name: "Alpha Pet",
            description: "",
            manifest_path: "/Users/test/.wuu/pets/alpha/pet.json",
            spritesheet_path: "/Users/test/.wuu/pets/alpha/spritesheet.webp",
            spritesheet_url: "wuu-file://local/alpha",
          },
          {
            id: "beta",
            display_name: "Beta Pet",
            description: "",
            manifest_path: "/Users/test/.wuu/pets/beta/pet.json",
            spritesheet_path: "/Users/test/.wuu/pets/beta/spritesheet.webp",
            spritesheet_url: "wuu-file://local/beta",
          },
        ],
      }),
    );
    const { rootText } = renderSettings({
      initialPage: "appearance",
      initialized: baseInitialized(),
      codexPets: emptyCodexPetsSnapshot({
        enabled: true,
        selected_id: "alpha",
        pets: [
          {
            id: "alpha",
            display_name: "Alpha Pet",
            description: "",
            manifest_path: "/Users/test/.wuu/pets/alpha/pet.json",
            spritesheet_path: "/Users/test/.wuu/pets/alpha/spritesheet.webp",
            spritesheet_url: "wuu-file://local/alpha",
          },
          {
            id: "beta",
            display_name: "Beta Pet",
            description: "",
            manifest_path: "/Users/test/.wuu/pets/beta/pet.json",
            spritesheet_path: "/Users/test/.wuu/pets/beta/spritesheet.webp",
            spritesheet_url: "wuu-file://local/beta",
          },
        ],
      }),
      onCodexPetsUpdate,
    });

    expect(rootText()).toContain("Codex Pet");
    expect(rootText()).toContain("Alpha Pet");

    const petSwitch = container.querySelector("[data-testid=\"settings-codex-pet-enabled\"]") as HTMLButtonElement | null;
    expect(petSwitch).not.toBeNull();
    await act(async () => {
      petSwitch?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onCodexPetsUpdate).toHaveBeenCalledWith({ enabled: false });

    // The pet picker is the shared SelectMenu, not a native select.
    const petSelect = container.querySelector("[data-testid=\"settings-codex-pet-select\"]") as HTMLButtonElement | null;
    expect(petSelect).not.toBeNull();
    expect(petSelect?.tagName).toBe("BUTTON");
    await act(async () => {
      petSelect?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const betaOption = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".select-menu-panel .select-menu-item"),
    ).find((item) => item.getAttribute("data-value") === "beta");
    expect(betaOption?.textContent).toContain("Beta Pet");
    await act(async () => {
      betaOption?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onCodexPetsUpdate).toHaveBeenCalledWith({ selected_id: "beta" });
  });
});

describe("SettingsView About section", () => {
  it("includes core version in the copied version info when initialized", async () => {
    installBuildInfoStub({
      core: {
        version: "v0.2.3",
        commit: "abc1234",
        date: "2026-06-04T07:00:00Z",
        dirty: false,
      },
      desktop: { version: "0.0.0-test", date: "2026-06-28T18:44:52Z" },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText }
    });
    const { about, text } = renderSettings({
      initialized: baseInitialized({
        core: {
          version: "v0.2.3",
          commit: "abc1234",
          date: "2026-06-04T07:00:00Z",
          dirty: false,
        },
      }),
    });
    expect(about).not.toBeNull();
    await act(async () => {
      await Promise.resolve();
    });
    // The visible About row only shows the desktop version; the core version
    // lives in the clipboard payload instead.
    expect(text()).toContain("v0.0.0-test");
    expect(text()).not.toContain("v0.2.3");
    const button = about?.querySelector("button.settings-button");
    await act(async () => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith(
      "wuu v0.0.0-test · 2026-06-28 18:44:52Z · core v0.2.3"
    );
  });

  it("omits core version from the copy when the app-server has not reported core info", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "2026-06-28T18:44:52Z" },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText }
    });
    const { about, text } = renderSettings({ initialized: baseInitialized() });
    await act(async () => {
      await Promise.resolve();
    });
    expect(text()).toContain("关于");
    expect(text()).not.toContain("未连接");
    const button = about?.querySelector("button.settings-button");
    await act(async () => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith("wuu v0.0.0-test · 2026-06-28 18:44:52Z");
  });

  it("renders About section with desktop version and copy action", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "2026-06-28T18:44:52Z" },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText }
    });
    const { about, text } = renderSettings({ initialized: baseInitialized() });
    await act(async () => {
      await Promise.resolve();
    });
    expect(text()).toContain("关于");
    expect(text()).toContain("v0.0.0-test");
    expect(text()).not.toContain("更新于");
    // 版本与复制合并为一行；复制是图标按钮，语义在 aria-label 上。
    const button = about?.querySelector<HTMLButtonElement>('button[aria-label="复制版本信息"]');
    expect(button).not.toBeNull();
    await act(async () => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(writeText).toHaveBeenCalledWith("wuu v0.0.0-test · 2026-06-28 18:44:52Z");
  });

  it("renders MCP server status", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    (window as unknown as GlobalWindow).wuu.listMCPServers = vi.fn().mockResolvedValue({
      servers: [
        {
          name: "docs",
          state: "connected",
          auth_status: "bearer_token",
          connected: true,
          tool_count: 3,
        },
      ],
    });
    const { rootText } = renderSettings({ initialized: baseInitialized(), initialPage: "mcp" });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(rootText()).toContain("MCP");
    expect(rootText()).toContain("docs");
    expect(rootText()).toContain("3 个工具");
    expect(rootText()).toContain("Header 认证");
    // A connected server needs no status mark; its connect control says it.
    expect(container.querySelector(".settings-row-attention")).toBeNull();
  });

  it("marks MCP servers that need a look with their state as the accessible name", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    (window as unknown as GlobalWindow).wuu.listMCPServers = vi.fn().mockResolvedValue({
      servers: [
        { name: "broken", state: "error", connected: false, tool_count: 0, error: "connect ECONNREFUSED" },
        { name: "linear", state: "needs_auth", connected: false, tool_count: 0, auth_status: "not_logged_in" },
      ],
    });
    renderSettings({ initialized: baseInitialized(), initialPage: "mcp" });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    const marks = Array.from(container.querySelectorAll(".settings-row-attention")).map((mark) => [
      mark.getAttribute("data-tone"),
      mark.getAttribute("aria-label"),
    ]);
    expect(marks).toEqual([["danger", "连接失败"], ["warning", "需要认证"]]);
  });

  it("opens MCP OAuth and completes the authorization code flow inline", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const api = (window as unknown as GlobalWindow).wuu;
    api.listMCPServers = vi.fn().mockResolvedValue({
      servers: [
        {
          name: "docs",
          state: "auth_required",
          auth_status: "not_logged_in",
          connected: false,
          tool_count: 0,
        },
      ],
    });
    api.startMCPAuth = vi.fn().mockResolvedValue({
      authorization_url: "https://auth.example.test/authorize",
      state: "oauth-state",
      scopes: ["tools"],
    });
    api.finishMCPAuth = vi.fn().mockResolvedValue({
      auth: { name: "docs", authenticated: true, scopes: ["tools"] },
      server: {
        name: "docs",
        state: "stopped",
        auth_status: "oauth",
        connected: false,
        tool_count: 0,
      },
    });
    api.removeMCPAuth = vi.fn().mockResolvedValue({
      auth: { name: "docs", authenticated: false },
      server: {
        name: "docs",
        state: "auth_required",
        auth_status: "not_logged_in",
        connected: false,
        tool_count: 0,
      },
    });
    api.openExternal = vi.fn().mockResolvedValue(undefined);

    renderSettings({ initialized: baseInitialized(), initialPage: "mcp" });
    const rendered = container;
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const login = rendered.querySelector('button[aria-label="登录 docs"]') as HTMLButtonElement | null;
    expect(login).not.toBeNull();
    await act(async () => {
      login?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(api.startMCPAuth).toHaveBeenCalledWith("docs");
    expect(api.openExternal).toHaveBeenCalledWith("https://auth.example.test/authorize");

    const code = rendered.querySelector('input[aria-label="docs 授权码"]') as HTMLInputElement | null;
    expect(code).not.toBeNull();
    act(() => {
      setInputValue(code!, "authorization-code");
    });
    const finish = rendered.querySelector('button[aria-label="完成 docs OAuth 登录"]') as HTMLButtonElement | null;
    await act(async () => {
      finish?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(api.finishMCPAuth).toHaveBeenCalledWith("docs", "oauth-state", "authorization-code");

    const remove = rendered.querySelector('button[aria-label="移除 docs OAuth 登录"]') as HTMLButtonElement | null;
    expect(remove).not.toBeNull();
    await act(async () => {
      remove?.click();
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(api.removeMCPAuth).toHaveBeenCalledWith("docs");
  });

  it("switches to the usage page from the settings sidebar", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const heatmapDates = Array.from({ length: 4 }, (_, index) => {
      const date = new Date();
      date.setHours(12, 0, 0, 0);
      date.setDate(date.getDate() - (3 - index));
      return [
        date.getFullYear(),
        String(date.getMonth() + 1).padStart(2, "0"),
        String(date.getDate()).padStart(2, "0"),
      ].join("-");
    });
    const usage: SettingsUsageResponse = {
      total_sessions: 1,
      generated_at: "2026-06-18T12:00:00Z",
      metrics: {
        prompt_tokens: 12_345_700,
        context_tokens: 999_999,
        input_tokens: 12_345_678,
        output_tokens: 1_234,
        cache_read_tokens: 50,
        cache_creation_tokens: 20,
        cache_hit_rate: 50 / 1050,
        turns: 1,
        agents: 0,
        date_range: ["2026-06-18", "2026-06-18"],
        active_days: 1,
      },
      model_breakdowns: [
        {
          provider: "OpenAI API",
          model: "fake-model",
          input_tokens: 1000,
          output_tokens: 200,
          cache_creation_tokens: 20,
          cache_read_tokens: 50,
          sessions: 1,
        },
      ],
      skill_usage: [
        { name: "review", count: 4 },
        { name: "docs", count: 2 },
        { name: "unknown-count", count: Number.NaN },
      ],
      days: heatmapDates.map((date, index) => ({
        date,
        input_tokens: (index + 1) * 100_000,
        output_tokens: 0,
        cache_creation_tokens: 20_000,
        cache_read_tokens: 50_000,
        cache_hit_rate: 0.5,
        turns: 1,
        agents: 0,
      })),
    };
    const { rootText } = renderSettings({
      initialized: baseInitialized(),
      usage,
    });
    const usageButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("用量"),
    );
    expect(usageButton).not.toBeUndefined();
    await act(async () => {
      usageButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(container.querySelector("[data-testid=\"settings-usage\"]")).not.toBeNull();
    expect(rootText()).toContain("12.3M");
    expect(rootText()).toContain("1M");
    expect(rootText()).toContain("1.2k");
    expect(rootText()).toContain("review");
    expect(rootText()).toContain("4");
    expect(rootText()).toContain("unknown-count");
    expect(rootText()).toContain("—");
    expect(rootText()).not.toContain("NaN");
    // Compact numbers keep their exact value in a hover tooltip.
    const totalInput = Array.from(
      container.querySelectorAll<HTMLElement>(".settings-usage-stat-value"),
    ).find((element) => element.textContent === "12.3M");
    expect(await hoverTooltipText(totalInput ?? null)).toBe("12,345,678");
    const modelInput = container.querySelector(".settings-usage-table td:nth-child(3) .settings-usage-number");
    expect(modelInput?.textContent).toBe("1k");
    expect(await hoverTooltipText(modelInput)).toBe("1,000");
    expect(rootText()).toContain("缓存命中率");
    expect(rootText()).toContain("5%");
    expect(container.querySelectorAll(".settings-usage-stat")).toHaveLength(4);
    expect(rootText()).not.toContain("活跃");
    expect(rootText()).toContain("OpenAI API");
    expect(rootText()).not.toContain("最近记录");
    const trend = container.querySelector(".settings-usage-trend");
    expect(trend?.getAttribute("aria-label")).toBe("Token 用量趋势");
    expect(trend?.querySelectorAll(".settings-usage-trend-day")).toHaveLength(30);
    expect(
      trend?.querySelector<HTMLElement>(`[aria-label^="${heatmapDates.at(-1)}"]`)?.getAttribute("aria-label"),
    ).toContain("总计 470k");
    const modelRows = container.querySelectorAll(".settings-usage-table tbody tr");
    expect(modelRows).toHaveLength(1);
    expect(modelRows[0]?.textContent).toContain("fake-model");
    // A model's share keeps its token total in a hover tooltip.
    const modelShare = modelRows[0]?.querySelector<HTMLElement>(".settings-usage-share") ?? null;
    expect(modelShare?.textContent).toBe("100%");
    expect(await hoverTooltipText(modelShare)).toBe("1.3k");
    const heatmap = container.querySelector(".settings-usage-heatmap");
    expect(heatmap).not.toBeNull();
    expect(heatmap?.getAttribute("aria-label")).toBe("每日用量热力图");
    expect(
      heatmapDates.map((date) =>
        heatmap
          ?.querySelector<HTMLElement>(`[aria-label^="${date}"]`)
          ?.getAttribute("data-level"),
      ),
    ).toEqual(["1", "2", "3", "4"]);
    expect(
      heatmap?.querySelector<HTMLElement>(`[aria-label^="${heatmapDates.at(-1)}"]`)?.getAttribute("aria-label"),
    ).toContain("输入 400k");
  });

  it("does not leave the usage skeleton visible after a load failure", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const rendered = renderSettings({
      initialized: baseInitialized(),
      initialPage: "usage",
      usageError: "无法加载用量信息，请稍后重试。",
    });

    expect(rendered.rootText()).toContain("无法加载用量信息，请稍后重试。");
    expect(container.querySelector(".settings-usage-skeleton-stats")).toBeNull();
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("无法加载用量信息，请稍后重试。");
  });

  it("marks usage as busy and hides placeholder graphics from assistive technology", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "usage",
      usageLoading: true,
    });

    const usage = container.querySelector('[data-testid="settings-usage"]');
    expect(usage?.getAttribute("aria-busy")).toBe("true");
    expect(usage?.children.length).toBeGreaterThan(0);
    for (const section of usage?.children ?? []) {
      expect(section.getAttribute("aria-hidden")).toBe("true");
    }
  });
});

describe("SettingsView archive page", () => {
  // SettingsView 在挂载时会同步触发 `window.wuu.getBuildInfo()` / `listMCPServers()`
  // 这两个 useEffect，archive 页的测试也得装上 stub，不然 effect 一进来就抛。
  beforeEach(() => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
  });

  // ArchivedSessionView 的结构子集：渲染层读取标题、时间和归档时所属工作区。
  // 这里直接返回对象字面量，结构上兼容 SettingsView 里的 ArchivedSessionView，
  // 避免引入 ThreadSummary（它要求 turns/turn_count 等计算字段，测试场景下冗余）。
  function archivedThread(
    id: string,
    overrides: Partial<ArchivedSessionView> = {},
  ): ArchivedSessionView {
    return {
      id,
      title: `已归档 ${id}`,
      updated_at: "2026-06-18T12:00:00Z",
      ...overrides,
    };
  }

  it("renders the empty-state card when no threads are archived", () => {
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [],
    });

    expect(container.textContent).toContain("暂无已归档的对话或群聊");
    expect(container.querySelector(".settings-archive-empty")).not.toBeNull();
    expect(container.querySelector(".settings-archive-list")).toBeNull();
  });

  it("lists archived threads and orders them by updated_at descending", () => {
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [
        archivedThread("older", {
          title: "旧会话",
          updated_at: "2026-06-10T00:00:00Z",
        }),
        archivedThread("newer", {
          title: "新会话",
          updated_at: "2026-06-20T00:00:00Z",
        }),
      ],
    });

    const titles = Array.from(
      container.querySelectorAll<HTMLElement>(".settings-archive-title"),
    ).map((node) => node.textContent);
    // 较新的会话排在前面，跟侧边栏"最新活动优先"的心智一致
    expect(titles).toEqual(["新会话", "旧会话"]);
  });

  it("groups archived threads by project and shows per-project counts", () => {
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [
        archivedThread("wuu-1", {
          archive_project_id: "wuu",
          archive_project_name: "wuu",
        }),
        archivedThread("wuu-2", {
          archive_project_id: "wuu",
          archive_project_name: "wuu",
        }),
        archivedThread("site-1", {
          archive_project_id: "site",
          archive_project_name: "网站",
        }),
      ],
    });

    const groups = container.querySelectorAll(".settings-archive-group");
    expect(groups).toHaveLength(2);
    const count = (group: Element | undefined) => group?.querySelector(".settings-archive-group-count");
    expect(groups[0]?.textContent).toContain("wuu");
    expect(count(groups[0])?.textContent).toBe("2");
    expect(count(groups[0])?.getAttribute("aria-label")).toBe("2 个对话");
    expect(groups[1]?.textContent).toContain("网站");
    expect(count(groups[1])?.textContent).toBe("1");
  });

  it("filters archived threads by workspace", () => {
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [
        archivedThread("wuu-session", {
          title: "wuu 会话",
          archive_project_id: "wuu",
          archive_project_name: "wuu",
        }),
        archivedThread("site-session", {
          title: "网站会话",
          archive_project_id: "site",
          archive_project_name: "网站",
        }),
      ],
    });

    const trigger = container.querySelector<HTMLButtonElement>(
      '[aria-label="按工作区筛选"]',
    );
    act(() => {
      trigger?.click();
    });
    const siteOption = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".select-menu-item"),
    ).find((option) => option.textContent?.includes("网站"));
    act(() => {
      siteOption?.click();
    });

    expect(container.textContent).toContain("网站会话");
    expect(container.textContent).not.toContain("wuu 会话");
  });

  it("filters archived threads by title", () => {
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [
        archivedThread("docker", { title: "运行 Docker bench" }),
        archivedThread("electron", { title: "排查 Electron 启动失败" }),
      ],
    });

    const search = container.querySelector<HTMLInputElement>(
      '.settings-archive-search input[type="search"]',
    );
    expect(search).not.toBeNull();
    act(() => {
      if (search) {
        setInputValue(search, "Electron");
      }
    });

    expect(container.textContent).toContain("排查 Electron 启动失败");
    expect(container.textContent).not.toContain("运行 Docker bench");
  });

  it("keeps the full long title in a single ellipsized title element", async () => {
    const longTitle = "这是一个非常长的归档会话标题，需要在恢复按钮前截断并显示省略号";
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [archivedThread("long", { title: longTitle })],
    });

    const title = container.querySelector<HTMLElement>(".settings-archive-title");
    expect(title?.textContent).toBe(longTitle);
    // No native title dump: the full text shows in a hover tooltip, and
    // only once the row actually ellipsizes (jsdom never truncates, so no
    // tooltip opens here).
    expect(title?.getAttribute("title")).toBeNull();
    expect(await hoverTooltipText(title)).toBeNull();
  });

  it("keeps archived rows compact and does not render their local paths", () => {
    const threadWithPath = {
      ...archivedThread("hidden-path"),
      cwd: "/private/workspace",
    };
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [threadWithPath],
    });

    expect(container.querySelectorAll(".settings-archive-row")).toHaveLength(1);
    expect(container.querySelectorAll(".settings-row")).toHaveLength(0);
    expect(container.textContent).not.toContain("/private/workspace");
  });

  it("invokes onUnarchiveThread with the clicked thread", () => {
    const onUnarchiveThread = vi.fn();
    const target = archivedThread("old-1", { title: "旧会话" });
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedThreads: [target],
      onUnarchiveThread,
    });

    const restoreButton = container.querySelector<HTMLButtonElement>(
      ".settings-archive-restore",
    );
    expect(restoreButton).not.toBeNull();
    act(() => {
      restoreButton?.click();
    });

    expect(onUnarchiveThread).toHaveBeenCalledTimes(1);
    expect(onUnarchiveThread).toHaveBeenCalledWith(target);
  });
});
