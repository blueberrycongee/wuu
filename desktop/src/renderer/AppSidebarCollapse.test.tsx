/**
 * Sidebar collapse-state independence regression test.
 *
 * User report: collapsing 置顶, then interacting with a project, made
 * 置顶 passively expand. Root cause: the App effect that prunes
 * collapsedSidebarSectionIDs against state.projects stripped pseudo-section
 * keys whenever the project list got a fresh array identity (any runtime
 * reload), silently re-expanding those sections.
 */
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ExtensionCatalogRefreshResult,
  ExtensionInventoryRecord,
  InitializeResult,
  ServerEvent,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";

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
  WorkspaceMonacoEditor: (): JSX.Element => (
    <div className="workspace-monaco-editor" data-testid="mock-monaco-editor" />
  ),
}));

import { createDefaultApplicationTestView, registerDefaultTestSurfaces, defaultSurfaceBindings } from "./test-support/DefaultApplication";
import { translateCurrent } from "./i18n";
import * as applicationState from "./plugins/ApplicationController";
import { ApplicationShellOutlet } from "./plugins/ApplicationShell";
import { activate as activateDefaultShell } from "./plugins/DefaultApplicationShell";
import { PluginHost } from "./plugins/PluginHost";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

const workspace = "/tmp/wuu-collapse-test";

function initialized(): InitializeResult {
  return {
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
  };
}

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
  // Fresh arrays on every call — mirrors production, where each
  // project-state reload replaces state.projects with a new identity.
  const workspaceState = (): {
    projects: never[];
    active_context: { kind: "no_project"; cwd: string };
  } => ({
    projects: [],
    active_context: { kind: "no_project", cwd: workspace },
  });
  const pinnedThread: Thread = {
    id: "thread-pinned-collapse",
    preview: "Pinned collapse probe",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "idle",
    read_only: false,
    pinned: true,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
  const api = {
    listProjects: vi.fn().mockImplementation(() => Promise.resolve(workspaceState())),
    selectNoProject: vi
      .fn()
      .mockImplementation(() => Promise.resolve(workspaceState())),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listEngines: vi.fn().mockResolvedValue({ engines: [] }),
    listThreads: vi.fn().mockResolvedValue({ threads: [pinnedThread] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    getSessionOrganization: vi.fn().mockResolvedValue({
      organization: { folders: [{ id: "folder-collapse", name: "Collapse probe" }] },
    }),
    setThemePreference: vi.fn().mockResolvedValue({ ok: true }),
    getBuildInfo: vi.fn().mockResolvedValue({ desktop: { version: "test" } }),
    listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread: pinnedThread }),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter(
          (item) => item !== handler,
        );
      };
    }),
    onWindowResizeState: vi.fn(() => () => {}),
    onTerminalEvent: vi.fn(() => () => {}),
    respondToServerRequest: vi.fn().mockResolvedValue(undefined),
    rejectServerRequest: vi.fn().mockResolvedValue(undefined),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: api,
  });
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function conversationSectionHeader(): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>(
    'button[aria-label="收起 对话 的对话"], button[aria-label="展开 对话 的对话"]',
  );
}

