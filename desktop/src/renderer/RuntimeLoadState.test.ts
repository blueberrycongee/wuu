import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  InitializeResult,
  ProjectListResult,
  RuntimeContext,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";
import {
  emptyRuntimeState,
  loadRuntime,
  loadRuntimeRestore,
  loadThreadListRefresh,
  applyRuntimeRestore,
} from "./RuntimeLoadState";

import { initialState, createThreadSessionTab, reconcileListedThreadState } from "./AppState";
import { writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { streamTextStore, streamTextKey } from "./StreamText";

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  delete (window as unknown as { wuu?: unknown }).wuu;
  window.localStorage.clear();
  vi.restoreAllMocks();
});

function installWuuStub(overrides: Partial<WuuDesktopApi>): void {
  (window as unknown as { wuu: WuuDesktopApi }).wuu = {
    ...overrides,
  } as WuuDesktopApi;
}

function thread(id: string, overrides: Partial<Thread> = {}): Thread {
  return {
    id,
    title: id,
    preview: id,
    cwd: "/tmp/wuu",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
    ...overrides,
  } as Thread;
}

function workspaceList(activeContext?: RuntimeContext): ProjectListResult {
  return {
    projects: [],
    active_context: activeContext,
  } as ProjectListResult;
}

describe("runtime load helpers", () => {
  it("starts runtime initialization and conversation loading together", async () => {
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project-1",
    };
    let resolveInitialize: ((value: InitializeResult) => void) | undefined;
    const initialize = vi.fn(
      () =>
        new Promise<InitializeResult>((resolve) => {
          resolveInitialize = resolve;
        }),
    );
    const listThreads = vi.fn().mockResolvedValue({ threads: [] });
    const listArchivedThreads = vi.fn().mockResolvedValue({ threads: [] });
    installWuuStub({ initialize, listThreads, listArchivedThreads });

    const loading = loadRuntime(workspaceList(activeContext));

    expect(initialize).toHaveBeenCalledOnce();
    expect(listThreads).toHaveBeenCalledOnce();
    expect(listArchivedThreads).toHaveBeenCalledOnce();
    resolveInitialize?.({
      status: "ready",
      protocol_version: "wuu-app-server/v0.1",
      provider: "fake",
      model: "gpt-test",
      workspace_root: activeContext.cwd,
    });
    await loading;
  });

  it("seeds a new conversation from the last composer provider, model, and effort", async () => {
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/project-1",
    };
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    const initialize = vi.fn().mockResolvedValue({
      status: "ready",
      protocol_version: "wuu-app-server/v0.1",
      provider: "work",
      model: "claude-sonnet",
      variant: "medium",
      effort: "medium",
      workspace_root: activeContext.cwd,
      providers: [
        {
          name: "tokenhub",
          type: "openai-compatible",
          model: "gpt-5.6-sol",
          models: [
            {
              id: "gpt-5.6-sol",
              supported_efforts: ["low", "medium", "high"],
              default_effort: "medium",
            },
          ],
        },
      ],
    });
    installWuuStub({
      initialize,
      listThreads: vi.fn().mockResolvedValue({ threads: [] }),
      listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    });

    const state = await loadRuntime(workspaceList(activeContext));

    expect(state.initialized).toMatchObject({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      variant: "high",
      effort: "high",
    });
  });

  it("returns a no-runtime state without initializing when no active context exists", async () => {
    const initialize = vi.fn();
    installWuuStub({ initialize });

    const state = await loadRuntime(workspaceList(undefined));

    expect(state).toEqual(emptyRuntimeState(workspaceList(undefined)));
    expect(initialize).not.toHaveBeenCalled();
  });

  it("keeps an unavailable active project selected without starting its runtime", async () => {
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: "project-1",
      cwd: "/tmp/offline-project",
    };
    const message =
      "工作区目录当前不可用：/tmp/offline-project。请恢复该目录，或从工作区菜单选择“重新定位…”。";
    const workspaceState: ProjectListResult = {
      projects: [
        {
          id: "project-1",
          name: "offline-project",
          path: activeContext.cwd,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          missing: true,
        },
      ],
      active_context: activeContext,
      active_project_id: "project-1",
      runtime_issue: {
        code: "active_project_unavailable",
        message,
        project_id: "project-1",
        cwd: activeContext.cwd,
      },
    };
    const initialize = vi.fn();
    const listThreads = vi.fn();
    const listArchivedThreads = vi.fn();
    installWuuStub({ initialize, listThreads, listArchivedThreads });

    const state = await loadRuntime(workspaceState);

    expect(state.projects).toEqual(workspaceState.projects);
    expect(state.activeContext).toEqual(activeContext);
    expect(state.activeProjectId).toBe("project-1");
    expect(state.initialized).toBeUndefined();
    expect(state.status).toBe(message);
    expect(state.threads).toEqual([]);
    expect(initialize).not.toHaveBeenCalled();
    expect(listThreads).not.toHaveBeenCalled();
    expect(listArchivedThreads).not.toHaveBeenCalled();
  });

  it("resumes the latest non-pinned thread when loading an active runtime", async () => {
    const activeContext: RuntimeContext = {
      kind: "no_project",
      cwd: "/tmp/wuu",
    };
    const pinned = thread("pinned", {
      pinned: true,
      updated_at: "2026-04-01T00:00:00Z",
    });
    const latest = thread("latest", {
      updated_at: "2026-03-01T00:00:00Z",
    });
    const resumed = thread("latest", {
      status: "in_progress",
      turns: [
        {
          id: "turn-1",
          status: "in_progress",
          items_view: "full",
          items: [],
        },
      ],
    } as Partial<Thread>);
    installWuuStub({
      initialize: vi.fn().mockResolvedValue({ model: "gpt-test" }),
      listThreads: vi.fn().mockResolvedValue({ threads: [pinned, latest] }),
      listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
      resumeThread: vi.fn().mockResolvedValue({ thread: resumed }),
    });

    const state = await loadRuntime(workspaceList(activeContext));

    expect(state.thread?.id).toBe("latest");
    expect(state.initialized?.model).toBe("gpt-test");
    expect(state.activeContext).toEqual(activeContext);
    expect(state.running).toBe(true);
    expect(state.threads?.some((item) => item.id === "latest")).toBe(true);
  });

  it("skips archived conversations when resuming, even if one is the most recent", async () => {
    const activeContext: RuntimeContext = {
      kind: "no_project",
      cwd: "/tmp/wuu",
    };
    const live = thread("live", { updated_at: "2026-03-01T00:00:00Z" });
    const archivedLatest = thread("archived-latest", {
      archived: true,
      updated_at: "2026-03-05T00:00:00Z",
    });
    const resumeThread = vi.fn().mockResolvedValue({ thread: live });
    installWuuStub({
      initialize: vi.fn().mockResolvedValue({ model: "gpt-test" }),
      listThreads: vi.fn().mockResolvedValue({ threads: [live] }),
      listArchivedThreads: vi
        .fn()
        .mockResolvedValue({ threads: [archivedLatest] }),
      resumeThread,
    });

    const state = await loadRuntime(workspaceList(activeContext));

    expect(resumeThread).toHaveBeenCalledWith("live");
    expect(state.thread?.id).toBe("live");
  });

  it("resumes nothing when every conversation is archived", async () => {
    const activeContext: RuntimeContext = {
      kind: "no_project",
      cwd: "/tmp/wuu",
    };
    const resumeThread = vi.fn();
    installWuuStub({
      initialize: vi.fn().mockResolvedValue({ model: "gpt-test" }),
      listThreads: vi.fn().mockResolvedValue({ threads: [] }),
      listArchivedThreads: vi.fn().mockResolvedValue({
        threads: [thread("archived", { archived: true })],
      }),
      resumeThread,
    });

    const state = await loadRuntime(workspaceList(activeContext));

    expect(resumeThread).not.toHaveBeenCalled();
    expect(state.thread).toBeUndefined();
  });

  it("keeps the runtime available when credentials need setup", async () => {
    const activeContext: RuntimeContext = {
      kind: "no_project",
      cwd: "/tmp/wuu",
    };
    installWuuStub({
      initialize: vi
        .fn()
        .mockResolvedValue({
          status: "needs_setup",
          model: "gpt-test",
          issues: [{ code: "credential_missing", message: "请配置模型凭据" }],
        }),
      listThreads: vi.fn().mockResolvedValue({ threads: [] }),
      listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    });

    const state = await loadRuntime(workspaceList(activeContext));

    expect(state.status).toBe("请配置模型凭据");
    expect(state.activeContext).toEqual(activeContext);
  });

  it("merges archived threads into state.threads so Settings → Archive can list them", async () => {
    const activeContext: RuntimeContext = {
      kind: "no_project",
      cwd: "/tmp/wuu",
    };
    const active = thread("active-1", {
      updated_at: "2026-03-02T00:00:00Z",
    });
    const archivedFromPriorCwd = thread("archived-2", {
      archived: true,
      updated_at: "2026-03-04T00:00:00Z",
      cwd: "/tmp/another-workspace",
    });
    installWuuStub({
      initialize: vi.fn().mockResolvedValue({ model: "gpt-test" }),
      listThreads: vi.fn().mockResolvedValue({ threads: [active] }),
      listArchivedThreads: vi
        .fn()
        .mockResolvedValue({ threads: [archivedFromPriorCwd] }),
    });

    const state = await loadRuntime(workspaceList(activeContext), {
      resumeLatestThread: false,
    });

    // The Settings → Archive panel filters state.threads by thread.archived,
    // so the cross-cwd archived session must end up in the same array as
    // the active one — otherwise the archive page stays empty after a
    // restart for any workspace the user has ever archived a thread in.
    const ids = (state.threads ?? []).map((t) => t.id);
    expect(ids).toContain("active-1");
    expect(ids).toContain("archived-2");
    const archived = state.threads?.find((t) => t.id === "archived-2");
    expect(archived?.archived).toBe(true);
    expect(archived?.cwd).toBe("/tmp/another-workspace");
  });
});


