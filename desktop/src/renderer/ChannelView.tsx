import { hostSupports } from "./HostCapabilities";
import { Bot, ChevronDown, ChevronUp, ClipboardList, ImagePlus, MessageCircle, Network, PanelLeftClose, PanelLeftOpen, Plus, Settings2, X } from "lucide-react";
import { Fragment, type CSSProperties, type KeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ChannelAgentInsight, ChannelMessage, ChannelMessageListResult, ChannelResponse, ChannelRoom, EngineInfo, InitializeResult, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark, randomAgentAvatarKey } from "./AgentAvatarMark";
import { AgentAvatarCreator } from "./AgentAvatarCreator";
import { AgentOnboarding, createAgentOnboardingDraft, type AgentOnboardingDraft } from "./AgentOnboarding";
import { AgentRelationshipGraph } from "./AgentRelationshipGraph";
import { squareAvatarImageFromFile } from "./avatarImage";
import { AUTO_FOLLOW_BOTTOM_THRESHOLD_PX, useAutoFollowScrollContainer } from "./AutoFollowScroll";
import { ChannelContinuity } from "./ChannelContinuity";
import { ChannelSessions } from "./ChannelSessions";
import { ChannelAgentHoverCard } from "./ChannelAgentHoverCard";
import { ChannelActivityInspector } from "./ChannelActivityInspector";
import { ChannelSessionInspector } from "./ChannelSessionInspector";
import { ChannelAgentSettings } from "./ChannelAgentSettings";
import { ChannelActivityPresence } from "./ChannelActivityPresence";
import { ChannelCoordinatorActivity } from "./ChannelCoordinatorActivity";
import { ChannelComposer, type ChannelComposerHandle } from "./ChannelComposer";
import { ChannelGroupAvatar } from "./ChannelGroupAvatar";
import { ChannelMemberPicker } from "./ChannelMemberPicker";
import { ChannelRecipientPicker } from "./ChannelRecipientPicker";
import { buildComposerAttachments } from "./ComposerDraftState";
import { ComposerAttachmentStrip } from "./ComposerInputSections";
import {
  sameChannelMessages,
  sameChannelRooms,
  sameNamedAgents,
} from "./ChannelRoomState";
import { HumanAvatarMark } from "./DefaultAvatar";
import { FieldError } from "./FieldError";
import {
  awaitComposerImages,
  inputFilesFromComposer,
  inputImagesFromComposer,
  type ComposerFile,
  type ComposerImage,
} from "./ComposerMessages";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { useI18n } from "./i18n";
import { JumpToLatestPill } from "./JumpToLatestPill";
import { useLongTextCollapse } from "./LongTextCollapse";
import { useChannelMessageMotion } from "./useChannelMessageMotion";
import { MessageBubble, MessageBubbleRow } from "./MessageBubbleFlow";
import { SelectMenu, type SelectMenuGroup } from "./SelectMenu";
import { SidebarNameDialog } from "./SidebarNameDialog";
import { RichContent } from "./RichContent";
import { effortLabel, providerModelEffortOptions } from "./RuntimeHelpers";
import { showErrorToast, toastErrorMessage } from "./Toast";
import { userFacingErrorForMessage } from "./UserFacingErrors";

type SetupPanel = "agent" | "room" | "task" | null;
type RoomMemberMode = "add" | null;

type AgentDetailDraft = {
  name: string;
  role: string;
  avatarKey: string;
  avatarImage: string;
  engine: string;
  model: string;
  effort: string;
};
export type ChannelSection = "rooms" | "agents" | "tasks";
type AgentActivityStatus = "idle" | "thinking" | "sending";
type ChannelResponseActivity = Omit<ChannelResponse, "body">;

const CHANNEL_SPLIT_WIDTH_KEY = "wuu.channels.splitPaneWidth";
const LEGACY_CHANNEL_LIST_WIDTH_KEY = "wuu.channels.listWidth";
const CHANNEL_LIST_COLLAPSED_KEY = "wuu.channels.listCollapsed";
const CHANNEL_SPLIT_MIN_WIDTH = 156;
const CHANNEL_SPLIT_MAX_WIDTH = 360;
const CHANNEL_SPLIT_DEFAULT_WIDTH = 208;
const CHANNEL_SPLIT_COLLAPSED_WIDTH = 44;
const CHANNEL_SPLIT_WIDTH_STEP = 16;
const MAX_ROOM_AGENTS = 6;

export function formatChannelUnreadCount(count: number): string {
  return count > 99 ? "99+" : String(Math.max(0, count));
}

function AgentAvatar({ id, name, avatarKey, avatarImage, status, statusText, model, modelLabel, compact = false, expressive = false, focusable = true }: {
  id: string;
  name: string;
  avatarKey: string;
  avatarImage?: string;
  status: AgentActivityStatus;
  statusText: string;
  model?: string;
  modelLabel: string;
  compact?: boolean;
  expressive?: boolean;
  focusable?: boolean;
}): JSX.Element {
  const accessibleDescription = model ? `${name}: ${statusText}, ${modelLabel}: ${model}` : `${name}: ${statusText}`;
  return (
    <span className={`channel-agent-avatar${compact ? " compact" : ""}`} tabIndex={focusable ? 0 : undefined} aria-label={accessibleDescription}>
      <AgentAvatarMark seed={id} avatarKey={avatarKey} avatarImage={avatarImage} status={expressive ? status : "idle"} motion={expressive ? "expressive" : "subtle"} />
      <span className="channel-agent-status-card" role="tooltip">
        <span>{statusText}</span>
        {model ? <span className="channel-agent-model">{model}</span> : null}
      </span>
    </span>
  );
}

function ChannelAuthorName({ name, mentionLabel, onMention }: {
  name: string;
  mentionLabel?: string;
  onMention?: () => void;
}): JSX.Element {
  if (!onMention) return <strong>{name}</strong>;
  return (
    <button className="channel-author-mention" type="button" aria-label={mentionLabel} onClick={onMention}>
      <span aria-hidden="true">@</span>
      {name}
    </button>
  );
}

type ChannelTimelineItem =
  | { kind: "message"; message: ChannelMessage }
  | { kind: "orchestration"; tasks: ChannelMessage[] };

async function readChannelMessages(roomID: string): Promise<ChannelMessageListResult> {
  const result = await window.wuu!.listChannelMessages({ room_id: roomID, limit: 500 });
  const messages = [...(result.messages ?? [])];
  let page = messages;
  // The API returns the oldest page first. Finish the snapshot before replacing
  // the timeline so an acknowledged or completed reply cannot fall off its end.
  while (page.length === 500) {
    const afterSeq = page.at(-1)!.seq;
    const next = await window.wuu!.listChannelMessages({ room_id: roomID, after_seq: afterSeq, limit: 500 });
    page = (next.messages ?? []).filter((message) => message.seq > afterSeq);
    messages.push(...page);
  }
  return { ...result, messages };
}

function buildChannelTimeline(messages: ChannelMessage[]): ChannelTimelineItem[] {
  const timeline: ChannelTimelineItem[] = [];
  for (const message of messages) {
    // Work cards share the timeline with public conversation replies.
    if (message.kind === "task") {
      const previous = timeline[timeline.length - 1];
      const previousTask = previous?.kind === "orchestration" ? previous.tasks[previous.tasks.length - 1] : undefined;
      if (previous?.kind === "orchestration" && previousTask && previousTask.seq + 1 === message.seq) {
        previous.tasks.push(message);
      } else {
        timeline.push({ kind: "orchestration", tasks: [message] });
      }
      continue;
    }
    timeline.push({ kind: "message", message });
  }
  return timeline;
}

export function assignmentState(state?: string): "open" | "doing" | "checking" | "revising" | "needs_human" | "done" {
  if (state === "checking") return "checking";
  if (state === "revising") return "revising";
  if (state === "needs_human") return "needs_human";
  if (state === "doing") return "doing";
  if (state === "done") return "done";
  return "open";
}

function assignmentStatusKey(state: ReturnType<typeof assignmentState>): "open" | "doing" | "checking" | "revising" | "needsHuman" | "done" {
  return state === "needs_human" ? "needsHuman" : state;
}

// Work records use a wider state vocabulary than the lightweight chat-task
// states (working/completed/integrating plus terminal failure states). Collapse
// them into one user-facing set so a card never shows raw "open → working"
// style transitions, and terminal states stay visually distinct.
function workDisplayState(state?: string): "open" | "doing" | "checking" | "revising" | "needs_human" | "done" | "integrating" | "failed" | "cancelled" | "interrupted" {
  if (state === "working" || state === "doing") return "doing";
  if (state === "checking") return "checking";
  if (state === "revising") return "revising";
  if (state === "needs_human") return "needs_human";
  if (state === "integrating") return "integrating";
  if (state === "done" || state === "completed") return "done";
  if (state === "failed") return "failed";
  if (state === "cancelled") return "cancelled";
  if (state === "interrupted") return "interrupted";
  return "open";
}

