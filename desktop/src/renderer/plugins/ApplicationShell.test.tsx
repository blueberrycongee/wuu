import { translateCurrent } from "../i18n";
import { initialState } from "../AppState";
import { resetWorkspaceBrowserNavigationForTests } from "../WorkspaceBrowserNavigation";
import { ApplicationDesktop } from "./ApplicationDesktop";
import type { HostApplicationService } from "./HostApplicationService";
import { ApplicationWebWorkspace } from "./ApplicationWebWorkspace";
import { ApplicationBrowser } from "./ApplicationBrowser";
import { ApplicationController } from "./ApplicationController.test-support";
import { ApplicationProjects } from "./ApplicationProjects";
import { ApplicationTerminals } from "./ApplicationTerminals";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { PluginDesktopActivationParams } from "../../shared/protocol";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { ApplicationCatalog } from "./ApplicationCatalog";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";
import { createApplicationSettingsControls } from "./hostApplicationSettingsControls";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { scopeApplicationSettingsControls } from "../../../../packages/plugin-sdk/src/ui";
import { retainPresentationReads } from "./PresentationApplication";
import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { SettingsControlsProvider } from "../SettingsControlsContext";
import { ContentThemeControl, ThemePreferenceControl } from "../ThemePreferenceSection";
import { MessageFlowFontSizeControl } from "../MessageFlowFontSizeSection";
import { AppearanceTypography } from "../AppearanceTypography";
import { AppContextSettingsSection } from "../AppContextSettingsSection";
import { ShellSkinSettings } from "../background/ShellSkinSettings";

