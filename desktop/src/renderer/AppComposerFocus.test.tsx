import * as composerMessages from "./ComposerMessages";
import * as React from "react";
import { act, useEffect, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  DesktopProject,
  EngineListResult,
  ExtensionInventoryRecord,
  InitializeResult,
  RuntimeContext,
  ServerEvent,
  Thread,
  Turn,
  WuuDesktopApi,
} from "../shared/protocol";

const composerFixture = vi.hoisted(() => ({ real: false }));

vi.mock("./ComposerView", async (importOriginal) => {
  const original = await importOriginal<typeof import("./ComposerView")>();
  type ComposerProps = ComponentProps<typeof original.Composer>;
  return {
    ...original,
    Composer: (props: ComposerProps): JSX.Element => {
      const variant = props.variant ?? "dock";
      // Match the real Composer's input-critical local value. App publishes
      // textarea drafts after an idle window, so a mock that reads only the
      // parent prop cannot exercise immediate Enter or slash commands.
      const [prompt, setLocalPrompt] = useState(props.prompt);
      useEffect(
        () => setLocalPrompt(props.prompt),
        [props.prompt, props.promptRevision],
      );
      if (composerFixture.real) return <original.Composer {...props} />;
      const label = props.mainConversation
        ? `main composer ${variant}`
        : "side composer";
      return (
        <div
          data-can-select-workspace={props.canSelectWorkspace}
          data-queued={props.queuedMessages.map((message) => message.text).join("|")}
          data-send-disabled={Boolean(props.sendDisabled || props.draftPending)}
          data-running={props.running}
          data-draft-pending={props.draftPending}
          data-main-conversation-composer={
            props.mainConversation ? variant : undefined
          }
        >
          {props.continuation && <button aria-label="continue-probe" onClick={() => props.continuation?.continue()}>continue</button>}
          <button aria-label="stop-probe" onClick={props.onInterrupt}>stop</button>
          <button aria-label="select-wuu-engine-probe" onClick={() => props.onSelectEngine?.("wuu")}>Wuu</button>
          {props.queuedMessages.map((message) => (
            <span key={message.id}>
              <button aria-label={`remove ${message.text}`} onClick={() => props.onRemoveQueuedMessage(message.id)}>remove</button>
              <button aria-label={`edit ${message.text}`} onClick={() => props.onEditQueuedMessage(message.id)}>edit</button>
            </span>
          ))}
          <textarea
            aria-label={label}
            value={prompt}
            onChange={(event) => {
              const value = event.currentTarget.value;
              setLocalPrompt(value);
              props.setPrompt(value);
            }}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              if (prompt.trim() === "/new") {
                props.onStartNewThread();
              } else if (prompt.trim() === "/side") {
                props.onOpenSideThread?.();
              } else if (!props.sendDisabled && !props.draftPending) {
                props.onSend(prompt);
              }
            }}
          />
        </div>
      );
    },
  };
});

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
  WorkspaceMonacoEditor: (): JSX.Element => <div />,
}));

import { createDefaultTestHost, createDefaultApplicationTestView, registerDefaultTestSurfaces, defaultSurfaceBindings } from "./test-support/DefaultApplication";
import { translateCurrent } from "./i18n";
import { permissionModeOption } from "./ComposerRuntimeMenus";
import * as applicationState from "./plugins/ApplicationController.test-support";
import { TestApplicationShellOutlet as ApplicationShellOutlet } from "./test-support/DefaultApplication";
import { activate as activateDefaultShell } from "./plugins/DefaultApplicationShell";
import { DesktopPluginRuntime } from "./plugins/DesktopPluginRuntime";
import { PluginHost, type PluginGenerationApi } from "./plugins/PluginHost";

const scratchCwd = "/tmp/wuu-composer-focus/scratch";
const workspaceCwd = "/tmp/wuu-composer-focus/project";
const project: DesktopProject = {
  id: "project-focus",
  name: "Focus Project",
  path: workspaceCwd,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let releaseWorkspaceSelection: (() => void) | null = null;
let releaseThreadStart: (() => void) | null = null;
let resizeCallbacks = new Set<ResizeObserverCallback>();

function initialized(cwd: string): InitializeResult {
  return {
    extension_inventory: [{
      id: "plugin:bundled:side", kind: "plugin", name: "Side", state: "active", enabled: true,
      approval_state: "official", runtime_state: "active",
      provenance: { kind: "plugin", source: "bundled", scope: "bundled", plugin_id: "side", official: true },
      contributions: { commands: [{ id: "side", title: "Side chat", kind: "side_conversation", template: "Side policy" }] },
    }],
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    variant: "high",
    effort: "high",
    workspace_root: cwd,
    permissions: { mode: "standard" },
    providers: [
      {
        name: "fake",
        type: "openai-compatible",
        model: "fake-model",
        api_key_configured: true,
      },
    ],
    advanced_settings: {
      max_steps: 64,
      max_context_tokens: 0,
      temperature: 0,
      disable_auto_compact: false,
    },
  };
}

function persistedThread(): Thread {
  return {
    id: "thread-focus",
    preview: "focus continuity",
    model_provider: "fake",
    model: "fake-model",
    cwd: scratchCwd,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    turns: [
      {
        id: "turn-existing",
        status: "completed",
        items_view: "full",
        items: [
          {
            id: "item-existing",
            type: "user_message",
            status: "completed",
            text: "existing query",
          },
        ],
      },
    ],
  };
}

function newThread(): Thread {
  return {
    id: "thread-new",
    preview: "new query",
    model_provider: "fake",
    model: "fake-model",
    cwd: scratchCwd,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2026-01-03T00:00:00Z",
    updated_at: "2026-01-03T00:00:00Z",
    turns: [],
  };
}

function installWindowStubs(): void {
  class MockResizeObserver {
    constructor(private callback: ResizeObserverCallback) { resizeCallbacks.add(callback); }
    observe(): void {}
    unobserve(): void {}
    disconnect(): void { resizeCallbacks.delete(this.callback); }
  }
  (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => {},
    })),
  });
  Object.defineProperty(window, "requestAnimationFrame", {
    configurable: true,
    value: (callback: FrameRequestCallback) => {
      window.setTimeout(() => callback(0), 0);
      return 1;
    },
  });
}

