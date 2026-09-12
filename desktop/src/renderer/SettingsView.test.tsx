import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import {
  SettingsView,
  type ArchivedRoomView,
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
  archivedRooms?: readonly ArchivedRoomView[];
  onUnarchiveThread?: (thread: ArchivedSessionView) => void;
  onUnarchiveRoom?: (room: ArchivedRoomView) => void;
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
        running={false}
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
        sidebarMotionMs={240}
        onBack={() => {}}
        onSave={props.onSave ?? (async () => {})}
        onRemoveProvider={props.onRemoveProvider ?? (async () => {})}
        onRefreshModelCatalog={props.onRefreshModelCatalog ?? (async () => {})}
        onAdvancedSave={props.onAdvancedSave ?? (async () => {})}
        onGeneralSave={props.onGeneralSave ?? (async () => {})}
        onSidebarResizeStart={noopResizeStart}
        onSidebarSeparatorKey={noopResizeKey}
        archivedThreads={props.archivedThreads ?? []}
        archivedRooms={props.archivedRooms ?? []}
        onUnarchiveThread={props.onUnarchiveThread ?? (() => {})}
        onUnarchiveRoom={props.onUnarchiveRoom ?? (() => {})}
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
    window.wuu.unsupportedMethods = ["getBuildInfo", "listCodexPets", "startSpeechRecognition", "getRemoteControlSnapshot"];
    renderSettings({ initialized: baseInitialized(), initialPage: "general" });
    await act(async () => { await Promise.resolve(); });
    expect(window.wuu.getBuildInfo).not.toHaveBeenCalled();
    expect(container.querySelector('[data-testid="settings-codex-pet-enabled"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-voice-input"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-appearance"]')).not.toBeNull();
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

  it("exposes the sidebar state and invokes the toggle action", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onToggleSidebar = vi.fn();
    renderSettings({ initialized: baseInitialized(), onToggleSidebar });

    const toggle = container.querySelector<HTMLButtonElement>(
      ".settings-titlebar .settings-sidebar-toggle",
    );
    expect(toggle).not.toBeNull();
    expect(toggle?.getAttribute("aria-pressed")).toBe("true");

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
    const advancedButton = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".settings-nav-item"),
    ).find((button) => button.textContent?.includes("高级"));

    act(() => {
      advancedButton?.click();
    });

    expect(scroll.scrollTop).toBe(0);
    expect(container.querySelector(".settings-page")).not.toBe(providersPage);
    expect(container.querySelector(".settings-page-title")?.textContent).toBe("高级");
  });
});