function fixture(options: Partial<PluginHostOptions> = {}) {
  const listeners = new Set<() => void>();
  const service: HostApplicationService = {
    contractVersion: 1,
    workbench: new ApplicationController().workbench,
    catalog: new ApplicationCatalog({ getSource: () => ({ contextKey: "test", inventory: [] }), assertActive: () => {} }),
    get environment() { return new ApplicationController().environment; },
    get artifacts() { return new ApplicationController().artifacts; },
    setPreviewVisibility: vi.fn(),
    createComposerPresentationScope: (signal, getHost) => new ApplicationController().createComposerPresentationScope(signal, getHost),
    createDesktopScope: signal => new ApplicationDesktop().createScope(signal),
    webWorkspace: new ApplicationWebWorkspace(undefined, "linux", async () => {}),
    get onboarding() { return new ApplicationController().onboarding; },
    browser: new ApplicationBrowser({ api: {}, onActivityChanged: () => {} }),
    get fileSelection() { return new ApplicationController().fileSelection; },
    projects: new ApplicationProjects({ getSource: () => ({ contextKey: "test", enabled: false }), updateThread: () => {} }),
    terminals: new ApplicationTerminals({ unsupportedMethods: ["startTerminalSession"] } as never),
    settingsControls: createApplicationSettingsControls(),
    conversationText: { getSnapshot: () => undefined, subscribe: () => () => {} },
    previewContent: new ApplicationPreviewContent({ platform: "linux",
      listWorkspaceDirectory: async () => { throw new Error("Not configured"); },
      readWorkspaceFile: async () => { throw new Error("Not configured"); },
      revealWorkspaceItem: async () => {}, showWorkspaceItemMenu: async () => ({ action: "none" }),
    }),
    getSnapshot: vi.fn(),
    subscribe: (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    invoke: vi.fn(async () => undefined) as ApplicationServiceV1["invoke"],
  };
  return { host: new PluginHost({ react: React, application: service, ...options }), service, listeners };
}

// Failure cases: no implicit root fallback, stale handles after same-fingerprint
// reload, child ownership/unload, single-slot conflicts, and recursive slots.
describe("application shell assembly", () => {
  it("rebinds retained settings subscriptions and window actions without replaying retired authority", async () => {
    const base = createApplicationSettingsControls();
    const skins = Object.freeze({ skins: [], selected: "default" });
    const make = (mode: "light" | "dark", size: number) => {
      const lifetime = new AbortController();
      const source = { ...base, capabilities: { ...base.capabilities, themeChanges: true, appContext: true, appContextChanges: true, appContextRecording: true },
        currentAppliedTheme: () => mode, getThemePreference: vi.fn(async () => mode), getMessageFlowFontSize: vi.fn(async () => size),
        readAppearance: () => ({ ...base.appearanceDefaults, motion: mode === "dark" ? "reduce" as const : "system" as const }),
        getShellSkins: () => skins, readBackground: async () => null,
        observeAppliedTheme: vi.fn(() => vi.fn()), observeContentThemeSelection: vi.fn(() => vi.fn()),
        onThemePreferenceChange: vi.fn(() => vi.fn()), observeAppearance: vi.fn(() => vi.fn()),
        observeBackground: vi.fn(() => vi.fn()), subscribeShellSkins: vi.fn(() => vi.fn()),
        onAppContextChanged: vi.fn(() => vi.fn()),
        getAppContextState: vi.fn(async () => ({ available: true, settings: { enabled: true, include_text: true, shortcut: "Command+Shift+C" } })),
        setAppContextShortcutRecording: vi.fn(async () => { lifetime.signal.throwIfAborted(); return { ok: true }; }),
      } as unknown as ReturnType<typeof createApplicationSettingsControls> & { setAppContextShortcutRecording: ReturnType<typeof vi.fn> };
      const scoped = scopeApplicationSettingsControls(() => source, lifetime.signal, () => lifetime.signal.throwIfAborted());
      const controls = retainPresentationReads({ settingsControls: scoped } as ApplicationServiceV1, lifetime.signal).settingsControls;
      return { source, controls, lifetime };
    };
    const old = make("light", 14), next = make("dark", 18), third = make("light", 20);
    const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
    const render = (controls: typeof old.controls) => root.render(<SettingsControlsProvider controls={controls}>
      <ContentThemeControl /><ThemePreferenceControl /><MessageFlowFontSizeControl />
      <AppearanceTypography section="motion" /><AppContextSettingsSection /><ShellSkinSettings />
    </SettingsControlsProvider>);
    try {
      await act(async () => render(old.controls));
      const theme = container.querySelector('[data-testid="settings-theme-dark"]');
      const font = container.querySelector<HTMLInputElement>('[data-testid="settings-message-flow-font-size-input"]')!;
      old.lifetime.abort(new PluginLifecycleCancellationError());
      await act(async () => window.dispatchEvent(new Event("blur")));
      expect(container.querySelector('[role="alert"]')).toBeNull();
      await act(async () => render(next.controls));
      expect(next.source.observeAppliedTheme).toHaveBeenCalledTimes(2);
      expect(next.source.observeContentThemeSelection).toHaveBeenCalledOnce();
      expect(next.source.onThemePreferenceChange).toHaveBeenCalledOnce();
      expect(next.source.observeAppearance).toHaveBeenCalledOnce();
      expect(next.source.onAppContextChanged).toHaveBeenCalledOnce();
      expect(next.source.getAppContextState).toHaveBeenCalledOnce();
      expect(container.querySelector('[data-testid="settings-theme-dark"]')).toBe(theme);
      expect(theme?.getAttribute("aria-checked")).toBe("true");
      expect(container.querySelector('[data-testid="settings-message-flow-font-size-input"]')).toBe(font);
      expect(font.value).toBe("18");
      expect(container.querySelector('[data-testid="settings-motion"] [role="switch"]')?.getAttribute("aria-checked")).toBe("true");
      next.source.setAppContextShortcutRecording.mockRejectedValueOnce(new Error("native recording failed"));
      await act(async () => window.dispatchEvent(new Event("blur")));
      expect(container.querySelector('[role="alert"]')?.textContent).toBe("native recording failed");
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(font, "1");
        font.dispatchEvent(new Event("input", { bubbles: true }));
      });
      expect(font.value).toBe("1");
      await act(async () => render(third.controls));
      expect(third.source.getMessageFlowFontSize).toHaveBeenCalledOnce();
      expect(font.value).toBe("1");
    } finally { act(() => root.unmount()); next.lifetime.abort(); third.lifetime.abort(); container.remove(); }
  });

  it("keeps explicit recovery usable while a successor shell registers late", async () => {
    const { host } = fixture();
    const container = document.createElement("div"), root = createRoot(container), retry = vi.fn();
    document.body.append(container);
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "late-shell", generation: "next", id: "shell" }} pending
        pendingFallback={<button onClick={retry}>Recover unavailable presentation</button>} />));
      expect(container.textContent).toBe("Recover unavailable presentation");
      expect(document.querySelector('.view-switch-loading')).toBeNull();
      await act(async () => container.querySelector('button')!.click()); expect(retry).toHaveBeenCalledOnce();
      await act(async () => host.activateGeneration({ pluginId: "late-shell", generation: "next", register: api => {
        api.registerApplicationShell({ id: "shell", render: () => <main>Successor shell</main> });
      } }));
      expect(container.textContent).toBe("Successor shell");
    } finally { act(() => root.unmount()); container.remove(); host.unload("late-shell"); }
  });

  it("lays out translated contributions inside wrappers and retires mounted entries", async () => {
    const { host } = fixture();
    const released: string[] = [];
    const Layout = ({ items }: import("../../../../packages/plugin-sdk/src/ui").UISlotLayoutProps) => <div data-layout>
      {items.map(item => <section key={item.key} aria-label={item.title}>{item.node}</section>)}
    </div>;
    await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => {
      api.registerUISlot({ id: "tools", cardinality: "many" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="shell/tools" layout={Layout} /> });
    } });
    await host.activateGeneration({ pluginId: "tools", generation: "one", register: api => {
      api.registerLocale({ id: "labels", locale: "en", entries: { "tools.label": "Translated" } });
      api.registerCleanup(() => { released.push("provider"); });
      api.registerUIContribution({ id: "second", target: "shell/tools", order: 2, title: "Second", render: () => <button>Second action</button> });
      api.registerUIContribution({ id: "first", target: "shell/tools", title: "First", render: ({ translate }) => {
        React.useEffect(() => () => { released.push("view"); }, []);
        return <button>{translate("tools.label")}</button>;
      } });
      api.registerUIContribution({ id: "wrap", target: "shell/tools", mode: "wrap", render: ({ children }) => <aside>{children}</aside> });
    } });
    const container = document.createElement("div"); const root = createRoot(container);
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "one", id: "shell" }} />));
      expect(container.querySelector("aside > [data-layout]")).not.toBeNull();
      expect([...container.querySelectorAll("section")].map(node => node.getAttribute("aria-label"))).toEqual(["First", "Second"]);
      expect(container.textContent).toBe("TranslatedSecond action");
      await act(async () => { host.unload("tools"); });
      expect(container.querySelector("button")).toBeNull();
      expect(released).toEqual(["view", "provider"]);
    } finally { act(() => root.unmount()); host.unload("tools"); host.unload("shell"); }
  });

  it("composes explicit wrappers around a slot, preserves children on failure, and drains retired views", async () => {
    const { host } = fixture();
    const cleanup: string[] = [];
    await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => {
      api.registerUISlot({ id: "body", cardinality: "one" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name="shell/body" context={{ label: "content" }} fallback={<span>native</span>} /> });
    } });
    await host.activateGeneration({ pluginId: "content", generation: "one", register: api => {
      api.registerUIContribution({ id: "body", target: "shell/body", render: ({ context }) => <span>{String(context.label)}</span> });
    } });
    const wrapper = (generation: string, broken = false) => host.activateGeneration({ pluginId: "frame", generation, register: api => {
      api.registerCleanup(() => { cleanup.push(`provider:${generation}`); });
      api.registerUIContribution({ id: "frame", target: "shell/body", mode: "wrap", render: ({ children }) => {
        React.useEffect(() => () => { cleanup.push(`view:${generation}`); }, []);
        if (broken) throw new Error("frame failed");
        return <section data-frame={generation}>{children}</section>;
      } });
    } });
    await wrapper("one");
    const container = document.createElement("div"); const root = createRoot(container);
    const logging = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "one", id: "shell" }} />));
      expect(container.querySelector("section")?.textContent).toBe("content");
      await act(async () => { await wrapper("two", true); });
      expect(container.textContent).toBe("content");
      expect(cleanup.indexOf("view:one")).toBeLessThan(cleanup.indexOf("provider:one"));
      expect(host.getGenerationDiagnostics("frame", "two").some(item => item.kind === "render")).toBe(true);
      await act(async () => { host.unload("content"); host.unload("frame"); });
      expect(container.textContent).toBe("native");
    } finally { act(() => root.unmount()); host.unload("shell"); host.unload("content"); host.unload("frame"); logging.mockRestore(); }
  });

  it("rejects wrappers for graph-selected surfaces without displacing the active contribution", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => {
      api.registerUISlot({ id: "navigation", cardinality: "one", service: "ui.navigation" });
    } });
    await expect(host.activateGeneration({ pluginId: "wrapper", generation: "one", register: api => {
      api.registerUIContribution({ id: "wrap", target: "shell/navigation", mode: "wrap", render: ({ children }) => children });
    } })).rejects.toThrow("Selected UI surfaces cannot be wrapped");
    expect(host.hasActivePlugin("shell")).toBe(true);
  });

  it("renders owner-local slots using the manifest namespace of an installed shell", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "user:custom-shell", manifestId: "custom-shell", generation: "one", register: api => {
      api.registerUISlot({ id: "body", cardinality: "one" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <slots.Slot name={`${api.manifestId}/body`} /> });
    } });
    await host.activateGeneration({ pluginId: "body", generation: "one", register: api => {
      api.registerUIContribution({ id: "content", target: "custom-shell/body", render: () => <span>Installed shell body</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "user:custom-shell", generation: "one", id: "shell" }} />));
      expect(container.textContent).toBe("Installed shell body");
      await act(async () => host.unload("user:custom-shell"));
      expect(container.textContent).toContain(translateCurrent("plugins.applicationUnavailable"));
    } finally { act(() => root.unmount()); host.unload("body"); host.unload("user:custom-shell"); }
  });

  it("renders contributions and fallback only within declared non-service slots", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "custom-shell", generation: "one", register: api => {
      api.registerUISlot({ id: "conversation.message.before", cardinality: "many" });
      api.registerUISlot({ id: "workspace.header", cardinality: "one", service: "ui.navigation" });
      api.registerApplicationShell({ id: "shell", render: ({ slots }) => <>
        <slots.Slot name="custom-shell/conversation.message.before" fallback={<span>Native fallback</span>} />
        <slots.Slot name="custom-shell/workspace.header" fallback={<span>Forbidden service fallback</span>} />
        <slots.Slot name="missing/conversation.message.before" fallback={<span>Forbidden missing fallback</span>} />
      </> });
    } });
    await host.activateGeneration({ pluginId: "public", generation: "one", register: api => {
      api.registerUIContribution({ id: "before", target: "custom-shell/conversation.message.before", render: () => <span>Public before</span> });
    } });
    const element = document.createElement("div"); const root = createRoot(element);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "custom-shell", generation: "one", id: "shell" }} />));
      expect(element.textContent).toContain("Public before");
      expect(element.textContent).not.toContain("Native fallback");
      expect(element.textContent).not.toContain("Forbidden");
      await act(async () => { host.unload("public"); });
      expect(element.textContent).toContain("Native fallback");
      expect(element.textContent).not.toContain("Forbidden");
    } finally { act(() => root.unmount()); host.unload("custom-shell"); host.unload("public"); }
  });

  it("retains the declaring slot service and fences legacy view hosts across same-code replacement", async () => {
    window.wuu = {} as never;
    let finish!: (value: string) => void;
    const execute = vi.fn().mockResolvedValue(undefined);
    const createViewHost = vi.fn(() => ({ getStorage: vi.fn(() => new Promise<string>(resolve => { finish = resolve; })),
      setStorage: vi.fn().mockResolvedValue(undefined), getSetting: vi.fn().mockResolvedValue("setting"), executeCommand: execute,
      openView: vi.fn().mockResolvedValue(undefined), closeView: vi.fn().mockResolvedValue(undefined) }));
    const { host, service } = fixture();
    const workbench = Object.create(service.workbench);
    workbench.createViewHostAPI = createViewHost;
    Object.defineProperty(service, "workbench", { value: workbench });
    let application!: ApplicationServiceV1;
    const activate = () => host.activateGeneration({ pluginId: "legacy-source", generation: "same", register: api => {
      application = api.application; api.registerUISlot({ id: "content.preview", cardinality: "one" });
    } });
    await activate();
    expect(host.getUISlot("legacy-source/content.preview")?.application).toBe(application);
    const old = application.workbench.createViewHostAPI({ id: "tab", pluginId: "legacy-source", generation: "same", viewTypeId: "view", region: "primary", persistence: "session", context: {} });
    const reading = old.getStorage("key");
    const rejected = expect(reading).rejects.toThrow();
    await activate(); finish("old value"); await rejected;
    await expect(old.executeCommand("command")).rejects.toThrow(); expect(execute).not.toHaveBeenCalled();
    const next = application.workbench.createViewHostAPI({ id: "tab", pluginId: "legacy-source", generation: "same", viewTypeId: "view", region: "primary", persistence: "session", context: {} });
    await next.executeCommand("command"); expect(execute).toHaveBeenCalledOnce();
    host.unload("legacy-source");
  });

  it("releases preview visibility on retirement without clearing a successor owner", async () => {
    window.wuu = { openExternal: vi.fn().mockResolvedValue(undefined) } as never;
    resetWorkspaceBrowserNavigationForTests();
    const controller = new ApplicationController({ ...initialState, activeContext: { kind: "no_project", cwd: "/fixture" } });
    const host = new PluginHost({ react: React, application: controller });
    let old!: ApplicationServiceV1, next!: ApplicationServiceV1;
    try {
      await host.activateGeneration({ pluginId: "preview-owner", generation: "one", register: api => { old = api.application; } });
      await old.invoke("preview.open-tool", { toolId: "terminal" });
      await old.invoke("preview.set-visibility", { open: true });
      await host.activateGeneration({ pluginId: "preview-owner", generation: "two", register: api => { next = api.application; } });
      await expect(old.invoke("preview.set-visibility", { open: false })).rejects.toThrow();
      await next.invoke("preview.open-url", { url: "https://example.test" });
      expect(next.getSnapshot().previewWorkspace.activeTabId).toBe("browser");
      let other!: ApplicationServiceV1;
      await host.activateGeneration({ pluginId: "other-preview", generation: "one", register: api => { other = api.application; } });
      await next.invoke("preview.set-visibility", { open: true });
      await other.invoke("preview.set-visibility", { open: true });
      await other.invoke("preview.open-tool", { toolId: "terminal" });
      host.unload("preview-owner");
      await other.invoke("preview.open-url", { url: "https://example.test/next" });
      expect(other.getSnapshot().previewWorkspace.activeTabId).toBe("terminal");
      await other.invoke("preview.set-visibility", { open: false });
      await other.invoke("preview.open-url", { url: "https://example.test/hidden" });
      expect(other.getSnapshot().previewWorkspace.activeTabId).toBe("browser");
    } finally { host.unload("preview-owner"); host.unload("other-preview"); resetWorkspaceBrowserNavigationForTests(); }
  });

  it("releases late artifact leases through their captured owner after UI replacement", async () => {
    const { host, service } = fixture();
    let finish!: (value: { url: string; leaseId: string }) => void;
    const releaseSource = vi.fn();
    Object.defineProperty(service, "artifacts", { value: { acquireSource: () => new Promise(resolve => { finish = resolve; }), releaseSource } });
    let application!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "artifacts", generation: "one", register: api => { application = api.application; } });
    const opening = application.artifacts.acquireSource({ id: "item", name: "file", mimeType: "application/pdf", text: "bytes" });
    const rejected = expect(opening).rejects.toThrow();
    await host.activateGeneration({ pluginId: "artifacts", generation: "two", register: () => {} });
    finish({ url: "blob:fixture", leaseId: "late" }); await rejected;
    expect(releaseSource).toHaveBeenCalledExactlyOnceWith("late");
    application.artifacts.releaseSource("late"); expect(releaseSource).toHaveBeenCalledOnce();
  });

  it("gives each replacement UI its own native window scope", async () => {
    const { host, service } = fixture();
    const signals: AbortSignal[] = [];
    const factory = vi.fn((signal: AbortSignal) => { signals.push(signal); return { close: async () => signal.throwIfAborted() }; });
    Object.defineProperty(service, "createDesktopScope", { value: factory });
    let first!: ApplicationServiceV1;
    let second!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "desktop", generation: "one", register: api => { first = api.application; } });
    const old = first.desktop; expect(first.desktop).toBe(old);
    await host.activateGeneration({ pluginId: "desktop", generation: "two", register: api => { second = api.application; } });
    const next = second.desktop;
    expect(next).not.toBe(old); expect(factory).toHaveBeenCalledTimes(2);
    expect(signals[0].aborted).toBe(true); expect(signals[1].aborted).toBe(false);
    await expect(old.close!()).rejects.toThrow(); await next.close!();
  });

  it("owns late web pages until generation cleanup completes", async () => {
    const { host, service } = fixture();
    let finish!: () => void;
    const dispose = vi.fn().mockResolvedValue(undefined);
    const owner = { open: () => new Promise<void>(resolve => { finish = resolve; }), dispose };
    Object.defineProperty(service, "webWorkspace", { value: { fork: () => owner } });
    let application!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "web", generation: "one", register: api => { application = api.application; } });
    const opening = application.webWorkspace.open({ id: "site", url: "https://example.test" });
    const rejected = expect(opening).rejects.toThrow();
    await host.activateGeneration({ pluginId: "web", generation: "two", register: () => {} });
    expect(dispose).toHaveBeenCalledOnce();
    finish(); await rejected;
    await application.webWorkspace.dispose();
    expect(dispose).toHaveBeenCalledOnce();
  });

  it("releases browser presentation leases when the selected generation retires", async () => {
    const { host, service } = fixture();
    const dispose = vi.fn(), reportBounds = vi.fn();
    Object.defineProperty(service, "browser", { value: { available: true, acquireSurface: () => ({ target: { workdir: "/fixture", tabID: "tab" }, dispose, reportBounds }) } });
    let application!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "browser", generation: "one", register: api => { application = api.application; } });
    const handle = application.browser.acquireSurface({ workdir: "/fixture", tabID: "tab" });
    handle.reportBounds(null);
    await host.activateGeneration({ pluginId: "browser", generation: "two", register: () => {} });
    expect(dispose).toHaveBeenCalledOnce();
    expect(() => handle.reportBounds(null)).toThrow();
    handle.dispose();
    expect(dispose).toHaveBeenCalledOnce();
    expect(reportBounds).toHaveBeenCalledOnce();
  });

  it("cleans up a terminal created after its selected generation retires", async () => {
    const { host, service } = fixture();
    let finish!: (value: { id: string; cwd: string; shell: string; started_at: string }) => void;
    const stop = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(service, "terminals", { value: { capabilities: { createTerminal: true },
      start: () => new Promise(resolve => { finish = resolve; }), stop } });
    let application!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "terminal", generation: "one", register: api => { application = api.application; } });
    const starting = application.terminals.start({ cwd: "/fixture" });
    const rejected = expect(starting).rejects.toThrow();
    await host.activateGeneration({ pluginId: "terminal", generation: "two", register: () => {} });
    finish({ id: "late", cwd: "/fixture", shell: "sh", started_at: "now" });
    await rejected;
    expect(stop).toHaveBeenCalledExactlyOnceWith("late");
  });

  it.each(["shell/navigation", "canonical/navigation"])("selects an independently owned surface from %s and fails visibly when its provider unloads", async target => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "root", register: api => {
      api.registerUISlot({ id: "navigation", cardinality: "one", service: "ui.navigation" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <slots.Slot name="shell/navigation" fallback={<span>Implicit default</span>} /> });
    } });
    for (const pluginId of ["default", "alternate"]) await host.activateGeneration({ pluginId, generation: "v1", register: api => {
      api.registerUIContribution({ id: "ui.navigation", target, render: () => <span>{pluginId} navigation</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    const render = (pluginId: string) => act(() => root.render(<ApplicationShellOutlet host={host}
      binding={{ pluginId: "shell", generation: "root", id: "ui.shell" }}
      surfaceBindings={{ "ui.navigation": { pluginId, generation: "v1", id: "ui.navigation" } }} />));
    render("alternate");
    expect(container.textContent).toBe("alternate navigation");
    render("default");
    expect(container.textContent).toBe("default navigation");
    act(() => host.unload("default"));
    expect(container.textContent).toBe("");
    expect(container.textContent).not.toContain("Implicit default");
    act(() => host.unload("shell"));
    expect(container.textContent).toContain(translateCurrent("plugins.applicationUnavailable"));
    act(() => root.unmount());
  });

  it.each(["root", "nested"])("reports %s render failure without a healthy view overwriting it", async (target) => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    const broken = () => { throw new Error("private render detail"); };
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: target === "root" ? broken : ({ slots }) => <slots.Slot name="shell/body" /> });
    } });
    if (target === "nested") await host.activateGeneration({ pluginId: "child", generation: "source", graphGeneration: "graph", register: api => {
      api.registerUIContribution({ id: "broken", target: "shell/body", order: 0, render: broken });
      api.registerUIContribution({ id: "healthy", target: "shell/body", order: 1, render: () => <span>Healthy sibling</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    const logging = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "source", id: "ui.shell" }} />));
      await host.flushActivationReports();
      const owner = target === "root" ? "shell" : "child";
      const reports = report.mock.calls.map(call => call[0]).filter(item => item.id === owner);
      expect(reports[0]).toMatchObject({ phase: "registration", status: "active", graph_generation: "graph", activation: 1 });
      expect(reports.at(-1)).toMatchObject({ phase: "render", status: "failed" });
      if (target === "root") expect(reports.some(item => item.phase === "render" && item.status === "active")).toBe(false);
      expect(JSON.stringify(reports)).not.toContain("private render detail");
      expect(container.textContent).not.toContain("private render detail");
      if (target === "nested") expect(container.querySelector("[role=alert]")).toBeNull();
    } finally { act(() => root.unmount()); logging.mockRestore(); }
  });

  it("reports provider cleanup after view cleanup and preserves cleanup failures", async () => {
    const order: string[] = [];
    const report = vi.fn(async (params: PluginDesktopActivationParams) => { order.push(`${params.phase}:${params.status}`); });
    const { host } = fixture({ reportActivation: report });
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: api => {
      api.registerCleanup(() => { order.push("provider-cleanup"); throw new Error("private cleanup detail"); });
      api.registerApplicationShell({ id: "ui.shell", render: () => {
        React.useEffect(() => () => { order.push("view-cleanup"); }, []);
        return <main>Mounted</main>;
      } });
    } });
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "source", id: "ui.shell" }} />));
    await act(async () => { host.unload("shell"); });
    await host.flushActivationReports();
    expect(order.indexOf("view-cleanup")).toBeLessThan(order.indexOf("provider-cleanup"));
    expect(order.at(-1)).toBe("cleanup:failed");
    expect(JSON.stringify(report.mock.calls)).not.toContain("private cleanup detail");
    act(() => root.unmount());
  });

  it("reports failed registration before cleanup and numbers same-code retries", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    await expect(host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => { throw new Error("broken"); } })).rejects.toThrow("broken");
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await host.flushActivationReports();
    expect(report.mock.calls.map(call => call[0])).toMatchObject([
      { activation: 1, phase: "registration", status: "failed" },
      { activation: 1, phase: "cleanup", status: "disposed" },
      { activation: 2, phase: "registration", status: "active" },
    ]);
  });

  it("does not send late disposal of a retained activation after the replacement registers", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    const { host } = fixture({ reportActivation: report });
    const activate = () => host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await activate();
    const release = host.retainGeneration("shell", "source");
    await activate();
    await host.flushActivationReports();
    release(); await Promise.resolve(); await host.flushActivationReports();
    const reports = report.mock.calls.map(call => call[0]);
    expect(reports.at(-1)).toMatchObject({ activation: 2, phase: "registration", status: "active" });
    expect(reports.filter(item => item.activation === 1 && item.phase === "cleanup")).toHaveLength(0);
  });

  it("clears a reporting failure only after that module registers successfully again", async () => {
    const report = vi.fn(async (_params: PluginDesktopActivationParams) => {});
    report.mockRejectedValueOnce(new Error("temporary failure"));
    const { host } = fixture({ reportActivation: report });
    const activate = (pluginId: string) => host.activateGeneration({ pluginId, generation: "source", graphGeneration: "graph", register: () => {} });
    await activate("failed"); await host.flushActivationReports();
    await activate("healthy"); await host.flushActivationReports();
    expect(host.getActivationReportFailure()).toContain("temporary failure");
    await activate("failed"); await host.flushActivationReports();
    expect(host.getActivationReportFailure()).toBeUndefined();
  });

  it("keeps committed UI live but exposes a mandatory ACK transport failure", async () => {
    const { host } = fixture({ reportActivation: async () => { throw new Error("bridge unavailable"); } });
    await host.activateGeneration({ pluginId: "shell", generation: "source", graphGeneration: "graph", register: () => {} });
    await host.flushActivationReports();
    expect(host.hasActivePlugin("shell")).toBe(true);
    expect(host.getActivationReportFailure()).toContain("bridge unavailable");
  });


  it("renders only the exact selected provider and removes its UI on unload", async () => {
    const { host } = fixture();
    for (const id of ["default", "research"]) await host.activateGeneration({
      pluginId: id, generation: "v1", register: api => {
        api.registerApplicationShell({ id: "ui.shell", render: () => <main>{id}</main> });
      },
    });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "research", generation: "v1", id: "ui.shell" }} />));
      expect(container.querySelector("main")?.textContent).toBe("research");
      act(() => host.unload("research"));
      expect(container.querySelector("main")).toBeNull();
      expect(container.textContent).not.toContain("default");
      expect(container.querySelector('[role="alert"]')).not.toBeNull();
    } finally { act(() => root.unmount()); }
  });

  it("aborts settings reads and subscriptions with the generation and fences captured methods", async () => {
    const { host, service } = fixture();
    let notify!: () => void;
    const stopped = vi.fn();
    let finish!: (value: { servers: [] }) => void;
    const list = vi.spyOn(service.settingsControls, "listMCPServers").mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    vi.spyOn(service.settingsControls, "subscribeRemote").mockImplementation(listener => { notify = listener; return stopped; });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "settings", generation: "v1", register: value => { api = value; } });
    const controls = api.application.settingsControls;
    const listener = vi.fn();
    const unsubscribe = controls.subscribeRemote(listener);
    const capturedRead = controls.listMCPServers;
    const pending = capturedRead();
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "settings", generation: "v2", register: () => {} });
    await rejected;
    notify();
    expect(listener).not.toHaveBeenCalled();
    expect(stopped).toHaveBeenCalledTimes(1);
    unsubscribe();
    expect(stopped).toHaveBeenCalledTimes(1);
    finish({ servers: [] });
    await expect(capturedRead()).rejects.toThrow(/no longer active/);
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("fences nested preferences, deferred background updates and typed subscriptions on replacement", async () => {
    const { host, service } = fixture();
    const controls = service.settingsControls;
    const setAlias = vi.fn();
    const stop = vi.fn();
    let notify!: (theme: "light" | "dark") => void;
    let update!: Parameters<typeof controls.updateBackground>[0];
    let finish!: () => void;
    Object.defineProperty(controls, "connectionDisplay", { value: { ...controls.connectionDisplay, setAlias } });
    vi.spyOn(controls, "observeAppliedTheme").mockImplementation(listener => { notify = listener; return stop; });
    vi.spyOn(controls, "updateBackground").mockImplementation(callback => {
      update = callback; return new Promise(resolve => { finish = resolve; });
    });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "settings", generation: "v1", register: value => { api = value; } });
    const scoped = api.application.settingsControls;
    const capturedAlias = scoped.connectionDisplay.setAlias;
    const listener = vi.fn();
    const unsubscribe = scoped.observeAppliedTheme(listener);
    notify("dark"); expect(listener).toHaveBeenCalledWith("dark");
    const updater = vi.fn(() => null);
    const pending = scoped.updateBackground(updater);
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "settings", generation: "v2", register: () => {} });
    await rejected;
    expect(() => capturedAlias("provider", "Old")).toThrow();
    expect(setAlias).not.toHaveBeenCalled();
    expect(() => update(null)).toThrow(); expect(updater).not.toHaveBeenCalled();
    notify("light"); expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe(); expect(stop).toHaveBeenCalledTimes(1);
    finish();
  });

  it("rejects a removed preview generation promptly and fences its captured file methods", async () => {
    const { host, service } = fixture();
    let finish!: (value: Awaited<ReturnType<typeof service.previewContent.readFile>>) => void;
    let sourceSignal!: AbortSignal;
    const read = vi.spyOn(service.previewContent, "readFile").mockImplementation((_target, options) => {
      sourceSignal = options!.signal!;
      return new Promise(resolve => { finish = resolve; });
    });
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "preview", generation: "v1", register: value => { api = value; } });
    const captured = api.application.previewContent.readFile;
    const pending = captured({ root: "/workspace", path: "file.txt" });
    const rejected = expect(pending).rejects.toThrow();
    await host.activateGeneration({ pluginId: "preview", generation: "v2", register: () => {} });
    await rejected;
    expect(sourceSignal.aborted).toBe(true);
    await expect(captured({ root: "/workspace", path: "other.txt" })).rejects.toThrow();
    expect(read).toHaveBeenCalledTimes(1);
    finish({ root: "/workspace", path: "file.txt", absolute_path: "/workspace/file.txt", size_bytes: 1, mtime_ms: 0, sha256: "digest", binary: false, truncated: false });
  });

  it("cancels catalog reads on caller abort without admitting a later generation effect", async () => {
    const { host, service } = fixture();
    let finish!: (value: readonly []) => void;
    const list = vi.spyOn(service.catalog, "listSkills").mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const remove = vi.spyOn(service.catalog, "remove");
    let api!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "catalog", generation: "v1", register: value => { api = value; } });
    const lifetime = new AbortController();
    const pending = api.application.catalog.listSkills({ signal: lifetime.signal });
    const rejected = expect(pending).rejects.toThrow();
    lifetime.abort(); await rejected;
    finish([]); expect(list).toHaveBeenCalledTimes(1);
    const captured = api.application.catalog.remove;
    await host.activateGeneration({ pluginId: "catalog", generation: "v2", register: () => {} });
    await expect(captured("package")).rejects.toThrow();
    expect(remove).not.toHaveBeenCalled();
  });

  it("disposes subscriptions and fences old application handles even after same-code reactivation", async () => {
    const { host, service, listeners } = fixture();
    let old!: PluginGenerationApi;
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => { old = api; } });
    old.application.subscribe(() => {});
    expect(listeners.size).toBe(1);
    await old.application.invoke("composer.set-draft", { text: "hello" });
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: () => {} });
    expect(listeners.size).toBe(0);
    await expect(old.application.invoke("composer.submit", { ownerTabId: "draft:test" })).rejects.toThrow(/no longer active/);
    expect(service.invoke).toHaveBeenCalledTimes(1);
  });

  it.each(["user", "project"])("composes manifest-named slots for %s installations and removes only their owner", async (scope) => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: `plugin:${scope}:shell`, manifestId: "shell", generation: "v1", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <main><slots.Slot name="shell/body" /></main> });
    } });
    await host.activateGeneration({ pluginId: `plugin:${scope}:panel`, manifestId: "panel", generation: "v1", register: api => {
      api.registerUISlot({ id: "children", cardinality: "many" });
      api.registerUIContribution({ id: "panel", target: "shell/body", render: ({ slots }) => <section>Panel<slots.Slot name="panel/children" /></section> });
    } });
    await host.activateGeneration({ pluginId: `plugin:${scope}:evidence`, manifestId: "evidence", generation: "v1", register: api => {
      api.registerUIContribution({ id: "panel", target: "panel/children", render: () => <span>Evidence</span> });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: `plugin:${scope}:shell`, generation: "v1", id: "ui.shell" }} />));
      expect(container.textContent).toBe("PanelEvidence");
      act(() => host.unload(`plugin:${scope}:evidence`));
      expect(container.textContent).toBe("Panel");
      act(() => host.unload(`plugin:${scope}:panel`));
      expect(container.textContent).toBe("");
    } finally { act(() => root.unmount()); }
  });

  it("rejects conflicting single-slot staging without disposing the previous provider", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "owner", generation: "v1", register: api => {
      api.registerUISlot({ id: "only", cardinality: "one" });
    } });
    const render = () => <span>One</span>;
    await host.activateGeneration({ pluginId: "one", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } });
    await expect(host.activateGeneration({ pluginId: "two", generation: "v1", register: api => {
      api.registerUIContribution({ id: "view", target: "owner/only", render });
    } })).rejects.toThrow(/conflict/i);
    expect(host.getUIContributions("owner/only").map(item => item.pluginId)).toEqual(["one"]);
  });

  it("remounts a nested contribution when the same module fingerprint is reactivated", async () => {
    const { host } = fixture();
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => {
      api.registerUISlot({ id: "body", cardinality: "many" });
      api.registerApplicationShell({ id: "ui.shell", render: ({ slots }) => <slots.Slot name="shell/body" /> });
    } });
    const mounted = vi.fn();
    const cleanup = vi.fn();
    const View = () => {
      React.useEffect(() => { mounted(); return cleanup; }, []);
      return <span>Evidence</span>;
    };
    const activate = () => host.activateGeneration({ pluginId: "child", generation: "same-code", register: api => {
      api.registerUIContribution({ id: "evidence", target: "shell/body", render: View });
    } });
    await activate();
    const container = document.createElement("div");
    const root = createRoot(container);
    try {
      act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "v1", id: "ui.shell" }} />));
      await act(async () => { await activate(); });
      expect(container.textContent).toBe("Evidence");
      expect(cleanup).toHaveBeenCalledTimes(1);
      expect(mounted).toHaveBeenCalledTimes(2);
    } finally { act(() => root.unmount()); }
  });
  it("retains a provider until mounted shell effects finish cleanup", async () => {
    const { host } = fixture();
    const order: string[] = [];
    await host.activateGeneration({ pluginId: "shell", generation: "v1", register: api => {
      api.registerCleanup(() => { order.push("provider"); });
      api.registerApplicationShell({ id: "ui.shell", render: () => {
        React.useEffect(() => () => { order.push("view"); }, []);
        return <main>Mounted</main>;
      } });
    } });
    const container = document.createElement("div");
    const root = createRoot(container);
    act(() => root.render(<ApplicationShellOutlet host={host} binding={{ pluginId: "shell", generation: "v1", id: "ui.shell" }} />));
    let cleanup: Promise<void> | undefined;
    act(() => { cleanup = host.unloadAndWait("shell"); });
    await cleanup;
    expect(order).toEqual(["view", "provider"]);
    act(() => root.unmount());
  });

});
