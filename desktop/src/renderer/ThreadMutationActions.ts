import type { SetStateAction } from "react";
import type { Thread } from "../shared/protocol";
import {
  activeThreadIDForState,
  draftSessionTabForContext,
  ensureSessionTab,
  isThreadRunning,
  removeSessionTab,
  threadSessionTabID,
  upsertThread,
  type AppState,
  type SessionTab,
  type ThreadSummary,
} from "./AppState";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

type SetAppState = (update: SetStateAction<AppState>) => void;

function withThreadTitle(
  state: AppState,
  threadID: string,
  title: string | undefined,
): AppState {
  const apply = <T extends { id: string }>(item: T): T =>
    item.id === threadID ? { ...item, title } : item;
  return {
    ...state,
    thread: state.thread ? apply(state.thread) : state.thread,
    secondaryThread: state.secondaryThread ? apply(state.secondaryThread) : state.secondaryThread,
    threads: state.threads.map((item) => apply(item)),
  };
}

export type ThreadMutationActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getActiveThreadID: () => string | undefined;
  nextDraftSessionTab: (context: NonNullable<AppState["activeContext"]>) => SessionTab;
  clearPrimaryComposerDraft: () => void;
  resetSplitComposerDrafts: () => void;
  updateCachedSidebarThread: (thread: Thread) => void;
  updateCachedSidebarThreadPinned: (threadID: string, pinned: boolean) => void;
  removeCachedSidebarThread: (threadID: string) => void;
  clearThreadPendingComposerMessages: (threadID: string) => void;
};

export type ThreadMutationActions = {
  toggleThreadPinned: (thread: ThreadSummary) => Promise<void>;
  renameThread: (thread: Pick<ThreadSummary, "id" | "title">, title: string) => Promise<void>;
  archiveThread: (
    thread: ThreadSummary,
    options?: ThreadArchiveOptions,
  ) => Promise<ThreadArchiveOutcome>;
  unarchiveThread: (thread: Pick<ThreadSummary, "id">) => Promise<void>;
  deleteThread: (thread: ThreadSummary) => Promise<void>;
};

export type ThreadArchiveOptions = {
  // Escape hatch for conversations stuck in a running state (for example a
  // dead turn that never settled): the server interrupts and settles the
  // stuck execution, then archives.
  force?: boolean;
};

export type ThreadArchiveOutcome =
  | { ok: true }
  // forceRetryable marks failures caused by a running-turn rejection, where
  // the UI may offer the force escape hatch.
  | { ok: false; error: string; forceRetryable?: boolean };

function isRunningArchiveRejection(message: string): boolean {
  return (
    message.includes("cannot archive a running thread") ||
    message.includes("already has a running turn") ||
    message.includes("execution is owned by another app-server")
  );
}

function archiveThreadFailure(error: unknown): { message: string; forceRetryable: boolean } {
  const message = error instanceof Error ? error.message : String(error ?? "");
  if (isRunningArchiveRejection(message)) {
    return { message: translateCurrent("thread.archive.running"), forceRetryable: true };
  }
  return {
    message: desktopApiErrorMessage(error, translateCurrent("thread.archive.failed")),
    forceRetryable: false,
  };
}