function installWuuApi(
  options: {
    withThread?: boolean;
    projectAgentEnabled?: boolean;
    deferWorkspaceSelection?: boolean;
    rejectWorkspaceSelection?: boolean;
    rejectNoProjectSelection?: boolean;
    deferThreadStart?: boolean;
    rejectThreadStart?: boolean;
    rejectTurnStart?: boolean;
  } = {},
): void {
  let activeContext: RuntimeContext = { kind: "no_project", cwd: scratchCwd };
  const thread = persistedThread();
  const api = {
    listProjects: vi.fn().mockImplementation(() =>
      Promise.resolve({ projects: [project], active_context: activeContext }),
    ),
    selectProject: vi.fn().mockImplementation(async () => {
      if (options.deferWorkspaceSelection) {
        await new Promise<void>((resolve) => {
          releaseWorkspaceSelection = resolve;
        });
      }
      if (options.rejectWorkspaceSelection) {
        throw new Error("project selection failed");
      }
      activeContext = {
        kind: "project",
        project_id: project.id,
        cwd: workspaceCwd,
      };
      return { projects: [project], active_context: activeContext };
    }),
    selectNoProject: vi.fn().mockImplementation(() => {
      if (options.rejectNoProjectSelection) {
        return Promise.reject(new Error("no-project selection failed"));
      }
      activeContext = { kind: "no_project", cwd: scratchCwd };
      return Promise.resolve({ projects: [project], active_context: activeContext });
    }),
    initialize: vi.fn().mockImplementation(() =>
      Promise.resolve({
        ...initialized(activeContext.cwd),
        features: { project_agent: options.projectAgentEnabled === true },
      }),
    ),
    listThreads: vi.fn().mockImplementation(() =>
      Promise.resolve({
        threads:
          options.withThread && activeContext.kind === "no_project" ? [thread] : [],
      }),
    ),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread }),
    startThread: vi.fn().mockImplementation(async () => {
      if (options.deferThreadStart) {
        await new Promise<void>((resolve) => {
          releaseThreadStart = resolve;
        });
      }
      if (options.rejectThreadStart) {
        throw new Error("thread start failed");
      }
      return { thread: newThread() };
    }),
    startTurn: options.rejectTurnStart
      ? vi.fn().mockRejectedValue(new Error("turn start failed"))
      : vi.fn().mockResolvedValue({
          turn: {
            id: "turn-started",
            status: "in_progress",
            items_view: "full",
            items: [],
          },
        }),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    openSideThread: vi.fn().mockResolvedValue({ summary: null }),
    getSideThreadHistory: vi.fn().mockResolvedValue(null),
    sendSideThreadMessage: vi.fn().mockResolvedValue({
      user_message_id: "side-user",
      summary: null,
    }),
    interruptSideThread: vi.fn().mockResolvedValue({ ok: true }),
    resetSideThread: vi.fn().mockResolvedValue({ ok: true }),
    onSideThreadEvent: vi.fn(() => () => {}),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter(
          (candidate) => candidate !== handler,
        );
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
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    await Promise.resolve();
  });
}

async function renderApp(
  withThread: boolean,
  options: {
    projectAgentEnabled?: boolean;
    deferWorkspaceSelection?: boolean;
    rejectWorkspaceSelection?: boolean;
    rejectNoProjectSelection?: boolean;
    deferThreadStart?: boolean;
    rejectThreadStart?: boolean;
    rejectTurnStart?: boolean;
  } = {},
): Promise<void> {
  installWuuApi({ withThread, ...options });
  await act(async () => {
    root = createRoot(container);
    root.render(await createDefaultApplicationTestView());
  });
  await flushAsync();
  if (withThread) {
    const source = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find(button => button.textContent?.trim() === "focus continuity");
    expect(source).toBeDefined();
    await act(async () => { source!.click(); });
    await flushAsync();
  }
}

function mainComposer(variant: "dock"): HTMLTextAreaElement {
  const textarea = container.querySelector<HTMLTextAreaElement>(
    `textarea[aria-label="main composer ${variant}"]`,
  );
  if (!textarea) throw new Error(`${variant} composer not rendered`);
  return textarea;
}

async function waitForMainComposerFocus(
  variant: "dock",
): Promise<void> {
  await act(async () => {
    await vi.waitFor(() => {
      expect(document.activeElement).toBe(mainComposer(variant));
    });
  });
}

async function waitForSideComposerFocus(): Promise<void> {
  await act(async () => {
    await vi.waitFor(() => {
      const side = container.querySelector<HTMLTextAreaElement>(
        'textarea[aria-label="side composer"]',
      );
      expect(side).not.toBeNull();
      expect(document.activeElement).toBe(side);
    });
  });
}

async function enterCommand(
  textarea: HTMLTextAreaElement,
  command: string,
): Promise<void> {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    setter?.call(textarea, command);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => {
    textarea.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    );
  });
  await flushAsync();
}

async function holdShellReplacement(stage: "bridge" | "register") {
  const controller = new applicationState.ApplicationController();
  vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(controller);
  const host = createDefaultTestHost(controller);
    await registerDefaultTestSurfaces(host);
  let replacing = false;
  let release!: () => void, started!: () => void;
  const ready = new Promise<void>(resolve => { started = resolve; });
  const gate = new Promise<void>(resolve => { release = resolve; });
  window.wuu.loadPluginDesktopModule = vi.fn(async ({ id, fingerprint }) => {
    if (replacing && stage === "bridge") { started(); await gate; }
    return { id, fingerprint, digest: "a".repeat(64), url: "runtime-picker-fixture" };
  });
  const runtime = new DesktopPluginRuntime(host, async () => ({ async activate(api: PluginGenerationApi) {
    activateDefaultShell(api);
    if (replacing && stage === "register") { started(); await gate; }
  } }));
  const shell: ExtensionInventoryRecord = {
    id: "user:shell", name: "shell", kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "shell" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    fingerprint: "same-source", graph_generation: "first", desktop: { entry: "desktop.js" },
  };
  let pending: Promise<unknown> | undefined;
  await runtime.sync([shell]);
  await act(async () => {
    root = createRoot(container);
    root.render(<ApplicationShellOutlet host={host} surfaceBindings={defaultSurfaceBindings} binding={{ pluginId: shell.id, generation: shell.fingerprint!, id: "ui.shell" }} />);
  });
  await flushAsync();
  replacing = true;
  act(() => { pending = runtime.sync([{ ...shell, graph_generation: "second" }]); });
  await ready;
  return {
    controller,
    commit: async () => { await act(async () => { release(); await pending; }); await flushAsync(); },
    dispose: async () => {
      release();
      await pending;
      act(() => { root?.unmount(); root = null; });
      await runtime.sync([]);
    },
  };
}

