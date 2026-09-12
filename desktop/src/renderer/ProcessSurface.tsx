import {
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type JSX,
  type SyntheticEvent,
} from "react";
import type { ThreadItem } from "../shared/protocol";
import {
  buildToolActivityProcessSegments,
  type ToolActivityProcessSegment,
} from "./ToolActivityHelpers";
import { ToolActivityTimeline } from "./ToolActivity";
import { ToolActivityPresenter } from "./plugins/ToolActivityPresenter";
import { ConversationProcessPresentation } from "./plugins/ConversationProcessPresentation";
import {
  AUTO_FOLLOW_NESTED_SCROLL_ATTR,
  useAutoFollowScrollContainer,
} from "./AutoFollowScroll";
import { AnimatedProcessText } from "./ProcessTextMotion";
import { useLiveTextWave } from "./LiveTextWave";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import { turnTelemetryStore } from "./TurnTelemetryStore";
import {
  ThinkingTokenCount,
  tokenCountLocale,
  turnTokenCountText,
} from "./ThinkingTokenCount";
import { translateCurrent as translate, useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

/**
 * How long to wait after the fold opens before snapping the reasoning
 * scroll container to the bottom. The fold content animates its height;
 * waiting a touch longer than the default transition duration
 * gives the body height time to settle before we read `scrollHeight`,
 * so the first snap lands on the actual final extent instead of a
 * mid-transition value.
 */
const REASONING_FOLD_OPEN_SNAP_DELAY_MS = 280;

export function ProcessSurfaceMascot({
  active,
  activity = "idle",
  provider,
  model,
}: {
  active: boolean;
  activity?: WuuMascotActivity;
  provider?: string;
  model?: string;
}): JSX.Element | null {
  return (
    <WuuMascot
      className="process-surface-blobatar"
      visible={active}
      size={28}
      provider={provider}
      model={model}
      activity={active ? activity : "idle"}
    />
  );
}

/**
 * Unified render surface for the process region of a single turn.
 *
 * The caller passes every process item for the current group as a flat
 * list. This component keeps one root node across single-tool,
 * multi-tool, reasoning, streaming, and settled states, so the visible
 * gray process row does not remount when the group changes shape.
 */
type ProcessSurfaceProps = {
  /**
   * Flat list of every process-region item for this turn. Order is the
   * wire order. The surface filters by type internally; the caller does
   * not have to split tools from reasoning first.
   */
  processItems: ThreadItem[];
  /**
   * The owning turn, used to subscribe the "正在思考" label's live token
   * counter to that turn's telemetry. Omitted by legacy callers/tests.
   */
  turnID?: string;
  /**
   * True while any process item is still receiving deltas. Drives the
   * tool/reasoning reveal behavior while the fold stays compact until
   * the user opens it.
   */
  streaming: boolean;
  /**
   * True while the surrounding turn is still running. This is visual
   * only: active gray process labels keep sweeping even when an earlier
   * process item has already settled.
   */
  active?: boolean;
  /** Runtime admitted for the owning turn. Historical process mascots keep
   * their original look after the session switches its next model. */
  provider?: string;
  model?: string;
  /**
   * Optional render hook for reasoning items in the expanded body.
   * The surface is decoupled from the reasoning fold's scroll and
   * auto-follow machinery — the parent already has the ThreadItemView
   * that knows how to render a reasoning item with full behavior.
   * Pass `undefined` to omit reasoning items from the body (the
   * summary line still mentions "思考过程" / "正在思考").
   */
  renderReasoningItem?: (
    item: ThreadItem,
    streaming: boolean,
  ) => JSX.Element | null;
};

const TOOL_ACTIVITY_ITEM_TYPES = new Set<string>(["tool_call"]);

// Mixed activity becomes harder to scan than a sentence once a group reaches
// this size. Same-kind groups keep their more useful count summary.
const CONDENSED_SUMMARY_MIN_TOOL_COUNT = 4;

function processKindLabel(kind: ToolActivityProcessSegment["kind"]): string {
  switch (kind) {
    case "edit": return translate("process.kind.edit");
    case "create": return translate("process.kind.create");
    case "search": return translate("process.kind.search");
    case "read": return translate("process.kind.read");
    case "list": return translate("process.kind.list");
    case "command": return translate("process.kind.command");
    case "agent": return translate("process.kind.agent");
    case "todo": return translate("process.kind.todo");
    case "interaction": return translate("process.kind.interaction");
    case "browser": return translate("process.kind.browser");
    case "skill": return translate("process.kind.skill");
    case "context": return translate("process.kind.context");
    default: return translate("process.kind.unknown");
  }
}

function mascotActivityForToolKind(
  kind: ToolActivityProcessSegment["kind"] | undefined,
): WuuMascotActivity {
  switch (kind) {
    case "search":
    case "list":
    case "browser":
      return "search";
    case "edit":
    case "create":
      return "edit";
    case "command":
      return "command";
    case "read":
    case "context":
    case "skill":
      return "read";
    default:
      return "tool";
  }
}

function isToolActivityItem(item: ThreadItem): boolean {
  return TOOL_ACTIVITY_ITEM_TYPES.has(item.type);
}

function condensedToolActivityText(
  segments: ToolActivityProcessSegment[],
  toolCount: number,
  reasoningStreaming: boolean,
): string {
  const running = segments.some((segment) => segment.status === "running");
  if (reasoningStreaming && !running) {
    return translate("process.thinkingAfterOperations", { count: toolCount });
  }

  const labels = Array.from(
    new Set(segments.map((segment) => processKindLabel(segment.kind))),
  );
  const shownLabels = labels.slice(0, 2);
  const categoryText =
    labels.length > 2
      ? translate("process.categoriesMore", { categories: shownLabels.join(translate("toolActivity.compactSeparator")) })
      : shownLabels.join(translate("process.categoryJoin"));
  return translate(running ? "process.runningOperations" : "process.completedOperations", {
    count: toolCount,
    categories: categoryText,
  });
}

export function ProcessSurface({
  processItems,
  streaming,
  active,
  provider,
  model,
  turnID,
  renderReasoningItem,
}: ProcessSurfaceProps): JSX.Element {
  const { t } = useI18n();
  const tokenSnapshot = useSyncExternalStore(
    turnTelemetryStore.subscribe,
    () => turnTelemetryStore.getSnapshot(turnID),
    () => turnTelemetryStore.getSnapshot(turnID),
  );
  const toolItems = processItems.filter(isToolActivityItem);
  const reasoningItems = processItems.filter(
    (item) => item.type === "reasoning",
  );
  const toolSegments = buildToolActivityProcessSegments(toolItems);
  const hasReasoning = reasoningItems.length > 0;
  const hasMultipleTools = toolItems.length > 1;
  const hasDetails = hasReasoning || hasMultipleTools;
  const reasoningStreaming =
    streaming &&
    reasoningItems.some((item) => item.status === "in_progress");
  const currentToolSegment =
    [...toolSegments]
      .reverse()
      .find((segment) => segment.status === "running") ??
    toolSegments.at(-1);
  const mascotActivity: WuuMascotActivity =
    reasoningStreaming || (hasReasoning && toolItems.length === 0)
      ? "thinking"
      : mascotActivityForToolKind(currentToolSegment?.kind);
  const useCondensedSummary =
    toolItems.length >= CONDENSED_SUMMARY_MIN_TOOL_COUNT &&
    toolSegments.length > 1;
  // Activity belongs to the synthesized process entry, not to any individual
  // tool item's running/completed status. In the real turn shell `active` is
  // assigned to the latest gray process entry; `streaming` is only the legacy
  // fallback for direct callers that do not provide that entry-level state.
  const processEntryActive = active ?? streaming;
  // The token counter belongs to the reasoning trail and stays visible after
  // thinking settles: the total freezes at the last sample and resumes
  // climbing on the next thinking phase. It persists for the live session
  // even once the process row stops sweeping.
  const showThinkingToken = Boolean(turnID) && hasReasoning;
  const summaryWaveRef = useLiveTextWave<HTMLSpanElement>(processEntryActive);

  // Details are opt-in. The running row itself should stay compact by
  // default; expanding it is a user request to inspect the process trail.
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    if (!hasDetails) {
      setExpanded(false);
    }
  }, [hasDetails]);

  // The fold body is the single bounded scroll container for the whole
  // expanded area (tool trail + reasoning). Auto-follow lives here so the
  // combined content stays pinned to the latest item while streaming,
  // and snaps to the bottom on every open.
  const processScroll = useAutoFollowScrollContainer({
    observeKey: processItems.map((item) => item.id).join("|"),
    open: expanded,
    openScrollDelayMs: REASONING_FOLD_OPEN_SNAP_DELAY_MS,
  });

  const handleToggle = (
    event: SyntheticEvent<HTMLDetailsElement>,
  ): void => {
    setExpanded(event.currentTarget.open);
  };

  const className = `process-surface${
    hasDetails ? " has-details" : " no-details"
  }${streaming ? " is-streaming" : ""}`;
  const summaryText = useCondensedSummary
    ? condensedToolActivityText(
        toolSegments,
        toolItems.length,
        reasoningStreaming,
      )
    : `${toolSegments
        .map((segment) =>
          typeof segment.count === "number"
            ? `${segment.countPrefix}${segment.count}${segment.countSuffix}`
            : (segment.text ?? ""),
        )
        .join(" · ")}${
        hasReasoning
          ? `${toolSegments.length > 0 ? " · " : ""}${
              reasoningStreaming ? t("process.thinking") : t("process.reasoning")
            }`
          : ""
      }`;

  const tokenWaveText =
    showThinkingToken &&
    (tokenSnapshot.inputTokens > 0 || tokenSnapshot.outputTokens > 0)
      ? turnTokenCountText(
          tokenSnapshot.inputTokens,
          tokenSnapshot.outputTokens,
          tokenCountLocale(),
        )
      : "";
  const summaryWaveText = `${summaryText}${tokenWaveText}`;

  const summaryLine = (
    <span className="process-surface-summary-line" aria-label={summaryText}>
      <ProcessSurfaceMascot
        active={processEntryActive}
        activity={mascotActivity}
        provider={provider}
        model={model}
      />
      <span
        ref={summaryWaveRef}
        className={`process-surface-summary-text${
          processEntryActive ? " wuu-live-text-wave" : ""
        }`}
        data-text={summaryWaveText}
        data-compact-text={summaryText}
      >
        {useCondensedSummary ? (
          <AnimatedProcessText
            className="process-surface-condensed-summary"
            text={condensedToolActivityText(
              toolSegments,
              toolItems.length,
              reasoningStreaming,
            )}
          />
        ) : (
          toolSegments.map((segment, index) => (
            <ProcessSurfaceSegmentView
              key={segment.id}
              segment={segment}
              separator={index > 0}
            />
          ))
        )}
        {hasReasoning && !useCondensedSummary ? (
          <span className="process-surface-segment process-surface-reasoning-segment">
            {toolSegments.length > 0 ? (
              <span className="process-surface-separator">{" · "}</span>
            ) : null}
            <AnimatedProcessText
              className="process-surface-reasoning-label"
              text={
                reasoningStreaming ? t("process.thinking") : t("process.reasoning")
              }
            />
          </span>
        ) : null}
        {showThinkingToken && turnID ? (
          <ThinkingTokenCount turnID={turnID} active={active} />
        ) : null}
      </span>
    </span>
  );

  const nativeFallback = (
    <div className={className}>
      <ProcessSurfaceFold
        summary={summaryLine}
        disabled={!hasDetails}
        open={expanded}
        onToggle={handleToggle}
        rowClassName={`${processEntryActive ? " is-live-gray" : ""}${
          streaming ? " is-streaming" : ""
        }`}
        bodyRef={processScroll.scrollRef}
        bodyProps={{ [AUTO_FOLLOW_NESTED_SCROLL_ATTR]: "true" }}
      >
        {hasMultipleTools ? (
          <div className="process-surface-tool-list">
            <ToolActivityTimeline
              items={toolItems}
              revealItems={streaming}
              streaming={streaming}
            />
          </div>
        ) : null}
        {hasReasoning && renderReasoningItem ? (
          <div className="process-surface-reasoning-list">
            {reasoningItems.map((item) => (
              <div
                key={item.id}
                className="process-surface-reasoning-item"
              >
                {renderReasoningItem(
                  item,
                  streaming && item.status === "in_progress",
                )}
              </div>
            ))}
          </div>
        ) : null}
      </ProcessSurfaceFold>
    </div>
  );
  // Nesting is deterministic: conversation.process is the complete outer
  // boundary. For a single tool, conversation.tool-activity remains the inner
  // keyed boundary, so process replacement wins and process wrappers surround
  // either the tool presenter result or the native unified surface.
  const toolActivityFallback = (
    <ToolActivityPresenter
      item={toolItems.length === 1 && !hasReasoning ? toolItems[0] : undefined}
      fallback={nativeFallback}
    />
  );
  return (
    <ConversationProcessPresentation
      processItems={processItems}
      streaming={streaming}
      active={active}
      fallback={toolActivityFallback}
    />
  );
}

