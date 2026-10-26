import {
  lazy,
  Suspense,
  useLayoutEffect,
  useRef,
  useState,
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
  Project,
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
  activeTurnForThread,
  isThreadPresentationRunning,
  turnStreamStatusForThread,
  type AppState,
  type ConversationPaneID,
} from "./AppState";
import {
  CONVERSATION_SPLIT_MAX_PERCENT,
  CONVERSATION_SPLIT_MIN_PERCENT,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
} from "./AppLayoutState";
import { EnvironmentSideStack } from "./EnvironmentSideStack";
import { ENABLE_ENVIRONMENT_PANEL } from "./FeatureFlags";
import {
  Composer,
} from "./ComposerView";
import { ConversationSplitPane } from "./ConversationSplitPane";
import type { HistoryMessageEditState } from "./ConversationHistoryActions";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { Tooltip } from "./Tooltip";
import { ViewSwitchLoading } from "./LoadingViews";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { useI18n } from "./i18n";
import { useProjectActions } from "./ProjectActions";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import { conversationHeadingTitle } from "./ThreadTitles";
import { showErrorToast } from "./Toast";

const SettingsView = lazy(() => import("./SettingsView").then((module) => ({
  default: module.SettingsView,
})));

type EnvironmentSideStackProps = ComponentProps<typeof EnvironmentSideStack>;
type SettingsViewProps = ComponentProps<typeof SettingsView>;

export type ConversationSplitPaneRendererProps = {
  state: AppState;
  thread: Thread;
  pane: ConversationPaneID;
  composerOwnerTabIds: Partial<Record<ConversationPaneID, string>>;
  splitPaneRefs: MutableRefObject<Record<ConversationPaneID, HTMLElement | null>>;
  historyMessageEdit?: HistoryMessageEditState;
  onActivatePane: (pane: ConversationPaneID) => void;
  onClosePane: (pane: ConversationPaneID) => void;
  onConversationScroll: (node: HTMLElement) => void;
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
  composerOwnerTabIds,
  splitPaneRefs,
  historyMessageEdit,
  onActivatePane,
  onClosePane,
  onConversationScroll,
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
      streamStatus={turnStreamStatusForThread(state, thread)}
      ownerTabId={composerOwnerTabIds[pane]}
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
  application: ApplicationServiceV1;
  slots: UICompositionV1;
  slotOwner: string;
};