async function clickConversationHeader(): Promise<void> {
  const header = conversationSectionHeader();
  expect(header).not.toBeNull();
  await act(async () => {
    header?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  await flushAsync();
}

describe("sidebar collapse-state independence", () => {
  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    delete document.documentElement.dataset.theme;
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("uses the selected settings surface and never restores a default after its provider unloads", async () => {
    installWuuApi();
    const controller = new applicationState.ApplicationController();
    vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
    const host = new PluginHost({ react: React, application: controller });
    await registerDefaultTestSurfaces(host);
    await host.activateGeneration({ pluginId: "shell", generation: "fixture", register: activateDefaultShell });
    await host.activateGeneration({ pluginId: "alternate-settings", generation: "fixture", register: api => {
      api.registerUIContribution({ id: "ui.settings", target: "another-shell/settings", render: () => <div data-testid="selected-settings">Alternate settings</div> });
    } });
    await act(async () => {
      root = createRoot(container);
      root.render(<ApplicationShellOutlet host={host}
        binding={{ pluginId: "shell", generation: "fixture", id: "ui.shell" }}
        surfaceBindings={{ "ui.settings": { pluginId: "alternate-settings", generation: "fixture", id: "ui.settings" } }} />);
    });
    await flushAsync();
    await act(async () => controller.invoke("settings.open", { pageId: "appearance" }));
    expect(container.querySelector('[data-testid="selected-settings"]')).not.toBeNull();
    expect(container.querySelector('[data-testid="settings-theme-light"]')).toBeNull();
    act(() => host.unload("alternate-settings"));
    expect(container.textContent).toContain("Selected UI surface is unavailable: ui.settings");
    expect(container.querySelector('[data-testid="settings-theme-light"]')).toBeNull();
  });

  it("keeps the draft worktree base through shell replacement without applying it to another project", async () => {
    installWuuApi();
    const projects = ["first", "second"].map(id => ({
      id, name: id, path: `${workspace}/${id}`,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    }));
    let activeContext = { kind: "project" as const, project_id: "first", cwd: projects[0].path };
    window.wuu.listProjects = vi.fn(async () => ({ projects, active_context: activeContext }));
    window.wuu.selectProject = vi.fn(async id => {
      activeContext = { kind: "project", project_id: id, cwd: projects.find(project => project.id === id)!.path };
      return { projects, active_context: activeContext };
    });
    window.wuu.initialize = vi.fn(async () => ({ ...initialized(), workspace_root: activeContext.cwd }));
    window.wuu.listThreads = vi.fn(async () => ({ threads: [] }));
    window.wuu.gitStatus = vi.fn(async () => ({ is_repo: true, branch: "main", branches: ["main", "feature"], dirty_count: 0, files: [] }));
    const controller = new applicationState.ApplicationController();
    const shared = vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
    const host = new PluginHost({ react: React, application: controller });
    await registerDefaultTestSurfaces(host);
    let release!: () => void, started!: () => void;
    const ready = new Promise<void>(resolve => { started = resolve; });
    const gate = new Promise<void>(resolve => { release = resolve; });
    let pending: ReturnType<PluginHost["activateGeneration"]> | undefined;
    const click = async (selector: string) => {
      const button = document.querySelector<HTMLButtonElement>(selector);
      expect(button).not.toBeNull();
      expect(button!.disabled).toBe(false);
      expect(button!.closest("[inert]")).toBeNull();
      await act(async () => button!.click());
      await flushAsync();
    };
    try {
      await host.activateGeneration({ pluginId: "shell", generation: "same-code", graphGeneration: "first", register: activateDefaultShell });
      await act(async () => {
        root = createRoot(container);
        root.render(<ApplicationShellOutlet host={host} surfaceBindings={defaultSurfaceBindings} binding={{ pluginId: "shell", generation: "same-code", id: "ui.shell" }} />);
      });
      await flushAsync();
      act(() => {
        pending = host.activateGeneration({ pluginId: "shell", generation: "same-code", graphGeneration: "second", register: async api => {
          activateDefaultShell(api);
          started();
          await gate;
        } });
      });
      await ready;
      const oldToggle = container.querySelector(".composer-worktree-toggle")!;
      await click(".composer-worktree-toggle");
      expect(oldToggle.getAttribute("aria-pressed")).toBe("true");
      await click(".composer-git-controls .hero-project-pill");
      await click('.composer-branch-menu button[title="feature"]');
      expect(container.querySelector(".composer-git-controls .hero-project-pill-text")?.textContent).toBe("feature");
      await act(async () => { release(); await pending; });
      await flushAsync();
      expect(oldToggle.isConnected).toBe(false);
      expect(container.querySelector(".composer-worktree-toggle")?.getAttribute("aria-pressed")).toBe("true");
      expect(container.querySelector(".composer-git-controls .hero-project-pill-text")?.textContent).toBe("feature");
      await click(".composer-project-control .hero-project-pill");
      await click('.composer-project-menu button[title="second"]');
      expect(controller.getState().activeContext?.cwd).toBe(projects[1].path);
      expect(container.querySelector(".composer-worktree-toggle")?.getAttribute("aria-pressed")).toBe("false");
      expect(container.querySelector(".composer-git-controls .hero-project-pill-text")?.textContent).toBe("main");
    } finally {
      release();
      await pending;
      act(() => { root?.unmount(); });
      root = null;
      await host.unloadAndWait("shell");
      shared.mockRestore();
    }
  });

  it("clears worktree selection when threads change without a mounted shell before the next draft submits", async () => {
    installWuuApi();
    const context = { kind: "project" as const, project_id: "worktree-project", cwd: workspace };
    const projects = [{ id: context.project_id, name: "Worktree project", path: workspace,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }];
    const thread: Thread = { id: "selected-thread", preview: "Existing conversation", model_provider: "fake", model: "fake-model",
      cwd: workspace, workspace_id: context.project_id, workspace_kind: "project", status: "idle",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", turns: [] };
    window.wuu.listProjects = vi.fn(async () => ({ projects, active_context: context }));
    window.wuu.selectProject = vi.fn(async () => ({ projects, active_context: context }));
    window.wuu.listThreads = vi.fn(async () => ({ threads: [] }));
    window.wuu.resumeThread = vi.fn(async () => ({ thread }));
    window.wuu.startThread = vi.fn(async () => ({ thread: { ...thread, id: "submitted-draft" } }));
    window.wuu.startTurn = vi.fn(async () => ({ turn: { id: "submitted-turn", status: "completed" as const, items_view: "full" as const, items: [] } }));
    window.wuu.gitStatus = vi.fn(async () => ({ is_repo: true, branch: "main", dirty_count: 0, files: [] }));
    const controller = new applicationState.ApplicationController();
    const shared = vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
    // DesktopApplicationRoot retains the controller independently of its shell.
    const stopController = controller.start();
    const host = new PluginHost({ react: React, application: controller });
    await registerDefaultTestSurfaces(host);
    const activate = (graph: string) => host.activateGeneration({ pluginId: "shell", generation: "same-code", graphGeneration: graph, register: activateDefaultShell });
    const render = () => root!.render(<ApplicationShellOutlet host={host} surfaceBindings={defaultSurfaceBindings} binding={{ pluginId: "shell", generation: "same-code", id: "ui.shell" }} />);
    try {
      await activate("first");
      await act(async () => { root = createRoot(container); render(); });
      await flushAsync();
      const toggle = container.querySelector<HTMLButtonElement>(".composer-worktree-toggle")!;
      expect(toggle.disabled).toBe(false);
      await act(async () => toggle.click());
      expect(toggle.getAttribute("aria-pressed")).toBe("true");
      await act(async () => root!.render(null));
      await act(async () => { await controller.invoke("thread.select", { threadId: thread.id }); });
      expect(controller.getState().thread?.id).toBe(thread.id);
      await act(async () => { await controller.invoke("workspace.select", { workspaceId: context.project_id }); });
      expect(controller.getState().thread).toBeUndefined();
      await activate("second");
      await act(async () => render());
      await flushAsync();
      const nextDraftPressed = container.querySelector(".composer-worktree-toggle")?.getAttribute("aria-pressed");
      const textarea = container.querySelector<HTMLTextAreaElement>(".composer textarea")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "Next draft after navigation");
        textarea.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await flushAsync();
      const send = container.querySelector<HTMLButtonElement>('.composer-send-button[data-wuu-state="send"]')!;
      expect(send.disabled).toBe(false);
      await act(async () => send.click());
      await flushAsync();
      expect(window.wuu.startThread).toHaveBeenCalledOnce();
      expect(vi.mocked(window.wuu.startThread).mock.calls[0][0]?.workspace).not.toBe("worktree");
      expect(nextDraftPressed).toBe("false");
    } finally {
      act(() => { root?.unmount(); });
      root = null;
      await host.unloadAndWait("shell");
      stopController();
      shared.mockRestore();
    }
  });

  // A refresh replaces the shell activation even when its code fingerprint is
  // unchanged. Preserve the selected catalog, but never replay stale navigation.
  it.each([
    { section: "skills", leave: false },
    { section: "plugins", leave: false },
    { section: "skills", leave: true },
  ] as const)("retains the latest route during $section refresh (leave=$leave)", async ({ section, leave }) => {
    installWuuApi();
    window.wuu.listSkills = vi.fn().mockResolvedValue({ skills: [] });
    let finishRefresh!: (value: ExtensionCatalogRefreshResult) => void;
    window.wuu.refreshExtensionCatalog = vi.fn(() => new Promise<ExtensionCatalogRefreshResult>(resolve => { finishRefresh = resolve; }));
    const controller = new applicationState.ApplicationController();
    const shared = vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
    const host = new PluginHost({ react: React, application: controller });
    await registerDefaultTestSurfaces(host);
    const activate = () => host.activateGeneration({ pluginId: "shell", generation: "same-code", register: activateDefaultShell });
    const click = async (selector: string) => {
      const button = container.querySelector<HTMLButtonElement>(selector);
      expect(button).not.toBeNull();
      expect(button!.disabled).toBe(false);
      await act(async () => button!.click());
      await flushAsync();
    };
    try {
      await activate();
      await act(async () => {
        root = createRoot(container);
        root.render(<ApplicationShellOutlet host={host} surfaceBindings={defaultSurfaceBindings} binding={{ pluginId: "shell", generation: "same-code", id: "ui.shell" }} />);
      });
      await flushAsync();
      await click('[data-ribbon-id="catalog"]');
      await click(`.sidebar-section-navigation > button:nth-of-type(${section === "skills" ? 2 : 1})`);
      const selectedLabel = container.querySelector('.sidebar-section-item[aria-current="page"]')?.getAttribute("aria-label");
      expect(selectedLabel).toBeTruthy();
      await click('.catalog-refresh');
      expect(container.querySelector('.catalog-refresh')?.getAttribute("aria-busy")).toBe("true");
      if (leave) await click('[data-wuu-component="conversations-section"]');
      const previousShell = container.querySelector('.app-shell');
      await act(async () => { await activate(); });
      await act(async () => { finishRefresh({ skills: [], extension_inventory: [] }); });
      await flushAsync();
      expect(window.wuu.refreshExtensionCatalog).toHaveBeenCalledOnce();
      expect(container.querySelector('.app-shell')).not.toBe(previousShell);
      if (leave) {
        expect(container.querySelector('.catalog-section-pane:not([hidden])')).toBeNull();
        await click('[data-ribbon-id="catalog"]');
      }
      expect(container.querySelector('.catalog-section-pane:not([hidden])')).not.toBeNull();
      expect(container.querySelector('.sidebar-section-item[aria-current="page"]')?.getAttribute("aria-label")).toBe(selectedLabel);
      expect(container.querySelector('.catalog-refresh')?.getAttribute("aria-busy")).toBe("false");
    } finally {
      act(() => { root?.unmount(); });
      root = null;
      await host.unloadAndWait("shell");
      shared.mockRestore();
    }
  });

  it("keeps plugin details actionable through disable and re-enable shell activations", async () => {
    installWuuApi();
    let record: ExtensionInventoryRecord = {
      id: "plugin:user:route-probe", name: "Route probe", kind: "plugin", state: "active",
      fingerprint: "unchanged", enabled: true, approval_state: "granted", runtime_state: "active",
      provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "route-probe" },
    };
    vi.mocked(window.wuu.initialize).mockResolvedValue({ ...initialized(), extension_inventory: [record] });
    window.wuu.listSkills = vi.fn().mockResolvedValue({ skills: [] });
    const controller = new applicationState.ApplicationController();
    const shared = vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
    const host = new PluginHost({ react: React, application: controller });
    await registerDefaultTestSurfaces(host);
    const activate = () => host.activateGeneration({ pluginId: "shell", generation: "same-code", register: activateDefaultShell });
    window.wuu.updateExtensionPackage = vi.fn(async ({ action }) => {
      record = { ...record, enabled: action === "enable", runtime_state: action === "enable" ? "active" : "inactive" };
      controller.updateState(current => ({ ...current, initialized: { ...current.initialized!, extension_inventory: [record] } }));
      await activate();
      return { extension_inventory: [record], skills: [] };
    });
    const click = async (selector: string) => {
      const button = container.querySelector<HTMLButtonElement>(selector);
      expect(button).not.toBeNull();
      expect(button!.disabled).toBe(false);
      await act(async () => button!.click());
      await flushAsync();
    };
    try {
      await activate();
      await act(async () => {
        root = createRoot(container);
        root.render(<ApplicationShellOutlet host={host} surfaceBindings={defaultSurfaceBindings} binding={{ pluginId: "shell", generation: "same-code", id: "ui.shell" }} />);
      });
      await flushAsync();
      await click('[data-ribbon-id="catalog"]');
      await click('.catalog-row-open');
      for (const [action, checked] of [["disable", "false"], ["enable", "true"]] as const) {
        await click('[data-wuu-component="plugin-detail"] [role="switch"]');
        expect(window.wuu.updateExtensionPackage).toHaveBeenLastCalledWith({ id: record.id, fingerprint: record.fingerprint, action });
        expect(container.querySelector('[data-wuu-component="plugin-detail"]')?.getAttribute("aria-label")).toBe(record.name);
        expect(container.querySelector('[data-wuu-component="plugin-detail"] [role="switch"]')?.getAttribute("aria-checked")).toBe(checked);
      }
      await click('.sidebar-section-navigation > button:nth-of-type(2)');
      await click('.sidebar-section-navigation > button:nth-of-type(1)');
      expect(container.querySelector('[data-wuu-component="plugin-detail"]')).toBeNull();
      await click('.catalog-row-open');
      await click('[data-testid="plugin-page-back"]');
      await act(async () => { await activate(); });
      expect(container.querySelector('[data-wuu-component="plugin-detail"]')).toBeNull();
      expect(container.querySelector('.catalog-row-open')).not.toBeNull();
    } finally {
      act(() => { root?.unmount(); });
      root = null;
      await host.unloadAndWait("shell");
      shared.mockRestore();
    }
  });

  it("keeps the account control available and dismisses its menu on collapse", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();
    const accountTrigger = () => container.querySelector<HTMLButtonElement>(".sidebar-account-trigger");
    expect(accountTrigger()).not.toBeNull();
    await act(async () => accountTrigger()?.click());
    expect(document.querySelector(".sidebar-account-menu")).not.toBeNull();

    const toggle = container.querySelector<HTMLButtonElement>(".sidebar-toggle-button");
    expect(toggle).not.toBeNull();
    await act(async () => toggle?.click());
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
    expect(accountTrigger()?.getAttribute("aria-expanded")).toBe("false");

    const expandedToggle = container.querySelector<HTMLButtonElement>(".sidebar-toggle-button");
    expect(expandedToggle).not.toBeNull();
    await act(async () => expandedToggle?.click());
    expect(accountTrigger()?.getAttribute("aria-expanded")).toBe("false");
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
  });

  it.each([
    ['folder', '[data-section-id="__wuu_folder_sort__:folder-collapse"] button[aria-expanded]'],
    ['pinned', '[data-functional-group-id="pinned"] button[aria-expanded]'],
    ['workspace', '[data-functional-group-id="workspace"] button[aria-expanded]'],
    ['scratch', 'button[aria-label="收起 对话 的对话"], button[aria-label="展开 对话 的对话"]'],
  ])("preserves the %s fold across settings visits and theme changes", async (_section, selector) => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();
    const header = () => container.querySelector<HTMLButtonElement>(selector);
    const click = async (button: HTMLButtonElement | null | undefined) => {
      expect(button).toBeTruthy();
      await act(async () => button!.click());
      await flushAsync();
    };
    expect(header()?.getAttribute("aria-expanded")).toBe("true");
    await click(header());
    expect(header()?.getAttribute("aria-expanded")).toBe("false");

    for (const theme of [null, "dark", "light"] as const) {
      await click(container.querySelector(".sidebar-account-trigger"));
      await click(document.querySelector('[data-settings-page="providers"]'));
      await act(async () => { await import("./SettingsView"); });
      expect(container.querySelector(".settings-embedded")).not.toBeNull();
      if (theme) {
        await click([...container.querySelectorAll<HTMLButtonElement>(".settings-nav button")]
          .find((button) => button.textContent === translateCurrent("settings.appearance")));
        await click(container.querySelector(`[data-testid="settings-theme-${theme}"]`));
        expect(document.documentElement.dataset.theme).toBe(theme);
        expect(window.wuu.setThemePreference).toHaveBeenLastCalledWith(theme);
      }
      await click(container.querySelector('[data-wuu-component="conversations-section"]'));
      expect(header()?.getAttribute("aria-expanded")).toBe("false");
    }
    await click(header());
    expect(header()?.getAttribute("aria-expanded")).toBe("true");
  });

  it("collapses the active 对话 section on the first header click", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "true",
    );

    await clickConversationHeader();
    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "false",
    );

    await clickConversationHeader();
    expect(conversationSectionHeader()?.getAttribute("aria-expanded")).toBe(
      "true",
    );
  });
  it("retains an explicit Codex credential switch through App and detail reopening without changing the default model", async () => {
    installWuuApi();
    const codex = { name: "codex-account", type: "openai-codex", model: "configured-model", base_url: "https://chatgpt.com/backend-api/codex", api_key_configured: true, reuse_codex_credentials: false, codex_credential_source: "wuu-auth-store", models: [{ id: "configured-model" }] };
    const config = initialized();
    vi.mocked(window.wuu.initialize).mockResolvedValue({ ...config, providers: [...config.providers!, codex] });
    const updated = { ...codex, model: "snapshot-model", reuse_codex_credentials: true, codex_credential_source: "codex-cli", models: [{ id: "configured-model" }, { id: "snapshot-model" }] };
    window.wuu.useCodexCredentials = vi.fn().mockResolvedValue({ providers: [...config.providers!, updated] });
    window.wuu.loadCodexModels = vi.fn().mockResolvedValue({ providers: [...config.providers!, updated], models: updated.models });
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    const click = async (selector: string, scope: ParentNode = container) => {
      const button = scope.querySelector<HTMLButtonElement>(selector);
      expect(button).not.toBeNull();
      await act(async () => button!.click());
      await flushAsync();
    };
    await click('.sidebar-account-trigger');
    // The account menu is portaled outside the mounted App container.
    await click('[data-settings-page="providers"]', document);
    await act(async () => { await import("./SettingsView"); });
    await click('[data-provider="codex-account"]');
    const sourceValue = () => container.querySelector('.connection-fields .model-service-status')?.textContent;
    expect(sourceValue()).toContain(translateCurrent("settings.codexSourceSaved"));
    await click('[data-testid="settings-auth-method-local"]');
    await flushAsync();
    expect(window.wuu.useCodexCredentials).toHaveBeenCalledWith("codex-account");
    expect(window.wuu.loadCodexModels).toHaveBeenCalledWith("codex-account");
    await click('[data-testid="settings-provider-back"]');
    expect(container.querySelector('[data-provider="fake"]')?.hasAttribute('data-default')).toBe(true);
    await click('[data-provider="codex-account"]');
    expect(sourceValue()).toContain(translateCurrent("settings.codexSourceLocal"));
    expect(container.querySelector('[data-testid="settings-provider-model"][data-model="configured-model"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(container.querySelector('[data-testid="settings-provider-model"][data-model="snapshot-model"]')?.getAttribute('aria-pressed')).toBe('false');
  });

  it("retains verified Grok login through App reopening without replacing the selected model or default", async () => {
    installWuuApi();
    const grok = { name: "grok-account", type: "grok-build", model: "configured-model", api_key_configured: false, models: [{ id: "configured-model" }] };
    const config = initialized();
    vi.mocked(window.wuu.initialize).mockResolvedValue({ ...config, providers: [...config.providers!, grok] });
    const updated = { ...grok, model: "snapshot-model", api_key_configured: true, models: [{ id: "configured-model" }, { id: "snapshot-model" }] };
    window.wuu.authenticateEngine = vi.fn().mockResolvedValue({ methods: [], authenticated: true });
    window.wuu.cancelEngineAuth = vi.fn().mockResolvedValue(undefined);
    window.wuu.refreshModelCatalog = vi.fn().mockResolvedValue({ providers: [...config.providers!, updated], model_count: 3 });
    await act(async () => { root = createRoot(container); root.render(await createDefaultApplicationTestView()); });
    await flushAsync();
    const click = async (selector: string, scope: ParentNode = container) => {
      const button = scope.querySelector<HTMLButtonElement>(selector);
      expect(button).not.toBeNull();
      expect(button!.disabled).toBe(false);
      await act(async () => button!.click());
      await flushAsync();
    };
    await click('.sidebar-account-trigger');
    // The account menu is portaled outside the mounted App container.
    await click('[data-settings-page="providers"]', document);
    await act(async () => { await import("./SettingsView"); });
    await click('[data-provider="grok-account"]');
    expect(window.wuu.authenticateEngine).not.toHaveBeenCalled();
    await click('[data-testid="grok-login-start"]');
    expect(window.wuu.authenticateEngine).toHaveBeenCalledExactlyOnceWith("grok", "local-cli");
    expect(window.wuu.refreshModelCatalog).toHaveBeenCalledOnce();
    expect(container.querySelector<HTMLButtonElement>('[data-testid="grok-login-local"]')?.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-make-default"]')?.disabled).toBe(false);
    await click('[data-testid="settings-provider-back"]');
    expect(container.querySelector('[data-provider="fake"]')?.hasAttribute('data-default')).toBe(true);
    await click('[data-provider="grok-account"]');
    expect(container.querySelector<HTMLButtonElement>('[data-testid="grok-login-local"]')?.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-make-default"]')?.disabled).toBe(false);
    expect(container.querySelector('[data-testid="settings-provider-model"][data-model="configured-model"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(container.querySelector('[data-testid="settings-provider-model"][data-model="snapshot-model"]')?.getAttribute('aria-pressed')).toBe('false');
  });

});
