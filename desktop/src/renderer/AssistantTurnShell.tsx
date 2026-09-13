import { ChevronRight } from "lucide-react";
import {
  type SyntheticEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  ThreadItem,
  Turn,
} from "../shared/protocol";
import type {
  AssistantTurnDisplay,
  TurnEntry,
  TurnProcessPreview,
} from "./AssistantTurnDisplay";
import { CollapsibleDetails } from "./CollapsibleMotion";
import { ThreadItemView } from "./ThreadItemView";
import { LightweightStreamingText } from "./LightweightStreamingText";
import { useLiveTextWave } from "./LiveTextWave";
import { streamFieldValue } from "./ThreadItemText";
import { StreamReconnectNotice, TurnEventNotice } from "./TurnNotice";
import { turnEventForItem } from "./TurnEvents";
import {
  clearPausedTurnElapsed,
  formatChineseDuration,
  formatDuration,
  pausedTurnElapsed,
  recordPausedTurnElapsed,
  useLiveNow,
} from "./TurnProgress";
import { ProcessSurface, ProcessSurfaceMascot } from "./ProcessSurface";
import { turnProgressContent } from "./TurnViewHelpers";
import { collectTurnSources } from "./ToolActivityHelpers";
import { TurnSourcesRow } from "./TurnSourcesRow";
import {
  AUTO_FOLLOW_NESTED_SCROLL_ATTR,
  useAutoFollowScrollContainer,
} from "./AutoFollowScroll";
import { AnimatedProcessText } from "./ProcessTextMotion";
import { useConversationRenderActive } from "./ConversationRenderActivity";
import { translateCurrent as translate, useI18n } from "./i18n";
import {
  collectTurnArtifacts,
  TurnEndArtifactOutputs,
  TurnInlineArtifactOutputs,
} from "./ArtifactOutputs";

const recoveredTurnStartedAt = new Map<string, number>();
const MAX_RECOVERED_TURN_STARTS = 1_000;

function parseTurnTimestampMs(value: string | null | undefined): number {
  return value ? Date.parse(value) : NaN;
}

function recoveredTurnStart(turnID: string): number {
  const existing = recoveredTurnStartedAt.get(turnID);
  if (existing !== undefined) {
    return existing;
  }
  const startedAt = Date.now();
  recoveredTurnStartedAt.set(turnID, startedAt);
  if (recoveredTurnStartedAt.size > MAX_RECOVERED_TURN_STARTS) {
    const oldestTurnID = recoveredTurnStartedAt.keys().next().value;
    if (oldestTurnID !== undefined) {
      recoveredTurnStartedAt.delete(oldestTurnID);
    }
  }
  return startedAt;
}

export function resetRecoveredTurnStarts(): void {
  recoveredTurnStartedAt.clear();
}

