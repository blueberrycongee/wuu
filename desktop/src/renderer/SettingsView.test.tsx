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

  it("exposes phone access on a native host", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "today" } });
    window.wuu.getRemoteControlSnapshot = vi.fn().mockResolvedValue({ status: null, host_running: false, pair_uri: null });
    window.wuu.onRemoteControlEvent = vi.fn(() => () => {});
    renderSettings({ initialized: baseInitialized(), initialPage: "remote" });
    await act(async () => { await Promise.resolve(); });
    expect(container.querySelector('[data-testid="settings-remote-page"]')).not.toBeNull();
    expect(window.wuu.getRemoteControlSnapshot).toHaveBeenCalled();
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

  it("keeps the drawer open while the pointer crosses onto the shell-level toggle", () => {
    vi.useFakeTimers();
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({ initialized: baseInitialized(), sidebarCollapsed: true });

    const shell = container.querySelector<HTMLElement>(".settings-shell");
    const sidebar = container.querySelector<HTMLElement>(".settings-sidebar");
    const toggle = container.querySelector<HTMLElement>(
      ".settings-titlebar .settings-sidebar-toggle",
    );
    expect(sidebar).not.toBeNull();
    expect(toggle).not.toBeNull();

    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: vi.fn(() => toggle),
    });
    act(() => {
      toggle?.dispatchEvent(new MouseEvent("pointerover", { bubbles: true }));
      vi.advanceTimersByTime(240);
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);

    act(() => {
      sidebar?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          clientX: 80,
          clientY: 24,
          relatedTarget: toggle,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(shell?.classList.contains("sidebar-drawer-closing")).toBe(false);
  });

  it("closes the drawer when the pointer leaves the shell-level toggle for a non-hover target", () => {
    vi.useFakeTimers();
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({ initialized: baseInitialized(), sidebarCollapsed: true });

    const shell = container.querySelector<HTMLElement>(".settings-shell");
    const toggle = container.querySelector<HTMLElement>(
      ".settings-titlebar .settings-sidebar-toggle",
    );
    expect(toggle).not.toBeNull();

    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: vi.fn(() => toggle),
    });
    act(() => {
      toggle?.dispatchEvent(new MouseEvent("pointerover", { bubbles: true }));
      vi.advanceTimersByTime(240);
    });
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(true);

    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: vi.fn(() => document.body),
    });
    act(() => {
      toggle?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          clientX: 320,
          clientY: 24,
          relatedTarget: document.body,
        }),
      );
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

