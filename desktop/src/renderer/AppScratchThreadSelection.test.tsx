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

import { App } from "./App";

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
  });

  it("resumes a scratch thread from another scratch cwd and switches activeContext to it", async () => {
    const { selectNoProject, resumeThread } = installWuuApi();

    await act(async () => {
      root = createRoot(container);
      root.render(<App />);
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
      root.render(<App />);
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
