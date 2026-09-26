import type { SetStateAction } from "react";
import { arrayMove } from "@dnd-kit/sortable";
import type { Thread } from "../shared/protocol";
import {
  activeSessionTab,
  cloneSessionTabDraft,
  conversationPaneThreadsByID,
  createThreadSessionTab,
  draftSessionTabForContext,
  ensureSessionTab,
  isThreadRunning,
  persistActiveSessionTabDraft,
  reconcileResumedThreadTurns,
  requireThread,
  runtimeContextKey,
  sameRuntimeContext,
  threadNeedsResumeOnReselect,
  threadSessionTabID,
  upsertThread,
  type AppState,
  type ComposerDraftState,
  type SessionTab,
} from "./AppState";
import {
  loadRuntime as defaultLoadRuntime,
  selectRuntimeContext as defaultSelectRuntimeContext,
} from "./RuntimeLoadState";
import { seedDraftRuntimeFromMemory } from "./DraftRuntimeMemory";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";
import { beginSessionSwitch, markSessionSwitch } from "./SessionSwitchPerformance";

type SetAppState = (update: SetStateAction<AppState>) => void;
type ViewSwitchKind = "thread" | "project" | "runtime";
type GlobalSessionTab = Extract<
  SessionTab,
  { kind: "channel-room" | "agents" | "tasks" }
>;

export type SessionTabActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getPrimaryComposerDraft: () => ComposerDraftState;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  clearPrimaryComposerDraft: () => void;
  resetSplitComposerDrafts: () => void;
  // Full conversation snapshots live in the cross-workspace sidebar cache
  // after a runtime switch removes them from AppState. Reading that cache is
  // what lets a tab click paint immediately instead of waiting for the target
  // app-server to resume the thread first.
  getCrossWorkspaceThreads?: () => readonly Thread[];
  getRunningThreadIDs?: () => ReadonlySet<string>;
  nextDraftSessionTab: (
    context: NonNullable<AppState["activeContext"]>,
  ) => SessionTab;
  isDraftPending?: (tabID: string) => boolean;
  selectThread: (threadID: string) => Promise<void>;
  beginViewSwitch: (kind: ViewSwitchKind, targetID: string) => number;
  beginInstantThreadSwitch: (targetID?: string) => number;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  loadRuntime?: typeof defaultLoadRuntime;
  selectRuntimeContext?: typeof defaultSelectRuntimeContext;
};

export type SessionTabActions = {
  openGlobalSessionTab: (tab: GlobalSessionTab) => void;
  selectSessionTab: (tabID: string) => Promise<void>;
  closeSessionTab: (tabID: string) => Promise<void>;
  closeSessionTabs: (tabIDs: string[]) => Promise<void>;
  startNewThread: () => Promise<void>;
  reorderSessionTabs: (activeID: string, overID: string) => void;
  popOutSessionTab: (tabID: string) => Promise<void>;
};

