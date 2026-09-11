import {
  lazy,
  Suspense,
  useSyncExternalStore,
  type ComponentProps,
  type KeyboardEvent as ReactKeyboardEvent,
  type MutableRefObject,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
import {
  Info,
} from "lucide-react";
import type {
  Agent,
  InputFile,
  InputImage,
  MessageContentPart,
  Thread,
  ThreadItem,
  UserQuestionAnswer,
  UserQuestionRequest,
} from "../shared/protocol";
import {
  emptyComposerDraft,
  isThreadPresentationRunning,
  queryTextsForThread,
  requestedHandoffIntentForThread,
  sessionTabLabel,
  threadForTab,
  turnStreamStatusForThread,
  type AppState,
  type ComposerDraftState,
  type ConversationPaneID,
} from "./AppState";
import {
  CONVERSATION_SPLIT_MAX_PERCENT,
  CONVERSATION_SPLIT_MIN_PERCENT,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
} from "./AppLayoutState";
import { EnvironmentSideStack } from "./EnvironmentSideStack";
import { CompactConversationActions } from "./CompactConversationActions";
import {
  Composer,
} from "./ComposerView";
import type { PendingComposerMessagesByThread } from "./ComposerPendingMessages";
import { ConversationSplitPane } from "./ConversationSplitPane";
import type { HistoryMessageEditState } from "./ConversationHistoryActions";
import { SessionTabStrip } from "./SessionTabs";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { ViewSwitchLoading } from "./LoadingViews";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { useI18n } from "./i18n";
import { HeaderPresentation, immutableHeaderSnapshot } from "./plugins/HeaderPresentation";
import { desktopPluginHost, desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import { PluginSlot } from "./plugins/PluginSlot";
import type { WorkbenchController } from "./plugins/Workbench";

const SettingsView = lazy(() => import("./SettingsView").then((module) => ({
  default: module.SettingsView,
})));

type EnvironmentSideStackProps = ComponentProps<typeof EnvironmentSideStack>;
type SettingsViewProps = ComponentProps<typeof SettingsView>;

export type ConversationSplitPaneRendererProps = {
  state: AppState;
  thread: Thread;
  pane: ConversationPaneID;
  splitComposerDrafts: Record<ConversationPaneID, ComposerDraftState>;
  splitPaneRefs: MutableRefObject<Record<ConversationPaneID, HTMLElement | null>>;
  viewSwitchPending: boolean;
  historyMessageEdit?: HistoryMessageEditState;
  onActivatePane: (pane: ConversationPaneID) => void;
  onClosePane: (pane: ConversationPaneID) => void;
  onConversationScroll: (node: HTMLElement) => void;
  onSetPrompt: (pane: ConversationPaneID, value: string) => void;
  onPasteAttachmentFiles: (
    pane: ConversationPaneID,
    files: File[],
  ) => void;
  onRemoveFile: (pane: ConversationPaneID, id: string) => void;
  onRemoveImage: (pane: ConversationPaneID, id: string) => void;
  onSend: (
    pane: ConversationPaneID,
    promptOverride?: string,
    contentParts?: MessageContentPart[],
  ) => void;
  onInterrupt: (pane: ConversationPaneID) => void;
  onForkMessage: (thread: Thread, turnID: string, itemID: string) => void;
  onOpenFile?: (thread: Thread, path: string) => void;
  onOpenAgent: (agent: Agent) => void;
  canEditThreadMessage: (thread: Thread) => boolean;
  onEditMessage: (
    thread: Thread,
    turnID: string,
    item: ThreadItem,
    pane: ConversationPaneID,
  ) => void;
  onCancelEditMessage: () => void;
  onSubmitEditMessage: (
    thread: Thread,
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts: MessageContentPart[] | undefined,
    pane: ConversationPaneID,
  ) => void;
  onStreamFrame: () => void;
  onOpenFileDiff: (threadID: string, selection: TurnFileDiffSelection) => void;
  pendingUserQuestion?: UserQuestionRequest;
  onAnswerUserQuestion?: (requestID: string, answer: UserQuestionAnswer) => Promise<void>;
  onCancelUserQuestion?: (requestID: string) => Promise<void>;
};

export function ConversationSplitPaneRenderer({
  state,
  thread,
  pane,
  splitComposerDrafts,
  splitPaneRefs,
  viewSwitchPending,
  historyMessageEdit,
  onActivatePane,
  onClosePane,
  onConversationScroll,
  onSetPrompt,
  onPasteAttachmentFiles,
  onRemoveFile,
  onRemoveImage,
  onSend,
  onInterrupt,
  onForkMessage,
  onOpenFile,
  onOpenAgent,
  canEditThreadMessage,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onStreamFrame,
  onOpenFileDiff,
  pendingUserQuestion,
  onAnswerUserQuestion,
  onCancelUserQuestion,
}: ConversationSplitPaneRendererProps): JSX.Element {
  return (
    <ConversationSplitPane
      pane={pane}
      thread={thread}
      active={state.activePane === pane}
      activeContextCwd={state.activeContext?.cwd}
      appStatus={state.status}
      streamStatus={turnStreamStatusForThread(state, thread)}
      draft={splitComposerDrafts[pane] ?? emptyComposerDraft()}
      viewSwitchPending={viewSwitchPending}
      queryHistory={queryTextsForThread(thread)}
      requestedHandoffIntent={requestedHandoffIntentForThread(thread)}
      editingMessage={
        historyMessageEdit?.threadID === thread.id
          ? historyMessageEdit
          : undefined
      }
      onActivate={() => onActivatePane(pane)}
      onClose={() => onClosePane(pane)}
      onBodyRef={(node) => {
        splitPaneRefs.current[pane] = node;
      }}
      onScroll={onConversationScroll}
      onSetPrompt={(value) => onSetPrompt(pane, value)}
      onPasteAttachmentFiles={(files) => onPasteAttachmentFiles(pane, files)}
      onRemoveFile={(id) => onRemoveFile(pane, id)}
      onRemoveImage={(id) => onRemoveImage(pane, id)}
      onSend={(promptOverride, contentParts) => onSend(pane, promptOverride, contentParts)}
      onInterrupt={() => onInterrupt(pane)}
      onForkMessage={(turnID, itemID) => onForkMessage(thread, turnID, itemID)}
      onOpenFile={(path) => onOpenFile?.(thread, path)}
      onOpenAgent={(agentID) => {
        const agent = thread.child_agents?.find(
          (candidate) => candidate.id === agentID,
        );
        if (agent) {
          onOpenAgent(agent);
        }
      }}
      onEditMessage={
        canEditThreadMessage(thread)
          ? (turnID, item) => onEditMessage(thread, turnID, item, pane)
          : undefined
      }
      onCancelEditMessage={onCancelEditMessage}
      onSubmitEditMessage={(turnID, item, text, images, files, contentParts) =>
        onSubmitEditMessage(thread, turnID, item, text, images, files, contentParts, pane)
      }
      onStreamFrame={onStreamFrame}
      onOpenFileDiff={(selection) => onOpenFileDiff(thread.id, selection)}
      pendingUserQuestion={pendingUserQuestion}
      onAnswerUserQuestion={onAnswerUserQuestion}
      onCancelUserQuestion={onCancelUserQuestion}
    />
  );
}

export type ConversationSplitLayoutRendererProps = Omit<
  ConversationSplitPaneRendererProps,
  "thread" | "pane"
> & {
  primaryThread: Thread;
  secondaryThread: Thread;
  splitLeftPercent: number;
  onSplitResizeStart: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onSplitSeparatorDoubleClick: () => void;
  onSplitSeparatorKey: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
};

export function ConversationSplitLayoutRenderer({
  primaryThread,
  secondaryThread,
  splitLeftPercent,
  onSplitResizeStart,
  onSplitSeparatorDoubleClick,
  onSplitSeparatorKey,
  ...paneProps
}: ConversationSplitLayoutRendererProps): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="conversation-split">
      <ConversationSplitPaneRenderer
        {...paneProps}
        thread={primaryThread}
        pane="primary"
      />
      <div
        className="conversation-split-resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label={t("shell.resizeSplit")}
        aria-valuemin={CONVERSATION_SPLIT_MIN_PERCENT}
        aria-valuemax={CONVERSATION_SPLIT_MAX_PERCENT}
        aria-valuenow={Math.round(splitLeftPercent)}
        tabIndex={0}
        onPointerDown={onSplitResizeStart}
        onDoubleClick={onSplitSeparatorDoubleClick}
        onKeyDown={onSplitSeparatorKey}
      />
      <ConversationSplitPaneRenderer
        {...paneProps}
        thread={secondaryThread}
        pane="secondary"
      />
    </div>
  );
}

