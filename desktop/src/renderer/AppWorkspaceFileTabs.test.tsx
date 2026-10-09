import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mockAnimationFrames } from "./AnimationFrameTestHarness";
import type {
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
  WorkspaceMonacoEditor: ({ path }: { path: string }): JSX.Element => (
    <div className="workspace-monaco-editor" data-path={path} />
  ),
}));

vi.mock("./WorkspacePdfPreview", () => ({
  WorkspacePdfPreview: ({ title }: { title: string }) => <div data-testid="pdf-preview">{title}</div>,
}));

vi.mock("./JumpToLatestPill", () => ({
  JumpToLatestPill: (): JSX.Element => (
    <div data-testid="jump-to-latest-probe" />
  ),
}));

import { App } from "./App";
import { requestOpenThreadInSplit } from "./ConversationSplitBridge";
import * as composerMessages from "./ComposerMessages";
import * as artifactComposer from "./ArtifactComposerFile";
import { useFileSelectionActions, type FileSelectionSource } from "./FileSelectionContext";
import type { FileSelectionControls } from "./FileSelectionSurface";
import type { ReactNode } from "react";

let selectionActions: ReturnType<typeof useFileSelectionActions>;
vi.mock("./FileSelectionSurface", () => ({
  FileSelectionSurface: ({ children }: { children: (controls: FileSelectionControls) => ReactNode }) => {
    selectionActions = useFileSelectionActions();
    return <div>{children({})}</div>;
  },
}));
import { rightPanelMotionMs } from "./AppLayoutState";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let startTurnMock: ReturnType<typeof vi.fn>;
const originalInnerWidth = window.innerWidth;

const workspace = "/tmp/wuu-artifact-tab-test";

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspace,
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

