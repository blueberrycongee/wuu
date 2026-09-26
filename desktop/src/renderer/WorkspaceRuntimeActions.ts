import type { SetStateAction } from "react";
import type { ProjectListResult } from "../shared/protocol";
import {
  activeSessionTab,
  applyLoadedRuntimeWithDraftCarry,
  cloneSessionTabDraft,
  composerDraftHasContent,
  draftSessionTabForContext,
  ensureSessionTab,
  persistActiveSessionTabDraft,
  sameRuntimeContext,
  withLoadedRuntimeSessionTab,
  type AppState,
  type ComposerDraftState,
  type SessionTab,
} from "./AppState";
import { seedDraftRuntimeFromMemory } from "./DraftRuntimeMemory";
import { loadRuntime as defaultLoadRuntime } from "./RuntimeLoadState";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type WorkspaceRuntimeActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getPrimaryComposerDraft: () => ComposerDraftState;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  clearPrimaryComposerDraft: () => void;
  restoreLoadedRuntimeComposerDraft: (
    loadedState: Partial<AppState>,
    carryDraft?: ComposerDraftState,
  ) => void;
  nextDraftSessionTab: (context: NonNullable<AppState["activeContext"]>) => SessionTab;
  isDraftPending?: (tabID: string) => boolean;
  closeWorkspaceMenus: () => void;
  
  beginViewSwitch: (kind: "thread" | "workspace" | "runtime", targetID: string) => number;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  loadRuntime?: typeof defaultLoadRuntime;
};

export type WorkspaceRuntimeActions = {
  selectWorkspaceForNewThread: (projectId: string) => Promise<void>;
  startNewThreadInWorkspace: (projectId: string) => Promise<boolean>;
  createBlankProject: () => Promise<void>;
  chooseProjectFolder: () => Promise<void>;
  removeProject: (projectId: string) => Promise<void>;
  relocateProject: (projectId: string) => Promise<void>;
  useNoProject: (fresh: boolean) => Promise<boolean>;
};