export type ConversationTitleContentProps = {
  state: AppState;
  crossWorkspaceThreads: Thread[];
  runningThreadIDs?: ReadonlySet<string>;
  sessionTabsVisible: boolean;
  pendingSwitchThreadID?: string;
  pendingComposerMessagesByThread: PendingComposerMessagesByThread;
  channelUnreadByRoomID?: Record<string, number>;
  activeTitle: string;
  onSelectSessionTab: (tabID: string) => void;
  onCloseSessionTab: (tabID: string) => void;
  onCloseSessionTabs: (tabIDs: string[]) => void;
  onPopOutSessionTab: (tabID: string) => void;
  onStartNewThread: () => void;
  onReorderSessionTabs: (activeID: string, overID: string) => void;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
};

export function ConversationTitleContent({
  state,
  crossWorkspaceThreads,
  runningThreadIDs,
  sessionTabsVisible,
  pendingSwitchThreadID,
  pendingComposerMessagesByThread,
  channelUnreadByRoomID,
  activeTitle,
  onSelectSessionTab,
  onCloseSessionTab,
  onCloseSessionTabs,
  onPopOutSessionTab,
  onStartNewThread,
  onReorderSessionTabs,
  pluginHost,
  workbenchController,
}: ConversationTitleContentProps): JSX.Element {
  const controller = workbenchController ?? desktopWorkbenchController;
  const workbenchSnapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  const primaryViews = workbenchSnapshot.views.filter((view) => view.region === "primary");
  const activePrimaryView = primaryViews.find(
    (view) => view.id === workbenchSnapshot.activeViewByRegion.primary,
  );
  const primaryTabs = primaryViews.map((view) => ({
    id: view.id,
    title: workbenchSnapshot.viewTypes.find((definition) =>
      definition.pluginId === view.pluginId
      && definition.id === view.viewTypeId
      && definition.generation === view.generation)?.title ?? view.viewTypeId,
  }));
  const fallback = sessionTabsVisible || primaryTabs.length > 0 ? (
      <SessionTabStrip
        state={state}
        crossWorkspaceThreads={crossWorkspaceThreads}
        runningThreadIDs={runningThreadIDs}
        pendingSwitchThreadID={pendingSwitchThreadID}
        pendingComposerMessagesByThread={pendingComposerMessagesByThread}
        channelUnreadByRoomID={channelUnreadByRoomID}
        canStartNewThread={Boolean(state.activeContext)}
        onSelect={(tabId) => {
          controller.deactivateRegion("primary");
          onSelectSessionTab(tabId);
        }}
        onClose={onCloseSessionTab}
        onCloseTabs={onCloseSessionTabs}
        onPopOut={onPopOutSessionTab}
        onNewThread={onStartNewThread}
        onReorder={onReorderSessionTabs}
        additionalTabs={primaryTabs}
        activeAdditionalTabID={activePrimaryView?.id}
        onSelectAdditionalTab={(tabId) => controller.activateView(tabId)}
        onCloseAdditionalTab={(tabId) => void controller.closeView(tabId)}
      />
  ) : (
    <h1>{activeTitle}</h1>
  );
  const tabState = { ...state, threads: crossWorkspaceThreads };
  const tabs = sessionTabsVisible
    ? state.sessionTabs.map((tab) => {
        const tabThread = tab.kind === "thread" ? threadForTab(tabState, tab.threadID) : undefined;
        const dirty = "prompt" in tab
          ? tab.prompt.length > 0 || tab.images.length > 0 || tab.files.length > 0
          : undefined;
        return {
          id: tab.id,
          title: sessionTabLabel(tab, tabState),
          kind: tab.kind,
          busy: isThreadPresentationRunning(
            tabThread,
            tab.kind === "thread" && runningThreadIDs?.has(tab.threadID),
          ) || (
            pendingSwitchThreadID !== undefined &&
            tab.kind === "thread" &&
            pendingSwitchThreadID === tab.threadID
          ),
          dirty: dirty || undefined,
        };
      })
    : undefined;
  const showingPrimaryWorkbench = activePrimaryView !== undefined;
  const headerTabs = [...(tabs ?? []), ...primaryTabs];
  const snapshot = immutableHeaderSnapshot({
    scope: showingPrimaryWorkbench ? "workspace" : "conversation",
    title: showingPrimaryWorkbench
      ? primaryTabs.find((tab) => tab.id === activePrimaryView.id)?.title
      : activeTitle,
    tabs: headerTabs.length > 0 ? headerTabs : undefined,
    activeTabId: activePrimaryView?.id
      ?? (sessionTabsVisible ? state.activeSessionTabID || undefined : undefined),
    busy: tabs?.some((tab) => tab.busy) || undefined,
    dirty: tabs?.some((tab) => tab.dirty) || undefined,
  });
  const primaryTabIDs = new Set(primaryTabs.map((tab) => tab.id));
  return (
    <>
      <PluginSlot
        host={pluginHost ?? desktopPluginHost}
        id={showingPrimaryWorkbench ? "workspace.header" : "conversation.header"}
        context={Object.freeze({
          scope: showingPrimaryWorkbench ? "workspace" : "conversation",
          hasSessionTabs: showingPrimaryWorkbench || sessionTabsVisible,
          tabCount: headerTabs.length,
          busy: snapshot.busy ?? false,
        })}
      />
      <HeaderPresentation
        snapshot={snapshot}
        fallback={fallback}
        onSelectTab={headerTabs.length > 0 ? (tabId) => {
          if (primaryTabIDs.has(tabId)) {
            controller.activateView(tabId);
            return;
          }
          controller.deactivateRegion("primary");
          onSelectSessionTab(tabId);
        } : undefined}
        onCloseTab={headerTabs.length > 0 ? (tabId) => {
          if (primaryTabIDs.has(tabId)) {
            void controller.closeView(tabId);
            return;
          }
          onCloseSessionTab(tabId);
        } : undefined}
        host={pluginHost}
        controller={controller}
      />
    </>
  );
}