function ChannelOrchestrationCluster({
  tasks,
  agents,
  onOpenSession,
}: {
  room: ChannelRoom;
  tasks: ChannelMessage[];
  agents: NamedAgent[];
  onOpenSession?: (sessionID: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const agentByID = useMemo(() => new Map(agents.map((agent) => [agent.id, agent])), [agents]);
  return (
    <MessageBubbleRow
      outgoing={false}
      className="channel-message channel-orchestration-message channel-orchestration-row"
      contentClassName="channel-message-content"
    >
      <div className="channel-assignment-list">
        {tasks.map((task, index) => {
          const owner = agentByID.get(task.task_owner ?? "");
          const ownerName = owner?.name ?? task.task_owner ?? t("channels.taskOwnerLabel");
          const work = task.work;
          const workState = workDisplayState(work?.state ?? task.task_state);
          const statusLabel = workState === "cancelled"
            ? t("channels.workStatus.cancelled")
            : workState === "failed"
              ? t("channels.workStatus.failed")
              : workState === "interrupted"
                ? t("channels.workStatus.interrupted")
                : workState === "integrating"
                  ? t("channels.workStatus.integrating")
                  : t(`channels.assignmentStatus.${assignmentStatusKey(workState)}`);
          const elapsedMilliseconds = work
            ? Math.max(0, Date.parse(work.updated_at) - Date.parse(work.created_at))
            : 0;
          const elapsedMinutes = Math.max(1, Math.round(elapsedMilliseconds / 60_000));
          const elapsed = elapsedMinutes >= 60
            ? `${Math.floor(elapsedMinutes / 60)}h ${elapsedMinutes % 60}m`
            : `${elapsedMinutes}m`;
          const inputTokens = work?.runs?.reduce((sum, run) => sum + (run.input_tokens ?? 0), 0) ?? 0;
          const outputTokens = work?.runs?.reduce((sum, run) => sum + (run.output_tokens ?? 0), 0) ?? 0;
          const queuedRuns = work?.runs?.filter((run) => run.state === "queued").length ?? 0;
          const hasInternalDetails = Boolean(
            work?.checks_summary
            || work?.verification?.report
            || work?.artifacts?.length
            || work?.runs?.length
            || work?.deliveries?.length
            || work?.unresolved_items,
          );
          const title = task.task_title?.trim() || task.body.trim() || t("channels.newTask");
          const body = task.task_title?.trim() && task.body.trim() !== task.task_title.trim()
            ? task.body.trim()
            : "";
          return (
            <details
              className="channel-assignment-item"
              data-state={workState}
              key={task.id}
              style={{ "--channel-assignment-index": index } as CSSProperties}
            >
              <summary className="channel-assignment-heading" title={ownerName}>
                <span className="channel-assignment-target" aria-hidden="true">
                  <AgentAvatarMark seed={owner?.id ?? task.task_owner ?? task.id}
                    avatarKey={owner?.avatar_key ?? "abstract-1"} avatarImage={owner?.avatar_image} />
                </span>
                <span className="channel-assignment-copy"><strong>{title}</strong></span>
                <ChevronDown className="channel-assignment-chevron" aria-hidden="true" />
              </summary>
              <div className="channel-assignment-context">
                <span>{ownerName}</span>
                <span className="channel-assignment-status" data-state={workState}>{statusLabel}</span>
              </div>
              {body ? <div className="channel-assignment-body"><RichContent text={body} /></div> : null}
              {work ? (
                <div className="channel-work-card-details">
                  <div className="channel-work-summary-line">
                    {work.changed_files_count ? <span>{t("channels.workFilesChanged", { count: work.changed_files_count })}</span> : null}
                    <span>{t("channels.workElapsed", { duration: elapsed })}</span>
                  </div>
                  {hasInternalDetails ? (
                    <details className="channel-work-evidence">
                      <summary>{t("channels.workDetails")}</summary>
                      <div className="channel-work-evidence-body">
                        <section>
                          <strong>{t("channels.workOrchestration")}</strong>
                          <p>{t("channels.workRevisions", { goal: work.goal_revision, candidate: work.candidate_revision })}</p>
                          <p>{t("channels.workRounds", { current: work.current_round ?? 1, max: work.max_rounds ?? 3, qualified: work.qualified_candidates ?? 0, candidates: work.candidates_used })}</p>
                          <p>{t("channels.workUsage", { input: inputTokens.toLocaleString(), output: outputTokens.toLocaleString(), queued: queuedRuns })}</p>
                          {work.total_cost_usd ? <p>{t("channels.workCost", { cost: work.total_cost_usd.toFixed(4) })}</p> : null}
                          {work.owner_capacity && work.room_capacity && work.global_capacity ? (
                            <p>{t("channels.workCapacity", {
                              ownerActive: work.owner_capacity.active + work.owner_capacity.starting,
                              ownerStarting: work.owner_capacity.starting,
                              ownerLimit: work.owner_capacity.limit,
                              roomActive: work.room_capacity.active + work.room_capacity.starting,
                              roomStarting: work.room_capacity.starting,
                              roomLimit: work.room_capacity.limit,
                              globalActive: work.global_capacity.active + work.global_capacity.starting,
                              globalStarting: work.global_capacity.starting,
                              globalLimit: work.global_capacity.limit,
                            })}</p>
                          ) : null}
                          {work.deadline_at ? <p>{t("channels.workDeadline", { deadline: new Date(work.deadline_at).toLocaleString() })}</p> : null}
                          {work.selection_reason ? <p>{t("channels.workSelection", { reason: work.selection_reason })}</p> : null}
                        </section>
                        {work.checks_summary ? <p>{t("channels.workChecks", { summary: work.checks_summary })}</p> : null}
                        {work.verification?.report ? (
                          <section>
                            <strong>{t("channels.workVerifierReport")}</strong>
                            <RichContent text={work.verification.report} />
                          </section>
                        ) : null}
                        {work.artifacts?.length ? (
                          <section>
                            <strong>{t("channels.workArtifacts")}</strong>
                            <ul>{work.artifacts.map((artifact) => (
                              <li key={artifact.id}>
                                <a href={artifact.uri}>{artifact.label || artifact.summary || artifact.kind}</a>
                                {artifact.id === work.candidate_artifact_ref ? <strong> · {t("channels.workCanonicalCandidate")}</strong> : null}
                              </li>
                            ))}</ul>
                          </section>
                        ) : null}
                        {work.runs?.length ? (
                          <section>
                            <strong>{t("channels.workRuns")}</strong>
                            <ul>{work.runs.map((run) => (
                              <li key={run.id}>
                                <span>
                                  {run.profile || run.kind} · {run.state} · {t("channels.workRunRound", { round: run.round ?? 1 })}
                                  {run.qualified ? ` · ${t("channels.workQualified")}` : ""}
                                  {(run.input_tokens || run.output_tokens) ? ` · ${((run.input_tokens ?? 0) + (run.output_tokens ?? 0)).toLocaleString()} tokens` : ""}
                                  {run.cost_usd ? ` · $${run.cost_usd.toFixed(4)}` : ""}
                                  {run.started_at && Date.parse(run.started_at) > Date.parse(run.created_at)
                                    ? ` · ${t("channels.workQueuedFor", { seconds: Math.max(1, Math.round((Date.parse(run.started_at) - Date.parse(run.created_at)) / 1000)) })}`
                                    : ""}
                                  {run.queue_reason ? ` · ${run.queue_reason}` : ""}
                                </span>
                                {run.session_ref ? (
                                  <button className="channel-work-session-link" type="button" onClick={() => onOpenSession?.(run.session_ref ?? "")}>
                                    <code>{run.session_ref}</code>
                                  </button>
                                ) : null}
                              </li>
                            ))}</ul>
                          </section>
                        ) : null}
                        {work.deliveries?.length ? (
                          <section>
                            <strong>{t("channels.workPrivateMessages")}</strong>
                            <ul>{work.deliveries.map((delivery) => (
                              <li key={delivery.id}>
                                <span>
                                  {delivery.kind || "control"} · {delivery.visibility ?? "private"} · {delivery.target_kind ?? "named_agent"}{delivery.target_id ? `:${delivery.target_id}` : ""}
                                  {delivery.terminal_state ? ` · ${delivery.terminal_state}` : ""}
                                  {delivery.correlation_id ? ` · #${delivery.correlation_id}` : ""}
                                  {delivery.invalidated_at ? ` · ${t("channels.workMessageInvalidated")}` : ""}
                                </span>
                                <RichContent text={delivery.body} />
                              </li>
                            ))}</ul>
                          </section>
                        ) : null}
                        {work.unresolved_items ? <p>{t("channels.workUnresolved", { items: work.unresolved_items })}</p> : null}
                      </div>
                    </details>
                  ) : null}
                </div>
              ) : null}
            </details>
          );
        })}
      </div>
    </MessageBubbleRow>
  );
}

function ChannelMessageBubble({
  message,
  outgoing,
  allowCollapse,
  onExpand,
  attachmentIDPrefix,
  beforeBody,
}: {
  message: ChannelMessage;
  outgoing: boolean;
  allowCollapse: boolean;
  onExpand?: () => void;
  attachmentIDPrefix: string;
  beforeBody?: JSX.Element;
}): JSX.Element {
  const { t } = useI18n();
  const { collapsible, expanded, toggleExpanded } = useLongTextCollapse(message.body);
  const canCollapse = allowCollapse && collapsible;
  const handleToggleExpanded = (): void => {
    if (!expanded) {
      onExpand?.();
    }
    toggleExpanded();
  };
  const hasBubble = Boolean(message.body || beforeBody);

  return (
    <>
      {hasBubble ? (
        <MessageBubble
          outgoing={outgoing}
          className={`channel-message-bubble${canCollapse ? ` long-card ${expanded ? "expanded" : "collapsed"}` : ""}`}
        >
          {beforeBody}
          {message.kind === "task" ? (
            <span className="channel-assignment-task-heading">
              <strong>{message.task_title?.trim() || message.body.trim() || t("channels.newTask")}</strong>
              <span className="channel-assignment-status" data-state={assignmentState(message.task_state)}>
                <i aria-hidden="true" />
                {t(`channels.assignmentStatus.${assignmentStatusKey(assignmentState(message.task_state))}`)}
              </span>
            </span>
          ) : null}
          {message.body && (
            message.kind !== "task"
            || Boolean(message.task_title?.trim() && message.body.trim() !== message.task_title.trim())
          ) ? <RichContent text={message.body} /> : null}
          {canCollapse ? (
            <button
              className="channel-message-expand-toggle"
              type="button"
              aria-expanded={expanded}
              onClick={handleToggleExpanded}
            >
              <span>{expanded ? t("common.collapse") : t("common.showMore")}</span>
              {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
            </button>
          ) : null}
        </MessageBubble>
      ) : null}
      {message.images?.length || message.files?.length ? (
        <ComposerAttachmentStrip
          images={(message.images ?? []).map((image, index) => ({ id: `${message.id}-${attachmentIDPrefix}-image-${index}`, ...image }))}
          files={(message.files ?? []).map((file, index) => ({ id: `${message.id}-${attachmentIDPrefix}-file-${index}`, ...file }))}
          removable={false}
        />
      ) : null}
    </>
  );
}

function ChannelAgentActivity({ agent, agentID, state, error, selected = false, onResume, onInspect }: {
  agent?: NamedAgent;
  agentID: string;
  state: ChannelResponse["state"];
  error?: string;
  selected?: boolean;
  onResume?: () => Promise<void>;
  onInspect?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [resuming, setResuming] = useState(false);
  const [resumeError, setResumeError] = useState("");
  const failed = state === "failed" || state === "interrupted";
  const errorDisplay = failed && error ? userFacingErrorForMessage(error, "turn") : undefined;
  const status = state === "thinking" || state === "responding"
    ? t("channels.agentWorking") : t(`channels.sessions.state.${state}`);
  const resume = async (): Promise<void> => {
    setResuming(true);
    setResumeError("");
    try {
      await onResume?.();
    } catch (reason) {
      setResumeError(toastErrorMessage(reason));
    } finally {
      setResuming(false);
    }
  };
  return (
    <div className={`channel-response-status channel-animated-activity${failed ? " failed" : ""}`} data-activity-state={state} role={failed ? "alert" : "status"}>
      <button className={`channel-activity-inspect${selected ? " selected" : ""}`} type="button" disabled={!onInspect} onClick={onInspect}
        aria-expanded={onInspect ? selected : undefined}
        title={`${agent?.name ?? agentID} · ${status}`}
        aria-label={`${agent?.name ?? agentID} · ${status} · ${t("channels.sessions.history")}`}>
      <span className="channel-response-status-avatar" aria-hidden="true">
        <AgentAvatarMark seed={agentID} avatarKey={agent?.avatar_key ?? "abstract-1"} avatarImage={agent?.avatar_image} status={state} />
      </span>
      <span className="channel-response-status-copy channel-activity-accessible">
        <strong>{agent?.name ?? agentID}</strong>
        <span>{status}</span>
      </span>
      </button>
      {errorDisplay ? <span className="channel-response-error" title={errorDisplay.detail}>{errorDisplay.title}</span> : null}
      {failed && onResume ? <button type="button" disabled={resuming} onClick={() => void resume()}>{t(resuming ? "channels.sessions.starting" : state === "interrupted" ? "channels.sessions.resume" : "channels.sessions.retry")}</button> : null}
      {resumeError ? <span className="channel-response-error">{resumeError}</span> : null}
    </div>
  );
}

function clampChannelSplitWidth(width: number): number {
  return Math.min(CHANNEL_SPLIT_MAX_WIDTH, Math.max(CHANNEL_SPLIT_MIN_WIDTH, Math.round(width)));
}

function initialChannelSplitWidth(): number {
  const storedValue = window.localStorage.getItem(CHANNEL_SPLIT_WIDTH_KEY) ?? window.localStorage.getItem(LEGACY_CHANNEL_LIST_WIDTH_KEY);
  const stored = Number(storedValue);
  return storedValue !== null && Number.isFinite(stored) && stored >= CHANNEL_SPLIT_MIN_WIDTH
    ? clampChannelSplitWidth(stored)
    : CHANNEL_SPLIT_DEFAULT_WIDTH;
}

function initialChannelListCollapsed(): boolean {
  return window.localStorage.getItem(CHANNEL_LIST_COLLAPSED_KEY) === "true";
}

type TaskBoardColumn = "todo" | "in_progress" | "review" | "needs_input" | "done";

const taskBoardColumns: TaskBoardColumn[] = ["todo", "in_progress", "review", "needs_input", "done"];

function taskBoardColumn(task: ChannelMessage): TaskBoardColumn {
  const state = task.work?.state ?? task.task_state ?? "open";
  if (state === "checking") return "review";
  if (state === "needs_human" || state === "failed" || state === "interrupted") return "needs_input";
  if (state === "done" || state === "completed" || state === "cancelled") return "done";
  if (state === "doing" || state === "working" || state === "revising" || state === "integrating") return "in_progress";
  return "todo";
}

function taskBoardColumnKey(column: TaskBoardColumn):
  | "channels.taskBoard.todo"
  | "channels.taskBoard.inProgress"
  | "channels.taskBoard.review"
  | "channels.taskBoard.needsInput"
  | "channels.taskBoard.done" {
  if (column === "in_progress") return "channels.taskBoard.inProgress";
  if (column === "review") return "channels.taskBoard.review";
  if (column === "needs_input") return "channels.taskBoard.needsInput";
  if (column === "done") return "channels.taskBoard.done";
  return "channels.taskBoard.todo";
}

type ChannelDirectoryStateUpdater<T> =
  (update: T[] | ((current: T[]) => T[])) => void;

export function ChannelView({ initialized, section = "rooms", navigation, archivedRoomIDs = [], onSectionChange, selectedRoomID: controlledRoomID, onSelectRoom, onRoomRead, onOpenMemoryDirectory, onOpenSession, onCreateAgent, onManageProviders, composerDraft, onComposerDraftChange, newRoomRequest, onNewRoomRequestHandled, editAgentRequestID, onEditAgentRequestHandled, directoryAgents, directoryRooms, onDirectoryAgentsChange, onDirectoryRoomsChange }: {
  initialized?: InitializeResult;
  engines?: EngineInfo[];
  section?: ChannelSection;
  navigation?: ReactNode;
  archivedRoomIDs?: string[];
  onSectionChange?: (section: ChannelSection) => void;
  // Optional controlled room selection. App.tsx drives this so the unified
  // sidebar can select rooms; when absent the view manages selection
  // internally (tests and standalone usage).
  selectedRoomID?: string;
  onSelectRoom?: (roomID: string) => void;
  onRoomRead?: (roomID: string) => void;
  onOpenMemoryDirectory?: (path: string) => void;
  onOpenSession?: (sessionID: string) => void;
  onCreateAgent?: () => void;
  onManageProviders?: () => void;
  composerDraft?: {
    prompt: string;
    images: ComposerImage[];
    files: ComposerFile[];
  };
  onComposerDraftChange?: (draft: {
    prompt: string;
    images: ComposerImage[];
    files: ComposerFile[];
  }) => void;
  // Incremented by the parent (sidebar ＋ button) to request the new-room
  // dialog; the dialog itself stays inside this view.
  newRoomRequest?: number;
  onNewRoomRequestHandled?: () => void;
  editAgentRequestID?: string;
  onEditAgentRequestHandled?: () => void;
  // App.tsx owns these arrays in the full desktop shell. Standalone tests and
  // embedded callers may omit them and keep the legacy local directory state.
  directoryAgents?: NamedAgent[];
  directoryRooms?: ChannelRoom[];
  onDirectoryAgentsChange?: ChannelDirectoryStateUpdater<NamedAgent>;
  onDirectoryRoomsChange?: ChannelDirectoryStateUpdater<ChannelRoom>;
}): JSX.Element {
  const { formatDate, locale, t } = useI18n();
  const [localAgents, setLocalAgents] = useState<NamedAgent[]>([]);
  const [agentInsights, setAgentInsights] = useState<Record<string, ChannelAgentInsight>>({});
  const [localRooms, setLocalRooms] = useState<ChannelRoom[]>([]);
  const agents = directoryAgents ?? localAgents;
  const rooms = directoryRooms ?? localRooms;
  const setAgents = onDirectoryAgentsChange ?? setLocalAgents;
  const setRooms = onDirectoryRoomsChange ?? setLocalRooms;
  const directoryIsControlled =
    directoryAgents !== undefined && directoryRooms !== undefined;
  const [internalSelectedRoomID, setInternalSelectedRoomID] = useState("");
  const selectedRoomID = controlledRoomID ?? internalSelectedRoomID;
  const [inspectedSession, setInspectedSession] = useState<{ roomID: string; sessionRef?: string; agentID?: string; turnID?: string; name: string } | null>(null);
  const [inspectorClosing, setInspectorClosing] = useState(false);
  const conversationRef = useRef<HTMLDivElement>(null);
  const inspectionTrigger = useRef<HTMLElement | null>(null);
  const [inspectorOverlay, setInspectorOverlay] = useState(false);
  useLayoutEffect(() => {
    const node = conversationRef.current;
    if (!node) return;
    const update = () => setInspectorOverlay(node.getBoundingClientRect().width < 1040);
    update();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(update);
    observer?.observe(node);
    return () => observer?.disconnect();
  }, [section]);
  const finishClosingInspector = useCallback(() => {
    setInspectedSession(null);
    setInspectorClosing(false);
    requestAnimationFrame(() => {
      if (inspectionTrigger.current?.isConnected) inspectionTrigger.current.focus({ preventScroll: true });
      else roomComposerRef.current?.focus();
    });
  }, []);
  const closeInspector = useCallback(() => {
    if (prefersReducedMotion() || motionDurationMs("--environment-panel-exit-duration", 220) === 0) finishClosingInspector();
    else setInspectorClosing(true);
  }, [finishClosingInspector]);
  useEffect(() => {
    if (!inspectorClosing) return;
    const timer = window.setTimeout(finishClosingInspector, motionDurationMs("--environment-panel-exit-duration", 220));
    return () => window.clearTimeout(timer);
  }, [inspectorClosing, finishClosingInspector]);
  const inspectSession = (sessionRef: string, turnID: string | undefined, name: string) => {
    if (savingAgent) return;
    if (settingsOpen) closeAgentPanel();
    if (!inspectorClosing && inspectedSession?.sessionRef === sessionRef && inspectedSession.turnID === turnID) {
      closeInspector();
      return;
    }
    setInspectorClosing(false);
    inspectionTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setInspectedSession({ roomID: selectedRoomID, sessionRef, turnID, name });
  };
  const inspectAgentActivity = (agentID: string, fallbackSessionRef?: string) => {
    if (!inspectorClosing && inspectedSession?.agentID === agentID) { closeInspector(); return; }
    setInspectorClosing(false);
    inspectionTrigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setInspectedSession({ roomID: selectedRoomID, agentID, sessionRef: fallbackSessionRef, name: agents.find(agent => agent.id === agentID)?.name ?? agentID });
  };
  const setSelectedRoomID = useCallback((value: string | ((current: string) => string)): void => {
    const base = controlledRoomID ?? internalSelectedRoomID;
    const next = typeof value === "function" ? value(base) : value;
    setInternalSelectedRoomID(next);
    if (next !== base) onSelectRoom?.(next);
  }, [controlledRoomID, internalSelectedRoomID, onSelectRoom]);
  const openSessionRoom = useCallback((roomID: string): void => {
    setSelectedRoomID(roomID);
    // Selecting the current room must still leave the identity page in the app shell.
    if (roomID === selectedRoomID) onSelectRoom?.(roomID);
    onSectionChange?.("rooms");
  }, [onSectionChange, onSelectRoom, selectedRoomID, setSelectedRoomID]);
  useEffect(() => {
    if (inspectedSession && (section !== "rooms" || inspectedSession.roomID !== selectedRoomID)) {
      setInspectedSession(null);
      setInspectorClosing(false);
    }
  }, [inspectedSession, section, selectedRoomID]);
  const [messagesByRoomID, setMessagesByRoomID] = useState<Record<string, ChannelMessage[]>>({});
  const [coordinatorsByRoomID, setCoordinatorsByRoomID] = useState<Record<string, ChannelMessageListResult["coordinator"]>>({});
  const [responsesByRoomID, setResponsesByRoomID] = useState<Record<string, ChannelResponseActivity[]>>({});
  const responses = useMemo(() => (responsesByRoomID[selectedRoomID] ?? []).filter(
    (response) => !messagesByRoomID[selectedRoomID]?.some((message) => message.id === response.id),
  ), [messagesByRoomID, responsesByRoomID, selectedRoomID]);
  const [sendError, setSendError] = useState<{ roomID: string; message: string } | null>(null);
  const [pendingMessage, setPendingMessage] = useState<ChannelMessage | null>(null);
  const [loadedRoomIDs, setLoadedRoomIDs] = useState<Set<string>>(() => new Set());
  const messages = messagesByRoomID[selectedRoomID] ?? [];
  const [trackedTasks, setTrackedTasks] = useState<ChannelMessage[]>([]);
  const [setupPanel, setSetupPanel] = useState<SetupPanel>(null);
  const [settingsRoomID, setSettingsRoomID] = useState("");
  const [showSettingsMembers, setShowSettingsMembers] = useState(false);
  const [savingAgent, setSavingAgent] = useState(false);
  const [agentSaveError, setAgentSaveError] = useState("");
  const agentEditorGeneration = useRef(0);
  const settingsTriggerRef = useRef<HTMLButtonElement>(null);
  const settingsOpen = section === "rooms" && settingsRoomID === selectedRoomID && Boolean(settingsRoomID);
  useEffect(() => {
    agentEditorGeneration.current += 1;
    setSettingsRoomID("");
    setShowSettingsMembers(false);
    setSetupPanel((panel) => panel === "agent" ? null : panel);
  }, [selectedRoomID, section]);
  useEffect(() => {
    if (!settingsOpen || !showSettingsMembers) return;
    document.querySelector<HTMLButtonElement>(".channel-settings-member, .channel-settings-manage")?.focus({ preventScroll: true });
    const escape = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      event.preventDefault();
      closeAgentPanel();
    };
    window.addEventListener("keydown", escape);
    return () => window.removeEventListener("keydown", escape);
  }, [settingsOpen, showSettingsMembers]);
  const [onboardingDraft, setOnboardingDraft] = useState<AgentOnboardingDraft | null>(null);
  const [splitWidth, setSplitWidth] = useState(initialChannelSplitWidth);
  const [listCollapsed, setListCollapsed] = useState(initialChannelListCollapsed);
  const [resizingSplit, setResizingSplit] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(true);
  const [sending, setSending] = useState(false);
  const [sendingAgentIDs, setSendingAgentIDs] = useState<Set<string>>(() => new Set());
  const [draftRevision, setDraftRevision] = useState(0);
  const responseSessionsRef = useRef<Set<string>>(new Set());
  responseSessionsRef.current = new Set(responses.map((response) => response.session_ref));
  const [body, setBody] = useState(composerDraft?.prompt ?? "");
  const [composerImages, setComposerImages] = useState<ComposerImage[]>(
    composerDraft?.images ?? [],
  );
  const [composerFiles, setComposerFiles] = useState<ComposerFile[]>(
    composerDraft?.files ?? [],
  );

  const composerRoomIDRef = useRef(selectedRoomID);
  const skipComposerPublishRef = useRef(false);
  useLayoutEffect(() => {
    if (composerRoomIDRef.current === selectedRoomID) return;
    composerRoomIDRef.current = selectedRoomID;
    skipComposerPublishRef.current = true;
    setBody(composerDraft?.prompt ?? "");
    setComposerImages(composerDraft?.images ?? []);
    setComposerFiles(composerDraft?.files ?? []);
  }, [composerDraft, selectedRoomID]);

  useEffect(() => {
    if (!selectedRoomID) return;
    if (skipComposerPublishRef.current) {
      skipComposerPublishRef.current = false;
      return;
    }
    onComposerDraftChange?.({
      prompt: body,
      images: composerImages,
      files: composerFiles,
    });
  }, [body, composerFiles, composerImages, onComposerDraftChange, selectedRoomID]);
  const [agentName, setAgentName] = useState("");
  const [agentRole, setAgentRole] = useState("");
  const [agentAvatarKey, setAgentAvatarKey] = useState<string>(() => randomAgentAvatarKey());
  const [agentAvatarImage, setAgentAvatarImage] = useState("");
  const [agentAvatarError, setAgentAvatarError] = useState("");
  const [agentEngine, setAgentEngine] = useState("wuu");
  const [agentModel, setAgentModel] = useState("");
  const [agentEffort, setAgentEffort] = useState("");
  const [editingAgentID, setEditingAgentID] = useState("");
  const [selectedAgentID, setSelectedAgentID] = useState("");
  const [savingAgentID, setSavingAgentID] = useState("");
  const [resettingAgentID, setResettingAgentID] = useState("");
  const [agentResetStatus, setAgentResetStatus] = useState("");
  const [agentAppearanceOpen, setAgentAppearanceOpen] = useState(false);
  const [roomName, setRoomName] = useState("");
  const [newConversationGroup, setNewConversationGroup] = useState(false);
  const [roomAgentIDs, setRoomAgentIDs] = useState<string[]>([]);
  const [proposalModels, setProposalModels] = useState<Record<string, string>>({});
  const [resolvingProposalID, setResolvingProposalID] = useState("");
  const [roomAvatarImage, setRoomAvatarImage] = useState("");
  const [editingRoomID, setEditingRoomID] = useState("");
  const [newRoomBody, setNewRoomBody] = useState("");
  const [newRoomImages, setNewRoomImages] = useState<ComposerImage[]>([]);
  const [newRoomFiles, setNewRoomFiles] = useState<ComposerFile[]>([]);
  const [newRoomCreatedID, setNewRoomCreatedID] = useState("");
  const [newRoomError, setNewRoomError] = useState("");
  const [creatingRoom, setCreatingRoom] = useState(false);
  const [roomMemberMode, setRoomMemberMode] = useState<RoomMemberMode>(null);
  const [roomMemberSelectionIDs, setRoomMemberSelectionIDs] = useState<string[]>([]);
  const [updatingRoomMembers, setUpdatingRoomMembers] = useState(false);
  const [taskTitle, setTaskTitle] = useState("");
  const [taskRoomID, setTaskRoomID] = useState("");
  const [taskOwnerID, setTaskOwnerID] = useState("");
  const [composerFooterNode, setComposerFooterNode] = useState<HTMLDivElement | null>(null);
  const agentDetailDraftRef = useRef<AgentDetailDraft>({ name: "", role: "", avatarKey: "", avatarImage: "", engine: "wuu", model: "", effort: "" });
  const selectedAgentIDRef = useRef("");
  const previousSectionRef = useRef(section);
  agentDetailDraftRef.current = { name: agentName, role: agentRole, avatarKey: agentAvatarKey, avatarImage: agentAvatarImage, engine: agentEngine, model: agentModel, effort: agentEffort };
  selectedAgentIDRef.current = selectedAgentID;
  const roomComposerRef = useRef<ChannelComposerHandle | null>(null);
  const agentAvatarInputRef = useRef<HTMLInputElement | null>(null);
  const agentDetailAvatarInputRef = useRef<HTMLInputElement | null>(null);
  const roomAvatarInputRef = useRef<HTMLInputElement | null>(null);
  const roomAvatarTargetRef = useRef<string>("");
  const savedRoomNameRef = useRef("");
  const messagesByRoomIDRef = useRef<Map<string, ChannelMessage[]>>(new Map());
  const messageRefreshGenerationByRoomRef = useRef<Map<string, number>>(new Map());
  const messageRefreshInFlightByRoomRef = useRef<Set<string>>(new Set());
  const markedMessageSeqByRoomRef = useRef<Map<string, number>>(new Map());
  const directoryRefreshInFlightRef = useRef(false);
  const trackedTasksRefreshInFlightRef = useRef(false);
  const visibleRoomIDRef = useRef("");
  visibleRoomIDRef.current = section === "rooms" ? selectedRoomID : "";
  const sendingTimersRef = useRef<Map<string, number>>(new Map());
  const splitResizeStartRef = useRef({ x: 0, width: CHANNEL_SPLIT_DEFAULT_WIDTH });
  const messageScroll = useAutoFollowScrollContainer({
    open: section === "rooms" && Boolean(selectedRoomID),
    observeKey: selectedRoomID,
  });
  const acknowledgeMessageMotion = useChannelMessageMotion(
    messageScroll.scrollRef, section === "rooms" ? selectedRoomID : "",
    loadedRoomIDs.has(selectedRoomID), messages,
    pendingMessage?.room_id === selectedRoomID ? pendingMessage.id : undefined,
  );

  const updateSplitWidth = useCallback((width: number): void => {
    const nextWidth = clampChannelSplitWidth(width);
    setSplitWidth(nextWidth);
    window.localStorage.setItem(CHANNEL_SPLIT_WIDTH_KEY, String(nextWidth));
  }, []);

  const toggleListCollapsed = useCallback((): void => {
    setListCollapsed((collapsed) => {
      const nextCollapsed = !collapsed;
      window.localStorage.setItem(CHANNEL_LIST_COLLAPSED_KEY, String(nextCollapsed));
      return nextCollapsed;
    });
  }, []);

  useEffect(() => {
    if (!resizingSplit) return;
    const handlePointerMove = (event: PointerEvent): void => {
      updateSplitWidth(splitResizeStartRef.current.width + event.clientX - splitResizeStartRef.current.x);
    };
    const handlePointerUp = (): void => setResizingSplit(false);
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp, { once: true });
    return () => {
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
    };
  }, [resizingSplit, updateSplitWidth]);

  useEffect(() => {
    if (!newRoomRequest) return;
    openNewRoom();
    onNewRoomRequestHandled?.();
  }, [newRoomRequest]);

  useEffect(() => {
    if (!editAgentRequestID) return;
    const agent = agents.find((candidate) => candidate.id === editAgentRequestID);
    if (!agent) return;
    if (section === "rooms") openConversationAgent(agent);
    else { loadAgentDraft(agent); setSetupPanel("agent"); }
    onEditAgentRequestHandled?.();
  }, [agents, editAgentRequestID]);

  function startSplitResize(event: ReactPointerEvent<HTMLButtonElement>): void {
    event.preventDefault();
    splitResizeStartRef.current = { x: event.clientX, width: splitWidth };
    setResizingSplit(true);
  }

  function handleSplitResizeKeyDown(event: KeyboardEvent<HTMLButtonElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      updateSplitWidth(splitWidth - CHANNEL_SPLIT_WIDTH_STEP);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      updateSplitWidth(splitWidth + CHANNEL_SPLIT_WIDTH_STEP);
    } else if (event.key === "Home") {
      event.preventDefault();
      updateSplitWidth(CHANNEL_SPLIT_MIN_WIDTH);
    } else if (event.key === "End") {
      event.preventDefault();
      updateSplitWidth(CHANNEL_SPLIT_MAX_WIDTH);
    }
  }

  const selectedRoom = useMemo(
    () => rooms.find((room) => room.id === selectedRoomID),
    [rooms, selectedRoomID],
  );
  const selectedRoomTitle = useMemo(() => {
    if (!selectedRoom || selectedRoom.kind !== "dm") return selectedRoom?.name ?? "";
    const agentID = selectedRoom.members.find((member) => member.member_type === "agent")?.member_id;
    return agents.find((agent) => agent.id === agentID)?.name ?? selectedRoom.name;
  }, [agents, selectedRoom]);
  const newRoomSelectedAgents = useMemo(() => roomAgentIDs
    .map((agentID) => agents.find((agent) => agent.id === agentID))
    .filter((agent): agent is NamedAgent => Boolean(agent)), [agents, roomAgentIDs]);
  const newRoomName = useMemo(() => new Intl.ListFormat(locale, { style: "short", type: "conjunction" })
    .format(newRoomSelectedAgents.map((agent) => agent.name)), [locale, newRoomSelectedAgents]);
  const composingNewRoom = setupPanel === "room" && !editingRoomID;
  const roomIncludesCurrentUser = selectedRoom?.members.some(
    (member) => member.member_type === "human" && member.member_id === "local-user",
  ) ?? false;
  const messageAgents = agents;
  const agentNames = useMemo(
    () => new Map(messageAgents.map((agent) => [agent.id, agent.name])),
    [messageAgents],
  );
  const selectedAgent = useMemo(
    () => agents.find((agent) => agent.id === selectedAgentID),
    [agents, selectedAgentID],
  );
  const selectedAgentRooms = useMemo(
    () => selectedAgent ? rooms.filter((room) => !archivedRoomIDs.includes(room.id) && room.members.some(
      (member) => member.member_type === "agent" && member.member_id === selectedAgent.id,
    )) : [],
    [archivedRoomIDs, rooms, selectedAgent],
  );
  useEffect(() => {
    if (previousSectionRef.current === "agents" && section !== "agents" && selectedAgentIDRef.current) {
      void saveAgentDetails(selectedAgentIDRef.current, agentDetailDraftRef.current);
    }
    previousSectionRef.current = section;
  }, [section]);
  useEffect(() => {
    if (selectedAgentID && !selectedAgent) setSelectedAgentID("");
  }, [selectedAgent, selectedAgentID]);
  const selectedRoomAgents = useMemo(() => {
    const memberIDs = new Set(
      selectedRoom?.members
        .filter((member) => member.member_type === "agent")
        .map((member) => member.member_id) ?? [],
    );
    return agents.filter((agent) => memberIDs.has(agent.id));
  }, [agents, selectedRoom]);
  const taskRoom = useMemo(
    () => rooms.find((room) => room.id === (taskRoomID || selectedRoomID)),
    [rooms, selectedRoomID, taskRoomID],
  );
  const taskOwnerAgents = useMemo(() => {
    const memberIDs = new Set(
      taskRoom?.members
        .filter((member) => member.member_type === "agent")
        .map((member) => member.member_id) ?? [],
    );
    return agents.filter((agent) => memberIDs.has(agent.id));
  }, [agents, taskRoom]);
  useEffect(() => {
    if (setupPanel !== "task") return;
    setTaskOwnerID((current) => (
      taskOwnerAgents.some((agent) => agent.id === current)
        ? current
        : (taskOwnerAgents[0]?.id ?? "")
    ));
  }, [setupPanel, taskOwnerAgents]);
  const channelTimeline = useMemo(() => buildChannelTimeline(messages), [messages]);
  const activityFor = useCallback((agent?: NamedAgent): AgentActivityStatus => {
    if (!agent) return "idle";
    if (sendingAgentIDs.has(agent.id)) return "sending";
    return agent.activity_status === "thinking" ? "thinking" : "idle";
  }, [sendingAgentIDs]);
  const activityText = useCallback(
    (status: AgentActivityStatus): string => t(`channels.agentStatus.${status}`),
    [t],
  );
  const respondingAgents = useMemo(() => {
    const roomAgentIDs = new Set(
      selectedRoom?.members
        .filter((member) => member.member_type === "agent")
        .map((member) => member.member_id) ?? [],
    );
    return agents
      .filter((agent) => roomAgentIDs.has(agent.id))
      .map((agent) => ({ agent, status: activityFor(agent) }))
      .filter(({ agent, status }) => {
        if (status === "sending") return true;
        if (status !== "thinking") return false;
        const activityRoomIDs = agent.activity_room_ids;
        // Activity observed through another app-server is intentionally coarse
        // and has no room IDs. Keep an active room member visible unless the
        // backend explicitly scopes that activity to a different room.
        return !activityRoomIDs?.length || activityRoomIDs.includes(selectedRoomID);
      });
  }, [activityFor, agents, selectedRoom, selectedRoomID]);
  const responseActivities = useMemo(() => {
    const memberOrder = new Map(selectedRoom?.members.map((member, index) => [member.member_id, index]));
    return [...responses].sort((left, right) => (memberOrder.get(left.agent_id) ?? Infinity) - (memberOrder.get(right.agent_id) ?? Infinity)
      || left.agent_id.localeCompare(right.agent_id) || left.id.localeCompare(right.id));
  }, [responses, selectedRoom]);
  const modelGroups = useMemo<SelectMenuGroup[]>(() => {
    const inherited = initialized ? `${initialized.provider} · ${initialized.model}` : undefined;
    const groups: SelectMenuGroup[] = [{ options: [{ value: "", label: t("channels.inheritModel"), hint: inherited }] }];
    for (const provider of initialized?.providers ?? []) {
      const models = provider.models?.length ? provider.models : [{ id: provider.model, display_name: provider.model }];
      groups.push({
        label: provider.name,
        options: models.filter((model) => model.id).map((model) => ({
          value: `${provider.name}\u0000${model.id}`,
          label: model.display_name || model.id,
          hint: model.id,
        })),
      });
    }
    // An agent's override can outlive the provider entry it points at (the
    // provider was renamed or removed from config). Surface the stored value
    // as its own group so the trigger reflects reality instead of rendering
    // an empty placeholder.
    if (agentModel && !groups.some((group) => group.options.some((option) => option.value === agentModel))) {
      const [providerName, modelID] = agentModel.split("\u0000");
      groups.push({
        label: providerName || undefined,
        options: [{ value: agentModel, label: modelID || agentModel, hint: t("channels.providerMissing"), disabled: agentEngine !== "wuu" }],
      });
    }
    return groups;
  }, [initialized, agentModel, agentEngine, t]);
  const [agentProviderName, agentModelID] = agentModel.split("\u0000");
  const agentProvider = initialized?.providers?.find((provider) => provider.name === agentProviderName);
  const agentEffortOptions = agentEngine === "wuu"
    ? providerModelEffortOptions(agentProvider, agentModelID ?? "", agentEffort)
    : [];

  function selectAgentModel(value: string): void {
    setAgentEngine("wuu");
    setAgentModel(value);
    setAgentEffort("");
  }

  const refreshRoomsAndAgents = useCallback(async (): Promise<void> => {
    if (!window.wuu) return;
    const result = await window.wuu.bootstrapChannels();
    const nextAgents = result.agents ?? [];
    const nextRooms = result.rooms ?? [];
    setAgents((current) =>
      sameNamedAgents(current, nextAgents) ? current : nextAgents,
    );
    setRooms((current) =>
      sameChannelRooms(current, nextRooms) ? current : nextRooms,
    );
    setSelectedRoomID((current) =>
      current && result.rooms.some((room) => room.id === current)
        ? current
        : (result.rooms[0]?.id ?? ""),
    );
  }, [setAgents, setRooms]);

  const markRoomRead = useCallback((roomID: string, latestMessageSeq: number): void => {
    if (!roomID || !window.wuu) return;
    const markedSeq = markedMessageSeqByRoomRef.current.get(roomID) ?? 0;
    if (latestMessageSeq <= markedSeq) return;
    markedMessageSeqByRoomRef.current.set(roomID, latestMessageSeq);
    setRooms((current) => current.map((room) => room.id === roomID ? { ...room, unread_count: 0 } : room));
    onRoomRead?.(roomID);
    void window.wuu.markChannelRoomRead({ room_id: roomID }).catch((reason: unknown) => {
      if (markedMessageSeqByRoomRef.current.get(roomID) === latestMessageSeq) {
        markedMessageSeqByRoomRef.current.delete(roomID);
      }
      showErrorToast(reason);
    });
  }, [onRoomRead]);

  const refreshMessages = useCallback(async (roomID: string, force = false): Promise<void> => {
    if (!window.wuu || !roomID) return;
    if (
      !force &&
      messageRefreshInFlightByRoomRef.current.has(roomID)
    ) {
      return;
    }
    messageRefreshInFlightByRoomRef.current.add(roomID);
    const generation = (messageRefreshGenerationByRoomRef.current.get(roomID) ?? 0) + 1;
    messageRefreshGenerationByRoomRef.current.set(roomID, generation);
    try {
      const result = await readChannelMessages(roomID);
      if (messageRefreshGenerationByRoomRef.current.get(roomID) !== generation) return;
      setCoordinatorsByRoomID((current) => JSON.stringify(current[roomID]) === JSON.stringify(result.coordinator) ? current : { ...current, [roomID]: result.coordinator });
      if (result.responses !== undefined) {
        // Only committed messages belong in the transcript. Token updates must
        // not rerender it, scroll it, or expose incomplete answers as messages.
        const nextResponses = result.responses.map(({ body: _body, ...activity }) => activity);
        setResponsesByRoomID((current) => JSON.stringify(current[roomID]) === JSON.stringify(nextResponses) ? current : { ...current, [roomID]: nextResponses });
      }
      const nextMessages = result.messages ?? [];
      const previousMessages = messagesByRoomIDRef.current.get(roomID) ?? [];
      const messagesUnchanged = sameChannelMessages(previousMessages, nextMessages);
      setLoadedRoomIDs((current) => {
        if (current.has(roomID)) return current;
        return new Set(current).add(roomID);
      });
      if (messagesUnchanged) {
        if (visibleRoomIDRef.current === roomID) {
          const latestMessageSeq = nextMessages.reduce(
            (latest, message) => Math.max(latest, message.seq),
            0,
          );
          if (latestMessageSeq > 0) markRoomRead(roomID, latestMessageSeq);
        }
        return;
      }
      messagesByRoomIDRef.current.set(roomID, nextMessages);
      setMessagesByRoomID((current) => ({ ...current, [roomID]: nextMessages }));
      if (visibleRoomIDRef.current !== roomID) return;
      const known = new Set(previousMessages.map((message) => message.id));
      if (known.size > 0) {
        for (const message of nextMessages) {
          if (message.author_type !== "agent" || known.has(message.id)) continue;
          const agentID = message.author_id;
          const previousTimer = sendingTimersRef.current.get(agentID);
          if (previousTimer) window.clearTimeout(previousTimer);
          setSendingAgentIDs((current) => new Set(current).add(agentID));
          const timer = window.setTimeout(() => {
            setSendingAgentIDs((current) => {
              const next = new Set(current);
              next.delete(agentID);
              return next;
            });
            sendingTimersRef.current.delete(agentID);
          }, 1_800);
          sendingTimersRef.current.set(agentID, timer);
        }
      }
      const latestMessageSeq = nextMessages.reduce(
        (latest, message) => Math.max(latest, message.seq),
        0,
      );
      if (latestMessageSeq > 0) markRoomRead(roomID, latestMessageSeq);
    } finally {
      if (messageRefreshGenerationByRoomRef.current.get(roomID) === generation) messageRefreshInFlightByRoomRef.current.delete(roomID);
    }
  }, [markRoomRead]);

  const refreshTrackedTasks = useCallback(async (): Promise<void> => {
    if (trackedTasksRefreshInFlightRef.current) return;
    trackedTasksRefreshInFlightRef.current = true;
    try {
      if (!window.wuu || rooms.length === 0) {
        setTrackedTasks((current) => current.length === 0 ? current : []);
        return;
      }
      const results = await Promise.all(
        rooms.map((room) => readChannelMessages(room.id)),
      );
      const nextTasks = results
        .flatMap((result) => result.messages ?? [])
        .filter((message) => message.kind === "task")
        .sort((left, right) => right.created_at.localeCompare(left.created_at));
      setTrackedTasks((current) =>
        sameChannelMessages(current, nextTasks) ? current : nextTasks,
      );
    } finally {
      trackedTasksRefreshInFlightRef.current = false;
    }
  }, [rooms]);

  useEffect(() => {
    if (directoryIsControlled) {
      setLoading(false);
      return;
    }
    let active = true;
    void refreshRoomsAndAgents()
      .catch((reason: unknown) => {
        if (active) setLoadError(toastErrorMessage(reason));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [directoryIsControlled, refreshRoomsAndAgents]);

  useEffect(() => {
    if (directoryIsControlled) return;
    if (!window.wuu) return;
    let active = true;
    const refresh = (): void => {
      if (directoryRefreshInFlightRef.current) return;
      directoryRefreshInFlightRef.current = true;
      void Promise.all([
        window.wuu!.listNamedAgents(),
        window.wuu!.listChannelRooms(),
      ]).then(([agentResult, roomResult]) => {
        if (!active) return;
        setLoadError("");
        const nextAgents = agentResult.agents ?? [];
        const nextRooms = roomResult.rooms ?? [];
        setAgents((current) =>
          sameNamedAgents(current, nextAgents) ? current : nextAgents,
        );
        setRooms((current) =>
          sameChannelRooms(current, nextRooms) ? current : nextRooms,
        );
      }).catch((reason: unknown) => {
        if (active) setLoadError(toastErrorMessage(reason));
      }).finally(() => {
        directoryRefreshInFlightRef.current = false;
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 1_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [directoryIsControlled, setAgents, setRooms]);

  useEffect(() => {
    if (!window.wuu || typeof window.wuu.getNamedAgentInsights !== "function" || section !== "agents") return;
    let active = true;
    const refresh = (): void => {
      void window.wuu!.getNamedAgentInsights().then((result) => {
        if (!active) return;
        setAgentInsights(Object.fromEntries((result.insights ?? []).map((insight) => [insight.agent_id, insight])));
      }).catch(() => {
        // Activity statistics are an enhancement; the relationship graph and
        // agent management remain usable if historical aggregation fails.
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 60_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [section]);

  useEffect(() => {
    if (section !== "rooms" || !selectedRoomID) return;
    let active = true;
    const refresh = (): void => {
      void refreshMessages(selectedRoomID).catch((reason: unknown) => {
        if (active) setLoadError(toastErrorMessage(reason));
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 2_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [refreshMessages, section, selectedRoomID]);

  useEffect(() => {
    if (section !== "rooms" || !selectedRoomID || typeof window.wuu?.onServerEvent !== "function") return;
    let timer: number | undefined;
    let active = true;
    const off = window.wuu.onServerEvent((event) => {
      if (event.kind !== "notification") return;
      const { method, params } = event.message;
      if (!["item/agentMessage/delta", "item/agentMessage/replace", "item/started", "item/completed", "turn/completed"].includes(method)) return;
      const threadID = (params as { thread_id?: string } | undefined)?.thread_id;
      if (!threadID || !responseSessionsRef.current.has(threadID) || timer !== undefined) return;
      // Events are invalidation signals only; the room API owns the public
      // projection and keeps private tool/reasoning text out of this view.
      timer = window.setTimeout(() => {
        timer = undefined;
        void refreshMessages(selectedRoomID).catch((reason: unknown) => {
          if (active) setLoadError(toastErrorMessage(reason));
        });
      }, 200);
    });
    return () => {
      active = false;
      off();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [refreshMessages, section, selectedRoomID]);

  useEffect(() => () => {
    for (const timer of sendingTimersRef.current.values()) window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    if (section !== "tasks") return;
    let active = true;
    const refresh = (): void => {
      void refreshTrackedTasks().catch((reason: unknown) => {
        if (active) setLoadError(toastErrorMessage(reason));
      });
    };
    refresh();
    const timer = window.setInterval(refresh, 2_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [refreshTrackedTasks, section]);

  useEffect(() => {
    messageScroll.scrollToBottom();
  }, [messageScroll, messages.length, messages.at(-1)?.id, pendingMessage]);

  async function submitAgent(): Promise<void> {
    if (!window.wuu || !editingAgentID || !agentName.trim() || savingAgent) return;
    const savedAgentID = editingAgentID;
    const generation = agentEditorGeneration.current;
    setSavingAgent(true);
    setAgentSaveError("");
    try {
      const [providerOverride, modelOverride] = agentModel.split("\u0000");
      const params = {
        name: agentName.trim(),
        role: agentRole.trim() || undefined,
        avatar_key: agentAvatarKey,
        avatar_image: agentAvatarImage,
        engine_override: agentEngine === "wuu" ? undefined : agentEngine,
        provider_override: providerOverride || undefined,
        model_override: modelOverride || undefined,
        effort_override: modelOverride && agentEffort ? agentEffort : undefined,
      };
      await window.wuu.updateNamedAgent({ agent_id: savedAgentID, ...params });
      await refreshRoomsAndAgents();
      if (generation === agentEditorGeneration.current) closeAgentPanel();
    } catch (reason) {
      if (generation === agentEditorGeneration.current) setAgentSaveError(toastErrorMessage(reason));
      showErrorToast(reason);
    } finally {
      setSavingAgent(false);
    }
  }

  function openAgentOnboarding(): void {
    if (selectedAgentID) void saveAgentDetails(selectedAgentID, agentDetailDraftRef.current);
    setSetupPanel(null);
    if (onCreateAgent) onCreateAgent();
    else setOnboardingDraft((current) => current ?? createAgentOnboardingDraft(initialized));
  }

  async function submitRoom(): Promise<void> {
    const name = roomName.trim();
    if (!window.wuu || !name) return;
    try {
      if (editingRoomID) {
        const roomID = editingRoomID;
        const result = await window.wuu.updateChannelRoom({
          room_id: roomID,
          name,
          agent_ids: roomAgentIDs,
        });
        if (result.room.id === roomID) {
          setRooms((current) => current.map((room) => room.id === result.room.id ? result.room : room));
        }
        await refreshRoomsAndAgents();
        setSelectedRoomID(roomID);
        closeRoomPanel();
        return;
      }
      const result = await window.wuu.createChannelRoom({
        name,
        avatar_image: roomAvatarImage || undefined,
        agent_ids: roomAgentIDs,
      });
      setRoomName("");
      setRoomAgentIDs([]);
      setRoomAvatarImage("");
      setSetupPanel(null);
      await refreshRoomsAndAgents();
      setSelectedRoomID(result.room.id);
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  async function sendMessage(promptOverride?: string): Promise<void> {
    const messageBody = (promptOverride ?? body).trim();
    if (!window.wuu || !selectedRoomID || (!messageBody && composerImages.length === 0 && composerFiles.length === 0) || sending) return;
    const roomID = selectedRoomID;
    const sentImageIDs = new Set(composerImages.map((image) => image.id));
    const sentFileIDs = new Set(composerFiles.map((file) => file.id));
    const pending: ChannelMessage = { id: `pending:${crypto.randomUUID()}`, room_id: roomID, seq: 0, author_type: "human", author_id: "local-user", kind: "text", body: messageBody, created_at: new Date().toISOString() };
    setSending(true);
    setSendError(null);
    setPendingMessage(pending);
    messageScroll.scrollToBottom({ force: true });
    try {
      const resolvedImages = await awaitComposerImages(composerImages);
      const images = inputImagesFromComposer(resolvedImages);
      const files = inputFilesFromComposer(composerFiles);
      setPendingMessage({ ...pending, images, files });
      const result = await window.wuu.sendChannelMessage({ room_id: roomID, body: messageBody, images, files });
      acknowledgeMessageMotion(pending.id, result.message.id);
      // The acknowledged message is already durable. Show it immediately and
      // invalidate any list snapshot that started before this send completed.
      messageRefreshGenerationByRoomRef.current.set(roomID, (messageRefreshGenerationByRoomRef.current.get(roomID) ?? 0) + 1);
      const current = messagesByRoomIDRef.current.get(roomID) ?? [];
      const nextMessages = [...current.filter((message) => message.id !== result.message.id), result.message].sort((left, right) => left.seq - right.seq);
      messagesByRoomIDRef.current.set(roomID, nextMessages);
      setMessagesByRoomID((all) => ({ ...all, [roomID]: nextMessages }));
      if (composerRoomIDRef.current === roomID) {
        setBody((currentBody) => currentBody.trim() === messageBody || currentBody === body ? "" : currentBody);
        setComposerImages((currentImages) => currentImages.filter((image) => !sentImageIDs.has(image.id)));
        setComposerFiles((currentFiles) => currentFiles.filter((file) => !sentFileIDs.has(file.id)));
      }
      void refreshMessages(roomID, true).catch((reason: unknown) => {
        if (visibleRoomIDRef.current === roomID) setLoadError(toastErrorMessage(reason));
      });
    } catch (reason) {
      setSendError({ roomID, message: toastErrorMessage(reason) });
      if (composerRoomIDRef.current === roomID) {
        setBody((currentBody) => currentBody === body ? messageBody : currentBody);
        setDraftRevision((revision) => revision + 1);
      }
    } finally {
      setPendingMessage(null);
      setSending(false);
    }
  }

  async function attachMessageFiles(files: File[]): Promise<void> {
    try {
      await buildComposerAttachments(
        files,
        (placeholder) => setComposerImages((current) => [...current, placeholder]),
        (encoded) => setComposerImages((current) => current.map((image) => image.id === encoded.id ? encoded : image)),
        (file) => setComposerFiles((current) => [...current, file]),
      );
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  async function submitTask(): Promise<void> {
    const title = taskTitle.trim();
    const roomID = taskRoomID || selectedRoomID;
    if (!window.wuu || !roomID || !title || !taskOwnerID) return;
    try {
      await window.wuu.createChannelTask({
        room_id: roomID,
        title,
        owner_id: taskOwnerID,
      });
      setTaskTitle("");
      setTaskRoomID("");
      setTaskOwnerID("");
      setSetupPanel(null);
      await refreshMessages(selectedRoomID, true);
      if (section === "tasks") await refreshTrackedTasks();
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  async function resolveAgentCreation(proposalID: string, approve: boolean): Promise<void> {
    if (!window.wuu || !selectedRoomID) return;
    const selection = proposalModels[proposalID] ?? "";
    const [provider = "", model = ""] = selection.split("\u0000");
    setResolvingProposalID(proposalID);
    try {
      await window.wuu.resolveChannelAgentCreation({
        proposal_id: proposalID,
        approve,
        provider: approve ? provider : undefined,
        model: approve ? model : undefined,
      });
      await Promise.all([refreshMessages(selectedRoomID, true), refreshRoomsAndAgents()]);
    } catch (reason) {
      showErrorToast(reason);
    } finally {
      setResolvingProposalID("");
    }
  }

  async function deleteAgent(agentID: string): Promise<void> {
    if (!window.wuu) return;
    if (!window.confirm(t("channels.deleteAgentConfirm", { name: agentName.trim() }))) return;
    try {
      await window.wuu.deleteNamedAgent({ agent_id: agentID });
      closeAgentPanel();
      await refreshRoomsAndAgents();
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  async function resetAgent(agentID: string): Promise<void> {
    if (!window.wuu) return;
    if (!window.confirm(t("channels.resetAgentConfirm", { name: agentName.trim() }))) return;
    setAgentResetStatus("");
    setResettingAgentID(agentID);
    try {
      const result = await window.wuu.resetNamedAgent({ agent_id: agentID });
      setAgentResetStatus(t(result.requested ? "channels.resetAgentRequested" : "channels.resetAgentIdle"));
    } catch (reason) {
      showErrorToast(reason);
    } finally {
      setResettingAgentID("");
    }
  }

  function loadAgentDraft(agent: NamedAgent): void {
    agentEditorGeneration.current += 1;
    setAgentSaveError("");
    setAgentAppearanceOpen(false);
    setEditingAgentID(agent.id);
    setAgentName(agent.name);
    setAgentRole(agent.role ?? "");
    setAgentAvatarKey(agent.avatar_key);
    setAgentAvatarImage(agent.avatar_image ?? "");
    setAgentAvatarError("");
    const engine = agent.engine_override || "wuu";
    setAgentEngine(engine);
    setAgentModel(agent.model_override ? `${engine === "wuu" ? agent.provider_override ?? "" : ""}\u0000${agent.model_override}` : "");
    setAgentEffort(agent.effort_override ?? "");
    setAgentResetStatus("");
  }

  function selectAgentDetails(agent: NamedAgent): void {
    if (selectedAgentID === agent.id) return;
    if (selectedAgentID) void saveAgentDetails(selectedAgentID, agentDetailDraftRef.current);
    setSelectedAgentID(agent.id);
    loadAgentDraft(agent);
  }

  async function saveAgentDetails(agentID: string, draft: AgentDetailDraft = agentDetailDraftRef.current): Promise<void> {
    if (!window.wuu || !draft.name.trim()) return;
    const [providerOverride, modelOverride] = draft.model.split("\u0000");
    const effortOverride = modelOverride && draft.effort ? draft.effort : undefined;
    const currentAgent = agents.find((agent) => agent.id === agentID);
    if (currentAgent
      && currentAgent.name === draft.name.trim()
      && (currentAgent.role ?? "") === draft.role.trim()
      && currentAgent.avatar_key === draft.avatarKey
      && (currentAgent.avatar_image ?? "") === draft.avatarImage
      && (currentAgent.engine_override || "wuu") === draft.engine
      && (currentAgent.provider_override ?? "") === (providerOverride ?? "")
      && (currentAgent.model_override ?? "") === (modelOverride ?? "")
      && (currentAgent.effort_override ?? "") === (effortOverride ?? "")) return;
    setSavingAgentID(agentID);
    try {
      const result = await window.wuu.updateNamedAgent({
        agent_id: agentID,
        name: draft.name.trim(),
        role: draft.role.trim() || undefined,
        avatar_key: draft.avatarKey,
        avatar_image: draft.avatarImage,
        engine_override: draft.engine === "wuu" ? undefined : draft.engine,
        provider_override: providerOverride || undefined,
        model_override: modelOverride || undefined,
        effort_override: effortOverride,
      });
      setAgents((current) => current.map((agent) => agent.id === result.agent.id ? result.agent : agent));
    } catch (reason) {
      showErrorToast(reason);
    } finally {
      setSavingAgentID("");
    }
  }

  function closeAgentPanel(): void {
    agentEditorGeneration.current += 1;
    if (settingsOpen) requestAnimationFrame(() => settingsTriggerRef.current?.focus({ preventScroll: true }));
    setSettingsRoomID("");
    setShowSettingsMembers(false);
    setSetupPanel(null);
    setEditingAgentID("");
    setAgentName("");
    setAgentRole("");
    setAgentAvatarKey(randomAgentAvatarKey());
    setAgentAvatarImage("");
    setAgentAvatarError("");
    setAgentEngine("wuu");
    setAgentModel("");
    setAgentEffort("");
    setAgentResetStatus("");
  }

  function openConversationAgent(agent: NamedAgent): void {
    if (savingAgent) return;
    setInspectedSession(null);
    setInspectorClosing(false);
    loadAgentDraft(agent);
    setSettingsRoomID(selectedRoomID);
    setShowSettingsMembers(false);
    setSetupPanel("agent");
  }

  function openConversationSettings(): void {
    if (!selectedRoom || savingAgent) return;
    if (settingsOpen) { closeAgentPanel(); return; }
    if (selectedRoom.kind === "dm" && selectedRoomAgents[0]) {
      openConversationAgent(selectedRoomAgents[0]);
    } else {
      setInspectedSession(null);
      setInspectorClosing(false);
      setSetupPanel(null);
      setSettingsRoomID(selectedRoomID);
      setShowSettingsMembers(true);
    }
  }

  function toggleRoomAgent(agentID: string): void {
    setRoomAgentIDs((current) =>
      current.includes(agentID)
        ? current.filter((candidate) => candidate !== agentID)
        : current.length < MAX_ROOM_AGENTS ? [...current, agentID] : current,
    );
  }

  function toggleRoomMemberSelection(agentID: string): void {
    setRoomMemberSelectionIDs((current) =>
      current.includes(agentID)
        ? current.filter((candidate) => candidate !== agentID)
        : roomAgentIDs.length + current.length < MAX_ROOM_AGENTS ? [...current, agentID] : current,
    );
  }

  function openRoomMemberMode(mode: Exclude<RoomMemberMode, null>): void {
    setRoomMemberSelectionIDs([]);
    setRoomMemberMode(mode);
  }

  function closeRoomMemberMode(): void {
    setRoomMemberMode(null);
    setRoomMemberSelectionIDs([]);
  }

  async function openDirectConversation(agentID: string): Promise<void> {
    if (creatingRoom) return;
    setCreatingRoom(true);
    setNewRoomError("");
    try {
      const { room } = await window.wuu.openChannelDirectMessage({ agent_id: agentID });
      setRooms((current) => [...current.filter((entry) => entry.id !== room.id), room]);
      closeRoomPanel();
      openSessionRoom(room.id);
    } catch (reason) {
      setNewRoomError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setCreatingRoom(false);
    }
  }

  function openNewRoom(): void {
    setNewConversationGroup(false);
    setEditingRoomID("");
    setRoomName("");
    setRoomAgentIDs([]);
    setRoomAvatarImage("");
    setNewRoomBody("");
    setNewRoomImages([]);
    setNewRoomFiles([]);
    setNewRoomCreatedID("");
    setNewRoomError("");
    closeRoomMemberMode();
    setSetupPanel("room");
  }

  function editRoom(room: ChannelRoom): void {
    setEditingRoomID(room.id);
    setRoomName(room.name);
    setRoomAgentIDs(room.members
      .filter((member) => member.member_type === "agent")
      .map((member) => member.member_id));
    setRoomAvatarImage(room.avatar_image ?? "");
    savedRoomNameRef.current = room.name;
    closeRoomMemberMode();
    setSetupPanel("room");
  }

  function closeRoomPanel(): void {
    if (editingRoomID) void persistRoomName();
    if (!editingRoomID && newRoomCreatedID) setSelectedRoomID(newRoomCreatedID);
    setSetupPanel(null);
    setEditingRoomID("");
    setRoomName("");
    setRoomAgentIDs([]);
    setRoomAvatarImage("");
    setNewRoomBody("");
    setNewRoomImages([]);
    setNewRoomFiles([]);
    setNewRoomCreatedID("");
    setNewRoomError("");
    closeRoomMemberMode();
  }

  async function attachNewRoomFiles(files: File[]): Promise<void> {
    try {
      await buildComposerAttachments(
        files,
        (placeholder) => setNewRoomImages((current) => [...current, placeholder]),
        (encoded) => setNewRoomImages((current) => current.map((image) => image.id === encoded.id ? encoded : image)),
        (file) => setNewRoomFiles((current) => [...current, file]),
      );
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  async function createRoomWithFirstMessage(promptOverride?: string): Promise<void> {
    const messageBody = (promptOverride ?? newRoomBody).trim();
    if (!window.wuu || roomAgentIDs.length === 0 || (!messageBody && newRoomImages.length === 0 && newRoomFiles.length === 0) || creatingRoom) return;
    setCreatingRoom(true);
    setNewRoomError("");
    let roomID = newRoomCreatedID;
    try {
      const resolvedImages = await awaitComposerImages(newRoomImages);
      const images = inputImagesFromComposer(resolvedImages);
      const files = inputFilesFromComposer(newRoomFiles);
      if (!roomID) {
        const result = await window.wuu.createChannelRoom({
          name: newRoomName || t("channels.newGroup"),
          agent_ids: roomAgentIDs,
        });
        roomID = result.room.id;
        setNewRoomCreatedID(roomID);
        setRooms((current) => [...current.filter((room) => room.id !== result.room.id), result.room]);
      }
      const result = await window.wuu.sendChannelMessage({ room_id: roomID, body: messageBody, images, files });
      messagesByRoomIDRef.current.set(roomID, [result.message]);
      setMessagesByRoomID((current) => ({ ...current, [roomID]: [result.message] }));
      setLoadedRoomIDs((current) => new Set(current).add(roomID));
      await refreshRoomsAndAgents();
      setSelectedRoomID(roomID);
      setSetupPanel(null);
      setRoomAgentIDs([]);
      setNewRoomBody("");
      setNewRoomImages([]);
      setNewRoomFiles([]);
      setNewRoomCreatedID("");
    } catch (reason) {
      setNewRoomError(toastErrorMessage(reason));
    } finally {
      setCreatingRoom(false);
    }
  }

  async function persistRoomName(): Promise<void> {
    const name = roomName.trim();
    if (!window.wuu || !editingRoomID || !name || name === savedRoomNameRef.current) return;
    const previousName = savedRoomNameRef.current;
    savedRoomNameRef.current = name;
    try {
      const result = await window.wuu.updateChannelRoom({
        room_id: editingRoomID,
        name,
        agent_ids: roomAgentIDs,
      });
      setRooms((current) => current.map((candidate) => candidate.id === editingRoomID ? result.room : candidate));
      setRoomName(result.room.name);
      savedRoomNameRef.current = result.room.name;
    } catch (reason) {
      savedRoomNameRef.current = previousName;
      showErrorToast(reason);
    }
  }

  async function submitRoomMemberChange(): Promise<void> {
    if (!window.wuu || !editingRoomID || !roomMemberMode || roomMemberSelectionIDs.length === 0 || updatingRoomMembers) return;
    const room = rooms.find((candidate) => candidate.id === editingRoomID);
    if (!room) return;
    const nextAgentIDs = [...roomAgentIDs, ...roomMemberSelectionIDs.filter((agentID) => !roomAgentIDs.includes(agentID))];
    setUpdatingRoomMembers(true);
    try {
      const result = await window.wuu.updateChannelRoom({
        room_id: editingRoomID,
        name: roomName.trim() || room.name,
        agent_ids: nextAgentIDs,
      });
      setRooms((current) => current.map((candidate) => candidate.id === editingRoomID ? result.room : candidate));
      setRoomAgentIDs(nextAgentIDs);
      closeRoomMemberMode();
      await refreshRoomsAndAgents();
    } catch (reason) {
      showErrorToast(reason);
    } finally {
      setUpdatingRoomMembers(false);
    }
  }

  async function removeRoomMember(agentID: string): Promise<void> {
    if (!window.wuu || !editingRoomID || updatingRoomMembers) return;
    const room = rooms.find((candidate) => candidate.id === editingRoomID);
    const agent = agents.find((candidate) => candidate.id === agentID);
    if (!room) return;
    if (!window.confirm(t("channels.removeMemberConfirm", { name: agent?.name ?? agentID }))) return;
    const nextAgentIDs = roomAgentIDs.filter((candidate) => candidate !== agentID);
    setUpdatingRoomMembers(true);
    try {
      const result = await window.wuu.updateChannelRoom({
        room_id: editingRoomID,
        name: roomName.trim() || room.name,
        agent_ids: nextAgentIDs,
      });
      setRooms((current) => current.map((candidate) => candidate.id === editingRoomID ? result.room : candidate));
      setRoomAgentIDs(nextAgentIDs);
      await refreshRoomsAndAgents();
    } catch (reason) {
      showErrorToast(reason);
    } finally {
      setUpdatingRoomMembers(false);
    }
  }

  async function deleteRoom(): Promise<void> {
    if (!window.wuu || !editingRoomID) return;
    if (!window.confirm(t("channels.deleteRoomConfirm", { name: roomName.trim() }))) return;
    try {
      const roomID = editingRoomID;
      await window.wuu.deleteChannelRoom({ room_id: roomID });
      closeRoomPanel();
      messagesByRoomIDRef.current.delete(roomID);
      setMessagesByRoomID((current) => {
        const next = { ...current };
        delete next[roomID];
        return next;
      });
      setLoadedRoomIDs((current) => {
        const next = new Set(current);
        next.delete(roomID);
        return next;
      });
      await refreshRoomsAndAgents();
    } catch (reason) {
      showErrorToast(reason);
    }
  }

  function chooseRoomAvatar(roomID: string): void {
    roomAvatarTargetRef.current = roomID;
    roomAvatarInputRef.current?.click();
  }

  async function updateRoomAvatarFromFile(file: File): Promise<void> {
    if (!window.wuu) return;
    try {
      const avatarImage = await squareAvatarImageFromFile(file);
      const roomID = roomAvatarTargetRef.current;
      if (!roomID) {
        setRoomAvatarImage(avatarImage);
        return;
      }
      const result = await window.wuu.updateChannelRoom({ room_id: roomID, avatar_image: avatarImage });
      setRooms((current) => current.map((room) => room.id === result.room.id ? result.room : room));
      if (editingRoomID === roomID) setRoomAvatarImage(result.room.avatar_image ?? "");
    } catch (reason) {
      showErrorToast(reason, t("channels.invalidAvatarImage"));
    }
  }

  if (onboardingDraft) return <AgentOnboarding
        navigation={navigation}
        draft={onboardingDraft}
        onDraftChange={setOnboardingDraft}
        initialized={initialized}
        onCreate={async (params) => {
          const { agent } = await window.wuu!.createNamedAgent(params);
          setAgents((current) => [...current.filter((entry) => entry.id !== agent.id), agent]);
          return agent;
        }}
        onOpenConversation={async (agent) => {
          const { room } = await window.wuu!.openChannelDirectMessage({ agent_id: agent.id });
          setRooms((current) => [...current.filter((entry) => entry.id !== room.id), room]);
          setSelectedAgentID("");
          openSessionRoom(room.id);
        }}
        onManageProviders={onManageProviders}
        onClose={() => setOnboardingDraft(null)}
      />;

  return (
    <section
      className={`channel-view channel-mode-${section}${settingsOpen ? " has-agent-settings" : ""}${listCollapsed && section === "agents" ? " channel-list-collapsed" : ""}${resizingSplit ? " resizing-channel-split" : ""}`}
      aria-label={t("channels.title")}
      data-wuu-component="channel-view"
      data-wuu-variant={section}
      style={section === "agents" ? { gridTemplateColumns: `${listCollapsed ? CHANNEL_SPLIT_COLLAPSED_WIDTH : splitWidth}px minmax(0, 1fr)` } : undefined}
    >
      {section === "rooms" ? <div
        ref={conversationRef}
        data-inspector-overlay={inspectorOverlay || undefined}
        className={`channel-conversation${inspectedSession ? " has-session-inspector" : ""}${inspectorClosing ? " inspector-closing" : ""}`}
      >
        <div inert={Boolean(inspectedSession && inspectorOverlay)} className={`channel-room-main${composingNewRoom ? " composing-new-room" : ""}`}>
            <header className="titlebar channel-room-header" data-wuu-component="conversation-titlebar">
              {navigation}
              {composingNewRoom ? <>
                <ChannelRecipientPicker
                  agents={agents}
                  selectedAgentIDs={roomAgentIDs}
                  onCreateAgent={!newConversationGroup ? openAgentOnboarding : undefined}
                  onCreateGroup={!newConversationGroup ? () => setNewConversationGroup(true) : undefined}
                  onToggle={(agentID) => {
                    if (newConversationGroup) toggleRoomAgent(agentID);
                    else void openDirectConversation(agentID);
                  }}
                  maxSelected={MAX_ROOM_AGENTS}
                  onCancel={closeRoomPanel}
                  disabled={creatingRoom}
                />
                <button className="icon-button channel-new-room-close" type="button" aria-label={t("channels.cancelNewRoom")} disabled={creatingRoom} onClick={closeRoomPanel}><X aria-hidden="true" /></button>
              </> : <>
                <button ref={settingsTriggerRef} type="button" className="channel-room-header-title channel-room-settings-trigger" disabled={!selectedRoom || savingAgent} aria-expanded={settingsOpen} aria-controls={settingsOpen ? "channel-conversation-settings" : undefined} onClick={openConversationSettings}>
                  {selectedRoom ? <span className="channel-room-header-avatar">
                    {selectedRoom.kind === "dm" && selectedRoomAgents[0] ? <AgentAvatar
                      id={selectedRoomAgents[0].id} name={selectedRoomAgents[0].name} focusable={false}
                      avatarKey={selectedRoomAgents[0].avatar_key} avatarImage={selectedRoomAgents[0].avatar_image}
                      status={activityFor(selectedRoomAgents[0])} statusText={activityText(activityFor(selectedRoomAgents[0]))}
                      model={selectedRoomAgents[0].model_override || initialized?.model} modelLabel={t("channels.model")} />
                      : <ChannelGroupAvatar room={selectedRoom} agents={agents} />}
                  </span> : null}
                  <span className="channel-room-settings-name" role="heading" aria-level={2}>{selectedRoomTitle || t("channels.rooms")}</span><ChevronDown className="icon" aria-hidden="true" />
                </button>
                {selectedRoom ? <ChannelContinuity key={selectedRoom.id} roomId={selectedRoom.id} agents={selectedRoomAgents} /> : null}
              </>}
            </header>
          {composingNewRoom ? <div className="channel-new-room-surface">
            <div className="channel-new-room-canvas" />
            <div className="channel-conversation-footer">
              {newRoomError ? <div className="channel-send-error" role="alert">{newRoomError}</div> : null}
              <ChannelComposer
                draft={newRoomBody}
                placeholder={!newConversationGroup ? t("channels.chooseConversationFirst") : roomAgentIDs.length > 0 ? t("channels.firstGroupMessage") : t("channels.chooseRecipientsFirst")}
                compact
                disabled={roomAgentIDs.length === 0}
                sending={creatingRoom}
                files={newRoomFiles}
                images={newRoomImages}
                mentionAgents={newRoomSelectedAgents}
                onChangeDraft={setNewRoomBody}
                onPasteAttachmentFiles={(files) => void attachNewRoomFiles(files)}
                onRemoveFile={(id) => setNewRoomFiles((current) => current.filter((file) => file.id !== id))}
                onRemoveImage={(id) => setNewRoomImages((current) => current.filter((image) => image.id !== id))}
                onSend={(promptOverride) => void createRoomWithFirstMessage(promptOverride)}
              />
            </div>
          </div> : null}
          {!loading && rooms.length === 0 ? (
            <div className="channel-room-main-empty channel-start-empty">
              {agents.length === 0 ? <span className="channel-start-avatar" aria-hidden="true"><AgentAvatarMark seed="new-agent" avatarKey="abstract-3" /></span> : null}
              <button className="channel-empty-action" type="button" onClick={agents.length === 0 ? openAgentOnboarding : openNewRoom}>
                {t(agents.length === 0 ? "channels.newAgent" : "channels.newConversation")}
              </button>
            </div>
          ) : null}
          <input
            ref={roomAvatarInputRef}
            className="channel-avatar-file-input"
            type="file"
            accept="image/png,image/jpeg,image/webp"
            onChange={(event) => {
              const input = event.currentTarget;
              const file = input.files?.[0];
              if (!file) return;
              void updateRoomAvatarFromFile(file).finally(() => { input.value = ""; });
            }}
          />
          {loadError ? <div className="channel-error" role="alert">{loadError}</div> : null}
        <div ref={messageScroll.scrollRef} className="channel-message-stream" role="log" aria-live="polite">
          {channelTimeline.map((item, index) => {
            if (item.kind === "orchestration" && selectedRoom) {
              return (
                <ChannelOrchestrationCluster
                  key={`orchestration-${item.tasks[0].id}`}
                  room={selectedRoom}
                  tasks={item.tasks}
                  agents={agents}
                  onOpenSession={onOpenSession}
                />
              );
            }
            if (item.kind !== "message") return null;
            const message = item.message;
            const proposal = message.agent_creation_proposal;
            if (proposal) {
              const pending = proposal.state === "pending";
              const resolving = resolvingProposalID === proposal.id || proposal.state === "processing";
              const selectedModel = proposalModels[proposal.id] ?? "";
              return (
                <section className="channel-agent-proposal" data-state={proposal.state} key={message.id} aria-label={t("channels.agentProposalTitle")}>
                  <header className="channel-agent-proposal-header">
                    <div className="channel-agent-proposal-kicker">{t("channels.agentProposalTitle")}</div>
                    <span className="channel-agent-proposal-state" data-state={proposal.state}>
                      <i aria-hidden="true" />
                      {t(`channels.agentProposalState.${proposal.state}`)}
                    </span>
                  </header>
                  <div className="channel-agent-proposal-identity">
                    <strong>{proposal.name}</strong>
                    {proposal.role ? <p>{proposal.role}</p> : null}
                  </div>
                  {pending || resolving ? (
                    <div className="channel-agent-proposal-controls">
                      <label>
                        <span>{t("channels.model")}</span>
                        <select
                          value={selectedModel}
                          disabled={resolving}
                          onChange={(event) => setProposalModels((current) => ({ ...current, [proposal.id]: event.target.value }))}
                        >
                          <option value="">{t("channels.inheritModel")}{initialized ? ` · ${initialized.provider} / ${initialized.model}` : ""}</option>
                          {(initialized?.providers ?? []).map((provider) => (
                            <optgroup label={provider.name} key={provider.name}>
                              {(provider.models?.length ? provider.models : [{ id: provider.model, display_name: provider.model }])
                                .filter((model) => model.id)
                                .map((model) => <option value={`${provider.name}\u0000${model.id}`} key={`${provider.name}-${model.id}`}>{provider.name} · {model.display_name || model.id}</option>)}
                            </optgroup>
                          ))}
                        </select>
                      </label>
                      <div className="channel-agent-proposal-actions">
                        <button type="button" disabled={resolving} onClick={() => void resolveAgentCreation(proposal.id, true)}>
                          {resolving ? t("channels.agentProposalCreating") : t("channels.agentProposalApprove")}
                        </button>
                        <button type="button" className="secondary" disabled={resolving} onClick={() => void resolveAgentCreation(proposal.id, false)}>
                          {t("common.cancel")}
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className="channel-agent-proposal-status" data-state={proposal.state}>
                      {t(proposal.state === "approved" ? "channels.agentProposalApproved" : "channels.agentProposalCancelled", {
                        model: proposal.model ? `${proposal.provider} · ${proposal.model}` : t("channels.inheritModel"),
                      })}
                    </div>
                  )}
                </section>
              );
            }
            if (message.kind === "system") {
              return <div className="channel-system-message" key={message.id}>{message.body}</div>;
            }
            const own = message.author_type === "human";
            const author = own ? t("channels.you") : (agentNames.get(message.author_id) ?? message.author_id);
            const agent = own ? undefined : messageAgents.find((candidate) => candidate.id === message.author_id);
            const status = activityFor(agent);
            const reply = message.reply_to ? messages.find((candidate) => candidate.id === message.reply_to) : undefined;
            const previousItem = channelTimeline[index - 1];
            const previous = previousItem?.kind === "message" ? previousItem.message : previousItem?.tasks.at(-1);
            const date = new Date(message.created_at);
            const gap = previous ? date.getTime() - Date.parse(previous.created_at) : Infinity;
            const showTimestamp = !previous || gap < 0 || gap >= 5 * 60_000 || date.toDateString() !== new Date(previous.created_at).toDateString();
            const continued = !showTimestamp && previous?.kind === "text" && !previous.agent_creation_proposal
              && previous.author_type === message.author_type && previous.author_id === message.author_id
              && previous.source_session_ref === message.source_session_ref && previous.source_turn_id === message.source_turn_id;
            const direct = selectedRoom?.kind === "dm";
            const model = agent?.model_override || initialized?.model;
            const provider = agent?.model_override ? agent.provider_override || agent.engine_override : initialized?.provider;
            const modelInfo = initialized?.providers?.find((item) => item.name === provider)?.models?.find((item) => item.id === model);
            const effort = agent?.effort_override || (agent?.model_override
              ? modelInfo?.default_effort
              : initialized?.effort || modelInfo?.default_effort);
            const traceCard = !own && message.source_session_ref ? {
              id: agent?.id ?? message.author_id, name: author,
              avatarKey: agent?.avatar_key ?? "abstract-1", avatarImage: agent?.avatar_image,
              model, effort,
              onEdit: agent ? () => openConversationAgent(agent) : undefined,
              onInspect: () => inspectSession(message.source_session_ref!, message.source_turn_id, author),
            } : undefined;
            return (
              <Fragment key={message.id}>
              {showTimestamp ? <time className="channel-timestamp" dateTime={message.created_at}>
                {formatDate(message.created_at, date.toDateString() === new Date().toDateString()
                  ? { hour: "2-digit", minute: "2-digit" }
                  : { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
              </time> : null}
              <MessageBubbleRow
                outgoing={own}
                messageID={message.id}
                className={`channel-message ${own ? "own" : "agent"}${direct && !traceCard ? " channel-direct-message" : ""}${continued ? " channel-message-continuation" : ""}`}
                contentClassName="channel-message-content"
                avatar={!own && (!direct || traceCard) && !continued ? (traceCard ? <ChannelAgentHoverCard {...traceCard} /> :
                  <AgentAvatar id={agent?.id ?? message.author_id} name={author} avatarKey={agent?.avatar_key ?? "abstract-1"} avatarImage={agent?.avatar_image} status={status} statusText={activityText(status)} model={agent?.model_override || initialized?.model} modelLabel={t("channels.model")} />
                ) : undefined}
                meta={!own && !direct && !continued ? (
                  <div className="channel-message-meta">
                    {!own ? (
                      <ChannelAuthorName name={author} mentionLabel={t("channels.mentionAgent", { name: author })} onMention={() => roomComposerRef.current?.insertMention(author, agent?.id ?? message.author_id)} />
                    ) : null}
                  </div>
                ) : undefined}
              >
                <ChannelMessageBubble
                  message={message}
                  outgoing={own}
                  allowCollapse={own}
                  onExpand={messageScroll.pauseAutoFollow}
                  attachmentIDPrefix="main"
                  beforeBody={reply ? <blockquote className="channel-message-reply"><strong>{reply.author_type === "human" ? t("channels.you") : (agentNames.get(reply.author_id) ?? reply.author_id)}</strong><span>{reply.body || reply.task_title}</span></blockquote> : undefined}
                />
              </MessageBubbleRow>
              </Fragment>
            );
          })}
          {pendingMessage?.room_id === selectedRoomID ? (
            <MessageBubbleRow outgoing messageID={pendingMessage.id} className="channel-message own channel-message-pending" contentClassName="channel-message-content">
              <ChannelMessageBubble message={pendingMessage} outgoing allowCollapse={false} attachmentIDPrefix="pending" />
              <span className="channel-send-status" role="status">{t("channels.messageSending")}</span>
            </MessageBubbleRow>
          ) : null}
          {!loading && loadedRoomIDs.has(selectedRoomID) && selectedRoom && channelTimeline.length === 0 && responses.length === 0 && !pendingMessage ? (
            <div className="channel-onboarding">
              <div className="channel-onboarding-members" aria-hidden="true">{selectedRoomAgents.map((agent) => <AgentAvatarMark key={agent.id} seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />)}</div>
              <p>{t("channels.empty")}</p>
            </div>
          ) : null}
        </div>
        {!(inspectedSession && inspectorOverlay) ? <JumpToLatestPill
          containerRef={messageScroll.scrollRef}
          bottomAnchor={composerFooterNode}
          threshold={AUTO_FOLLOW_BOTTOM_THRESHOLD_PX}
        /> : null}
        {selectedRoom ? (
          <div ref={setComposerFooterNode} className="channel-conversation-footer">
            <div className="channel-activity-region channel-activity-motion" aria-live="polite">
              <ChannelCoordinatorActivity key={selectedRoomID} status={coordinatorsByRoomID[selectedRoomID]} agents={agents} activeAgentIDs={responseActivities.map(response => response.agent_id)}
                onInspectAgent={inspectAgentActivity}
                onInspectCoordinator={ref => inspectSession(ref, undefined, t("channels.sessions.coordination"))}
                onRetry={async (sessionRef) => { await window.wuu!.resumeChannelSession({ sessionRef }); await refreshMessages(selectedRoomID, true); }} />
              <ChannelActivityPresence key={`members:${selectedRoomID}`}>
              {responseActivities.filter((response, index, items) => items.findIndex(item => item.agent_id === response.agent_id) === index).map((response) => <ChannelAgentActivity key={response.id}
                agent={agents.find((agent) => agent.id === response.agent_id)} agentID={response.agent_id}
                state={response.state} error={response.error}
                selected={!inspectorClosing && inspectedSession?.agentID === response.agent_id}
                onInspect={() => inspectAgentActivity(response.agent_id, response.session_ref)}
                onResume={async () => { await window.wuu!.resumeChannelSession({ sessionRef: response.session_ref }); await refreshMessages(response.room_id, true); }}
              />)}
              {responsesByRoomID[selectedRoomID] === undefined ? respondingAgents.map(({ agent }) => (
                <ChannelAgentActivity key={agent.id} agent={agent} agentID={agent.id} state="thinking" onInspect={() => inspectAgentActivity(agent.id)} />
              )) : null}
              </ChannelActivityPresence>
            </div>
            {sendError?.roomID === selectedRoomID ? <div className="channel-send-error" role="alert"><span>{t("composer.sendFailed")} · {sendError.message}</span><button type="button" disabled={sending} onClick={() => void sendMessage()}>{t("channels.sessions.retry")}</button></div> : null}
            <ChannelComposer
              ref={roomComposerRef}
              draft={body}
              draftRevision={draftRevision}
              placeholder={t("channels.messageTo", { name: selectedRoomTitle })}
              compact
              disabled={false}
              sending={sending}
              files={composerFiles}
              images={composerImages}
              mentionAgents={selectedRoomAgents}
              queryHistorySessionID={selectedRoomID}
              onChangeDraft={setBody}
              onPasteAttachmentFiles={(files) => void attachMessageFiles(files)}
              onRemoveFile={(id) => setComposerFiles((current) => current.filter((file) => file.id !== id))}
              onRemoveImage={(id) => setComposerImages((current) => current.filter((image) => image.id !== id))}
              onSend={(promptOverride) => void sendMessage(promptOverride)}
            />
          </div>
        ) : null}
        </div>
        {inspectedSession?.agentID ? <ChannelActivityInspector key={`${inspectedSession.roomID}:${inspectedSession.agentID}`}
          roomID={inspectedSession.roomID} agentID={inspectedSession.agentID} name={inspectedSession.name}
          fallbackSessionRef={inspectedSession.sessionRef} overlay={inspectorOverlay} closing={inspectorClosing} onClose={closeInspector}
        /> : inspectedSession?.sessionRef ? <ChannelSessionInspector
          key={`${inspectedSession.sessionRef}:${inspectedSession.turnID ?? "latest"}`}
          sessionRef={inspectedSession.sessionRef}
          name={inspectedSession.name}
          turnID={inspectedSession.turnID}
          overlay={inspectorOverlay}
          closing={inspectorClosing}
          onClose={closeInspector}
        /> : null}
      </div> : section === "agents" ? (
        <div className="channel-agent-workspace">
          <aside className={`channel-list-pane channel-agent-directory${listCollapsed ? " collapsed" : ""}`}>
            <div className="channel-pane-heading">
              {!listCollapsed ? <span>{t("channels.agents")}<small className="channel-pane-count">{agents.length}</small></span> : null}
              <div className="channel-heading-actions">
                <button className="icon-button channel-list-collapse-toggle" type="button" aria-label={t(listCollapsed ? "channels.expandList" : "channels.collapseList")} aria-expanded={!listCollapsed} onClick={toggleListCollapsed}>
                  {listCollapsed ? <PanelLeftOpen className="icon" /> : <PanelLeftClose className="icon" />}
                </button>
                {!listCollapsed ? <button
                  className="icon-button"
                  type="button"
                  aria-label={t("channels.newAgent")}
                  onClick={openAgentOnboarding}
                >
                  <Plus className="icon" />
                </button> : null}
              </div>
            </div>
            {!listCollapsed && loadError ? <div className="channel-error" role="alert">{loadError}</div> : null}
            {!listCollapsed ? <div className="channel-agent-directory-list channel-directory-list">
            <button
              className={`channel-agent-graph-entry${selectedAgentID ? "" : " selected"}`}
              type="button"
              aria-current={selectedAgentID ? undefined : "page"}
              onClick={() => {
                if (selectedAgentID) void saveAgentDetails(selectedAgentID, agentDetailDraftRef.current);
                setSelectedAgentID("");
              }}
            >
              <Network className="icon" />
              <span>{t("channels.relationshipGraph")}</span>
            </button>
            {agents.map((agent) => {
              const status = activityFor(agent);
              const roomCount = rooms.filter((room) => room.members.some((member) => member.member_type === "agent" && member.member_id === agent.id)).length;
              const model = agent.model_override || t("channels.inheritModel");
              return (
                <div className={`channel-directory-row channel-agent-directory-row${selectedAgentID === agent.id ? " selected" : ""}`} key={agent.id}>
                  <button className="channel-directory-avatar" type="button" aria-label={t("channels.viewAgent", { name: agent.name })} onClick={() => selectAgentDetails(agent)}>
                    <AgentAvatar id={agent.id} name={agent.name} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={status} statusText={activityText(status)} model={agent.model_override || initialized?.model} modelLabel={t("channels.model")} expressive />
                  </button>
                  <button className="channel-directory-identity channel-agent-directory-identity" type="button" aria-current={selectedAgentID === agent.id ? "page" : undefined} onClick={() => selectAgentDetails(agent)}>
                    <span><strong>{agent.name}</strong><small>{agent.role || model} · {t("channels.agentRoomCount", { count: roomCount })}</small></span>
                  </button>
                </div>
              );
            })}
            </div> : null}
            {!listCollapsed ? <button
              className="channel-split-resizer"
              type="button"
              role="separator"
              aria-label={t("channels.resizeList")}
              aria-orientation="vertical"
              aria-valuemin={CHANNEL_SPLIT_MIN_WIDTH}
              aria-valuemax={CHANNEL_SPLIT_MAX_WIDTH}
              aria-valuenow={splitWidth}
              onPointerDown={startSplitResize}
              onKeyDown={handleSplitResizeKeyDown}
            /> : null}
          </aside>
          <div className="channel-agent-graph-pane">
            {selectedAgent ? (
              <article className="channel-agent-detail">
                <header className="channel-agent-detail-header">
                  <button className="channel-agent-detail-avatar" type="button" aria-label={t("channels.customAvatar")} onClick={() => agentDetailAvatarInputRef.current?.click()}>
                    <AgentAvatarMark seed={selectedAgent.id} avatarKey={agentAvatarKey} avatarImage={agentAvatarImage} status={activityFor(selectedAgent)} />
                    <span aria-hidden="true"><ImagePlus className="icon" /></span>
                  </button>
                  <input
                    ref={agentDetailAvatarInputRef}
                    className="channel-avatar-file-input"
                    type="file"
                    accept="image/png,image/jpeg,image/webp"
                    onChange={(event) => {
                      const input = event.currentTarget;
                      const file = input.files?.[0];
                      if (!file) return;
                      setAgentAvatarError("");
                      void squareAvatarImageFromFile(file)
                        .then(setAgentAvatarImage)
                        .catch(() => setAgentAvatarError(t("channels.invalidAvatarImage")))
                        .finally(() => { input.value = ""; });
                    }}
                  />
                  <div>
                    <span>{t("channels.agentDetails")}</span>
                    <h2>{selectedAgent.name}</h2>
                    <p>{activityText(activityFor(selectedAgent))}</p>
                  </div>
                  <div className="channel-agent-detail-actions">
                    <ChannelSessions key={selectedAgent.id} agents={[selectedAgent]} rooms={rooms} agentId={selectedAgent.id} initialized={initialized} onOpenRoom={openSessionRoom} />
                    <button type="button" disabled={Boolean(resettingAgentID || savingAgentID)} onClick={() => void resetAgent(selectedAgent.id)}>
                      {t(resettingAgentID === selectedAgent.id ? "channels.resettingAgent" : "channels.resetAgent")}
                    </button>
                  </div>
                </header>
                {agentAvatarError ? <div className="channel-agent-detail-notice channel-error" role="alert">{agentAvatarError}</div> : null}
                {agentResetStatus ? <div className="channel-agent-detail-notice channel-agent-reset-status" role="status">{agentResetStatus}</div> : null}
                <div className="channel-agent-detail-grid">
                  <div className="channel-agent-detail-main">
                    <section>
                      <h3>{t("channels.agentRuntime")}</h3>
                      <div className="channel-agent-detail-form">
                        <label className="channel-form-field">
                          <span>{t("channels.name")}</span>
                          <input value={agentName} onChange={(event) => setAgentName(event.currentTarget.value)} />
                        </label>
                        <label className="channel-form-field">
                          <span>{t("channels.agentRole")}</span>
                          <textarea value={agentRole} onChange={(event) => setAgentRole(event.currentTarget.value)} maxLength={280} placeholder={t("channels.agentRolePlaceholder")} />
                        </label>
                        <AgentAvatarCreator
                          seed={selectedAgent.id}
                          avatarKey={agentAvatarKey}
                          avatarImage={agentAvatarImage}
                          onChange={(nextAvatarKey) => {
                            setAgentAvatarKey(nextAvatarKey);
                            setAgentAvatarImage("");
                            setAgentAvatarError("");
                          }}
                        />
                        {agentEngine !== "wuu" ? <p className="channel-error">{t("channels.sessions.byokRequired")}</p> : null}
                        <label className="channel-form-field">
                          <span>{t("channels.model")}</span>
                          <SelectMenu value={agentModel} onChange={selectAgentModel} groups={modelGroups} ariaLabel={t("channels.model")} />
                        </label>
                        {agentModel && agentEffortOptions.length > 1 ? (
                          <div className="channel-form-field">
                            <span id="channel-agent-detail-effort-label">{t("channels.effort")}</span>
                            <div className="channel-effort-picker" role="radiogroup" aria-labelledby="channel-agent-detail-effort-label">
                              {agentEffortOptions.map((effort) => (
                                <button className="channel-effort-chip" type="button" role="radio" key={effort} aria-checked={agentEffort === effort} aria-pressed={agentEffort === effort} onClick={() => setAgentEffort(effort)}>
                                  {effortLabel(effort)}
                                </button>
                              ))}
                            </div>
                          </div>
                        ) : null}
                      </div>
                    </section>
                  </div>
                  <div className="channel-agent-detail-side">
                    <section>
                      <h3>{t("channels.agentChannels")}</h3>
                      {selectedAgentRooms.length ? (
                        <div className="channel-agent-detail-rooms">
                          {selectedAgentRooms.map((room) => <span key={room.id}># {room.name}</span>)}
                        </div>
                      ) : <p className="channel-agent-detail-empty">{t("channels.agentNoChannels")}</p>}
                    </section>
                    <section>
                      <h3>{t("channels.agentInfo")}</h3>
                      <dl>
                        <div><dt>{t("channels.agentAutostart")}</dt><dd>{selectedAgent.autostart ? t("channels.enabled") : t("channels.disabled")}</dd></div>
                        <div>
                          <dt>{t("channels.agentCapacity")}</dt>
                          <dd>{t("channels.agentCapacitySummary", {
                            active: (selectedAgent.session_capacity?.active ?? 0) + (selectedAgent.session_capacity?.starting ?? 0),
                            starting: selectedAgent.session_capacity?.starting ?? 0,
                            limit: selectedAgent.session_capacity?.limit ?? 5,
                            queued: selectedAgent.session_capacity?.queued ?? 0,
                            idle: selectedAgent.session_capacity?.idle ?? 0,
                          })}</dd>
                        </div>
                        <div>
                          <dt>{t("channels.agentMemoryDirectory")}</dt>
                          <dd>
                            <button
                              className="channel-agent-memory-link"
                              type="button"
                              title={selectedAgent.memory_dir}
                              disabled={!onOpenMemoryDirectory && !hostSupports("revealWorkspaceItem")}
                              onClick={() => {
                                if (onOpenMemoryDirectory) {
                                  onOpenMemoryDirectory(selectedAgent.memory_dir);
                                  return;
                                }
                                void window.wuu?.revealWorkspaceItem(selectedAgent.memory_dir);
                              }}
                            >
                              <code>./memory</code>
                            </button>
                          </dd>
                        </div>
                        <div><dt>{t("channels.agentCreatedAt")}</dt><dd>{formatDate(selectedAgent.created_at)}</dd></div>
                      </dl>
                    </section>
                  </div>
                </div>
              </article>
            ) : !loading && agents.length === 0 ? (
              <div className="channel-start-empty">
                <span className="channel-start-avatar" aria-hidden="true"><AgentAvatarMark seed="new-agent" avatarKey="abstract-3" /></span>
                <button className="channel-empty-action" type="button" onClick={openAgentOnboarding}>{t("channels.newAgent")}</button>
              </div>
            ) : (
              <AgentRelationshipGraph
                agents={agents}
                rooms={rooms}
                insights={agentInsights}
                inheritedProvider={initialized?.provider}
                inheritedModel={initialized?.model}
                onSelectAgent={selectAgentDetails}
                ariaLabel={t("channels.relationshipGraph")}
                zoomInLabel={t("channels.zoomIn")}
                zoomOutLabel={t("channels.zoomOut")}
                resetViewLabel={t("channels.resetGraphView")}
              />
            )}
          </div>
        </div>
      ) : (
        <div className="channel-management-view channel-tasks-view">
          <div className="channel-management-heading">
            <div>
              <strong>{t("channels.tasks")}</strong>
              <span>{trackedTasks.length}</span>
            </div>
            <button
              className="channel-management-primary"
              type="button"
              disabled={!selectedRoom || selectedRoomAgents.length === 0}
              onClick={() => {
                setTaskRoomID(selectedRoomID || rooms[0]?.id || "");
                setTaskOwnerID(selectedRoomAgents[0]?.id ?? "");
                setSetupPanel("task");
              }}
            >
              <Plus className="icon" />
              {t("channels.newTask")}
            </button>
          </div>
          {loadError ? <div className="channel-error" role="alert">{loadError}</div> : null}
          <div className="channel-task-board" aria-label={t("channels.taskBoard.label")}>
            {taskBoardColumns.map((column) => {
              const tasks = trackedTasks.filter((task) => taskBoardColumn(task) === column);
              return (
                <section className={`channel-task-column channel-task-column-${column}`} key={column} data-column={column}>
                  <header className="channel-task-column-heading">
                    <strong>{t(taskBoardColumnKey(column))}</strong>
                    <span>{tasks.length}</span>
                  </header>
                  <div className="channel-task-column-items">
                    {tasks.map((task) => {
                      const room = rooms.find((candidate) => candidate.id === task.room_id);
                      const owner = agentNames.get(task.task_owner ?? "") ?? task.task_owner ?? "";
                      return (
                        <button
                          className={`channel-task-card${column === "done" ? " done" : ""}`}
                          type="button"
                          key={task.id}
                          data-tooltip={`${room ? `# ${room.name}` : task.room_id} · ${owner}`}
                          onClick={() => {
                            setSelectedRoomID(task.room_id);
                            onSectionChange?.("rooms");
                          }}
                        >
                          <strong>{task.task_title?.trim() || task.body}</strong>
                          <span className="channel-task-card-meta">{room ? `# ${room.name}` : task.room_id} · {owner}</span>
                        </button>
                      );
                    })}
                    {tasks.length === 0 ? <span className="channel-task-column-empty">—</span> : null}
                  </div>
                </section>
              );
            })}
          </div>
        </div>
      )}


      {settingsOpen && showSettingsMembers ? <aside id="channel-conversation-settings" className="channel-settings-panel" aria-label={t("channels.memberCount", { count: selectedRoomAgents.length })}>
        <header className="channel-settings-header"><h2>{t("channels.memberCount", { count: selectedRoomAgents.length })}</h2><button className="icon-button" type="button" aria-label={t("common.close")} onClick={closeAgentPanel}><X /></button></header>
        <div className="channel-settings-members">
          {selectedRoomAgents.map((agent) => <button className="channel-settings-member" type="button" key={agent.id} onClick={() => openConversationAgent(agent)}>
            <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />
            <span><strong>{agent.name}</strong>{agent.role ? <span>{agent.role}</span> : null}</span><Settings2 className="icon" aria-hidden="true" />
          </button>)}
          {selectedRoom ? <button className="channel-settings-manage" type="button" onClick={() => { closeAgentPanel(); editRoom(selectedRoom); }}>{t("channels.manageRoom", { name: selectedRoom.name })}</button> : null}
        </div>
      </aside> : null}
      <ChannelAgentSettings
        inline={settingsOpen}
        busy={savingAgent}
        error={agentSaveError}
        onBack={settingsOpen && selectedRoom?.kind === "channel" ? () => { setSetupPanel(null); setShowSettingsMembers(true); } : undefined}
        open={setupPanel === "agent" && Boolean(editingAgentID)}
        title={agentName}
        onTitleChange={setAgentName}
        onSubmit={() => void submitAgent()}
        onClose={closeAgentPanel}
        dialogTitle={t("channels.editAgent")}
        dialogTitleId="channel-agent-dialog-title"
        dialogClassName="channel-agent-editor-dialog"
        fieldLabel={t("channels.name")}
        fieldAriaLabel={t("channels.name")}
        placeholder="Andy"
        icon={Bot}
        submitLabel={t("channels.save")}
        cancelLabel={t("channels.cancel")}
        submitDisabled={!agentName.trim() || Boolean(resettingAgentID) || agentEngine !== "wuu"}
        content={<div className="channel-agent-editor-body">
          {agentResetStatus ? <div className="channel-agent-reset-status" role="status">{agentResetStatus}</div> : null}
          <div className="channel-agent-editor-identity">
            <button className="channel-identity-avatar-button" type="button"
              aria-label={t("channels.editAppearance")} title={t("channels.editAppearance")}
              aria-expanded={agentAppearanceOpen} aria-controls="channel-agent-appearance"
              aria-invalid={Boolean(agentAvatarError)} aria-describedby={agentAvatarError ? "channel-agent-avatar-error" : undefined}
              onClick={() => setAgentAppearanceOpen((open) => !open)}>
              <AgentAvatarMark seed={editingAgentID} avatarKey={agentAvatarKey} avatarImage={agentAvatarImage} />
              <span className="channel-identity-avatar-badge" aria-hidden="true"><Settings2 className="icon" /></span>
            </button>
            <label className="channel-agent-editor-field channel-agent-editor-name">
              <span>{t("channels.name")}</span>
              <input value={agentName} onChange={(event) => setAgentName(event.currentTarget.value)} autoFocus autoComplete="off" />
            </label>
          </div>
          <FieldError id="channel-agent-avatar-error">{agentAvatarError}</FieldError>
          {agentAppearanceOpen ? <div className="channel-agent-editor-appearance" id="channel-agent-appearance">
            <AgentAvatarCreator seed={editingAgentID} avatarKey={agentAvatarKey} avatarImage={agentAvatarImage}
              onChange={(nextAvatarKey) => {
                setAgentAvatarKey(nextAvatarKey);
                setAgentAvatarImage("");
                setAgentAvatarError("");
              }} />
            <button className="channel-agent-editor-upload" type="button" aria-label={t("channels.customAvatar")}
              onClick={() => agentAvatarInputRef.current?.click()}>
              <ImagePlus className="icon" />{t("participant.avatar.upload")}
            </button>
          </div> : null}
          <input ref={agentAvatarInputRef} className="channel-avatar-file-input" type="file" accept="image/png,image/jpeg,image/webp"
            onChange={(event) => {
              const input = event.currentTarget;
              const file = input.files?.[0];
              if (!file) return;
              setAgentAvatarError("");
              const generation = agentEditorGeneration.current;
              void squareAvatarImageFromFile(file)
                .then((image) => { if (generation === agentEditorGeneration.current) setAgentAvatarImage(image); })
                .catch(() => { if (generation === agentEditorGeneration.current) setAgentAvatarError(t("channels.invalidAvatarImage")); })
                .finally(() => { input.value = ""; });
            }} />
          <label className="channel-agent-editor-field">
            <span>{t("channels.agentRole")}</span>
            <textarea value={agentRole} onChange={(event) => setAgentRole(event.currentTarget.value)} maxLength={280} rows={2} />
          </label>
          <div className="channel-agent-editor-runtime">
            {agentEngine !== "wuu" ? <p className="channel-error">{t("channels.sessions.byokRequired")}</p> : null}
            <div className="channel-agent-editor-setting">
              <span>{t("channels.model")}</span>
              <SelectMenu value={agentModel} onChange={selectAgentModel} groups={modelGroups} ariaLabel={t("channels.model")} flip />
            </div>
            {agentModel && agentEffortOptions.length > 1 ? <div className="channel-agent-editor-setting">
              <span>{t("channels.effort")}</span>
              <SelectMenu value={agentEffort} onChange={setAgentEffort}
                options={agentEffortOptions.map((effort) => ({ value: effort, label: effortLabel(effort) }))}
                ariaLabel={t("channels.effort")} flip />
            </div> : null}
          </div>
          <details className="channel-agent-editor-more">
            <summary>{t("channels.moreAgentActions")}<ChevronDown className="icon" aria-hidden="true" /></summary>
            <div className="channel-agent-editor-maintenance">
              <button type="button" disabled={Boolean(resettingAgentID)} onClick={() => void resetAgent(editingAgentID)}>
                {t(resettingAgentID === editingAgentID ? "channels.resettingAgent" : "channels.resetAgent")}
              </button>
              <button className="danger" type="button" disabled={Boolean(resettingAgentID)} onClick={() => void deleteAgent(editingAgentID)}>
                {t("channels.deleteAgent")}
              </button>
            </div>
          </details>
        </div>}
      />
      <SidebarNameDialog
        open={setupPanel === "room" && Boolean(editingRoomID)}
        title={roomName}
        onTitleChange={setRoomName}
        onSubmit={() => void submitRoom()}
        onClose={closeRoomPanel}
        dialogTitle={t(editingRoomID ? "channels.roomDetails" : "channels.newRoom")}
        dialogTitleId="channel-room-dialog-title"
        fieldLabel={t("channels.name")}
        fieldAriaLabel={t("channels.name")}
        placeholder={t("channels.newRoom")}
        icon={MessageCircle}
        submitLabel={t(editingRoomID ? "channels.save" : "channels.create")}
        cancelLabel={t("channels.cancel")}
        submitDisabled={!roomName.trim()}
        variant={editingRoomID ? "drawer" : "default"}
        hideActions={Boolean(editingRoomID)}
        closeOnEscape={!roomMemberMode}
        backgrounded={Boolean(roomMemberMode)}
        content={editingRoomID ? (
          <div className="channel-room-details-form">
            {selectedRoom ? (
              <section className="channel-room-identity" aria-label={t("channels.groupOverview")}>
                <span className="channel-room-identity-avatar">
                  <ChannelGroupAvatar room={selectedRoom} agents={agents} />
                </span>
                <span className="channel-room-identity-copy">
                  <strong>{roomName}</strong>
                  <span>{t("channels.agentCount", { count: roomAgentIDs.length })}</span>
                </span>
              </section>
            ) : null}
            <section className="channel-room-members-section" aria-labelledby="channel-room-members-title">
              <header className="channel-room-members-header">
                <div>
                  <h3 id="channel-room-members-title">{t("channels.groupMembers")}</h3>
                  <span>{t("channels.memberCount", { count: roomAgentIDs.length + (roomIncludesCurrentUser ? 1 : 0) })}</span>
                </div>
              </header>
              <div className="channel-room-member-list">
                {roomIncludesCurrentUser ? (
                  <div className="channel-room-member-row current" aria-label={t("channels.you")}>
                    <span className="channel-room-member-avatar">
                      <HumanAvatarMark />
                    </span>
                    <span className="channel-room-member-identity">
                      <strong>{t("channels.you")}</strong>
                    </span>
                  </div>
                ) : null}
                {agents.filter((agent) => roomAgentIDs.includes(agent.id)).map((agent) => (
                  <div className="channel-room-member-row" key={agent.id}>
                    <span className="channel-room-member-avatar">
                      <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} status={activityFor(agent)} />
                    </span>
                    <span className="channel-room-member-identity">
                      <strong>{agent.name}</strong>
                    </span>
                    <button
                      className="channel-room-member-remove"
                      type="button"
                      aria-label={t("channels.removeMember", { name: agent.name })}
                      disabled={updatingRoomMembers}
                      onClick={() => void removeRoomMember(agent.id)}
                    >
                      <X className="icon" aria-hidden="true" />
                    </button>
                  </div>
                ))}
                <button
                  className="channel-room-member-add"
                  type="button"
                  onClick={() => openRoomMemberMode("add")}
                  disabled={roomAgentIDs.length >= MAX_ROOM_AGENTS || agents.every((agent) => roomAgentIDs.includes(agent.id))}
                >
                  <Plus className="icon" aria-hidden="true" />
                  <span>{t("channels.addMember")}</span>
                </button>
              </div>
            </section>
            <section className="channel-room-settings-section" aria-labelledby="channel-room-settings-title">
              <h3 id="channel-room-settings-title">{t("channels.groupSettings")}</h3>
              <label className="sidebar-name-dialog-field">
                <span className="sidebar-name-dialog-label">{t("channels.name")}</span>
                <input
                  className="sidebar-name-dialog-input"
                  value={roomName}
                  onChange={(event) => setRoomName(event.currentTarget.value)}
                  onBlur={() => {
                    if (!roomName.trim()) {
                      setRoomName(savedRoomNameRef.current);
                      return;
                    }
                    void persistRoomName();
                  }}
                />
              </label>
            </section>
            <section className="channel-room-danger-zone" aria-labelledby="channel-room-danger-title">
              <div>
                <h3 id="channel-room-danger-title">{t("channels.dangerZone")}</h3>
                <p>{t("channels.deleteRoomHint")}</p>
              </div>
              <button type="button" onClick={() => void deleteRoom()}>{t("channels.deleteRoom")}</button>
            </section>
          </div>
        ) : (
          <div className="channel-setup-form">
            <div className="channel-identity-row">
              <button
                className="channel-identity-avatar-button"
                type="button"
                aria-label={t("channels.customGroupAvatar")}
                onClick={() => chooseRoomAvatar("")}
              >
                <ChannelGroupAvatar
                  room={{
                    id: editingRoomID || "new-room",
                    kind: "channel",
                    name: roomName,
                    avatar_image: roomAvatarImage || undefined,
                    created_by: "local-user",
                    created_at: "",
                    members: [
                      { room_id: editingRoomID || "new-room", member_type: "human", member_id: "local-user", joined_at: "" },
                      ...roomAgentIDs.map((agentID) => ({ room_id: editingRoomID || "new-room", member_type: "agent" as const, member_id: agentID, joined_at: "" })),
                    ],
                  }}
                  agents={agents}
                />
                <span className="channel-identity-avatar-badge" aria-hidden="true"><ImagePlus className="icon" /></span>
              </button>
              <label className="channel-form-field">
                <span>{t("channels.name")}</span>
                <input value={roomName} onChange={(event) => setRoomName(event.currentTarget.value)} autoFocus placeholder={t("channels.newRoom")} />
              </label>
            </div>
            <ChannelMemberPicker agents={agents} selectedAgentIDs={roomAgentIDs} onToggle={toggleRoomAgent} maxSelected={MAX_ROOM_AGENTS} />
          </div>
        )}
      />
      <SidebarNameDialog
        open={Boolean(editingRoomID && roomMemberMode)}
        title=""
        onTitleChange={() => undefined}
        onSubmit={() => void submitRoomMemberChange()}
        onClose={closeRoomMemberMode}
        dialogTitle={t("channels.addAgents")}
        dialogTitleId="channel-room-member-dialog-title"
        fieldLabel={t("channels.groupMembers")}
        fieldAriaLabel={t("channels.groupMembers")}
        placeholder=""
        icon={Plus}
        submitLabel={t("channels.addSelectedMembers", { count: roomMemberSelectionIDs.length })}
        cancelLabel={t("channels.cancel")}
        submitDisabled={roomMemberSelectionIDs.length === 0 || updatingRoomMembers}
        dialogClassName="channel-room-member-dialog"
        content={roomMemberMode ? (
          <div className="channel-room-member-flow">
            <p>{t("channels.addAgentsHint")}</p>
            <ChannelMemberPicker
              agents={agents.filter((agent) => !roomAgentIDs.includes(agent.id))}
              selectedAgentIDs={roomMemberSelectionIDs}
              onToggle={toggleRoomMemberSelection}
              label={t("channels.availableAgents")}
              maxSelected={Math.max(0, MAX_ROOM_AGENTS - roomAgentIDs.length)}
            />
          </div>
        ) : null}
      />
      <SidebarNameDialog
        open={setupPanel === "task"}
        title={taskTitle}
        onTitleChange={setTaskTitle}
        onSubmit={() => void submitTask()}
        onClose={() => { setSetupPanel(null); setTaskRoomID(""); }}
        dialogTitle={t("channels.newTask")}
        dialogTitleId="channel-task-dialog-title"
        fieldLabel={t("channels.taskTitle")}
        fieldAriaLabel={t("channels.taskTitle")}
        placeholder={t("channels.taskTitle")}
        icon={ClipboardList}
        submitLabel={t("channels.create")}
        cancelLabel={t("channels.cancel")}
        submitDisabled={!taskTitle.trim() || !(taskRoomID || selectedRoomID) || !taskOwnerID}
        content={<div className="channel-setup-form">
          <label className="sidebar-name-dialog-field"><span className="sidebar-name-dialog-label">{t("channels.taskTitle")}</span><input className="sidebar-name-dialog-input" value={taskTitle} onChange={(event) => setTaskTitle(event.currentTarget.value)} autoFocus /></label>
          <label className="sidebar-name-dialog-field"><span className="sidebar-name-dialog-label">{t("channels.taskRoomLabel")}</span><SelectMenu value={taskRoomID || selectedRoomID} onChange={setTaskRoomID} options={rooms.map((room) => ({ value: room.id, label: `# ${room.name}` }))} ariaLabel={t("channels.taskRoomLabel")} flip /></label>
          <label className="sidebar-name-dialog-field"><span className="sidebar-name-dialog-label">{t("channels.taskOwnerLabel")}</span><SelectMenu value={taskOwnerID} onChange={setTaskOwnerID} options={taskOwnerAgents.map((agent) => ({ value: agent.id, label: agent.name }))} ariaLabel={t("channels.taskOwnerLabel")} flip /></label>
        </div>}
      />
    </section>
  );
}
