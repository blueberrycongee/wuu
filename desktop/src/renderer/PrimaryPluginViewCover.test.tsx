import "./test-support/AbortSignal";
import { webcrypto } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { PluginGenerationApi } from "./plugins/PluginHost";
/**
 * A primary plugin page shares the conversation pane. With an empty-session
 * wallpaper the page is transparent, so the greeting and dock composer must
 * leave the pane instead of painting through Automations.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, ServerEvent, Thread, WuuDesktopApi } from "../shared/protocol";

vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn(),
    dispose: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })),
  })),
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(() => ({ fit: vi.fn() })),
}));

vi.mock("./WorkspaceMonacoEditor", () => ({
  WorkspaceMonacoEditor: (): JSX.Element => <div data-testid="mock-monaco-editor" />,
}));

import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { ApplicationShellOutlet } from "./plugins/ApplicationShell";
import { activate as activateApplication } from "./plugins/DefaultApplicationProvider";
import { activate as activateShell } from "./plugins/DefaultApplicationShell";
import { activate as activateNavigation } from "./modules/DefaultNavigationSurface";
import { activate as activateComposer } from "./modules/DefaultComposerSurface";
import { activate as activateConversation } from "./modules/DefaultConversationSurface";
import { activate as activatePreview } from "./modules/DefaultPreviewSurface";
import { activate as activateSettings } from "./modules/DefaultSettingsSurface";
let desktopWorkbenchController: import("../../../packages/plugin-sdk/src/ui-contributions").ApplicationWorkbenchV1;
let selectedView: JSX.Element | undefined;
const providers = [["wuu.default-navigation", activateNavigation], ["wuu.default-composer", activateComposer], ["wuu.default-conversation", activateConversation], ["wuu.default-preview", activatePreview], ["wuu.default-settings", activateSettings], ["wuu.default-shell", activateShell]] as const;
async function createDefaultApplicationTestView(): Promise<JSX.Element> {
  if (selectedView) return selectedView;
  await desktopPluginHost.activateGeneration({ pluginId: "wuu.default-application", generation: "fixture", graphGeneration: "test-graph", register: activateApplication });
  await desktopPluginHost.selectApplicationBinding({ pluginId: "wuu.default-application", generation: "fixture", id: "ui.application" });
  for (const [id, register] of providers) await desktopPluginHost.activateGeneration({ pluginId: id, generation: "fixture", graphGeneration: "test-graph", register });
  await desktopPluginHost.activateGeneration({ pluginId: "test-controls", generation: "fixture", graphGeneration: "test-graph", register(api) { desktopWorkbenchController = api.application.workbench; } });
  const surfaceBindings = Object.fromEntries(["navigation", "composer", "conversation", "preview", "settings"].map(name => [`ui.${name}`, { pluginId: `wuu.default-${name}`, generation: "fixture", id: `ui.${name}` }]));
  return selectedView = <ApplicationShellOutlet host={desktopPluginHost} binding={{ pluginId: "wuu.default-shell", generation: "fixture", id: "ui.shell" }} surfaceBindings={surfaceBindings} />;
}

const pluginId = "test:primary-cover";
let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

function installWindowStubs(): void {
  class MockResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

function installWuuApi(): void {
  const workspace = "/tmp/wuu-primary-plugin-cover";
  const thread: Thread = {
    id: "draft-thread-1",
    preview: "New conversation",
    title: "",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
  const initialized = (): InitializeResult => ({
    extension_inventory: [{ id: pluginId, kind: "plugin", name: "Test view", enabled: true, state: "active", approval_state: "official", fingerprint: "fixture", provenance: { kind: "plugin", source: "fixture", scope: "user", plugin_id: pluginId } }],
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspace,
    permissions: { mode: "standard" },
    providers: [
      { name: "fake", type: "openai-compatible", model: "fake-model", api_key_configured: true },
    ],
    advanced_settings: {
      max_steps: 64,
      max_context_tokens: 0,
      temperature: 0,
      disable_auto_compact: false,
    },
  });
  const api = {
    reportPluginDesktopActivation: vi.fn().mockResolvedValue(undefined),
    listProjects: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    selectNoProject: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads: [] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread: undefined }),
    startThread: vi.fn().mockResolvedValue({ thread }),
    deleteThread: vi.fn().mockResolvedValue(undefined),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({ is_repo: false, dirty_count: 0, files: [] }),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter((item) => item !== handler);
      };
    }),
    onWindowResizeState: vi.fn(() => () => {}),
    onTerminalEvent: vi.fn(() => () => {}),
    respondToServerRequest: vi.fn().mockResolvedValue(undefined),
    rejectServerRequest: vi.fn().mockResolvedValue(undefined),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("primary plugin view covers the conversation", () => {
  beforeEach(() => {
    installWindowStubs();
    installWuuApi();
    serverEventHandlers = [];
    window.localStorage.clear();
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(async () => {
    act(() => {
      for (const view of desktopWorkbenchController?.getSnapshot().views ?? []) {
        void desktopWorkbenchController.closeView(view.id);
      }
      desktopPluginHost.unload(pluginId);
      root?.unmount();
    });
    await desktopPluginHost.selectApplicationBinding(undefined);
    for (const id of [...providers.map(([id]) => id), "test-controls", "wuu.default-application"]) await desktopPluginHost.unloadAndWait(id);
    selectedView = undefined;
    root = null;
    container.remove();
    window.localStorage.clear();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("drops the empty-session greeting and dock composer while Automations is open", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    expect(container.querySelector(".empty-home")).not.toBeNull();
    expect(container.querySelector(".conversation-pane .dock-composer-wrap")).not.toBeNull();

    await act(async () => {
      await desktopPluginHost.activateGeneration({ graphGeneration: "renderer-test-graph",
        pluginId,
        generation: "one",
        register(api) {
          api.registerViewType({
            id: "catalog",
            title: "Automations",
            render: () => <article>Automation catalog</article>,
          });
        },
      });
      await desktopWorkbenchController.openPluginView(pluginId, "catalog");
    });

    const pane = container.querySelector(".conversation-pane");
    expect(pane?.hasAttribute("data-primary-plugin-view")).toBe(true);
    expect(pane?.querySelector(".plugin-workbench-view-primary")?.textContent).toContain("Automation catalog");
    expect(pane?.querySelector(".empty-home")).toBeNull();
    expect(pane?.querySelector(".dock-composer-wrap")).toBeNull();
    expect(pane?.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(true);

    await act(async () => {
      desktopWorkbenchController.deactivateRegion("primary");
    });

    expect(pane?.hasAttribute("data-primary-plugin-view")).toBe(false);
    expect(pane?.querySelector(".empty-home")).not.toBeNull();
    expect(pane?.querySelector(".dock-composer-wrap")).not.toBeNull();
    expect(pane?.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(false);
  });

  it("keeps a failed Personal recovery local and restores the existing Coding composer", async () => {
    const request = vi.fn().mockRejectedValue(new Error("main_session_recovery_required: bound history unavailable"));
    Object.assign(window.wuu, { requestPluginRuntime: request, cancelPluginRuntime: vi.fn().mockResolvedValue({ cancelled: false }) });
    const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
    const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    await act(async () => {
      await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph", pluginId, generation: "gated", register: activate });
      await desktopWorkbenchController.openPluginView(pluginId, "personal.home");
    });
    await flushAsync();
    await vi.waitFor(async () => { await flushAsync(); expect(container.textContent).toContain("main_session_recovery_required"); });
    expect(container.querySelector(".mosaic textarea")).toBeNull();
    expect(request.mock.calls.every(([params]) => params.user_space_id === "local" && params.product_domain === "personal")).toBe(true);
    await act(async () => desktopWorkbenchController.deactivateRegion("primary"));
    expect(container.querySelector(".conversation-pane .dock-composer-wrap")).not.toBeNull();
    expect(container.querySelector(".conversation-pane")?.hasAttribute("data-primary-plugin-view")).toBe(false);
    expect(window.wuu.listProjects).toHaveBeenCalled();
  });

  it("carries exact user input provenance from the production Personal composer to the main bridge", async () => {
    const main = { agent_id: "main", user_space_id: "local", name: "twin", kind: "main", main_session_id: "personal-main", status: "active", revision: 1 };
    const request = vi.fn(async (params) => ({ result: params.method === "agents.ensure_default" ? { agent: main }
      : params.method === "agents.list" ? { agents: [main], default_agent_id: "main" }
      : params.method === "agents.open" ? { agent: main, session: { state: "completed" } }
      : params.method === "agents.history" ? { records: [] }
      : params.method === "tasks.list" ? { tasks: [] }
      : params.method === "agents.proposals" ? { proposals: [] } : { state: "accepted" } }));
    Object.assign(window.wuu, { requestPluginRuntime: request, cancelPluginRuntime: vi.fn().mockResolvedValue({ cancelled: false }) });
    const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
    const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await act(async () => {
      await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph", pluginId, generation: "proof", register: activate });
      await desktopWorkbenchController.openPluginView(pluginId, "personal.home");
    });
    await flushAsync();
    const prompt = "  Remember this exactly\n";
    Object.defineProperty(crypto, "subtle", { configurable: true, value: webcrypto.subtle });
    const filePicker = container.querySelector<HTMLInputElement>('.mosaic input[type="file"]'); expect(filePicker).not.toBeNull();
    await act(async () => {
      Object.defineProperty(filePicker!, "files", { configurable: true, value: [new File(["hello"], "notes.txt", { type: "text/plain" })] });
      filePicker!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await vi.waitFor(async () => { await act(async () => {}); expect(container.querySelector('.composer-attachment-tray')).not.toBeNull(); });
    const textarea = container.querySelector<HTMLTextAreaElement>(".mosaic textarea")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, prompt);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => container.querySelector<HTMLButtonElement>('.mosaic .composer-send-button')!.click());
    const sent = request.mock.calls.map(([params]) => params).filter(params => params.method === "agents.send");
    expect(sent).toHaveLength(1);
    expect(sent[0].input.prompt).toBe(prompt);
    expect(sent[0].input.attachments).toEqual([{ sha256: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", filename: "notes.txt", media_type: "text/plain" }]);
    expect(sent[0].user_input).toEqual({ request_id: sent[0].input.request_id, prompt, attachments: [{ ...sent[0].input.attachments[0], data: "aGVsbG8=" }] });
    expect(sent[0].request_id).not.toBe(sent[0].input.request_id);
    expect(request.mock.calls.filter(([params]) => params.method !== "agents.send").every(([params]) => params.user_input === undefined)).toBe(true);
  });

  it("loads twin history through sidebar navigation and fences late reads after rapid reentry", async () => {
    const main = { agent_id: "main", user_space_id: "local", name: "twin", kind: "main", main_session_id: "personal-main", status: "active", revision: 1 };
    let resolveOld!: (value: unknown) => void;
    const oldRead = new Promise(resolve => { resolveOld = resolve; });
    let reads = 0;
    let content = "Saved twin conversation";
    const request = vi.fn(async (params) => ({ result: params.method === "agents.ensure_default" ? { agent: main }
      : params.method === "agents.open" ? { agent: main, session: { state: "completed" } }
      : params.method === "agents.history" ? ++reads === 1 ? await oldRead : { records: [{ seq: 2, role: "assistant", content }], first_seq: 1, has_earlier: false }
      : params.method === "tasks.list" ? { tasks: [] }
      : params.method === "routines.list" ? { routines: [] } : {} }));
    Object.assign(window.wuu, { requestPluginRuntime: request, cancelPluginRuntime: vi.fn().mockResolvedValue({ cancelled: false }) });
    const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
    const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    await act(async () => desktopPluginHost.activateGeneration({ graphGeneration: "test-graph", pluginId, generation: "navigation-history", contributions: { navigation: [{ id: "twin", title: "twin", view: "personal.home" }] }, register: activate }));
    const clickTwin = async () => act(async () => container.querySelector<HTMLButtonElement>(`[data-wuu-component="plugin-navigation-item"][data-wuu-plugin="${pluginId}"]`)!.click());
    const clickCoding = async () => act(async () => container.querySelector<HTMLButtonElement>('[data-ribbon-id="conversations"]')!.click());
    await clickTwin();
    await vi.waitFor(async () => { await flushAsync(); expect(container.querySelector(".plugin-conversation")?.getAttribute("data-conversation-id")).toBe("personal-main"); });
    expect(container.querySelector('.plugin-conversation [role="log"]')?.getAttribute("aria-busy")).toBe("true");
    await clickCoding();
    expect(container.querySelector(".mosaic")).toBeNull();
    await clickTwin();
    expect(container.querySelector(".mosaic-main")?.textContent).toContain("Saved twin conversation");
    await act(async () => resolveOld({ records: [{ seq: 9, role: "assistant", content: "Stale response from closed view" }], first_seq: 1, has_earlier: false }));
    expect(container.querySelector(".mosaic-main")?.textContent).not.toContain("Stale response from closed view");
    content = "New saved twin reply";
    await clickTwin();
    await clickTwin();
    expect(container.querySelector(".mosaic-main")?.textContent).toContain(content);
    expect(container.querySelectorAll('.plugin-conversation [data-role="assistant"]')).toHaveLength(1);
    await clickCoding();
    expect(container.querySelector(".mosaic")).toBeNull();
    await clickTwin();
    expect(container.querySelector(".mosaic-main")?.textContent).toContain(content);
    expect(request.mock.calls.filter(([params]) => params.method === "agents.history").every(([params]) => params.input.agent_id === main.agent_id && params.user_space_id === "local" && params.product_domain === "personal")).toBe(true);
    expect(window.wuu.resumeThread).not.toHaveBeenCalledWith(main.main_session_id);
    await act(async () => { root!.unmount(); root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    expect(container.querySelector(".mosaic-main")?.textContent).toContain(content);
    expect(container.querySelector(".plugin-conversation")?.getAttribute("data-conversation-id")).toBe("personal-main");
  });

  it("keeps existing Wuu navigation in a user-space view and restores the saved Coding panel", async () => {
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelOpen", "true");
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    const panelToggle = container.querySelector<HTMLButtonElement>('[data-wuu-component="right-sidebar-toggle"]');
    expect(panelToggle?.getAttribute("aria-pressed")).toBe("true");
    expect(container.querySelector(".conversation-search-trigger")).not.toBeNull();
    await act(async () => {
      await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph", pluginId, generation: "user-layout", register(api) {
        api.registerViewType({ id: "generic.user", title: "User dashboard", layout: "user-space", render: () => <article>Independent user view</article> });
      } });
      await desktopWorkbenchController.openPluginView(pluginId, "generic.user");
    });
    expect(container.querySelector('[data-workbench-layout="user-space"]')).not.toBeNull();
    expect(container.querySelector(".conversation-search-trigger")).not.toBeNull();
    expect(container.querySelector('[data-wuu-component="right-sidebar-toggle"]')).toBeNull();
    expect(container.querySelector(".workspace-right-panel-resizer")).toBeNull();
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "p", ctrlKey: true, bubbles: true })));
    expect(container.querySelector(".conversation-search-dialog")).not.toBeNull();
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(window.localStorage.getItem("wuu.desktop.workspaceRightPanelOpen")).toBe("true");
    const search = container.querySelector<HTMLButtonElement>(".conversation-search-trigger");
    expect(search).not.toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-ribbon-id="conversations"]')!.click());
    expect(container.querySelector('[data-workbench-layout="user-space"]')).toBeNull();
    expect(container.querySelector(".conversation-search-trigger")).not.toBeNull();
    expect(container.querySelector('[data-wuu-component="right-sidebar-toggle"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(container.querySelector(".workspace-right-panel-resizer")).not.toBeNull();
  });

  it("places the collapsed sidebar toggle after a user-space view so native drag regions cannot cover it", async () => {
    window.localStorage.setItem("wuu.desktop.sidebarCollapsed", "true");
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    await act(async () => {
      await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph", pluginId, generation: "user-sidebar-toggle", register(api) {
        api.registerViewType({ id: "generic.user", title: "User dashboard", layout: "user-space", render: () => <article>Independent user view</article> });
      } });
      await desktopWorkbenchController.openPluginView(pluginId, "generic.user");
    });

    const view = container.querySelector(".plugin-workbench-view-primary");
    const toggle = container.querySelector<HTMLButtonElement>('[data-wuu-component="sidebar-toggle"]');
    expect(view).not.toBeNull();
    expect(toggle).not.toBeNull();
    expect(view!.compareDocumentPosition(toggle!) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0);

    await act(async () => toggle!.click());
    expect(container.querySelector(".app-shell")?.classList.contains("sidebar-collapsed")).toBe(false);
  });

});
