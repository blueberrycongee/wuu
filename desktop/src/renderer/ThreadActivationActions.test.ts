import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  Agent,
  DesktopProject,
  RuntimeContext,
  Thread,
} from "../shared/protocol";
import {
  emptyComposerDraft,
  initialState,
  threadSessionTabID,
  type AppState,
  type ComposerDraftState,
} from "./AppState";
import { createThreadActivationActions } from "./ThreadActivationActions";
import type { PendingViewSwitch } from "./ViewSwitchState";

const originalWuu = (window as unknown as { wuu?: unknown }).wuu;

function restoreWuu(): void {
  if (originalWuu === undefined) {
    delete (window as unknown as { wuu?: unknown }).wuu;
    return;
  }
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: originalWuu,
  });
}

afterEach(() => {
  restoreWuu();
});

function project(id: string, path = `/tmp/${id}`): DesktopProject {
  return {
    id,
    name: id,
    path,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function projectContext(id = "project-1"): RuntimeContext {
  return { kind: "project", project_id: id, cwd: `/tmp/${id}` };
}

function thread(id = "thread-1", cwd = "/tmp/project-1"): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "fake",
    model: "fake-model",
    cwd,
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

function installWuuApi(resumedThread: Thread): {
  resumeThread: ReturnType<typeof vi.fn>;
} {
  const resumeThread = vi.fn().mockResolvedValue({ thread: resumedThread });
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: { resumeThread },
  });
  return { resumeThread };
}

function buildActions({
  initial,
  draft = emptyComposerDraft(),
  activeThreadID,
  pendingViewSwitch,
  sidebarThreads = [],
  sidebarProjectThreadsByProjectID = {},
  loadedState,
}: {
  initial: AppState;
  draft?: ComposerDraftState;
  activeThreadID?: string;
  pendingViewSwitch?: PendingViewSwitch;
  sidebarThreads?: Thread[];
  sidebarProjectThreadsByProjectID?: Record<string, Thread[] | undefined>;
  loadedState?: Partial<AppState>;
}) {
  let appState = initial;
  let currentDraft = draft;
  const beginViewSwitch = vi.fn(() => 1);
  const beginInstantThreadSwitch = vi.fn(() => 2);
  const finishViewSwitch = vi.fn(() => true);
  const cancelViewSwitch = vi.fn();
  const restorePrimaryComposerDraft = vi.fn((nextDraft: ComposerDraftState) => {
    currentDraft = nextDraft;
  });
  const resetSplitComposerDrafts = vi.fn();
  const selectRuntimeContext = vi.fn().mockResolvedValue({});
  const loadRuntime = vi.fn().mockResolvedValue(loadedState ?? {});

  const actions = createThreadActivationActions({
    getAppState: () => appState,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    getActiveThreadID: () => activeThreadID,
    getPendingViewSwitch: () => pendingViewSwitch,
    getPrimaryComposerDraft: () => currentDraft,
    restorePrimaryComposerDraft,
    resetSplitComposerDrafts,
    getSidebarThreads: () => sidebarThreads,
    getSidebarProjectThreadsByProjectID: () => sidebarProjectThreadsByProjectID,
    
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest: vi.fn(() => true),
    loadRuntime,
    selectRuntimeContext,
  });

  return {
    actions,
    getAppState: () => appState,
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    restorePrimaryComposerDraft,
    resetSplitComposerDrafts,
    selectRuntimeContext,
    loadRuntime,
  };
}

