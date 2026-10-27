import {
  useEffect,
  useId,
  useRef,
  useState,
  type JSX,
  type SyntheticEvent,
} from "react";
import type { ThreadItem } from "../shared/protocol";
import {
  browserActivityOpenURL,
  mcpToolActivityTitle,
  latestToolActivityProcessSegment,
  buildToolActivityProcessSegments,
  collectTurnSources,
} from "./ToolActivityHelpers";
import { ToolActivityRow, ToolActivityTimeline } from "./ToolActivity";
import { ToolActivityPresenter } from "./plugins/ToolActivityPresenter";
import { ConversationProcessPresentation } from "./plugins/ConversationProcessPresentation";
import {
  AUTO_FOLLOW_NESTED_SCROLL_ATTR,
  useAutoFollowScrollContainer,
} from "./AutoFollowScroll";
import { AnimatedProcessText, ProcessLabel } from "./ProcessTextMotion";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import { ContextCompactionIndicator } from "./ContextCompactionIndicator";
import { TurnSourcesRow, TurnSourceChips } from "./TurnSourcesRow";
import { collectTurnArtifacts } from "./ArtifactOutputs";
import { useI18n } from "./i18n";
import {
  useWorkspaceBrowserOpen,
  workspaceBrowserClickModifiers,
  type WorkspaceBrowserOpenModifiers,
} from "./WorkspaceBrowserOpen";
import { mascotActivityForToolKind, processSegmentText } from "./ProcessSummary";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

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
}): JSX.Element {
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
  cwd?: string;
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
  onOpenURL?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
  /**
   * Render reasoning and compaction notices in the expanded body. The
   * parent supplies ThreadItemView to preserve their full detail behavior.
   * Without this hook, only tool details are rendered.
   */
  renderProcessItem?: (
    item: ThreadItem,
    streaming: boolean,
  ) => JSX.Element | null;
};

const TOOL_ACTIVITY_ITEM_TYPES = new Set<string>(["tool_call"]);

function isToolActivityItem(item: ThreadItem): boolean {
  return TOOL_ACTIVITY_ITEM_TYPES.has(item.type);
}

