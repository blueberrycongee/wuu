/**
 * Regression test for two 对话 (scratch) sidebar bugs:
 *
 * Bug 1 (dead click): clicking a scratch conversation row called
 * selectWorkspaceThread(SCRATCH_PSEUDO_PROJECT_ID, threadID). Since the
 * scratch pseudo project never exists in state.projects, the lookup
 * silently failed and the click did nothing.
 *
 * Bug 2 (context not switched): even once resumed, the thread would open
 * under whatever activeContext was already active, because the app-server
 * resumes any persisted session regardless of workdir — the renderer has
 * to explicitly drive the context switch itself.
 *
 * This test boots the app already inside one scratch workspace (cwd A),
 * with a second scratch thread living at a different cwd (B) in the
 * 对话 list, and asserts that clicking B's row actually resumes it AND
 * moves activeContext to B's own cwd (selectNoProject called with B's cwd,
 * resumeThread called with B's id, and B's row becomes the active one).
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
  WorkspaceMonacoEditor: (): JSX.Element => (
    <div className="workspace-monaco-editor" data-testid="mock-monaco-editor" />
  ),
}));

import { createDefaultApplicationTestView } from "./test-support/DefaultApplication";
import { initialState } from "./AppState";
import { mockAnimationFrames } from "./AnimationFrameTestHarness";
import { getDesktopApplication } from "./plugins/ApplicationController";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];

const SCRATCH_CWD_A = "/tmp/wuu-scratch-select-test/a";
const SCRATCH_CWD_B = "/tmp/wuu-scratch-select-test/b";

function initialized(workspaceRoot: string): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspaceRoot,
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

function scratchThreadA(): Thread {
  return {
    id: "thread-scratch-a",
    preview: "scratch conversation A",
    model_provider: "fake",
    model: "fake-model",
    cwd: SCRATCH_CWD_A,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-02T00:00:00Z",
    turns: [],
  };
}

function scratchThreadB(): Thread {
  return {
    id: "thread-scratch-b",
    preview: "scratch conversation B",
    model_provider: "fake",
    model: "fake-model",
    cwd: SCRATCH_CWD_B,
    workspace_kind: "scratch",
    status: "idle",
    created_at: "2025-12-31T00:00:00Z",
    updated_at: "2025-12-31T00:00:00Z",
    turns: [],
  };
}

function pinnedScratchThreadB(): Thread {
  return { ...scratchThreadB(), id: "thread-pinned-b", pinned: true, preview: "pinned scratch B" };
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

function installWuuApi(
  threads: Thread[] = [scratchThreadA(), scratchThreadB()],
): {
  selectNoProject: ReturnType<typeof vi.fn>;
  resumeThread: ReturnType<typeof vi.fn>;
} {
  const threadsByID = new Map<string, Thread>(
    threads.map((thread) => [thread.id, thread]),
  );

  const selectNoProject = vi
    .fn()
    .mockImplementation((_fresh?: boolean, cwd?: string) =>
      Promise.resolve({
        projects: [],
        active_context: { kind: "no_project", cwd: cwd ?? SCRATCH_CWD_A },
      }),
    );
  const resumeThread = vi.fn().mockImplementation((threadID: string) =>
    Promise.resolve({ thread: threadsByID.get(threadID) }),
  );

  const api = {
    listProjects: vi.fn().mockResolvedValue({
      projects: [],
      active_context: { kind: "no_project", cwd: SCRATCH_CWD_A },
    }),
    selectNoProject,
    initialize: vi
      .fn()
      .mockImplementation(() => Promise.resolve(initialized(SCRATCH_CWD_A))),
    listThreads: vi.fn().mockResolvedValue({ threads }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread,
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
  return { selectNoProject, resumeThread };
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

describe("对话 (scratch) sidebar thread selection", () => {
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
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
    vi.clearAllTimers();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("finishes a pending historical message jump while a replacement shell initializes", async () => {
    vi.useFakeTimers();
    const frames = mockAnimationFrames();
    const matchMedia = vi.mocked(window.matchMedia).getMockImplementation()!;
    vi.mocked(window.matchMedia).mockImplementation(query => ({
      ...matchMedia(query), matches: query.includes("prefers-reduced-motion"),
    }));
    const thread: Thread = { ...scratchThreadB(), turns: Array.from({ length: 45 }, (_, index) => ({
      id: `turn-${index + 1}`, status: "completed", items_view: "full", items: [
        { id: `answer-${index + 1}`, type: "agent_message", seq: index + 1,
          text: index === 4 ? "historical needle" : `answer ${index + 1}` },
      ],
    })) };
    installWuuApi([thread]);
    // jsdom has no Range geometry; keep the real match and reveal behavior.
    let viewport: HTMLElement | null = null;
    const matchBounds = () => new DOMRect(0, 500 - (viewport?.scrollTop ?? 0), 10, 10);
    const createRange = document.createRange.bind(document);
    vi.spyOn(document, "createRange").mockImplementation(() => {
      const range = createRange();
      range.getBoundingClientRect = matchBounds;
      return range;
    });
    const application = getDesktopApplication();
    // Workspace activation has completed. The replacement shell must consume
    // its pending message address without replaying it from an init snapshot.
    application.updateState({ ...initialState, initialized: initialized(SCRATCH_CWD_B),
      activeContext: { kind: "no_project", cwd: SCRATCH_CWD_B }, threads: [thread], thread,
      conversationSearchTarget: { id: "pending-search", ready: true, query: "historical needle",
        result: { thread, message_seq: 5, snippet: "historical needle" } },
    });
    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView(application));
    });
    viewport = container.querySelector<HTMLElement>(".scroll-region")!;
    Object.defineProperties(viewport, {
      clientHeight: { configurable: true, value: 600 },
      scrollHeight: { configurable: true, value: 6000 },
    });
    viewport.getBoundingClientRect = () => new DOMRect(0, 0, 800, 600);
    viewport.scrollTop = 5400;
    await frames.flush();
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });

    const match = container.querySelector("#message-turn-5-answer-5");
    expect(match?.textContent).toContain("historical needle");
    expect(match?.classList.contains("user-message-jump-flash")).toBe(true);
    expect(matchBounds().top).toBeGreaterThanOrEqual(0);
    expect(matchBounds().bottom).toBeLessThanOrEqual(viewport.clientHeight);
    expect(application.getState().conversationSearchTarget).toBeUndefined();
  });

  it("resumes a scratch thread from another scratch cwd and switches activeContext to it", async () => {
    const { selectNoProject, resumeThread } = installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    // Boot auto-resumes the most recently updated thread (A, cwd A) —
    // confirms the fixture loaded before we exercise the click path.
    expect(resumeThread).toHaveBeenCalledWith("thread-scratch-a");
    const rowA = threadRowButton("scratch conversation A");
    expect(rowA).toBeDefined();
    expect(rowA?.closest(".thread-row")?.classList.contains("active")).toBe(
      true,
    );

    const rowB = threadRowButton("scratch conversation B");
    expect(rowB).toBeDefined();
    expect(rowB?.closest(".thread-row")?.classList.contains("active")).toBe(
      false,
    );

    selectNoProject.mockClear();
    resumeThread.mockClear();

    await act(async () => {
      rowB?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    // Bug 1: this used to be a dead click (selectWorkspaceThread bailed out
    // because SCRATCH_PSEUDO_PROJECT_ID is not in state.projects).
    expect(resumeThread).toHaveBeenCalledWith("thread-scratch-b");
    // Bug 2: the context switch must actually happen — activeContext has
    // to move to thread B's own cwd, not stay on A's.
    expect(selectNoProject).toHaveBeenCalledWith(false, SCRATCH_CWD_B);

    const rowBAfter = threadRowButton("scratch conversation B");
    expect(
      rowBAfter?.closest(".thread-row")?.classList.contains("active"),
    ).toBe(true);
    const rowAAfter = threadRowButton("scratch conversation A");
    expect(
      rowAAfter?.closest(".thread-row")?.classList.contains("active"),
    ).toBe(false);
  });

  it("resumes a pinned (置顶) thread from a different cwd and switches activeContext to it (activateThread fallback)", async () => {
    // Pinned threads never show up in any project's (or the scratch pseudo
    // project's) own thread list — they move to 置顶 instead. So clicking
    // one always goes through activateThread's "thread not found in any
    // real project's loaded list" fallback, not selectWorkspaceThread.
    const threadA = scratchThreadA();
    const pinnedB = pinnedScratchThreadB();
    const { selectNoProject, resumeThread } = installWuuApi([
      threadA,
      pinnedB,
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(await createDefaultApplicationTestView());
    });
    await flushAsync();

    // Boot auto-resumes the only unpinned thread (A, cwd A).
    expect(resumeThread).toHaveBeenCalledWith(threadA.id);

    const pinnedSection = container.querySelector(
      'section[aria-label="置顶"]',
    );
    expect(pinnedSection).not.toBeNull();
    const pinnedRow = Array.from(
      pinnedSection?.querySelectorAll<HTMLButtonElement>(
        ".thread-row-main",
      ) ?? [],
    ).find((button) => button.textContent?.includes("pinned scratch B"));
    expect(pinnedRow).toBeDefined();

    selectNoProject.mockClear();
    resumeThread.mockClear();

    await act(async () => {
      pinnedRow?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await flushAsync();

    // Bug 2: activateThread's fallback used to call selectThread directly,
    // resuming the thread WITHOUT switching activeContext to its own cwd.
    expect(resumeThread).toHaveBeenCalledWith(pinnedB.id);
    expect(selectNoProject).toHaveBeenCalledWith(false, SCRATCH_CWD_B);

    const pinnedRowAfter = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".thread-row-main"),
    ).find((button) => button.textContent?.includes("pinned scratch B"));
    expect(
      pinnedRowAfter?.closest(".thread-row")?.classList.contains("active"),
    ).toBe(true);
  });
});
