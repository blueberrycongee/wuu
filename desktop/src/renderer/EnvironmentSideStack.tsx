import type { RefObject } from "react";
import type { TodoUpdate } from "../shared/protocol";
import type { InspectorSnapshotV1 } from "../shared/workbench";
import type { AppState } from "./AppState";
import {
  EnvironmentPanel,
  type EnvironmentPanelMenu,
  type EnvironmentPanelMotionState,
} from "./EnvironmentPanel";
import { desktopPluginHost, desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { PluginInspectorSections } from "./plugins/PluginInspector";
export function EnvironmentSideStack({
  visible,
  mounted,
  state,
  panelRef,
  closing,
  motionState,
  todoUpdate,
  activeMenu,
  running,
  pullRequestDisabledReason,
  rightPanelFilePath,
  onCloseFilePreview,
  onSetActiveMenu,
  onClose,
  onSelectBranch,
  onCreateBranch,
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
}: {
  visible: boolean;
  mounted: boolean;
  state: AppState;
  panelRef: RefObject<HTMLDivElement | null>;
  closing: boolean;
  motionState: EnvironmentPanelMotionState;
  todoUpdate?: TodoUpdate;
  activeMenu: EnvironmentPanelMenu;
  running: boolean;
  pullRequestDisabledReason: string;
  /**
   * Absolute path of the file the right panel should preview. When set
   * together with `activeMenu === "file"`, the panel swaps to a file
   * viewer; `onCloseFilePreview` returns it to the default environment view.
   */
  rightPanelFilePath?: string;
  onCloseFilePreview?: () => void;
  onSetActiveMenu: (menu: EnvironmentPanelMenu) => void;
  onClose: () => void;
  onSelectBranch: (branch: string) => void;
  onCreateBranch: (branch: string) => Promise<void>;
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
}): JSX.Element | null {
  const shouldRender = (visible || mounted) && Boolean(state.initialized);

  if (!shouldRender || !state.initialized) {
    return null;
  }

  const inspectorSnapshot = buildInspectorSnapshot(state, todoUpdate);

  return (
    <div
      className="environment-side-stack environment-info-side-stack"
      data-pip-obstacle="environment"
    >
      <EnvironmentPanel
        panelRef={panelRef}
        motionState={closing ? "closing" : motionState}
        initialized={state.initialized}
        gitStatus={state.gitStatus}
        activeMenu={activeMenu}
        running={running}
        pullRequestDisabledReason={pullRequestDisabledReason}
        rightPanelFilePath={rightPanelFilePath}
        onCloseFilePreview={onCloseFilePreview}
        onSetActiveMenu={onSetActiveMenu}
        onClose={onClose}
        onSelectBranch={onSelectBranch}
        onCreateBranch={onCreateBranch}
        onOpenReview={onOpenReview}
        onOpenCommit={onOpenCommit}
        onOpenPullRequest={onOpenPullRequest}
        pluginSections={
          <PluginInspectorSections
            host={desktopPluginHost}
            controller={desktopWorkbenchController}
            snapshot={inspectorSnapshot}
          />
        }
      />
    </div>
  );
}

function buildInspectorSnapshot(state: AppState, todoUpdate?: TodoUpdate): InspectorSnapshotV1 {
  const latestTurn = state.thread?.turns.at(-1);
  const activeContext = state.activeContext;
  const session = Object.freeze({
    id: state.thread?.id || state.activeSessionTabID || undefined,
    status: state.running || state.thread?.status === "in_progress" ? "running" as const : "idle" as const,
    turnId: latestTurn?.id,
    turnStatus: latestTurn?.status,
  });
  const activeWorkspace = activeContext?.kind === "project"
    ? state.projects.find((project) => project.id === activeContext.project_id)
    : undefined;
  const workspace = activeContext === undefined
    ? undefined
    : Object.freeze({
        kind: activeContext.kind,
        cwd: activeContext.cwd,
        projectId: activeContext.kind === "project" ? activeContext.project_id : undefined,
        projectName: activeWorkspace?.name,
        branch: state.gitStatus?.branch,
        dirtyFileCount: state.gitStatus?.dirty_count,
      });
  const todo = todoUpdate === undefined
    ? undefined
    : Object.freeze({
        completed: todoUpdate.todos.filter((item) => item.status === "completed").length,
        total: todoUpdate.todos.length,
        activeContent: todoUpdate.todos.find((item) => item.status === "in_progress")?.content,
        items: Object.freeze(todoUpdate.todos.map((item) => Object.freeze({
          content: item.content,
          status: item.status,
        }))),
      });
  return Object.freeze({ contractVersion: 1, session, workspace, todo });
}