function ProcessSurfaceSegmentView({
  segment,
  separator,
}: {
  segment: ToolActivityProcessSegment;
  separator: boolean;
}): JSX.Element {
  return (
    <span
      className={`process-surface-segment process-surface-segment-${segment.kind}`}
    >
      {separator ? (
        <span className="process-surface-separator">{" · "}</span>
      ) : null}
      {typeof segment.count === "number" ? (
        <>
          <span>{segment.countPrefix}</span>
          <ProcessSurfaceAnimatedCount value={segment.count} />
          <span>{segment.countSuffix}</span>
        </>
      ) : (
        <AnimatedProcessText text={segment.text ?? ""} />
      )}
    </span>
  );
}

function ProcessSurfaceAnimatedCount({
  value,
}: {
  value: number;
}): JSX.Element {
  const previousValue = useRef(value);
  const [changing, setChanging] = useState(false);

  useEffect(() => {
    if (previousValue.current === value) {
      return undefined;
    }
    previousValue.current = value;
    setChanging(true);
    const timeoutId = window.setTimeout(() => {
      setChanging(false);
    }, 180);
    return () => window.clearTimeout(timeoutId);
  }, [value]);

  return (
    <span
      className={`process-surface-count${changing ? " is-changing" : ""}`}
    >
      {value}
    </span>
  );
}