export type ConversationTitleActionsProps = {
  state: AppState;
  compactNavigation?: boolean;
  onStartNewThread: () => void;
  environmentToggleRef: RefObject<HTMLButtonElement | null>;
  environmentPanelVisible: boolean;
  onToggleEnvironmentPanel: () => void;
  rightPanelOpen: boolean;
  onToggleRightPanel: () => void;
};

export function ConversationTitleActions({
  state,
  compactNavigation,
  onStartNewThread,
  environmentToggleRef,
  environmentPanelVisible,
  onToggleEnvironmentPanel,
  rightPanelOpen,
  onToggleRightPanel,
}: ConversationTitleActionsProps): JSX.Element {
  const { t } = useI18n();
  if (compactNavigation) {
    return <CompactConversationActions
      canStartNewThread={Boolean(state.activeContext)} onStartNewThread={onStartNewThread}
      environmentToggleRef={environmentToggleRef} environmentPanelVisible={environmentPanelVisible}
      onToggleEnvironmentPanel={onToggleEnvironmentPanel} rightPanelOpen={rightPanelOpen}
      onToggleRightPanel={onToggleRightPanel}
    />;
  }
  return (
    <div className="title-actions">
      <button
            ref={environmentToggleRef}
            className={`icon-button environment-toggle-button${environmentPanelVisible ? " active" : ""}`}
            type="button"
            aria-label={
              environmentPanelVisible
                ? t("shell.hideEnvironmentInfo")
                : t("shell.showEnvironmentInfo")
            }
            aria-pressed={environmentPanelVisible}
            onClick={onToggleEnvironmentPanel}
          >
            <Info className="icon-lg" size={18} />
      </button>
      <button
            className="icon-button side-panel-toggle-button"
            type="button"
            aria-label={t(
              rightPanelOpen ? "shell.closeRightSidebar" : "shell.openRightSidebar",
            )}
            aria-pressed={rightPanelOpen}
            onClick={onToggleRightPanel}
          >
            <SidePanelToggleIcon side="right" open={rightPanelOpen} />
      </button>
    </div>
  );
}