export function AssistantTurnShell({
  turn,
  display,
  cwd,
  onOpenFile,
  onOpenAgent,
  actionableAgentMessageID,
  latestAgentMessageID,
  animateCompletionActions,
  onStreamFrame,
  onForkMessage,
  onCollapseComplete,
  trailingContent,
  editSummaryCard,
}: {
  turn: Turn;
  display: AssistantTurnDisplay;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenAgent?: (agentID: string) => void;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  animateCompletionActions?: boolean;
  onStreamFrame: () => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
  onCollapseComplete?: () => void;
  /**
   * Turn-owned output rendered at the end of the turn content region (after
   * the answer body). The turn-level governance notices (event chips, stream
   * reconnect) stay outside the shell; anything passed here belongs to the
   * turn's own content.
   */
  trailingContent?: JSX.Element;
  /**
   * Turn-owned output attached inside the actionable answer message (between
   * its text and its action bar). Mounted by the renderer that actually
   * paints the message — the display-builder filter never renders JSX.
   */
  editSummaryCard?: JSX.Element;
}): JSX.Element {
  const processEntries = display.entries.filter(
    (entry) => entry.position === "process",
  );
  const answerEntries = display.entries.filter(
    (entry) => entry.position === "answer",
  );
  const artifacts = useMemo(() => collectTurnArtifacts(turn), [turn]);
  // Sources derive from the full turn — web_search and web_fetch happen
  // in the process region, but the source affordance belongs beside the
  // process header so it reads as turn metadata instead of extra answer
  // content. Dedupe by host is handled inside collectTurnSources so a
  // burst of hits on docs.anthropic.com still produces a single icon.
  // process_group entries wrap several raw items under one .items array,
  // so we flatten entries.items ?? [entry.item] before feeding the helper.
  const turnSources = useMemo(
    () =>
      collectTurnSources(
        display.entries.flatMap((entry) => entry.items ?? [entry.item]),
      ),
    [display.entries],
  );
  const handleOpenSource = useCallback((url: string): void => {
    if (typeof window !== "undefined") {
      void window.wuu?.openExternal?.(url);
    }
  }, []);

  // An in_progress turn always shows the process header, even before the
  // first server item arrives (the optimistic placeholder right after
  // send). Without this the shell mounts as an empty box and the user
  // faces a bare hairline with no label or timer until the first item —
  // exactly the dead-air moment the optimistic turn exists to fill. The
  // pre-item label is the neutral "正在处理" (never "正在思考": whether
  // the model thinks is unknown until a reasoning item actually arrives).
  const hasAnswer = answerEntries.length > 0;
  // The process row also owns the turn-level elapsed summary. Keep its compact
  // header for direct answers that have no commentary/tool records; otherwise
  // those turns lose the duration entirely when the empty process body is
  // omitted.
  const hasProcess =
    processEntries.length > 0 ||
    Boolean(display.latestProcessPreview) ||
    turn.status === "in_progress" ||
    turn.status === "interrupted" ||
    hasAnswer;
  const answerHandoffRequested = answerEntries.some(
    (entry) =>
      entry.item.type === "agent_message" &&
      streamFieldValue(turn.id, entry.item, "text").trim().length > 0,
  );
  const processCollapseRequested = answerHandoffRequested;
  const className = [
    "assistant-turn-shell",
    hasProcess ? "has-process" : "",
    hasAnswer ? "has-answer" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const entryProps = {
    turn,
    cwd,
    onOpenFile,
    onOpenAgent,
    actionableAgentMessageID,
    latestAgentMessageID,
    animateCompletionActions,
    onStreamFrame,
    onForkMessage,
    onCollapseComplete,
    editSummaryCard,
  };

  return (
    <div className={className}>
      {hasProcess ? (
        <TurnProcessFold
          entries={processEntries}
          collapseRequested={processCollapseRequested}
          latestPreview={
            answerHandoffRequested ? undefined : display.latestProcessPreview
          }
          sources={turnSources}
          onOpenSource={handleOpenSource}
          {...entryProps}
        />
      ) : null}
      <TurnInlineArtifactOutputs artifacts={artifacts} cwd={cwd} onOpenFile={onOpenFile} />
      {hasAnswer ? (
        <div className="turn-answer-body">
          {answerEntries.map((entry) => (
            <EntryRenderer key={entry.key} entry={entry} {...entryProps} />
          ))}
        </div>
      ) : null}
      <TurnEndArtifactOutputs artifacts={artifacts} cwd={cwd} onOpenFile={onOpenFile} />
      {trailingContent}
    </div>
  );
}

function TurnProcessFold({
  turn,
  entries,
  collapseRequested,
  latestPreview,
  sources,
  onOpenSource,
  cwd,
  onOpenFile,
  onOpenAgent,
  actionableAgentMessageID,
  latestAgentMessageID,
  onStreamFrame,
  onForkMessage,
  onCollapseComplete,
  editSummaryCard,
}: {
  turn: Turn;
  entries: TurnEntry[];
  collapseRequested: boolean;
  latestPreview?: TurnProcessPreview;
  sources: ReturnType<typeof collectTurnSources>;
  onOpenSource?: (url: string) => void;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenAgent?: (agentID: string) => void;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  onStreamFrame: () => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
  /**
   * Fires once the fold has finished collapsing so the conversation
   * scroll container can re-anchor `scrollTop = scrollHeight`. The
   * fold collapse drops scrollHeight by the fold body's height, and
   * without this callback the browser silently clamps `scrollTop`
   * to the new max, which the user perceives as the scroll bar
   * jumping upward at turn-settle.
   */
  onCollapseComplete?: () => void;
  editSummaryCard?: JSX.Element;
}): JSX.Element {
  const renderActive = useConversationRenderActive();
  const [expanded, setExpanded] = useState(!collapseRequested);
  const handoffHandledRef = useRef(collapseRequested);
  // Once the reader changes the fold manually, that preference owns the
  // rest of this turn. The answer handoff must not take control back.
  const userToggledRef = useRef(false);
  const autoCollapsePendingRef = useRef(false);
  const previousExpanded = useRef(expanded);
  const detailsID = `${turn.id}-process-fold`;

  const parsedStartedAt = parseTurnTimestampMs(turn.started_at);
  const parsedAnswerReadyAt = parseTurnTimestampMs(turn.answer_ready_at);
  const parsedCompletedAt = parseTurnTimestampMs(turn.completed_at);
  const answerReady = Number.isFinite(parsedAnswerReadyAt);
  // Recovered/provider-backed turns do not always carry duration_ms even
  // though their boundary timestamps are present. Treating that omission as
  // zero makes any such completed turn read "under 1 second".
  const settledTimestampDuration =
    Number.isFinite(parsedStartedAt) && Number.isFinite(parsedCompletedAt)
      ? Math.max(0, parsedCompletedAt - parsedStartedAt)
      : undefined;
  const answerReadyDuration =
    Number.isFinite(parsedStartedAt) && answerReady
      ? Math.max(0, parsedAnswerReadyAt - parsedStartedAt)
      : undefined;
  const reportedDuration =
    typeof turn.duration_ms === "number" && Number.isFinite(turn.duration_ms)
      ? Math.max(0, turn.duration_ms)
      : undefined;
  // Both values describe the same boundary, so prefer the larger valid value.
  // This also prevents a stale explicit 0 from masking usable timestamps.
  const settledDuration =
    reportedDuration !== undefined && settledTimestampDuration !== undefined
      ? Math.max(reportedDuration, settledTimestampDuration)
      : reportedDuration ?? settledTimestampDuration;
  // The visible timer stops at answer readiness. Provider cleanup can continue
  // internally, but must not make a completed-looking answer keep aging.
  const completedDuration = answerReadyDuration ?? settledDuration;
  const liveDuration =
    completedDuration === undefined &&
    turn.status === "in_progress";
  // A legacy/recovered in-progress turn can arrive without started_at. Keep
  // one fallback per turn outside the component lifecycle: switching session
  // tabs unmounts this shell, but must not restart the elapsed timer.
  const startedAt = Number.isFinite(parsedStartedAt)
    ? parsedStartedAt
    : liveDuration
      ? recoveredTurnStart(turn.id)
      : Date.now();
  if (Number.isFinite(parsedStartedAt) || !liveDuration) {
    recoveredTurnStartedAt.delete(turn.id);
  }
  const liveNow = useLiveNow(liveDuration && renderActive);
  const liveElapsedMs = liveDuration
    ? Math.max(0, liveNow - startedAt)
    : undefined;
  // Keep the last live elapsed value outside the component lifecycle. A
  // paused turn settles as "interrupted" and its server snapshot usually
  // carries no duration, which would otherwise drop the running timer to
  // nothing; the frozen value lets the paused turn keep showing how long it
  // had been processing (and survives session-tab unmounts like the
  // recovered started_at above).
  if (liveElapsedMs !== undefined) {
    recordPausedTurnElapsed(turn.id, liveElapsedMs);
  } else if (completedDuration !== undefined) {
    clearPausedTurnElapsed(turn.id);
  }
  // Only the frozen value is meaningful for a paused turn: when the server
  // provides a real duration the title already reports it ("用时 …"), so the
  // meta chip would be redundant there.
  const pausedElapsedMs =
    turn.status === "interrupted" && completedDuration === undefined
      ? pausedTurnElapsed(turn.id)
      : undefined;
  const elapsedMs = completedDuration ?? liveElapsedMs ?? pausedElapsedMs ?? 0;
  const processLabel = turnProcessTitle(
    turn,
    elapsedMs,
    completedDuration !== undefined,
    collapseRequested,
    answerReady,
  );
  const metaParts = turnProcessMetaParts(
    turn,
    elapsedMs,
    pausedElapsedMs !== undefined,
    answerReady,
  );

  // A confirmed non-empty final answer is the handoff edge: process work
  // yields immediately so the answer becomes the primary reading surface.
  // Completion only changes the compact status copy; it must not trigger a
  // second layout transition. If the user manually changed the fold before
  // handoff, preserve that intent instead of taking control back.
  useEffect(() => {
    if (!collapseRequested) {
      handoffHandledRef.current = false;
      userToggledRef.current = false;
      autoCollapsePendingRef.current = false;
      return;
    }
    if (handoffHandledRef.current || userToggledRef.current) {
      return;
    }
    handoffHandledRef.current = true;
    autoCollapsePendingRef.current = true;
    setExpanded(false);
  }, [collapseRequested]);

  // Watch the expanded → collapsed transition and fire the callback
  // once the CSS transition has settled (slightly longer than
  // --collapse-motion-duration so the fold height has reached its
  // final value before the caller re-anchors scrollTop). Without
  // this, the browser would silently clamp scrollTop to the new
  // max as scrollHeight drops by the fold body's height, which the
  // user perceives as the scroll bar jumping during answer handoff.
  useEffect(() => {
    if (previousExpanded.current && !expanded) {
      const autoCollapse = autoCollapsePendingRef.current;
      autoCollapsePendingRef.current = false;
      const timeoutId = window.setTimeout(() => {
        if (autoCollapse) {
          onCollapseComplete?.();
        }
      }, 440);
      previousExpanded.current = expanded;
      return () => window.clearTimeout(timeoutId);
    }
    previousExpanded.current = expanded;
    return undefined;
  }, [expanded, onCollapseComplete]);

  // User-initiated toggle: record the intent so later snapshots from the
  // same turn never undo the reader's choice.
  const handleToggle = useCallback(() => {
    userToggledRef.current = true;
    autoCollapsePendingRef.current = false;
    setExpanded((prev) => !prev);
  }, []);

  const hasDetails = entries.length > 0;
  const visiblePreview = expanded ? undefined : latestPreview;
  const hasPreview = Boolean(visiblePreview);
  const previewWaveRef = useLiveTextWave<HTMLSpanElement>(
    turn.status === "in_progress" && hasPreview,
  );
  const activeGrayEntryKey =
    turn.status === "in_progress"
      ? latestActiveGrayProcessEntryKey(entries)
      : undefined;

  const toggleContent = (
    <>
      <span className="turn-process-header">
        <span className="turn-process-title">{processLabel}</span>
        {metaParts.map((part) => (
          <span className="turn-process-meta" key={part}>
            {part}
          </span>
        ))}
      </span>
      {hasPreview ? (
        <span
          className={`turn-process-preview turn-process-preview-${visiblePreview?.kind ?? "process"}${
            turn.status === "in_progress" ? " is-live" : ""
          }`}
        >
          <span className="turn-process-live-dot" aria-hidden />
          <span
            ref={previewWaveRef}
            className={`turn-process-preview-text${
              turn.status === "in_progress" ? " wuu-live-text-wave" : ""
            }`}
            data-text={visiblePreview?.text ?? ""}
          >
            <LightweightStreamingText
              text={visiblePreview?.text ?? ""}
              live={turn.status === "in_progress" && renderActive}
            />
          </span>
        </span>
      ) : null}
    </>
  );

  // The outer element is a plain <div> instead of a native <details>.
// Native <details> closes instantly with no height transition, so the
// moment the turn settles the fold body snaps from full height to zero
// and the message bubble reflows visibly. We drive the open/closed
// state ourselves and animate it through CollapsibleDetails
// (grid-template-rows + opacity + transform). a11y is preserved with
// role="button" + aria-expanded + aria-controls + an Enter/Space
// keyboard handler, matching what <details>/<summary> gave us for free.
return (
    <div
      className={`turn-process-fold${expanded ? " expanded" : " collapsed"}${
        hasDetails ? "" : " no-details"
      }${hasPreview ? " has-preview" : ""}`}
      id={detailsID}
    >
      <div className="turn-process-topline">
        <div
          role="button"
          tabIndex={0}
          aria-expanded={expanded}
          aria-controls={`${detailsID}-body`}
          onClick={handleToggle}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              handleToggle();
            }
          }}
          className="turn-process-toggle"
        >
          {toggleContent}
        </div>
        <TurnSourcesRow sources={sources} onOpen={onOpenSource} />
      </div>
      {hasDetails || hasPreview ? (
        <CollapsibleDetails
          id={`${detailsID}-body`}
          expanded={expanded}
          innerClassName="turn-process-fold-body"
        >
          {hasDetails ? (
            <div className="turn-process-fold-body-inner">
              {entries.map((entry) => (
                <div
                  className={`turn-process-entry turn-process-entry-${entry.kind}`}
                  key={entry.key}
                >
                  <EntryRenderer
                    key={entry.key}
                    entry={entry}
                    activeGray={entry.key === activeGrayEntryKey}
                    turn={turn}
                    cwd={cwd}
                    onOpenFile={onOpenFile}
                    onOpenAgent={onOpenAgent}
                    actionableAgentMessageID={actionableAgentMessageID}
                    latestAgentMessageID={latestAgentMessageID}
                    onStreamFrame={onStreamFrame}
                    onForkMessage={onForkMessage}
                    editSummaryCard={editSummaryCard}
                  />
                </div>
              ))}
            </div>
          ) : null}
        </CollapsibleDetails>
      ) : null}
    </div>
  );
}

