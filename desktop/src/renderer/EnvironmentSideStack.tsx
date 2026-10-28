import type { ApplicationEnvironmentV1 } from "../../../packages/plugin-sdk/src/ui-environment";
import { useSyncExternalStore, type RefObject } from "react";
import type { ApplicationServiceV1, UICompositionV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import type { TodoUpdate } from "../shared/protocol";
import type { InspectorSnapshotV1 } from "../shared/workbench";
import {
  EnvironmentPanel,
  type EnvironmentPanelMenu,
  type EnvironmentPanelMotionState,
} from "./EnvironmentPanel";
export function EnvironmentSideStack({
  visible,
  mounted,
  application,
  slots,
  slotOwner,
  environment,
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
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
}: {
  visible: boolean;
  mounted: boolean;
  application: ApplicationServiceV1;
  slots: UICompositionV1;
  slotOwner: string;
  environment: ApplicationEnvironmentV1;
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
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
}): JSX.Element | null {
  const snapshot = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const git = useSyncExternalStore(environment.subscribe, environment.getSnapshot).status;
  const primaryThread = snapshot.conversationWorkspace.threads.find(thread => thread.id === snapshot.conversationWorkspace.primaryThreadId);
  const shouldRender = (visible || mounted) && snapshot.navigationWorkspace.initialized;

  if (!shouldRender) {
    return null;
  }

  const inspectorSnapshot = buildInspectorSnapshot(snapshot, git, todoUpdate);

  return (
    <div
      className="environment-side-stack environment-info-side-stack"
      data-pip-obstacle="environment"
    >
      <EnvironmentPanel
        onOpenWorktree={async path => { await application.invoke("workspace.open-worktree", { path }); onSetActiveMenu(null); }}
        panelRef={panelRef}
        motionState={closing ? "closing" : motionState}
        thread={primaryThread}
        environment={environment}
        activeMenu={activeMenu}
        running={running}
        pullRequestDisabledReason={pullRequestDisabledReason}
        rightPanelFilePath={rightPanelFilePath}
        onCloseFilePreview={onCloseFilePreview}
        onSetActiveMenu={onSetActiveMenu}
        onClose={onClose}
        onOpenReview={onOpenReview}
        onOpenCommit={onOpenCommit}
        onOpenPullRequest={onOpenPullRequest}
        pluginSections={
          <div className="plugin-inspector-sections" data-wuu-component="plugin-inspector-sections">
            <slots.Slot name={`${slotOwner}/inspector.sections`} context={{ snapshot: inspectorSnapshot }} />
          </div>
        }
      />
    </div>
  );
}

function buildInspectorSnapshot(snapshot: ApplicationSnapshotV1, git: ReturnType<ApplicationEnvironmentV1["getSnapshot"]>["status"], todoUpdate?: TodoUpdate): InspectorSnapshotV1 {
  const thread = snapshot.conversationWorkspace.threads.find(thread => thread.id === snapshot.conversationWorkspace.primaryThreadId);
  const latestTurn = thread?.turns.at(-1);
  const activeRoot = snapshot.workspaces.activeRoot;
  const activeWorkspace = snapshot.workspaces.workspaces.find(workspace => workspace.id === snapshot.workspaces.activeWorkspaceId);
  const session = Object.freeze({
    id: thread?.id || snapshot.tabs.activeTabId || undefined,
    status: thread?.status === "in_progress" ? "running" as const : "idle" as const,
    turnId: latestTurn?.id,
    turnStatus: latestTurn?.status,
  });
  const workspace = activeRoot === undefined ? undefined : Object.freeze({
    kind: snapshot.workspaces.activeWorkspaceId ? "project" as const : "no_project" as const,
    cwd: activeRoot,
    projectId: snapshot.workspaces.activeWorkspaceId,
    projectName: activeWorkspace?.name,
    branch: git?.branch,
    dirtyFileCount: git?.dirty_count,
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
