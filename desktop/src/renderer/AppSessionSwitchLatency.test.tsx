/**
 * Session tab switching should feel local when the target thread is already
 * loaded. The app still resumes the thread in the background so the server
 * snapshot can refresh status/turns, but the click must not wait for that IPC
 * round trip before the active tab and conversation pane change.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  InitializeResult,
  RuntimeContext,
  ServerEvent,
  Thread,
  Turn,
  WuuDesktopApi,
} from "../shared/protocol";

vi.mock("./ConversationTurnList", () => ({
  ConversationTurnList: ({
    threadID,
    turns,
  }: {
    threadID: string;
    turns: Array<{ status?: string; items?: Array<{ type: string; text?: string }> }>;
  }): JSX.Element => (
    <div
      data-testid="turn-list-probe"
      data-thread-id={threadID}
      data-turn-count={turns.length}
      data-latest-turn-status={turns.at(-1)?.status}
      data-latest-user-text={turns.at(-1)?.items?.find(item => item.type === "user_message")?.text}
      data-latest-agent-text={turns.at(-1)?.items?.find(item => item.type === "agent_message")?.text}
    />
  ),
}));

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

import { App } from "./App";
import { requestOpenThreadInSplit } from "./ConversationSplitBridge";
import * as ComposerMessages from "./ComposerMessages";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

const workspace = "/tmp/wuu-session-switch-latency-test";
const threadAID = "thread-switch-a";
const threadBID = "thread-switch-b";

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: Error) => void;
};

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "provider-b",
    model: "model-b",
    workspace_root: workspace,
    permissions: { mode: "standard" },
    providers: [
      {
        name: "provider-a",
        type: "openai-compatible",
        model: "model-a",
        api_key_configured: true,
      },
      {
        name: "provider-b",
        type: "openai-compatible",
        model: "model-b",
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

function completedThread({
  id,
  preview,
  updatedAt,
  turns = 1,
  provider,
  model,
}: {
  id: string;
  preview: string;
  updatedAt: string;
  turns?: number;
  provider: string;
  model: string;
}): Thread {
  return {
    id,
    preview,
    model_provider: provider,
    model,
    cwd: workspace,
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: updatedAt,
    turns: Array.from({ length: turns }, (_, index) => ({
      id: `${id}-turn-${index + 1}`,
      items_view: "full",
      status: "completed",
      items: [
        {
          id: `${id}-user-${index + 1}`,
          type: "user_message",
          text: `${preview} question ${index + 1}`,
        },
        {
          id: `${id}-agent-${index + 1}`,
          type: "agent_message",
          text: `${preview} answer ${index + 1}`,
        },
      ],
    })),
  };
}

function threadA(turns = 1): Thread {
  return completedThread({
    id: threadAID,
    preview: "session switch A",
    updatedAt: "2026-01-02T00:00:00Z",
    turns,
    provider: "provider-a",
    model: "model-a",
  });
}

function runningThreadA(): Thread {
  const thread = threadA();
  return {
    ...thread,
    status: "in_progress",
    turns: thread.turns.map((turn) => ({
      ...turn,
      status: "in_progress",
      items: turn.items.map((item) =>
        item.type === "agent_message"
          ? { ...item, status: "in_progress", terminal: false }
          : item,
      ),
    })),
  };
}

function threadB(): Thread {
  return completedThread({
    id: threadBID,
    preview: "session switch B",
    updatedAt: "2026-01-01T00:00:00Z",
    provider: "provider-b",
    model: "model-b",
  });
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

function installWuuApi(): {
  resumeThread: ReturnType<typeof vi.fn>;
  startTurn: ReturnType<typeof vi.fn>;
  threadsByID: Map<string, Thread>;
} {
  const threadsByID = new Map<string, Thread>([
    [threadAID, threadA()],
    [threadBID, threadB()],
  ]);
  const resumeThread = vi
    .fn()
    .mockImplementation((threadID: string) =>
      Promise.resolve({ thread: threadsByID.get(threadID) }),
    );
  const startTurn = vi.fn().mockResolvedValue({
    turn: {
      id: "turn-after-switch",
      items_view: "full",
      status: "in_progress",
      items: [],
    },
  });
  const api = {
    listProjects: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    selectNoProject: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: workspace },
    }),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockImplementation(async () => ({
      threads: Array.from(threadsByID.values(), thread => ({ ...thread, turns: [] })),
    })),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread,
    startTurn,
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
  return { resumeThread, startTurn, threadsByID };
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function threadRowButton(previewText: string): HTMLButtonElement | undefined {
  return Array.from(
    container.querySelectorAll<HTMLButtonElement>(".thread-row-main"),
  ).find((button) => button.textContent?.includes(previewText));
}

function activeSessionTabLabel(): string {
  return (
    container.querySelector(".conversation-title-heading h1")?.textContent ?? ""
  );
}

function activeThreadProbe(): HTMLElement | null {
  return container.querySelector('[data-testid="turn-list-probe"]');
}

function visibleRuntimeModel(): string {
  return (
    container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")
      ?.textContent ?? ""
  );
}

function mainComposerTextarea(): HTMLTextAreaElement {
  const textarea = container.querySelector<HTMLTextAreaElement>(
    '[data-main-conversation-composer="dock"] textarea[data-wuu-component="composer-input"]',
  );
  if (!textarea) {
    throw new Error("main composer textarea was not rendered");
  }
  return textarea;
}

function mainComposerSendButton(): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>(
    '[data-main-conversation-composer="dock"] .composer-send-button',
  );
  if (!button) {
    throw new Error("main composer send button was not rendered");
  }
  return button;
}

function setMainComposerPrompt(value: string): void {
  const textarea = mainComposerTextarea();
  const setter = Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    "value",
  )?.set;
  setter?.call(textarea, value);
  textarea.dispatchEvent(new Event("input", { bubbles: true }));
}

function emitNotification(method: string, params: Record<string, unknown>, workdir = workspace): void {
  const event = {
    kind: "notification",
    workdir,
    message: { method, params },
  } as ServerEvent;
  for (const handler of serverEventHandlers) {
    handler(event);
  }
}

describe("session tab switch latency", () => {
  beforeEach(() => {
    installWindowStubs();
    serverEventHandlers = [];
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.useRealTimers();
    act(() => {
      root?.unmount();
    });
    root = null;
    vi.restoreAllMocks();
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("keeps pending creation visible and stoppable after a background list refresh", async () => {
    const { threadsByID, startTurn } = installWuuApi();
    threadsByID.clear();
    const creation = deferred<{ thread: Thread }>();
    window.wuu.startThread = vi.fn(() => creation.promise);
    window.wuu.deleteThread = vi.fn().mockResolvedValue(undefined);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { setMainComposerPrompt("first pending query"); });
    await act(async () => { mainComposerSendButton().click(); });
    expect(threadRowButton("first pending query")).toBeDefined();

    // Re-listing sees no server thread yet and clears the global running flag.
    const listCount = vi.mocked(window.wuu.listThreads).mock.calls.length;
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    await flushAsync();
    expect(vi.mocked(window.wuu.listThreads).mock.calls.length).toBeGreaterThan(listCount);
    await act(async () => { setMainComposerPrompt("queued follow-up"); });
    await act(async () => {
      mainComposerTextarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(window.wuu.startThread).toHaveBeenCalledTimes(1);
    expect(mainComposerTextarea().value).toBe("");
    await act(async () => { setMainComposerPrompt("newer draft"); });
    await act(async () => { mainComposerTextarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })); });
    expect(threadRowButton("first pending query")).toBeUndefined();
    expect(mainComposerTextarea().value).toBe("newer draft");
    const recovery = document.querySelector<HTMLButtonElement>('[role="alert"] .archive-tip-action');
    expect(recovery).not.toBeNull();
    await act(async () => { recovery!.click(); });
    expect(mainComposerTextarea().value).toBe("first pending query");

    const late = { ...threadA(), id: "late-created", preview: "", turns: [] };
    await act(async () => {
      emitNotification("thread/started", { thread: late });
      creation.resolve({ thread: late });
    });
    await flushAsync();
    expect(startTurn).not.toHaveBeenCalled();
    expect(window.wuu.deleteThread).toHaveBeenCalledWith(late.id);
    expect(mainComposerTextarea().value).toBe("first pending query");
    expect(threadRowButton("未命名对话")).toBeUndefined();
  });

  it.each(["top", "workspace"])("keeps pending drafts separate when starting another conversation from %s", async (entry) => {
    const { threadsByID, startTurn } = installWuuApi();
    threadsByID.clear();
    const first = deferred<{ thread: Thread }>();
    const second = deferred<{ thread: Thread }>();
    window.wuu.startThread = vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { setMainComposerPrompt("pending A"); });
    await act(async () => { mainComposerSendButton().click(); });
    const newConversation = container.querySelector<HTMLButtonElement>(entry === "top" ? '.primary-nav .nav-item' : '.project-row-new-thread');
    expect(newConversation).not.toBeNull();
    await act(async () => { newConversation!.click(); });
    await act(async () => { setMainComposerPrompt("pending B"); });
    await act(async () => { mainComposerSendButton().click(); });
    expect(window.wuu.startThread).toHaveBeenCalledTimes(2);
    expect(threadRowButton("pending A")).toBeDefined();
    expect(threadRowButton("pending B")).toBeDefined();
    await act(async () => { threadRowButton("pending A")!.click(); });
    expect(container.querySelector('.composer-stop-button')).not.toBeNull();
    await act(async () => { second.resolve({ thread: { ...threadB(), preview: "", turns: [] } }); });
    expect(startTurn).toHaveBeenCalledTimes(1);
    expect(startTurn.mock.calls[0].slice(0, 2)).toEqual([threadBID, "pending B"]);
    expect(container.querySelector('.composer-stop-button')).not.toBeNull();
    await act(async () => { first.resolve({ thread: { ...threadA(), preview: "", turns: [] } }); });
    expect(startTurn).toHaveBeenCalledTimes(2);
    expect(startTurn.mock.calls[1].slice(0, 2)).toEqual([threadAID, "pending A"]);
    expect(container.querySelectorAll('.thread-row-main')).toHaveLength(2);
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
  });

  it("keeps sidebar row titles and click targets after A-B-A switching and a delayed project list", async () => {
    installWuuApi();
    const projects = ["alpha", "beta"].map((id) => ({
      id, name: id, path: `/tmp/${id}`, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    }));
    let context: RuntimeContext = { kind: "project", project_id: "alpha", cwd: projects[0].path };
    const alpha = { ...threadA(), cwd: projects[0].path, workspace_id: "alpha", title: "Alpha investigation" };
    const oldBeta = { ...threadB(), cwd: projects[1].path, workspace_id: "beta", title: "Beta initial question" };
    const beta = { ...oldBeta, title: "Beta release investigation", preview: "Beta release investigation" };
    const backgroundList = deferred<{ threads: Thread[] }>();
    const betaResume = deferred<{ thread: Thread }>();
    window.wuu.listProjects = vi.fn(async () => ({ projects, active_context: context }));
    window.wuu.selectProject = vi.fn(async (id) => {
      context = { kind: "project", project_id: id, cwd: projects.find((project) => project.id === id)!.path };
      return { projects, active_context: context };
    });
    window.wuu.initialize = vi.fn(async () => ({ ...initialized(), workspace_root: context.cwd }));
    window.wuu.listThreads = vi.fn(async (cwd) => cwd === projects[1].path
      ? backgroundList.promise
      : { threads: [context.cwd === alpha.cwd ? alpha : oldBeta] });
    window.wuu.resumeThread = vi.fn(async (id) => id === beta.id ? betaResume.promise : { thread: alpha });
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => {
      const betaSection = Array.from(container.querySelectorAll(".project-row-name"))
        .find((label) => label.textContent === "beta");
      (betaSection?.closest("button") as HTMLButtonElement).click();
    });
    expect(window.wuu.listThreads).toHaveBeenCalledWith(projects[1].path);
    act(() => { emitNotification("thread/updated", { thread: oldBeta }, oldBeta.cwd); });
    expect(threadRowButton(oldBeta.title)).toBeDefined();
    await act(async () => { threadRowButton(oldBeta.title)!.click(); });
    expect(activeThreadProbe()?.dataset.threadId).toBe(beta.id);
    await act(async () => { betaResume.resolve({ thread: beta }); });
    expect(threadRowButton(beta.title)).toBeDefined();
    await act(async () => {
      const alphaSection = Array.from(container.querySelectorAll(".project-row-name"))
        .find((label) => label.textContent === "alpha");
      (alphaSection?.closest("button") as HTMLButtonElement).click();
    });
    await act(async () => { threadRowButton(alpha.title)!.click(); });
    expect(activeThreadProbe()?.dataset.threadId).toBe(alpha.id);
    await act(async () => { backgroundList.resolve({ threads: [oldBeta] }); });
    expect(threadRowButton(beta.title)).toBeDefined();
    expect(threadRowButton(oldBeta.title)).toBeUndefined();
    await act(async () => { threadRowButton(beta.title)!.click(); });
    expect(activeThreadProbe()?.dataset.threadId).toBe(beta.id);
    expect(activeSessionTabLabel()).toContain(beta.title);
  });

  it("switches to an already loaded same-runtime thread before resume resolves", async () => {
    const { resumeThread, threadsByID } = installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    expect(resumeThread).toHaveBeenCalledWith(threadAID);
    expect(activeSessionTabLabel()).toContain("session switch A");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(visibleRuntimeModel()).toContain("model-a");

    const rowB = threadRowButton("session switch B");
    expect(rowB).toBeDefined();
    await act(async () => {
      rowB?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    expect(activeSessionTabLabel()).toContain("session switch B");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadBID);
    expect(visibleRuntimeModel()).toContain("model-b");

    const delayedResumeA = deferred<{ thread: Thread }>();
    resumeThread.mockClear();
    resumeThread.mockImplementation((threadID: string) => {
      if (threadID === threadAID) {
        return delayedResumeA.promise;
      }
      return Promise.resolve({ thread: threadsByID.get(threadID) });
    });

    const tabA = threadRowButton("session switch A");
    expect(tabA).toBeDefined();
    await act(async () => {
      tabA?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });

    expect(resumeThread).toHaveBeenCalledWith(threadAID);
    expect(activeSessionTabLabel()).toContain("session switch A");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(activeThreadProbe()?.dataset.turnCount).toBe("1");
    expect(visibleRuntimeModel()).toContain("model-a");

    delayedResumeA.resolve({ thread: threadA(2) });
    await flushAsync();

    expect(activeSessionTabLabel()).toContain("session switch A");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(activeThreadProbe()?.dataset.turnCount).toBe("2");
  });

  it.each(["Enter", "click"])("accepts %s during a cached switch before background resume finishes", async (action) => {
    const { resumeThread, startTurn } = installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    // Summary lists do not load B's history. Open it once before exercising
    // the cached-switch path with a delayed resume.
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { threadRowButton("session switch A")!.click(); });

    const delayedResumeB = deferred<{ thread: Thread }>();
    resumeThread.mockImplementation((threadID: string) =>
      threadID === threadBID
        ? delayedResumeB.promise
        : Promise.resolve({ thread: threadA() }),
    );

    const rowB = threadRowButton("session switch B");
    expect(rowB).toBeDefined();
    await act(async () => {
      rowB?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      await Promise.resolve();
    });
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadBID);

    await act(async () => {
      setMainComposerPrompt("send after switching");
    });
    await act(async () => {
      if (action === "Enter") {
        mainComposerTextarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      } else {
        mainComposerSendButton().click();
      }
    });
    expect(mainComposerTextarea().value).toBe("");
    expect(startTurn).toHaveBeenCalledWith(
      threadBID,
      "send after switching",
      expect.any(Array),
      expect.any(Array),
      undefined,
      undefined,
      undefined,
      { kind: "no_project", cwd: workspace },
      expect.any(String),
    );
    await act(async () => { setMainComposerPrompt("a newer draft"); });
    delayedResumeB.resolve({ thread: threadB() });
    await flushAsync();
    expect(mainComposerTextarea().value).toBe("a newer draft");
    expect(startTurn).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["main", "Enter"], ["main", "click"], ["split", "Enter"], ["split", "click"],
  ])("starts a normal follow-up from %s via %s after the final answer while cleanup is running", async (pane, action) => {
    const { threadsByID, startTurn } = installWuuApi();
    const answerReady = runningThreadA();
    answerReady.turns[0].items[1] = {
      ...answerReady.turns[0].items[1], status: "completed", terminal: true,
    };
    threadsByID.set(threadAID, answerReady);
    window.wuu.queueTurn = vi.fn();
    window.wuu.steerTurn = vi.fn();
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    if (pane === "split") {
      await act(async () => { requestOpenThreadInSplit(threadBID); });
    }
    const composer = pane === "split"
      ? container.querySelector(".conversation-split-pane")!
      : container.querySelector('[data-main-conversation-composer="dock"]')!;
    const textarea = composer.querySelector<HTMLTextAreaElement>("textarea")!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, "next question");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      if (action === "Enter") {
        textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      } else {
        composer.querySelector<HTMLButtonElement>(".composer-send-button")!.click();
      }
    });
    expect(startTurn).toHaveBeenCalledTimes(1);
    expect(startTurn.mock.calls[0].slice(0, 2)).toEqual([threadAID, "next question"]);
    expect(window.wuu.queueTurn).not.toHaveBeenCalled();
    expect(window.wuu.steerTurn).not.toHaveBeenCalled();
    expect(textarea.value).toBe("");
  });

  it.each(["", "newer draft"])("preserves failed input without overwriting the current draft (%s)", async (newerDraft) => {
    const { startTurn } = installWuuApi();
    const pending = deferred<{ turn: Turn }>();
    startTurn.mockReturnValueOnce(pending.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    // Repeating an earlier prompt is not evidence that this send was admitted.
    const text = threadA().turns[0].items[0].text!;
    await act(async () => { setMainComposerPrompt(text); });
    await act(async () => { mainComposerSendButton().click(); });
    expect(mainComposerTextarea().value).toBe("");
    await act(async () => { setMainComposerPrompt(newerDraft); });
    await act(async () => { pending.reject(new Error("send rejected")); });
    expect(mainComposerTextarea().value).toBe(newerDraft || text);
    if (newerDraft) {
      expect(activeThreadProbe()?.dataset.latestUserText).toBe(text);
      expect(activeThreadProbe()?.dataset.latestTurnStatus).toBe("failed");
    }
    expect(startTurn).toHaveBeenCalledTimes(1);
  });

  it.each(["accepted", "rejected"])("does not reactivate a submitted conversation when its delayed response is %s", async (outcome) => {
    const { startTurn } = installWuuApi();
    const pending = deferred<{ turn: Turn }>();
    startTurn.mockReturnValueOnce(pending.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { setMainComposerPrompt("send to A"); });
    await act(async () => { mainComposerSendButton().click(); });
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { setMainComposerPrompt("draft for B"); });
    await act(async () => {
      if (outcome === "accepted") {
        pending.resolve({ turn: { id: "accepted", items_view: "full", status: "in_progress", items: [] } });
      } else {
        pending.reject(new Error("A failed"));
      }
    });
    expect(activeSessionTabLabel()).toContain("session switch B");
    expect(mainComposerTextarea().value).toBe("draft for B");
    expect(startTurn).toHaveBeenCalledTimes(1);
  });

  it("keeps the captured workspace while an attachment finishes preparing after a switch", async () => {
    const { threadsByID, startTurn } = installWuuApi();
    const projects = [
      { id: "alpha", name: "Alpha", path: workspace },
      { id: "beta", name: "Beta", path: `${workspace}-other` },
    ].map((project) => ({ ...project, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" }));
    let context: RuntimeContext = { kind: "project", project_id: "alpha", cwd: workspace };
    threadsByID.set(threadAID, { ...threadA(), workspace_id: "alpha" });
    const otherThread = { ...threadB(), workspace_id: "beta", cwd: projects[1].path };
    threadsByID.set(threadBID, otherThread);
    window.wuu.listProjects = vi.fn(async () => ({ projects, active_context: context }));
    window.wuu.selectProject = vi.fn(async (id) => {
      context = { kind: "project", project_id: id, cwd: projects.find((project) => project.id === id)!.path };
      return { projects, active_context: context };
    });
    window.wuu.initialize = vi.fn(async () => ({ ...initialized(), workspace_root: context.cwd }));
    const encoded = { id: "prepared-image", media_type: "image/png", data: "aW1hZ2U=" };
    const encoding = deferred<ComposerMessages.ComposerImage>();
    vi.spyOn(ComposerMessages, "composerImagePlaceholder").mockReturnValue({ ...encoded, data: "", encodePromise: encoding.promise });
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => {
      const input = container.querySelector<HTMLInputElement>('[data-main-conversation-composer="dock"] input[type="file"]')!;
      Object.defineProperty(input, "files", { value: [new File(["image"], "image.png", { type: "image/png" })] });
      input.dispatchEvent(new Event("change", { bubbles: true }));
      setMainComposerPrompt("inspect this in alpha");
    });
    await act(async () => { mainComposerSendButton().click(); });
    expect(startTurn).not.toHaveBeenCalled();
    await act(async () => {
      const section = Array.from(container.querySelectorAll(".project-row-name")).find((label) => label.textContent === "Beta");
      (section!.closest("button") as HTMLButtonElement).click();
    });
    await act(async () => { emitNotification("thread/updated", { thread: otherThread }, otherThread.cwd); });
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { setMainComposerPrompt("beta draft"); });
    await act(async () => { encoding.resolve(encoded); });
    expect(startTurn).toHaveBeenCalledExactlyOnceWith(
      threadAID, "inspect this in alpha", [{ media_type: encoded.media_type, data: encoded.data }], [],
      undefined, undefined, undefined, { kind: "project", project_id: "alpha", cwd: workspace },
      expect.any(String),
    );
    expect(activeSessionTabLabel()).toContain("session switch B");
    expect(mainComposerTextarea().value).toBe("beta draft");
  });

  it("shows the admitted model during a turn and the session pin after it settles", async () => {
    installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    expect(visibleRuntimeModel()).toContain("model-a");

    const runningTurn = {
      id: "thread-switch-a-running-turn",
      model_provider: "turn-provider",
      model: "turn-model",
      items_view: "full",
      status: "in_progress",
      items: [],
    };
    await act(async () => {
      emitNotification("turn/started", {
        thread_id: threadAID,
        turn: runningTurn,
      });
    });

    expect(visibleRuntimeModel()).toContain("turn-model");

    await act(async () => {
      emitNotification("turn/completed", {
        thread_id: threadAID,
        turn: { ...runningTurn, status: "completed" },
      });
    });

    expect(visibleRuntimeModel()).toContain("model-a");
  });

  it.each(["focus", "running snapshot", "active reconciliation"])("repairs a missed completion via %s with summary-only lists", async trigger => {
    vi.useFakeTimers();
    const { threadsByID, resumeThread } = installWuuApi();
    threadsByID.set(threadAID, runningThreadA());
    let onRunning!: Parameters<WuuDesktopApi["onRunningThreadsChanged"]>[0];
    window.wuu.onRunningThreadsChanged = handler => {
      onRunning = handler;
      return () => {};
    };

    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    expect(activeThreadProbe()?.dataset.latestTurnStatus).toBe("in_progress");
    expect(container.querySelector(".composer-stop-button")).not.toBeNull();

    resumeThread.mockClear();
    // The app-server completed durably, but this renderer missed the terminal
    // event during a tab/workspace transition.
    const completed = threadA();
    completed.turns[0].items[1].text = "Recovered final answer";
    threadsByID.set(threadAID, completed);
    await act(async () => {
      if (trigger === "focus") window.dispatchEvent(new Event("focus"));
      else if (trigger === "running snapshot") onRunning([]);
      else await vi.advanceTimersByTimeAsync(200);
    });
    await flushAsync();

    expect(activeThreadProbe()?.dataset.latestTurnStatus).toBe("completed");
    expect(container.querySelector(".composer-stop-button")).toBeNull();
    expect(container.querySelector(".composer-send-button")).not.toBeNull();
    expect(activeThreadProbe()?.dataset.latestAgentText).toBe("Recovered final answer");
    expect(resumeThread).toHaveBeenCalledExactlyOnceWith(threadAID);
  });

  it("shows a managed follow-up emitted by another workspace's executor", async () => {
    installWuuApi();
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();

    const turn = {
      id: "managed-follow-up",
      items_view: "full",
      status: "in_progress",
      items: [{ id: "managed-input", type: "user_message", text: "Check the revised task" }],
    };
    await act(async () => {
      emitNotification("turn/started", { thread_id: threadAID, turn }, "/another-executor");
    });
    expect(activeThreadProbe()?.dataset.latestUserText).toBe("Check the revised task");
    expect(activeThreadProbe()?.dataset.latestTurnStatus).toBe("in_progress");
    expect(container.querySelector(".composer-stop-button")).not.toBeNull();

    await act(async () => {
      emitNotification("turn/completed", { thread_id: threadAID, turn: { ...turn, status: "completed" } }, "/another-executor");
      emitNotification("turn/started", { thread_id: "unrelated-session", turn }, "/another-executor");
      emitNotification("config/changed", { provider: "other", model: "other" }, "/another-executor");
    });
    expect(activeThreadProbe()?.dataset.latestTurnStatus).toBe("completed");
    expect(container.querySelector(".composer-stop-button")).toBeNull();
    expect(visibleRuntimeModel()).toContain("model-a");
  });

  it("recovers a missed managed start from the aggregate running snapshot", async () => {
    const { threadsByID, resumeThread } = installWuuApi();
    let onRunning!: (snapshot: Array<{ workdir: string; thread_id: string }>) => void;
    window.wuu.onRunningThreadsChanged = handler => {
      onRunning = handler;
      return () => {};
    };
    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
    });
    await flushAsync();
    resumeThread.mockClear();
    // The project client still has the old snapshot; only the execution
    // owner's resume can supply the missed turn.
    vi.mocked(window.wuu.listThreads).mockResolvedValue({
      threads: [threadA(), threadB()].map(thread => ({ ...thread, turns: [] })),
    });
    const running = threadA();
    running.status = "in_progress";
    running.turns.push({
      id: "missed-follow-up", status: "in_progress", items_view: "full",
      items: [{ id: "missed-input", type: "user_message", text: "New managed input" }],
    });
    threadsByID.set(threadAID, running);
    vi.useFakeTimers();
    await act(async () => {
      onRunning([{ workdir: "/another-executor", thread_id: threadAID }]);
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(resumeThread).toHaveBeenCalledWith(threadAID);
    expect(activeThreadProbe()?.dataset.latestUserText).toBe("New managed input");
    expect(container.querySelector(".composer-stop-button")).not.toBeNull();
  });
});