function ConversationTitleText({
  title,
  editable,
  editKey,
  headingRef,
  onRenameTitle,
}: {
  title: string;
  editable: boolean;
  editKey?: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  onRenameTitle?: (title: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(title);
  const [sessionKey, setSessionKey] = useState(editKey);
  const inputRef = useRef<HTMLInputElement>(null);
  const skipBlurCommit = useRef(false);
  if (sessionKey !== editKey) {
    if (editing) skipBlurCommit.current = true;
    setSessionKey(editKey);
    setEditing(false);
  }
  useLayoutEffect(() => {
    if (!editing) return;
    const input = inputRef.current;
    input?.focus();
    input?.select();
  }, [editing]);

  function closeEditor(commit: boolean): void {
    const next = draft.trim();
    skipBlurCommit.current = true;
    setEditing(false);
    if (commit && next && next !== title.trim()) {
      onRenameTitle?.(next);
    }
  }

  function handleBlur(): void {
    if (skipBlurCommit.current) {
      skipBlurCommit.current = false;
      return;
    }
    closeEditor(true);
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLInputElement>): void {
    if (event.nativeEvent.isComposing || event.key === "Process") return;
    if (event.key !== "Enter" && event.key !== "Escape") return;
    event.preventDefault();
    event.stopPropagation();
    closeEditor(event.key === "Enter");
  }

  return (
    <h1
      ref={headingRef}
      className={editing ? "is-editing" : editable ? "is-editable" : undefined}
      tabIndex={-1}
    >
      {editing ? (
        <input
          ref={inputRef}
          className="conversation-title-edit"
          aria-label={t("threadSidebar.title")}
          value={draft}
          spellCheck={false}
          autoComplete="off"
          onChange={(event) => setDraft(event.target.value)}
          onBlur={handleBlur}
          onKeyDown={handleKeyDown}
        />
      ) : editable ? (
        <button
          type="button"
          className="conversation-title-rename"
          aria-label={t("thread.rename.editNamed", { title })}
          title={t("threadSidebar.rename")}
          onClick={(event) => {
            // Keyboard activation has detail 0; pointer clicks wait for a double click.
            if (event.detail !== 0) return;
            setDraft(title);
            setEditing(true);
          }}
          onDoubleClick={() => {
            setDraft(title);
            setEditing(true);
          }}
        >
          {title}
        </button>
      ) : title}
    </h1>
  );
}

export function ConversationTitleContent({ application, slots, slotOwner }: ConversationTitleContentProps): JSX.Element {
  const { t } = useI18n();
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
  const activePrimaryView = view.navigationWorkspace.activePluginView;
  const tab = view.tabs.items.find(tab => tab.id === view.tabs.activeTabId);
  const workspace = view.conversationWorkspace;
  const threadId = workspace.activePane === "secondary" ? workspace.secondaryThreadId : workspace.primaryThreadId;
  const thread = workspace.threads.find(thread => thread.id === threadId);
  const catalog = tab?.kind === "skills";
  const nativeTitle = catalog ? t("skills.title") : conversationHeadingTitle(thread, tab?.kind === "draft" ? tab.title : undefined, t("tabs.newConversation"));
  const title = activePrimaryView?.title ?? nativeTitle;
  const editable = !activePrimaryView && !catalog && Boolean(thread ? !thread.read_only && !thread.ephemeral : tab?.kind === "draft");
  const headingRef = useRef<HTMLHeadingElement>(null);
  const restoreFocus = useRef(false);
  useLayoutEffect(() => {
    if (!restoreFocus.current) return;
    restoreFocus.current = false;
    headingRef.current?.focus();
  }, [activePrimaryView?.id]);
  const runNavigation = (request: Promise<unknown>) => {
    void request.catch(error => { restoreFocus.current = false; showErrorToast(error); });
  };
  const navigateBack = activePrimaryView ? () => {
    restoreFocus.current = true;
    runNavigation(application.invoke("navigation.show-native", {}));
  } : undefined;
  const rename = (title: string) => {
    if (thread) void application.invoke("thread.rename", { threadId: thread.id, title }).catch(showErrorToast);
    else if (tab?.kind === "draft") void application.invoke("draft.rename", { tabId: tab.id, title }).catch(showErrorToast);
  };
  const scope = activePrimaryView ? "workspace" : "conversation";
  const activeTurn = activeTurnForThread(thread);
  const presentation = thread && { status: thread.status, child_agents: thread.child_agents,
    turns: activeTurn ? [{ status: activeTurn.status, answer_ready_at: activeTurn.answer_ready_at,
      items: activeTurn.items.map(({ id, type, status, terminal }) => ({ id, type, status, terminal })) }] : [] };
  const busy = !activePrimaryView && (Boolean(view.tabs.pending) || isThreadPresentationRunning(presentation, view.navigationWorkspace.threads.some(row => row.id === threadId && row.status === "in_progress")));
  const snapshot = Object.freeze({ contractVersion: 1 as const, scope, title, canNavigateBack: navigateBack ? true : undefined, busy: busy || undefined });
  const context = Object.freeze({ snapshot, scope, threadId, ownerTabId: tab?.id, viewId: activePrimaryView?.id, hasSessionTabs: false, tabCount: 0, busy });
  const fallback = <div className="conversation-title-heading">
    {navigateBack ? <button className="icon-button" data-wuu-component="primary-view-back" type="button" aria-label={t("common.back")} title={t("common.back")} onClick={navigateBack}>
      <ArrowLeft aria-hidden="true" />
    </button> : <Tooltip content={t("tabs.newConversation")} side="bottom"><button className="icon-button session-tab-new" type="button" aria-label={t("tabs.newConversation")} disabled={!view.navigationWorkspace.activeContextKind}
      onClick={event => {
        const document = event.currentTarget.ownerDocument;
        let focusRequested = true;
        // A later focus choice takes precedence over this asynchronous creation.
        const cancelFocus = () => { focusRequested = false; };
        document.addEventListener("focusin", cancelFocus);
        void application.invoke("workspace.start-draft", { workspaceId: view.navigationWorkspace.selectedWorkspaceId })
          .then(({ activated, tabId }) => {
            if (activated && tabId && focusRequested) return application.invoke("navigation.focus-composer", { tabId });
          })
          .catch(showErrorToast)
          .finally(() => document.removeEventListener("focusin", cancelFocus));
      }}><SquarePen aria-hidden="true" /></button></Tooltip>}
    <ConversationTitleText title={title} editable={editable} editKey={activePrimaryView?.id ?? thread?.id ?? tab?.id} headingRef={headingRef} onRenameTitle={rename} />
  </div>;
  return <>
    <slots.Slot name={`${slotOwner}/${scope}.header`} context={context} />
    <slots.Slot name={`${slotOwner}/header.${scope}`} context={context} fallback={fallback} />
    {activePrimaryView?.closable ? <button className="icon-button" data-wuu-component="primary-view-close" type="button" aria-label={t("workspace.closeTab", { label: title })} title={t("workspace.closeTab", { label: title })}
      onClick={() => { restoreFocus.current = true; runNavigation(application.invoke("navigation.close-plugin", { viewId: activePrimaryView.id })); }}><X aria-hidden="true" /></button> : null}
  </>;
}

export type ConversationTitleActionsProps = {
  thread?: Thread;
  /** A primary plugin page covers the conversation and its environment panel. */
  pluginPageVisible?: boolean;
  environmentToggleRef: RefObject<HTMLButtonElement | null>;
  environmentPanelVisible: boolean;
  onToggleEnvironmentPanel: () => void;
  rightPanelOpen: boolean;
  onToggleRightPanel: () => void;
  showRightPanelToggle?: boolean;
};

export function ConversationTitleActions({
  thread,
  pluginPageVisible,
  environmentToggleRef,
  environmentPanelVisible,
  onToggleEnvironmentPanel,
  rightPanelOpen,
  onToggleRightPanel,
  showRightPanelToggle = true,
}: ConversationTitleActionsProps): JSX.Element {
  const { t } = useI18n();
  const projectActions = useProjectActions();
  // Controls for the covered conversation leave with it; new conversations
  // and the right panel stay available on every page.
  const control = pluginPageVisible ? undefined : thread?.session_control;
  const controlLabel = control ? t(`sessionControl.${control.state === "taken_over" ? "takenOver" : control.state}`) : "";
  // Project membership is independent of user intervention; only extensions
  // expose ownership state here.
  const projectSession = thread && control && projectActions && thread.project_id === control.manager_id;
  const management = projectSession ? <>
    <button type="button" className="session-control-project" title={t("projects.openCoordinator")}
      onClick={() => projectActions.openThread(control.manager_id)}>
      <Project aria-hidden="true" />
      <span>{control.manager_name}</span>
    </button>
  </> : control ? <span className="session-control-label" title={control.state === "active" ? t("sessionControl.takeoverHint") : `${control.manager_name} · ${controlLabel}`}>
    {control.manager_name} · {controlLabel}
  </span> : null;
  const environmentLabel = environmentPanelVisible
    ? t("shell.hideEnvironmentInfo")
    : t("shell.showEnvironmentInfo");
  const rightPanelLabel = t(rightPanelOpen ? "shell.closeRightSidebar" : "shell.openRightSidebar");
  return (
    <div className="title-actions">
      {management}
      {!ENABLE_ENVIRONMENT_PANEL || pluginPageVisible ? null : (
        <Tooltip content={environmentLabel} side="bottom">
          <button
            ref={environmentToggleRef}
            className={`icon-button environment-toggle-button${environmentPanelVisible ? " active" : ""}`}
            type="button"
            aria-label={environmentLabel}
            aria-pressed={environmentPanelVisible}
            onClick={onToggleEnvironmentPanel}
          >
            <Info />
          </button>
        </Tooltip>
      )}
      {showRightPanelToggle ? <Tooltip content={rightPanelLabel} side="bottom">
        <button
          className="icon-button side-panel-toggle-button"
          type="button"
          aria-label={rightPanelLabel}
          aria-pressed={rightPanelOpen}
          onClick={onToggleRightPanel}
        >
          <SidePanelToggleIcon side="right" open={rightPanelOpen} />
        </button>
      </Tooltip> : null}
    </div>
  );
}

export type ConversationSidePanelsProps = {
  application: EnvironmentSideStackProps["application"];
  slots: EnvironmentSideStackProps["slots"];
  slotOwner: string;
  environment: EnvironmentSideStackProps["environment"];
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
  onOpenReview: () => void;
  onOpenCommit: () => void;
  onOpenPullRequest: () => void;
  rightPanelFilePath?: string;
  onCloseFilePreview: () => void;
};

export function ConversationSidePanels({
  application,
  slots,
  slotOwner,
  environment,
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
  onOpenReview,
  onOpenCommit,
  onOpenPullRequest,
  rightPanelFilePath,
  onCloseFilePreview,
}: ConversationSidePanelsProps): JSX.Element {
  return (
    <>
      <EnvironmentSideStack
        visible={environmentPanelVisible}
        mounted={environmentPanelMounted}
        application={application}
        slots={slots}
        slotOwner={slotOwner}
        environment={environment}
        panelRef={environmentPanelRef}
        closing={environmentPanelClosing}
        motionState={environmentPanelMotionState}
        todoUpdate={activeTodoUpdate}
        activeMenu={environmentPanelMenu}
        running={environmentGitBusy}
        pullRequestDisabledReason={pullRequestDisabledReason}
        onSetActiveMenu={onSetEnvironmentPanelMenu}
        onClose={onCloseEnvironmentPanel}
        onOpenReview={onOpenReview}
        onOpenCommit={onOpenCommit}
        onOpenPullRequest={onOpenPullRequest}
        rightPanelFilePath={rightPanelFilePath}
        onCloseFilePreview={onCloseFilePreview}
      />

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
    <Suspense fallback={<ViewSwitchLoading placement="inline" />}>
      <SettingsView
        {...props}
        sidebarMinWidth={SIDEBAR_MIN_WIDTH}
        sidebarMaxWidth={SIDEBAR_MAX_WIDTH}
      />
    </Suspense>
  );
}
