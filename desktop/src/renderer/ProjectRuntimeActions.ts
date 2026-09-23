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

export type ProjectRuntimeActionsDeps = {
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
  closeProjectMenus: () => void;
  
  beginViewSwitch: (kind: "thread" | "project" | "runtime", targetID: string) => number;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  loadRuntime?: typeof defaultLoadRuntime;
};

export type ProjectRuntimeActions = {
  selectProjectForNewThread: (projectId: string) => Promise<void>;
  startNewThreadForProject: (projectId: string) => Promise<boolean>;
  createBlankProject: () => Promise<void>;
  chooseProjectFolder: () => Promise<void>;
  removeProject: (projectId: string) => Promise<void>;
  relocateProject: (projectId: string) => Promise<void>;
  useNoProject: (fresh: boolean) => Promise<boolean>;
};

export function createProjectRuntimeActions(
  deps: ProjectRuntimeActionsDeps,
): ProjectRuntimeActions {
  const loadRuntime = deps.loadRuntime ?? defaultLoadRuntime;

  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function withoutProjectSessionTabs(state: AppState, projectId: string): AppState {
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
   * The composer's project picker has one mental model regardless of the
   * destination: "retarget the conversation I'm drafting at that
   * context". Selecting a project and selecting 不使用项目 are the same
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
    switchKind: "project" | "runtime";
    switchTarget: string;
    isCurrentContext: (state: AppState) => boolean;
    selectContext: () => Promise<ProjectListResult>;
    failureStatus: string;
  }): Promise<boolean> {
    const currentState = deps.getAppState();
    if (isCurrentContext(currentState)) {
      deps.closeProjectMenus();
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
    deps.closeProjectMenus();

    const outgoingDraft = deps.getPrimaryComposerDraft();
    const carryDraft =
      activeSessionTab(currentState)?.kind === "draft" &&
      composerDraftHasContent(outgoingDraft)
        ? outgoingDraft
        : undefined;
    try {
      const projectState = await selectContext();
      const loadedState = await loadRuntime(projectState, {
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

  async function selectProjectForNewThread(projectId: string): Promise<void> {
    await retargetDraftToContext({
      switchKind: "project",
      switchTarget: projectId,
      isCurrentContext: (state) =>
        projectId === state.activeProjectId &&
        state.activeContext?.kind === "project",
      selectContext: () => window.wuu.selectProject(projectId),
      failureStatus: translateCurrent("project.openFailed"),
    });
  }

  async function startNewThreadForProject(projectId: string): Promise<boolean> {
    deps.cancelViewSwitch();
    deps.closeProjectMenus();
    
    const currentState = deps.getAppState();
    if (
      projectId === currentState.activeProjectId &&
      currentState.activeContext?.kind === "project"
    ) {
      activateWorkspaceDraft(currentState.activeContext);
      return true;
    }
    const requestID = deps.beginViewSwitch("project", projectId);
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const projectState = await window.wuu.selectProject(projectId);
      const loadedState = await loadRuntime(projectState, {
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
      setStatus(error instanceof Error ? error.message : translateCurrent("project.openFailed"));
      return false;
    }
  }

  async function createBlankProject(): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "create-project");
    deps.closeProjectMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const projectState = await window.wuu.createBlankProject();
      if (sameRuntimeContext(projectState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: projectState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(projectState);
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
      setStatus(error instanceof Error ? error.message : translateCurrent("project.createFailed"));
    }
  }

  async function chooseProjectFolder(): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "choose-project");
    deps.closeProjectMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const projectState = await window.wuu.chooseProjectFolder();
      if (sameRuntimeContext(projectState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: projectState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(projectState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.clearPrimaryComposerDraft();
      deps.setAppState((current) => {
        const persisted = persistActiveSessionTabDraft(current, outgoingDraft);
        const destinationProjectID =
          loadedState.activeContext?.kind === "project"
            ? loadedState.activeContext.project_id
            : undefined;
        return withLoadedRuntimeSessionTab(
          destinationProjectID
            ? withoutProjectSessionTabs(persisted, destinationProjectID)
            : persisted,
          loadedState,
        );
      });
    } catch (error) {
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      setStatus(error instanceof Error ? error.message : translateCurrent("project.folderOpenFailed"));
    }
  }

  async function removeProject(projectId: string): Promise<void> {
    const currentState = deps.getAppState();
    const removedProject = currentState.projects.find(
      (project) => project.id === projectId,
    );
    if (
      !removedProject ||
      !window.confirm(
        translateCurrent("project.removeConfirm", { name: removedProject.name }),
      )
    ) {
      return;
    }
    const requestID = deps.beginViewSwitch("runtime", "remove-project");
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const projectState = await window.wuu.removeProject(projectId);
      if (sameRuntimeContext(projectState.active_context, currentState.activeContext)) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...withoutProjectSessionTabs(current, projectId),
          projects: projectState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(projectState, {
        resumeLatestThread: false,
      });
      if (!deps.finishViewSwitch(requestID)) {
        return;
      }
      deps.restoreLoadedRuntimeComposerDraft(loadedState);
      deps.setAppState((current) =>
        withLoadedRuntimeSessionTab(
          withoutProjectSessionTabs(
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
      setStatus(error instanceof Error ? error.message : translateCurrent("project.removeFailed"));
    }
  }

  async function relocateProject(projectId: string): Promise<void> {
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "relocate-project");
    const outgoingDraft = deps.getPrimaryComposerDraft();
    const previousCwd = currentState.activeContext?.cwd;
    const wasActive = currentState.activeProjectId === projectId;
    try {
      const projectState = await window.wuu.relocateProject(projectId);
      const newCwd = projectState.active_context?.cwd;
      if (!wasActive || newCwd === previousCwd) {
        if (!deps.finishViewSwitch(requestID)) {
          return;
        }
        deps.setAppState((current) => ({
          ...current,
          projects: projectState.projects,
        }));
        return;
      }
      const loadedState = await loadRuntime(projectState);
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
          : translateCurrent("project.relocateFailed"),
      );
    }
  }

  async function useNoProject(fresh: boolean): Promise<boolean> {
    // The non-fresh flavor is the composer picker's 不使用项目 entry —
    // the same "retarget my draft" gesture as picking a project, so it
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
        failureStatus: translateCurrent("project.scratchOpenFailed"),
      });
    }
    const currentState = deps.getAppState();
    const requestID = deps.beginViewSwitch("runtime", "no-project:fresh");
    deps.closeProjectMenus();
    const outgoingDraft = deps.getPrimaryComposerDraft();
    try {
      const projectState = await window.wuu.selectNoProject(true);
      const loadedState = await loadRuntime(projectState, {
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
      setStatus(error instanceof Error ? error.message : translateCurrent("project.scratchOpenFailed"));
      return false;
    }
  }

  return {
    selectProjectForNewThread,
    startNewThreadForProject,
    createBlankProject,
    chooseProjectFolder,
    removeProject,
    relocateProject,
    useNoProject,
  };
}
