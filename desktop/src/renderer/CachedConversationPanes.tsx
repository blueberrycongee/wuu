import {
  Profiler,
  memo,
  useCallback,
  useLayoutEffect,
  useRef,
  type ReactNode,
} from "react";
import type {
  Agent,
  InputFile,
  InputImage,
  MessageContentPart,
  Thread,
  ThreadItem,
  Turn,
  UserQuestionAnswer,
  UserQuestionRequest,
} from "../shared/protocol";
import type { TurnStreamStatus } from "./AppState";
import { ConversationTurnList } from "./ConversationTurnList";
import {
  ContextCompositionCard,
  type ContextCompositionEntry,
} from "./ContextCompositionCard";
import { ForkWorktreeNotice } from "./ForkWorktreeNotice";
import {
  InstructionFilesCard,
  type InstructionFilesEntry,
} from "./InstructionFilesCard";
import { TurnView } from "./TurnView";
import { UserQuestionCard } from "./UserQuestionCard";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { latestAgentMessageLocation } from "./TurnViewHelpers";
import type { HistoryMessageEditState } from "./ConversationHistoryActions";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { PluginConversationCards } from "./plugins/PluginConversationCards";
import { ConversationRenderActivityProvider } from "./ConversationRenderActivity";
import {
  markSessionSwitch,
  recordSessionSwitchPaneRender,
  SESSION_SWITCH_PERF_ENABLED,
} from "./SessionSwitchPerformance";

