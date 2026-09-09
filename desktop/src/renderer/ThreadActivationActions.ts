import type { SetStateAction } from "react";
import type { Agent, RuntimeContext, Thread } from "../shared/protocol";
import {
  conversationPaneThreadsByID,
  createThreadSessionTab,
  ensureSessionTab,
  isThreadRunning,
  persistActiveSessionTabDraft,
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
} from "./AppState";
import {
  loadRuntime as defaultLoadRuntime,
  selectRuntimeContext as defaultSelectRuntimeContext,
} from "./RuntimeLoadState";
import type { PendingViewSwitch } from "./ViewSwitchState";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

type SetAppState = (update: SetStateAction<AppState>) => void;
type SidebarProjectThreads = Record<string, Thread[] | undefined>;

export type ThreadActivationActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getActiveThreadID: () => string | undefined;
  getPendingViewSwitch: () => PendingViewSwitch | undefined;
  getPrimaryComposerDraft: () => ComposerDraftState;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  resetSplitComposerDrafts: () => void;
  getSidebarThreads: () => Thread[];
  getSidebarProjectThreadsByProjectID: () => SidebarProjectThreads;
  getRunningThreadIDs?: () => ReadonlySet<string>;
  
  beginViewSwitch: (
    kind: "thread" | "project" | "runtime",
    targetID: string,
  ) => number;
  beginInstantThreadSwitch: (targetID?: string) => number;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  isCurrentViewSwitchRequest: (requestID: number) => boolean;
  loadRuntime?: typeof defaultLoadRuntime;
  selectRuntimeContext?: typeof defaultSelectRuntimeContext;
};

export type ThreadActivationActions = {
  selectThread: (threadID: string) => Promise<void>;
  selectProjectThread: (projectID: string, threadID: string) => Promise<void>;
  activateThread: (threadID: string) => Promise<void>;
  selectChildAgent: (agent: Agent) => Promise<void>;
};