export function createThreadMutationActions(
  deps: ThreadMutationActionsDeps,
): ThreadMutationActions {
  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function clearActiveComposer(): void {
    deps.clearPrimaryComposerDraft();
    deps.resetSplitComposerDrafts();
  }

  function workspaceDraftOrNew(current: AppState): SessionTab | undefined {
    if (!current.activeContext) {
      return undefined;
    }
    return (
      draftSessionTabForContext(current.sessionTabs, current.activeContext) ??
      deps.nextDraftSessionTab(current.activeContext)
    );
  }

  async function toggleThreadPinned(thread: ThreadSummary): Promise<void> {
    const previousPinned = thread.pinned === true;
    const nextPinned = !previousPinned;
    // Pinning is a lightweight sidebar preference. Reflect it immediately so
    // the row moves between its workspace and the global pinned section on the
    // click that initiated the mutation, regardless of which tab/context is
    // currently active. The server result below remains the persisted source
    // of truth; failures restore the previous value.
    deps.updateCachedSidebarThreadPinned(thread.id, nextPinned);
    deps.setAppState((current) => ({
      ...current,
      thread:
        current.thread?.id === thread.id
          ? { ...current.thread, pinned: nextPinned }
          : current.thread,
      secondaryThread:
        current.secondaryThread?.id === thread.id
          ? { ...current.secondaryThread, pinned: nextPinned }
          : current.secondaryThread,
      threads: current.threads.map((item) =>
        item.id === thread.id ? { ...item, pinned: nextPinned } : item,
      ),
    }));
    try {
      const result = await window.wuu.pinThread(thread.id, nextPinned);
      deps.updateCachedSidebarThread(result.thread);
      deps.setAppState((current) => ({
        ...current,
        thread:
          current.thread?.id === thread.id ? result.thread : current.thread,
        secondaryThread:
          current.secondaryThread?.id === thread.id
            ? result.thread
            : current.secondaryThread,
        threads: current.threads.map((item) =>
          item.id === result.thread.id ? result.thread : item,
        ),
        status: current.status === "ready" ? "ready" : current.status,
      }));
    } catch (error) {
      deps.updateCachedSidebarThreadPinned(thread.id, previousPinned);
      deps.setAppState((current) => ({
        ...current,
        thread:
          current.thread?.id === thread.id
            ? { ...current.thread, pinned: previousPinned }
            : current.thread,
        secondaryThread:
          current.secondaryThread?.id === thread.id
            ? { ...current.secondaryThread, pinned: previousPinned }
            : current.secondaryThread,
        threads: current.threads.map((item) =>
          item.id === thread.id ? { ...item, pinned: previousPinned } : item,
        ),
      }));
      setStatus(
        error instanceof Error ? error.message : translateCurrent("thread.pinFailed"),
      );
    }
  }

  async function renameThread(
    thread: Pick<ThreadSummary, "id" | "title">,
    title: string,
  ): Promise<void> {
    const trimmed = title.trim();
    if (!trimmed) {
      return;
    }
    const previousTitle = thread.title;
    deps.setAppState((current) => withThreadTitle(current, thread.id, trimmed));
    try {
      const result = await window.wuu.renameThread(thread.id, trimmed);
      deps.updateCachedSidebarThread(result.thread);
      deps.setAppState((current) => ({
        ...current,
        thread:
          current.thread?.id === thread.id ? result.thread : current.thread,
        secondaryThread:
          current.secondaryThread?.id === thread.id
            ? result.thread
            : current.secondaryThread,
        threads:
          current.threads.some((item) => item.id === result.thread.id) ||
          current.activeContext?.cwd === result.thread.cwd
            ? upsertThread(current.threads, result.thread)
            : current.threads,
        status: current.status === "ready" ? "ready" : current.status,
      }));
    } catch (error) {
      deps.setAppState((current) => {
        const visible = current.thread?.id === thread.id
          ? current.thread
          : current.threads.find((item) => item.id === thread.id);
        if (visible?.title !== trimmed) {
          return current;
        }
        return withThreadTitle(current, thread.id, previousTitle);
      });
      setStatus(desktopApiErrorMessage(error, translateCurrent("thread.rename.failed")));
    }
  }

  async function archiveThread(
    thread: ThreadSummary,
    options?: ThreadArchiveOptions,
  ): Promise<ThreadArchiveOutcome> {
    const currentState = deps.getAppState();
    if (!currentState.activeContext) {
      const error = translateCurrent("thread.archive.noWorkspace");
      setStatus(error);
      return { ok: false, error };
    }
    const force = options?.force === true;
    if (!force && isThreadRunning(thread)) {
      const error = translateCurrent("thread.archive.running");
      setStatus(error);
      return { ok: false, error, forceRetryable: true };
    }
    deps.clearThreadPendingComposerMessages(thread.id);
    const archivedActiveThread = thread.id === deps.getActiveThreadID();
    const fallbackDraft = archivedActiveThread
      ? workspaceDraftOrNew(currentState)
      : undefined;
    if (archivedActiveThread) {
      clearActiveComposer();
    }
    // Reflect the archive in the visible lists on the click: the sidebar row
    // disappears and Settings → Archive gains the entry immediately. Pane and
    // session-tab teardown stays on the server confirmation so a rejection
    // never has to rebuild an open conversation; failures flip the row back.
    const previousThread = currentState.threads.find(
      (candidate) => candidate.id === thread.id,
    );
    deps.removeCachedSidebarThread(thread.id);
    deps.setAppState((current) => ({
      ...current,
      threads: current.threads.map((candidate) =>
        candidate.id === thread.id ? { ...candidate, archived: true } : candidate,
      ),
    }));
    try {
      const result = force
        ? await window.wuu.archiveThread(thread.id, true, true)
        : await window.wuu.archiveThread(thread.id, true);
      // Archived conversations remain in AppState for Settings → Archive, and
      // the optimistic flip already dropped the sidebar row; the confirmation
      // only needs to tear down the panes/tabs that still show the thread.
      deps.setAppState((current) => {
        const nextTabs = removeSessionTab(
          current.sessionTabs,
          threadSessionTabID(thread.id),
        );
        return archiveMarkThreadState(
          current,
          result.thread.id,
          true,
          nextTabs,
          fallbackDraft,
        );
      });
      return { ok: true };
    } catch (error) {
      if (previousThread) {
        deps.updateCachedSidebarThread(previousThread);
      }
      deps.setAppState((current) => ({
        ...current,
        threads: current.threads.map((candidate) =>
          candidate.id === thread.id
            ? { ...candidate, archived: false }
            : candidate,
        ),
      }));
      const failure = archiveThreadFailure(error);
      setStatus(failure.message);
      return { ok: false, error: failure.message, forceRetryable: failure.forceRetryable };
    }
  }

  async function unarchiveThread(thread: Pick<ThreadSummary, "id">): Promise<void> {
    // Un-archiving is optimistic: the Settings → Archive row leaves the list
    // and the thread rejoins the sidebar on the click, before the server
    // round-trip. The server response stays the persisted source of truth;
    // failures roll the row back into the archive list.
    const previousThread = deps
      .getAppState()
      .threads.find((candidate) => candidate.id === thread.id);
    const markThread = (archived: boolean, candidate: Thread): Thread =>
      candidate.id === thread.id ? { ...candidate, archived } : candidate;
    if (previousThread) {
      deps.updateCachedSidebarThread({ ...previousThread, archived: false });
    }
    deps.setAppState((current) => ({
      ...current,
      thread:
        current.thread?.id === thread.id
          ? { ...current.thread, archived: false }
          : current.thread,
      secondaryThread:
        current.secondaryThread?.id === thread.id
          ? { ...current.secondaryThread, archived: false }
          : current.secondaryThread,
      threads: current.threads.map((candidate) => markThread(false, candidate)),
      status: current.status === "ready" ? "ready" : current.status,
    }));
    try {
      const result = await window.wuu.archiveThread(thread.id, false);
      deps.updateCachedSidebarThread(result.thread);
      deps.setAppState((current) => ({
        ...current,
        thread:
          current.thread?.id === thread.id ? result.thread : current.thread,
        secondaryThread:
          current.secondaryThread?.id === thread.id
            ? result.thread
            : current.secondaryThread,
        threads: upsertThread(current.threads, result.thread),
        status: current.status === "ready" ? "ready" : current.status,
      }));
    } catch (error) {
      if (previousThread) {
        deps.removeCachedSidebarThread(thread.id);
      }
      deps.setAppState((current) => ({
        ...current,
        thread:
          current.thread?.id === thread.id
            ? { ...current.thread, archived: true }
            : current.thread,
        secondaryThread:
          current.secondaryThread?.id === thread.id
            ? { ...current.secondaryThread, archived: true }
            : current.secondaryThread,
        threads: current.threads.map((candidate) => markThread(true, candidate)),
        status: current.status === "ready" ? "ready" : current.status,
      }));
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("thread.unarchiveFailed"),
      );
    }
  }

  async function deleteThread(thread: ThreadSummary): Promise<void> {
    const currentState = deps.getAppState();
    if (
      !currentState.activeContext ||
      isThreadRunning(thread)
    ) {
      return;
    }
    deps.clearThreadPendingComposerMessages(thread.id);
    const deletedActiveThread = thread.id === deps.getActiveThreadID();
    const fallbackDraft = deletedActiveThread
      ? workspaceDraftOrNew(currentState)
      : undefined;
    if (deletedActiveThread) {
      clearActiveComposer();
    }
    try {
      await window.wuu.deleteThread(thread.id);
      deps.removeCachedSidebarThread(thread.id);
      deps.setAppState((current) => {
        const nextTabs = removeSessionTab(
          current.sessionTabs,
          threadSessionTabID(thread.id),
        );
        return archiveMarkThreadState(current, thread.id, false, nextTabs, fallbackDraft);
      });
    } catch (error) {
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("thread.deleteFailed"),
      );
    }
  }

  function archiveMarkThreadState(
    current: AppState,
    threadID: string,
    archived: boolean,
    nextTabs: AppState["sessionTabs"],
    fallbackDraft: SessionTab | undefined,
  ): AppState {
    return {
      ...current,
      thread: current.thread?.id === threadID ? undefined : current.thread,
      secondaryThread:
        current.secondaryThread?.id === threadID
          ? undefined
          : current.secondaryThread,
      activePane:
        current.activePane === "secondary" &&
        current.secondaryThread?.id === threadID
          ? "primary"
          : current.activePane,
      sessionTabs: fallbackDraft ? ensureSessionTab(nextTabs, fallbackDraft) : nextTabs,
      activeSessionTabID:
        current.activeSessionTabID === threadSessionTabID(threadID) &&
        fallbackDraft
          ? fallbackDraft.id
          : current.activeSessionTabID,
      // Keep the thread in `threads` when archiving so the Settings → Archive
      // page can list every archived session; the sidebar already filters by
      // `!archived`. Deletion (`archived === false` from deleteThread) still
      // drops the record entirely.
      threads:
        archived === false
          ? current.threads.filter((candidate) => candidate.id !== threadID)
          : current.threads.map((candidate) =>
              candidate.id === threadID
                ? { ...candidate, archived: true }
                : candidate,
            ),
      running: activeThreadIDForState(current) === threadID ? false : current.running,
      status: "ready",
    };
  }

  return {
    toggleThreadPinned,
    renameThread,
    archiveThread,
    unarchiveThread,
    deleteThread,
  };
}