export type CachedConversationPanesProps = {
  threadIDs: string[];
  threadsByID: ReadonlyMap<string, Thread>;
  activeThreadID?: string;
  activeContextCwd?: string;
  contextCompositionEntries: ContextCompositionEntry[];
  instructionFilesEntries: InstructionFilesEntry[];
  historyMessageEdit?: HistoryMessageEditState;
  onStreamFrame: () => void;
  onCollapseComplete: () => void;
  onDismissContextComposition: (id: string) => void;
  onDismissInstructions: (id: string) => void;
  canEditThreadMessage: (thread: Thread) => boolean;
  onForkMessage: (thread: Thread, turnID: string, itemID: string) => void;
  onOpenFile?: (thread: Thread, path: string) => void;
  onOpenAgent: (agent: Agent) => void;
  onEditMessage: (thread: Thread, turnID: string, item: ThreadItem) => void;
  onCancelEditMessage: () => void;
  onSubmitEditMessage: (
    thread: Thread,
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void | Promise<void>;
  onOpenFileDiff: (thread: Thread, selection: TurnFileDiffSelection) => void;
  turnStreamStatus: Record<string, TurnStreamStatus>;
  pendingUserQuestion?: UserQuestionRequest;
  onAnswerUserQuestion?: (requestID: string, answer: UserQuestionAnswer) => Promise<void>;
  onCancelUserQuestion?: (requestID: string) => Promise<void>;
};

export const CachedConversationPanes = memo(function CachedConversationPanes({
  threadIDs,
  threadsByID,
  activeThreadID,
  activeContextCwd,
  contextCompositionEntries,
  instructionFilesEntries,
  historyMessageEdit,
  onStreamFrame,
  onCollapseComplete,
  onDismissContextComposition,
  onDismissInstructions,
  canEditThreadMessage,
  onForkMessage,
  onOpenFile,
  onOpenAgent,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onOpenFileDiff,
  turnStreamStatus,
  pendingUserQuestion,
  onAnswerUserQuestion,
  onCancelUserQuestion,
}: CachedConversationPanesProps): JSX.Element {
  return (
    <div className="cached-conversation-panes">
      {threadIDs.map((threadID) => {
        const thread = threadsByID.get(threadID);
        if (!thread) return null;
        const isActive = threadID === activeThreadID;
        return (
          <CachedConversationPane
            key={threadID}
            thread={thread}
            isActive={isActive}
            activeContextCwd={activeContextCwd}
            contextCompositionEntries={contextCompositionEntries}
            instructionFilesEntries={instructionFilesEntries}
            historyMessageEdit={
              historyMessageEdit?.threadID === threadID
                ? historyMessageEdit
                : undefined
            }
            onStreamFrame={onStreamFrame}
            onCollapseComplete={onCollapseComplete}
            onDismissContextComposition={onDismissContextComposition}
            onDismissInstructions={onDismissInstructions}
            canEditThreadMessage={canEditThreadMessage}
            onForkMessage={onForkMessage}
            onOpenFile={onOpenFile}
            onOpenAgent={onOpenAgent}
            onEditMessage={onEditMessage}
            onCancelEditMessage={onCancelEditMessage}
            onSubmitEditMessage={onSubmitEditMessage}
            onOpenFileDiff={onOpenFileDiff}
            turnStreamStatus={turnStreamStatus}
            pendingUserQuestion={pendingUserQuestion}
            onAnswerUserQuestion={onAnswerUserQuestion}
            onCancelUserQuestion={onCancelUserQuestion}
          />
        );
      })}
    </div>
  );
});

type CachedConversationPaneProps = Omit<
  CachedConversationPanesProps,
  "threadIDs" | "threadsByID" | "activeThreadID"
> & {
  thread: Thread;
  isActive: boolean;
};

const CachedConversationPane = memo(function CachedConversationPane({
  thread,
  isActive,
  activeContextCwd,
  contextCompositionEntries,
  instructionFilesEntries,
  historyMessageEdit,
  onStreamFrame,
  onCollapseComplete,
  onDismissContextComposition,
  onDismissInstructions,
  canEditThreadMessage,
  onForkMessage,
  onOpenFile,
  onOpenAgent,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onOpenFileDiff,
  turnStreamStatus,
  pendingUserQuestion,
  onAnswerUserQuestion,
  onCancelUserQuestion,
}: CachedConversationPaneProps): JSX.Element {
  const threadRef = useRef(thread);
  threadRef.current = thread;
  const wasActiveRef = useRef(isActive);
  useLayoutEffect(() => {
    if (!SESSION_SWITCH_PERF_ENABLED) {
      return undefined;
    }
    if (isActive && !wasActiveRef.current) {
      markSessionSwitch(thread.id, "pane-layout-effect");
      const frame = window.requestAnimationFrame(() => {
        markSessionSwitch(thread.id, "next-animation-frame");
      });
      wasActiveRef.current = isActive;
      return () => window.cancelAnimationFrame(frame);
    }
    wasActiveRef.current = isActive;
    return undefined;
  }, [isActive, thread.id]);
  const handleOpenFile = useCallback(
    (path: string) => onOpenFile?.(threadRef.current, path),
    [onOpenFile],
  );
  // Memo-safe callbacks: PaneTurnView is React.memo'd, so every function prop
  // must keep a stable identity across re-renders or the bailout never fires.
  // They read the live thread through threadRef (same pattern as
  // handleOpenFile) instead of closing over the render-scope thread.
  const handleOpenAgentByID = useCallback(
    (agentID: string) => {
      const agent = threadRef.current.child_agents?.find(
        (candidate) => candidate.id === agentID,
      );
      if (agent) {
        void onOpenAgent(agent);
      }
    },
    [onOpenAgent],
  );
  const handleForkMessage = useCallback(
    (turnID: string, itemID: string) =>
      onForkMessage(threadRef.current, turnID, itemID),
    [onForkMessage],
  );
  const handleEditMessage = useCallback(
    (turnID: string, item: ThreadItem) =>
      onEditMessage(threadRef.current, turnID, item),
    [onEditMessage],
  );
  const handleSubmitEditMessage = useCallback(
    (
      turnID: string,
      item: ThreadItem,
      text: string,
      images: InputImage[],
      files: InputFile[],
      contentParts?: MessageContentPart[],
    ) =>
      onSubmitEditMessage(
        threadRef.current,
        turnID,
        item,
        text,
        images,
        files,
        contentParts,
      ),
    [onSubmitEditMessage],
  );
  const handleOpenFileDiffSelection = useCallback(
    (selection: TurnFileDiffSelection) =>
      onOpenFileDiff(threadRef.current, selection),
    [onOpenFileDiff],
  );
  const threadTurns = thread.turns ?? [];
  // Location (owner turn + item) lets renderTurn scope latestAgentMessageID to
  // the one turn that can match it, so a new agent message re-renders only
  // the previous and next owner turns instead of every PaneTurnView.
  const latestAgentLocation = latestAgentMessageLocation(threadTurns);
  const threadContextEntries = contextCompositionEntries.filter(
    (entry) => entry.threadID === thread.id,
  );
  const turnIDs = new Set(threadTurns.map((turn) => turn.id));
  const entriesBeforeTurns = threadContextEntries.filter(
    (entry) => !entry.afterTurnID,
  );
  const entriesAfterMissingTurn = threadContextEntries.filter(
    (entry) => entry.afterTurnID && !turnIDs.has(entry.afterTurnID),
  );
  const entriesByAfterTurnID = new Map<string, ContextCompositionEntry[]>();
  for (const entry of threadContextEntries) {
    if (!entry.afterTurnID || !turnIDs.has(entry.afterTurnID)) {
      continue;
    }
    const existing = entriesByAfterTurnID.get(entry.afterTurnID) ?? [];
    existing.push(entry);
    entriesByAfterTurnID.set(entry.afterTurnID, existing);
  }
  const renderContextEntry = (entry: ContextCompositionEntry) => (
    <ContextCompositionCard
      entry={entry}
      key={entry.id}
      onDismiss={onDismissContextComposition}
    />
  );
  // Ask-user interruptions render inline after the turn that paused for them,
  // not above the composer dock.
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
  const threadInstructionEntries = instructionFilesEntries.filter(
    (entry) => entry.threadID === thread.id,
  );
  const threadInstructionCards = threadInstructionEntries.map((entry) => (
    <InstructionFilesCard
      entry={entry}
      key={entry.id}
      onDismiss={onDismissInstructions}
    />
  ));
  const forkWorktreeNotice =
    thread.worktree && thread.forked_from_id ? (
      <ForkWorktreeNotice thread={thread} />
    ) : null;
  const latestTurn = threadTurns[threadTurns.length - 1];
  const latestTurnStreamStatus = latestTurn
    ? turnStreamStatus[latestTurn.id]
    : undefined;
  return (
    <ConversationRenderActivityProvider active={isActive}>
      <SessionSwitchProfiler threadID={thread.id}>
        <div
          aria-hidden={isActive ? undefined : true}
          className="cached-conversation-pane"
          data-active={isActive}
          data-thread-id={thread.id}
          inert={isActive ? undefined : true}
        >
          <div className="conversation-width session-flow">
            <ConversationTurnList
              historyCursor={thread.history_cursor}
              threadID={thread.id}
              turns={threadTurns}
              renderBeforeTurns={[
                ...entriesBeforeTurns.map(renderContextEntry),
              ]}
              renderAfterMissingTurn={
                <>
                  {entriesAfterMissingTurn.map(renderContextEntry)}
                  {forkWorktreeNotice}
                  {threadInstructionCards}
                  <PluginConversationCards
                    host={desktopPluginHost}
                    threadId={thread.id}
                    onStreamFrame={onStreamFrame}
                  />
                  {pendingQuestion && !turnIDs.has(pendingQuestion.request.turn_id)
                    ? renderPendingQuestionCard(false)
                    : null}
                </>
              }
              renderAfterTurn={(turn) => (
                <>
                  {(entriesByAfterTurnID.get(turn.id) ?? []).map(renderContextEntry)}
                  {turn.id === pendingQuestion?.request.turn_id
                    ? renderPendingQuestionCard(true)
                    : null}
                </>
              )}
              forcedFullTurnIDs={
                [
                  ...(historyMessageEdit ? [historyMessageEdit.turnID] : []),
                  ...(pendingQuestion ? [pendingQuestion.request.turn_id] : []),
                ]
              }
              autoLoadEarlier={isActive}
              renderTurn={(turn) => (
                <PaneTurnView
                  turn={turn}
                  cwd={thread.cwd ?? activeContextCwd}
                  onOpenFile={onOpenFile ? handleOpenFile : undefined}
                  onOpenAgent={handleOpenAgentByID}
                  latestAgentMessageID={
                    latestAgentLocation?.turnID === turn.id
                      ? latestAgentLocation.itemID
                      : undefined
                  }
                  isLatestTurn={latestTurn?.id === turn.id}
                  onStreamFrame={onStreamFrame}
                  onCollapseComplete={onCollapseComplete}
                  onForkMessage={handleForkMessage}
                  canEdit={canEditThreadMessage(thread)}
                  onEditMessage={handleEditMessage}
                  editingMessage={historyMessageEdit}
                  onCancelEditMessage={onCancelEditMessage}
                  onSubmitEditMessage={handleSubmitEditMessage}
                  onOpenFileDiff={handleOpenFileDiffSelection}
                  streamStatus={
                    latestTurn?.id === turn.id ? latestTurnStreamStatus : undefined
                  }
                />
              )}
            />
          </div>
        </div>
      </SessionSwitchProfiler>
    </ConversationRenderActivityProvider>
  );
}, reuseCachedConversationPane);

type SessionSwitchProfilerProps = {
  threadID: string;
  children: ReactNode;
};

function SessionSwitchProfiler({
  threadID,
  children,
}: SessionSwitchProfilerProps): ReactNode {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return children;
  }
  return (
    <Profiler
      id={`conversation-pane:${threadID}`}
      onRender={(_id, phase, actualDuration, baseDuration) =>
        recordSessionSwitchPaneRender(threadID, phase, actualDuration, baseDuration)
      }
    >
      {children}
    </Profiler>
  );
}

