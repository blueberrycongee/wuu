import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import {
  createDraftSessionTab,
  createThreadSessionTab,
  initialState,
  threadSessionTabID,
  type AppState,
  type ThreadSummary,
} from "./AppState";
import { createThreadMutationActions } from "./ThreadMutationActions";

const toastMocks = vi.hoisted(() => ({
  showErrorToast: vi.fn(),
}));

vi.mock("./Toast", () => ({
  showErrorToast: toastMocks.showErrorToast,
}));

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
  toastMocks.showErrorToast.mockClear();
  restoreWuu();
});

function projectContext(): RuntimeContext {
  return { kind: "project", project_id: "project-1", cwd: "/tmp/project-1" };
}

function thread(id = "thread-1"): Thread {
  return {
    id,
    title: id,
    preview: id,
    model_provider: "fake",
    model: "fake-model",
    cwd: "/tmp/project-1",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

function summary(source: Thread): ThreadSummary {
  return {
    ...source,
    turns: [],
    turn_count: source.turns.length,
  };
}

function installWuuApi(baseThread: Thread): {
  pinThread: ReturnType<typeof vi.fn>;
  archiveThread: ReturnType<typeof vi.fn>;
  deleteThread: ReturnType<typeof vi.fn>;
} {
  const pinThread = vi.fn().mockResolvedValue({
    thread: { ...baseThread, pinned: true },
  });
  const archiveThread = vi.fn().mockResolvedValue({
    thread: { ...baseThread, archived: true },
  });
  const deleteThread = vi.fn().mockResolvedValue({});
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      pinThread,
      archiveThread,
      deleteThread,
      renameThread: vi.fn().mockResolvedValue({ thread: baseThread }),
    },
  });
  return { pinThread, archiveThread, deleteThread };
}

function buildActions({
  initial,
  activeThreadID,
}: {
  initial: AppState;
  activeThreadID?: string;
}) {
  let appState = initial;
  const clearPrimaryComposerDraft = vi.fn();
  const resetSplitComposerDrafts = vi.fn();
  const updateCachedSidebarThread = vi.fn();
  const updateCachedSidebarThreadPinned = vi.fn();
  const removeCachedSidebarThread = vi.fn();
  const clearThreadPendingComposerMessages = vi.fn();
  const actions = createThreadMutationActions({
    getAppState: () => appState,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    getActiveThreadID: () => activeThreadID,
    nextDraftSessionTab: (context) =>
      createDraftSessionTab("draft:fallback", context),
    clearPrimaryComposerDraft,
    resetSplitComposerDrafts,
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    clearThreadPendingComposerMessages,
  });

  return {
    actions,
    getAppState: () => appState,
    clearPrimaryComposerDraft,
    resetSplitComposerDrafts,
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    clearThreadPendingComposerMessages,
  };
}

