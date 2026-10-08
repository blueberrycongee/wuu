/**
 * Session switching keeps the outgoing conversation covered until the resumed
 * target has restored its presentation and scroll position.
 */
import { act, Fragment, type ReactNode } from "react";
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

// Exercise the retained panel interactions while the product entry point is hidden.
vi.mock("./FeatureFlags", async (importOriginal) => ({
  ...await importOriginal<typeof import("./FeatureFlags")>(),
  ENABLE_ENVIRONMENT_PANEL: true,
}));

const turnListFixture = vi.hoisted(() => ({ renderTurns: false }));

vi.mock("./ConversationTurnList", () => ({
  ConversationTurnList: ({
    threadID,
    turns,
    renderTurn,
  }: {
    threadID: string;
    turns: Turn[];
    renderTurn: (turn: Turn) => ReactNode;
  }): JSX.Element => (
    <div
      data-testid="turn-list-probe"
      data-thread-id={threadID}
      data-turn-count={turns.length}
      data-latest-turn-status={turns.at(-1)?.status}
      data-latest-user-text={turns.at(-1)?.items?.find(item => item.type === "user_message")?.text}
      data-latest-agent-text={turns.at(-1)?.items?.find(item => item.type === "agent_message")?.text}
    >
      {turnListFixture.renderTurns ? turns.map(turn => <Fragment key={turn.id}>{renderTurn(turn)}</Fragment>) : null}
    </div>
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
import { mockAnimationFrames } from "./AnimationFrameTestHarness";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let animationFrames: ReturnType<typeof mockAnimationFrames>;

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
  animationFrames = mockAnimationFrames();
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
  await animationFrames.flush();
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

async function pickPendingAttachment(owner: Element, extension: "pdf" | "png" | "mp4") {
  const bytes = new TextEncoder().encode(`attachment owned by A (${extension})`);
  const read = deferred<ArrayBuffer>();
  const encoded = deferred<void>();
  const originalRead = FileReader.prototype.readAsDataURL;
  vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader, blob) {
    this.addEventListener("loadend", () => encoded.resolve(), { once: true });
    originalRead.call(this, blob);
  });
  if (extension !== "pdf") {
    vi.stubGlobal("URL", class extends URL {
      static createObjectURL = vi.fn(() => "blob:pending-owner-image");
      static revokeObjectURL = vi.fn();
    });
  }
  const type = extension === "pdf" ? "application/pdf" : extension === "png" ? "image/png" : "video/mp4";
  const file = new File([bytes], `A-only.${extension}`, { type });
  Object.defineProperty(file, "arrayBuffer", { value: () => read.promise });
  await act(async () => {
    const input = owner.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(input, "files", { configurable: true, value: [file] });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  return {
    data: btoa(new TextDecoder().decode(bytes)),
    finish: async () => {
      await act(async () => {
        read.resolve(bytes.buffer);
        await encoded.promise;
      });
    },
    fail: async () => { await act(async () => read.reject(new Error("File read failed"))); },
  };
}

const mainComposerSelector = '[data-main-conversation-composer="dock"]';
const attachmentCardSelector = ".composer-attachment-card";

describe("session tab switch latency", () => {
  beforeEach(() => {
    installWindowStubs();
    turnListFixture.renderTurns = false;
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
    vi.unstubAllGlobals();
    container.remove();
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it.each([false, true, "error"] as const)("waits for confirmed Git status before automatically opening workspace info (repository=%s)", async (isRepository) => {
    vi.useFakeTimers();
    vi.stubGlobal("innerWidth", 1600);
    vi.stubGlobal("innerHeight", 900);
    installWuuApi();
    const status = deferred<Awaited<ReturnType<typeof window.wuu.gitStatus>>>();
    vi.mocked(window.wuu.gitStatus).mockReturnValue(status.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(activeSessionTabLabel()).toContain("session switch A");
    expect(window.wuu.gitStatus).toHaveBeenCalled();
    const toggle = container.querySelector<HTMLButtonElement>(".environment-toggle-button")!;
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    expect(container.querySelector(".environment-panel")).toBeNull();

    await act(async () => {
      if (isRepository === "error") status.reject(new Error("Git status temporarily unavailable"));
      else status.resolve({ is_repo: isRepository, dirty_count: 0 });
    });
    await flushAsync();
    expect(toggle.getAttribute("aria-pressed")).toBe(String(isRepository === true));
    if (isRepository === true) {
      // A user's dismissal must survive later conversation/status refreshes.
      await act(async () => { toggle.click(); });
      await act(async () => { threadRowButton("session switch B")!.click(); });
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(toggle.getAttribute("aria-pressed")).toBe("false");
    } else {
      expect(container.querySelector(".environment-panel")).toBeNull();
    }
  });

  it.each([false, true])("preserves explicit workspace-info intent while Git detection is pending (dismiss=%s)", async (dismiss) => {
    vi.useFakeTimers();
    vi.stubGlobal("innerWidth", 1600);
    vi.stubGlobal("innerHeight", 900);
    installWuuApi();
    const status = deferred<Awaited<ReturnType<typeof window.wuu.gitStatus>>>();
    vi.mocked(window.wuu.gitStatus).mockReturnValue(status.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    const toggle = container.querySelector<HTMLButtonElement>(".environment-toggle-button")!;
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    await act(async () => { toggle.click(); });
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    if (dismiss) await act(async () => { toggle.click(); });
    await act(async () => { status.resolve({ is_repo: dismiss, dirty_count: 0 }); });
    await flushAsync();
    expect(toggle.getAttribute("aria-pressed")).toBe(String(!dismiss));
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(toggle.getAttribute("aria-pressed")).toBe(String(!dismiss));
  });

  it("persists the latest mounted effort-slider choice before the previous request settles", async () => {
    const { threadsByID } = installWuuApi();
    threadsByID.set(threadAID, { ...threadA(), model_variant: "high", model_effort: "high" });
    const workspaceDefaults = initialized();
    workspaceDefaults.providers![0].models = [{ id: "model-a", supported_efforts: ["low", "medium", "high"] }];
    vi.mocked(window.wuu.initialize).mockResolvedValue(workspaceDefaults);
    const first = deferred<InitializeResult>();
    const update = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(workspaceDefaults);
    window.wuu.updateRuntimeSettings = update;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!.click(); });
    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(slider).not.toBeNull();
    const choose = async (value: string, key: string): Promise<void> => {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(slider, value);
        slider.dispatchEvent(new Event("input", { bubbles: true }));
        slider.dispatchEvent(new KeyboardEvent("keyup", { key, bubbles: true }));
      });
    };
    await choose(slider.min, "Home");
    await choose(slider.max, "End");
    await act(async () => { first.resolve(workspaceDefaults); });
    await flushAsync();

    expect(update.mock.calls.map(call => call[4])).toEqual(["", "high"]);
    expect(update.mock.calls.every(call => call[6] === threadAID)).toBe(true);
    expect(slider.value).toBe(slider.max);
  });

  it("preserves Home after reopening the effort picker while End is still pending", async () => {
    const { threadsByID } = installWuuApi();
    threadsByID.set(threadAID, { ...threadA(), model_variant: "", model_effort: "" });
    const workspaceDefaults = initialized();
    workspaceDefaults.providers![0].models = [{ id: "model-a", supported_efforts: ["low", "medium", "high"] }];
    vi.mocked(window.wuu.initialize).mockResolvedValue(workspaceDefaults);
    const first = deferred<InitializeResult>();
    const update = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(workspaceDefaults);
    window.wuu.updateRuntimeSettings = update;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const toggle = () => container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!.click();
    await act(async () => { toggle(); });
    let slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(slider.value).toBe(slider.min);
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(slider, slider.max);
      slider.dispatchEvent(new Event("input", { bubbles: true }));
      slider.dispatchEvent(new KeyboardEvent("keyup", { key: "End", bubbles: true }));
    });
    expect(update.mock.calls.map(call => call[4])).toEqual(["high"]);
    await act(async () => { toggle(); });
    await act(async () => { toggle(); });
    slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(slider.value).toBe(slider.min);
    // Native Home does not emit input/change when the range already shows min.
    await act(async () => {
      slider.focus();
      slider.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
      slider.dispatchEvent(new KeyboardEvent("keyup", { key: "Home", bubbles: true }));
    });
    await act(async () => { first.resolve(workspaceDefaults); });
    await flushAsync();
    expect(update.mock.calls.map(call => call[4])).toEqual(["high", ""]);
    expect(slider.value).toBe(slider.min);
  });

  it.each(["switch", "away-and-back"])("does not reopen a related-session split after newer cached navigation: %s", async (navigation) => {
    const { threadsByID, resumeThread } = installWuuApi();
    const child = { ...threadB(), id: "related-child", preview: "related child" };
    threadsByID.set(child.id, child);
    const source = threadA();
    source.turns[0].items[0] = { ...source.turns[0].items[0], origin: "host", presentation_kind: "session_message", related_session_id: child.id };
    threadsByID.set(threadAID, source);
    turnListFixture.renderTurns = true;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    // Cache B before requesting the child, then navigate again while its
    // next resume is behind the child in the IPC stream.
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await flushAsync();
    await act(async () => { threadRowButton("session switch A")!.click(); });
    await flushAsync();
    const childResume = deferred<{ thread: Thread }>();
    const bResume = deferred<{ thread: Thread }>();
    const aResume = deferred<{ thread: Thread }>();
    resumeThread.mockImplementation((id: string) => id === child.id ? childResume.promise
      : id === threadBID ? bResume.promise : id === threadAID ? aResume.promise : Promise.resolve({ thread: threadsByID.get(id) }));
    const sourceButton = container.querySelector<HTMLButtonElement>(".session-message-source");
    expect(sourceButton).not.toBeNull();
    await act(async () => { sourceButton!.click(); });
    expect(resumeThread).toHaveBeenLastCalledWith(child.id);
    await act(async () => { threadRowButton("session switch B")!.click(); });
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(container.querySelector(".conversation-split-pane")).toBeNull();
    if (navigation === "away-and-back") {
      await act(async () => { threadRowButton("session switch A")!.click(); });
      expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    }
    // The real bridge emits each resume snapshot before resolving its IPC call.
    // Deliver both notifications and responses in FIFO request order.
    await act(async () => { emitNotification("thread/resumed", { thread: child }); childResume.resolve({ thread: child }); });
    await flushAsync();
    const splitAfterOldResume = [...container.querySelectorAll<HTMLElement>(".conversation-split-pane")].map(node => node.dataset.threadId);
    await act(async () => { emitNotification("thread/resumed", { thread: threadB() }); bResume.resolve({ thread: threadB() }); });
    if (navigation === "away-and-back") await act(async () => { emitNotification("thread/resumed", { thread: source }); aResume.resolve({ thread: source }); });
    await flushAsync();
    const splitAfterAllResponses = [...container.querySelectorAll<HTMLElement>(".conversation-split-pane")].map(node => node.dataset.threadId);
    expect({ splitAfterOldResume, splitAfterAllResponses }).toEqual({ splitAfterOldResume: [], splitAfterAllResponses: [] });
    expect(activeThreadProbe()?.dataset.threadId).toBe(navigation === "switch" ? threadBID : threadAID);
  });

  it.each([false, true])("preserves the primary draft when opening and closing a related-session split (delayed=%s)", async (delayed) => {
    const { threadsByID, resumeThread } = installWuuApi();
    const child = { ...threadB(), id: "related-child", preview: "related child" };
    threadsByID.set(child.id, child);
    const source = threadA();
    source.turns[0].items[0] = { ...source.turns[0].items[0], origin: "host", presentation_kind: "session_message", related_session_id: child.id };
    threadsByID.set(threadAID, source);
    turnListFixture.renderTurns = true;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    act(() => setMainComposerPrompt("unsent primary draft"));
    expect(mainComposerTextarea().value).toBe("unsent primary draft");
    const pending = deferred<{ thread: Thread }>();
    if (delayed) resumeThread.mockImplementationOnce(() => pending.promise);
    await act(async () => { container.querySelector<HTMLButtonElement>(".session-message-source")!.click(); });
    if (delayed) {
      act(() => setMainComposerPrompt("newer primary draft"));
      await act(async () => { emitNotification("thread/resumed", { thread: child }); pending.resolve({ thread: child }); });
    }
    await flushAsync();
    const whileSplit = container.querySelector<HTMLTextAreaElement>(`.conversation-split-pane[data-thread-id="${threadAID}"] textarea`)!.value;
    await act(async () => { container.querySelector<HTMLButtonElement>(".conversation-split-close")!.click(); });
    await flushAsync();
    const afterClose = mainComposerTextarea().value;
    const expected = delayed ? "newer primary draft" : "unsent primary draft";
    expect({ whileSplit, afterClose }).toEqual({ whileSplit: expected, afterClose: expected });
  });

  it("keeps only the latest related-session request and restores replaced pane drafts", async () => {
    const { threadsByID, resumeThread } = installWuuApi();
    const first = { ...threadB(), id: "first-child", preview: "first child" };
    const second = { ...threadB(), id: "second-child", preview: "second child" };
    threadsByID.set(first.id, first); threadsByID.set(second.id, second);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const firstRead = deferred<{ thread: Thread }>();
    const secondRead = deferred<{ thread: Thread }>();
    resumeThread.mockImplementationOnce(() => firstRead.promise).mockImplementationOnce(() => secondRead.promise);
    await act(async () => { requestOpenThreadInSplit(first.id); requestOpenThreadInSplit(second.id); });
    await act(async () => { emitNotification("thread/resumed", { thread: first }); firstRead.resolve({ thread: first }); });
    const stalePane = container.querySelector(".conversation-split-pane");
    await act(async () => { emitNotification("thread/resumed", { thread: second }); secondRead.resolve({ thread: second }); });
    await flushAsync();
    const secondary = () => container.querySelector<HTMLTextAreaElement>(`.conversation-split-pane[data-thread-id="${second.id}"] textarea`)!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(secondary(), "draft for second child");
      secondary().dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => { requestOpenThreadInSplit(first.id); });
    const firstChildDraft = container.querySelector<HTMLTextAreaElement>(`.conversation-split-pane[data-thread-id="${first.id}"] textarea`)!.value;
    await act(async () => { requestOpenThreadInSplit(second.id); });
    const recoveredDraft = secondary().value;
    await act(async () => { requestOpenThreadInSplit(second.id); });
    expect({ stalePaneOpened: !!stalePane, firstChildDraft, recoveredDraft, repeatedDraft: secondary().value }).toEqual({
      stalePaneOpened: false, firstChildDraft: "", recoveredDraft: "draft for second child", repeatedDraft: "draft for second child",
    });
  });

  it.each([
    { activePane: "primary", direct: false }, { activePane: "secondary", direct: false },
    { activePane: "primary", direct: true }, { activePane: "secondary", direct: true },
  ])("preserves both split drafts from $activePane (direct pane promotion=$direct)", async ({ activePane, direct }) => {
    const { threadsByID } = installWuuApi();
    const third = { ...threadB(), id: "thread-third", preview: "third conversation" };
    threadsByID.set(third.id, third);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await flushAsync();
    await act(async () => { threadRowButton("session switch A")!.click(); });
    await flushAsync();
    act(() => setMainComposerPrompt("older global A draft"));
    await act(async () => { requestOpenThreadInSplit(threadBID); });
    await flushAsync();
    const pane = (id: string) => container.querySelector<HTMLElement>(`.conversation-split-pane[data-thread-id="${id}"]`)!;
    const setSplit = (id: string, text: string) => {
      const input = pane(id).querySelector<HTMLTextAreaElement>("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, text);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    };
    act(() => { setSplit(threadAID, "new draft for A"); setSplit(threadBID, "new draft for B"); });
    const activeID = activePane === "primary" ? threadAID : threadBID;
    act(() => pane(activeID).dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(pane(activeID).classList.contains("active")).toBe(true);
    // All calls settle in request order; there is no delayed or reversed IPC.
    const destination = direct ? (activePane === "primary" ? "session switch B" : "session switch A") : "third conversation";
    await act(async () => { threadRowButton(destination)!.click(); });
    await flushAsync();
    expect(mainComposerTextarea().value).toBe(direct ? (activePane === "primary" ? "new draft for B" : "new draft for A") : "");
    await act(async () => { threadRowButton("session switch A")!.click(); });
    await flushAsync();
    const restoredA = mainComposerTextarea().value;
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await flushAsync();
    const restoredB = mainComposerTextarea().value;
    expect({ restoredA, restoredB }).toEqual({ restoredA: "new draft for A", restoredB: "new draft for B" });
  });

  it("keeps the rendered target draft when a delayed fork completes after switching", async () => {
    const { threadsByID } = installWuuApi();
    const source = threadA();
    source.turns[0].items[1] = { ...source.turns[0].items[1], terminal: true, status: "completed" };
    threadsByID.set(threadAID, source);
    turnListFixture.renderTurns = true;
    const fork = deferred<{ thread: Thread }>();
    window.wuu.forkThread = vi.fn(() => fork.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const forkButton = container.querySelector<HTMLButtonElement>(
      '.agent-message-actions [aria-label="分叉"]');
    expect(forkButton).not.toBeNull();
    await act(async () => { forkButton!.click(); });
    const destination = document.querySelector<HTMLButtonElement>(".fork-dialog-option:not(:disabled)");
    expect(destination).not.toBeNull();
    await act(async () => { destination!.click(); });
    expect(window.wuu.forkThread).toHaveBeenCalledOnce();
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { setMainComposerPrompt("draft owned by B"); });

    await act(async () => { fork.resolve({ thread: { ...threadA(), id: "delayed-fork", preview: "delayed fork" } }); });
    await flushAsync();

    expect(activeThreadProbe()?.dataset.threadId).toBe(threadBID);
    expect(mainComposerTextarea().value).toBe("draft owned by B");
    await act(async () => { threadRowButton("session switch A")!.click(); });
    expect(mainComposerTextarea().value).toBe("");
    await act(async () => { threadRowButton("session switch B")!.click(); });
    expect(mainComposerTextarea().value).toBe("draft owned by B");
  });

  it("keeps the rendered composer draft and attachment when archive fails", async () => {
    installWuuApi();
    const archive = deferred<{ thread: Thread }>();
    window.wuu.archiveThread = vi.fn(() => archive.promise);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const attachment = await pickPendingAttachment(container.querySelector(mainComposerSelector)!, "pdf");
    await attachment.finish();
    await act(async () => { setMainComposerPrompt("unsent archive draft"); });
    const row = threadRowButton("session switch A")!.closest(".thread-row");
    const archiveButton = row?.querySelector<HTMLButtonElement>(".thread-row-action.archive");
    expect(archiveButton).not.toBeNull();
    await act(async () => { archiveButton!.click(); });
    expect(window.wuu.archiveThread).toHaveBeenCalledOnce();
    expect(mainComposerTextarea().value).toBe("unsent archive draft");
    await act(async () => { archive.reject(new Error("synthetic archive failure")); });
    await flushAsync();

    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(mainComposerTextarea().value).toBe("unsent archive draft");
    expect(container.querySelectorAll(`${mainComposerSelector} ${attachmentCardSelector}`)).toHaveLength(1);
  });

  it.each([
    { split: false, failure: false }, { split: true, failure: false },
    { split: false, failure: true }, { split: true, failure: true },
  ])("keeps attachment completion while a cold navigation waits (split=$split, failure=$failure)", async ({ split, failure }) => {
    const { threadsByID, resumeThread } = installWuuApi();
    const third = { ...threadB(), id: "cold-third", preview: "cold third" };
    threadsByID.set(third.id, third);
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    if (split) await act(async () => { requestOpenThreadInSplit(threadBID); });
    const ownerID = split ? threadBID : threadAID;
    const owner = split ? container.querySelector(`.conversation-split-pane[data-thread-id="${ownerID}"]`)! : container.querySelector(mainComposerSelector)!;
    const attachment = await pickPendingAttachment(owner, "png");
    const pending = deferred<{ thread: Thread }>();
    resumeThread.mockImplementationOnce(() => pending.promise);
    await act(async () => { threadRowButton("cold third")!.click(); });
    expect(resumeThread).toHaveBeenLastCalledWith(third.id);
    expect(owner.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(true);
    if (failure) await attachment.fail(); else await attachment.finish();
    await act(async () => { emitNotification("thread/resumed", { thread: third }); pending.resolve({ thread: third }); });
    await flushAsync();
    await act(async () => { threadRowButton(split ? "session switch B" : "session switch A")!.click(); });
    await flushAsync();
    if (failure) expect(container.querySelector(`${mainComposerSelector} ${attachmentCardSelector}`)).toBeNull();
    else expect(container.querySelector(`${mainComposerSelector} img`)?.getAttribute("src")).toBe(`data:image/png;base64,${attachment.data}`);
  });

  it.each(["pdf", "png", "mp4"] as const)("keeps a pending %s on its original draft after switching conversations", async (extension) => {
    const { startTurn } = installWuuApi();
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const attachment = await pickPendingAttachment(container.querySelector(mainComposerSelector)!, extension);
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await attachment.finish();
    expect(container.querySelector(`${mainComposerSelector} ${attachmentCardSelector}`)).toBeNull();
    await act(async () => { threadRowButton("session switch A")!.click(); });
    await flushAsync();
    expect(container.querySelectorAll(`${mainComposerSelector} ${attachmentCardSelector}`)).toHaveLength(1);
    if (extension === "png") {
      expect(container.querySelector(`${mainComposerSelector} img`)?.getAttribute("src")).toBe(`data:image/png;base64,${attachment.data}`);
    }
    await act(async () => { mainComposerSendButton().click(); });
    expect(startTurn).toHaveBeenCalledOnce();
    expect(startTurn.mock.calls[0][0]).toBe(threadAID);
    expect(startTurn.mock.calls[0][extension === "png" ? 2 : 3][0].data).toBe(attachment.data);
  });

  it.each(["pdf", "png"] as const)("finishes a %s after its draft moves from a split pane to the main composer", async (extension) => {
    installWuuApi();
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { requestOpenThreadInSplit(threadBID); });
    const attachment = await pickPendingAttachment(container.querySelector(`.conversation-split-pane[data-thread-id="${threadAID}"]`)!, extension);
    await act(async () => { container.querySelector<HTMLButtonElement>(".conversation-split-close")!.click(); });
    await attachment.finish();
    expect(container.querySelectorAll(`${mainComposerSelector} ${attachmentCardSelector}`)).toHaveLength(1);
    if (extension === "png") {
      expect(container.querySelector(`${mainComposerSelector} img`)?.getAttribute("src")).toBe(`data:image/png;base64,${attachment.data}`);
    }
  });

  it.each(["remove", "send", "failure"] as const)("does not restore a pending PDF after %s", async (action) => {
    const { startTurn } = installWuuApi();
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const attachment = await pickPendingAttachment(container.querySelector(mainComposerSelector)!, "pdf");
    expect(container.querySelectorAll(`${mainComposerSelector} ${attachmentCardSelector}`)).toHaveLength(1);
    if (action === "remove") {
      await act(async () => { container.querySelector<HTMLButtonElement>(`${mainComposerSelector} .composer-attachment-card-remove`)!.click(); });
    } else if (action === "send") {
      await act(async () => { mainComposerSendButton().click(); });
      expect(startTurn).not.toHaveBeenCalled();
    }
    if (action === "failure") await attachment.fail();
    else await attachment.finish();
    expect(container.querySelector(`${mainComposerSelector} ${attachmentCardSelector}`)).toBeNull();
    if (action === "send") {
      expect(startTurn).toHaveBeenCalledOnce();
      expect(startTurn.mock.calls[0][3][0].data).toBe(attachment.data);
    } else expect(startTurn).not.toHaveBeenCalled();
    await act(async () => { threadRowButton("session switch B")!.click(); });
    await act(async () => { threadRowButton("session switch A")!.click(); });
    expect(container.querySelector(`${mainComposerSelector} ${attachmentCardSelector}`)).toBeNull();
  });

  it.each(["pdf", "png"] as const)("does not revive a closed split draft when its %s finishes", async (extension) => {
    installWuuApi();
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    await act(async () => { requestOpenThreadInSplit(threadBID); });
    const attachment = await pickPendingAttachment(container.querySelector(`.conversation-split-pane[data-thread-id="${threadBID}"]`)!, extension);
    await act(async () => { container.querySelector<HTMLButtonElement>(".conversation-split-close")!.click(); });
    await attachment.finish();
    expect(container.querySelector(attachmentCardSelector)).toBeNull();
    await act(async () => { requestOpenThreadInSplit(threadBID); });
    expect(container.querySelector(attachmentCardSelector)).toBeNull();
  });

  it.each(["queue", "steer"] as const)("waits for pending PDF bytes before %s without refilling the sent draft", async (action) => {
    const { threadsByID } = installWuuApi();
    threadsByID.set(threadAID, runningThreadA());
    const submit = vi.fn(async () => ({ queued: { id: "queued-pdf", thread_id: threadAID }, turn_id: threadA().turns[0].id }));
    window.wuu.queueTurn = submit;
    window.wuu.steerTurn = submit;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const attachment = await pickPendingAttachment(container.querySelector(mainComposerSelector)!, "pdf");
    await act(async () => {
      mainComposerTextarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", ctrlKey: action === "queue", bubbles: true }));
    });
    expect(submit).not.toHaveBeenCalled();
    await attachment.finish();
    expect(submit).toHaveBeenCalledOnce();
    const args = vi.mocked(action === "queue" ? window.wuu.queueTurn : window.wuu.steerTurn).mock.calls[0];
    expect(args[0]).toBe(threadAID);
    expect(args[action === "queue" ? 4 : 5]).toEqual([{ media_type: "application/pdf", filename: "A-only.pdf", data: attachment.data }]);
    expect(container.querySelector(`${mainComposerSelector} ${attachmentCardSelector}`)).toBeNull();
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
    expect(container.querySelector('.view-switch-loading-conversation')).not.toBeNull();
    await act(async () => { betaResume.resolve({ thread: beta }); });
    await flushAsync();
    expect(activeThreadProbe()?.dataset.threadId).toBe(beta.id);
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

  it.each(["thread", "new-draft", "resume-error", "source-unavailable"])("restores the source runtime when abandoning a cross-workspace resume via %s", async navigation => {
    installWuuApi();
    const projects = ["alpha", "beta"].map(id => ({
      id, name: id, path: "/tmp/" + id, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    }));
    let context: RuntimeContext = { kind: "project", project_id: "alpha", cwd: projects[0].path };
    const alpha = { ...threadA(), cwd: projects[0].path, workspace_id: "alpha" };
    const beta = { ...threadB(), cwd: projects[1].path, workspace_id: "beta" };
    const pending = deferred<{ thread: Thread }>();
    let sourceUnavailable = false;
    window.wuu.listProjects = vi.fn(async () => ({ projects, active_context: context }));
    window.wuu.selectProject = vi.fn<WuuDesktopApi["selectProject"]>(async id => {
      context = { kind: "project", project_id: id, cwd: projects.find(project => project.id === id)!.path };
      return {
        projects: projects.map(project => ({ ...project, missing: sourceUnavailable && project.id === "alpha" })),
        active_context: context,
        runtime_issue: sourceUnavailable && id === "alpha"
          ? { code: "active_project_unavailable", message: "Source workspace is unavailable", project_id: "alpha", cwd: projects[0].path }
          : undefined,
      };
    });
    window.wuu.initialize = vi.fn(async () => ({ ...initialized(), workspace_root: context.cwd }));
    window.wuu.listThreads = vi.fn(async cwd => ({ threads: [cwd === beta.cwd ? beta : alpha] }));
    window.wuu.resumeThread = vi.fn(async id => id === beta.id ? pending.promise : { thread: alpha });
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    act(() => { setMainComposerPrompt("Retained source draft"); });
    const projectRow = (id: string) => Array.from(container.querySelectorAll(".project-row-name"))
      .find(label => label.textContent === id)!.closest(".project-group")!;
    await act(async () => { (projectRow("beta").querySelector(".project-row-name")!.closest("button") as HTMLButtonElement).click(); });
    await flushAsync();
    await act(async () => { threadRowButton("session switch B")!.click(); });
    expect(context.project_id).toBe("beta");
    expect(activeThreadProbe()?.dataset.threadId).toBe(alpha.id);
    if (navigation === "resume-error" || navigation === "source-unavailable") {
      sourceUnavailable = navigation === "source-unavailable";
      await act(async () => { pending.reject(new Error("Target resume failed")); });
      await flushAsync();
      expect(context.project_id).toBe("alpha");
      expect(activeSessionTabLabel()).toContain(alpha.preview);
      if (sourceUnavailable) {
        expect(container.querySelector(".composer-send-button")).toBeNull();
        expect(container.querySelector(".session-switch-loading")).toBeNull();
        sourceUnavailable = false;
        window.wuu.resumeThread = vi.fn(async id => ({ thread: id === beta.id ? beta : alpha }));
        await act(async () => { threadRowButton(beta.preview)!.click(); });
        await flushAsync();
        if (!threadRowButton(alpha.preview)) {
          await act(async () => { (projectRow("alpha").querySelector(".project-row-name")!.closest("button") as HTMLButtonElement).click(); });
          await flushAsync();
        }
        await act(async () => { threadRowButton(alpha.preview)!.click(); });
        await flushAsync();
      }
      expect(mainComposerTextarea().value).toBe("Retained source draft");
      expect(activeThreadProbe()?.dataset.threadId).toBe(alpha.id);
      expect(mainComposerTextarea().disabled).toBe(false);
      return;
    }
    if (navigation === "thread" && !threadRowButton("session switch A")) {
      await act(async () => { (projectRow("alpha").querySelector(".project-row-name")!.closest("button") as HTMLButtonElement).click(); });
      await flushAsync();
    }
    await act(async () => {
      if (navigation === "thread") threadRowButton("session switch A")!.click();
      else projectRow("alpha").querySelector<HTMLButtonElement>(".project-row-new-thread")!.click();
    });
    await flushAsync();
    expect(context.project_id).toBe("alpha");
    await act(async () => { pending.resolve({ thread: beta }); });
    await flushAsync();
    expect(activeThreadProbe()?.dataset.threadId).toBe(navigation === "thread" ? alpha.id : undefined);
    expect(mainComposerTextarea().disabled).toBe(false);
  });

  it("keeps the outgoing cached conversation until the target resume resolves", async () => {
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
    expect(activeSessionTabLabel()).toContain("session switch B");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadBID);
    expect(container.querySelector(".view-switch-loading-conversation")).not.toBeNull();
    expect(mainComposerTextarea().disabled).toBe(true);

    delayedResumeA.resolve({ thread: threadA(2) });
    await flushAsync();

    expect(activeSessionTabLabel()).toContain("session switch A");
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(activeThreadProbe()?.dataset.turnCount).toBe("2");
  });

  it.each(["Enter", "click"])("blocks %s while a cached switch is restoring", async (action) => {
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
    expect(activeThreadProbe()?.dataset.threadId).toBe(threadAID);
    expect(mainComposerTextarea().disabled).toBe(true);
    await act(async () => {
      if (action === "Enter") {
        mainComposerTextarea().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      } else {
        mainComposerSendButton().click();
      }
    });
    expect(startTurn).not.toHaveBeenCalled();
    delayedResumeB.resolve({ thread: threadB() });
    await flushAsync();
    await act(async () => { setMainComposerPrompt("send after switching"); });
    await act(async () => { mainComposerSendButton().click(); });
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

  it.each([
    { key: "End", initial: "high", pending: "", responseBeforeKeyUp: false },
    { key: "Home", initial: "", pending: "high", responseBeforeKeyUp: true },
    { key: "End", initial: "high", pending: "", responseBeforeKeyUp: true },
  ])("preserves $key after reopening while an earlier effort response arrives before keyup=$responseBeforeKeyUp", async ({ key, initial, pending, responseBeforeKeyUp }) => {
    const { threadsByID } = installWuuApi();
    threadsByID.set(threadAID, { ...threadA(), model_variant: initial, model_effort: initial });
    const workspaceDefaults = initialized();
    workspaceDefaults.providers![0].models = [{ id: "model-a", supported_efforts: ["low", "medium", "high"] }];
    vi.mocked(window.wuu.initialize).mockResolvedValue(workspaceDefaults);
    const first = deferred<InitializeResult>();
    const update = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(workspaceDefaults);
    window.wuu.updateRuntimeSettings = update;
    await act(async () => { root = createRoot(container); root.render(<App />); });
    await flushAsync();
    const toggle = () => container.querySelector<HTMLButtonElement>(".codex-runtime-trigger")!.click();
    await act(async () => { toggle(); });
    let slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    const endpoint = (): string => key === "Home" ? slider.min : slider.max;
    expect(slider.value).toBe(endpoint());
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(slider, key === "Home" ? slider.max : slider.min);
      slider.dispatchEvent(new Event("input", { bubbles: true }));
      slider.dispatchEvent(new KeyboardEvent("keyup", { key: key === "Home" ? "End" : "Home", bubbles: true }));
    });
    expect(update.mock.calls.map(call => call[4])).toEqual([pending]);
    await act(async () => { toggle(); });
    await act(async () => { toggle(); });
    await flushAsync();
    slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(slider.value).toBe(endpoint());
    // Home/End at the displayed endpoint produces no native input/change event.
    await act(async () => {
      slider.focus();
      slider.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
    });
    if (responseBeforeKeyUp) {
      await act(async () => { first.resolve(workspaceDefaults); });
      await flushAsync();
      expect(slider.value).not.toBe(endpoint());
      expect(document.activeElement).toBe(slider);
    }
    await act(async () => { slider.dispatchEvent(new KeyboardEvent("keyup", { key, bubbles: true })); });
    if (!responseBeforeKeyUp) await act(async () => { first.resolve(workspaceDefaults); });
    await flushAsync();
    expect(update.mock.calls.map(call => call[4])).toEqual([pending, initial]);
    expect(slider.value).toBe(endpoint());
  });
});
