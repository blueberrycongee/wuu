/// <reference path="../shared/jsx-compat.d.ts" />

import { useLayoutEffect, useRef } from "react";
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
import { TurnArtifactSummaryPresentation } from "./ArtifactOutputs";
import { ArtifactThreadContext } from "./ArtifactPreviewContext";
import { TurnEditSummaryPresentation } from "./TurnEditSummaryPresentation";
import { ENABLE_TURN_ARTIFACT_SUMMARY, ENABLE_TURN_EDIT_SUMMARY } from "./FeatureFlags";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { TurnEventNotice, StreamStatusNotice, StreamReconnectNotice } from "./TurnNotice";
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
  onOpenURL?: (url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => void;
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
  ) => void | Promise<void>;
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
    <ArtifactThreadContext.Provider value={threadId}>
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
    </ArtifactThreadContext.Provider>
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
  onOpenURL,
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
  const turnElementRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const node = turnElementRef.current;
    if (!node || typeof ResizeObserver === "undefined" || !node.checkVisibility) return;
    // Recent turns render eagerly, so Chromium has no remembered auto size
    // when they leave the recent band. Retain their real height instead of
    // falling back to 260px and shifting the reader on the next queued turn.
    const observer = new ResizeObserver(entries => {
      if (!node.checkVisibility({ contentVisibilityAuto: true })) return;
      const height = entries[0]?.contentRect.height;
      if (!height) return;
      const value = `auto ${height}px`;
      if (node.style.containIntrinsicBlockSize !== value) node.style.containIntrinsicBlockSize = value;
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
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
  const reconnectItems = turn.items.filter((item) => item.type === "stream_reconnect");
  const visibleStreamStatus = isLatestTurn && turn.status === "in_progress" && reconnectItems.length === 0
    ? streamStatus
    : undefined;
  const previousStreamStatus = useRef<TurnStreamStatus | undefined>(undefined);
  useLayoutEffect(() => {
    if (turn.status === "in_progress") previousStreamStatus.current = visibleStreamStatus;
  }, [turn.status, visibleStreamStatus]);
  // Ordinary streaming has no transport notice. Preserve space only for a
  // notice that was actually visible at settlement, using its real layout.
  const retainedStreamStatus = isLatestTurn && turn.status !== "in_progress" && !streamStatus
    ? previousStreamStatus.current
    : undefined;
  const renderedStreamStatus = visibleStreamStatus ?? retainedStreamStatus;
  const retryMessage = userItems.at(-1);
  const event = turnEventForTurn(turn);
  const incomplete = turn.status === "failed" || turn.status === "interrupted";
  const editSummary = ENABLE_TURN_EDIT_SUMMARY ? (
    <TurnEditSummaryPresentation
      turn={turn}
      isLatestTurn={Boolean(isLatestTurn)}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onOpenFileDiff={onOpenFileDiff}
      onCollapseComplete={onCollapseComplete}
    />
  ) : undefined;
  const artifactSummary = ENABLE_TURN_ARTIFACT_SUMMARY ? (
    <TurnArtifactSummaryPresentation
      turn={turn}
      isLatestTurn={Boolean(isLatestTurn)}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onCollapseComplete={onCollapseComplete}
    />
  ) : undefined;
  const outputSummary = editSummary || artifactSummary ? (
    <>
      {editSummary}
      {artifactSummary}
    </>
  ) : undefined;

  return (
    <section
      className="turn"
      ref={turnElementRef}
      data-wuu-component="turn"
      id={turnAnchorID(turn.id)}
      data-turn-id={turn.id}
      data-turn-status={turn.status}
      data-latest-turn={isLatestTurn || undefined}
    >
      {userItems.map((item) => renderThreadItem(item, false))}
      {assistantDisplay ? (
        <AssistantTurnShell
          turn={turn}
          display={assistantDisplay}
          cwd={cwd}
          onOpenFile={onOpenFile}
          onOpenURL={onOpenURL}
          actionableAgentMessageID={actionableAgentMessageID}
          latestAgentMessageID={latestAgentMessageID}
          animateCompletionActions={animateCompletionActions}
          onStreamFrame={onStreamFrame}
          onForkMessage={onForkMessage}
          onCollapseComplete={onCollapseComplete}
          onOpenAgent={onOpenAgent}
          editSummaryCard={
            !incomplete && runActionAttachedToMessage ? outputSummary : undefined
          }
          trailingContent={
            !incomplete && !runActionAttachedToMessage ? outputSummary : undefined
          }
        />
      ) : null}
      {reconnectItems.map((item) => (
        <StreamReconnectNotice
          key={item.id}
          item={item}
          error={turn.error}
          onRetry={isLatestTurn && turn.status === "failed" && retryMessage && onEditMessage && onSubmitEditMessage
            ? () => onSubmitEditMessage(
                turn.id, retryMessage, retryMessage.input_text ?? retryMessage.text ?? "",
                retryMessage.images ?? [], retryMessage.files ?? [], retryMessage.content_parts,
              )
            : undefined}
        />
      ))}
      {renderedStreamStatus ? (
        <div className={visibleStreamStatus ? undefined : "turn-stream-status-spacer"} aria-hidden={!visibleStreamStatus || undefined}>
          <StreamStatusNotice status={renderedStreamStatus} />
        </div>
      ) : null}
      {event ? <TurnEventNotice event={event} /> : null}
      {incomplete ? outputSummary : null}
    </section>
  );
}
