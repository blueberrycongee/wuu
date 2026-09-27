import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  Agent,
  DesktopProject,
  InitializeResult,
  RuntimeContext,
  Thread,
} from "../shared/protocol";
import {
  createThreadSessionTab,
  emptyComposerDraft,
  initialState,
  threadSessionTabID,
  type AppState,
  type ComposerDraftState,
} from "./AppState";
import { createThreadActivationActions } from "./ThreadActivationActions";
import { loadRuntimeConfiguration, loadRuntimeThreadList } from "./RuntimeLoadState";
import { showErrorToast } from "./Toast";
import type { PendingViewSwitch } from "./ViewSwitchState";

vi.mock("./Toast", () => ({ showErrorToast: vi.fn() }));

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
  vi.clearAllMocks();
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
  reject: (error: Error) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
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
  sidebarWorkspaceThreadsByWorkspaceID = {},
  loadedState,
}: {
  initial: AppState;
  draft?: ComposerDraftState;
  activeThreadID?: string;
  pendingViewSwitch?: PendingViewSwitch;
  sidebarThreads?: Thread[];
  sidebarWorkspaceThreadsByWorkspaceID?: Record<string, Thread[] | undefined>;
  loadedState?: Partial<AppState>;
}) {
  let appState = initial;
  let currentDraft = draft;
  let requestID = 0;
  const beginViewSwitch = vi.fn(() => ++requestID);
  const beginInstantThreadSwitch = vi.fn(() => ++requestID);
  const finishViewSwitch = vi.fn((id: number) => id === requestID);
  const cancelViewSwitch = vi.fn(() => { requestID++; });
  const restorePrimaryComposerDraft = vi.fn((nextDraft: ComposerDraftState) => {
    currentDraft = nextDraft;
  });
  const resetSplitComposerDrafts = vi.fn();
  const selectRuntimeContext = vi.fn().mockImplementation(async (context: RuntimeContext) => ({
    projects: appState.projects,
    active_context: context,
  }));
  const loadConfiguration = vi.fn().mockImplementation(async (projects) => loadedState ?? {
    activeContext: projects.active_context,
    activeProjectId: projects.active_context?.project_id,
    projects: projects.projects,
  });
  const loadCatalog = vi.fn().mockResolvedValue([]);

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
    getSidebarWorkspaceThreadsByWorkspaceID: () => sidebarWorkspaceThreadsByWorkspaceID,
    
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest: (id) => id === requestID,
    loadRuntimeConfiguration: loadConfiguration,
    loadRuntimeThreadList: loadCatalog,
    selectRuntimeContext,
  });

  return {
    actions,
    getAppState: () => appState,
    setAppState: (next: AppState) => { appState = next; },
    getDraft: () => currentDraft,
    setDraft: (next: ComposerDraftState) => { currentDraft = next; },
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    restorePrimaryComposerDraft,
    resetSplitComposerDrafts,
    selectRuntimeContext,
    loadRuntimeConfiguration: loadConfiguration,
    loadRuntimeThreadList: loadCatalog,
  };
}

