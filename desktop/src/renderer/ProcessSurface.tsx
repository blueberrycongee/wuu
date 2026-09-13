import {
  useContext,
  useEffect,
  useRef,
  useState,
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
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import { translateCurrent as translate, useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { RoomCoordinatorAvatar } from "./RoomCoordinatorAvatar";
import { AgentIdentityContext } from "./AgentIdentityContext";

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
  const agent = useContext(AgentIdentityContext);
  if (agent === "room") return active ? <span className="process-surface-blobatar"><RoomCoordinatorAvatar size={28} activity={activity} /></span> : null;
  if (agent) return active ? (
    <span className="process-surface-blobatar">
      <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image}
        activity={activity} status={activity === "responding" ? "responding" : "thinking"} />
    </span>
  ) : null;
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

function processSegmentText(segment: ToolActivityProcessSegment): string {
  return typeof segment.count === "number"
    ? `${segment.countPrefix}${segment.count}${segment.countSuffix}`
    : (segment.text ?? "");
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
  if (reasoningStreaming && !segments.some((segment) => segment.status === "running")) {
    return translate("process.thinkingAfterOperations", { count: toolCount });
  }
  const parts = segments.slice(0, 3).map(processSegmentText);
  if (segments.length > 3) parts.push("…");
  if (reasoningStreaming) parts.push(translate("process.thinking"));
  return parts.join(translate("process.actionSeparator"));
}

export function ProcessSurface({
  processItems,
  streaming,
  active,
  provider,
  model,
  renderReasoningItem,
}: ProcessSurfaceProps): JSX.Element {
  const { t } = useI18n();
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
    ? condensedToolActivityText(toolSegments, toolItems.length, reasoningStreaming)
    : `${toolSegments
        .map(processSegmentText)
        .join(t("process.actionSeparator"))}${
        hasReasoning
          ? `${toolSegments.length > 0 ? " · " : ""}${
              reasoningStreaming ? t("process.thinking") : t("process.reasoning")
            }`
          : ""
      }`;

  const summaryLine = (
    <span className="process-surface-summary-line" aria-label={summaryText}>
      <ProcessSurfaceMascot
        active={processEntryActive}
        activity={mascotActivity}
        provider={provider}
        model={model}
      />
      <span
        className={`process-surface-summary-text${
          processEntryActive ? " wuu-live-text-wave" : ""
        }`}
        data-text={summaryText}
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
            <ToolActivityTimeline items={toolItems} />
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
        <span className="process-surface-separator">{translate("process.actionSeparator")}</span>
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
