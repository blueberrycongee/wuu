import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import {
  createDraftSessionTab,
  captureComposerDrafts,
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
  splitDrafts,
}: {
  initial: AppState;
  draft?: ComposerDraftState;
  splitDrafts?: Record<"primary" | "secondary", ComposerDraftState>;
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
    getComposerDraftSnapshot: () => captureComposerDrafts(appState, appState.secondaryThread && splitDrafts ? splitDrafts : currentDraft),
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
    setAppState: (state: AppState) => { appState = state; },
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
  it.each(["primary", "secondary"] as const)("saves both %s-active split drafts before a new conversation", async (activePane) => {
    const context = projectContext();
    const a: Thread = { id: "split-a", title: "A", preview: "A", cwd: context.cwd, status: "idle", model_provider: "fake", model: "fake", pinned: false, archived: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", turns: [] };
    const b = { ...a, id: "split-b", title: "B" };
    const aTab = createThreadSessionTab(a, context);
    const bTab = createThreadSessionTab(b, context);
    const initial: AppState = { ...initialState, activeContext: context, thread: a, secondaryThread: b, activePane, sessionTabs: [aTab, bTab], activeSessionTabID: activePane === "primary" ? aTab.id : bTab.id };
    const splitDrafts = { primary: { prompt: "A edited", images: [], files: [] }, secondary: { prompt: "B edited", images: [], files: [] } };
    const harness = buildActions({ initial, draft: { prompt: "stale global", images: [], files: [] }, splitDrafts });
    await harness.actions.startNewThread();
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, aTab.id)).toBe("A edited");
    expect(sessionTabPrompt(harness.getAppState().sessionTabs, bTab.id)).toBe("B edited");
    expect(harness.getCurrentDraft().prompt).toBe("");
    expect(harness.getAppState().secondaryThread).toBeUndefined();
  });

  it.each(["draft", "thread"])("restores attachment bytes that settle in the target %s during a runtime switch", async (kind) => {
    const sourceContext = projectContext();
    const targetContext = projectContext("project-2");
    const source = createDraftSessionTab("source", sourceContext);
    const targetThread: Thread = { id: "target", title: "Target", preview: "Target", cwd: targetContext.cwd, status: "idle", model_provider: "fake", model: "fake", pinned: false, archived: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", turns: [] };
    const draft = { prompt: "Target", images: [], files: [{ id: "pdf", media_type: "application/pdf", data: "" }] };
    const target = kind === "draft" ? createDraftSessionTab("target", targetContext, draft) : createThreadSessionTab(targetThread, targetContext, draft);
    Object.defineProperty(window, "wuu", { configurable: true, value: { resumeThread: vi.fn().mockResolvedValue({ thread: targetThread }) } });
    const harness = buildActions({ initial: { ...initialState, activeContext: sourceContext, sessionTabs: [source, target], activeSessionTabID: source.id } });
    let finish!: (state: Partial<AppState>) => void;
    harness.selectRuntimeContext.mockResolvedValue({ active_context: targetContext, projects: [] });
    harness.loadRuntime.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const navigation = harness.actions.selectSessionTab(target.id);
    await Promise.resolve();
    // Attachment completion updates its stored slot while the runtime IPC waits.
    const current = harness.getAppState();
    harness.setAppState({ ...current, sessionTabs: current.sessionTabs.map(tab => tab.id === target.id ? { ...target, files: [{ id: "pdf", media_type: "application/pdf", data: "encoded" }] } : tab) });
    finish({ activeContext: targetContext });
    await navigation;
    expect(harness.getCurrentDraft().files[0].data).toBe("encoded");
    expect(harness.getAppState().sessionTabs.find(tab => tab.id === target.id)).toMatchObject({ files: [{ data: "encoded" }] });
  });

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

  it("starts a new no-project conversation in the current workspace", async () => {
    const context = noProjectContext();
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: {
          id: "thread-1",
          title: "existing",
          preview: "existing",
          cwd: context.cwd,
          status: "idle",
          model_provider: "fake",
          model: "fake-model",
          pinned: false,
          archived: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          turns: [],
        },
      },
    });

    await harness.actions.startNewThread();

    expect(harness.nextDraftSessionTab).toHaveBeenCalledWith(context);
    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:new");
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