describe("main composer focus continuity", () => {
  const originalWindowWidth = window.innerWidth;

  beforeEach(() => {
    composerFixture.real = false;
    installWindowStubs();
    serverEventHandlers = [];
    resizeCallbacks = new Set();
    releaseWorkspaceSelection = null;
    releaseThreadStart = null;
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  afterEach(() => {
    act(() => root?.unmount());
    Object.defineProperty(window, "innerWidth", { configurable: true, value: originalWindowWidth });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
    vi.restoreAllMocks();
  });

  it.each(["bridge", "register"] as const)("keeps an opened runtime picker usable through a held %s shell replacement", async (stage) => {
    composerFixture.real = true;
    installWuuApi();
    await window.wuu.selectProject(project.id);
    window.wuu.initialize = vi.fn(async () => ({
      ...initialized(workspaceCwd), provider: "codex", model: "gpt-fixture",
      providers: [{ name: "codex", type: "openai-codex", model: "gpt-fixture", api_key_configured: true }],
    }));
    window.wuu.loadCodexModels = vi.fn(async () => ({
      provider: "codex", model: "gpt-fixture", models: [{ slug: "gpt-fixture", display_name: "Loaded fixture model", supported_in_api: true }],
      providers: [{ name: "codex", type: "openai-codex", model: "gpt-fixture", api_key_configured: true,
        models: [{ id: "gpt-fixture", display_name: "Loaded fixture model" }, { id: "gpt-alternative", display_name: "Alternative fixture model" }] }],
    }));
    const gitStatus = { is_repo: true, branch: "main", branches: ["main", "feature"], dirty_count: 0, files: [] };
    vi.mocked(window.wuu.gitStatus).mockResolvedValue(gitStatus);
    let finishCheckout!: () => void;
    window.wuu.checkoutGitBranch = vi.fn<WuuDesktopApi["checkoutGitBranch"]>(() => new Promise(resolve => {
      finishCheckout = () => resolve({ ...gitStatus, branch: "feature" });
    }));
    const replacement = await holdShellReplacement(stage);
    const click = async (selector: string) => {
      const button = document.querySelector<HTMLButtonElement>(selector)!;
      expect(button).not.toBeNull();
      expect(button.disabled).toBe(false);
      expect(button.closest("[inert]")).toBeNull();
      await act(async () => button.click());
      await flushAsync();
    };
    try {
      const { controller } = replacement;
      // An old checkout may finish after the replacement; its menu cleanup
      // must not dismiss a picker opened in the replacement shell.
      await click(".composer-git-controls .hero-project-pill");
      await click('.composer-branch-menu button[title="feature"]');
      expect(window.wuu.checkoutGitBranch).toHaveBeenCalledWith("feature", workspaceCwd);
      const oldTrigger = container.querySelector(".codex-runtime-trigger")!;
      await click(".codex-runtime-trigger");
      expect(oldTrigger.getAttribute("aria-expanded")).toBe("true");
      expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe("Loaded fixture model");
      await replacement.commit();
      expect(oldTrigger.isConnected).toBe(false);
      expect(container.querySelector(".codex-runtime-trigger")?.getAttribute("aria-expanded")).toBe("true");
      expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe("Loaded fixture model");
      await act(async () => finishCheckout());
      await flushAsync();
      expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe("Loaded fixture model");
      await click(".runtime-panel-model");
      const alternative = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
        .find(button => button.textContent?.includes("Alternative fixture model"));
      expect(alternative).toBeDefined();
      await act(async () => alternative!.click());
      const runtime = controller.getSnapshot().composerRuntime;
      expect(runtime.owners.find(owner => owner.ownerTabId === runtime.activeOwnerTabId)?.selection.model).toBe("gpt-alternative");
      expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe("Alternative fixture model");
      await click(".codex-runtime-trigger");
      expect(document.querySelector(".runtime-panel")).toBeNull();
      await click(".codex-runtime-trigger");
      await click(".composer-project-control .hero-project-pill");
      expect(document.querySelector(".runtime-panel")).toBeNull();
    } finally {
      await act(async () => finishCheckout?.());
      await replacement.dispose();
    }
  });

  it.each([
    { stage: "bridge", mode: "read_only" },
    { stage: "register", mode: "standard" },
  ] as const)("submits the chosen $mode permission after a held $stage shell replacement", async ({ stage, mode }) => {
    composerFixture.real = true;
    installWuuApi();
    window.wuu.listEngines = vi.fn(async () => ({ settings: { default_engine: "wuu" }, engines: [
      { id: "wuu", enabled: true, binary_ok: true },
      { id: "codex", enabled: true, binary_ok: true, models: [{ id: "engine-model", display_name: "Engine fixture model", supported_efforts: ["medium", "high"] }] },
    ] }));
    let storedThread: Thread | undefined;
    vi.mocked(window.wuu.startThread).mockImplementation(async (params) => {
      storedThread = { ...newThread(), engine_id: params?.engine, model: params?.model ?? "", permission_mode: params?.permission_mode };
      return { thread: storedThread };
    });
    vi.mocked(window.wuu.listThreads).mockImplementation(async () => ({ threads: storedThread ? [storedThread] : [] }));
    vi.mocked(window.wuu.resumeThread).mockImplementation(async () => ({ thread: storedThread! }));
    vi.mocked(window.wuu.startTurn).mockImplementation(async () => {
      const turn: Turn = { id: "accepted", status: "completed", items_view: "full", items: [] };
      storedThread = { ...storedThread!, turns: [turn] };
      return { turn };
    });
    const replacement = await holdShellReplacement(stage);
    const click = async (selector: string) => {
      const button = document.querySelector<HTMLButtonElement>(selector)!;
      expect(button).not.toBeNull();
      expect(button.disabled).toBe(false);
      await act(async () => button.click());
      await flushAsync();
    };
    const selectEngine = async (name: string) => {
      await click(".codex-runtime-trigger");
      await click(".runtime-panel-context button:has(.engine-icon)");
      const option = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-engine-option"))
        .find(button => button.querySelector(".runtime-engine-option-name")?.textContent === name)!;
      await act(async () => option.click());
      await flushAsync();
    };
    const selectPermission = async () => {
      await click(".permission-chip");
      const option = Array.from(document.querySelectorAll<HTMLButtonElement>('button[role="menuitemradio"]'))
        .find(button => button.textContent?.includes(permissionModeOption(mode).label))!;
      await act(async () => option.click());
      await flushAsync();
    };
    try {
      await selectEngine("Codex");
      await click(".runtime-panel-model");
      const model = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
        .find(button => button.textContent?.includes("Engine fixture model"))!;
      await act(async () => model.click());
      await selectPermission();
      expect(container.querySelector(".permission-chip")?.textContent).toBe(permissionModeOption(mode).label);
      const oldTrigger = container.querySelector(".codex-runtime-trigger")!;
      await replacement.commit();
      expect(oldTrigger.isConnected).toBe(false);
      expect.soft(container.querySelector(".permission-chip")?.textContent).toBe(permissionModeOption(mode).label);
      await enterCommand(container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer] textarea')!, "preserve this permission");
      expect.soft(window.wuu.startThread).toHaveBeenCalledWith(expect.objectContaining({
        engine: "codex", model: "engine-model", permission_mode: mode,
      }), { kind: "no_project", cwd: scratchCwd });
      expect.soft(vi.mocked(window.wuu.startTurn).mock.calls[0]?.[4]).toBe(mode);
      // Entering a new draft after a real thread still uses the existing default.
      await click("button.session-tab-new");
      expect(container.querySelector(".permission-chip")?.textContent).toBe(permissionModeOption("unconfined").label);
      await selectPermission();
      await selectEngine("Wuu");
      await click(".codex-runtime-trigger");
      await selectEngine("Codex");
      expect(container.querySelector(".permission-chip")?.textContent).toBe(permissionModeOption("unconfined").label);
    } finally {
      await replacement.dispose();
    }
  });

  it.each(["composer", "history edit"])("places a %s submission through the real App send and acknowledgement flow", async (entry) => {
    await renderApp(true);
    vi.mocked(window.matchMedia).mockImplementation(query => ({
      matches: query.includes("prefers-reduced-motion"),
      addEventListener: () => {}, removeEventListener: () => {},
    } as unknown as MediaQueryList));
    const viewport = container.querySelector<HTMLElement>(".scroll-region")!;
    const content = viewport.querySelector<HTMLElement>(".scroll-region-content")!;
    let natural = 2000;
    let top = 500;
    const tail = () => Number.parseFloat(content.style.paddingBottom || "0");
    Object.defineProperties(viewport, {
      clientHeight: { configurable: true, get: () => 600 },
      scrollHeight: { configurable: true, get: () => natural + tail() },
      scrollTop: { configurable: true, get: () => top, set: value => { top = Math.max(0, Math.min(value, natural + tail() - 600)); } },
      scrollTo: { configurable: true, value: (options: ScrollToOptions) => { viewport.scrollTop = options.top ?? 0; } },
    });
    const originalRect = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      if (this === viewport) return { top: 100, bottom: 700, height: 600 } as DOMRect;
      if (this === content) return { top: 100 - top, height: natural + tail() } as DOMRect;
      if (this.hasAttribute("data-user-message-id")) return { top: 1870 - top, bottom: 1950 - top, height: 80 } as DOMRect;
      return originalRect.call(this);
    });
    let accept!: (result: { turn: Turn }) => void;
    vi.mocked(window.wuu.startTurn).mockImplementation(() => new Promise(resolve => { accept = resolve; }));
    let storedThread = persistedThread();
    // Background reconciliation must read the same history the write mocks acknowledge.
    vi.mocked(window.wuu.resumeThread).mockImplementation(async () => ({ thread: storedThread }));
    vi.mocked(window.wuu.listThreads).mockImplementation(async () => ({ threads: [storedThread] }));
    window.wuu.editThreadMessage = vi.fn().mockImplementation(async () => {
      storedThread = { ...storedThread, turns: [] };
      return { thread: storedThread };
    });

    await act(async () => {
      viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, bubbles: true }));
      viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
    });
    if (entry === "history edit") {
      await act(async () => container.querySelector<HTMLButtonElement>(".message-edit-button")!.click());
      const editor = container.querySelector<HTMLTextAreaElement>("[data-user-message-id] textarea")!;
      expect(editor).not.toBeNull();
      await enterCommand(editor, "replacement query");
      expect(window.wuu.editThreadMessage).toHaveBeenCalledWith("thread-focus", "turn-existing", "item-existing");
    } else {
      await enterCommand(mainComposer("dock"), "replacement query");
    }
    expect(window.wuu.startTurn).toHaveBeenCalled();
    if (entry === "composer") {
      expect(tail()).toBe(0);
      expect(viewport.scrollTop).toBe(500);
      expect(mainComposer("dock").value).toBe("replacement query");
    } else {
      expect(tail()).toBeGreaterThan(0);
      expect(viewport.scrollTop).toBeCloseTo(1650);
    }
    const acceptedTurn: Turn = {
      id: "accepted", status: "in_progress", items_view: "full",
      items: [{ id: "accepted-user", type: "user_message", text: "replacement query", source_id: vi.mocked(window.wuu.startTurn).mock.calls[0][8] }],
    };
    await act(async () => {
      storedThread = { ...storedThread, status: "in_progress", turns: [...storedThread.turns, acceptedTurn] };
      accept({ turn: acceptedTurn });
    });
    await flushAsync();
    await act(async () => {
      await vi.waitFor(() => expect(container.querySelector('[data-user-message-id="accepted-user"]')).not.toBeNull());
    });
    expect(tail()).toBeGreaterThan(0);
    const placedTop = viewport.scrollTop;
    expect(placedTop).toBeCloseTo(1650);
    natural += 100;
    act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
    expect(viewport.scrollTop).toBe(placedTop);
    natural += 500;
    act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
    expect(viewport.scrollTop).toBe(natural - 600);
  });

  it.each(["edit failure", "send failure", "stop", "success"])("settles a slow history submission after %s", async (outcome) => {
    await renderApp(true);
    let finishEdit!: (result: { thread: Thread }) => void;
    let failEdit!: (error: Error) => void;
    window.wuu.editThreadMessage = vi.fn().mockImplementation(() => new Promise((resolve, reject) => {
      finishEdit = resolve;
      failEdit = reject;
    }));
    if (outcome === "send failure") vi.mocked(window.wuu.startTurn).mockRejectedValue(new Error("send unavailable"));
    await act(async () => container.querySelector<HTMLButtonElement>(".message-edit-button")!.click());
    const editor = container.querySelector<HTMLTextAreaElement>("[data-user-message-id] textarea")!;
    await enterCommand(editor, "replacement query");
    expect(editor.disabled).toBe(true);
    expect(container.querySelector('[data-main-conversation-composer="dock"]')?.getAttribute("data-running")).toBe("true");
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    if (outcome === "stop") {
      await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="stop-probe"]')!.click());
    }
    await act(async () => {
      if (outcome === "edit failure") failEdit(new Error("edit unavailable"));
      else finishEdit({ thread: { ...persistedThread(), turns: [] } });
    });
    await flushAsync();
    if (outcome === "edit failure") {
      expect(editor.isConnected).toBe(true);
      expect(editor.disabled).toBe(false);
      expect(editor.value).toBe("replacement query");
      expect(window.wuu.startTurn).not.toHaveBeenCalled();
    } else if (outcome === "stop") {
      expect(window.wuu.startTurn).not.toHaveBeenCalled();
      expect(container.textContent).toContain("replacement query");
    } else if (outcome === "send failure") {
      expect(mainComposer("dock").value).toBe("replacement query");
    } else {
      expect(window.wuu.startTurn).toHaveBeenCalledTimes(1);
    }
    if (outcome !== "success") {
      expect(container.querySelector('[data-main-conversation-composer="dock"]')?.getAttribute("data-running")).toBe("false");
    }
  });

  it("focuses the dock composer after /new", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/new");

    await waitForMainComposerFocus("dock");
  });

  it("focuses the dock composer from the new-tab button", async () => {
    await renderApp(true);
    const button = container.querySelector<HTMLButtonElement>(
      'button.session-tab-new[aria-label="新建对话"]',
    );
    if (!button) throw new Error("new-tab button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  it("clears an unpublished local draft when switching to an empty new conversation", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    const setter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      setter?.call(dock, "draft newer than App");
      dock.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dock.value).toBe("draft newer than App");

    const button = container.querySelector<HTMLButtonElement>(
      'button.session-tab-new[aria-label="新建对话"]',
    );
    if (!button) throw new Error("new-tab button not rendered");
    await act(async () => button.click());
    await flushAsync();

    expect(mainComposer("dock").value).toBe("");
  });

  it("focuses the dock composer from a project's new-conversation button", async () => {
    await renderApp(true);
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建对话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  // A project starts from the Projects group in one step: the draft takes the
  // goal, and the first send starts the coordinator named after that goal.
  it("starts a project coordinator from the Projects group on the first send", async () => {
    await renderApp(false, { projectAgentEnabled: true });
    const button = container.querySelector<HTMLButtonElement>(
      '.sidebar-functional-group[data-functional-group-id="projects"] button[aria-label="新建项目"]',
    );
    if (!button) throw new Error("new project button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();
    await waitForMainComposerFocus("dock");
    await enterCommand(mainComposer("dock"), "Page catalog search results\nKeep the public API unchanged.");

    const workspace: RuntimeContext = { kind: "project", project_id: project.id, cwd: workspaceCwd };
    expect(window.wuu.startThread).toHaveBeenCalledWith({
      project: { name: "Page catalog search results" },
      workspace_id: project.id,
      cwd: workspaceCwd,
      engine: "wuu",
      provider: "fake",
      model: "fake-model",
      effort: "high",
      speed: "",
    }, workspace);
    expect(window.wuu.startTurn).toHaveBeenCalled();
  });

  it("waits for the destination dock before focusing across projects", async () => {
    await renderApp(false, { deferWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建对话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    expect(document.activeElement).toBe(button);

    if (!releaseWorkspaceSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseWorkspaceSelection();
    await flushAsync();

    await waitForMainComposerFocus("dock");
  });

  it("allows an explicit Wuu pick after engine discovery fails without guessing the default", async () => {
    installWuuApi();
    window.wuu.listEngines = vi.fn().mockRejectedValue(new Error("engine discovery unavailable"));
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();
    expect(mainComposer("dock").parentElement?.dataset.sendDisabled).toBe("true");
    await enterCommand(mainComposer("dock"), "first query");
    expect(window.wuu.startThread).not.toHaveBeenCalled();

    await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="select-wuu-engine-probe"]')!.click());
    expect(mainComposer("dock").parentElement?.dataset.sendDisabled).toBe("false");
    await enterCommand(mainComposer("dock"), "first query");
    expect(window.wuu.startThread).toHaveBeenCalledWith(expect.objectContaining({
      engine: "wuu", provider: "fake", model: "fake-model",
    }), { kind: "no_project", cwd: scratchCwd });
  });

  it("honors a public composer focus request for the current draft", async () => {
    await renderApp(false);
    const trigger = document.createElement("button");
    container.append(trigger);
    trigger.focus();
    const application = applicationState.getDesktopApplication();
    await act(async () => application.invoke("navigation.focus-composer", { tabId: application.getSnapshot().tabs.activeTabId }));
    await waitForMainComposerFocus("dock");
  });

  it("keeps the new workspace sendable when an older engine settings save finishes", async () => {
    await import("./SettingsView");
    installWuuApi();
    window.wuu.getBuildInfo = vi.fn().mockResolvedValue({});
    window.wuu.listMCPServers = vi.fn().mockResolvedValue({ servers: [] });
    const inventory: EngineListResult = {
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: true, binary_ok: true },
      ],
      settings: { default_engine: "wuu" },
    };
    let finishInventory!: (result: EngineListResult) => void;
    window.wuu.listEngines = vi.fn((options) => options?.cwd === workspaceCwd
      ? new Promise<EngineListResult>((resolve) => { finishInventory = resolve; })
      : Promise.resolve(inventory));
    let finishUpdate!: (result: EngineListResult) => void;
    window.wuu.updateEngines = vi.fn(() => new Promise<EngineListResult>((resolve) => {
      finishUpdate = resolve;
    }));
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    await act(async () => container.querySelector<HTMLButtonElement>(".sidebar-account-trigger")!.click());
    await act(async () => document.querySelector<HTMLButtonElement>('[data-settings-page="providers"]')!.click());
    await vi.waitFor(async () => {
      await flushAsync();
      expect(container.querySelector(".settings-nav")).not.toBeNull();
      expect(container.querySelector('[data-testid="settings-engine-codex-status"]')).not.toBeNull();
    });
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-status"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-engine-codex-enabled"]')!.click());
    expect(window.wuu.updateEngines).toHaveBeenCalledWith({ codex: { enabled: false } });
    await act(async () => container.querySelector<HTMLButtonElement>('[data-ribbon-id="conversations"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="在 Focus Project 中新建对话"]')!.click());
    await flushAsync();
    expect(finishInventory).toBeTypeOf("function");

    // The old save completes while the destination's first inventory is pending.
    await act(async () => finishUpdate(inventory));
    await flushAsync();
    await act(async () => finishInventory(inventory));
    await flushAsync();
    expect(mainComposer("dock").parentElement?.dataset.sendDisabled).toBe("false");
    await enterCommand(mainComposer("dock"), "query in the new workspace");
    expect(window.wuu.startThread).toHaveBeenCalledWith(expect.objectContaining({ engine: "wuu" }), {
      kind: "project", project_id: project.id, cwd: workspaceCwd,
    });
  });

  it("does not focus the old dock when project selection fails", async () => {
    await renderApp(false, { rejectWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建对话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    expect(document.activeElement).toBe(button);
  });

  it("does not focus the old dock when no-project selection fails", async () => {
    await renderApp(false, { rejectNoProjectSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 对话 中新建对话"]',
    );
    if (!button) throw new Error("scratch new-conversation button not rendered");
    button.focus();

    await act(async () => button.click());
    await flushAsync();

    expect(document.activeElement).toBe(button);
  });

  it("does not steal focus changed during an asynchronous project switch", async () => {
    await renderApp(false, { deferWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建对话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();
    await act(async () => button.click());

    const other = document.createElement("button");
    document.body.appendChild(other);
    other.focus();
    if (!releaseWorkspaceSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseWorkspaceSelection();
    await flushAsync();

    expect(document.activeElement).toBe(other);
    other.remove();
  });

  it("does not steal focus after a non-focusable user interaction", async () => {
    await renderApp(false, { deferWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建对话"]',
    );
    if (!button) throw new Error("project new-conversation button not rendered");
    button.focus();
    await act(async () => button.click());

    const surface = document.createElement("div");
    document.body.appendChild(surface);
    surface.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    button.blur();
    if (!releaseWorkspaceSelection) {
      throw new Error("project selection was not deferred");
    }
    releaseWorkspaceSelection();
    await flushAsync();

    expect(document.activeElement).not.toBe(mainComposer("dock"));
    surface.remove();
  });

  it("focuses the side composer after /side", async () => {
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/side");

    await waitForSideComposerFocus();
    const updateSide = async (enabled: boolean) => {
      const inventory = initialized(scratchCwd).extension_inventory!;
      inventory[0].enabled = enabled;
      inventory[0].runtime_state = enabled ? "active" : "inactive";
      await act(async () => {
        for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: scratchCwd,
          message: { method: "plugin/inventory/changed", params: { epoch: enabled ? 3 : 2, extension_inventory: inventory, skills: [] } } });
      });
      await flushAsync();
    };
    await updateSide(false);
    expect(container.querySelector('textarea[aria-label="side composer"]')).toBeNull();
    await enterCommand(mainComposer("dock"), "/side");
    expect(container.querySelector('textarea[aria-label="side composer"]')).toBeNull();
    await updateSide(true);
    await enterCommand(mainComposer("dock"), "/side");
    await waitForSideComposerFocus();
  });

  it.each([
    [390, false], [390, true], [1440, false], [1440, true],
  ] as const)("keeps the composer mounted at width=%s when the first send fails=%s", async (width, rejectThreadStart) => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: width });
    await renderApp(false, { rejectThreadStart });
    const dock = mainComposer("dock");
    expect(dock.parentElement?.dataset.canSelectWorkspace).toBe("true");
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    dock.focus();

    await enterCommand(dock, "first compact query");

    expect(mainComposer("dock")).toBe(dock);
    expect(document.activeElement).toBe(dock);
    expect(dock.value).toBe(rejectThreadStart ? "first compact query" : "");
    expect(dock.parentElement?.dataset.canSelectWorkspace).toBe(String(rejectThreadStart));
    if (!rejectThreadStart) {
      expect(window.wuu.startTurn).toHaveBeenCalled();
    }
  });

  it("focuses the bottom composer after /new in a compact window", async () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
    await renderApp(true);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "/new");

    await waitForMainComposerFocus("dock");
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(mainComposer("dock").value).toBe("");
  });

  it("keeps focus in the bottom composer on the first query", async () => {
    await renderApp(false);
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "first query");

    await waitForMainComposerFocus("dock");
    expect(window.wuu.startThread).toHaveBeenCalledWith({
      engine: "wuu",
      provider: "fake",
      model: "fake-model",
      effort: "high",
      speed: "",
      permission_mode: "standard",
      approve_for_me: false,
    }, { kind: "no_project", cwd: scratchCwd });
    expect(window.wuu.startTurn).toHaveBeenCalled();
  });

  it("shows the first query while thread creation is still pending", async () => {
    await renderApp(false, { deferThreadStart: true });
    const dock = mainComposer("dock");

    await enterCommand(dock, "first query before thread start");

    expect(container.textContent).toContain("first query before thread start");
    expect(container.querySelector(".turn-process-title")?.textContent).toBe(
      "正在处理",
    );
    expect(mainComposer("dock")).not.toBeNull();
    expect(window.wuu.startTurn).not.toHaveBeenCalled();

    if (!releaseThreadStart) {
      throw new Error("thread start was not deferred");
    }
    releaseThreadStart();
    await flushAsync();
  });

  it("retains one pending submission and permits the next draft after confirmation", async () => {
    await renderApp(false, { deferThreadStart: true });
    let accept!: (value: { turn: Turn }) => void;
    window.wuu.startTurn = vi.fn(() => new Promise<{ turn: Turn }>((resolve) => { accept = resolve; }));
    let acceptQueue!: (value: { queued: { id: string; thread_id: string } }) => void;
    window.wuu.queueTurn = vi.fn(() => new Promise((resolve) => { acceptQueue = resolve; })) as WuuDesktopApi["queueTurn"];
    await enterCommand(mainComposer("dock"), "first");
    await enterCommand(mainComposer("dock"), "first");
    expect(window.wuu.startThread).toHaveBeenCalledTimes(1);
    expect(mainComposer("dock").value).toBe("first");
    await enterCommand(mainComposer("dock"), "second");
    expect(mainComposer("dock").value).toBe("second");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-send-disabled")).toBe("true");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-queued")).toBe("");
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    await act(async () => { accept({ turn: { id: "accepted-first", status: "in_progress", items_view: "full", items: [] } }); });
    expect(mainComposer("dock").value).toBe("second");
    await enterCommand(mainComposer("dock"), "second");
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    const secondID = vi.mocked(window.wuu.queueTurn).mock.calls[0][3]!;
    await act(async () => { acceptQueue({ queued: { id: secondID, thread_id: newThread().id } }); });
    expect(mainComposer("dock").value).toBe("");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-send-disabled")).toBe("false");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-queued")).toBe("second");
    await enterCommand(mainComposer("dock"), "third");
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(2);
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: scratchCwd,
        message: { method: "turn/started", params: { thread_id: newThread().id,
          turn: { id: "accepted-second", status: "in_progress", items_view: "full", items: [
            { id: "second-user", type: "user_message", source_id: secondID, text: "second" },
          ] } } } });
    });
    expect(mainComposer("dock").value).toBe("third");
    await enterCommand(mainComposer("dock"), "third");
    expect(vi.mocked(window.wuu.queueTurn).mock.calls.map(call => call[1])).toEqual(["second", "third"]);
    expect(vi.mocked(window.wuu.queueTurn).mock.calls.every(call => call[0] === newThread().id)).toBe(true);
    await act(async () => { acceptQueue({ queued: { id: vi.mocked(window.wuu.queueTurn).mock.calls[1][3]!, thread_id: newThread().id } }); });
  });

  it("holds a preparing follow-up at Stop and permits a later send after hold confirmation", async () => {
    await renderApp(false, { deferThreadStart: true });
    let finishEncoding!: (images: []) => void;
    vi.spyOn(composerMessages, "awaitComposerImages")
      .mockResolvedValueOnce([])
      .mockImplementationOnce(() => new Promise((resolve) => { finishEncoding = resolve; }))
      .mockResolvedValue([]);
    let accept!: (value: { turn: Turn }) => void;
    window.wuu.startTurn = vi.fn(() => new Promise<{ turn: Turn }>((resolve) => { accept = resolve; }));
    window.wuu.interruptTurn = vi.fn().mockResolvedValue({ ok: true });
    window.wuu.queueTurn = vi.fn().mockImplementation(async (threadID, _text, _images, id) => ({ queued: { id, thread_id: threadID } }));
    await enterCommand(mainComposer("dock"), "first");
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    await act(async () => { accept({ turn: { id: "late-first", status: "in_progress", items_view: "full", items: [] } }); });
    await enterCommand(mainComposer("dock"), "second");
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="stop-probe"]')!.click(); });
    expect(window.wuu.interruptTurn).toHaveBeenCalledTimes(1);
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: scratchCwd,
        message: { method: "turn/completed", params: { thread_id: newThread().id,
          turn: { id: "late-first", status: "interrupted", items_view: "full", items: [] } } } });
    });
    await enterCommand(mainComposer("dock"), "third after stop");
    expect(window.wuu.startTurn).toHaveBeenCalledTimes(1);
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    await act(async () => { finishEncoding([]); });
    expect(vi.mocked(window.wuu.queueTurn).mock.calls.map((call) => [call[1], call[9]])).toEqual([
      ["second", true],
    ]);
    expect(mainComposer("dock").value).toBe("third after stop");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: scratchCwd,
        message: { method: "turn/held", params: { thread_id: newThread().id,
          messages: [{ id: vi.mocked(window.wuu.queueTurn).mock.calls[0][3], origin: "queue", prompt: "second" }] } } });
    });
    await enterCommand(mainComposer("dock"), "third after stop");
    expect(window.wuu.startTurn).toHaveBeenCalledTimes(2);
    expect(vi.mocked(window.wuu.startTurn).mock.calls[1][1]).toBe("third after stop");
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    await act(async () => { accept({ turn: { id: "after-stop", status: "in_progress", items_view: "full", items: [] } }); });
  });

  it("cancels attachment preparation without waiting for encoding or starting execution", async () => {
    await renderApp(true);
    let finishEncoding!: (images: []) => void;
    vi.spyOn(composerMessages, "awaitComposerImages").mockImplementationOnce(() => new Promise((resolve) => { finishEncoding = resolve; }));
    window.wuu.interruptTurn = vi.fn().mockResolvedValue({ ok: true });
    await enterCommand(mainComposer("dock"), "cancel before admission");
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="stop-probe"]')!.click(); });
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(window.wuu.interruptTurn).not.toHaveBeenCalled();
    expect(mainComposer("dock").value).toBe("cancel before admission");
    expect(container.querySelector('[data-user-message-id^="optimistic-turn-"]')).toBeNull();
    expect(container.querySelector('[aria-label="continue-probe"]')).toBeNull();
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-send-disabled")).toBe("false");
    await act(async () => { finishEncoding([]); });
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
  });

  it.each([false, true])("preserves newer edits and recovers the original when creation fails (cleared=%s)", async (cleared) => {
    await renderApp(false, { deferThreadStart: true, rejectThreadStart: true });
    await enterCommand(mainComposer("dock"), "first retained");
    await enterCommand(mainComposer("dock"), "second retained");
    if (cleared) await enterCommand(mainComposer("dock"), "");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-queued")).toBe("");
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    expect(mainComposer("dock").value).toBe(cleared ? "first retained" : "second retained");
    const recovery = document.querySelector<HTMLButtonElement>('[role="alert"] .archive-tip-action');
    if (cleared) expect(recovery).toBeNull();
    else {
      expect(recovery).not.toBeNull();
      await act(async () => { recovery!.click(); });
      expect(mainComposer("dock").value).toBe("first retained");
    }
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
  });

  it("retries an unadmitted history-edit failure without editing a fabricated turn", async () => {
    await renderApp(true);
    let rejectSend!: (error: Error) => void;
    vi.mocked(window.wuu.startTurn).mockImplementationOnce(() => new Promise((_, reject) => { rejectSend = reject; }));
    window.wuu.editThreadMessage = vi.fn().mockResolvedValue({ thread: { ...persistedThread(), turns: [] } });
    await act(async () => container.querySelector<HTMLButtonElement>(".message-edit-button")!.click());
    await enterCommand(container.querySelector<HTMLTextAreaElement>("[data-user-message-id] textarea")!, "replacement retained");
    const dock = mainComposer("dock");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(dock, "newer draft");
      dock.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => rejectSend(new Error("send unavailable")));
    await flushAsync();
    expect(mainComposer("dock").value).toBe("newer draft");
    expect(container.querySelector('[aria-label="continue-probe"]')).toBeNull();
    const retry = container.querySelector<HTMLButtonElement>('[data-recovery-state] .turn-failure-action');
    expect(retry).not.toBeNull();
    await act(async () => retry!.click());
    await flushAsync();
    expect(window.wuu.editThreadMessage).toHaveBeenCalledTimes(1);
    expect(window.wuu.startTurn).toHaveBeenCalledTimes(2);
    const calls = vi.mocked(window.wuu.startTurn).mock.calls;
    expect(calls[1][1]).toBe("replacement retained");
    expect(calls[1][8]).toBe(calls[0][8]);
    expect(calls[1][9]).toBeUndefined();
  });

  it.each(["", "newer draft"])("recovers failed thread creation without replacing newer input (%s)", async (newerDraft) => {
    await renderApp(false, { deferThreadStart: true, rejectThreadStart: true });
    const dock = mainComposer("dock");

    await enterCommand(dock, "query that fails before thread start");
    if (newerDraft) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(dock, newerDraft);
        dock.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    expect(mainComposer("dock").value).toBe(newerDraft || "query that fails before thread start");
    expect(container.querySelector(".turn-process-title")).toBeNull();
    if (newerDraft) {
      const recovery = document.querySelector<HTMLButtonElement>('[role="alert"] .archive-tip-action');
      expect(recovery).not.toBeNull();
      await act(async () => { recovery!.click(); });
      expect(mainComposer("dock").value).toBe("query that fails before thread start");
      await enterCommand(mainComposer("dock"), "/new");
      expect(mainComposer("dock").value).toBe(newerDraft);
    }
  });

  it("retains a failed send in its thread when Skills hides the composer before acknowledgement", async () => {
    await renderApp(true);
    let rejectSend!: (error: Error) => void;
    const acknowledgement = new Promise<never>((_resolve, reject) => { rejectSend = reject; });
    vi.mocked(window.wuu.startTurn).mockReturnValueOnce(acknowledgement);
    window.wuu.listSkills = vi.fn().mockResolvedValue({ skills: [] });
    await enterCommand(mainComposer("dock"), "Retain the failed Skills send");
    expect(window.wuu.startTurn).toHaveBeenCalled();
    const skills = container.querySelector<HTMLButtonElement>('[data-ribbon-id="catalog"]');
    expect(skills).not.toBeNull();
    await act(async () => { skills!.click(); });
    await flushAsync();
    const skillsSection = Array.from(container.querySelectorAll<HTMLButtonElement>(".sidebar-section-item"))
      .find(button => button.textContent?.trim() === translateCurrent("skills.tabSkills"));
    expect(skillsSection).toBeDefined();
    await act(async () => { skillsSection!.click(); });
    await flushAsync();
    expect(container.querySelector('[data-main-conversation-composer]')).toBeNull();
    expect(container.querySelector(".catalog-section-pane")?.hasAttribute("hidden")).toBe(false);
    expect(container.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(true);
    await act(async () => { rejectSend(new Error("deferred send failed")); });
    await flushAsync();
    await act(async () => { container.querySelector<HTMLButtonElement>('[data-ribbon-id="conversations"]')!.click(); });
    await flushAsync();
    const source = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find(button => button.textContent?.trim() === "focus continuity");
    expect(source).toBeDefined();
    await act(async () => { source!.click(); });
    await flushAsync();
    expect(mainComposer("dock").value).toBe("Retain the failed Skills send");
    expect(Array.from(container.querySelectorAll("[data-user-message-id]"))
      .some(item => item.textContent?.includes("Retain the failed Skills send"))).toBe(false);
  });

  it("restores a removed queue message to its saved tab when Skills opens before dequeue acknowledgement", async () => {
    await renderApp(true);
    let acknowledge!: (result: { ok: boolean }) => void;
    window.wuu.dequeueTurn = vi.fn(() => new Promise<{ ok: boolean }>(resolve => { acknowledge = resolve; }));
    window.wuu.listSkills = vi.fn().mockResolvedValue({ skills: [] });
    await act(async () => {
      for (const handler of serverEventHandlers) handler({
        kind: "notification", workdir: scratchCwd,
        message: { method: "turn/held", params: { thread_id: "thread-focus", messages: [{ id: "held-skills", origin: "queue", prompt: "Recover queue after Skills" }] } },
      });
    });
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="edit Recover queue after Skills"]')!.click(); });
    expect(window.wuu.dequeueTurn).toHaveBeenCalledWith("thread-focus", "held-skills");
    const skills = container.querySelector<HTMLButtonElement>('[data-ribbon-id="catalog"]')!;
    await act(async () => { skills.click(); });
    await flushAsync();
    const skillsSection = Array.from(container.querySelectorAll<HTMLButtonElement>(".sidebar-section-item"))
      .find(button => button.textContent?.trim() === translateCurrent("skills.tabSkills"));
    expect(skillsSection).toBeDefined();
    await act(async () => { skillsSection!.click(); });
    await flushAsync();
    expect(container.querySelector('[data-main-conversation-composer]')).toBeNull();
    expect(container.querySelector(".catalog-section-pane")?.hasAttribute("hidden")).toBe(false);
    expect(container.querySelector(".scroll-region")?.hasAttribute("inert")).toBe(true);
    await act(async () => { acknowledge({ ok: true }); });
    await flushAsync();
    await act(async () => { container.querySelector<HTMLButtonElement>('[data-ribbon-id="conversations"]')!.click(); });
    await flushAsync();
    const source = Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
      .find(button => button.textContent?.trim() === "focus continuity")!;
    await act(async () => { source.click(); });
    await flushAsync();
    expect(mainComposer("dock").value).toBe("Recover queue after Skills");
  });

  it("restores focus to the dock when the first query fails", async () => {
    await renderApp(false, { rejectTurnStart: true });
    const dock = mainComposer("dock");
    dock.focus();

    await enterCommand(dock, "first query");

    await waitForMainComposerFocus("dock");
  });

  it("does not hand focus to the dock after the user focuses another control", async () => {
    await renderApp(false);
    const dock = mainComposer("dock");
    const sentinel = document.createElement("button");
    container.appendChild(sentinel);
    dock.focus();

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(dock, "first query");
      dock.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      dock.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
      sentinel.focus();
    });
    await flushAsync();

    expect(document.activeElement).toBe(sentinel);
  });
});