describe("summary list refresh", () => {
  function running(id: string): Thread {
    return thread(id, { status: "in_progress", turns: [{
      id: `${id}-turn`, status: "in_progress", items_view: "full", items: [],
    }] });
  }

  it("repairs both panes and cached running histories without hydrating the catalog", async () => {
    const primary = running("primary");
    const secondary = running("secondary");
    const cached = running("cached");
    const live = running("live");
    const completed = thread(primary.id, { turns: [{
      ...primary.turns[0], status: "completed",
      items: [{ id: "answer", type: "agent_message", text: "Final answer", terminal: true }],
    }] });
    const failed = thread(secondary.id, { turns: [{
      ...secondary.turns[0], status: "failed", error: { message: "Provider unavailable" },
    }] });
    const interrupted = thread(cached.id, { turns: [{ ...cached.turns[0], status: "interrupted" }] });
    const summaries = [completed, failed, interrupted, live, thread("unopened")]
      .map(item => ({ ...item, turns: [] }));
    const resumeThread = vi.fn(async (id?: string) => ({
      thread: [completed, failed, interrupted].find(item => item.id === id)!,
    }));
    installWuuStub({ listThreads: vi.fn().mockResolvedValue({ threads: summaries }), resumeThread });
    const state = { ...initialState, thread: primary, secondaryThread: secondary,
      threads: [primary, secondary, cached, live], activePane: "secondary" as const, running: true };

    const refreshed = reconcileListedThreadState(state, await loadThreadListRefresh(state));

    expect(resumeThread.mock.calls.map(([id]) => id).sort()).toEqual(["cached", "primary", "secondary"]);
    expect(refreshed.thread?.turns).toEqual(completed.turns);
    expect(refreshed.secondaryThread?.turns).toEqual(failed.turns);
    expect(refreshed.threads.find(item => item.id === cached.id)?.turns).toEqual(interrupted.turns);
    expect(refreshed.threads.find(item => item.id === live.id)?.turns).toEqual(live.turns);
    expect(refreshed.threads.find(item => item.id === "unopened")?.turns).toEqual([]);
    expect(refreshed.running).toBe(false);
    expect(refreshed.activePane).toBe("secondary");
  });

  it("keeps failed repairs retryable without blocking other repairs or discovery", async () => {
    const one = running("one");
    const two = running("two");
    const finished = [one, two].map(item => thread(item.id, {
      turns: [{ ...item.turns[0], status: "completed" }],
    }));
    const resumeThread = vi.fn(async (id?: string) => ({ thread: finished.find(item => item.id === id)! }))
      .mockRejectedValueOnce(new Error("Temporary disconnect"));
    installWuuStub({
      listThreads: vi.fn().mockResolvedValue({ threads: [thread("one"), thread("two"), thread("new")] }),
      resumeThread,
    });
    const state = { ...initialState, thread: one, threads: [one, two], running: true };
    const first = reconcileListedThreadState(state, await loadThreadListRefresh(state));
    expect(first.thread?.turns).toEqual(one.turns);
    expect(first.threads.find(item => item.id === "two")?.turns).toEqual(finished[1].turns);
    expect(first.threads.some(item => item.id === "new")).toBe(true);
    expect(first.running).toBe(true);

    resumeThread.mockClear();
    const retried = reconcileListedThreadState(first, await loadThreadListRefresh(first));
    expect(resumeThread).toHaveBeenCalledExactlyOnceWith("one");
    expect(retried.thread?.turns).toEqual(finished[0].turns);
    expect(retried.running).toBe(false);
  });
});

