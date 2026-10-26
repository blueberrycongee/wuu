import { Tooltip } from "./Tooltip";
import { X } from "./WuuIcons";
import type {
  InputFile,
  InputImage,
  MessageContentPart,
  Thread,
  ThreadItem,
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
        <Tooltip asChild content={t("split.closeRight")}><button
          className="icon-button conversation-split-close"
          type="button"
          aria-label={t("split.closeRight")}
          onClick={onClose}
        >
          <X className="icon" />
        </button></Tooltip>
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
                <TurnView
                  threadID={thread.id}
                  engineID={thread.engine_id}
                  turn={turn}
                  cwd={thread.cwd ?? activeContextCwd}
                  onOpenFile={onOpenFile}
                  onOpenURL={onOpenURL}
                  onOpenAgent={onOpenAgent}
                  latestAgentMessageID={paneLatestAgentMessageID}
                  isLatestTurn={
                    paneTurns[paneTurns.length - 1]?.id === turn.id
                  }
                  onStreamFrame={onStreamFrame}
                  onForkMessage={onForkMessage}
                  onEditMessage={
                    onEditMessage
                      ? (turnID, item) => onEditMessage(turnID, item)
                      : undefined
                  }
                  editingMessage={editingMessage}
                  onCancelEditMessage={onCancelEditMessage}
                  onSubmitEditMessage={
                    onSubmitEditMessage
                      ? (turnID, item, text, images, files, contentParts) =>
                          onSubmitEditMessage(turnID, item, text, images, files, contentParts)
                      : undefined
                  }
                  onOpenFileDiff={onOpenFileDiff}
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
        <slots.Slot name={`${slotOwner}/application.composer`} context={{ variant: "dock", pane, ownerTabId }} />
      )}
    </section>
  );
}