function reuseCachedConversationPane(
  previous: CachedConversationPaneProps,
  next: CachedConversationPaneProps,
): boolean {
  // A hidden pane only needs the newest props when it becomes visible again.
  // Freezing it here prevents background thread events from reconciling a full
  // retained conversation tree. The false -> true transition always renders
  // and receives the latest thread snapshot from the parent.
  if (!previous.isActive && !next.isActive) {
    return true;
  }

  const previousKeys = Object.keys(previous) as Array<
    keyof CachedConversationPaneProps
  >;
  if (previousKeys.length !== Object.keys(next).length) {
    return false;
  }
  for (const key of previousKeys) {
    if (previous[key] !== next[key]) {
      return false;
    }
  }
  return true;
}

type PaneTurnViewProps = {
  turn: Turn;
  cwd?: string;
  latestAgentMessageID?: string;
  isLatestTurn: boolean;
  canEdit: boolean;
  editingMessage?: HistoryMessageEditState;
  streamStatus?: TurnStreamStatus;
  onOpenFile?: (path: string) => void;
  onOpenAgent: (agentID: string) => void;
  onStreamFrame: () => void;
  onCollapseComplete: () => void;
  onForkMessage: (turnID: string, itemID: string) => void;
  onEditMessage: (turnID: string, item: ThreadItem) => void;
  onCancelEditMessage: () => void;
  onSubmitEditMessage: (
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void | Promise<void>;
  onOpenFileDiff: (selection: TurnFileDiffSelection) => void;
};

// Memo boundary for the turn tree. Server events (item/started, item/
// completed, turn/completed) rebuild the thread object but preserve the
// identity of every turn they did not touch, so a memoized per-turn wrapper
// lets hundreds of untouched TurnView subtrees bail out of the re-render
// instead of reconciling the whole conversation on every event. All props
// must be value-compared or identity-stable — every callback the pane passes
// is a useCallback reading through threadRef for exactly that reason.
const PaneTurnView = memo(function PaneTurnView({
  turn,
  cwd,
  latestAgentMessageID,
  isLatestTurn,
  canEdit,
  editingMessage,
  streamStatus,
  onOpenFile,
  onOpenAgent,
  onStreamFrame,
  onCollapseComplete,
  onForkMessage,
  onEditMessage,
  onCancelEditMessage,
  onSubmitEditMessage,
  onOpenFileDiff,
}: PaneTurnViewProps): JSX.Element {
  return (
    <TurnView
      turn={turn}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onOpenAgent={onOpenAgent}
      latestAgentMessageID={latestAgentMessageID}
      isLatestTurn={isLatestTurn}
      onStreamFrame={onStreamFrame}
      onCollapseComplete={onCollapseComplete}
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