describe("createThreadActivationActions", () => {
  it("resumes a thread into the active context", async () => {
    const context = projectContext();
    const resumed = thread("thread-1");
    const api = installWuuApi(resumed);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeProjectId: "project-1",
        projects: [project("project-1")],
        status: "ready",
      },
    });

    await harness.actions.selectThread("thread-1");

    expect(api.resumeThread).toHaveBeenCalledWith("thread-1");
    expect(harness.beginViewSwitch).toHaveBeenCalledWith("thread", "thread-1");
    expect(harness.finishViewSwitch).toHaveBeenCalledWith(1);
    expect(harness.restorePrimaryComposerDraft).toHaveBeenCalled();
    expect(harness.resetSplitComposerDrafts).toHaveBeenCalled();
    expect(harness.getAppState().thread?.id).toBe("thread-1");
    expect(harness.getAppState().activeSessionTabID).toBe(
      threadSessionTabID("thread-1"),
    );
  });

  it("activates a thread from another project by switching runtime first", async () => {
    const projectTwo = project("project-2");
    const targetContext = projectContext("project-2");
    const targetThread = thread("thread-2", projectTwo.path);
    installWuuApi(targetThread);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext("project-1"),
        activeProjectId: "project-1",
        projects: [project("project-1"), projectTwo],
        status: "ready",
      },
      sidebarProjectThreadsByProjectID: { "project-2": [targetThread] },
      loadedState: {
        activeContext: targetContext,
        activeProjectId: "project-2",
        projects: [project("project-1"), projectTwo],
        threads: [targetThread],
      },
    });

    await harness.actions.activateThread("thread-2");

    expect(harness.selectRuntimeContext).toHaveBeenCalledWith(targetContext);
    expect(harness.loadRuntime).toHaveBeenCalledWith(
      {},
      { resumeLatestThread: false },
    );
    expect(harness.getAppState().activeProjectId).toBe("project-2");
    expect(harness.getAppState().thread?.id).toBe("thread-2");
  });

  it("shows a loaded thread from another project before runtime selection resolves", async () => {
    const projectTwo = project("project-2");
    const targetContext = projectContext("project-2");
    const targetThread = {
      ...thread("thread-2", projectTwo.path),
      turns: [{ id: "turn-1", status: "completed", items_view: "full", items: [] }],
    } as Thread;
    installWuuApi(targetThread);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext("project-1"),
        activeProjectId: "project-1",
        projects: [project("project-1"), projectTwo],
        status: "ready",
      },
      sidebarThreads: [targetThread],
      sidebarProjectThreadsByProjectID: { "project-2": [targetThread] },
      loadedState: {
        activeContext: targetContext,
        activeProjectId: "project-2",
        projects: [project("project-1"), projectTwo],
        threads: [targetThread],
      },
    });
    const runtimeSelection = deferred<Record<string, never>>();
    harness.selectRuntimeContext.mockReturnValue(runtimeSelection.promise);

    const activation = harness.actions.activateThread(targetThread.id);

    expect(harness.beginInstantThreadSwitch).toHaveBeenCalledWith(targetThread.id);
    expect(harness.getAppState().activeContext).toEqual(targetContext);
    expect(harness.getAppState().thread?.id).toBe(targetThread.id);
    runtimeSelection.resolve({});
    await activation;
  });

  it("ignores duplicate selection while the same thread switch is pending", async () => {
    const context = projectContext();
    const resumed = thread("thread-1");
    const api = installWuuApi(resumed);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeProjectId: "project-1",
        projects: [project("project-1")],
        status: "ready",
      },
      pendingViewSwitch: {
        kind: "thread",
        targetID: "thread-1",
        visible: true,
      },
    });

    await harness.actions.selectThread("thread-1");

    expect(api.resumeThread).not.toHaveBeenCalled();
    expect(harness.beginViewSwitch).not.toHaveBeenCalled();
  });

  it("keeps a cached resume pending when its selected tab is clicked after loading appears", async () => {
    const cached = {
      ...thread(),
      turns: [{ id: "turn-1", status: "completed", items_view: "full", items: [] }],
    } as Thread;
    const api = installWuuApi(cached);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        thread: cached,
        threads: [cached],
        status: "ready",
      },
      activeThreadID: cached.id,
      pendingViewSwitch: { kind: "thread", targetID: cached.id, visible: true },
    });
    await harness.actions.selectThread(cached.id);
    expect(harness.cancelViewSwitch).not.toHaveBeenCalled();
    expect(api.resumeThread).not.toHaveBeenCalled();
  });

  it("selects a child agent through the same resume path", async () => {
    const context = projectContext();
    const agent = { id: "agent-1", status: "idle" } as Agent;
    const resumed = thread("agent-1");
    const api = installWuuApi(resumed);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeProjectId: "project-1",
        projects: [project("project-1")],
        status: "ready",
      },
    });

    await harness.actions.selectChildAgent(agent);

    expect(api.resumeThread).toHaveBeenCalledWith("agent-1");
    expect(harness.getAppState().thread?.id).toBe("agent-1");
  });
});
