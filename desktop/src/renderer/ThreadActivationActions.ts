import type { SetStateAction } from "react";
import type { Agent, RuntimeContext, Thread } from "../shared/protocol";
import {
  conversationPaneThreadsByID,
  createThreadSessionTab,
  ensureSessionTab,
  isThreadRunning,
  mergeListedThreads,
  persistComposerDrafts,
  refreshComposerDrafts,
  reconcileResumedThreadTurns,
  requireThread,
  resolveThreadRuntimeContext,
  sameRuntimeContext,
  SCRATCH_PSEUDO_PROJECT_ID,
  sessionTabDraftForThread,
  threadForTab,
  threadNeedsResumeOnReselect,
  threadSessionTabID,
  upsertThread,
  type AppState,
  type ComposerDraftState,
  type ComposerDraftSnapshot,
} from "./AppState";
import {
  loadRuntimeConfiguration as defaultLoadRuntimeConfiguration,
  loadRuntimeThreadList as defaultLoadRuntimeThreadList,
  selectRuntimeContext as defaultSelectRuntimeContext,
} from "./RuntimeLoadState";
import type { PendingViewSwitch } from "./ViewSwitchState";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

type SetAppState = (update: SetStateAction<AppState>) => void;
type SidebarWorkspaceThreads = Record<string, Thread[] | undefined>;

export type ThreadActivationActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getActiveThreadID: () => string | undefined;
  getPendingViewSwitch: () => PendingViewSwitch | undefined;
  getComposerDraftSnapshot: () => ComposerDraftSnapshot;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  resetSplitComposerDrafts: () => void;
  getSidebarThreads: () => Thread[];
  getSidebarWorkspaceThreadsByWorkspaceID: () => SidebarWorkspaceThreads;
  getRunningThreadIDs?: () => ReadonlySet<string>;
  
  beginViewSwitch: (
    kind: "thread" | "workspace" | "runtime",
    targetID: string,
    contextSwitching?: boolean,
  ) => number;
  prepareThreadReveal: (requestID: number) => boolean;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  isCurrentViewSwitchRequest: (requestID: number) => boolean;
  loadRuntimeConfiguration?: typeof defaultLoadRuntimeConfiguration;
  loadRuntimeThreadList?: typeof defaultLoadRuntimeThreadList;
  selectRuntimeContext?: typeof defaultSelectRuntimeContext;
};

export type ThreadActivationActions = {
  selectThread: (threadID: string) => Promise<void>;
  selectContextThread: (context: RuntimeContext, threadID: string) => Promise<void>;
  selectWorkspaceThread: (workspaceID: string, threadID: string) => Promise<void>;
  activateThread: (threadID: string) => Promise<void>;
  selectChildAgent: (agent: Agent) => Promise<void>;
};