describe("createThreadActivationActions", () => {
  it.each([false, true])(
    "reuses sidebar history instead of a runtime summary (cross-project: %s)",
    async (crossWorkspace) => {
      const cached = {
        ...thread("target", "/tmp/project-2"),
        turns: [{ id: "cached-turn", status: "completed", items_view: "full", items: [] }],
      } as Thread;
      const summary = { ...cached, turns: [] };
      const resume = deferred<{ thread: Thread }>();
      const api = installWuuApi(cached);
      api.resumeThread.mockReturnValue(resume.promise);
      const harness = buildActions({
        initial: {
          ...initialState,
          activeContext: projectContext(crossWorkspace ? "project-1" : "project-2"),
          activeProjectId: crossWorkspace ? "project-1" : "project-2",
          projects: [project("project-1"), project("project-2")],
          threads: [summary],
        },
        sidebarThreads: [summary],
        sidebarWorkspaceThreadsByWorkspaceID: { "project-2": [cached] },
      });

      const activation = harness.actions.activateThread(cached.id);

      expect(harness.getAppState().thread?.turns).toEqual(cached.turns);
      expect(harness.getAppState().activeContext).toEqual(projectContext("project-2"));
      expect(harness.beginViewSwitch).not.toHaveBeenCalled();
      expect(harness.finishViewSwitch).not.toHaveBeenCalled();
      resume.resolve({ thread: cached });
      await activation;
    },
  );

  it("keeps live pane history ahead of an older sidebar snapshot", async () => {
    const cached = {
      ...thread(),
      turns: [{ id: "cached-turn", status: "completed", items_view: "full", items: [] }],
    } as Thread;
    const live = { ...cached, turns: [...cached.turns, { ...cached.turns[0], id: "new-turn" }] };
    const resume = deferred<{ thread: Thread }>();
    installWuuApi(live).resumeThread.mockReturnValue(resume.promise);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        projects: [project("project-1")],
        secondaryThread: live,
        threads: [{ ...cached, turns: [] }],
      },
      sidebarThreads: [cached],
    });

    await harness.actions.selectThread(live.id);

    expect(harness.getAppState().thread?.turns).toEqual(live.turns);
    resume.resolve({ thread: live });
  });

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
    const workspaceTwo = project("project-2");
    const targetContext = projectContext("project-2");
    const targetThread = thread("thread-2", workspaceTwo.path);
    installWuuApi(targetThread);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext("project-1"),
        activeProjectId: "project-1",
        projects: [project("project-1"), workspaceTwo],
        status: "ready",
      },
      sidebarWorkspaceThreadsByWorkspaceID: { "project-2": [targetThread] },
      loadedState: {
        activeContext: targetContext,
        activeProjectId: "project-2",
        projects: [project("project-1"), workspaceTwo],
        threads: [targetThread],
      },
    });

    await harness.actions.activateThread("thread-2");

    expect(harness.selectRuntimeContext).toHaveBeenCalledWith(targetContext);
    expect(harness.loadRuntimeConfiguration).toHaveBeenCalledWith(
      expect.objectContaining({ active_context: targetContext }),
    );
    expect(harness.getAppState().activeProjectId).toBe("project-2");
    expect(harness.getAppState().thread?.id).toBe("thread-2");
  });

  it("shows a loaded thread from another project before runtime selection resolves", async () => {
    const workspaceTwo = project("project-2");
    const targetContext = projectContext("project-2");
    const targetThread = {
      ...thread("thread-2", workspaceTwo.path),
      turns: [{ id: "turn-1", status: "completed", items_view: "full", items: [] }],
    } as Thread;
    installWuuApi(targetThread);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext("project-1"),
        activeProjectId: "project-1",
        projects: [project("project-1"), workspaceTwo],
        status: "ready",
      },
      sidebarThreads: [targetThread],
      sidebarWorkspaceThreadsByWorkspaceID: { "project-2": [targetThread] },
      loadedState: {
        activeContext: targetContext,
        activeProjectId: "project-2",
        projects: [project("project-1"), workspaceTwo],
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

  it("opens a cold conversation after initialization without waiting for catalogs, then preserves live edits", async () => {
    const target = thread("target", "/tmp/project-2");
    const initialized = deferred<InitializeResult>();
    const listed = deferred<{ threads: Thread[] }>();
    const archived = deferred<{ threads: Thread[] }>();
    const api = installWuuApi(target);
    const initialize = vi.fn(() => initialized.promise);
    const listThreads = vi.fn(() => listed.promise);
    Object.assign(window.wuu, {
      initialize,
      listThreads,
      listArchivedThreads: vi.fn(() => archived.promise),
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
      },
    });
    harness.loadRuntimeConfiguration.mockImplementation(loadRuntimeConfiguration);
    harness.loadRuntimeThreadList.mockImplementation(loadRuntimeThreadList);

    const activation = harness.actions.selectWorkspaceThread("project-2", target.id);
    await Promise.resolve(); // Runtime selection has resolved; initialization has not.
    expect(api.resumeThread).toHaveBeenCalledWith(target.id);
    expect(harness.getAppState().activeProjectId).toBe("project-1");
    expect(harness.finishViewSwitch).not.toHaveBeenCalled();
    initialized.resolve({ status: "ready", workspace_root: target.cwd } as InitializeResult);
    await activation;

    expect(harness.getAppState().thread?.id).toBe(target.id);
    expect(harness.getAppState().initialized?.workspace_root).toBe(target.cwd);
    expect(harness.finishViewSwitch).toHaveReturnedWith(true);
    expect(listThreads).toHaveBeenCalledWith(target.cwd);
    const live = {
      ...target,
      status: "in_progress",
      turns: [{ id: "new-turn", status: "in_progress", items_view: "full", items: [] }],
    } as Thread;
    const editedDraft = { ...emptyComposerDraft(), prompt: "typed after activation" };
    harness.setDraft(editedDraft);
    harness.setAppState({ ...harness.getAppState(), thread: live, threads: [live], running: true });
    const saved = { ...thread("archived", "/tmp/project-3"), archived: true };
    listed.resolve({ threads: [target, thread("other", target.cwd)] });
    archived.resolve({ threads: [saved] });
    await harness.loadRuntimeThreadList.mock.results[0].value;

    expect(harness.getAppState().threads.map((item) => item.id)).toContain(saved.id);
    expect(harness.getAppState().threads.map((item) => item.id)).toContain("other");
    expect(harness.getAppState().thread).toBe(live);
    expect(harness.getAppState().threads.find((item) => item.id === live.id)).toBe(live);
    expect(harness.getAppState().running).toBe(true);
    expect(harness.getDraft()).toEqual(editedDraft);
  });

  it("still hydrates catalogs after a same-context selection without undoing local catalog changes", async () => {
    const target = thread("target", "/tmp/project-2");
    const next = thread("next", target.cwd);
    const removed = thread("removed", target.cwd);
    const archived = thread("archived", target.cwd);
    const api = installWuuApi(target);
    const pending = deferred<Thread[]>();
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
        threads: [removed, archived],
      },
    });
    harness.loadRuntimeThreadList.mockReturnValue(pending.promise);
    await harness.actions.selectWorkspaceThread("project-2", target.id);
    api.resumeThread.mockResolvedValue({ thread: next });
    await harness.actions.selectThread(next.id);
    const created = thread("created", target.cwd);
    harness.setAppState({
      ...harness.getAppState(),
      threads: [target, next, { ...archived, archived: true }, created],
    });
    const listed = thread("listed", target.cwd);
    pending.resolve([target, removed, archived, listed]);
    await pending.promise;

    expect(harness.getAppState().thread?.id).toBe(next.id);
    expect(harness.getAppState().threads.map((item) => item.id).sort())
      .toEqual([target.id, next.id, archived.id, created.id, listed.id].sort());
    expect(harness.getAppState().threads.find((item) => item.id === archived.id)?.archived).toBe(true);
  });

  it("ignores a catalog from an earlier visit to the same workspace", async () => {
    const target = thread("target", "/tmp/project-2");
    const api = installWuuApi(target);
    const pending = deferred<Thread[]>();
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
      },
    });
    harness.loadRuntimeThreadList.mockReturnValueOnce(pending.promise);
    await harness.actions.selectWorkspaceThread("project-2", target.id);
    api.resumeThread.mockResolvedValueOnce({ thread: thread("source") });
    await harness.actions.selectWorkspaceThread("project-1", "source");
    await harness.actions.selectWorkspaceThread("project-2", target.id);
    const current = harness.getAppState();
    pending.resolve([thread("obsolete", target.cwd)]);
    await pending.promise;

    expect(harness.getAppState()).toBe(current);
  });

  it("does not overwrite either draft when a cached cross-project resume finishes", async () => {
    const source = thread("source");
    const target = {
      ...thread("target", "/tmp/project-2"),
      turns: [{ id: "turn", status: "completed", items_view: "full", items: [] }],
    } as Thread;
    const outgoingDraft = { ...emptyComposerDraft(), prompt: "source draft" };
    const targetDraft = { ...emptyComposerDraft(), prompt: "saved target draft" };
    const resume = deferred<{ thread: Thread }>();
    installWuuApi(target).resumeThread.mockReturnValue(resume.promise);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
        thread: source,
        sessionTabs: [
          createThreadSessionTab(source, projectContext()),
          createThreadSessionTab(target, projectContext("project-2"), targetDraft),
        ],
        activeSessionTabID: threadSessionTabID(source.id),
      },
      draft: outgoingDraft,
      sidebarThreads: [target],
    });
    const activation = harness.actions.selectWorkspaceThread("project-2", target.id);
    expect(harness.getDraft()).toEqual(targetDraft);
    const editedDraft = { ...targetDraft, prompt: "edited while waiting" };
    harness.setDraft(editedDraft);
    resume.resolve({ thread: target });
    await activation;

    expect(harness.getDraft()).toEqual(editedDraft);
    expect(harness.getAppState().sessionTabs.find((tab) => tab.id === threadSessionTabID(source.id)))
      .toMatchObject({ prompt: outgoingDraft.prompt });
    expect(harness.getAppState().sessionTabs.find((tab) => tab.id === threadSessionTabID(target.id)))
      .toMatchObject({ prompt: editedDraft.prompt });
  });

  it.each(["selection", "resume", "catalog"] as const)(
    "ignores an obsolete cross-project %s after a newer activation",
    async (phase) => {
      const older = thread("older", "/tmp/project-2");
      const newer = thread("newer", "/tmp/project-3");
      const pending = deferred<unknown>();
      const api = installWuuApi(newer);
      const harness = buildActions({
        initial: {
          ...initialState,
          activeContext: projectContext(),
          activeProjectId: "project-1",
          projects: [project("project-1"), project("project-2"), project("project-3")],
        },
      });
      if (phase === "selection") harness.selectRuntimeContext.mockReturnValueOnce(pending.promise);
      if (phase === "resume") api.resumeThread.mockReturnValueOnce(pending.promise);
      if (phase === "catalog") {
        api.resumeThread.mockResolvedValueOnce({ thread: older });
        harness.loadRuntimeThreadList.mockReturnValueOnce(pending.promise);
      }
      const olderActivation = harness.actions.selectWorkspaceThread("project-2", older.id);
      if (phase === "catalog") await olderActivation;
      else await Promise.resolve();
      await harness.actions.selectWorkspaceThread("project-3", newer.id);
      const newerState = harness.getAppState();

      pending.resolve(phase === "selection"
        ? { projects: newerState.projects, active_context: projectContext("project-2") }
        : phase === "resume" ? { thread: older } : [older]);
      await pending.promise;
      await olderActivation;

      expect(harness.getAppState()).toBe(newerState);
      if (phase === "selection") expect(api.resumeThread).not.toHaveBeenCalledWith(older.id);
    },
  );

  it("keeps the resumed conversation usable when a background catalog fails", async () => {
    const target = thread("target", "/tmp/project-2");
    installWuuApi(target);
    const pending = deferred<Thread[]>();
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
      },
    });
    harness.loadRuntimeThreadList.mockReturnValue(pending.promise);
    await harness.actions.selectWorkspaceThread("project-2", target.id);
    const activated = harness.getAppState();
    pending.reject(new Error("catalog unavailable"));
    await pending.promise.catch(() => {});

    expect(harness.getAppState()).toBe(activated);
    expect(activated.thread?.id).toBe(target.id);
    expect(activated.status).toBe("ready");
    expect(showErrorToast).toHaveBeenCalledWith("catalog unavailable");
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