describe("SettingsView provider configuration", () => {
  it("renders provider configuration in English", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const { rootText } = renderSettings({
      locale: "en-US",
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "local",
        model: "",
        providers: [{
          name: "local",
          type: "openai-compatible",
          model: "",
          base_url: "http://127.0.0.1:11434/v1",
          api_key_configured: false,
        }],
      }),
    });

    expect(rootText()).toContain("Model providers");
    expect(rootText()).toContain("Local model provider");
    expect(rootText()).toContain("No model selected");
    expect(rootText()).toContain("Missing API key");
    expect(rootText()).toContain("Reasoning effort");
    expect(rootText()).not.toContain("模型服务");
  });

  it("shows BYOK provider controls as a first-class settings page", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const { rootText } = renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "openrouter",
        model: "openai/gpt-5.5",
        providers: [
          {
            name: "openrouter",
            type: "openai-compatible",
            model: "openai/gpt-5.5",
            base_url: "https://openrouter.ai/api/v1",
            api_key_configured: true,
          },
        ],
      }),
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(container.querySelector("[data-testid=\"settings-providers\"]")).not.toBeNull();
    expect(rootText()).toContain("模型服务");
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Base URL"]')?.value).toBe("https://openrouter.ai/api/v1");
    expect(rootText()).toContain("Base URL");
    expect(rootText()).toContain("API key 已配置");
    expect(rootText()).toContain("新增服务");
  });

  it("edits the provider selected from the service list", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "1970-01-01" } });
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      onSave,
      initialized: baseInitialized({
        provider: "first",
        model: "first-model",
        providers: [
          { name: "first", type: "openai-compatible", model: "first-model", base_url: "https://api.openai.com/v1" },
          { name: "second", type: "openai-compatible", model: "second-model", base_url: "https://api.deepseek.com/v1" },
        ],
      }),
    });
    const options = container.querySelectorAll<HTMLButtonElement>(".settings-provider-button");
    await act(async () => { options[1].click(); });
    expect(onSave).not.toHaveBeenCalled();
    expect(options[1].getAttribute("aria-pressed")).toBe("true");
    expect(options[0].getAttribute("aria-pressed")).toBe("false");
    const model = container.querySelector<HTMLInputElement>('input[aria-label="模型名称"]')!;
    expect(model.value).toBe("second-model");
    await act(async () => { setInputValue(model, "updated-model"); });
    await act(async () => { model.dispatchEvent(new FocusEvent("focusout", { bubbles: true })); });
    expect(onSave.mock.calls.at(-1)?.slice(0, 2)).toEqual(["second", "updated-model"]);
  });

  it("keeps the service editor selected across runtime inventory refreshes", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "1970-01-01" } });
    const onSave = vi.fn();
    const initial = baseInitialized({
      provider: "grok", model: "grok-4.6",
      providers: [
        { name: "grok", type: "grok-build", model: "grok-4.6" },
        { name: "kimi", type: "anthropic", model: "k3" },
      ],
    });
    renderSettings({ initialized: initial, initialPage: "providers", onSave, runningProviderNames: ["grok"] });
    await act(async () => { container.querySelectorAll<HTMLButtonElement>(".settings-provider-button")[1].click(); });
    const model = container.querySelector<HTMLInputElement>('input[aria-label="模型名称"]')!;
    await act(async () => { setInputValue(model, "draft-model"); });
    renderSettings({ initialized: { ...initial, providers: initial.providers?.map((p) => ({ ...p })) }, initialPage: "providers", onSave, runningProviderNames: ["grok"] });
    expect(container.querySelectorAll(".settings-provider-button")[1].getAttribute("aria-pressed")).toBe("true");
    expect(model.value).toBe("draft-model");
    expect(onSave).not.toHaveBeenCalled();
  });

  it("refreshes the model catalog with a button-only loading state", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    let finishRefresh: (() => void) | undefined;
    const onRefreshModelCatalog = vi.fn(
      () => new Promise<void>((resolve) => { finishRefresh = resolve; }),
    );
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized(),
      onRefreshModelCatalog,
    });

    const button = container.querySelector(
      '[data-testid="settings-model-catalog-refresh"]',
    ) as HTMLButtonElement;
    expect(button.textContent).toContain("更新模型目录");
    expect(button.disabled).toBe(false);

    act(() => {
      button.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onRefreshModelCatalog).toHaveBeenCalledTimes(1);
    expect(button.disabled).toBe(true);
    expect(button.textContent).toContain("正在更新");

    await act(async () => {
      finishRefresh?.();
      await Promise.resolve();
    });
    expect(button.disabled).toBe(false);
    expect(button.textContent).toContain("更新模型目录");
  });

  it("submits a new OpenAI-compatible provider with editable connection fields", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "openai",
        model: "gpt-5.5",
        providers: [
          {
            name: "openai",
            type: "openai",
            model: "gpt-5.5",
            base_url: "https://api.openai.com/v1",
            api_key_configured: true,
          },
        ],
      }),
      onSave,
    });
    const addButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("新增服务"),
    );
    expect(addButton).not.toBeUndefined();
    await act(async () => {
      addButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    const providerInput = container.querySelector<HTMLInputElement>('input[aria-label="服务标识"]')!;
    const modelInput = container.querySelector<HTMLInputElement>('input[aria-label="模型名称"]')!;
    const baseURLInput = container.querySelector<HTMLInputElement>('input[aria-label="Base URL"]')!;
    const apiKeyInput = container.querySelector<HTMLInputElement>('input[type="password"]')!;
    await act(async () => {
      setInputValue(providerInput, "openrouter");
      setInputValue(modelInput, "openai/gpt-5.5");
      setInputValue(baseURLInput, "https://openrouter.ai/api/v1");
      setInputValue(apiKeyInput, "sk-test");
    });

    const submitButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("添加服务"),
    ) as HTMLButtonElement | undefined;
    expect(submitButton?.disabled).toBe(false);
    await act(async () => {
      submitButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(onSave).toHaveBeenCalledWith(
      "openrouter",
      "openai/gpt-5.5",
      undefined,
      {
        base_url: "https://openrouter.ai/api/v1",
        api_key: "sk-test",
        type: "openai-compatible",
        create_provider: true,
      },
      "",
    );
  });

  it("shows SuperGrok login instead of an API key field", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "xai-subscription",
        model: "grok-4.6",
        providers: [
          {
            name: "xai-subscription",
            type: "xai-subscription",
            model: "grok-4.6",
            base_url: "https://api.x.ai/v1",
            api_key_configured: false,
            connection_locked: true,
          },
        ],
      }),
    });
    expect(container.textContent).toContain("使用 SuperGrok 登录");
    expect(container.querySelector("input[type='password']")).toBeNull();
  });

  it("adds Grok Build with CLI-managed credentials and model defaults", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "openai",
        model: "gpt-5.5",
        providers: [{
          name: "openai", type: "openai", model: "gpt-5.5",
          base_url: "https://api.openai.com/v1", api_key_configured: true,
        }],
      }),
      onSave,
    });
    const addButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("新增服务"),
    );
    await act(async () => addButton?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    const typeTrigger = container.querySelector('[data-testid="settings-provider-type-select"]') as HTMLButtonElement;
    await act(async () => typeTrigger.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    const option = Array.from(document.querySelectorAll<HTMLButtonElement>(".select-menu-panel .select-menu-item"))
      .find((item) => item.getAttribute("data-value") === "grok-build");
    expect(option).toBeDefined();
    await act(async () => option?.dispatchEvent(new MouseEvent("click", { bubbles: true })));

    expect(container.textContent).toContain("grok login");
    expect(container.querySelector("input[type='password']")).toBeNull();
    expect(container.querySelector<HTMLInputElement>('input[aria-label="服务标识"]')?.value).toBe("grok-build");
    expect(container.querySelector<HTMLInputElement>('input[aria-label="模型名称"]')?.value).toBe("grok-4.5");
    expect(container.querySelector(".settings-managed-value")?.textContent).toBe("https://cli-chat-proxy.grok.com/v1");
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find((button) => button.textContent?.includes("添加服务"));
    expect(submit?.disabled).toBe(false);
    await act(async () => {
      submit?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(onSave).toHaveBeenCalledWith(
      "grok-build", "grok-4.5", undefined,
      { base_url: "https://cli-chat-proxy.grok.com/v1", type: "grok-build", create_provider: true },
      "",
    );
  });

  it("uses an auto-discovered local Grok login without an add or login step", () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "grok-build",
        model: "grok-4.6",
        providers: [{
          name: "grok-build",
          type: "grok-build",
          model: "grok-4.6",
          base_url: "https://cli-chat-proxy.grok.com/v1",
          api_key_configured: true,
          connection_locked: true,
          auto_discovered: true,
          models: [{
            id: "grok-4.6",
            supported_efforts: ["low", "medium", "high", "xhigh"],
            default_variant: "high",
          }],
        }],
      }),
    });

    expect(container.textContent).toContain("已找到 Grok Build CLI 登录");
    expect(container.textContent).not.toContain("当前模型不支持思考");
    const reasoning = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find((button) => button.getAttribute("aria-label") === "思考强度");
    expect(reasoning?.disabled).toBe(false);
    expect(container.querySelector(".settings-provider-remove")).toBeNull();
  });

  it("shows an alert instead of removing a provider used by a running turn", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const alert = vi.spyOn(window, "alert").mockImplementation(() => {});
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const onRemoveProvider = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      runningProviderNames: ["drop"],
      initialized: baseInitialized({
        provider: "keep",
        model: "keep-model",
        providers: [
          {
            name: "keep",
            type: "openai-compatible",
            model: "keep-model",
            base_url: "https://keep.example.test/v1",
            api_key_configured: true,
          },
          {
            name: "drop",
            type: "openai-compatible",
            model: "drop-model",
            base_url: "https://drop.example.test/v1",
            api_key_configured: true,
          },
        ],
      }),
      onRemoveProvider,
    });

    const removeButtons = Array.from(container.querySelectorAll<HTMLButtonElement>(".settings-provider-remove"));
    expect(removeButtons).toHaveLength(2);
    await act(async () => {
      removeButtons[1].dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(alert).toHaveBeenCalledWith("这个模型服务正在被运行中的会话使用，等当前回复结束后再删除。");
    expect(confirm).not.toHaveBeenCalled();
    expect(onRemoveProvider).not.toHaveBeenCalled();
  });

  it("submits a new Anthropic-compatible provider with API key auth", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "openai",
        model: "gpt-5.5",
        providers: [
          {
            name: "openai",
            type: "openai",
            model: "gpt-5.5",
            base_url: "https://api.openai.com/v1",
            api_key_configured: true,
          },
        ],
      }),
      onSave,
    });
    const addButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("新增服务"),
    );
    await act(async () => {
      addButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    const providerTypeTrigger = container.querySelector("[data-testid=\"settings-provider-type-select\"]") as HTMLButtonElement;
    await act(async () => {
      providerTypeTrigger.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const anthropicOption = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".select-menu-panel .select-menu-item"),
    ).find((item) => item.getAttribute("data-value") === "anthropic");
    await act(async () => {
      anthropicOption?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    const providerInput = container.querySelector<HTMLInputElement>('input[aria-label="服务标识"]')!;
    const modelInput = container.querySelector<HTMLInputElement>('input[aria-label="模型名称"]')!;
    const baseURLInput = container.querySelector<HTMLInputElement>('input[aria-label="Base URL"]')!;
    const apiKeyInput = container.querySelector<HTMLInputElement>('input[type="password"]')!;
    await act(async () => {
      setInputValue(providerInput, "anthropic-gateway");
      setInputValue(modelInput, "claude-sonnet-4-6[1M]");
      setInputValue(baseURLInput, "https://anthropic-gateway.example.test/");
      setInputValue(apiKeyInput, "sk-token");
    });

    const submitButton = Array.from(container.querySelectorAll("button")).find((button) =>
      button.textContent?.includes("添加服务"),
    ) as HTMLButtonElement | undefined;
    expect(submitButton?.disabled).toBe(false);
    await act(async () => {
      submitButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(onSave).toHaveBeenCalledWith(
      "anthropic-gateway",
      "claude-sonnet-4-6[1M]",
      undefined,
      {
        base_url: "https://anthropic-gateway.example.test/",
        api_key: "sk-token",
        type: "anthropic",
        create_provider: true,
      },
      "",
    );
  });
});

describe("SettingsView provider model catalog", () => {
  it("resets incompatible effort on model changes and restores selection after a failed save", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "1970-01-01T00:00:00Z" } });
    const onSave = vi.fn().mockRejectedValue(new Error("connection unavailable"));
    renderSettings({ initialPage: "providers", onSave, initialized: baseInitialized({
      provider: "kimi", model: "k3", variant: "max", providers: [
        { name: "kimi", type: "openai", model: "k3", models: [
          { id: "k3", variants: [{ id: "max" }] }, { id: "kimi-for-coding-highspeed" },
        ] },
      ],
    }) });
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('form button[aria-pressed]'));
    await act(async () => { buttons[1].click(); });
    expect(onSave).toHaveBeenCalledWith("kimi", "kimi-for-coding-highspeed", undefined, undefined, "");
    expect(buttons[0].getAttribute("aria-pressed")).toBe("true");
    expect(buttons[1].getAttribute("aria-pressed")).toBe("false");
    expect(container.textContent).toContain("connection unavailable");
  });

  it("removes tags without selecting them, switches away from a removed default, and protects the last model", async () => {
    installBuildInfoStub({ core: undefined, desktop: { version: "test", date: "1970-01-01T00:00:00Z" } });
    const onSave = vi.fn().mockResolvedValue(undefined);
    const initialized = baseInitialized({ provider: "kimi", model: "k3", providers: [
      { name: "kimi", type: "openai", model: "k3", models: [{ id: "k3" }, { id: "k2" }] },
    ] });
    renderSettings({ initialPage: "providers", initialized, onSave });
    const remove = (model: string) => container.querySelector<HTMLButtonElement>(`button[aria-label="删除 ${model}"]`)!;
    await act(async () => { remove("k2").click(); });
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenLastCalledWith("kimi", "k3", undefined, { remove_model: "k2" }, expect.any(String));
    await act(async () => { remove("k3").click(); });
    expect(onSave).toHaveBeenLastCalledWith("kimi", "k2", undefined, { remove_model: "k3" }, expect.any(String));
    renderSettings({ initialPage: "providers", onSave, initialized: { ...initialized, model: "k2", providers: [
      { name: "kimi", type: "openai", model: "k2", models: [{ id: "k2" }] },
    ] } });
    expect(remove("k3")).toBeNull();
    expect(remove("k2").disabled).toBe(true);
  });

  it("shows all known models, preserves an unlisted selection, and saves a catalog choice", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "providers",
      initialized: baseInitialized({
        provider: "kimi",
        model: "custom-k3",
        providers: [{ name: "kimi", type: "openai", model: "custom-k3", models: [
          { id: "k2" }, { id: "p7" }, { id: "k2" },
        ] }],
      }),
      onSave,
    });
    const modelButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('form button[aria-pressed]'));
    expect(modelButtons.map((button) => button.textContent)).toEqual(["custom-k3", "k2", "p7"]);
    expect(modelButtons[0].getAttribute("aria-pressed")).toBe("true");
    await act(async () => {
      modelButtons[1].click();
    });
    expect(onSave).toHaveBeenCalledWith("kimi", "k2", undefined, undefined, expect.any(String));
    expect(modelButtons[1].getAttribute("aria-pressed")).toBe("true");
  });
});