export function createThreadActivationActions(
  deps: ThreadActivationActionsDeps,
): ThreadActivationActions {
  const loadRuntimeConfiguration =
    deps.loadRuntimeConfiguration ?? defaultLoadRuntimeConfiguration;
  const loadRuntimeThreadList = deps.loadRuntimeThreadList ?? defaultLoadRuntimeThreadList;
  const selectRuntimeContext =
    deps.selectRuntimeContext ?? defaultSelectRuntimeContext;

  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function currentThreadSnapshot(thread: Thread | undefined): Thread | undefined {
    if (!thread || !deps.getRunningThreadIDs?.().has(thread.id) || isThreadRunning(thread)) {
      return thread;
    }
    return { ...thread, status: "in_progress" };
  }

  async function selectThread(threadID: string): Promise<void> {
    const currentState = deps.getAppState();
    if (!currentState.activeContext) {
      return;
    }
    const activeContext = currentState.activeContext;
    const pendingContextSwitch = deps.getPendingViewSwitch();
    // The visible source is retained while the main process selects another
    // runtime. Returning to it must restore that runtime, not just dismiss UI.
    if (pendingContextSwitch?.contextSwitching && pendingContextSwitch.targetID !== threadID) {
      await selectContextThread(activeContext, threadID);
      return;
    }
    if (
      threadID === deps.getActiveThreadID() &&
      !threadNeedsResumeOnReselect(currentState, threadID)
    ) {
      const pendingViewSwitch = deps.getPendingViewSwitch();
      if (
        pendingViewSwitch?.kind === "thread" &&
        pendingViewSwitch.targetID === threadID
      ) {
        return;
      }
      if (pendingViewSwitch) {
        deps.cancelViewSwitch();
      }
      return;
    }
    const pendingViewSwitch = deps.getPendingViewSwitch();
    if (
      pendingViewSwitch?.kind === "thread" &&
      pendingViewSwitch.targetID === threadID
    ) {
      return;
    }
    
    let outgoingDraft = deps.getComposerDraftSnapshot();
    let targetDraft = sessionTabDraftForThread(persistComposerDrafts(currentState, outgoingDraft), threadID);
    const sourceContext = currentState.activeContext;
    const requestID = deps.beginViewSwitch("thread", threadID);
    try {
      const thread = requireThread(
        await window.wuu.resumeThread(threadID),
        translateCurrent("thread.resumeMissing"),
      );
      if (
        !deps.prepareThreadReveal(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      outgoingDraft = refreshComposerDrafts(outgoingDraft, deps.getComposerDraftSnapshot());
      targetDraft = sessionTabDraftForThread(persistComposerDrafts(deps.getAppState(), outgoingDraft), thread.id);
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistComposerDrafts(current, outgoingDraft);
        const reconciled = reconcileResumedThreadTurns(
          thread,
          findKnownThread(thread.id, current),
        );
        return {
          ...withDraft,
          thread: reconciled,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            withDraft.sessionTabs,
            createThreadSessionTab(reconciled, sourceContext, targetDraft),
          ),
          activeSessionTabID: threadSessionTabID(reconciled.id),
          threads: upsertThread(current.threads, reconciled),
          running: isThreadRunning(reconciled),
          status: "ready",
        };
      });
    } catch (error) {
      if (
        !deps.finishViewSwitch(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
    }
  }

  async function selectContextThread(
    targetContext: RuntimeContext,
    threadID: string,
  ): Promise<void> {
    const currentState = deps.getAppState();
    let outgoingDraft = deps.getComposerDraftSnapshot();
    let targetDraft = sessionTabDraftForThread(persistComposerDrafts(currentState, outgoingDraft), threadID);
    const requestID = deps.beginViewSwitch("thread", threadID, true);
    try {
      const workspaceState = await selectRuntimeContext(targetContext);
      if (!deps.isCurrentViewSwitchRequest(requestID)) {
        return;
      }
      const [loadedState, resumed] = await Promise.all([
        loadRuntimeConfiguration(workspaceState),
        window.wuu.resumeThread(threadID),
      ]);
      const thread = requireThread(
        resumed,
        translateCurrent("thread.resumeMissing"),
      );
      if (!deps.prepareThreadReveal(requestID)) {
        return;
      }
      outgoingDraft = refreshComposerDrafts(outgoingDraft, deps.getComposerDraftSnapshot());
      targetDraft = sessionTabDraftForThread(persistComposerDrafts(deps.getAppState(), outgoingDraft), thread.id);
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistComposerDrafts(current, outgoingDraft);
        const localThread = conversationPaneThreadsByID(
          current.threads,
          current.thread,
          current.secondaryThread,
        ).get(thread.id) ?? findKnownThread(thread.id, current);
        const reconciled = reconcileResumedThreadTurns(thread, localThread);
        const next = {
          ...withDraft,
          ...loadedState,
          threads: withDraft.threads.filter((candidate) =>
            candidate.archived || sameRuntimeContext(
              resolveThreadRuntimeContext(candidate, withDraft.projects), targetContext,
            ),
          ),
        };
        return {
          ...next,
          thread: reconciled,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            next.sessionTabs,
            createThreadSessionTab(reconciled, targetContext, targetDraft),
          ),
          activeSessionTabID: threadSessionTabID(reconciled.id),
          threads: upsertThread(next.threads, reconciled),
          running: isThreadRunning(reconciled),
          status: "ready",
        };
      });
      // Catalogs are not needed to display or send to the resumed conversation.
      // Start them after activation so large archives cannot delay its first paint.
      if (loadedState.activeContext) {
        void refreshThreadCatalog(loadedState.activeContext);
      }
    } catch (error) {
      if (!deps.isCurrentViewSwitchRequest(requestID)) return;
      let failure = error;
      if (currentState.activeContext) {
        try {
          await selectRuntimeContext(currentState.activeContext);
        } catch (restoreError) {
          if (!deps.isCurrentViewSwitchRequest(requestID)) return;
          // The source workspace may have disappeared too. Keep its history,
          // but do not leave a composer attached to another selected runtime.
          deps.setAppState(current => ({
            ...current,
            initialized: undefined,
            activeContext: undefined,
            activeProjectId: undefined,
            gitStatus: undefined,
          }));
          failure = restoreError;
        }
      }
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(failure instanceof Error ? failure.message : translateCurrent("thread.loadFailed"));
    }
  }

  async function refreshThreadCatalog(context: RuntimeContext): Promise<void> {
    const before = new Map(deps.getAppState().threads.map((thread) => [thread.id, thread]));
    try {
      const threads = await loadRuntimeThreadList(context.cwd);
      deps.setAppState((current) => {
        // A same-context thread selection still needs this catalog. Context
        // identity changes on runtime reload, including leaving and returning
        // to the same workspace. Check inside the updater, after activation.
        if (current.activeContext !== context) return current;
        const currentByID = new Map(current.threads.map((thread) => [thread.id, thread]));
        const unchanged = threads.filter((thread) =>
          !before.has(thread.id) || currentByID.has(thread.id),
        );
        const changed = current.threads.filter((thread) => before.get(thread.id) !== thread);
        return {
          ...current,
          // Local archives, new threads, and streaming updates may be newer
          // than the list response. Keep those snapshots and live panes.
          threads: upsertThread(
            upsertThread(mergeListedThreads(current.threads, [...unchanged, ...changed]), current.thread),
            current.secondaryThread,
          ),
        };
      });
    } catch (error) {
      if (deps.getAppState().activeContext === context) {
        setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
      }
    }
  }

  function findKnownThread(
    threadID: string,
    state = deps.getAppState(),
  ): Thread | undefined {
    const candidates = [
      threadForTab(state, threadID),
      deps.getSidebarThreads().find((thread) => thread.id === threadID),
      ...Object.values(deps.getSidebarWorkspaceThreadsByWorkspaceID()).map(
        (threads) => threads?.find((thread) => thread.id === threadID),
      ),
    ];
    // Runtime reloads replace the catalog with summaries. Prefer a loaded
    // snapshot, but keep live pane history ahead of older sidebar caches.
    return currentThreadSnapshot(
      candidates.find((thread) => thread && thread.turns.length > 0) ??
        candidates.find((thread) => thread !== undefined),
    );
  }

  async function selectWorkspaceThread(
    workspaceID: string,
    threadID: string,
  ): Promise<void> {
    const currentState = deps.getAppState();
    if (
      workspaceID === currentState.activeProjectId &&
      currentState.activeContext?.kind === "project"
    ) {
      await selectThread(threadID);
      return;
    }
    const pendingViewSwitch = deps.getPendingViewSwitch();
    if (
      pendingViewSwitch?.kind === "thread" &&
      pendingViewSwitch.targetID === threadID
    ) {
      return;
    }
    if (workspaceID === SCRATCH_PSEUDO_PROJECT_ID) {
      const thread = findKnownThread(threadID);
      if (!thread) {
        return;
      }
      const targetContext = resolveThreadRuntimeContext(
        thread,
        currentState.projects,
      );
      if (sameRuntimeContext(targetContext, currentState.activeContext)) {
        await selectThread(threadID);
        return;
      }
      await selectContextThread(targetContext, threadID);
      return;
    }
    const project = currentState.projects.find(
      (candidate) => candidate.id === workspaceID,
    );
    if (!project) {
      return;
    }
    const targetContext: RuntimeContext = {
      kind: "project",
      project_id: project.id,
      cwd: project.path,
    };
    await selectContextThread(targetContext, threadID);
  }

  async function activateThread(threadID: string): Promise<void> {
    const currentState = deps.getAppState();
    const project = currentState.projects.find((candidate) =>
      deps.getSidebarWorkspaceThreadsByWorkspaceID()[candidate.id]?.some(
        (thread) => thread.id === threadID,
      ),
    );
    if (
      project &&
      (project.id !== currentState.activeProjectId ||
        currentState.activeContext?.kind !== "project")
    ) {
      await selectWorkspaceThread(project.id, threadID);
      return;
    }
    if (!project) {
      const thread = findKnownThread(threadID);
      const targetContext = thread
        ? resolveThreadRuntimeContext(thread, currentState.projects)
        : undefined;
      if (
        targetContext &&
        !sameRuntimeContext(targetContext, currentState.activeContext)
      ) {
        await selectContextThread(targetContext, threadID);
        return;
      }
    }
    await selectThread(threadID);
  }

  async function selectChildAgent(agent: Agent): Promise<void> {
    const currentState = deps.getAppState();
    if (!currentState.activeContext) {
      return;
    }
    if (deps.getPendingViewSwitch()?.contextSwitching) {
      await selectContextThread(currentState.activeContext, agent.id);
      return;
    }
    if (agent.id === deps.getActiveThreadID()) {
      if (deps.getPendingViewSwitch()) {
        deps.cancelViewSwitch();
      }
      return;
    }
    const pendingViewSwitch = deps.getPendingViewSwitch();
    if (
      pendingViewSwitch?.kind === "thread" &&
      pendingViewSwitch.targetID === agent.id
    ) {
      return;
    }
    
    let outgoingDraft = deps.getComposerDraftSnapshot();
    let targetDraft = sessionTabDraftForThread(persistComposerDrafts(currentState, outgoingDraft), agent.id);
    const sourceContext = currentState.activeContext;
    const requestID = deps.beginViewSwitch("thread", agent.id);
    try {
      const thread = requireThread(
        await window.wuu.resumeThread(agent.id),
        translateCurrent("thread.childResumeMissing"),
      );
      if (
        !deps.prepareThreadReveal(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      outgoingDraft = refreshComposerDrafts(outgoingDraft, deps.getComposerDraftSnapshot());
      targetDraft = sessionTabDraftForThread(persistComposerDrafts(deps.getAppState(), outgoingDraft), thread.id);
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistComposerDrafts(current, outgoingDraft);
        const reconciled = reconcileResumedThreadTurns(
          thread,
          findKnownThread(thread.id, current),
        );
        return {
          ...withDraft,
          thread: reconciled,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            withDraft.sessionTabs,
            createThreadSessionTab(reconciled, sourceContext, targetDraft),
          ),
          activeSessionTabID: threadSessionTabID(reconciled.id),
          threads: upsertThread(current.threads, reconciled),
          running: isThreadRunning(reconciled),
          status: "ready",
        };
      });
    } catch (error) {
      if (
        !deps.finishViewSwitch(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("thread.childLoadFailed"),
      );
    }
  }

  return {
    selectThread,
    selectContextThread,
    selectWorkspaceThread,
    activateThread,
    selectChildAgent,
  };
}
