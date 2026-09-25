import { showErrorToast } from "./Toast";
import {
  lazy,
  Suspense,
  useLayoutEffect,
  useRef,
  useSyncExternalStore,
  type ComponentProps,
  type KeyboardEvent as ReactKeyboardEvent,
  type MutableRefObject,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
import {
  ArrowLeft,
  SquarePen,
  Info,
  X,
} from "./WuuIcons";
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
import { CompactConversationActions } from "./CompactConversationActions"
import {
  Composer,
} from "./ComposerView";
import { ConversationSplitPane } from "./ConversationSplitPane";
import type { HistoryMessageEditState } from "./ConversationHistoryActions";
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
  ) => boolean | void;
  onInterrupt: (pane: ConversationPaneID) => void;
  onForkMessage: (thread: Thread, turnID: string, itemID: string) => void;
  onOpenFile?: (thread: Thread, path: string) => void;
  onOpenURL?: (url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => void;
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
  ) => void | Promise<void>;
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
  onOpenURL,
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
      onOpenURL={onOpenURL}
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
  runningThreadIDs?: ReadonlySet<string>;
  pendingSwitchThreadID?: string;
  activeTitle: string;
  onStartNewThread: () => void;
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
};

export function ConversationTitleContent({
  state,
  runningThreadIDs,
  pendingSwitchThreadID,
  activeTitle,
  onStartNewThread,
  pluginHost,
  workbenchController,
}: ConversationTitleContentProps): JSX.Element {
  const { t } = useI18n();
  const controller = workbenchController ?? desktopWorkbenchController;
  const workbenchSnapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  const activePrimaryView = workbenchSnapshot.views.find(
    (view) => view.region === "primary" && view.id === workbenchSnapshot.activeViewByRegion.primary,
  );
  const headingRef = useRef<HTMLHeadingElement>(null);
  const restoreFocus = useRef(false);
  useLayoutEffect(() => {
    if (!restoreFocus.current) return;
    restoreFocus.current = false;
    headingRef.current?.focus();
  }, [activePrimaryView?.id]);
  const title = activePrimaryView
    ? workbenchSnapshot.viewTypes.find((definition) =>
      definition.pluginId === activePrimaryView.pluginId
      && definition.id === activePrimaryView.viewTypeId)?.title ?? activePrimaryView.viewTypeId
    : activeTitle;
  const navigateBack = activePrimaryView ? () => {
    restoreFocus.current = true;
    controller.deactivateRegion("primary");
  } : undefined;
  const fallback = (
    <div className="conversation-title-heading">
      {navigateBack ? <button
        className="icon-button"
        data-wuu-component="primary-view-back"
        type="button"
        aria-label={t("common.back")}
        title={t("common.back")}
        onClick={navigateBack}
      >
        <ArrowLeft aria-hidden="true" />
      </button> : (
      <button
        className="icon-button session-tab-new"
        type="button"
        aria-label={t("tabs.newConversation")}
        title={t("tabs.newConversation")}
        disabled={!state.activeContext}
        onClick={onStartNewThread}
      >
        <SquarePen aria-hidden="true" />
      </button>)}
      <h1 ref={headingRef} tabIndex={-1}>{title}</h1>
    </div>
  );
  const showingPrimaryWorkbench = activePrimaryView !== undefined;
  // Session records still own drafts and recovery; they are not visible navigation.
  const snapshot = immutableHeaderSnapshot({
    scope: showingPrimaryWorkbench ? "workspace" : "conversation",
    title,
    canNavigateBack: navigateBack ? true : undefined,
    busy: !showingPrimaryWorkbench && (
      isThreadPresentationRunning(state.thread, state.thread ? runningThreadIDs?.has(state.thread.id) : false)
      || pendingSwitchThreadID !== undefined
    ) || undefined,
  });
  return (
    <>
      <PluginSlot
        host={pluginHost ?? desktopPluginHost}
        id={showingPrimaryWorkbench ? "workspace.header" : "conversation.header"}
        context={Object.freeze({
          scope: showingPrimaryWorkbench ? "workspace" : "conversation",
          hasSessionTabs: false,
          tabCount: 0,
          busy: snapshot.busy ?? false,
        })}
      />
      <HeaderPresentation
        snapshot={snapshot}
        fallback={fallback}
        onNavigateBack={navigateBack}
        host={pluginHost}
        controller={controller}
      />
      {activePrimaryView ? <button
        className="icon-button"
        data-wuu-component="primary-view-close"
        type="button"
        aria-label={t("workspace.closeTab", { label: title })}
        title={t("workspace.closeTab", { label: title })}
        onClick={() => {
          restoreFocus.current = true;
          void controller.closeView(activePrimaryView.id);
        }}
      >
        <X aria-hidden="true" />
      </button> : null}
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
  const controlledThread = state.activePane === "secondary" ? state.secondaryThread : state.thread;
  const control = controlledThread?.session_control;
  const controlLabel = control ? t(`channels.sessions.control.${control.state === "taken_over" ? "takenOver" : control.state}`) : "";
  const management = control ? <span className="session-control-label" title={control.state === "active" ? t("channels.sessions.takeoverHint") : `${control.manager_name} · ${controlLabel}`}>
    {control.manager_name} · {controlLabel}
    {control.room_id && control.state !== "active" ? <button type="button" onClick={() => void window.wuu!.returnManagedSession({ thread_id: controlledThread!.id, revision: control.revision }).catch(reason => showErrorToast(reason))}>{t("channels.sessions.returnControl")}</button> : null}
  </span> : null;
  if (compactNavigation) {
    return <div className="title-actions">{management}<CompactConversationActions
      canStartNewThread={Boolean(state.activeContext)} onStartNewThread={onStartNewThread}
      environmentToggleRef={environmentToggleRef} environmentPanelVisible={environmentPanelVisible}
      onToggleEnvironmentPanel={onToggleEnvironmentPanel} rightPanelOpen={rightPanelOpen}
      onToggleRightPanel={onToggleRightPanel}
    /></div>;
  }
  return (
    <div className="title-actions">
      {management}
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
            <Info />
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

      {switchLoadingVisible ? <ViewSwitchLoading placement="conversation" /> : null}
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
