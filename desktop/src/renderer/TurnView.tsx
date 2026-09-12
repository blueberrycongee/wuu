/// <reference path="../shared/jsx-compat.d.ts" />

import { useRef } from "react";
import type {
  InputFile,
  InputImage,
  MessageContentPart,
  ThreadItem,
  Turn,
} from "../shared/protocol";
import { buildAssistantTurnDisplay } from "./AssistantTurnDisplay";
import { useAssistantTurnPresentation } from "./AssistantTurnPresentation";
import { AssistantTurnShell } from "./AssistantTurnShell";
import { ThreadItemView } from "./ThreadItemView";
import { TurnEditSummaryPresentation } from "./TurnEditSummaryPresentation";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { TurnEventNotice, StreamStatusNotice } from "./TurnNotice";
import { turnEventForTurn } from "./TurnEvents";
import { isInternalUserNotificationItem } from "./InternalUserNotification";
import { turnIsAnswerReady, type TurnStreamStatus } from "./AppState";
import {
  latestAgentMessageItemID,
  messageFlowAgentMessageItemID,
  scrollToUserMessage,
  turnAnchorID,
} from "./TurnViewHelpers";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { PluginSurface } from "./plugins";

export { latestAgentMessageItemID, scrollToUserMessage };

export type TurnViewProps = {
  turn: Turn;
  threadID?: string;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenAgent?: (agentID: string) => void;
  latestAgentMessageID?: string;
  onStreamFrame: () => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
  onEditMessage?: (turnID: string, item: ThreadItem) => void;
  editingMessage?: { turnID: string; itemID: string; submitting: boolean };
  onCancelEditMessage?: () => void;
  onSubmitEditMessage?: (
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void;
  onCollapseComplete?: () => void;
  onOpenFileDiff?: (selection: TurnFileDiffSelection) => void;
  streamStatus?: TurnStreamStatus;
  isLatestTurn?: boolean;
};

export function TurnView(props: TurnViewProps): JSX.Element | null {
  const projectedTurn = projectTurnForPresentation(props.turn);
  if (props.turn.items.length > 0 && projectedTurn.items.length === 0) {
    return null;
  }
  const threadId = props.threadID ?? desktopPluginHost.getActiveConversationThreadId();
  return (
    <PluginSurface
      host={desktopPluginHost}
      id="conversation.timeline"
      context={{
        version: 1,
        turns: [projectedTurn],
        awaiting: false,
        interrupted: projectedTurn.status === "interrupted",
        ...(threadId === undefined ? {} : { threadId }),
        cwd: props.cwd,
        actions: {
          openFile: props.onOpenFile,
          openAgent: props.onOpenAgent,
          forkMessage: props.onForkMessage,
          editMessage: props.onEditMessage,
        },
      }}
      fallback={<TurnContent {...props} turn={projectedTurn} />}
    />
  );
}

function projectTurnForPresentation(turn: Turn): Turn {
  const items = turn.items.filter(
    (item) =>
      item.type !== "user_message" || !isInternalUserNotificationItem(item),
  );
  return items.length === turn.items.length ? turn : { ...turn, items };
}

function TurnContent({
  turn,
  cwd,
  onOpenFile,
  onOpenAgent,
  latestAgentMessageID,
  onStreamFrame,
  onForkMessage,
  onEditMessage,
  editingMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onCollapseComplete,
  onOpenFileDiff,
  streamStatus,
  isLatestTurn,
}: TurnViewProps): JSX.Element {
  // Remember live submissions so completion actions can animate without
  // replaying their entrance when a finished conversation is opened.
  const startedAsLiveSubmissionRef = useRef(
    Boolean(
      isLatestTurn &&
        turn.status === "in_progress" &&
        turn.items.some((item) => item.type === "user_message"),
    ),
  );
  const animateCompletionActions = Boolean(
    isLatestTurn && startedAsLiveSubmissionRef.current,
  );
  const actionableAgentMessageID =
    turn.status === "completed" || turnIsAnswerReady(turn)
      ? messageFlowAgentMessageItemID(turn)
      : undefined;
  // The edit summary card should sit inside the actionable answer message
  // (between its text and its action bar) when that message actually renders
  // an action bar. Otherwise it falls back to its turn-level slot below the
  // assistant shell.
  const actionableAnswerItem = actionableAgentMessageID
    ? turn.items.find(
        (item) =>
          item.id === actionableAgentMessageID &&
          item.type === "agent_message" &&
          item.terminal === true &&
          item.text?.trim(),
      )
    : undefined;
  const runActionAttachedToMessage = actionableAnswerItem != null;

  function renderThreadItem(
    item: ThreadItem,
    streaming: boolean,
    pendingCompanionReasoning?: boolean,
  ): JSX.Element | null {
    return (
      <ThreadItemView
        key={item.id}
        turnID={turn.id}
        turnStatus={turn.status}
        turnStartedAt={turn.started_at}
        item={item}
        cwd={cwd}
        onOpenFile={onOpenFile}
        streaming={streaming}
        pendingCompanionReasoning={pendingCompanionReasoning}
        actionableAgentMessageID={actionableAgentMessageID}
        latestAgentMessageID={latestAgentMessageID}
        animateCompletionActions={animateCompletionActions}
        onStreamFrame={onStreamFrame}
        onForkMessage={onForkMessage}
        onEditMessage={onEditMessage}
        editing={
          editingMessage?.turnID === turn.id && editingMessage.itemID === item.id
        }
        editSubmitting={
          editingMessage?.turnID === turn.id && editingMessage.itemID === item.id
            ? editingMessage.submitting
            : false
        }
        onCancelEditMessage={onCancelEditMessage}
        onSubmitEditMessage={onSubmitEditMessage}
        onOpenAgent={onOpenAgent}
      />
    );
  }

  const userItems = turn.items.filter((item) => item.type === "user_message");
  const rawAssistantDisplay = buildAssistantTurnDisplay(
    turn,
    actionableAgentMessageID,
    renderThreadItem,
  );
  const assistantDisplay = useAssistantTurnPresentation(
    turn.id,
    rawAssistantDisplay,
  );
  const event = turnEventForTurn(turn);
  const incomplete = turn.status === "failed" || turn.status === "interrupted";
  const editSummary = (
    <TurnEditSummaryPresentation
      turn={turn}
      isLatestTurn={Boolean(isLatestTurn)}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onOpenFileDiff={onOpenFileDiff}
      onCollapseComplete={onCollapseComplete}
    />
  );

  return (
    <section
      className="turn"
      data-wuu-component="turn"
      id={turnAnchorID(turn.id)}
      data-turn-id={turn.id}
      data-turn-status={turn.status}
    >
      {userItems.map((item) => renderThreadItem(item, false))}
      {assistantDisplay ? (
        <AssistantTurnShell
          turn={turn}
          display={assistantDisplay}
          cwd={cwd}
          onOpenFile={onOpenFile}
          actionableAgentMessageID={actionableAgentMessageID}
          latestAgentMessageID={latestAgentMessageID}
          animateCompletionActions={animateCompletionActions}
          onStreamFrame={onStreamFrame}
          onForkMessage={onForkMessage}
          onCollapseComplete={onCollapseComplete}
          onOpenAgent={onOpenAgent}
          editSummaryCard={
            !incomplete && runActionAttachedToMessage ? editSummary : undefined
          }
          trailingContent={
            !incomplete && !runActionAttachedToMessage ? editSummary : undefined
          }
        />
      ) : null}
      {isLatestTurn && turn.status === "in_progress" && streamStatus ? (
        <StreamStatusNotice status={streamStatus} />
      ) : null}
      {/*
       * Hold the row the streaming status chip occupied when the live turn
       * settles. Without this subspace, unmounting the chip collapses the
       * turn's height on the completion frame and auto-follow re-pins the
       * already-rendered answer upward. The spacer is only rendered for the
       * turn that mounted live (animateCompletionActions) once it is no
       * longer in_progress, so reloaded history keeps its current spacing
       * and a mid-stream transport gap never flashes a placeholder.
       */}
      {animateCompletionActions && turn.status !== "in_progress" && !streamStatus ? (
        <div className="turn-stream-status-spacer" aria-hidden="true" />
      ) : null}
      {event ? <TurnEventNotice event={event} /> : null}
      {incomplete ? editSummary : null}
    </section>
  );
}
