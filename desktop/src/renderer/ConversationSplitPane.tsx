import type { UIRegisterFocusTargetV1 } from "../../../packages/plugin-sdk/src/ui";
import { memo, useCallback, useRef } from "react";
import { X } from "./WuuIcons";
import type {
  InputFile,
  InputImage,
  MessageContentPart,
  SessionLocatorV1,
  Thread,
  ThreadItem,
  Turn,
  UserQuestionAnswer,
  UserQuestionRequest,
} from "../shared/protocol";
import { useConversationControls } from "./ConversationControlsContext";
import {
  type ConversationPaneID,
  type TurnStreamStatus,
} from "./AppState";
import { ConversationTurnList } from "./ConversationTurnList";
import { TurnView, latestAgentMessageItemID } from "./TurnView";
import { UserQuestionCard } from "./UserQuestionCard";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { useI18n } from "./i18n";

export function ConversationSplitPane({
  pane,
  ownerTabId,
  registerComposerFocusTarget,
  thread,
  active,
  activeContextCwd,
  streamStatus,
  editingMessage,
  onActivate,
  onClose,
  onBodyRef,
  onScroll,
  onForkMessage,
  onOpenFile,
  onOpenURL,
  onOpenAgent,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onStreamFrame,
  onOpenFileDiff,
  pendingUserQuestion,
  onAnswerUserQuestion,
  onCancelUserQuestion,
}: {
  pane: ConversationPaneID;
  ownerTabId?: string;
  registerComposerFocusTarget?: UIRegisterFocusTargetV1;
  thread: Thread;
  active: boolean;
  activeContextCwd?: string;
  streamStatus?: TurnStreamStatus;
  editingMessage?: { turnID: string; itemID: string; submitting: boolean };
  onActivate: () => void;
  onClose: () => void;
  onBodyRef: (node: HTMLElement | null) => void;
  onScroll: (node: HTMLElement) => void;
  onForkMessage: (turnID: string, itemID: string) => void;
  onOpenFile?: (path: string) => void;
  onOpenURL?: (url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => void;
  onOpenAgent?: (agentID: string) => void;
  onEditMessage?: (turnID: string, item: ThreadItem) => void;
  onCancelEditMessage?: () => void;
  onSubmitEditMessage?: (
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void | Promise<void>;
  onStreamFrame: () => void;
  onOpenFileDiff?: (selection: TurnFileDiffSelection) => void;
  pendingUserQuestion?: UserQuestionRequest;
  onAnswerUserQuestion?: (requestID: string, answer: UserQuestionAnswer) => Promise<void>;
  onCancelUserQuestion?: (requestID: string) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const { slots, slotOwner } = useConversationControls();
  const paneTurns = thread.turns ?? [];
  const paneLatestAgentMessageID = latestAgentMessageItemID(paneTurns);
  // The callback props arrive as fresh closures from the split pane's caller.
  // Read them through a ref so the per-turn memo boundary below only receives
  // identity-stable functions that still invoke the latest implementations.
  const liveRef = useRef({
    onForkMessage, onOpenFile, onOpenURL, onOpenAgent, onEditMessage,
    onCancelEditMessage, onSubmitEditMessage, onStreamFrame, onOpenFileDiff,
  });
  liveRef.current = {
    onForkMessage, onOpenFile, onOpenURL, onOpenAgent, onEditMessage,
    onCancelEditMessage, onSubmitEditMessage, onStreamFrame, onOpenFileDiff,
  };
  const handleForkMessage = useCallback((turnID: string, itemID: string) => liveRef.current.onForkMessage(turnID, itemID), []);
  const handleOpenFile = useCallback((path: string) => liveRef.current.onOpenFile?.(path), []);
  const handleOpenURL = useCallback((url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => liveRef.current.onOpenURL?.(url, modifiers), []);
  const handleOpenAgent = useCallback((agentID: string) => liveRef.current.onOpenAgent?.(agentID), []);
  const handleEditMessage = useCallback((turnID: string, item: ThreadItem) => liveRef.current.onEditMessage?.(turnID, item), []);
  const handleCancelEditMessage = useCallback(() => liveRef.current.onCancelEditMessage?.(), []);
  const handleSubmitEditMessage = useCallback((turnID: string, item: ThreadItem, text: string, images: InputImage[], files: InputFile[], contentParts?: MessageContentPart[]) => liveRef.current.onSubmitEditMessage?.(turnID, item, text, images, files, contentParts), []);
  const handleStreamFrame = useCallback(() => liveRef.current.onStreamFrame(), []);
  const handleOpenFileDiff = useCallback((selection: TurnFileDiffSelection) => liveRef.current.onOpenFileDiff?.(selection), []);
  const canEditMessage = onEditMessage !== undefined;
  const pendingQuestion =
    pendingUserQuestion &&
    pendingUserQuestion.thread_id === thread.id &&
    onAnswerUserQuestion &&
    onCancelUserQuestion
      ? {
          request: pendingUserQuestion,
          onAnswerUserQuestion,
          onCancelUserQuestion,
        }
      : undefined;
  const renderPendingQuestionCard = (attachAfterTurn: boolean): JSX.Element | null => {
    if (!pendingQuestion) return null;
    const card = (
      <UserQuestionCard
        key={pendingQuestion.request.request_id}
        request={pendingQuestion.request}
        onAnswer={(answer) =>
          pendingQuestion.onAnswerUserQuestion(pendingQuestion.request.request_id, answer)
        }
        onCancel={() =>
          pendingQuestion.onCancelUserQuestion(pendingQuestion.request.request_id)
        }
      />
    );
    return attachAfterTurn ? <div className="user-question-after-turn">{card}</div> : card;
  };
  const paneReadOnly = Boolean(thread.read_only);

  return (
    <section
      className={`conversation-split-pane${active ? " active" : ""}`}
      data-thread-id={thread.id}
      aria-label={t(
        pane === "secondary" ? "split.forkConversation" : "split.sourceConversation",
      )}
      onPointerDown={onActivate}
    >
      {pane === "secondary" && (
        <button
          className="icon-button conversation-split-close"
          type="button"
          aria-label={t("split.closeRight")}
          onClick={onClose}
        >
          <X className="icon" />
        </button>
      )}
      <div
        ref={onBodyRef}
        className="conversation-split-body"
        data-scroll-fade=""
        onScroll={(event) => onScroll(event.currentTarget)}
      >
        <div className="conversation-width conversation-split-width session-flow">
          <ConversationTurnList
            threadID={thread.id}
              historyCursor={thread.history_cursor}
            turns={paneTurns}
            renderAfterMissingTurn={
              pendingQuestion &&
              !paneTurns.some((turn) => turn.id === pendingQuestion.request.turn_id)
                ? renderPendingQuestionCard(false)
                : null
            }
            renderAfterTurn={(turn) =>
              turn.id === pendingQuestion?.request.turn_id
                ? renderPendingQuestionCard(true)
                : null
            }
            forcedFullTurnIDs={
              [
                ...(editingMessage ? [editingMessage.turnID] : []),
                ...(pendingQuestion ? [pendingQuestion.request.turn_id] : []),
              ]
            }
            renderTurn={(turn) => (
                <SplitPaneTurnView
                  threadID={thread.id}
                  sessionLocator={thread.session_locator}
                  engineID={thread.engine_id}
                  turn={turn}
                  cwd={thread.cwd ?? activeContextCwd}
                  onOpenFile={handleOpenFile}
                  onOpenURL={handleOpenURL}
                  onOpenAgent={handleOpenAgent}
                  latestAgentMessageID={paneLatestAgentMessageID}
                  isLatestTurn={
                    paneTurns[paneTurns.length - 1]?.id === turn.id
                  }
                  onStreamFrame={handleStreamFrame}
                  onForkMessage={handleForkMessage}
                  canEdit={canEditMessage}
                  onEditMessage={handleEditMessage}
                  editingMessage={editingMessage}
                  onCancelEditMessage={handleCancelEditMessage}
                  onSubmitEditMessage={handleSubmitEditMessage}
                  onOpenFileDiff={handleOpenFileDiff}
                  streamStatus={
                    paneTurns[paneTurns.length - 1]?.id === turn.id
                      ? streamStatus
                      : undefined
                  }
                />
            )}
          />
        </div>
      </div>
      {!paneReadOnly && ownerTabId && (
        <slots.Slot name={`${slotOwner}/application.composer`} context={{ variant: "dock", pane, ownerTabId, registerFocusTarget: registerComposerFocusTarget }} />
      )}
    </section>
  );
}

type SplitPaneTurnViewProps = {
  threadID: string;
  sessionLocator?: SessionLocatorV1;
  engineID?: string;
  turn: Turn;
  cwd?: string;
  latestAgentMessageID?: string;
  isLatestTurn: boolean;
  canEdit: boolean;
  editingMessage?: { turnID: string; itemID: string; submitting: boolean };
  streamStatus?: TurnStreamStatus;
  onOpenFile?: (path: string) => void;
  onOpenURL?: (url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => void;
  onOpenAgent?: (agentID: string) => void;
  onStreamFrame: () => void;
  onForkMessage: (turnID: string, itemID: string) => void;
  onEditMessage: (turnID: string, item: ThreadItem) => void;
  onCancelEditMessage?: () => void;
  onSubmitEditMessage?: (
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void | Promise<void>;
  onOpenFileDiff?: (selection: TurnFileDiffSelection) => void;
};

// Memo boundary for the turn tree, mirroring PaneTurnView in
// CachedConversationPanes: server events rebuild the thread object but keep
// untouched turns referentially identical, so the memoized wrapper lets
// untouched TurnView subtrees bail out of each split-pane re-render. Every
// function prop above is a useCallback reading through liveRef for exactly
// that reason.
const SplitPaneTurnView = memo(function SplitPaneTurnView({
  threadID,
  sessionLocator,
  engineID,
  turn,
  cwd,
  latestAgentMessageID,
  isLatestTurn,
  canEdit,
  editingMessage,
  streamStatus,
  onOpenFile,
  onOpenURL,
  onOpenAgent,
  onStreamFrame,
  onForkMessage,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onOpenFileDiff,
}: SplitPaneTurnViewProps): JSX.Element {
  return (
    <TurnView
      threadID={threadID}
      sessionLocator={sessionLocator}
      engineID={engineID}
      turn={turn}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onOpenURL={onOpenURL}
      onOpenAgent={onOpenAgent}
      latestAgentMessageID={latestAgentMessageID}
      isLatestTurn={isLatestTurn}
      onStreamFrame={onStreamFrame}
      onForkMessage={onForkMessage}
      onEditMessage={canEdit ? onEditMessage : undefined}
      editingMessage={editingMessage}
      onCancelEditMessage={onCancelEditMessage}
      onSubmitEditMessage={onSubmitEditMessage}
      onOpenFileDiff={onOpenFileDiff}
      streamStatus={streamStatus}
    />
  );
});