function latestActiveGrayProcessEntryKey(entries: TurnEntry[]): string | undefined {
  const latest = entries[entries.length - 1];
  if (!latest || !isGrayProcessEntry(latest)) {
    return undefined;
  }
  return latest.key;
}

function isGrayProcessEntry(entry: TurnEntry): boolean {
  if (entry.kind === "activity" || entry.kind === "process_group") {
    return true;
  }
  return entry.item.type === "reasoning";
}

function EntryRenderer({
  entry,
  activeGray,
  turn,
  cwd,
  onOpenFile,
  onOpenAgent,
  actionableAgentMessageID,
  latestAgentMessageID,
  animateCompletionActions,
  onStreamFrame,
  onForkMessage,
  editSummaryCard,
}: {
  entry: TurnEntry;
  activeGray?: boolean;
  turn: Turn;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenAgent?: (agentID: string) => void;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  animateCompletionActions?: boolean;
  onStreamFrame: () => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
  editSummaryCard?: JSX.Element;
}): JSX.Element | null {
  const { item, kind, streaming } = entry;
  if (kind === "activity" || kind === "process_group") {
    return (
      <ProcessSurface
        processItems={entry.items ?? [item]}
        streaming={streaming}
        active={activeGray}
        provider={turn.model_provider}
        model={turn.model}
        renderReasoningItem={(processItem, isStreaming) => (
          <ThreadItemView
            turnID={turn.id}
            turnStatus={turn.status}
            item={processItem}
            cwd={cwd}
            onOpenFile={onOpenFile}
            onOpenAgent={onOpenAgent}
            streaming={isStreaming}
            onStreamFrame={onStreamFrame}
          />
        )}
      />
    );
  }
  if (item.type === "reasoning") {
    // Per the message-display policy (rule 3): reasoning is in the
    // process region, but its content is folded by default. Show a
    // single-line status row ("正在思考" while streaming, "查看思考
    // 过程" once settled) and let the user expand to read the trail.
    // Reasoning never collapses the outer fold on its own, and the
    // user's expanded/collapsed choice persists across re-renders.
    return (
      <ReasoningFold
        item={item}
        streaming={streaming}
        activeGray={activeGray}
        turnID={turn.id}
        turnStatus={turn.status}
        cwd={cwd}
        onOpenFile={onOpenFile}
        onOpenAgent={onOpenAgent}
        onStreamFrame={onStreamFrame}
      />
    );
  }
  if (item.type === "agent_message") {
    return (
      <ThreadItemView
        turnID={turn.id}
        turnStatus={turn.status}
        item={item}
        cwd={cwd}
        onOpenFile={onOpenFile}
        onOpenAgent={onOpenAgent}
        streaming={streaming}
        actionableAgentMessageID={actionableAgentMessageID}
        latestAgentMessageID={latestAgentMessageID}
        animateCompletionActions={animateCompletionActions}
        onStreamFrame={onStreamFrame}
        onForkMessage={onForkMessage}
        editSummaryCard={
          editSummaryCard !== undefined && item.id === actionableAgentMessageID
            ? editSummaryCard
            : undefined
        }
      />
    );
  }
  if (item.type === "stream_reconnect") {
    // The reconnect row renders straight from the item (retry counters and
    // countdown live on it), not through the generic turn-event projection.
    return <StreamReconnectNotice item={item} />;
  }
  if (item.type === "context_compaction" || item.type === "error") {
    const event = turnEventForItem(item);
    return event ? <TurnEventNotice event={event} /> : null;
  }
  return null;
}