describe("SettingsView collaboration models", () => {
  it("shows inherited capability models and saves explicit overrides", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onAdvancedSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      initialPage: "collaboration",
      initialized: baseInitialized({
        provider: "openai",
        model: "gpt-default",
        providers: [{
          name: "openai",
          type: "openai",
          model: "gpt-default",
          models: [
            { id: "gpt-default", display_name: "GPT Default" },
            { id: "gpt-review", display_name: "GPT Review" },
          ],
        }],
        model_roles: [
          { role: "coordination", provider: "openai", model: "gpt-default", inherited: true },
          { role: "verification", provider: "openai", model: "gpt-default", inherited: true },
        ],
      }),
      onAdvancedSave,
    });

    const page = container.querySelector('[data-testid="settings-collaboration"]');
    expect(page).not.toBeNull();
    const triggers = Array.from(page!.querySelectorAll<HTMLButtonElement>(".settings-select-trigger"));
    expect(triggers).toHaveLength(2);

    await act(async () => {
      triggers[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const coordinationOption = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".select-menu-panel .select-menu-item"),
    ).find((item) => item.getAttribute("data-value") === "openai\u0000gpt-review");
    await act(async () => {
      coordinationOption?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({
      coordination_model: { provider: "openai", model: "gpt-review" },
    });

    await act(async () => {
      triggers[1].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const inheritOption = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".select-menu-panel .select-menu-item"),
    ).find((item) => item.getAttribute("data-value") === "");
    await act(async () => {
      inheritOption?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onAdvancedSave).toHaveBeenLastCalledWith({
      verification_model: { provider: "", model: "" },
    });
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
  it("loads and toggles WUU Agent commit attribution", async () => {
    installBuildInfoStub({
      core: undefined,
      desktop: { version: "0.0.0-test", date: "1970-01-01T00:00:00Z" },
    });
    const onGeneralSave = vi.fn().mockResolvedValue(undefined);
    renderSettings({
      locale: "en-US",
      initialized: baseInitialized({
        general_settings: {
          git_attribution_enabled: true,
          mcp_server_enabled: {},
        },
      }),
      initialPage: "general",
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
      initialPage: "general",
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
    expect(container.querySelector("[data-testid=\"settings-general\"]")).not.toBeNull();
    expect(container.querySelector("[data-testid=\"settings-voice-input\"]")).toBeNull();
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
      initialPage: "general",
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
    // 版本与复制合并为一行；按钮语义在 aria-label 上，行内只显示「复制」。
    expect(about?.querySelector('button[aria-label="复制版本信息"]')).not.toBeNull();
    const button = about?.querySelector("button.settings-button");
    expect(button?.textContent).toBe("复制");
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
    const { rootText } = renderSettings({ initialized: baseInitialized() });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(rootText()).toContain("MCP");
    expect(rootText()).toContain("docs");
    expect(rootText()).toContain("已连接");
    expect(rootText()).toContain("3 个工具");
    expect(rootText()).toContain("Header 认证");
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

    renderSettings({ initialized: baseInitialized() });
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
    expect(rootText()).toContain("Skills 使用分析");
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
    const modelInput = container.querySelector(".settings-usage-number");
    expect(modelInput?.textContent).toBe("1k");
    expect(await hoverTooltipText(modelInput)).toBe("1,000");
    expect(rootText()).toContain("模型使用");
    expect(rootText()).toContain("Token 用量趋势");
    expect(rootText()).toContain("最近 30 天");
    expect(rootText()).toContain("模型用量构成");
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
    const modelChartRows = container.querySelectorAll(".settings-model-chart-row");
    expect(modelChartRows).toHaveLength(1);
    expect(modelChartRows[0]?.textContent).toContain("fake-model");
    expect(modelChartRows[0]?.textContent).toContain("100%");
    expect(
      await hoverTooltipText(modelChartRows[0]?.querySelector<HTMLElement>(".settings-model-chart-share") ?? null),
    ).toBe("1.3k");
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

  // ArchivedSessionView 的结构子集：渲染层读取标题、时间和归档时所属项目。
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

    expect(container.textContent).toContain("暂无已归档的会话或群聊");
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
    expect(groups[0]?.textContent).toContain("wuu");
    expect(groups[0]?.textContent).toContain("2 个会话");
    expect(groups[1]?.textContent).toContain("网站");
    expect(groups[1]?.textContent).toContain("1 个会话");
  });

  it("filters archived threads by project", () => {
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
      '[aria-label="按项目筛选"]',
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
    const target = archivedThread("dm-1", { title: "DM 旧会话" });
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

  it("lists archived group chats and restores them independently", () => {
    const onUnarchiveRoom = vi.fn();
    const room: ArchivedRoomView = {
      id: "room-1",
      name: "设计讨论",
      created_at: "2026-06-18T12:00:00Z",
    };
    renderSettings({
      initialized: baseInitialized(),
      initialPage: "archive",
      archivedRooms: [room],
      onUnarchiveRoom,
    });

    const roomGroup = container.querySelector('[data-archive-kind="rooms"]');
    expect(roomGroup?.textContent).toContain("设计讨论");
    const restoreButton = roomGroup?.querySelector<HTMLButtonElement>(
      ".settings-archive-restore",
    );
    act(() => restoreButton?.click());

    expect(onUnarchiveRoom).toHaveBeenCalledWith(room);
  });
});