function completedThread(): Thread {
  return {
    id: "thread-artifact-tabs",
    preview: "artifact conversation",
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [
      {
        id: "turn-1",
        items_view: "full",
        status: "completed",
        items: [
          {
            id: "item-user",
            type: "user_message",
            role: "user",
            status: "completed",
            text: "Show me the document.",
          },
          {
            id: "item-agent",
            type: "agent_message",
            role: "assistant",
            terminal: true,
            status: "completed",
            text: "Open [README.md](README.md) beside this conversation.",
          },
        ],
      },
    ],
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
  const thread = completedThread();
  startTurnMock = vi.fn().mockResolvedValue({ turn: thread.turns[0] });
  const api = {
    listProjects: vi.fn().mockResolvedValue({
      projects: [
        {
          id: "project-wuu",
          name: "wuu",
          path: "/repo/wuu",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      ],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    selectNoProject: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads: [thread] }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockResolvedValue({ thread }),
    startThread: vi.fn().mockResolvedValue({ thread: { ...thread, id: "thread-selection-new", turns: [] } }),
    startTurn: startTurnMock,
    queueTurn: vi.fn().mockResolvedValue({ queued: { id: "queued-selection" } }),
    steerTurn: vi.fn().mockResolvedValue({}),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    listWorkspaceDirectory: vi.fn().mockResolvedValue({
      root: workspace,
      path: "",
      entries: [{ kind: "file", name: "README.md", path: "README.md" }],
      truncated: false,
    }),
    readWorkspaceFile: vi.fn().mockResolvedValue({
      root: workspace,
      path: "README.md",
      absolute_path: `${workspace}/README.md`,
      size_bytes: 16,
      mtime_ms: 1000,
      sha256: "a".repeat(64),
      binary: false,
      truncated: false,
      text: "# Artifact\n",
    }),
    browserSurface: vi.fn().mockResolvedValue(null),
    browserCommand: vi.fn(async (params: Parameters<NonNullable<WuuDesktopApi["browserCommand"]>>[0]) => ({
      workdir: params.workdir, tabID: params.tabID, url: params.url,
      title: "Browser fixture", loading: false, canGoBack: false, canGoForward: false,
    })),
    reportBrowserBounds: vi.fn(),
    suppressBrowserOverlay: vi.fn(),
    onBrowserSurface: vi.fn(() => () => {}),
    onBrowserUserInput: vi.fn(() => () => {}),
    onBrowserTabAdopted: vi.fn(() => () => {}),
    writeWorkspaceFile: vi.fn(),
    revealWorkspaceItem: vi.fn().mockResolvedValue(undefined),
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

function setInnerWidth(width: number): void {
  window.innerWidth = width;
}

async function flushAsync(): Promise<void> {
  await animationFrames.flush();
}

let animationFrames: ReturnType<typeof mockAnimationFrames>;

describe("workspace file tabs", () => {
  beforeEach(() => {
    animationFrames = mockAnimationFrames();
    setInnerWidth(1280);
    installWindowStubs();
    installWuuApi();
    Element.prototype.scrollIntoView = vi.fn();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    setInnerWidth(originalInnerWidth);
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
    vi.useRealTimers();
  });

  it("keeps the project overview on the selected coordinator or managed session", async () => {
    vi.mocked(window.wuu.initialize).mockResolvedValue({
      ...initialized(),
      features: { project_agent: true },
    });
    const first = { ...completedThread(), id: "project-a", source: "project", preview: "Project Alpha" };
    const second = { ...completedThread(), id: "project-b", source: "project", preview: "Project Beta" };
    const workerA = { ...completedThread(), id: "worker-a", source: "project-session", project_id: first.id, preview: "Alpha worker" };
    const workerB = { ...completedThread(), id: "worker-b", source: "project-session", project_id: second.id, preview: "Beta worker" };
    const ordinary = completedThread();
    const threads = [first, second, workerA, workerB, ordinary];
    vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads });
    vi.mocked(window.wuu.resumeThread).mockImplementation(async (id) => ({ thread: threads.find((thread) => thread.id === id)! }));
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    const select = async (title: string) => {
      const button = Array.from(container.querySelectorAll(".sidebar .thread-row-title"))
        .find((entry) => entry.textContent === title)?.closest("button");
      expect(button, title).toBeDefined();
      await act(async () => button!.click());
      await flushAsync();
    };
    await select("Project Alpha");
    await act(async () => container.querySelector<HTMLButtonElement>(".project-status-capsule")!.click());
    await flushAsync();
    expect(container.querySelector(".project-panel")?.textContent).toContain("Alpha worker");
    await select("Project Beta");
    expect(container.querySelector(".project-panel h2")?.textContent).toBe("Project Beta");
    expect(container.querySelector(".project-panel")?.textContent).toContain("Beta worker");
    expect(container.querySelector(".project-panel")?.textContent).not.toContain("Alpha worker");
    await act(async () => container.querySelector<HTMLButtonElement>(".project-panel-row-main")!.click());
    await flushAsync();
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toBe("Beta worker");
    expect(container.querySelector(".project-panel h2")?.textContent).toBe("Project Beta");
    await select("Project Alpha");
    expect(container.querySelector(".project-panel h2")?.textContent).toBe("Project Alpha");
    // A file stays selected when the project changes; only the overview follows.
    await act(async () => container.querySelector<HTMLButtonElement>(".rich-file-link")!.click());
    await flushAsync();
    await select("Project Beta");
    expect(container.querySelector(".workspace-tool-tab.active")?.textContent).toContain("README.md");
    await select("artifact conversation");
    expect(container.querySelector(".project-panel")).toBeNull();
    expect(container.querySelector(".workspace-tool-tab.active")?.textContent).toContain("README.md");
    expect(container.querySelector(".workspace-right-panel")?.textContent).not.toContain("Project Beta");
  });

  it("returns focus after closing the browser without scrolling back to its historical link", async () => {
    const thread = completedThread();
    thread.turns[0].items[1].text = "Open [reference](https://example.com/history).";
    vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads: [thread] });
    vi.mocked(window.wuu.resumeThread).mockResolvedValue({ thread });
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    const link = container.querySelector<HTMLAnchorElement>('a[href="https://example.com/history"]')!;
    expect(link).not.toBeNull();
    await act(async () => {
      link.focus();
      link.click();
    });
    await flushAsync();
    expect(container.querySelector(".workspace-browser-panel")).not.toBeNull();
    const viewport = container.querySelector<HTMLElement>(".conversation-pane > .scroll-region")!;
    await act(async () => {
      container.querySelector<HTMLInputElement>(".workspace-browser-panel input")!.focus();
      viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: 900, bubbles: true }));
      viewport.scrollTop = 1800;
      viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
    });
    const restoreFocus = vi.spyOn(link, "focus");
    const close = container.querySelector<HTMLButtonElement>('[data-wuu-component="right-sidebar-toggle"]')!;
    await act(async () => {
      close.focus();
      close.click();
    });
    await flushAsync();
    expect(document.activeElement).toBe(link);
    expect(restoreFocus).toHaveBeenCalledWith({ preventScroll: true });
    // jsdom has no layout; native Electron coverage checks the reading geometry.
  });

  it("does not restore a reused conversation control after its owning thread changes", async () => {
    const first = completedThread();
    first.turns[0].items[1].text = "Open [reference](https://example.com/history).";
    const second = { ...completedThread(), id: "other-thread", preview: "Other conversation" };
    const threads = [first, second];
    vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads });
    vi.mocked(window.wuu.resumeThread).mockImplementation(async (id) => ({ thread: threads.find((thread) => thread.id === id)! }));
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    const composer = container.querySelector<HTMLTextAreaElement>(".composer textarea")!;
    expect(composer).not.toBeNull();
    await act(async () => {
      composer.focus();
      container.querySelector<HTMLAnchorElement>('a[href="https://example.com/history"]')!.click();
    });
    await flushAsync();
    const close = container.querySelector<HTMLButtonElement>('[data-wuu-component="right-sidebar-toggle"]')!;
    await act(async () => close.focus());
    const select = Array.from(container.querySelectorAll(".sidebar .thread-row-title"))
      .find((entry) => entry.textContent === "Other conversation")?.closest("button")!;
    expect(select).toBeDefined();
    await act(async () => select.click());
    await flushAsync();
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toBe("Other conversation");
    expect(composer.isConnected).toBe(true);
    const restoreFocus = vi.spyOn(composer, "focus");
    await act(async () => close.click());
    await flushAsync();
    expect(restoreFocus).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(container.querySelector(".conversation-title-heading h1"));
  });

  async function openSelectionDocument(): Promise<void> {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    await act(async () => container.querySelector<HTMLButtonElement>(".rich-file-link")?.click());
    await flushAsync();
    expect(selectionActions).toBeTruthy();
  }

  const selectionSource: FileSelectionSource = {
    workspace, path: "README.md", start_line: 1, start_column: 1,
    end_line: 1, end_column: 11, quote: "# Artifact", revision: "selection-version",
  };

  async function typePrompt(textarea: HTMLTextAreaElement, value: string): Promise<void> {
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(textarea, value);
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function typeMainPrompt(value: string): Promise<HTMLTextAreaElement> {
    const textarea = container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!;
    await typePrompt(textarea, value);
    return textarea;
  }

  function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (error: Error) => void;
    const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail; });
    return { promise, resolve, reject };
  }

  async function submitMainPrompt(key = "Enter"): Promise<void> {
    await act(async () => container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!
      .dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true })));
    await flushAsync();
  }

  it.each([2000, 1280, 600])("reveals a side selection from the focused workspace at width %i without losing the main draft", async (width) => {
    setInnerWidth(width);
    Object.assign(window.wuu, {
      openSideThread: vi.fn().mockResolvedValue({ summary: null }),
      getSideThreadHistory: vi.fn().mockResolvedValue(null),
      sendSideThreadMessage: vi.fn(),
      interruptSideThread: vi.fn().mockResolvedValue({ ok: true }),
      resetSideThread: vi.fn().mockResolvedValue({ ok: true }),
      onSideThreadEvent: vi.fn(() => () => {}),
    });
    await openSelectionDocument();
    if (width >= 1280) {
      await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]')!.click());
      await flushAsync();
    }
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);
    await typeMainPrompt("Keep the main question");
    act(() => selectionActions!.askSide!(selectionSource));
    await flushAsync();
    await flushAsync();
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);
    const side = container.querySelector<HTMLElement>(".workspace-right-panel .side-thread-panel")!;
    expect(side.closest("[inert]")).toBeNull();
    expect(side.querySelector(".composer-file-selection-card")).not.toBeNull();
    expect(document.activeElement).toBe(side.querySelector("textarea"));
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(window.wuu.sendSideThreadMessage).not.toHaveBeenCalled();
    expect(container.querySelector(".workspace-right-panel")?.getAttribute("aria-hidden")).toBe("false");
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-tab-kind="side-thread"] .workspace-tool-tab-close')!.click());
    await flushAsync();
    expect(container.querySelector(".workspace-file-resource.active .workspace-file-preview")?.textContent).toContain("Artifact");
    expect(container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer="document"] textarea')!.value).toBe("Keep the main question");
  });

  it("restores file selection attachments after first turn failure with untouched draft", async () => {
    await openSelectionDocument();
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="在 对话 中新建对话"]')!.click());
    await flushAsync();
    vi.mocked(window.wuu.startThread).mockResolvedValueOnce({ thread: { ...completedThread(), id: "thread-review-new", turns: [] } });
    startTurnMock.mockRejectedValueOnce(new Error("offline"));
    await typeMainPrompt("Original question");
    act(() => selectionActions!.addComment(selectionSource, "Original comment"));
    const original = selectionActions!.comments[0];
    await submitMainPrompt();
    await flushAsync();
    expect(startTurnMock).toHaveBeenCalledTimes(1);
    expect(selectionActions!.comments).toEqual([original]);
    expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value).toBe("Original question");
    expect(container.querySelector(".composer-file-selection-card")).not.toBeNull();
  });

  it.each(["thread", "turn"] as const)("preserves file comments after first %s failure without replacing a newer draft", async (failure) => {
    await openSelectionDocument();
    const newConversation = container.querySelector<HTMLButtonElement>('button[aria-label="在 对话 中新建对话"]');
    expect(newConversation).not.toBeNull();
    await act(async () => newConversation!.click());
    await flushAsync();
    const threadStart = deferred<Awaited<ReturnType<WuuDesktopApi["startThread"]>>>();
    const turnStart = deferred<Awaited<ReturnType<WuuDesktopApi["startTurn"]>>>();
    vi.mocked(window.wuu.startThread).mockReturnValueOnce(threadStart.promise);
    startTurnMock.mockReturnValueOnce(turnStart.promise);
    await typeMainPrompt("Original question");
    act(() => selectionActions!.addComment(selectionSource, "Original comment"));
    const original = selectionActions!.comments[0];
    await submitMainPrompt();
    expect(window.wuu.startThread).toHaveBeenCalledTimes(1);
    await typeMainPrompt("Next question");
    act(() => selectionActions!.addComment(selectionSource, "Next comment"));
    const next = selectionActions!.comments[0];
    if (failure === "thread") {
      await act(async () => threadStart.reject(new Error("offline")));
    } else {
      await act(async () => threadStart.resolve({ thread: { ...completedThread(), id: "thread-selection-new", turns: [] } }));
      expect(startTurnMock).toHaveBeenCalledTimes(1);
      await act(async () => turnStart.reject(new Error("offline")));
      expect(startTurnMock.mock.calls[0][6]).toEqual([original, { type: "text", text: "Original question" }]);
    }
    await flushAsync();
    expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value)
      .toBe("Next question");
    expect(selectionActions!.comments).toEqual([next]);
    if (failure === "thread") {
      const recovery = document.querySelector<HTMLButtonElement>('[role="alert"] .archive-tip-action');
      expect(recovery).not.toBeNull();
      await act(async () => recovery!.click());
      expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value)
        .toBe("Original question");
      expect(selectionActions!.comments).toEqual([original]);
    }
  });

  it.each([
    { name: "empty inventory", providers: [] },
    { name: "missing readiness flags", providers: [{ name: "fake", type: "openai-compatible", model: "fake-model" }] },
  ])("submits an existing conversation with $name in the provider readiness snapshot", async ({ providers }) => {
    vi.mocked(window.wuu.initialize).mockResolvedValue({ ...initialized(), providers });
    await openSelectionDocument();
    await typeMainPrompt("Continue the existing conversation");
    await act(async () => container.querySelector<HTMLButtonElement>("[data-main-conversation-composer] .composer-send-button")!.click());
    await flushAsync();
    expect(startTurnMock).toHaveBeenCalledTimes(1);
    expect(startTurnMock.mock.calls[0][0]).toBe(completedThread().id);
    expect(startTurnMock.mock.calls[0][1]).toBe("Continue the existing conversation");
  });

  it.each([
    { engine: "codex", providers: false, phase: "idle", accepted: true, queued: false },
    { engine: "wuu", providers: true, phase: "answer-ready", accepted: true, queued: false },
    { engine: "wuu", providers: false, phase: "idle", accepted: true, queued: false },
    { engine: "wuu", providers: true, phase: "running", accepted: true, queued: true },
  ])("routes $engine selection edits in a $phase split pane (providers=$providers)", async ({ engine, providers, phase, accepted, queued }) => {
    if (!providers) vi.mocked(window.wuu.initialize).mockResolvedValue({ ...initialized(), providers: [] });
    await openSelectionDocument();
    const secondary: Thread = { ...completedThread(), id: "thread-selection-secondary", engine_id: engine };
    if (phase !== "idle") {
      secondary.status = "in_progress";
      secondary.turns = [{
        ...secondary.turns[0], status: "in_progress", items: [],
        ...(phase === "answer-ready" ? { answer_ready_at: "2026-09-19T00:00:00Z" } : {}),
      }];
    }
    vi.mocked(window.wuu.resumeThread).mockResolvedValueOnce({ thread: secondary });
    await act(async () => requestOpenThreadInSplit(secondary.id));
    await flushAsync();
    const panes = container.querySelectorAll<HTMLElement>(".conversation-split-pane");
    expect(panes).toHaveLength(2);
    await typePrompt(panes[0].querySelector("textarea")!, "Primary draft");
    act(() => selectionActions!.addComment(selectionSource, "Primary comment"));
    await act(async () => panes[1].dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    await typePrompt(panes[1].querySelector("textarea")!, "Secondary draft");
    act(() => selectionActions!.addComment(selectionSource, "Secondary comment"));
    const comment = selectionActions!.comments[0];
    let result: boolean | undefined;
    await act(async () => { result = await selectionActions!.edit(selectionSource, "Revise the heading"); });
    expect(result).toBe(accepted);
    expect(startTurnMock).toHaveBeenCalledTimes(accepted && !queued ? 1 : 0);
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(queued ? 1 : 0);
    if (accepted) {
      const call = queued ? vi.mocked(window.wuu.queueTurn).mock.calls[0] : startTurnMock.mock.calls[0];
      expect(call[0]).toBe(secondary.id);
      expect(call[queued ? 7 : 6]).toEqual([expect.objectContaining({ type: "file_selection", intent: "edit", source: selectionSource })]);
    }
    expect(panes[0].querySelector("textarea")!.value).toBe("Primary draft");
    expect(panes[1].querySelector("textarea")!.value).toBe("Secondary draft");
    expect(selectionActions!.comments).toEqual([comment]);
    await act(async () => panes[0].dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(selectionActions!.comments[0].comment).toBe("Primary comment");
  });

  it("sends comments with their source and restores the comment draft after a rejected send", async () => {
    await openSelectionDocument();
    const textarea = await typeMainPrompt("Discuss this section");
    act(() => selectionActions!.addComment(selectionSource, "Explain the heading"));
    startTurnMock.mockRejectedValueOnce(new Error("offline"));
    await act(async () => textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    await flushAsync();
    const call = startTurnMock.mock.calls[0];
    expect(call[1]).toContain(selectionSource.quote);
    expect(call[1]).toContain("Explain the heading");
    expect(call[6]).toEqual([
      expect.objectContaining({ type: "file_selection", source: selectionSource, comment: "Explain the heading" }),
      { type: "text", text: "Discuss this section" },
    ]);
    expect(textarea.value).toBe("Discuss this section");
    expect(selectionActions!.comments).toHaveLength(1);
    expect(container.querySelector(".composer-file-selection-card")).not.toBeNull();
  });

  it("submits a selection edit without consuming the main draft or pending comments", async () => {
    await openSelectionDocument();
    const textarea = await typeMainPrompt("Keep this unsent question");
    act(() => selectionActions!.addComment(selectionSource, "Keep this comment"));
    let accepted = false;
    await act(async () => { accepted = await selectionActions!.edit(selectionSource, "Replace the heading"); });
    expect(accepted).toBe(true);
    const call = startTurnMock.mock.calls[0];
    expect(call[1]).toContain("Replace the heading");
    expect(call[1]).not.toContain("Keep this unsent question");
    expect(call[1]).not.toContain("Keep this comment");
    expect(call[2]).toEqual([]);
    expect(call[3]).toEqual([]);
    expect(call[6]).toEqual([expect.objectContaining({ type: "file_selection", intent: "edit", source: selectionSource })]);
    expect(textarea.value).toBe("Keep this unsent question");
    expect(selectionActions!.comments[0].comment).toBe("Keep this comment");
  });

  it("keeps unrelated comments under their new owner when a first inline edit fails", async () => {
    await openSelectionDocument();
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="在 对话 中新建对话"]')!.click());
    await flushAsync();
    await typeMainPrompt("Keep this draft");
    act(() => selectionActions!.addComment(selectionSource, "Keep this comment"));
    const comment = selectionActions!.comments[0];
    const owner = selectionActions!.ownerKey;
    startTurnMock.mockRejectedValueOnce(new Error("offline"));
    let accepted = true;
    await act(async () => { accepted = await selectionActions!.edit(selectionSource, "Retry this edit later"); });
    expect(accepted).toBe(false);
    expect(window.wuu.startThread).toHaveBeenCalledTimes(1);
    expect(selectionActions!.ownerKey).toBe(owner);
    expect(selectionActions!.comments).toEqual([comment]);
    expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value).toBe("Keep this draft");
    await submitMainPrompt();
    expect(startTurnMock.mock.calls.at(-1)![6]).toEqual([comment, { type: "text", text: "Keep this draft" }]);
  });

  it("restores an attachment once when thread creation throws before draft state clears", async () => {
    await openSelectionDocument();
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="在 对话 中新建对话"]')!.click());
    await flushAsync();
    const attachment = { id: "file-selection-retry-attachment", filename: "context.pdf", media_type: "application/pdf", data: "JVBERg==" };
    const encode = vi.spyOn(composerMessages, "composerFilePlaceholder").mockReturnValue({ ...attachment, encodePromise: Promise.resolve(attachment) });
    try {
      const input = container.querySelector<HTMLInputElement>("[data-main-conversation-composer] input[type=file]")!;
      Object.defineProperty(input, "files", { configurable: true, value: [new File(["%PDF"], attachment.filename, { type: attachment.media_type })] });
      await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
      await flushAsync();
    } finally {
      encode.mockRestore();
    }
    await typeMainPrompt("Keep the attachment");
    act(() => selectionActions!.addComment(selectionSource, "Explain the heading"));
    const comment = selectionActions!.comments[0];
    vi.mocked(window.wuu.startThread).mockImplementationOnce(() => { throw new Error("offline"); });
    await submitMainPrompt();
    expect(selectionActions!.comments).toEqual([comment]);
    await submitMainPrompt();
    expect(startTurnMock).toHaveBeenCalledTimes(1);
    expect(startTurnMock.mock.calls[0][3]).toEqual([{
      filename: attachment.filename, media_type: attachment.media_type, data: attachment.data,
    }]);
  });

  it.each([
    { pane: "main", action: "start" },
    { pane: "main", action: "queue" },
    { pane: "main", action: "steer" },
    { pane: "split", action: "start" },
    { pane: "split", action: "queue" },
  ] as const)("restores the exact $pane draft and attachments after an oversized $action rejection for retry", async ({ pane, action }) => {
    await openSelectionDocument();
    const target: Thread = {
      ...completedThread(),
      id: pane === "split" ? "thread-attachment-secondary" : completedThread().id,
    };
    if (action !== "start") {
      target.status = "in_progress";
      target.turns = [...target.turns, {
        id: "turn-attachment-running", status: "in_progress", items_view: "full", items: [],
      }];
    }
    let composer: HTMLElement;
    let otherComposer: HTMLElement | undefined;
    if (pane === "split") {
      vi.mocked(window.wuu.resumeThread).mockResolvedValueOnce({ thread: target });
      await act(async () => requestOpenThreadInSplit(target.id));
      await flushAsync();
      const panes = container.querySelectorAll<HTMLElement>(".conversation-split-pane");
      expect(panes).toHaveLength(2);
      otherComposer = panes[0];
      await typePrompt(otherComposer.querySelector("textarea")!, "Keep the primary draft");
      composer = panes[1];
      await act(async () => composer.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    } else {
      await act(async () => {
        for (const handler of serverEventHandlers) handler({
          kind: "notification", workdir: workspace,
          message: { method: "thread/updated", params: { thread: target } },
        } as ServerEvent);
      });
      composer = container.querySelector<HTMLElement>("[data-main-conversation-composer]")!;
    }
    const image = { id: "retry-image", media_type: "image/png", data: "aW1hZ2U=" };
    const files = [
      { id: "retry-file-1", filename: "context.pdf", media_type: "application/pdf", data: "JVBERg==" },
      { id: "retry-file-2", filename: "资料.txt", media_type: "text/plain", data: "Y29udGV4dA==" },
    ];
    const imageEncode = vi.spyOn(composerMessages, "composerImagePlaceholder")
      .mockReturnValue({ ...image, encodePromise: Promise.resolve(image) });
    const fileEncode = vi.spyOn(composerMessages, "composerFilePlaceholder")
      .mockImplementation((file) => {
        const attachment = files.find((entry) => entry.filename === file.name)!;
        return { ...attachment, encodePromise: Promise.resolve(attachment) };
      });
    try {
      const input = composer.querySelector<HTMLInputElement>("input[type=file]")!;
      Object.defineProperty(input, "files", { configurable: true, value: [
        new File(["image"], "reference.png", { type: image.media_type }),
        ...files.map((file) => new File(["context"], file.filename, { type: file.media_type })),
      ] });
      await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
      await flushAsync();
    } finally {
      imageEncode.mockRestore();
      fileEncode.mockRestore();
    }
    const prompt = "  Compare these attachments.\n保留原文和空格。  ";
    await typePrompt(composer.querySelector("textarea")!, prompt);
    const submit = action === "start" ? startTurnMock
      : action === "queue" ? vi.mocked(window.wuu.queueTurn) : vi.mocked(window.wuu.steerTurn);
    submit.mockRejectedValueOnce(new Error("Message is too large. Remove an attachment and try again."));
    const send = async () => {
      await act(async () => composer.querySelector<HTMLTextAreaElement>("textarea")!
        .dispatchEvent(new KeyboardEvent("keydown", {
          key: "Enter", ctrlKey: pane === "main" && action === "queue", bubbles: true,
        })));
      await flushAsync();
    };
    await send();
    expect(submit).toHaveBeenCalledTimes(1);
    expect(composer.querySelector("textarea")!.value).toBe(prompt);
    expect(composer.querySelectorAll(".composer-attachment-card")).toHaveLength(3);
    if (otherComposer) expect(otherComposer.querySelector("textarea")!.value).toBe("Keep the primary draft");

    await send();
    expect(submit).toHaveBeenCalledTimes(2);
    for (const call of submit.mock.calls) {
      expect(call[0]).toBe(target.id);
      expect(call[action === "steer" ? 2 : 1]).toBe(prompt.trim());
      expect(call[action === "steer" ? 3 : 2]).toEqual([{ media_type: image.media_type, data: image.data }]);
      expect(call[action === "start" ? 3 : action === "queue" ? 4 : 5]).toEqual(files.map(({ filename, media_type, data }) => ({ filename, media_type, data })));
    }
    expect(composer.querySelector("textarea")!.value).toBe("");
    if (otherComposer) expect(otherComposer.querySelector("textarea")!.value).toBe("Keep the primary draft");
  });

  it("keeps new file comments when a queued selection send fails", async () => {
    await openSelectionDocument();
    const running: Thread = { ...completedThread(), status: "in_progress", turns: [
      ...completedThread().turns,
      { id: "turn-selection-running", status: "in_progress", items_view: "full", items: [] },
    ] };
    await act(async () => {
      for (const handler of serverEventHandlers) handler({
        kind: "notification", workdir: workspace,
        message: { method: "thread/updated", params: { thread: running } },
      } as ServerEvent);
    });
    const queued = deferred<Awaited<ReturnType<WuuDesktopApi["queueTurn"]>>>();
    vi.mocked(window.wuu.queueTurn).mockReturnValueOnce(queued.promise);
    await typeMainPrompt("Queued question");
    act(() => selectionActions!.addComment(selectionSource, "Queued comment"));
    const original = selectionActions!.comments[0];
    await act(async () => container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!
      .dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", ctrlKey: true, bubbles: true })));
    await flushAsync();
    expect(window.wuu.queueTurn).toHaveBeenCalledTimes(1);
    expect(vi.mocked(window.wuu.queueTurn).mock.calls[0][7]).toEqual([original, { type: "text", text: "Queued question" }]);
    await typeMainPrompt("New question");
    act(() => selectionActions!.addComment(selectionSource, "New comment"));
    const next = selectionActions!.comments[0];
    await act(async () => queued.reject(new Error("offline")));
    expect(selectionActions!.comments).toEqual([next]);
    expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value)
      .toBe("New question");
  });

  it.each(["/repo/wuu", "/repo/wuu-other"])("keeps split document previews read-only instead of sending another draft (%s)", async (cwd) => {
    const first = { ...completedThread(), cwd: "/repo/wuu" };
    const second = { ...first, id: "thread-other-worktree", cwd };
    vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads: [first] });
    vi.mocked(window.wuu.resumeThread).mockImplementation(async id => ({ thread: id === second.id ? second : first }));
    const projects = await window.wuu.listProjects();
    vi.mocked(window.wuu.listProjects).mockResolvedValue({ ...projects,
      active_context: { kind: "project", project_id: "project-wuu", cwd: first.cwd },
    });
    await openSelectionDocument();
    await act(async () => requestOpenThreadInSplit(second.id));
    await flushAsync();
    const secondary = container.querySelectorAll<HTMLElement>('.conversation-split-pane')[1];
    expect(secondary).toBeDefined();
    await act(async () => secondary.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true })));
    await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
    await flushAsync();
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    expect(startTurnMock).not.toHaveBeenCalled();
  });

  it("opens a PDF from the right file tree and submits its path in the current conversation", async () => {
    vi.mocked(window.wuu.listWorkspaceDirectory).mockResolvedValue({
      root: workspace, path: "", truncated: false,
      entries: [{ kind: "file", name: "report.pdf", path: "report.pdf" }],
    });
    await openSelectionDocument();
    vi.mocked(window.wuu.readWorkspaceFile).mockResolvedValue({
      root: workspace, path: "report.pdf", absolute_path: `${workspace}/report.pdf`,
      size_bytes: 100, mtime_ms: 1000, sha256: "a".repeat(64), binary: true, truncated: false,
      renderable_kind: "pdf", renderable_url: "data:application/pdf;base64,JVBERg==",
    });
    const file = container.querySelector('file-tree-container')?.shadowRoot
      ?.querySelector<HTMLButtonElement>('[data-item-path="report.pdf"]');
    expect(file).toBeDefined();
    await act(async () => file!.click());
    await flushAsync();
    expect(container.querySelector('[data-testid="pdf-preview"]')?.textContent).toBe("report.pdf");
    expect(container.querySelector('.app-shell')!.classList.contains('right-panel-globalized')).toBe(false);
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
    await flushAsync();
    expect(container.querySelector('[data-main-conversation-composer="document"]')).not.toBeNull();
    await typeMainPrompt("Rewrite the conclusion");
    await submitMainPrompt();
    expect(startTurnMock.mock.calls[0][0]).toBe("thread-artifact-tabs");
    expect(startTurnMock.mock.calls[0][5]).toEqual({ path: "report.pdf" });
    expect(startTurnMock.mock.calls[0][7]).toEqual({ kind: "no_project", cwd: workspace });
  });

  it.each(["success", "send", "read", "validation"])("submits a delivered PDF snapshot without inventing a path and recovers failures (%s)", async (failure) => {
    const thread = completedThread();
    thread.turns[0].items.push({
      id: "present-pdf", type: "tool_call", name: "present_artifact", status: "completed",
      result_detail: { content: [{ type: "file", mime_type: "application/pdf", name: "report.pdf",
        uri: "wuu-artifact://workspace/thread-artifact-tabs/digest/report.pdf",
        artifact: { placement: "turn_end", sha256: "digest" } }] },
    });
    vi.mocked(window.wuu.listThreads).mockResolvedValue({ threads: [thread] });
    vi.mocked(window.wuu.resumeThread).mockResolvedValue({ thread });
    const snapshotFile = { id: "pdf-context", filename: "report.pdf", media_type: "application/pdf", data: "JVBERg==" };
    const attach = vi.spyOn(artifactComposer, "createArtifactComposerFile").mockReturnValue(snapshotFile);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const card = Array.from(container.querySelectorAll<HTMLButtonElement>(".turn-edit-summary-overview"))
      .find(button => button.textContent?.includes("report.pdf"));
    expect(card).toBeDefined();
    await act(async () => card!.click());
    await flushAsync();
    expect(container.querySelector('[data-testid="pdf-preview"]')).not.toBeNull();
    if (!container.querySelector('.app-shell')!.classList.contains('right-panel-globalized')) {
      expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
      await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
      await flushAsync();
    }
    expect(container.querySelector('[data-main-conversation-composer="document"]')).not.toBeNull();
    expect(container.querySelectorAll('[data-main-conversation-composer]')).toHaveLength(1);
    await typeMainPrompt("Revise this report");
    if (failure === "send") startTurnMock.mockRejectedValueOnce(new Error("offline"));
    if (failure === "validation") attach.mockImplementationOnce(() => { throw new Error("Invalid preview source"); });
    if (failure === "read") attach.mockImplementationOnce(() => ({
      ...snapshotFile, data: "", encodePromise: Promise.reject(new Error("Preview unavailable")),
    }));
    await submitMainPrompt();
    expect(attach).toHaveBeenCalledWith(expect.objectContaining({ threadID: thread.id }), thread.id);
    if (failure === "success" || failure === "send") {
      expect(startTurnMock.mock.calls[0][3]).toEqual([{ filename: "report.pdf", media_type: "application/pdf", data: "JVBERg==" }]);
      expect(startTurnMock.mock.calls[0][5]).toBeUndefined();
    } else expect(startTurnMock).not.toHaveBeenCalled();
    if (failure !== "success") {
      expect(container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer="document"] textarea')!.value)
        .toBe("Revise this report");
      expect(container.querySelectorAll('.composer-file-card')).toHaveLength(0);
      await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-add')!.click());
      await flushAsync();
      expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
      await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
      await flushAsync();
      expect(container.querySelector('[data-main-conversation-composer="dock"]')).not.toBeNull();
      await submitMainPrompt();
      expect(attach).toHaveBeenCalledTimes(1);
      expect(startTurnMock.mock.calls.at(-1)![3]).toEqual([]);
      expect(startTurnMock.mock.calls.at(-1)![5]).toBeUndefined();
    }
  });

  it("returns the preview draft and keyboard focus through Open conversation", async () => {
    await openSelectionDocument();
    await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
    await flushAsync();
    const input = await typeMainPrompt("Continue editing");
    await act(async () => input.focus());
    await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="对话选项"]')!.click());
    const open = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
      .find(button => button.textContent === "打开对话")!;
    await act(async () => { open.focus(); open.click(); });
    await flushAsync();
    const main = container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer="dock"] textarea')!;
    expect(main.value).toBe("Continue editing");
    expect(document.activeElement).toBe(main);
  });

  it("hands the live draft to the right preview and back without tagging a hidden file", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    await typeMainPrompt("Keep this unfinished request");
    await act(async () => container.querySelector<HTMLButtonElement>(".rich-file-link")!.click());
    await flushAsync();
    expect(container.querySelector(".app-shell")!.classList.contains("right-panel-globalized")).toBe(false);
    expect(container.querySelector(".conversation-pane")!.hasAttribute("inert")).toBe(false);
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer="dock"] textarea')!.value)
      .toBe("Keep this unfinished request");
    await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
    await flushAsync();
    expect(container.querySelector<HTMLTextAreaElement>('[data-main-conversation-composer="document"] textarea')!.value)
      .toBe("Keep this unfinished request");
    await typeMainPrompt("Updated in the preview");
    await act(async () => container.querySelector<HTMLButtonElement>('.workspace-panel-globalize')!.click());
    await flushAsync();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-wuu-component="right-sidebar-toggle"]')!.click());
    await flushAsync();
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    expect(container.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea")!.value)
      .toBe("Updated in the preview");
    await submitMainPrompt();
    expect(startTurnMock).toHaveBeenCalledTimes(1);
    expect(startTurnMock.mock.calls[0][1]).toBe("Updated in the preview");
    expect(startTurnMock.mock.calls[0][5]).toBeUndefined();
  });

  it("opens a document beside the active conversation instead of replacing it", async () => {
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    const fileLink = container.querySelector<HTMLButtonElement>(".rich-file-link");
    expect(fileLink).not.toBeNull();
    expect(container.querySelectorAll(".session-tab")).toHaveLength(0);
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toContain(
      "artifact conversation",
    );
    expect(
      container.querySelector('[data-testid="jump-to-latest-probe"]'),
    ).not.toBeNull();

    vi.useFakeTimers();
    await act(async () => {
      fileLink?.click();
    });
    await flushAsync();

    expect(container.querySelectorAll(".session-tab")).toHaveLength(0);
    expect(container.querySelector(".conversation-title-heading h1")?.textContent).toContain(
      "artifact conversation",
    );
    expect(container.querySelector(".rich-file-link")).not.toBeNull();
    expect(
      container.querySelector(".workspace-right-panel .workspace-tool-tab.active")?.textContent,
    ).toContain("README.md");
    expect(container.querySelector(".workspace-right-panel .workspace-files-content-header")).toBeNull();
    expect(container.querySelector(".workspace-right-panel .workspace-files-tree")).not.toBeNull();
    const rightFilePreview = container.querySelector(
      ".workspace-right-panel .workspace-file-resource.active .workspace-file-preview",
    );
    expect(rightFilePreview).not.toBeNull();
    expect(rightFilePreview?.textContent).toContain("Artifact");
    expect(container.querySelector('[data-main-conversation-composer="document"]')).toBeNull();
    expect(container.querySelector('[data-main-conversation-composer="dock"]')).not.toBeNull();

    act(() => {
      vi.advanceTimersByTime(rightPanelMotionMs());
    });
    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-animating")).toBe(false);

    fileLink?.focus();
    const expand = container.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]');
    await act(async () => expand?.click());
    await flushAsync();

    expect(shell?.classList.contains("right-panel-animating")).toBe(true);
    expect(shell?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector(".workspace-right-panel")?.hasAttribute("inert")).toBe(false);
    expect(
      container.querySelector('.globalized-sidebar-toggle[aria-label="展开左侧栏"]'),
    ).toBeNull();
    expect(container.querySelector('[data-testid="workspace-document-composer"]')).not.toBeNull();
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
    expect(
      container.querySelector('[data-main-conversation-composer="document"]'),
    ).not.toBeNull();
    expect(
      container
        .querySelector(".workspace-document-turn-summary")
        ?.getAttribute("aria-expanded"),
    ).toBe("false");
    expect(
      container.querySelector('[data-testid="jump-to-latest-probe"]'),
    ).toBeNull();
    expect(document.activeElement).toBe(
      container.querySelector(".workspace-tool-tab.active .workspace-tool-tab-main"),
    );

    const focusedTextarea = container.querySelector<HTMLTextAreaElement>(
      '[data-testid="workspace-document-composer"] textarea',
    );
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    await act(async () => {
      valueSetter?.call(focusedTextarea, "Rewrite the weak section.");
      focusedTextarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    startTurnMock.mockResolvedValueOnce({
      turn: {
        id: "turn-document-edit",
        items_view: "full",
        status: "in_progress",
        items: [
          {
            id: "item-document-edit-user",
            type: "user_message",
            text: "Rewrite the weak section.",
          },
        ],
      },
    });
    await act(async () => {
      focusedTextarea?.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
    await flushAsync();
    expect(startTurnMock).toHaveBeenCalledWith(
      "thread-artifact-tabs",
      "Rewrite the weak section.",
      [],
      [],
      undefined,
      { path: "README.md" },
      undefined,
      { kind: "no_project", cwd: "/tmp/wuu-artifact-tab-test" },
      expect.any(String),
    );
    expect(container.querySelector('[data-testid="workspace-document-turn-drawer"]')).not.toBeNull();

    act(() => {
      vi.advanceTimersByTime(rightPanelMotionMs());
    });
    expect(shell?.classList.contains("right-panel-animating")).toBe(false);

    const exit = container.querySelector<HTMLButtonElement>('[aria-label="退出全面板"]');
    await act(async () => exit?.click());
    await flushAsync();
    expect(shell?.classList.contains("right-panel-animating")).toBe(true);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);
    expect(container.querySelector('[data-testid="workspace-document-composer"]')).toBeNull();
    expect(container.querySelector('[data-main-conversation-composer="dock"]')).not.toBeNull();
    expect(document.activeElement).toBe(fileLink);
  });

  it("mounts only the focused composer for an empty conversation", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    vi.useFakeTimers();
    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();
    act(() => {
      vi.advanceTimersByTime(rightPanelMotionMs());
    });
    await act(async () => {
      container.querySelector<HTMLButtonElement>('[aria-label="展开为全面板"]')?.click();
    });
    await flushAsync();

    const emptyThread = { ...completedThread(), turns: [] };
    await act(async () => {
      for (const handler of serverEventHandlers) {
        handler({
          kind: "notification",
          workdir: workspace,
          message: {
            method: "thread/updated",
            params: { thread: emptyThread },
          },
        } as ServerEvent);
      }
    });
    await flushAsync();

    expect(container.querySelector('[data-testid="workspace-document-composer"]')).not.toBeNull();
    expect(container.querySelectorAll("[data-main-conversation-composer]")).toHaveLength(1);
  });

  it("focuses the workspace automatically when a narrow window cannot keep conversation usable", async () => {
    setInnerWidth(674);
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();

    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-globalized")).toBe(true);
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(true);

    expect(container.querySelector(".sidebar")?.hasAttribute("inert")).toBe(false);
  });

  it("keeps all three columns docked when an open sidebar is the only space pressure", async () => {
    // 1000px window (>= 900, so the sidebar stays docked, not auto-collapsed).
    // Conversation + panel fit without the sidebar, but adding the docked
    // sidebar tips the layout over the focus threshold. The panel must NOT
    // auto-globalize as a side effect of the sidebar being open — instead all
    // three stay docked and the conversation column absorbs the squeeze. Only a
    // manual toggle globalizes here.
    setInnerWidth(1000);
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    await act(async () => {
      container.querySelector<HTMLButtonElement>(".rich-file-link")?.click();
    });
    await flushAsync();

    const shell = container.querySelector<HTMLElement>(".app-shell");
    expect(shell?.classList.contains("right-panel-open")).toBe(true);
    expect(shell?.classList.contains("right-panel-globalized")).toBe(false);
    // The sidebar stays a real docked column, not a drawer overlay.
    expect(shell?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(
      container.querySelector(".conversation-pane")?.hasAttribute("inert"),
    ).toBe(false);
  });
});