describe("SettingsView model services", () => {
  it("keeps connection actions available while the catalog loads and reports failure", async () => {
    const api = installServicesStub();
    let rejectCatalog!: (reason: Error) => void;
    api.listCatalogProviders = vi.fn(() => new Promise<Awaited<ReturnType<WuuDesktopApi["listCatalogProviders"]>>>((_resolve, reject) => { rejectCatalog = reject; }));
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers" });
    await flush();

    expect(container.querySelector('[role="status"]')).not.toBeNull();
    click(container.querySelector('[data-testid="settings-provider-custom"]'));
    expect(document.querySelector('[data-testid="settings-provider-connect-base-url"]')).not.toBeNull();

    await act(async () => { rejectCatalog(new Error("catalog unavailable")); });
    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-provider-custom"]')).not.toBeNull();
  });

  it("leads with the default model and names connected services by vendor", async () => {
    installServicesStub();
    const initialized = servicesInitialized();
    const beforeBrowsing = structuredClone(initialized);
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized, initialPage: "providers", locale: "en-US", onSave });
    await flush();

    const defaultCard = container.querySelector('[data-testid="settings-default-model"]');
    expect(defaultCard?.textContent).toContain("GPT-6 Astra");
    expect(defaultCard?.textContent).toContain("ChatGPT");
    const cards = [...container.querySelectorAll('[data-testid="settings-provider-card"]')];
    expect(cards.map((card) => card.getAttribute("aria-label"))).toEqual(["ChatGPT", "DeepSeek", "OpenRouter"]);
    expect(cards[0]?.textContent).toContain("Default");
    expect(cards[2]?.textContent).toContain("API key missing");
    // Connected vendors stay discoverable and can add a separate connection.
    const connectedTile = container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-tiles"] [data-catalog="deepseek"]');
    expect(connectedTile?.querySelector('[role="img"][data-ready]')?.getAttribute("aria-label")).toBeTruthy();
    expect(connectedTile?.disabled).toBe(false);
    expect(container.querySelector('[data-testid="settings-provider-custom"]')).not.toBeNull();

    click(connectedTile);
    await flush();
    const dialog = document.querySelector('[role="dialog"]')!;
    const name = dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-name"]')!.value;
    expect(name).not.toBe("");
    expect(initialized.providers?.map((provider) => provider.name)).not.toContain(name);
    expect(dialog.querySelector('[data-testid="settings-provider-connect-default"]')?.getAttribute("aria-checked")).toBe("false");
    expect(onSave).not.toHaveBeenCalled();
    act(() => setInputValue(dialog.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-key"]')!, "sk-another-connection"));
    click(dialog.querySelector('.environment-dialog-footer button[type="button"]'));

    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(onSave).not.toHaveBeenCalled();
    expect(initialized).toEqual(beforeBrowsing);
  });

  it("saves another service's key without changing the default", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();

    click(container.querySelector('[data-provider="deepseek"]'));
    click(container.querySelector('[data-testid="settings-provider-key-edit"]'));
    const input = container.querySelector<HTMLInputElement>('[data-testid="settings-provider-key-input"]')!;
    expect(input.type).toBe("password");
    act(() => setInputValue(input, "sk-rotated"));
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-key-save"]')!.click();
    });

    expect(onSave).toHaveBeenCalledWith("deepseek", "deepseek-v4-pro", undefined, {
      base_url: "https://api.deepseek.com",
      api_key: "sk-rotated",
      keep_selection: true,
    });
    expect(container.querySelector('[data-testid="settings-provider-key-input"]')).toBeNull();
  });

  it("chooses a service model in place and makes the service default only on request", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    click(container.querySelector('[data-provider="deepseek"]'));

    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-model="deepseek-v4-flash"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-flash", undefined, { keep_selection: true });

    await act(async () => {
      container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-make-default"]')!.click();
    });
    // The default carries the model and a variant the model accepts.
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, undefined, "");
  });

  it("hides and restores model choices", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();
    click(container.querySelector('[data-provider="deepseek"]'));

    // The service's own model cannot be hidden; its row keeps the slot empty.
    expect(container.querySelector('[aria-label="隐藏 DeepSeek V4 Pro"]')).toBeNull();
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[aria-label="隐藏 DeepSeek V4 Flash"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, { remove_model: "deepseek-v4-flash", keep_selection: true });

    await act(async () => {
      container.querySelector<HTMLButtonElement>('[aria-label="恢复 deepseek-old"]')!.click();
    });
    expect(onSave).toHaveBeenLastCalledWith("deepseek", "deepseek-v4-pro", undefined, { add_model: "deepseek-old", keep_selection: true });
  });

  it("connects a catalog provider with its endpoint and suggested model", async () => {
    installServicesStub();
    const onSave = vi.fn(async () => {});
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onSave });
    await flush();

    click(container.querySelector('[data-catalog="moonshotai-cn"]'));
    await flush();
    const dialog = document.querySelector('[role="dialog"]')!;
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

    expect(container.querySelector('[data-testid="settings-providers-empty"]')).not.toBeNull();
    click(container.querySelector('[data-testid="settings-provider-custom"]'));
    const dialog = document.querySelector('[role="dialog"]')!;
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
    click(container.querySelector('[data-provider="deepseek"]'));
    click(container.querySelector('[aria-label="deepseek 的更多操作"], [aria-label="DeepSeek 的更多操作"]'));
    click([...document.querySelectorAll('[role="menuitem"]')].find((item) => item.textContent?.includes("删除服务")));
    expect(container.textContent).toContain("这个模型服务正在被运行中的对话使用");
    expect(onRemoveProvider).not.toHaveBeenCalled();

    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onRemoveProvider, runningProviderNames: [] });
    click(container.querySelector('[aria-label="DeepSeek 的更多操作"]'));
    click([...document.querySelectorAll('[role="menuitem"]')].find((item) => item.textContent?.includes("删除服务")));
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.click();
    });
    expect(onRemoveProvider).toHaveBeenCalledWith("deepseek");
  });

  it("refreshes the model catalog with a button-only loading state", async () => {
    installServicesStub();
    let finish: () => void = () => {};
    const onRefreshModelCatalog = vi.fn(() => new Promise<void>((resolve) => { finish = resolve; }));
    renderSettings({ initialized: servicesInitialized(), initialPage: "providers", onRefreshModelCatalog });
    await flush();
    const refresh = container.querySelector<HTMLButtonElement>('[data-testid="settings-model-catalog-refresh"]')!;
    click(refresh);
    expect(refresh.disabled).toBe(true);
    expect(refresh.getAttribute("aria-busy")).toBe("true");
    await act(async () => finish());
    expect(refresh.disabled).toBe(false);
    expect(window.wuu.listCatalogProviders).toHaveBeenCalledTimes(2);
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

    const inputs = Array.from(container.querySelectorAll("input"));
    expect(inputs.length).toBeGreaterThanOrEqual(6);
    const [compactThreshold, compactKeepRecent, providerContextWindow, maxContextTokens, maxSteps, temperature] = inputs;
    expect((temperature as HTMLInputElement).value).toBe("");
    // The placeholder carries the automatic-value meaning instead of the row description.
    expect((temperature as HTMLInputElement).placeholder).toBe("自动");

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

    await act(async () => {
      setInputValue(maxSteps, "12");
    });
    await commit(maxSteps);
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ max_steps: 12 });

    await act(async () => {
      setInputValue(temperature, "0.4");
    });
    await commit(temperature);
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ temperature: 0.4 });

    // Blurring an untouched field does not round-trip the same value.
    const callsBefore = onAdvancedSave.mock.calls.length;
    await commit(temperature);
    expect(onAdvancedSave.mock.calls.length).toBe(callsBefore);

    const switchButton = container.querySelector(".settings-switch") as HTMLButtonElement;
    await act(async () => {
      switchButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ disable_auto_compact: true });
  });

  it("saves cleared temperature as Auto on commit", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onAdvancedSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "advanced",
      initialized: baseInitialized({
        provider: "openrouter",
        model: "openai/gpt-5.5",
        advanced_settings: {
          max_steps: 0,
          max_context_tokens: 0,
          temperature: 0,
          disable_auto_compact: false,
        },
      }),
      onAdvancedSave,
    });
    await act(async () => {
      await Promise.resolve();
    });

    const temperature = Array.from(container.querySelectorAll("input")).at(-1) as HTMLInputElement;
    await act(async () => {
      setInputValue(temperature, "0.4");
      temperature.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ temperature: 0.4 });

    await act(async () => {
      setInputValue(temperature, "");
      temperature.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({ temperature: 0 });
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
