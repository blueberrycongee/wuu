import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext } from "../shared/protocol";
import {
  createAgentsSessionTab,
  createChannelRoomSessionTab,
  createDraftSessionTab,
  createThreadSessionTab,
  emptyComposerDraft,
  initialState,
  type AppState,
  type ComposerDraftState,
  type SessionTab,
} from "./AppState";
import { writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { createSessionTabActions } from "./SessionTabActions";

function projectContext(id = "project-1"): RuntimeContext {
  return { kind: "project", project_id: id, cwd: `/tmp/${id}` };
}

function noProjectContext(): RuntimeContext {
  return { kind: "no_project", cwd: "/tmp/scratch/default" };
}

function sessionTabPrompt(tabs: SessionTab[], id: string): string | undefined {
  const tab = tabs.find((candidate) => candidate.id === id);
  return tab?.kind === "draft" || tab?.kind === "thread" ? tab.prompt : undefined;
}

function buildActions({
  initial,
  draft = emptyComposerDraft(),
}: {
  initial: AppState;
  draft?: ComposerDraftState;
}) {
  let appState = initial;
  let currentDraft = draft;
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
  const resetSplitComposerDrafts = vi.fn();
  const nextDraftSessionTab = vi.fn((context: RuntimeContext): SessionTab =>
    createDraftSessionTab("draft:new", context),
  );
  const selectThread = vi.fn();
  const loadRuntime = vi.fn();
  const selectRuntimeContext = vi.fn();

  const actions = createSessionTabActions({
    getAppState: () => appState,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    getPrimaryComposerDraft: () => currentDraft,
    restorePrimaryComposerDraft,
    clearPrimaryComposerDraft,
    resetSplitComposerDrafts,
    nextDraftSessionTab,
    selectThread,
    beginViewSwitch: vi.fn(() => 1),
    beginInstantThreadSwitch: vi.fn(() => 2),
    finishViewSwitch: vi.fn(() => true),
    cancelViewSwitch: vi.fn(),
    loadRuntime,
    selectRuntimeContext,
  });

  return {
    actions,
    getAppState: () => appState,
    getCurrentDraft: () => currentDraft,
    clearPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    resetSplitComposerDrafts,
    nextDraftSessionTab,
    selectThread,
    loadRuntime,
    selectRuntimeContext,
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
  Reflect.deleteProperty(window, "wuu");
});

describe("createSessionTabActions", () => {
  it("focuses an already active draft without creating or clearing it", async () => {
    const context = projectContext();
    const draftTab = createDraftSessionTab("draft:active", context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: draftTab.id,
        sessionTabs: [draftTab],
      },
    });

    await harness.actions.startNewThread();

    expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    expect(harness.nextDraftSessionTab).not.toHaveBeenCalled();
    expect(harness.getAppState().activeSessionTabID).toBe(draftTab.id);
    expect(harness.getAppState().thread).toBeUndefined();
  });

  it("starts a new conversation and preserves the source conversation draft", async () => {
    const context = noProjectContext();
    const thread = {
      id: "thread-1", title: "existing", preview: "existing", cwd: context.cwd,
      status: "idle" as const, model_provider: "fake", model: "fake-model",
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", turns: [],
    };
    const source = createThreadSessionTab(thread, context);
    const draft = { ...emptyComposerDraft(), prompt: "keep this unfinished follow-up" };
    const harness = buildActions({
      initial: {
        ...initialState, activeContext: context, thread,
        activeSessionTabID: source.id, sessionTabs: [source],
      },
      draft,
    });

    await harness.actions.startNewThread();

    expect(harness.nextDraftSessionTab).toHaveBeenCalledWith(context);
    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:new");
    expect(harness.getCurrentDraft()).toEqual(emptyComposerDraft());
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, source.id)).toBe(draft.prompt);
  });

  it("restores the last composer provider, model, and effort on a new conversation", async () => {
    const context = noProjectContext();
    writeDraftRuntimeMemory({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      effort: "high",
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        initialized: {
          protocol_version: "wuu-app-server/v0.1",
          provider: "work",
          model: "claude-sonnet",
          variant: "medium",
          effort: "medium",
          workspace_root: context.cwd,
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
        },
        thread: {
          id: "thread-1",
          title: "existing",
          preview: "existing",
          cwd: context.cwd,
          status: "idle",
          model_provider: "work",
          model: "claude-sonnet",
          pinned: false,
          archived: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          turns: [],
        },
      },
    });

    await harness.actions.startNewThread();

    expect(harness.getAppState().initialized).toMatchObject({
      provider: "tokenhub",
      model: "gpt-5.6-sol",
      variant: "high",
      effort: "high",
    });
  });

  it("reuses the existing project draft instead of creating another tab", async () => {
    const context = projectContext();
    const source = {
      id: "thread-1",
      title: "existing",
      preview: "existing",
      cwd: context.cwd,
      status: "idle" as const,
      model_provider: "fake",
      model: "fake-model",
      pinned: false,
      archived: false,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      turns: [],
    };
    const sourceTab = createThreadSessionTab(source, context);
    const existingDraft = createDraftSessionTab("draft:existing", context, {
      prompt: "continue this later",
      images: [],
      files: [],
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: source,
        sessionTabs: [sourceTab, existingDraft],
        activeSessionTabID: sourceTab.id,
      },
      draft: { prompt: "keep this in the source", images: [], files: [] },
    });

    await harness.actions.startNewThread();

    expect(harness.nextDraftSessionTab).not.toHaveBeenCalled();
    expect(harness.getAppState().activeSessionTabID).toBe(existingDraft.id);
    expect(harness.getCurrentDraft().prompt).toBe("continue this later");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, sourceTab.id)).toBe(
      "keep this in the source",
    );
  });

  it("reorders session tabs", () => {
    const context = projectContext();
    const first = createDraftSessionTab("draft:first", context);
    const second = createDraftSessionTab("draft:second", context);
    const third = createDraftSessionTab("draft:third", context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: first.id,
        sessionTabs: [first, second, third],
      },
    });

    harness.actions.reorderSessionTabs(first.id, third.id);

    expect(harness.getAppState().sessionTabs.map((tab) => tab.id)).toEqual([
      second.id,
      third.id,
      first.id,
    ]);
  });

  it("selects global channel tabs without changing the workspace runtime", async () => {
    const context = projectContext();
    const otherContext = projectContext("project-2");
    const source = createDraftSessionTab("draft:source", context);
    const agents = createAgentsSessionTab(otherContext);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: source.id,
        sessionTabs: [source, agents],
      },
      draft: { prompt: "keep this draft", images: [], files: [] },
    });

    await harness.actions.selectSessionTab(agents.id);

    expect(harness.getAppState().activeSessionTabID).toBe(agents.id);
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, source.id)).toBe(
      "keep this draft",
    );
    expect(harness.loadRuntime).not.toHaveBeenCalled();
    expect(harness.selectRuntimeContext).not.toHaveBeenCalled();
  });

  it("ensures and activates a newly opened global channel tab", () => {
    const context = projectContext();
    const source = createDraftSessionTab("draft:source", context);
    const agents = createAgentsSessionTab(context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: source.id,
        sessionTabs: [source],
      },
    });

    harness.actions.openGlobalSessionTab(agents);
    harness.actions.openGlobalSessionTab({ ...agents, title: "updated" });

    expect(harness.getAppState().activeSessionTabID).toBe(agents.id);
    expect(harness.getAppState().sessionTabs.filter((tab) => tab.id === agents.id)).toEqual([
      { ...agents, title: "updated" },
    ]);
  });

  it("preserves a room draft when the same room is opened again", () => {
    const context = projectContext();
    const source = createDraftSessionTab("draft:source", context);
    const room = {
      ...createChannelRoomSessionTab("room-1", "Old title", context),
      prompt: "unfinished message",
    };
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: source.id,
        sessionTabs: [source, room],
      },
    });

    harness.actions.openGlobalSessionTab(
      createChannelRoomSessionTab("room-1", "New title", context),
    );

    expect(harness.getAppState().sessionTabs.find((tab) => tab.id === room.id)).toMatchObject({
      title: "New title",
      prompt: "unfinished message",
    });
  });

  it("falls back to a global channel tab without resuming a thread", async () => {
    const context = projectContext();
    const source = createDraftSessionTab("draft:source", context);
    const room = createChannelRoomSessionTab("room-1", "Design review", context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: source.id,
        sessionTabs: [source, room],
      },
    });

    await harness.actions.closeSessionTab(source.id);

    expect(harness.getAppState().activeSessionTabID).toBe(room.id);
    expect(harness.selectThread).not.toHaveBeenCalled();
    expect(harness.loadRuntime).not.toHaveBeenCalled();
  });

  it("removes the active tab before the fallback thread finishes resuming", async () => {
    const context = projectContext();
    const source = {
      id: "thread-source",
      preview: "Source",
      model_provider: "fake",
      model: "fake-model",
      cwd: context.cwd,
      status: "idle" as const,
      archived: false,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      turns: [],
    };
    const fallback = { ...source, id: "thread-fallback", preview: "Fallback" };
    const sourceTab = createThreadSessionTab(source, context);
    const fallbackTab = createThreadSessionTab(fallback, context);
    let resolveResume: ((value: { thread: typeof fallback }) => void) | undefined;
    const resumeThread = vi.fn(
      () =>
        new Promise<{ thread: typeof fallback }>((resolve) => {
          resolveResume = resolve;
        }),
    );
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { resumeThread },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: source,
        threads: [source, fallback],
        sessionTabs: [sourceTab, fallbackTab],
        activeSessionTabID: sourceTab.id,
      },
    });

    const closing = harness.actions.closeSessionTab(sourceTab.id);

    expect(harness.getAppState().sessionTabs.map((tab) => tab.id)).toEqual([
      fallbackTab.id,
    ]);
    expect(harness.getAppState().activeSessionTabID).toBe(fallbackTab.id);
    expect(resumeThread).toHaveBeenCalledWith(fallback.id);

    resolveResume?.({ thread: fallback });
    await closing;
  });

  it("preserves input typed during a cached cross-workspace tab refresh", async () => {
    const sourceContext = projectContext("source");
    const targetContext = projectContext("target");
    const source = createDraftSessionTab("draft:source", sourceContext);
    const thread = {
      id: "target-thread", preview: "Target", cwd: targetContext.cwd, status: "idle" as const,
      model_provider: "fake", model: "fake", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
      turns: [{ id: "turn", items_view: "full" as const, status: "completed" as const, items: [] }],
    };
    const target = createThreadSessionTab(thread, targetContext, { ...emptyComposerDraft(), prompt: "old target draft" });
    const harness = buildActions({
      initial: { ...initialState, activeContext: sourceContext, activeSessionTabID: source.id, sessionTabs: [source, target], threads: [thread] },
    });
    let resolveResume!: (value: { thread: typeof thread }) => void;
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { resumeThread: () => new Promise((resolve) => { resolveResume = resolve; }) },
    });
    harness.selectRuntimeContext.mockResolvedValue({ projects: [], active_context: targetContext });
    const switching = harness.actions.selectSessionTab(target.id);
    expect(harness.getCurrentDraft().prompt).toBe("old target draft");
    harness.restorePrimaryComposerDraft({ ...emptyComposerDraft(), prompt: "new target draft" });
    await Promise.resolve();
    resolveResume({ thread });
    await switching;
    expect(harness.getCurrentDraft().prompt).toBe("new target draft");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, target.id)).toBe("new target draft");
    expect(harness.resetSplitComposerDrafts).toHaveBeenCalledTimes(1);
  });

  it("pops out a draft tab and closes it after the detached window opens", async () => {
    const context = projectContext();
    const activeDraft = createDraftSessionTab("draft:active", context);
    const fallbackDraft = createDraftSessionTab("draft:fallback", context);
    const popOutSession = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { popOutSession },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        activeSessionTabID: activeDraft.id,
        sessionTabs: [activeDraft, fallbackDraft],
      },
    });

    await harness.actions.popOutSessionTab(activeDraft.id);

    expect(popOutSession).toHaveBeenCalledWith({
      kind: "draft",
      context,
    });
    expect(harness.getAppState().sessionTabs.map((tab) => tab.id)).toEqual([
      fallbackDraft.id,
    ]);
    expect(harness.getAppState().activeSessionTabID).toBe(fallbackDraft.id);
  });
});