export function ProcessSurface({
  processItems,
  cwd,
  streaming,
  active,
  provider,
  model,
  onOpenURL,
  renderProcessItem,
}: ProcessSurfaceProps): JSX.Element {
  const { t } = useI18n();
  const openWorkspaceURL = useWorkspaceBrowserOpen(onOpenURL);
  const toolItems = processItems.filter(isToolActivityItem);
  const sources = collectTurnSources(toolItems.filter(item =>
    item.status === "completed" && !item.error && !item.result_detail?.is_error), "url");
  const reasoningItems = processItems.filter(
    (item) => item.type === "reasoning",
  );
  const compactionItems = processItems.filter(item => item.type === "context_compaction");
  const hasCompaction = compactionItems.length > 0;
  const currentToolSegment = latestToolActivityProcessSegment(toolItems, hasCompaction);
  const hasReasoning = reasoningItems.length > 0;
  const hasMultipleTools = toolItems.length > 1;
  const hasStandaloneDisclosure = toolItems.length === 1 && !hasReasoning && !hasCompaction
    && (["set_session_workspace", "run_code", "exec"].includes(toolItems[0].name ?? "")
      || Boolean(mcpToolActivityTitle(toolItems[0])));
  const hasInspection = !hasReasoning && !hasMultipleTools && !hasCompaction
    && collectTurnArtifacts({ items: toolItems }).some(artifact => artifact.foldPreview);
  const hasWebActivity = sources.length > 0 || toolItems.some(item => ["websearch", "webfetch"].includes((item.name ?? "").toLowerCase().replaceAll("_", "")));
  const hasDetails = hasReasoning || hasCompaction || hasMultipleTools || hasInspection || hasWebActivity || hasStandaloneDisclosure;
  const detailRuns: ThreadItem[][] = [];
  for (const item of processItems) {
    if (!isToolActivityItem(item) && item.type !== "reasoning" && item.type !== "context_compaction") continue;
    const previous = detailRuns.at(-1);
    if (previous?.[0].type === item.type) previous.push(item);
    else detailRuns.push([item]);
  }
  const reasoningStreaming =
    streaming &&
    reasoningItems.some((item) => item.status === "in_progress");
  const mascotActivity: WuuMascotActivity = compactionItems.some(item => item.status === "in_progress")
    ? "compact"
    : hasReasoning && toolItems.length === 0
      ? "thinking"
      : mascotActivityForToolKind(currentToolSegment?.kind);
  // Activity belongs to the synthesized process entry, not to any individual
  // tool item's running/completed status. In the real turn shell `active` is
  // assigned to the latest gray process entry; `streaming` is only the legacy
  // fallback for direct callers that do not provide that entry-level state.
  const processEntryActive = active ?? streaming;

  // Details are opt-in. The running row itself should stay compact by
  // default; expanding it is a user request to inspect the process trail.
  const [expanded, setExpanded] = useState(false);
  const sourceDetailsID = useId();
  const disclosureTrigger = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!hasDetails) {
      setExpanded(false);
    }
  }, [hasDetails]);

  // Live first visits start at the current activity; history starts at the
  // beginning. Reopening retains the reader's position and following mode.
  const processScroll = useAutoFollowScrollContainer({
    observeKey: processItems.map((item) => item.id).join("|"),
    open: expanded,
    initialAutoFollow: streaming && !hasWebActivity,
  });

  const handleToggle = (
    event: SyntheticEvent<HTMLDetailsElement>,
  ): void => {
    setExpanded(event.currentTarget.open);
  };

  const browserURL = browserActivityOpenURL(toolItems);
  const handleSummaryClick = (event: SyntheticEvent<HTMLElement>): void => {
    disclosureTrigger.current = event.currentTarget;
    if (hasDetails || !browserURL) {
      return;
    }
    event.preventDefault();
    const mouse = event.nativeEvent as MouseEvent;
    openWorkspaceURL(browserURL, workspaceBrowserClickModifiers(mouse));
  };

  const className = `process-surface${
    hasDetails ? " has-details" : " no-details"
  }${streaming ? " is-streaming" : ""}`;
  const summarySegments = processEntryActive
    ? currentToolSegment ? [currentToolSegment] : []
    : buildToolActivityProcessSegments(toolItems).filter(segment =>
      !hasCompaction || segment.status !== "completed" || !["context", "context-window"].includes(segment.id),
    );
  const summaryText = summarySegments.length > 0
    ? summarySegments.map(processSegmentText).join(t("process.actionSeparator"))
    : hasReasoning ? t(reasoningStreaming ? "process.thinking" : "process.reasoning") : "";

  const summaryLine = (
    <span
      className={`process-surface-summary-line${hasCompaction ? " has-compaction" : ""}`}
      aria-label={hasCompaction ? undefined : summaryText}
    >
      <ProcessSurfaceMascot
        active={processEntryActive}
        activity={mascotActivity}
        provider={provider}
        model={model}
      />
      {summaryText ? <ProcessLabel
        className="process-surface-summary-text"
        active={processEntryActive}
        text={summaryText}
      >
        <span className={`process-surface-segment${currentToolSegment ? "" : " process-surface-reasoning-segment"}`}>
          <AnimatedProcessText
            className={currentToolSegment ? undefined : "process-surface-reasoning-label"}
            text={summaryText}
          />
        </span>
      </ProcessLabel> : null}
      {hasCompaction ? (
        <span className="process-surface-compactions">
          {/* The expanded notice owns live announcements while it is visible. */}
          {compactionItems.map(item => <ContextCompactionIndicator key={item.id} item={item} announce={!expanded} />)}
        </span>
      ) : null}
    </span>
  );

  const nativeFallback = (
    <div className={className}>
      <ProcessSurfaceFold
        summary={summaryLine}
        summaryAccessory={<TurnSourcesRow sources={sources} running={processEntryActive} onOpen={onOpenURL} inline />}
        disabled={!hasDetails}
        open={expanded}
        onToggle={handleToggle}
        onSummaryClick={handleSummaryClick}
        rowClassName={`${processEntryActive ? " is-live-gray" : ""}${
          streaming ? " is-streaming" : ""
        }`}
        bodyRef={processScroll.scrollRef}
        bodyProps={{ [AUTO_FOLLOW_NESTED_SCROLL_ATTR]: "true", id: sourceDetailsID, inert: !expanded,
          onKeyDownCapture: event => {
            if (!hasWebActivity || event.key !== "Escape") return;
            const target = event.target;
            if (target instanceof Element && target.closest("input, textarea, [contenteditable=true], [role=textbox]")) return;
            event.preventDefault();
            event.stopPropagation();
            setExpanded(false);
            (disclosureTrigger.current ?? event.currentTarget.closest("details")?.querySelector("summary"))?.focus();
          },
        }}
      >
        {expanded ? <TurnSourceChips sources={sources} onOpen={onOpenURL} /> : null}
        {hasStandaloneDisclosure ? (
          expanded ? <ToolActivityPresenter item={toolItems[0]} fallback={<ToolActivityRow items={toolItems} streaming={streaming} detailsOnly />} /> : null
        ) : detailRuns.map(items => items[0].type === "tool_call" ? (
          <div className="process-surface-tool-list" key={items[0].id}>
            <ToolActivityTimeline items={items} cwd={cwd} showInspectionPreviews={expanded} />
          </div>
        ) : renderProcessItem ? (
          <div className="process-surface-reasoning-list" key={items[0].id}>
            {items.map((item) => (
              <div
                key={item.id}
                className="process-surface-reasoning-item"
              >
                {renderProcessItem(
                  item,
                  streaming && item.status === "in_progress",
                )}
              </div>
            ))}
          </div>
        ) : null)}
      </ProcessSurfaceFold>
    </div>
  );
  // Keep one boundary as a single activity grows into a process group.
  return <ConversationProcessPresentation processItems={processItems} streaming={streaming} active={processEntryActive} fallback={nativeFallback} />;
}
