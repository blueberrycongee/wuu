import type { SetStateAction } from "react";
import { arrayMove } from "@dnd-kit/sortable";
import type { RuntimeContext } from "../shared/protocol";
import {
  activeSessionTab,
  cloneSessionTabDraft,
  createThreadSessionTab,
  draftSessionTabForContext,
  ensureSessionTab,
  isThreadRunning,
  persistComposerDrafts,
  refreshComposerDrafts,
  requireThread,
  runtimeContextKey,
  sameRuntimeContext,
  threadNeedsResumeOnReselect,
  threadSessionTabID,
  upsertThread,
  type AppState,
  type ComposerDraftState,
  type ComposerDraftSnapshot,
  type SessionTab,
} from "./AppState";
import {
  loadRuntime as defaultLoadRuntime,
  selectRuntimeContext as defaultSelectRuntimeContext,
} from "./RuntimeLoadState";
import { seedDraftRuntimeFromMemory } from "./DraftRuntimeMemory";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";
import { beginSessionSwitch } from "./SessionSwitchPerformance";
import type { PendingViewSwitch } from "./ViewSwitchState";

type SetAppState = (update: SetStateAction<AppState>) => void;
type ViewSwitchKind = "thread" | "workspace" | "runtime";

export type SessionTabActionsDeps = {
  getAppState: () => AppState;
  getPendingViewSwitch: () => PendingViewSwitch | undefined;
  setAppState: SetAppState;
  getComposerDraftSnapshot: () => ComposerDraftSnapshot;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  clearPrimaryComposerDraft: () => void;
  resetSplitComposerDrafts: () => void;
  nextDraftSessionTab: (
    context: NonNullable<AppState["activeContext"]>,
  ) => SessionTab;
  isDraftPending?: (tabID: string) => boolean;
  selectThread: (threadID: string) => Promise<void>;
  beginViewSwitch: (kind: ViewSwitchKind, targetID: string, contextSwitching?: boolean) => number;
  selectContextThread: (context: RuntimeContext, threadID: string) => Promise<void>;
  prepareThreadReveal: (requestID: number) => boolean;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  loadRuntime?: typeof defaultLoadRuntime;
  selectRuntimeContext?: typeof defaultSelectRuntimeContext;
};

export type SessionTabActions = {
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

  async function selectSessionTab(tabID: string): Promise<void> {
    const currentState = deps.getAppState();
    const tab = currentState.sessionTabs.find((item) => item.id === tabID);
    if (!tab) {
      return;
    }
    if (tabID === currentState.activeSessionTabID && !deps.getPendingViewSwitch()) {
      if (
        tab.kind === "thread" &&
        threadNeedsResumeOnReselect(currentState, tab.threadID)
      ) {
        await deps.selectThread(tab.threadID);
      }
      return;
    }

    const sameContext = sameRuntimeContext(
      tab.context,
      currentState.activeContext,
    ) && !deps.getPendingViewSwitch()?.contextSwitching;
    if (tab.kind === "skills") {
      let outgoingDraft = deps.getComposerDraftSnapshot();
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
        outgoingDraft = refreshComposerDrafts(outgoingDraft, deps.getComposerDraftSnapshot());
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const withDraft = persistComposerDrafts(
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
      let outgoingDraft = deps.getComposerDraftSnapshot();
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
        outgoingDraft = refreshComposerDrafts(outgoingDraft, deps.getComposerDraftSnapshot());
        const targetTab = deps.getAppState().sessionTabs.find(item => item.id === tab.id) ?? tab;
        deps.restorePrimaryComposerDraft(cloneSessionTabDraft(targetTab));
        deps.resetSplitComposerDrafts();
        deps.setAppState((current) => {
          const withDraft = persistComposerDrafts(
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
            sessionTabs: ensureSessionTab(next.sessionTabs, targetTab),
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
    beginSessionSwitch(tab.threadID, "cross-runtime");
    await deps.selectContextThread(tab.context, tab.threadID);
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
    
    if (fallbackTab.kind === "skills") {
      const sameContext = sameRuntimeContext(
        fallbackTab.context,
        currentState.activeContext,
      ) && !deps.getPendingViewSwitch()?.contextSwitching;
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
      ) && !deps.getPendingViewSwitch()?.contextSwitching;
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
    ) && !deps.getPendingViewSwitch()?.contextSwitching;
    const requestID = deps.beginViewSwitch("thread", fallbackTab.threadID, !sameContext);
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
      if (!deps.prepareThreadReveal(requestID)) {
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
    
    const outgoingDraft = deps.getComposerDraftSnapshot();
    deps.clearPrimaryComposerDraft();
    const nextTab =
      activeSessionTab(currentState)?.kind === "draft" &&
      !deps.isDraftPending?.(currentState.activeSessionTabID) &&
      !outgoingDraft.activeDraft.prompt.trim() &&
      outgoingDraft.activeDraft.images.length === 0 &&
      outgoingDraft.activeDraft.files.length === 0
        ? activeSessionTab(currentState)
        : deps.nextDraftSessionTab(currentState.activeContext);
    if (!nextTab) {
      return;
    }
    deps.resetSplitComposerDrafts();
    deps.setAppState((current) => {
      const withDraft = persistComposerDrafts(current, outgoingDraft);
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
    selectSessionTab,
    closeSessionTab,
    closeSessionTabs,
    startNewThread,
    reorderSessionTabs,
    popOutSessionTab,
  };
}