describe("runtime restoration", () => {
  const context: RuntimeContext = { kind: "no_project", cwd: "/tmp/wuu" };

  it("restores opened histories and streaming text without discarding tab drafts", async () => {
    const running = thread("opened", {
      status: "in_progress",
      turns: [{ id: "restore-turn", status: "in_progress", items_view: "full", items: [
        { id: "restore-item", type: "agent_message", status: "in_progress", text: "Before disconnect, and after reconnect" },
      ] }],
    } as Partial<Thread>);
    const cached = thread("opened", { status: "in_progress", turns: [
      { ...running.turns[0], items: [{ ...running.turns[0].items[0], text: "Before disconnect" }] },
    ] });
    const key = streamTextKey("restore-turn", "restore-item", "text");
    streamTextStore.set(key, "Before disconnect");
    const tab = { ...createThreadSessionTab(cached, context), prompt: "Unsent draft" };
    const state = { ...initialState, activeContext: context, thread: cached, threads: [cached],
      sessionTabs: [tab], activeSessionTabID: tab.id, running: true };
    const resumeThread = vi.fn().mockResolvedValue({ thread: running, pending_user_messages: [] });
    installWuuStub({
      initialize: vi.fn().mockResolvedValue({ status: "ready", model: "updated" }),
      listThreads: vi.fn().mockResolvedValue({ threads: [thread("opened"), thread("not-opened")] }),
      listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
      resumeThread,
    });
    window.wuu.listProjects = vi.fn().mockResolvedValue({ projects: [], active_context: context });
    window.wuu.listAllThreads = window.wuu.listThreads;
    const snapshot = await loadRuntimeRestore(state);
    const restored = applyRuntimeRestore(state, snapshot);
    expect(resumeThread).toHaveBeenCalledExactlyOnceWith("opened");
    expect(restored.thread?.turns[0].items[0].text).toBe("Before disconnect, and after reconnect");
    expect(streamTextStore.get(key)).toBe("Before disconnect, and after reconnect");
    expect(restored.sessionTabs[0]).toMatchObject({ prompt: "Unsent draft" });
    expect(restored.activeSessionTabID).toBe(tab.id);
    expect(restored.running).toBe(true);
    streamTextStore.clearTurn("restore-turn");
  });

  it("restores inactive workspace tabs and opened child conversations", async () => {
    const one = thread("one", { child_agents: [{ id: "child" }] } as Partial<Thread>);
    const two = thread("two", { cwd: "/other" });
    const child = thread("child", { read_only: true, parent_id: "one" });
    const tabs = [one, two, child].map((item) => createThreadSessionTab(item, { kind: "no_project", cwd: item.cwd }));
    const state = { ...initialState, activeContext: context, thread: one,
      threads: [one, two, child], sessionTabs: tabs, activeSessionTabID: tabs[0].id };
    const resumeThread = vi.fn(async (id) => ({ thread: [one, two, child].find((item) => item.id === id)! }));
    installWuuStub({
      listProjects: vi.fn().mockResolvedValue({ projects: [], active_context: context }),
      initialize: vi.fn().mockResolvedValue({ status: "ready" }),
      listAllThreads: vi.fn().mockResolvedValue({ threads: [one, two] }),
      listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }), resumeThread,
    });
    const restored = applyRuntimeRestore(state, await loadRuntimeRestore(state));
    expect(resumeThread.mock.calls.map(([id]) => id).sort()).toEqual(["child", "one", "two"]);
    expect(restored.sessionTabs).toEqual(tabs);
    expect(restored.activeContext).toEqual(context);
  });

  it("restores only visible remote histories while retaining background and child tabs", async () => {
    const one = thread("one", {child_agents:[{id:"child"}]} as Partial<Thread>);
    const two = thread("two");
    const child = thread("child", {parent_id:"one",read_only:true});
    const tabs = [one,two,child].map(item => createThreadSessionTab(item, context));
    const state = {...initialState,activeContext:context,thread:one,threads:[one,two,child],sessionTabs:tabs,activeSessionTabID:tabs[0].id};
    const resumeThread = vi.fn(async () => ({thread:one}));
    installWuuStub({listProjects:vi.fn().mockResolvedValue({projects:[],active_context:context}),initialize:vi.fn().mockResolvedValue({status:"ready"}),listAllThreads:vi.fn().mockResolvedValue({threads:[one,two]}),listArchivedThreads:vi.fn().mockResolvedValue({threads:[]}),resumeThread});
    window.wuu.loadEarlierThreadHistory = vi.fn();
    const restored = applyRuntimeRestore(state,await loadRuntimeRestore(state));
    expect(resumeThread).toHaveBeenCalledExactlyOnceWith("one");
    expect(restored.sessionTabs).toEqual(tabs);
    delete window.wuu.loadEarlierThreadHistory;
  });

  it("reconciles completion, unarchive and deletion performed while disconnected", () => {
    const running = thread("running", { status: "in_progress", turns: [
      { id: "turn", status: "in_progress", items_view: "full", items: [] },
    ] } as Partial<Thread>);
    const completed = { ...running, status: "idle", turns: [{ ...running.turns[0], status: "completed" }] } as Thread;
    const archived = thread("archived", { archived: true });
    const removed = thread("removed");
    const tabs = [running, removed].map((item) => createThreadSessionTab(item, context));
    const state = { ...initialState, thread: running, secondaryThread: removed, threads: [running, archived, removed],
      sessionTabs: tabs, activeSessionTabID: tabs[0].id, running: true };
    const restored = applyRuntimeRestore(state, {
      projects: { projects: [], active_context: context },
      initialized: { status: "ready" } as InitializeResult,
      threads: [completed, { ...archived, archived: false }], resumed: [],
    });
    expect(restored.running).toBe(false);
    expect(restored.secondaryThread).toBeUndefined();
    expect(restored.threads.map((item) => item.id)).not.toContain("removed");
    expect(restored.threads.find((item) => item.id === "archived")?.archived).toBe(false);
    expect(restored.sessionTabs).toHaveLength(1);
  });
});