export function createWorkspaceRuntimeActions(
  deps: WorkspaceRuntimeActionsDeps,
): WorkspaceRuntimeActions {
  const loadRuntime = deps.loadRuntime ?? defaultLoadRuntime;

  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function withoutWorkspaceSessionTabs(state: AppState, projectId: string): AppState {
    return {
      ...state,
      sessionTabs: state.sessionTabs.filter(
        (tab) =>
          tab.context.kind !== "project" ||
          tab.context.project_id !== projectId,
      ),
    };
  }

  function activateWorkspaceDraft(
    context: NonNullable<AppState["activeContext"]>,
  ): void {
    const draft = deps.getPrimaryComposerDraft();
    const currentState = deps.getAppState();
    const existingDraft = draftSessionTabForContext(
      currentState.sessionTabs,
      context,
    );
    if (existingDraft && !deps.isDraftPending?.(existingDraft.id)) {
      if (existingDraft.id === currentState.activeSessionTabID) {
        return;
      }
      deps.restorePrimaryComposerDraft(cloneSessionTabDraft(existingDraft));
      deps.setAppState((current) => ({
        ...persistActiveSessionTabDraft(current, draft),
        thread: undefined,
        secondaryThread: undefined,
        activePane: "primary",
        activeSessionTabID: existingDraft.id,
        allowThreadAutoActivation: false,
        running: false,
        status: "ready",
      }));
      return;
    }
    const nextTab = deps.nextDraftSessionTab(context);
    deps.clearPrimaryComposerDraft();
    deps.setAppState((current) => {
      const withDraft = persistActiveSessionTabDraft(current, draft);
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

  /**
   * The composer's workspace picker has one mental model regardless of the
   * destination: "retarget the conversation I'm drafting at that
   * context". Selecting a workspace and selecting 不使用工作区 are the same
   * gesture, so they share this implementation:
   *
   *   - re-selecting the current context returns to its draft page when
   *     a conversation is on screen (and is otherwise a no-op);
   *   - the switch lands on the destination context's draft tab — it
   *     never resumes an existing conversation, because the user is
   *     holding a draft, not asking to reopen history;
   *   - an in-progress draft travels with the user (see
   *     applyLoadedRuntimeWithDraftCarry).
   */
  async function retargetDraftToContext({
    switchKind,
    switchTarget,
    isCurrentContext,
    selectContext,
    failureStatus,
  }: {
    switchKind: "workspace" | "runtime";
    switchTarget: string;
    isCurrentContext: (state: AppState) => boolean;
    selectContext: () => Promise<ProjectListResult>;
    failureStatus: string;
  }): Promise<boolean> {
    const currentState = deps.getAppState();
    if (isCurrentContext(currentState)) {
      deps.closeWorkspaceMenus();
      const context = currentState.activeContext;
      if (context && (currentState.thread || currentState.secondaryThread)) {
        activateWorkspaceDraft(context);
      }
      return true;
    }
    // Project navigation is independent from task execution. The main process
    // pools app-server clients by workdir and keeps busy clients alive, so a
    // running thread in the source context must not lock this draft in place.
    const requestID = deps.beginViewSwitch(switchKind, switchTarget);
    deps.closeWorkspaceMenus();

    const outgoingDraft = deps.getPrimaryComposerDraft();
    const carryDraft =
      activeSessionTab(currentState)?.kind === "draft" &&
      composerDraftHasContent(outgoingDraft)
        ? outgoingDraft
        : undefined;
    try {
      const workspaceState = await selectContext();
      const loadedState = await loadRuntime(workspaceState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      deps.restoreLoadedRuntimeComposerDraft(loadedState, carryDraft);
      deps.setAppState((current) => {
        const next = applyLoadedRuntimeWithDraftCarry(
          current,
          loadedState,
          outgoingDraft,
        );
        return {
          ...next,
          thread: undefined,
          secondaryThread: undefined,
          activePane: "primary",
          allowThreadAutoActivation: false,
          running: false,
          status: "ready",
        };
      });
      return true;
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      setStatus(error instanceof Error ? error.message : failureStatus);
      return false;
    }
  }

  async function selectWorkspaceForNewThread(projectId: string): Promise<void> {
    await retargetDraftToContext({
      switchKind: "workspace",
      switchTarget: projectId,
      isCurrentContext: (state) =>
        projectId === state.activeProjectId &&
        state.activeContext?.kind === "project",
      selectContext: () => window.wuu.selectProject(projectId),
      failureStatus: translateCurrent("workspace.openFailed"),
    });
  }

  async function startNewThreadInWorkspace(projectId: string): Promise<boolean> {
    deps.cancelViewSwitch();
    deps.closeWorkspaceMenus();
    
    const currentState = deps.getAppState();
    if (
      projectId === currentState.activeProjectId &&
      currentState.activeContext?.kind === "project"
    ) {
      activateWorkspaceDraft(currentState.activeContext);
      return true;
    }
    const requestID = deps.beginViewSwitch("workspace", projectId);
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const workspaceState = await window.wuu.selectProject(projectId);
      const loadedState = await loadRuntime(workspaceState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      if (!loadedState.activeContext) {
        return false;
      }
      const existingDraft = draftSessionTabForContext(
        currentState.sessionTabs,
        loadedState.activeContext,
      );
      if (existingDraft && !deps.isDraftPending?.(existingDraft.id)) {
        deps.restoreLoadedRuntimeComposerDraft(loadedState);
        deps.setAppState((current) => {
          const next = withLoadedRuntimeSessionTab(
            persistActiveSessionTabDraft(current, outgoingDraft),
            loadedState,
          );
          return {
            ...next,
            thread: undefined,
            secondaryThread: undefined,
            activePane: "primary",
            allowThreadAutoActivation: false,
            running: false,
            status: "ready",
          };
        });
        return true;
      }
      deps.clearPrimaryComposerDraft();
      const nextTab = deps.nextDraftSessionTab(loadedState.activeContext);
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        return {
          ...withDraft,
          ...loadedState,
          thread: undefined,
          secondaryThread: undefined,
          activePane: "primary",
          sessionTabs: ensureSessionTab(withDraft.sessionTabs, nextTab),
          activeSessionTabID: nextTab.id,
          allowThreadAutoActivation: false,
          running: false,
          status: "ready",
        };
      });
      return true;
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("workspace.openFailed"));
      return false;
    }
  }

  async function createBlankProject(): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "create-project");
    deps.closeWorkspaceMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const workspaceState = await window.wuu.createBlankProject();
      if (sameRuntimeContext(workspaceState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: workspaceState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(workspaceState);
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restoreLoadedRuntimeComposerDraft(loadedState);
      deps.setAppState((current) =>
        withLoadedRuntimeSessionTab(
          persistActiveSessionTabDraft(current, outgoingDraft),
          loadedState,
        ),
      );
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("workspace.createFailed"));
    }
  }

  async function chooseProjectFolder(): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "choose-project");
    deps.closeWorkspaceMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const workspaceState = await window.wuu.chooseProjectFolder();
      if (sameRuntimeContext(workspaceState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: workspaceState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(workspaceState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.clearPrimaryComposerDraft();
      deps.setAppState((current) => {
        const persisted = persistActiveSessionTabDraft(current, outgoingDraft);
        const destinationWorkspaceID =
          loadedState.activeContext?.kind === "project"
            ? loadedState.activeContext.project_id
            : undefined;
        return withLoadedRuntimeSessionTab(
          destinationWorkspaceID
            ? withoutWorkspaceSessionTabs(persisted, destinationWorkspaceID)
            : persisted,
          loadedState,
        );
      });
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("workspace.folderOpenFailed"));
    }
  }

  async function removeProject(projectId: string): Promise<void> {
    const currentState = deps.getAppState();
    const removedWorkspace = currentState.projects.find(
      (project) => project.id === projectId,
    );
    if (
      !removedWorkspace ||
      !window.confirm(
        translateCurrent("workspace.removeConfirm", { name: removedWorkspace.name }),
      )
    ) {
      return;
    }
    const requestID = deps.beginViewSwitch("runtime", "remove-project");
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const workspaceState = await window.wuu.removeProject(projectId);
      if (sameRuntimeContext(workspaceState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...withoutWorkspaceSessionTabs(current, projectId),
          projects: workspaceState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(workspaceState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restoreLoadedRuntimeComposerDraft(loadedState);
      deps.setAppState((current) =>
        withLoadedRuntimeSessionTab(
          withoutWorkspaceSessionTabs(
            persistActiveSessionTabDraft(current, outgoingDraft),
            projectId,
          ),
          loadedState,
        ),
      );
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("workspace.removeFailed"));
    }
  }

  async function relocateProject(projectId: string): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "relocate-project");
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const previousCwd = currentState.activeContext?.cwd;
    const wasActive = currentState.activeProjectId === projectId;
    try {
      const workspaceState = await window.wuu.relocateProject(projectId);
      const newCwd = workspaceState.active_context?.cwd;
      if (!wasActive || newCwd === previousCwd) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: workspaceState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(workspaceState);
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restoreLoadedRuntimeComposerDraft(loadedState);
      deps.setAppState((current) =>
        withLoadedRuntimeSessionTab(
          persistActiveSessionTabDraft(current, outgoingDraft),
          loadedState,
        ),
      );
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("workspace.relocateFailed"),
      );
    }
  }

  async function useNoProject(fresh: boolean): Promise<boolean> {
    // The non-fresh flavor is the composer picker's 不使用工作区 entry —
    // the same "retarget my draft" gesture as picking a workspace, so it
    // shares that path (land on the 对话 draft page, never resume an
    // old conversation). The fresh flavor below is the sidebar's 新对话
    // button: an explicit "start clean" that discards nothing but also
    // carries nothing.
    if (!fresh) {
      return retargetDraftToContext({
        switchKind: "runtime",
        switchTarget: "no-project",
        isCurrentContext: (state) => state.activeContext?.kind === "no_project",
        selectContext: () => window.wuu.selectNoProject(false),
        failureStatus: translateCurrent("workspace.scratchOpenFailed"),
      });
    }
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "no-project:fresh");
    deps.closeWorkspaceMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const workspaceState = await window.wuu.selectNoProject(true);
      const loadedState = await loadRuntime(workspaceState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      if (!loadedState.activeContext) {
        return false;
      }
      const existingDraft = draftSessionTabForContext(
        currentState.sessionTabs,
        loadedState.activeContext,
      );
      if (existingDraft && !deps.isDraftPending?.(existingDraft.id)) {
        deps.restoreLoadedRuntimeComposerDraft(loadedState);
        deps.setAppState((current) =>
          withLoadedRuntimeSessionTab(
            persistActiveSessionTabDraft(current, outgoingDraft),
            loadedState,
          ),
        );
        return true;
      }
      const nextTab = deps.nextDraftSessionTab(loadedState.activeContext);
      deps.clearPrimaryComposerDraft();
      deps.setAppState((current) => {
        const withDraft = persistActiveSessionTabDraft(current, outgoingDraft);
        return {
          ...withDraft,
          ...loadedState,
          thread: undefined,
          secondaryThread: undefined,
          activePane: "primary",
          sessionTabs: ensureSessionTab(withDraft.sessionTabs, nextTab),
          activeSessionTabID: nextTab.id,
          allowThreadAutoActivation: false,
          running: false,
        };
      });
      return true;
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return false;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("workspace.scratchOpenFailed"));
      return false;
    }
  }

  return {
    selectWorkspaceForNewThread,
    startNewThreadInWorkspace,
    createBlankProject,
    chooseProjectFolder,
    removeProject,
    relocateProject,
    useNoProject,
  };
}