function ReasoningFold({
  item,
  streaming,
  activeGray,
  turnID,
  turnStatus,
  cwd,
  onOpenFile,
  onOpenAgent,
  onStreamFrame,
}: {
  item: ThreadItem;
  streaming: boolean;
  activeGray?: boolean;
  turnID: string;
  turnStatus: Turn["status"];
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenAgent?: (agentID: string) => void;
  onStreamFrame: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const label = streaming ? t("process.thinking") : t("process.viewReasoning");
  // Only the latest visible gray process label sweeps while the turn
  // is still running. The label text still reflects this item's own
  // state.
  const textClass = `turn-reasoning-summary-text${
    activeGray ? " is-live-gray wuu-live-text-wave" : ""
  }${streaming ? " is-streaming" : ""}`;
  const waveRef = useLiveTextWave<HTMLSpanElement>(Boolean(activeGray));
  const [open, setOpen] = useState(false);
  const reasoningScroll = useAutoFollowScrollContainer();

  const handleReasoningStreamFrame = useCallback((): void => {
    onStreamFrame();
    reasoningScroll.scheduleScrollToBottom();
  }, [onStreamFrame, reasoningScroll]);

  // When the user opens this fold, land at the latest reasoning. After
  // that, keep following only while the user stays near the bottom.
  const handleToggle = useCallback((event: SyntheticEvent<HTMLDetailsElement>) => {
    const details = event.currentTarget;
    const nextOpen = details.open;
    setOpen(nextOpen);
    if (!nextOpen) return;
    const body = details.querySelector(
      ".turn-reasoning-body",
    ) as HTMLElement | null;
    if (!body) return;
    let settled = false;
    const snapToBottom = (transitionEvent?: Event) => {
      const propertyName = (transitionEvent as TransitionEvent | undefined)
        ?.propertyName;
      if (propertyName && propertyName !== "grid-template-rows") {
        return;
      }
      if (settled) return;
      settled = true;
      body.removeEventListener("transitionend", snapToBottom);
      reasoningScroll.scrollToBottom({ force: true, revealScrollbar: true });
    };
    body.addEventListener("transitionend", snapToBottom);
    // Fallback when transitionend never fires (reduced motion, or the
    // grid already settled before the listener attached).
    window.setTimeout(snapToBottom, 280);
  }, [reasoningScroll]);
  return (
    <details
      className="turn-reasoning-fold"
      open={open}
      onToggle={handleToggle}
    >
      <summary className="turn-reasoning-summary">
        <ProcessSurfaceMascot
          active={Boolean(activeGray)}
          activity="thinking"
        />
        <span
          ref={waveRef}
          className={textClass}
          data-text={label}
        >
          <AnimatedProcessText
            className="turn-reasoning-summary-label"
            text={label}
          />
        </span>
        <ChevronRight
          className="turn-reasoning-chevron icon-xs"
          aria-hidden
        />
      </summary>
      <div className="turn-reasoning-body">
        <div className="turn-reasoning-body-inner">
          <div
            className="turn-reasoning-scroll"
            ref={reasoningScroll.scrollRef}
            {...{ [AUTO_FOLLOW_NESTED_SCROLL_ATTR]: "true" }}
          >
            <ThreadItemView
              turnID={turnID}
              turnStatus={turnStatus}
              item={item}
              cwd={cwd}
              onOpenFile={onOpenFile}
              onOpenAgent={onOpenAgent}
              streaming={streaming}
              onStreamFrame={handleReasoningStreamFrame}
            />
          </div>
        </div>
      </div>
    </details>
  );
}

function turnProcessTitle(
  turn: Turn,
  elapsedMs: number,
  hasKnownDuration: boolean,
  hasFinalText: boolean,
  answerReady: boolean,
): string {
  if (answerReady) {
    return hasKnownDuration
      ? taskFinishedLabel(elapsedMs)
      : translate("task.status.completed");
  }
  if (turn.status === "completed" || turn.status === "interrupted") {
    if (!hasKnownDuration) {
      return turn.status === "interrupted"
        ? turnProgressContent(turn, elapsedMs, hasFinalText).label
        : translate("task.status.completed");
    }
    return taskFinishedLabel(elapsedMs);
  }
  return turnProgressContent(turn, elapsedMs, hasFinalText).label;
}

function taskFinishedLabel(elapsedMs: number): string {
  if (elapsedMs < 1000) return translate("process.finishedUnderSecond");
  return translate("process.finishedIn", { duration: formatChineseDuration(elapsedMs) });
}

function turnProcessMetaParts(
  turn: Turn,
  elapsedMs: number,
  showPausedElapsed: boolean,
  answerReady: boolean,
): string[] {
  const parts: string[] = [];
  if (turn.status === "in_progress" && !answerReady) {
    parts.push(formatDuration(elapsedMs));
  } else if (turn.status === "interrupted" && showPausedElapsed) {
    parts.push(formatDuration(elapsedMs));
  }
  return parts;
}
