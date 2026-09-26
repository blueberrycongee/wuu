import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  DesktopProject,
  ProjectListResult,
  RuntimeContext,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";
import {
  createDraftSessionTab,
  createThreadSessionTab,
  emptyComposerDraft,
  initialState,
  threadSessionTabID,
  type AppState,
  type ComposerDraftState,
  type SessionTab,
} from "./AppState";
import { createWorkspaceRuntimeActions } from "./WorkspaceRuntimeActions";

function projectContext(id = "project-1"): RuntimeContext {
  return { kind: "project", project_id: id, cwd: `/tmp/${id}` };
}

function noProjectContext(): RuntimeContext {
  return { kind: "no_project", cwd: "/tmp/scratch/default" };
}

function project(id = "project-1"): DesktopProject {
  return {
    id,
    name: id,
    path: `/tmp/${id}`,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function thread(id = "thread-1", status = "idle"): Thread {
  return {
    id,
    title: id,
    preview: id,
    cwd: "/tmp/project-1",
    status,
    model_provider: "fake",
    model: "fake-model",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: status === "in_progress"
      ? [{ id: "turn-1", status: "in_progress", items_view: "full", items: [] }]
      : [],
  } as Thread;
}

function sessionTabPrompt(tabs: SessionTab[], id: string): string | undefined {
  const tab = tabs.find((candidate) => candidate.id === id);
  return tab?.kind === "draft" || tab?.kind === "thread" ? tab.prompt : undefined;
}

function buildActions({
  initial,
  draft = emptyComposerDraft(),
  loadRuntime = vi.fn(),
}: {
  initial: AppState;
  draft?: ComposerDraftState;
  loadRuntime?: ReturnType<typeof vi.fn>;
}) {
  let appState = initial;
  let currentDraft = draft;
  const closeWorkspaceMenus = vi.fn();
  const clearPrimaryComposerDraft = vi.fn(() => {
    currentDraft = emptyComposerDraft();
  });
  const restorePrimaryComposerDraft = vi.fn((nextDraft: ComposerDraftState) => {
    currentDraft = {
      prompt: nextDraft.prompt,
      images: [...nextDraft.images],
      files: [...nextDraft.files],
    };
  });
  const nextDraftSessionTab = vi.fn((context: RuntimeContext): SessionTab =>
    createDraftSessionTab("draft:test", context),
  );

  const actions = createWorkspaceRuntimeActions({
    getAppState: () => appState,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    getPrimaryComposerDraft: () => currentDraft,
    restorePrimaryComposerDraft,
    clearPrimaryComposerDraft,
    restoreLoadedRuntimeComposerDraft: vi.fn(),
    nextDraftSessionTab,
    closeWorkspaceMenus,
    beginViewSwitch: vi.fn(() => 1),
    finishViewSwitch: vi.fn(() => true),
    cancelViewSwitch: vi.fn(),
    loadRuntime,
  });

  return {
    actions,
    getAppState: () => appState,
    closeWorkspaceMenus,
    clearPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    getCurrentDraft: () => currentDraft,
    nextDraftSessionTab,
  };
}

afterEach(() => {
  Reflect.deleteProperty(window, "wuu");
  vi.restoreAllMocks();
});

describe("createWorkspaceRuntimeActions", () => {
  it("preserves an unsent draft when the project workspace plus opens a session", async () => {
    const context = projectContext();
    const source = thread();
    const sourceTab = createThreadSessionTab(source, context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeProjectId: "project-1",
        projects: [project()],
        thread: source,
        sessionTabs: [sourceTab],
        activeSessionTabID: sourceTab.id,
      },
      draft: { prompt: "keep this draft", images: [], files: [] },
    });

    await harness.actions.startNewThreadInWorkspace("project-1");

    expect(harness.getAppState().activeSessionTabID).toBe("draft:test");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, sourceTab.id)).toBe(
      "keep this draft",
    );
  });

  it("focuses the existing project draft instead of adding another one", async () => {
    const context = projectContext();
    const source = thread();
    const sourceTab = createThreadSessionTab(source, context);
    const existingDraft = createDraftSessionTab("draft:existing", context, {
      prompt: "finish this project task",
      images: [],
      files: [],
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeProjectId: "project-1",
        projects: [project()],
        thread: source,
        sessionTabs: [sourceTab, existingDraft],
        activeSessionTabID: sourceTab.id,
      },
      draft: { prompt: "keep this thread draft", images: [], files: [] },
    });

    await harness.actions.startNewThreadInWorkspace("project-1");

    expect(harness.nextDraftSessionTab).not.toHaveBeenCalled();
    expect(harness.getAppState().activeSessionTabID).toBe(existingDraft.id);
    expect(harness.getCurrentDraft().prompt).toBe("finish this project task");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, sourceTab.id)).toBe(
      "keep this thread draft",
    );
  });

  it("retargets a draft to another project while a background thread is running", async () => {
    const sourceContext = projectContext("project-1");
    const targetContext = projectContext("project-2");
    const sourceDraft = createDraftSessionTab("draft:project-1", sourceContext);
    const workspaceState = {
      projects: [project("project-1"), project("project-2")],
      active_context: targetContext,
    } as ProjectListResult;
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: targetContext,
      activeProjectId: "project-2",
      thread: undefined,
      threads: [],
      status: "ready",
    });
    const selectProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { selectProject } as Partial<WuuDesktopApi>,
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
        threads: [thread("running", "in_progress")],
        sessionTabs: [sourceDraft],
        activeSessionTabID: sourceDraft.id,
      },
      loadRuntime,
    });

    await harness.actions.selectWorkspaceForNewThread("project-2");

    expect(selectProject).toHaveBeenCalledWith("project-2");
    expect(harness.closeWorkspaceMenus).toHaveBeenCalled();
    expect(harness.getAppState().activeContext).toEqual(targetContext);
    expect(harness.getAppState().status).toBe("ready");
  });

  it("opens a blank draft when creating a no-project conversation", async () => {
    const context = noProjectContext();
    const source = { ...thread(), cwd: context.cwd };
    const workspaceState = {
      projects: [],
      active_context: context,
    } as ProjectListResult;
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: context,
      thread: undefined,
      threads: [source],
      status: "ready",
    });
    const selectNoProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { selectNoProject } as Partial<WuuDesktopApi>,
    });
    const sourceTab = createThreadSessionTab(source, context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: source,
        sessionTabs: [sourceTab],
        activeSessionTabID: threadSessionTabID(source.id),
      },
      draft: { prompt: "keep this draft", images: [], files: [] },
      loadRuntime,
    });

    await harness.actions.useNoProject(true);

    expect(selectNoProject).toHaveBeenCalledWith(true);
    expect(loadRuntime).toHaveBeenCalledWith(workspaceState, {
      resumeLatestThread: false,
    });
    expect(harness.nextDraftSessionTab).toHaveBeenCalledWith(context);
    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:test");
    expect(harness.getCurrentDraft()).toEqual(emptyComposerDraft());
  });

  it("lands on the 对话 draft page instead of resuming an old conversation when the picker selects 不使用工作区", async () => {
    const sourceContext = projectContext();
    const context = noProjectContext();
    const oldScratchThread = { ...thread("old-scratch"), cwd: context.cwd };
    const sourceDraftTab = createDraftSessionTab("draft:new-project", sourceContext);
    const workspaceState = {
      projects: [project()],
      active_context: context,
    } as ProjectListResult;
    // The runtime load must be asked NOT to resume: even though the 对话
    // workspace holds an older conversation, retargeting a draft lands on
    // the draft page, never inside history.
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: context,
      thread: undefined,
      threads: [oldScratchThread],
      status: "ready",
    });
    const selectNoProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { selectNoProject } as Partial<WuuDesktopApi>,
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        projects: [project()],
        sessionTabs: [sourceDraftTab],
        activeSessionTabID: sourceDraftTab.id,
      },
      draft: { prompt: "draft aimed at no project", images: [], files: [] },
      loadRuntime,
    });

    await harness.actions.useNoProject(false);

    expect(selectNoProject).toHaveBeenCalledWith(false);
    expect(loadRuntime).toHaveBeenCalledWith(workspaceState, {
      resumeLatestThread: false,
    });
    const state = harness.getAppState();
    expect(state.thread).toBeUndefined();
    const landingTab = state.sessionTabs.find(
      (tab) => tab.id === state.activeSessionTabID,
    );
    expect(landingTab?.kind).toBe("draft");
    expect(
      landingTab?.kind === "draft" ? landingTab.context : undefined,
    ).toEqual(context);
    // The typed draft travels with the user into the landing tab.
    expect(sessionTabPrompt(state.sessionTabs, state.activeSessionTabID)).toBe(
      "draft aimed at no project",
    );
  });

  it("returns to the 对话 draft page when 不使用工作区 is re-selected over an open conversation", async () => {
    const context = noProjectContext();
    const source = { ...thread(), cwd: context.cwd };
    const sourceTab = createThreadSessionTab(source, context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: source,
        sessionTabs: [sourceTab],
        activeSessionTabID: sourceTab.id,
      },
      draft: { prompt: "keep this thread draft", images: [], files: [] },
    });

    await harness.actions.useNoProject(false);

    expect(harness.closeWorkspaceMenus).toHaveBeenCalled();
    expect(harness.nextDraftSessionTab).toHaveBeenCalledWith(context);
    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:test");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, sourceTab.id)).toBe(
      "keep this thread draft",
    );
  });

  it("switches to 不使用工作区 while a background thread is running", async () => {
    const sourceContext = projectContext("project-1");
    const targetContext = noProjectContext();
    const sourceDraft = createDraftSessionTab("draft:project-1", sourceContext);
    const workspaceState = {
      projects: [project("project-1")],
      active_context: targetContext,
    } as ProjectListResult;
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: targetContext,
      activeProjectId: undefined,
      thread: undefined,
      threads: [],
      status: "ready",
    });
    const selectNoProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { selectNoProject } as Partial<WuuDesktopApi>,
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        projects: [project("project-1")],
        threads: [thread("running", "in_progress")],
        sessionTabs: [sourceDraft],
        activeSessionTabID: sourceDraft.id,
      },
      loadRuntime,
    });

    await harness.actions.useNoProject(false);

    expect(selectNoProject).toHaveBeenCalledWith(false);
    expect(harness.closeWorkspaceMenus).toHaveBeenCalled();
    expect(harness.getAppState().activeContext).toEqual(targetContext);
    expect(harness.getAppState().status).toBe("ready");
  });

  it("reuses an existing no-project draft when the 对话 workspace plus is clicked", async () => {
    const sourceContext = projectContext();
    const context = noProjectContext();
    const source = thread();
    const sourceTab = createThreadSessionTab(source, sourceContext);
    const existingDraft = createDraftSessionTab("draft:existing", context);
    const workspaceState = {
      projects: [],
      active_context: context,
    } as ProjectListResult;
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: context,
      thread: undefined,
      threads: [],
      status: "ready",
    });
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        selectNoProject: vi.fn().mockResolvedValue(workspaceState),
      } as Partial<WuuDesktopApi>,
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        thread: source,
        sessionTabs: [sourceTab, existingDraft],
        activeSessionTabID: sourceTab.id,
      },
      draft: { prompt: "keep this draft", images: [], files: [] },
      loadRuntime,
    });

    await harness.actions.useNoProject(true);

    expect(harness.nextDraftSessionTab).not.toHaveBeenCalled();
    expect(harness.getAppState().activeSessionTabID).toBe(existingDraft.id);
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, sourceTab.id)).toBe(
      "keep this draft",
    );
  });

  it("adds an existing workspace without switching or reloading the runtime", async () => {
    const sourceContext = projectContext("project-1");
    const addedContext = projectContext("project-2");
    const sourceTab = createDraftSessionTab("draft:source", sourceContext);
    const staleThreadTab = createThreadSessionTab(thread("old-session"), addedContext);
    const staleDraftTab = createDraftSessionTab("draft:stale", addedContext, {
      prompt: "old workspace draft",
      images: [],
      files: [],
    });
    const workspaceState = {
      projects: [project("project-1"), project("project-2")],
      active_context: sourceContext,
    } as ProjectListResult;
    const loadRuntime = vi.fn();
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        chooseProjectFolder: vi.fn().mockResolvedValue(workspaceState),
      } as Partial<WuuDesktopApi>,
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        projects: [project("project-1")],
        sessionTabs: [sourceTab, staleThreadTab, staleDraftTab],
        activeSessionTabID: sourceTab.id,
      },
      loadRuntime,
    });

    await harness.actions.chooseProjectFolder();

    expect(loadRuntime).not.toHaveBeenCalled();
    const state = harness.getAppState();
    expect(state.activeContext).toEqual(sourceContext);
    expect(state.activeSessionTabID).toBe(sourceTab.id);
    expect(state.sessionTabs).toEqual([sourceTab, staleThreadTab, staleDraftTab]);
    expect(state.projects).toEqual(workspaceState.projects);
  });

  it("does not remove a workspace when confirmation is cancelled", async () => {
    const removeProject = vi.fn();
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { removeProject } as Partial<WuuDesktopApi>,
    });
    vi.spyOn(window, "confirm").mockReturnValue(false);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        activeProjectId: "project-1",
        projects: [project()],
      },
    });

    await harness.actions.removeProject("project-1");

    expect(removeProject).not.toHaveBeenCalled();
    expect(harness.getAppState().projects).toEqual([project()]);
  });

  it("closes only the removed workspace tabs when another workspace stays active", async () => {
    const activeContext = projectContext("project-1");
    const removedContext = projectContext("project-2");
    const activeTab = createDraftSessionTab("draft:active", activeContext);
    const removedTab = createThreadSessionTab(thread("removed-session"), removedContext);
    const workspaceState = {
      projects: [project("project-1")],
      active_context: activeContext,
    } as ProjectListResult;
    const removeProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { removeProject } as Partial<WuuDesktopApi>,
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const loadRuntime = vi.fn();
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext,
        activeProjectId: "project-1",
        projects: [project("project-1"), project("project-2")],
        sessionTabs: [activeTab, removedTab],
        activeSessionTabID: activeTab.id,
      },
      loadRuntime,
    });

    await harness.actions.removeProject("project-2");

    expect(loadRuntime).not.toHaveBeenCalled();
    expect(harness.getAppState().sessionTabs).toEqual([activeTab]);
    expect(harness.getAppState().activeSessionTabID).toBe(activeTab.id);
  });

  it("lands on a scratch draft and closes workspace tabs after removing the active workspace", async () => {
    const sourceContext = projectContext();
    const scratchContext = noProjectContext();
    const source = thread();
    const sourceTab = createThreadSessionTab(source, sourceContext);
    const workspaceState = {
      projects: [],
      active_context: scratchContext,
    } as ProjectListResult;
    const loadRuntime = vi.fn().mockResolvedValue({
      activeContext: scratchContext,
      activeProjectId: undefined,
      thread: undefined,
      threads: [
        {
          ...thread("background-agent"),
          source: "background-agent",
          workspace_kind: "scratch",
        },
      ],
      status: "ready",
    });
    const removeProject = vi.fn().mockResolvedValue(workspaceState);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { removeProject } as Partial<WuuDesktopApi>,
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: sourceContext,
        activeProjectId: "project-1",
        projects: [project()],
        thread: source,
        sessionTabs: [
          sourceTab,
          createDraftSessionTab("draft:removed-project", sourceContext),
        ],
        activeSessionTabID: sourceTab.id,
      },
      loadRuntime,
    });

    await harness.actions.removeProject("project-1");

    expect(removeProject).toHaveBeenCalledWith("project-1");
    expect(loadRuntime).toHaveBeenCalledWith(workspaceState, {
      resumeLatestThread: false,
    });
    expect(harness.getAppState().thread).toBeUndefined();
    const activeTab = harness.getAppState().sessionTabs.find(
      (tab) => tab.id === harness.getAppState().activeSessionTabID,
    );
    expect(activeTab?.kind).toBe("draft");
    expect(activeTab?.kind === "draft" ? activeTab.context : undefined).toEqual(
      scratchContext,
    );
    expect(
      harness.getAppState().sessionTabs.some(
        (tab) =>
          tab.context.kind === "project" &&
          tab.context.project_id === "project-1",
      ),
    ).toBe(false);
  });
});