export type ConversationSidePanelsProps = {
  state: AppState;
  environmentPanelVisible: boolean;
  environmentPanelMounted: boolean;
  environmentPanelRef: EnvironmentSideStackProps["panelRef"];
  environmentPanelClosing: boolean;
  environmentPanelMotionState: EnvironmentSideStackProps["motionState"];
  activeTodoUpdate: EnvironmentSideStackProps["todoUpdate"];
  environmentPanelMenu: EnvironmentSideStackProps["activeMenu"];
  environmentGitBusy: boolean;
  pullRequestDisabledReason: string;
  onSetEnvironmentPanelMenu: EnvironmentSideStackProps["onSetActiveMenu"];
  onCloseEnvironmentPanel: () => void;
  onSelectBranch: (branch: string) => void;
  onCreateBranch: EnvironmentSideStackProps["onCreateBranch"];
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
  rightPanelFilePath?: string;
  onCloseFilePreview: () => void;
  switchLoadingVisible: boolean;
};

export function ConversationSidePanels({
  state,
  environmentPanelVisible,
  environmentPanelMounted,
  environmentPanelRef,
  environmentPanelClosing,
  environmentPanelMotionState,
  activeTodoUpdate,
  environmentPanelMenu,
  environmentGitBusy,
  pullRequestDisabledReason,
  onSetEnvironmentPanelMenu,
  onCloseEnvironmentPanel,
  onSelectBranch,
  onCreateBranch,
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
  rightPanelFilePath,
  onCloseFilePreview,
  switchLoadingVisible,
}: ConversationSidePanelsProps): JSX.Element {
  return (
    <>
      <EnvironmentSideStack
        visible={environmentPanelVisible}
        mounted={environmentPanelMounted}
        state={state}
        panelRef={environmentPanelRef}
        closing={environmentPanelClosing}
        motionState={environmentPanelMotionState}
        todoUpdate={activeTodoUpdate}
        activeMenu={environmentPanelMenu}
        running={environmentGitBusy}
        pullRequestDisabledReason={pullRequestDisabledReason}
        onSetActiveMenu={onSetEnvironmentPanelMenu}
        onClose={onCloseEnvironmentPanel}
        onSelectBranch={onSelectBranch}
        onCreateBranch={onCreateBranch}
        onOpenReview={onOpenReview}
        onOpenCommit={onOpenCommit}
        onOpenPullRequest={onOpenPullRequest}
        rightPanelFilePath={rightPanelFilePath}
        onCloseFilePreview={onCloseFilePreview}
      />

      {switchLoadingVisible ? <ViewSwitchLoading /> : null}
    </>
  );
}

export type SettingsShellRendererProps = Omit<
  SettingsViewProps,
  "sidebarMinWidth" | "sidebarMaxWidth"
>;

export function SettingsShellRenderer(
  props: SettingsShellRendererProps,
): JSX.Element {
  return (
    <Suspense fallback={<ViewSwitchLoading />}>
      <SettingsView
        {...props}
        sidebarMinWidth={SIDEBAR_MIN_WIDTH}
        sidebarMaxWidth={SIDEBAR_MAX_WIDTH}
      />
    </Suspense>
  );
}
