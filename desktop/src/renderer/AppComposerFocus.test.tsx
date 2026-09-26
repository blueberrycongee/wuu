import * as composerMessages from "./ComposerMessages";
import { act, useEffect, useState, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  DesktopProject,
  InitializeResult,
  RuntimeContext,
  ServerEvent,
  Thread,
  Turn,
  WuuDesktopApi,
} from "../shared/protocol";

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
      const label = props.mainConversation
        ? `main composer ${variant}`
        : "side composer";
      return (
        <div
          data-can-select-workspace={props.canSelectWorkspace}
          data-queued={props.queuedMessages.map((message) => message.text).join("|")}
          data-send-disabled={props.sendDisabled}
          data-main-conversation-composer={
            props.mainConversation ? variant : undefined
          }
        >
          <button aria-label="stop-probe" onClick={props.onInterrupt}>stop</button>
          {props.queuedMessages.map((message) => (
            <button key={message.id} aria-label={`remove ${message.text}`} onClick={() => props.onRemoveQueuedMessage(message.id)}>remove</button>
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
              } else {
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

import { App } from "./App";

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
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
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
      Promise.resolve(initialized(activeContext.cwd)),
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
    root.render(<App />);
  });
  await flushAsync();
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

describe("main composer focus continuity", () => {
  const originalWindowWidth = window.innerWidth;

  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    resizeCallbacks = new Set();
    releaseWorkspaceSelection = null;
    releaseThreadStart = null;
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
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

  it.each(["composer", "history edit"])("places a %s submission through the real App send and acknowledgement flow", async (entry) => {
    await renderApp(true);
    vi.mocked(window.matchMedia).mockImplementation(query => ({
      matches: query.includes("prefers-reduced-motion"),
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
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
    window.wuu.editThreadMessage = vi.fn().mockResolvedValue({ thread: { ...persistedThread(), turns: [] } });

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
    expect(tail()).toBeGreaterThan(0);
    const placedTop = viewport.scrollTop;
    expect(placedTop).toBeCloseTo(1650);
    await act(async () => accept({ turn: {
      id: "accepted", status: "in_progress", items_view: "full",
      items: [{ id: "accepted-user", type: "user_message", text: "replacement query" }],
    } }));
    await flushAsync();
    await act(async () => {
      await vi.waitFor(() => expect(container.querySelector('[data-user-message-id="accepted-user"]')).not.toBeNull());
    });
    natural += 100;
    act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
    expect(viewport.scrollTop).toBe(placedTop);
    natural += 500;
    act(() => { for (const callback of [...resizeCallbacks]) callback([], {} as ResizeObserver); });
    expect(viewport.scrollTop).toBe(natural - 600);
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
      'button[aria-label="在 Focus Project 中新建会话"]',
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
    await renderApp(false);
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
      speed: undefined,
    }, workspace);
    expect(window.wuu.startTurn).toHaveBeenCalled();
  });

  it("waits for the destination dock before focusing across projects", async () => {
    await renderApp(false, { deferWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
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

  it("does not focus the old dock when project selection fails", async () => {
    await renderApp(false, { rejectWorkspaceSelection: true });
    const button = container.querySelector<HTMLButtonElement>(
      'button[aria-label="在 Focus Project 中新建会话"]',
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
      'button[aria-label="在 对话 中新建会话"]',
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
      'button[aria-label="在 Focus Project 中新建会话"]',
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
      'button[aria-label="在 Focus Project 中新建会话"]',
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
      provider: "fake",
      model: "fake-model",
      effort: "high",
      permission_mode: "standard",
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

  it("accepts follow-ups during creation and sends them in order after the first admission", async () => {
    await renderApp(false, { deferThreadStart: true });
    let accept!: (value: { turn: Turn }) => void;
    window.wuu.startTurn = vi.fn(() => new Promise<{ turn: Turn }>((resolve) => { accept = resolve; }));
    let acceptQueue!: (value: { queued: { id: string; thread_id: string } }) => void;
    window.wuu.queueTurn = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { acceptQueue = resolve; }))
      .mockImplementation(async (threadID, _text, _images, id) => ({ queued: { id, thread_id: threadID } }));
    await enterCommand(mainComposer("dock"), "first");
    await enterCommand(mainComposer("dock"), "second");
    await enterCommand(mainComposer("dock"), "third");
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-queued")).toBe("second|third");
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    await act(async () => { accept({ turn: { id: "accepted-first", status: "in_progress", items_view: "full", items: [] } }); });
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    expect(vi.mocked(window.wuu.queueTurn).mock.calls[0][1]).toBe("second");
    await act(async () => {
      for (const handler of serverEventHandlers) handler({ kind: "notification", workdir: scratchCwd,
        message: { method: "turn/completed", params: { thread_id: newThread().id,
          turn: { id: "accepted-first", status: "completed", items_view: "full", items: [] } } } });
    });
    await enterCommand(mainComposer("dock"), "fourth after completion");
    expect(window.wuu.startTurn).toHaveBeenCalledTimes(1);
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    await act(async () => { acceptQueue({ queued: { id: "second", thread_id: newThread().id } }); });
    expect(vi.mocked(window.wuu.queueTurn).mock.calls.map((call) => call[1])).toEqual([
      "second", "third", "fourth after completion",
    ]);
    expect(vi.mocked(window.wuu.queueTurn).mock.calls.every((call) => call[0] === newThread().id)).toBe(true);
  });

  it("holds buffered follow-ups at Stop while later sends resume in order", async () => {
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
    await enterCommand(mainComposer("dock"), "second");
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="stop-probe"]')!.click(); });
    await act(async () => { accept({ turn: { id: "late-first", status: "in_progress", items_view: "full", items: [] } }); });
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
      ["second", true], ["third after stop", false],
    ]);
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
    expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-send-disabled")).toBe("false");
    await act(async () => { finishEncoding([]); });
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
  });

  it.each([false, true])("recovers only retained inputs when creation fails (removed=%s)", async (removed) => {
    await renderApp(false, { deferThreadStart: true, rejectThreadStart: true });
    await enterCommand(mainComposer("dock"), "first retained");
    await enterCommand(mainComposer("dock"), "second retained");
    if (removed) {
      await act(async () => { container.querySelector<HTMLButtonElement>('[aria-label="remove second retained"]')!.click(); });
      expect(container.querySelector('[data-main-conversation-composer]')?.getAttribute("data-queued")).toBe("");
    }
    await act(async () => { releaseThreadStart!(); });
    await flushAsync();
    expect(mainComposer("dock").value).toBe("first retained");
    const recovery = document.querySelector<HTMLButtonElement>('[role="alert"] .archive-tip-action');
    if (removed) {
      expect(recovery).toBeNull();
    } else {
      expect(recovery).not.toBeNull();
      await act(async () => { recovery!.click(); });
      expect(mainComposer("dock").value).toBe("second retained");
    }
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
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