export function createSessionTabActions(
  deps: SessionTabActionsDeps,
): SessionTabActions {
  const loadRuntime = deps.loadRuntime ?? defaultLoadRuntime;
  const selectRuntimeContext =
    deps.selectRuntimeContext ?? defaultSelectRuntimeContext;
  const poppingOutThreadIDs = new Set<string>();

  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function currentThreadSnapshot(thread: Thread | undefined): Thread | undefined {
    if (!thread || !deps.getRunningThreadIDs?.().has(thread.id) || isThreadRunning(thread)) {
      return thread;
    }
    return { ...thread, status: "in_progress" };
  }

  function activateGlobalSessionTab(tab: GlobalSessionTab): void {
    const outgoingDraft = deps.getPrimaryComposerDraft();
    deps.cancelViewSwitch();
    deps.resetSplitComposerDrafts();
    deps.setAppState((current) => {
      const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
      return {
        ...withDraft,
        secondaryThread: undefined,
        activePane: "primary",
        sessionTabs: ensureSessionTab(withDraft.sessionTabs, tab),
        activeSessionTabID: tab.id,
        allowThreadAutoActivation: false,
        running: false,
        status: "ready",
      };
    });
  }

  function openGlobalSessionTab(tab: GlobalSessionTab): void {
    const currentState = deps.getAppState();
    const existing = currentState.sessionTabs.find((candidate) => candidate.id === tab.id);
    const target =
      tab.kind === "channel-room" && existing?.kind === "channel-room"
        ? {
            ...tab,
            prompt: existing.prompt,
            images: existing.images,
            files: existing.files,
          }
        : tab;
    if (currentState.activeSessionTabID === target.id) {
      deps.setAppState((current) => ({
        ...current,
        sessionTabs: ensureSessionTab(current.sessionTabs, target),
      }));
      return;
    }
    activateGlobalSessionTab(target);
  }

  async function selectSessionTab(tabID: string): Promise<void> {
    const currentState = deps.getAppState();
    const tab = currentState.sessionTabs.find((item) => item.id === tabID);
    if (!tab) {
      return;
    }
    if (tabID === currentState.activeSessionTabID) {
      if (
        tab.kind === "thread" &&
        threadNeedsResumeOnReselect(currentState, tab.threadID)
      ) {
        await deps.selectThread(tab.threadID);
      }
      return;
    }

    if (
      tab.kind === "channel-room" ||
      tab.kind === "agents" ||
      tab.kind === "tasks"
    ) {
      activateGlobalSessionTab(tab);
      return;
    }

    const sameContext = sameRuntimeContext(
      tab.context,
      currentState.activeContext,
    );
    if (tab.kind === "skills") {
      const outgoingDraft = deps.getPrimaryComposerDraft();
      const requestID = sameContext
        ? undefined
        : deps.beginViewSwitch("runtime", runtimeContextKey(tab.context));
      try {
        const loadedState = sameContext
          ? undefined
          : await loadRuntime(await selectRuntimeContext(tab.context), {
              resumeLatestThread: false,
            });
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        if (requestID === undefined) {
          deps.cancelViewSwitch();
        }
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const withDraft = persistActiveSessionTabDraft(
            current,
            outgoingDraft,
          );
          const next = loadedState
            ? { ...withDraft, ...loadedState }
            : withDraft;
          return {
            ...next,
            secondaryThread: undefined,
            activePane: "primary",
            sessionTabs: ensureSessionTab(next.sessionTabs, tab),
            activeSessionTabID: tab.id,
            allowThreadAutoActivation: false,
            running: false,
            status: "ready",
          };
        });
      } catch (error) {
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
      }
      return;
    }
    if (tab.kind === "draft") {
      const outgoingDraft = deps.getPrimaryComposerDraft();
      const requestID = sameContext
        ? undefined
        : deps.beginViewSwitch("runtime", runtimeContextKey(tab.context));
      try {
        const loadedState = sameContext
          ? undefined
          : await loadRuntime(await selectRuntimeContext(tab.context), {
              resumeLatestThread: false,
            });
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        if (requestID === undefined) {
          deps.cancelViewSwitch();
        }
        deps.restorePrimaryComposerDraft(cloneSessionTabDraft(tab));
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const withDraft = persistActiveSessionTabDraft(
            current,
            outgoingDraft,
          );
          const next = loadedState
            ? { ...withDraft, ...loadedState }
            : withDraft;
          return {
            ...next,
            thread: undefined,
            secondaryThread: undefined,
            activePane: "primary",
            sessionTabs: ensureSessionTab(next.sessionTabs, tab),
            activeSessionTabID: tab.id,
            allowThreadAutoActivation: false,
            running: false,
            status: "ready",
          };
        });
      } catch (error) {
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
      }
      return;
    }
    if (sameContext) {
      beginSessionSwitch(tab.threadID, "same-runtime");
      await deps.selectThread(tab.threadID);
      return;
    }
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const targetDraft = cloneSessionTabDraft(tab);
    const localThread = currentThreadSnapshot(
      conversationPaneThreadsByID(
        currentState.threads,
        currentState.thread,
        currentState.secondaryThread,
      ).get(tab.threadID) ??
        deps.getCrossWorkspaceThreads?.().find((thread) => thread.id === tab.threadID),
    );
    const canSwitchInstantly = localThread !== undefined && localThread.turns.length > 0;
    const performanceThreadID = tab.threadID;
    beginSessionSwitch(performanceThreadID, "cross-runtime");
    const requestID = canSwitchInstantly
      ? deps.beginInstantThreadSwitch(tab.threadID)
      : deps.beginViewSwitch("thread", tab.threadID);
    if (canSwitchInstantly) {
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const optimisticThread =
          conversationPaneThreadsByID(
            withDraft.threads,
            withDraft.thread,
            withDraft.secondaryThread,
          ).get(tab.threadID) ?? localThread;
        return {
          ...withDraft,
          activeContext: tab.context,
          activeProjectId:
            tab.context.kind === "project" ? tab.context.project_id : undefined,
          thread: optimisticThread,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            withDraft.sessionTabs,
            createThreadSessionTab(optimisticThread, tab.context, targetDraft),
          ),
          activeSessionTabID: threadSessionTabID(optimisticThread.id),
          threads: upsertThread(withDraft.threads, optimisticThread),
          running: isThreadRunning(optimisticThread),
          status: "ready",
        };
      });
      markSessionSwitch(performanceThreadID, "state-update-issued");
    }
    try {
      const projectState = await selectRuntimeContext(tab.context);
      const [loadedState, resumed] = await Promise.all([
        loadRuntime(projectState, { resumeLatestThread: false }),
        window.wuu.resumeThread(tab.threadID),
      ]);
      markSessionSwitch(performanceThreadID, "runtime-loaded");
      const resumedThread = requireThread(
        resumed,
        translateCurrent("thread.resumeMissing"),
      );
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      if (!canSwitchInstantly) {
        deps.restorePrimaryComposerDraft(targetDraft);
        deps.resetSplitComposerDrafts();
      }
      const currentDraft = canSwitchInstantly ? deps.getPrimaryComposerDraft() : targetDraft;
      deps.setAppState((current) => {
        const withDraft = canSwitchInstantly ? current : persistActiveSessionTabDraft(current, outgoingDraft);
        const cachedThread =
          conversationPaneThreadsByID(
            withDraft.threads,
            withDraft.thread,
            withDraft.secondaryThread,
          ).get(resumedThread.id) ?? localThread;
        const thread = reconcileResumedThreadTurns(resumedThread, cachedThread);
        const next = { ...withDraft, ...loadedState };
        return {
          ...next,
          thread,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            next.sessionTabs,
            createThreadSessionTab(thread, tab.context, currentDraft),
          ),
          activeSessionTabID: threadSessionTabID(thread.id),
          threads: upsertThread(next.threads, thread),
          running: isThreadRunning(thread),
          status: "ready",
        };
      });
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
    }
  }

  async function closeSessionTab(tabID: string): Promise<void> {
    const currentState = deps.getAppState();
    const tabIndex = currentState.sessionTabs.findIndex(
      (tab) => tab.id === tabID,
    );
    if (tabIndex < 0) {
      return;
    }
    const closingActive = currentState.activeSessionTabID === tabID;
    const nextTabs = currentState.sessionTabs.filter((tab) => tab.id !== tabID);
    const closedTab = currentState.sessionTabs[tabIndex];
    if (!closingActive) {
      deps.setAppState((current) => ({
        ...current,
        sessionTabs: current.sessionTabs.filter((tab) => tab.id !== tabID),
      }));
      return;
    }

    const fallbackTab =
      nextTabs[Math.min(tabIndex, Math.max(nextTabs.length - 1, 0))] ??
      deps.nextDraftSessionTab(closedTab.context);
    const tabsWithFallback = nextTabs.length > 0 ? nextTabs : [fallbackTab];
    deps.setAppState((current) => ({
      ...current,
      sessionTabs: tabsWithFallback,
      activeSessionTabID: fallbackTab.id,
    }));
    
    if (
      fallbackTab.kind === "channel-room" ||
      fallbackTab.kind === "agents" ||
      fallbackTab.kind === "tasks"
    ) {
      deps.cancelViewSwitch();
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => ({
        ...current,
        sessionTabs: current.sessionTabs,
        activeSessionTabID: fallbackTab.id,
        secondaryThread: undefined,
        activePane: "primary",
        allowThreadAutoActivation: false,
        running: false,
        status: "ready",
      }));
      return;
    }

    if (fallbackTab.kind === "skills") {
      const sameContext = sameRuntimeContext(
        fallbackTab.context,
        currentState.activeContext,
      );
      const requestID = sameContext
        ? undefined
        : deps.beginViewSwitch("runtime", runtimeContextKey(fallbackTab.context));
      try {
        const loadedState = sameContext
          ? undefined
          : await loadRuntime(await selectRuntimeContext(fallbackTab.context), {
              resumeLatestThread: false,
            });
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        if (requestID === undefined) {
          deps.cancelViewSwitch();
        }
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const next = loadedState ? { ...current, ...loadedState } : current;
          return {
            ...next,
            sessionTabs: current.sessionTabs,
            activeSessionTabID: fallbackTab.id,
            secondaryThread: undefined,
            activePane: "primary",
            allowThreadAutoActivation: false,
            running: false,
            status: "ready",
          };
        });
      } catch (error) {
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
      }
      return;
    }
    if (fallbackTab.kind === "draft") {
      const sameContext = sameRuntimeContext(
        fallbackTab.context,
        currentState.activeContext,
      );
      const requestID = sameContext
        ? undefined
        : deps.beginViewSwitch("runtime", runtimeContextKey(fallbackTab.context));
      try {
        const loadedState = sameContext
          ? undefined
          : await loadRuntime(await selectRuntimeContext(fallbackTab.context), {
              resumeLatestThread: false,
            });
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        if (requestID === undefined) {
          deps.cancelViewSwitch();
        }
        deps.restorePrimaryComposerDraft(cloneSessionTabDraft(fallbackTab));
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const next = loadedState ? { ...current, ...loadedState } : current;
          return {
            ...next,
            sessionTabs: current.sessionTabs,
            activeSessionTabID: fallbackTab.id,
            thread: undefined,
            secondaryThread: undefined,
            activePane: "primary",
            allowThreadAutoActivation: false,
            running: false,
            status: "ready",
          };
        });
      } catch (error) {
        if (requestID !== undefined && !deps.finishViewSwitch(requestID)) {
          return;
        }
        setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
      }
      return;
    }

    const restoredDraft = cloneSessionTabDraft(fallbackTab);
    const sameContext = sameRuntimeContext(
      fallbackTab.context,
      currentState.activeContext,
    );
    const requestID = deps.beginViewSwitch("thread", fallbackTab.threadID);
    try {
      const loadedState = sameContext
        ? undefined
        : await loadRuntime(await selectRuntimeContext(fallbackTab.context), {
            resumeLatestThread: false,
          });
      const thread = requireThread(
        await window.wuu.resumeThread(fallbackTab.threadID),
        translateCurrent("thread.resumeMissing"),
      );
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restorePrimaryComposerDraft(restoredDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const next = loadedState ? { ...current, ...loadedState } : current;
        return {
          ...next,
          thread,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            current.sessionTabs,
            createThreadSessionTab(thread, fallbackTab.context, restoredDraft),
          ),
          activeSessionTabID: threadSessionTabID(thread.id),
          threads: upsertThread(next.threads, thread),
          running: isThreadRunning(thread),
          status: "ready",
        };
      });
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
    }
  }

  async function closeSessionTabs(tabIDs: string[]): Promise<void> {
    if (tabIDs.length === 0) {
      return;
    }
    const activeID = deps.getAppState().activeSessionTabID;
    const orderedIDs = tabIDs.includes(activeID)
      ? [...tabIDs.filter((id) => id !== activeID), activeID]
      : tabIDs;
    for (const tabID of orderedIDs) {
      await closeSessionTab(tabID);
    }
  }

  function reorderSessionTabs(activeID: string, overID: string): void {
    deps.setAppState((current) => {
      const sourceIndex = current.sessionTabs.findIndex(
        (tab) => tab.id === activeID,
      );
      const targetIndex = current.sessionTabs.findIndex(
        (tab) => tab.id === overID,
      );
      if (sourceIndex < 0 || targetIndex < 0) {
        return current;
      }
      return {
        ...current,
        sessionTabs: arrayMove(current.sessionTabs, sourceIndex, targetIndex),
      };
    });
  }

  async function popOutSessionTab(tabID: string): Promise<void> {
    const currentState = deps.getAppState();
    const tab = currentState.sessionTabs.find((item) => item.id === tabID);
    if (!tab || (tab.kind !== "thread" && tab.kind !== "draft")) {
      return;
    }
    if (poppingOutThreadIDs.has(tabID)) {
      return;
    }
    poppingOutThreadIDs.add(tabID);
    try {
      await window.wuu.popOutSession(
        tab.kind === "thread"
          ? {
              kind: "thread",
              threadID: tab.threadID,
              context: tab.context,
            }
          : {
              kind: "draft",
              context: tab.context,
            },
      );
      await closeSessionTab(tabID);
    } catch (error) {
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("window.openDetachedFailed"),
      );
    } finally {
      poppingOutThreadIDs.delete(tabID);
    }
  }

  async function startNewThread(): Promise<void> {
    const currentState = deps.getAppState();
    if (!currentState.activeContext) {
      return;
    }
    const existingDraft = draftSessionTabForContext(
      currentState.sessionTabs.filter((tab) => !deps.isDraftPending?.(tab.id)),
      currentState.activeContext,
    );
    if (existingDraft) {
      await selectSessionTab(existingDraft.id);
      return;
    }
    deps.cancelViewSwitch();
    
    const outgoingDraft = deps.getPrimaryComposerDraft();
    deps.clearPrimaryComposerDraft();
    const nextTab =
      activeSessionTab(currentState)?.kind === "draft" &&
      !deps.isDraftPending?.(currentState.activeSessionTabID) &&
      !outgoingDraft.prompt.trim() &&
      outgoingDraft.images.length === 0 &&
      outgoingDraft.files.length === 0
        ? activeSessionTab(currentState)
        : deps.nextDraftSessionTab(currentState.activeContext);
    if (!nextTab) {
      return;
    }
    deps.setAppState((current) => {
      const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
      return seedDraftRuntimeFromMemory({
        ...withDraft,
        thread: undefined,
        secondaryThread: undefined,
        activePane: "primary",
        sessionTabs: ensureSessionTab(withDraft.sessionTabs, nextTab),
        activeSessionTabID: nextTab.id,
        allowThreadAutoActivation: false,
        running: false,
        status: "ready",
      });
    });
  }

  return {
    openGlobalSessionTab,
    selectSessionTab,
    closeSessionTab,
    closeSessionTabs,
    startNewThread,
    reorderSessionTabs,
    popOutSessionTab,
  };
}