describe("createThreadMutationActions", () => {
  it("pins a server thread and updates the sidebar cache", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    await harness.actions.toggleThreadPinned(summary(base));

    expect(api.pinThread).toHaveBeenCalledWith(base.id, true);
    expect(harness.updateCachedSidebarThreadPinned).toHaveBeenCalledWith(base.id, true);
    expect(harness.updateCachedSidebarThread).toHaveBeenCalledWith({
      ...base,
      pinned: true,
    });
    expect(harness.getAppState().thread?.pinned).toBe(true);
    expect(harness.getAppState().threads[0]?.pinned).toBe(true);
  });

  it("optimistically pins without an active workspace context", async () => {
    const base = thread();
    let resolvePin: ((value: { thread: Thread }) => void) | undefined;
    const pinThread = vi.fn().mockReturnValue(
      new Promise<{ thread: Thread }>((resolve) => {
        resolvePin = resolve;
      }),
    );
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { pinThread },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: undefined,
        thread: base,
        threads: [base],
      },
    });

    const pending = harness.actions.toggleThreadPinned(summary(base));

    expect(pinThread).toHaveBeenCalledWith(base.id, true);
    expect(harness.updateCachedSidebarThreadPinned).toHaveBeenCalledWith(base.id, true);
    expect(harness.getAppState().thread?.pinned).toBe(true);
    expect(harness.getAppState().threads[0]?.pinned).toBe(true);

    resolvePin?.({ thread: { ...base, pinned: true } });
    await pending;
  });

  it("rolls back an optimistic pin when persistence fails", async () => {
    const base = thread();
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { pinThread: vi.fn().mockRejectedValue(new Error("pin failed")) },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: undefined,
        thread: base,
        threads: [base],
      },
    });

    await harness.actions.toggleThreadPinned(summary(base));

    expect(harness.updateCachedSidebarThreadPinned).toHaveBeenNthCalledWith(1, base.id, true);
    expect(harness.updateCachedSidebarThreadPinned).toHaveBeenNthCalledWith(2, base.id, false);
    expect(harness.getAppState().thread?.pinned).toBe(false);
    expect(harness.getAppState().threads[0]?.pinned).toBe(false);
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("pin failed");
  });

  it("shows a renamed conversation immediately and keeps the saved title", async () => {
    const base = thread();
    const renamed = { ...base, title: "Release notes", preview: "Release notes" };
    const renameThread = vi.fn().mockResolvedValue({ thread: renamed });
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { renameThread },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    const pending = harness.actions.renameThread(summary(base), "  Release notes  ");

    expect(harness.getAppState().thread?.title).toBe("Release notes");
    await pending;
    expect(renameThread).toHaveBeenCalledWith(base.id, "Release notes");
    expect(harness.updateCachedSidebarThread).toHaveBeenCalledWith(renamed);
    expect(harness.getAppState().thread).toEqual(renamed);
    expect(harness.getAppState().threads[0]).toEqual(renamed);
  });

  it("restores the previous title when renaming fails", async () => {
    const base = thread();
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { renameThread: vi.fn().mockRejectedValue(new Error("rename failed")) },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: projectContext(),
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    await harness.actions.renameThread(summary(base), "Release notes");

    expect(harness.getAppState().thread?.title).toBe(base.title);
    expect(harness.getAppState().threads[0]?.title).toBe(base.title);
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("rename failed");
  });

  it("archives the active thread after confirmation and opens a fallback draft", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    const threadTab = createThreadSessionTab(base, context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        sessionTabs: [threadTab],
        activeSessionTabID: threadSessionTabID(base.id),
        status: "ready",
      },
      activeThreadID: base.id,
    });

    const outcome = await harness.actions.archiveThread(summary(base));
    expect(outcome).toEqual({ ok: true });
    expect(api.archiveThread).toHaveBeenCalledWith(base.id, true);
    expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(base.id);
    expect(harness.updateCachedSidebarThread).not.toHaveBeenCalled();
    expect(harness.clearThreadPendingComposerMessages).toHaveBeenCalledWith(
      base.id,
    );
    expect(harness.clearPrimaryComposerDraft).toHaveBeenCalled();
    expect(harness.resetSplitComposerDrafts).toHaveBeenCalled();
    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:fallback");
  });

  it("allows archiving after crash recovery settles a child agent", async () => {
    const context = projectContext();
    const child = {
      id: "agent-interrupted",
      status: "failed",
    };
    const base = { ...thread(), child_agents: [child] };
    const api = installWuuApi(base);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    const outcome = await harness.actions.archiveThread(summary(base));

    expect(outcome).toEqual({ ok: true });
    expect(api.archiveThread).toHaveBeenCalledWith(base.id, true);
  });

  it("reports a running thread instead of claiming it was archived", async () => {
    const context = projectContext();
    const base = { ...thread(), status: "in_progress" as const };
    const api = installWuuApi(base);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    const outcome = await harness.actions.archiveThread(summary(base));

    expect(outcome).toEqual({
      ok: false,
      error: "会话仍在运行，结束后再归档",
      forceRetryable: true,
    });
    expect(api.archiveThread).not.toHaveBeenCalled();
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
    expect(harness.getAppState().status).toBe("ready");
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("会话仍在运行，结束后再归档");
  });

  it("reports a remotely owned running turn without showing archive success", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    api.archiveThread.mockRejectedValueOnce(
      new Error(
        `thread "${base.id}" already has a running turn in another app-server: thread execution is owned by another app-server`,
      ),
    );
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    const outcome = await harness.actions.archiveThread(summary(base));

    expect(outcome).toEqual({
      ok: false,
      error: "会话仍在运行，结束后再归档",
      forceRetryable: true,
    });
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
    expect(harness.getAppState().status).toBe("ready");
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("会话仍在运行，结束后再归档");
  });

  it("reuses a parked workspace draft when archiving the active thread", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    const threadTab = createThreadSessionTab(base, context);
    const draftTab = createDraftSessionTab("draft:parked", context, {
      prompt: "keep this draft",
      images: [],
      files: [],
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        sessionTabs: [threadTab, draftTab],
        activeSessionTabID: threadTab.id,
        status: "ready",
      },
      activeThreadID: base.id,
    });

    await harness.actions.archiveThread(summary(base));

    expect(api.archiveThread).toHaveBeenCalledWith(base.id, true);
    expect(harness.getAppState().activeSessionTabID).toBe(draftTab.id);
    expect(harness.getAppState().sessionTabs).toHaveLength(1);
    expect(harness.getAppState().sessionTabs[0]).toMatchObject({
      id: draftTab.id,
      prompt: "keep this draft",
    });
  });

  it("deletes a thread and removes it from sidebar caches", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    await harness.actions.deleteThread(summary(base));

    expect(api.deleteThread).toHaveBeenCalledWith(base.id);
    expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(base.id);
    expect(harness.getAppState().threads).toHaveLength(0);
  });

  it("optimistically unarchives before the server confirms", async () => {
    const context = projectContext();
    const archived = { ...thread(), archived: true };
    let resolveUnarchive: ((value: { thread: Thread }) => void) | undefined;
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        archiveThread: vi.fn().mockReturnValue(
          new Promise<{ thread: Thread }>((resolve) => {
            resolveUnarchive = resolve;
          }),
        ),
      },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        threads: [archived],
        status: "ready",
      },
    });

    const pending = harness.actions.unarchiveThread({ id: archived.id });

    expect(harness.updateCachedSidebarThread).toHaveBeenCalledWith({
      ...archived,
      archived: false,
    });
    expect(harness.getAppState().threads[0]?.archived).toBe(false);

    resolveUnarchive?.({ thread: { ...archived, archived: false } });
    await pending;
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
  });

  it("rolls back an optimistic unarchive when persistence fails", async () => {
    const context = projectContext();
    const archived = { ...thread(), archived: true };
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        archiveThread: vi.fn().mockRejectedValue(new Error("unarchive failed")),
      },
    });
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        threads: [archived],
        status: "ready",
      },
    });

    await harness.actions.unarchiveThread({ id: archived.id });

    expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(archived.id);
    expect(harness.getAppState().threads[0]?.archived).toBe(true);
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("unarchive failed");
  });

  it("optimistically hides the archived row and defers pane teardown until confirmation", async () => {
    const context = projectContext();
    const base = thread();
    let resolveArchive: ((value: { thread: Thread }) => void) | undefined;
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        archiveThread: vi.fn().mockReturnValue(
          new Promise<{ thread: Thread }>((resolve) => {
            resolveArchive = resolve;
          }),
        ),
      },
    });
    const threadTab = createThreadSessionTab(base, context);
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        sessionTabs: [threadTab],
        activeSessionTabID: threadTab.id,
        status: "ready",
      },
      activeThreadID: base.id,
    });

    const pending = harness.actions.archiveThread(summary(base));

    expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(base.id);
    expect(harness.getAppState().threads[0]?.archived).toBe(true);
    expect(harness.getAppState().thread?.id).toBe(base.id);
    expect(harness.getAppState().activeSessionTabID).toBe(threadTab.id);

    resolveArchive?.({ thread: { ...base, archived: true } });
    await pending;

    expect(harness.getAppState().thread).toBeUndefined();
    expect(harness.getAppState().activeSessionTabID).toBe("draft:fallback");
  });

  it("rolls back an optimistic archive when the server rejects", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    api.archiveThread.mockRejectedValueOnce(new Error("archive failed"));
    const harness = buildActions({
      initial: {
        ...initialState,
        activeContext: context,
        thread: base,
        threads: [base],
        status: "ready",
      },
    });

    const outcome = await harness.actions.archiveThread(summary(base));

    expect(outcome.ok).toBe(false);
    expect(harness.updateCachedSidebarThread).toHaveBeenCalledWith(base);
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
    expect(harness.getAppState().thread?.id).toBe(base.id);
  });
});
