import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext, Thread } from "../shared/protocol";
import {
  createDraftSessionTab,
  createThreadSessionTab,
  initialState,
  initialSplitComposerDrafts,
  threadSessionTabID,
  type AppState,
  type ComposerDraftState,
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
}: {
  initial: AppState;
}) {
  let appState = initial;
  const clearPrimaryComposerDraft = vi.fn();
  let splitDrafts = initialSplitComposerDrafts();
  const restorePrimaryComposerDraft = vi.fn();
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
    nextDraftSessionTab: (context) =>
      createDraftSessionTab("draft:fallback", context),
    clearPrimaryComposerDraft,
    restorePrimaryComposerDraft,
    getSplitComposerDrafts: () => splitDrafts,
    resetSplitComposerDrafts,
    updateCachedSidebarThread,
    updateCachedSidebarThreadPinned,
    removeCachedSidebarThread,
    clearThreadPendingComposerMessages,
  });

  return {
    actions,
    getAppState: () => appState,
    setAppState: (state: AppState) => { appState = state; },
    setSplitDrafts: (drafts: Record<"primary" | "secondary", ComposerDraftState>) => { splitDrafts = drafts; },
    restorePrimaryComposerDraft,
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
      error: "对话仍在运行，结束后再归档",
      forceRetryable: true,
    });
    expect(api.archiveThread).not.toHaveBeenCalled();
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
    expect(harness.getAppState().status).toBe("ready");
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("对话仍在运行，结束后再归档");
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
      error: "对话仍在运行，结束后再归档",
      forceRetryable: true,
    });
    expect(harness.getAppState().threads[0]?.archived).toBe(false);
    expect(harness.getAppState().status).toBe("ready");
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith("对话仍在运行，结束后再归档");
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

  it("only removes an archived conversation after a guarded server deletion succeeds", async () => {
    const archived = { ...thread("archived"), archived: true };
    const active = thread("active");
    const api = installWuuApi(archived);
    const harness = buildActions({ initial: { ...initialState, activeContext: projectContext(),
      thread: active, threads: [archived, active], status: "ready" } });
    api.deleteThread.mockRejectedValueOnce(new Error("no longer archived"));
    await expect(harness.actions.deleteArchivedThread(archived.id)).rejects.toThrow("no longer archived");
    expect(harness.getAppState().threads).toHaveLength(2);
    expect(harness.removeCachedSidebarThread).not.toHaveBeenCalled();
    await harness.actions.deleteArchivedThread(archived.id);
    expect(api.deleteThread).toHaveBeenLastCalledWith(archived.id, { onlyIfArchived: true });
    expect(harness.getAppState().threads.map(item => item.id)).toEqual([active.id]);
    expect(harness.getAppState().thread?.id).toBe(active.id);
    expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(archived.id);
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


describe("archive draft ownership", () => {
  it("preserves drafts and pending messages when archive is rejected", async () => {
    const context = projectContext();
    const base = thread();
    const api = installWuuApi(base);
    api.archiveThread.mockRejectedValueOnce(new Error("storage unavailable"));
    const harness = buildActions({ initial: {
      ...initialState, activeContext: context, thread: base,
      threads: [base], sessionTabs: [createThreadSessionTab(base, context)],
      activeSessionTabID: threadSessionTabID(base.id),
    } });

    const result = await harness.actions.archiveThread(summary(base));

    expect(result.ok).toBe(false);
    expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    expect(harness.resetSplitComposerDrafts).not.toHaveBeenCalled();
    expect(harness.clearThreadPendingComposerMessages).not.toHaveBeenCalled();
  });

  it("does not clear a newer conversation draft when archive succeeds after navigation", async () => {
    const context = projectContext();
    const base = thread();
    const target = thread("target");
    const api = installWuuApi(base);
    let resolve!: (value: { thread: Thread }) => void;
    api.archiveThread.mockReturnValue(new Promise((complete) => { resolve = complete; }));
    const harness = buildActions({ initial: {
      ...initialState, activeContext: context, thread: base,
      threads: [base, target], sessionTabs: [createThreadSessionTab(base, context)],
      activeSessionTabID: threadSessionTabID(base.id),
    } });
    const pending = harness.actions.archiveThread(summary(base));
    expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    expect(harness.clearThreadPendingComposerMessages).not.toHaveBeenCalled();
    harness.setAppState({ ...harness.getAppState(), thread: target,
      activeSessionTabID: threadSessionTabID(target.id) });
    resolve({ thread: { ...base, archived: true } });
    await pending;

    expect(harness.getAppState().thread).toBe(target);
    expect(harness.getAppState().activeSessionTabID).toBe(threadSessionTabID(target.id));
    expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    expect(harness.resetSplitComposerDrafts).not.toHaveBeenCalled();
    expect(harness.clearThreadPendingComposerMessages).toHaveBeenCalledWith(base.id);
  });
});


describe("confirmed thread removal", () => {
  it.each(["archive", "delete", "deleteArchived"] as const)(
    "%s closes every member of a Fusion split without promoting the deleted Sidekick", async (operation) => {
      const lead = thread("lead");
      const side: Thread = { ...thread("side"), source: "fusion-side", fusion_lead_id: lead.id, session_control: { manager_id: lead.id, manager_name: "Lead", state: "active", revision: 1 } };
      const context = projectContext();
      const api = installWuuApi(lead);
      api.archiveThread.mockResolvedValue({ thread: { ...lead, archived: true }, threads: [lead, side].map(item => ({ ...item, archived: true })) });
      api.deleteThread.mockResolvedValue({ thread_id: lead.id, thread_ids: [lead.id, side.id] });
      const harness = buildActions({ initial: {
        ...initialState, activeContext: context, thread: lead, secondaryThread: side,
        activePane: "secondary", threads: [lead, side],
        activeSessionTabID: threadSessionTabID(side.id),
        sessionTabs: [createThreadSessionTab(lead, context), createThreadSessionTab(side, context)],
      } });
      if (operation === "archive") await harness.actions.archiveThread(summary(lead));
      else if (operation === "delete") await harness.actions.deleteThread(summary(lead));
      else await harness.actions.deleteArchivedThread(lead.id);
      const state = harness.getAppState();
      expect(state.thread).toBeUndefined();
      expect(state.secondaryThread).toBeUndefined();
      expect(state.activeSessionTabID).toBe("draft:fallback");
      expect(state.sessionTabs.map(tab => tab.id)).toEqual(["draft:fallback"]);
      for (const member of [lead, side]) {
        expect(harness.clearThreadPendingComposerMessages).toHaveBeenCalledWith(member.id);
        expect(harness.removeCachedSidebarThread).toHaveBeenCalledWith(member.id);
      }
      expect(state.threads).toEqual(operation === "archive" ? [lead, side].map(item => ({ ...item, archived: true })) : []);
    },
  );

  it("restores both persisted members of an archived Fusion pair", async () => {
    const lead = thread("lead");
    const side = thread("side");
    const api = installWuuApi(lead);
    api.archiveThread.mockResolvedValue({ thread: lead, threads: [lead, side] });
    const harness = buildActions({ initial: { ...initialState, threads: [lead, side].map(item => ({ ...item, archived: true })) } });
    await harness.actions.unarchiveThread(lead);
    expect(harness.getAppState().threads.every(item => !item.archived)).toBe(true);
    expect(harness.updateCachedSidebarThread).toHaveBeenCalledWith(side);
  });

  it("preserves drafts and pending messages when deletion fails", async () => {
    const base = thread();
    const api = installWuuApi(base);
    api.deleteThread.mockRejectedValueOnce(new Error("storage unavailable"));
    const harness = buildActions({ initial: {
      ...initialState, activeContext: projectContext(), thread: base,
      threads: [base], activeSessionTabID: threadSessionTabID(base.id),
    } });

    await harness.actions.deleteThread(summary(base));

    expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    expect(harness.resetSplitComposerDrafts).not.toHaveBeenCalled();
    expect(harness.clearThreadPendingComposerMessages).not.toHaveBeenCalled();
    expect(harness.getAppState().thread).toBe(base);
  });

  it.each(["primary", "secondary"] as const)(
    "preserves the surviving split draft when archiving the %s pane", async (pane) => {
      const primary = thread("primary");
      const secondary = thread("secondary");
      const removed = pane === "primary" ? primary : secondary;
      const retained = pane === "primary" ? secondary : primary;
      const context = projectContext();
      installWuuApi(removed);
      const harness = buildActions({ initial: {
        ...initialState, activeContext: context, thread: primary, secondaryThread: secondary,
        activePane: pane, threads: [primary, secondary],
        activeSessionTabID: threadSessionTabID(removed.id),
        sessionTabs: [createThreadSessionTab(primary, context), createThreadSessionTab(secondary, context)],
      } });
      const drafts = {
        primary: { prompt: "primary draft", images: [], files: [] },
        secondary: { prompt: "secondary draft", images: [], files: [] },
      };
      harness.setSplitDrafts(drafts);

      await harness.actions.archiveThread(summary(removed));

      expect(harness.getAppState().thread).toBe(retained);
      expect(harness.getAppState().secondaryThread).toBeUndefined();
      expect(harness.getAppState().activeSessionTabID).toBe(threadSessionTabID(retained.id));
      expect(harness.restorePrimaryComposerDraft).toHaveBeenCalledWith(
        drafts[pane === "primary" ? "secondary" : "primary"]);
      expect(harness.clearPrimaryComposerDraft).not.toHaveBeenCalled();
    },
  );
});