export function createThreadActivationActions(
  deps: ThreadActivationActionsDeps,
): ThreadActivationActions {
  const loadRuntime = deps.loadRuntime ?? defaultLoadRuntime;
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
    
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const targetDraft = sessionTabDraftForThread(currentState, threadID);
    const sourceContext = currentState.activeContext;
    const localThread = currentThreadSnapshot(
      threadForTab(deps.getAppState(), threadID),
    );
    const localThreadContext = localThread
      ? resolveThreadRuntimeContext(localThread, deps.getAppState().projects)
      : undefined;
    if (
      localThread &&
      localThread.turns.length > 0 &&
      localThreadContext &&
      sameRuntimeContext(localThreadContext, sourceContext)
    ) {
      const requestID = deps.beginInstantThreadSwitch(threadID);
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const optimisticThread =
          threadForTab(withDraft, threadID) ?? localThread;
        return {
          ...withDraft,
          thread: optimisticThread,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            withDraft.sessionTabs,
            createThreadSessionTab(
              optimisticThread,
              sourceContext,
              targetDraft,
            ),
          ),
          activeSessionTabID: threadSessionTabID(optimisticThread.id),
          threads: upsertThread(withDraft.threads, optimisticThread),
          running: isThreadRunning(optimisticThread),
          status: "ready",
        };
      });
      void (async () => {
        try {
          const resumedThread = requireThread(
            await window.wuu.resumeThread(threadID),
            translateCurrent("thread.resumeMissing"),
          );
          const latestState = deps.getAppState();
          if (
            !deps.isCurrentViewSwitchRequest(requestID) ||
            latestState.thread?.id !== threadID ||
            !sameRuntimeContext(latestState.activeContext, sourceContext)
          ) {
            return;
          }
          deps.finishViewSwitch(requestID);
          deps.setAppState((current) => {
            if (current.thread?.id !== threadID) {
              return current;
            }
            const localThreadForReconcile =
              current.thread?.id === resumedThread.id
                ? current.thread
                : current.threads.find((item) => item.id === resumedThread.id);
            const reconciled = reconcileResumedThreadTurns(
              resumedThread,
              localThreadForReconcile,
            );
            return {
              ...current,
              thread: reconciled,
              sessionTabs: ensureSessionTab(
                current.sessionTabs,
                createThreadSessionTab(
                  reconciled,
                  sourceContext,
                  sessionTabDraftForThread(current, reconciled.id),
                ),
              ),
              activeSessionTabID: threadSessionTabID(reconciled.id),
              threads: upsertThread(current.threads, reconciled),
              running: isThreadRunning(reconciled),
              status: "ready",
            };
          });
        } catch (error) {
          const latestState = deps.getAppState();
          if (
            !deps.isCurrentViewSwitchRequest(requestID) ||
            latestState.thread?.id !== threadID ||
            !sameRuntimeContext(latestState.activeContext, sourceContext)
          ) {
            return;
          }
          deps.finishViewSwitch(requestID);
          deps.setAppState((current) =>
            current.thread?.id === threadID
              ? {
                  ...current,
                  status:
                    error instanceof Error
                      ? error.message
                      : translateCurrent("thread.loadFailed"),
                }
              : current,
          );
        }
      })();
      return;
    }
    const requestID = deps.beginViewSwitch("thread", threadID);
    try {
      const thread = requireThread(
        await window.wuu.resumeThread(threadID),
        translateCurrent("thread.resumeMissing"),
      );
      if (
        !deps.finishViewSwitch(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const reconciled = reconcileResumedThreadTurns(
          thread,
          current.threads.find((item) => item.id === thread.id),
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

  async function switchContextAndResumeThread(
    targetContext: RuntimeContext,
    threadID: string,
  ): Promise<void> {
    const currentState = deps.getAppState();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const targetDraft = sessionTabDraftForThread(currentState, threadID);
    const localThread = currentThreadSnapshot(
      threadForTab(currentState, threadID) ?? findKnownThread(threadID),
    );
    const canSwitchInstantly =
      localThread !== undefined &&
      localThread.turns.length > 0 &&
      sameRuntimeContext(
        resolveThreadRuntimeContext(localThread, currentState.projects),
        targetContext,
      );
    const requestID = canSwitchInstantly
      ? deps.beginInstantThreadSwitch(threadID)
      : deps.beginViewSwitch("thread", threadID);
    if (canSwitchInstantly) {
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const optimisticThread = threadForTab(withDraft, threadID) ?? localThread;
        return {
          ...withDraft,
          activeContext: targetContext,
          activeProjectId:
            targetContext.kind === "project" ? targetContext.project_id : undefined,
          thread: optimisticThread,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: true,
          sessionTabs: ensureSessionTab(
            withDraft.sessionTabs,
            createThreadSessionTab(optimisticThread, targetContext, targetDraft),
          ),
          activeSessionTabID: threadSessionTabID(optimisticThread.id),
          threads: upsertThread(withDraft.threads, optimisticThread),
          running: isThreadRunning(optimisticThread),
          status: "ready",
        };
      });
    }
    try {
      const projectState = await selectRuntimeContext(targetContext);
      const [loadedState, resumed] = await Promise.all([
        loadRuntime(projectState, { resumeLatestThread: false }),
        window.wuu.resumeThread(threadID),
      ]);
      const thread = requireThread(
        resumed,
        translateCurrent("thread.resumeMissing"),
      );
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const localThread = conversationPaneThreadsByID(
          current.threads,
          current.thread,
          current.secondaryThread,
        ).get(thread.id);
        const reconciled = reconcileResumedThreadTurns(thread, localThread);
        const next = { ...withDraft, ...loadedState };
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
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("thread.loadFailed"));
    }
  }

  function findKnownThread(threadID: string): Thread | undefined {
    return currentThreadSnapshot(
      deps.getSidebarThreads().find((thread) => thread.id === threadID),
    );
  }

  async function selectProjectThread(
    projectID: string,
    threadID: string,
  ): Promise<void> {
    const currentState = deps.getAppState();
    if (
      projectID === currentState.activeProjectId &&
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
    if (projectID === SCRATCH_PSEUDO_PROJECT_ID) {
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
      await switchContextAndResumeThread(targetContext, threadID);
      return;
    }
    const project = currentState.projects.find(
      (candidate) => candidate.id === projectID,
    );
    if (!project) {
      return;
    }
    const targetContext: RuntimeContext = {
      kind: "project",
      project_id: project.id,
      cwd: project.path,
    };
    await switchContextAndResumeThread(targetContext, threadID);
  }

  async function activateThread(threadID: string): Promise<void> {
    const currentState = deps.getAppState();
    const project = currentState.projects.find((candidate) =>
      deps.getSidebarProjectThreadsByProjectID()[candidate.id]?.some(
        (thread) => thread.id === threadID,
      ),
    );
    if (
      project &&
      (project.id !== currentState.activeProjectId ||
        currentState.activeContext?.kind !== "project")
    ) {
      await selectProjectThread(project.id, threadID);
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
        await switchContextAndResumeThread(targetContext, threadID);
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
    
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const targetDraft = sessionTabDraftForThread(currentState, agent.id);
    const sourceContext = currentState.activeContext;
    const requestID = deps.beginViewSwitch("thread", agent.id);
    try {
      const thread = requireThread(
        await window.wuu.resumeThread(agent.id),
        translateCurrent("thread.childResumeMissing"),
      );
      if (
        !deps.finishViewSwitch(requestID) ||
        !sameRuntimeContext(deps.getAppState().activeContext, sourceContext)
      ) {
        return;
      }
      deps.restorePrimaryComposerDraft(targetDraft);
      deps.resetSplitComposerDrafts();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        const reconciled = reconcileResumedThreadTurns(
          thread,
          current.threads.find((item) => item.id === thread.id),
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
    selectProjectThread,
    activateThread,
    selectChildAgent,
  };
}
