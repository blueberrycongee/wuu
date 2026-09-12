import type {
  Agent,
  AppServerNotification,
  ChannelRoom,
  ConfigChangedNotification,
  DesktopProject,
  ExtensionInventoryRecord,
  GitStatusResult,
  InitializeResult,
  TodoUpdate,
  PluginInventoryChangedNotification,
  RuntimeContext,
  ServerEvent,
  Thread,
  ThreadItem,
  Turn,
} from "../shared/protocol";
import {
  OPTIMISTIC_TURN_ID_PREFIX,
  type ComposerFile,
  type ComposerImage,
} from "./ComposerMessages";
import { parseRequestedHandoffIntent } from "./HandoffDraft";
import { isInternalUserNotificationItem } from "./InternalUserNotification";
import { threadDisplayTitle } from "./ThreadTitles";
import { sortChildAgents } from "./ThreadAgents";
import {
  mergeTurnItemsInOrder,
  orderedTurnItems,
  upsertTurnItemInOrder,
} from "./TurnOrdering";
import {
  isRecord,
  type JsonRecord,
  numberValue,
  recordValue,
  stringValue,
} from "./ToolActivity";
import {
  streamTextKey,
  streamTextStore,
  type StreamTextField,
} from "./StreamText";
import { estimateStreamingOutputTokens } from "./TurnTelemetryStore";
import { statusMessageForError } from "./UserFacingErrors";
import {
  formatCurrentDate,
  formatCurrentNumber,
  localizedText,
  translateCurrent as t,
} from "./i18n";

type ConversationPaneID = "primary" | "secondary";

type ComposerDraftState = {
  prompt: string;
  images: ComposerImage[];
  files: ComposerFile[];
};

export type TurnStreamStatus = {
  text: string;
  liveProgress: boolean;
};

function emptyComposerDraft(): ComposerDraftState {
  return { prompt: "", images: [], files: [] };
}

function initialSplitComposerDrafts(): Record<
  ConversationPaneID,
  ComposerDraftState
> {
  return {
    primary: emptyComposerDraft(),
    secondary: emptyComposerDraft(),
  };
}

function cloneComposerDraft(draft: ComposerDraftState): ComposerDraftState {
  return {
    prompt: draft.prompt,
    images: draft.images.map((image) => ({ ...image })),
    files: draft.files.map((file) => ({ ...file })),
  };
}

type SessionTab =
  | {
      id: string;
      kind: "draft";
      context: RuntimeContext;
      title: string;
      prompt: string;
      images: ComposerImage[];
      files: ComposerFile[];
      createdAt: number;
    }
  | {
      id: string;
      kind: "thread";
      context: RuntimeContext;
      threadID: string;
      title: string;
      prompt: string;
      images: ComposerImage[];
      files: ComposerFile[];
    }
  | {
      id: string;
      kind: "skills";
      context: RuntimeContext;
      title: string;
    }
  | {
      id: string;
      kind: "channel-room";
      context: RuntimeContext;
      roomID: string;
      title: string;
      prompt: string;
      images: ComposerImage[];
      files: ComposerFile[];
    }
  | {
      id: string;
      kind: "agents";
      context: RuntimeContext;
      title: string;
    }
  | {
      id: string;
      kind: "tasks";
      context: RuntimeContext;
      title: string;
    };

type AppState = {
  initialized?: InitializeResult;
  projects: DesktopProject[];
  activeContext?: RuntimeContext;
  activeProjectId?: string;
  gitStatus?: GitStatusResult;
  thread?: Thread;
  secondaryThread?: Thread;
  activePane: ConversationPaneID;
  allowThreadAutoActivation: boolean;
  sessionTabs: SessionTab[];
  activeSessionTabID: string;
  threads: Thread[];
  running: boolean;
  status: string;
  // turnTokenUsage tracks per-turn cumulative token counts
  // pushed by the appserver's "turn/usage" notification. The samples field
  // is a rolling window used to derive a smoothed tokens-per-second read
  // for the live token-speed gauge in the composer.
  turnTokenUsage: Record<string, TurnTokenUsage>;
  // turnRequestContext tracks the latest request-shape telemetry emitted
  // during a turn. It explains cache-sensitive context structure without
  // treating provider cache counters as retained conversation size.
  turnRequestContext: Record<string, TurnRequestContextDigest>;
  // turnStreamStatus tracks transient transport lifecycle chips for live turns.
  // It is UI-only state, cleared as soon as the stream reconnects or the turn
  // settles. liveProgress is structural, so chips do not infer animation from
  // Chinese display text.
  turnStreamStatus: Record<string, TurnStreamStatus>;
  // turnStreamTransport remembers the last provider-reported transport for a
  // turn so later reconnect lifecycle events can say "WebSocket" or "SSE"
  // without coupling UI text to provider names.
  turnStreamTransport: Record<string, string>;
  // lastViewedTurnByThreadID remembers the most recent turn that the user
  // has actually been on the thread for. It is the source of truth for the
  // sidebar / session-tab "has-unread" indicator on work sessions — a thread
  // is unread when its latest completed turn ID is not in this map (or the
  // entry is older). Active-tab tracking is what advances this map. The
  // user-visible completion boundary is answer-ready, not runtime cleanup:
  // a still-running turn with a terminal answer counts as completed here,
  // so unread is not deferred until turn/completed and then re-applied.
  lastViewedTurnByThreadID: Record<string, string>;
};

export type ThreadTurnSummary = Pick<
  Turn,
  "id" | "status" | "started_at" | "completed_at" | "duration_ms"
>;

export type ThreadSummary = Omit<
  Thread,
  "turns"
> & {
  turns: ThreadTurnSummary[];
  turn_count: number;
};

type ThreadRunningCandidate = {
  status: Thread["status"];
  turns?: TurnAnswerReadyCandidate[];
  child_agents?: Array<Pick<Agent, "status" | "nested_running_count">>;
};

type TurnAnswerReadyCandidate = Pick<Turn, "status"> &
  Partial<Pick<Turn, "answer_ready_at" | "items">>;

type TurnTokenSample = {
  tokens: number;
  at: number;
};

type TokenSpeedSource = "real" | "estimated" | "none";

type TurnTokenUsage = {
  threadID: string;
  inputTokens: number;
  outputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
  speedTokens: number;
  speedSource: Exclude<TokenSpeedSource, "none">;
  samples: TurnTokenSample[];
  // contextTokens is the retained conversation estimate produced by the
  // agent loop after request-only context has been excluded. It is what the
  // composer context meter displays.
  contextTokens?: number;
  // contextWindowTokens is the resolved runtime window size for the active
  // model at the time of the latest real (provider-reported) usage sample.
  // Streaming estimates preserve it from the previous real sample so the
  // context meter does not flicker to "unknown" between provider reports.
  contextWindowTokens?: number;
  model?: string;
};

export type TurnRequestContextDigest = {
  stepIndex: number;
  messageCount: number;
  stablePrefix: number;
  turnPrefix: number;
  transientMessages: number;
  hiddenMessages: number;
  toolCount: number;
  stablePrefixBytes: number;
  turnPrefixBytes: number;
  messageBytes: number;
  dynamicBytes: number;
  toolSchemaBytes: number;
  promptCacheKey?: string;
  stablePrefixHash?: string;
  turnPrefixHash?: string;
  toolSurfaceHash?: string;
  loadableToolSurfaceHash?: string;
};

export type TurnContextUsage = {
  turnID: string;
  // used is the retained conversation estimate after request-only context
  // has been excluded. Raw provider input/cache usage is not a proxy for
  // this number because it can include one-off tool context.
  used: number;
  window: number;
  inputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
  requestContext?: TurnRequestContextDigest;
};

type TurnTokenSpeedSnapshot = {
  tokensPerSecond: number;
  source: TokenSpeedSource;
  sampledAt?: number;
};

type ContextUsageFallback = {
  model?: string;
  contextWindowTokens?: number;
};

const TOKEN_SPEED_WINDOW_MS = 2000;
const STREAM_TEXT_FIELDS: StreamTextField[] = ["text", "arguments", "result"];
// Live token speed and request-shape telemetry are UI caches, not the durable
// turn history. Completed turns already persist their usage totals on Turn.
// Keeping a generous recent window preserves model/context fallbacks while
// preventing every 250ms stream sample from copying an ever-growing record.
export const RETAINED_TURN_TELEMETRY_LIMIT = 256;

const INITIAL_DRAFT_SESSION_TAB_ID = "draft:initial";

const initialState: AppState = {
  projects: [],
  activePane: "primary",
  allowThreadAutoActivation: false,
  sessionTabs: [],
  activeSessionTabID: "",
  threads: [],
  running: false,
  status: "connecting",
  turnTokenUsage: {},
  turnRequestContext: {},
  turnStreamStatus: {},
  turnStreamTransport: {},
  lastViewedTurnByThreadID: {},
};

function reduceServerEvent(state: AppState, event: ServerEvent): AppState {
  switch (event.kind) {
    case "notification":
      return reduceNotification(state, event.message);
    case "server-request": {
      void window.wuu.rejectServerRequest(
        event.message.id,
        `unsupported server request: ${event.message.method}`,
      );
      return state;
    }
    case "server-error":
      return {
        ...state,
        status: statusMessageForError(event.message, localizedText("error.server")),
      };
    case "server-exit":
      return {
        ...state,
        running: false,
        status: event.message.trim() || localizedText("appState.coreExited"),
      };
  }
}

function serverEventTargetsActiveContext(
  event: ServerEvent,
  state: AppState,
): boolean {
  return event.workdir === state.activeContext?.cwd;
}

type StreamingNotificationHandling =
  | "state"
  | "stream"
  | "stream-state"
  | "background-stream"
  | "skip";

// Both Harness and a mounted inspector may consume the same bridge event.
// Text belongs to the shared stream store and must be appended only once.
const ingestedStreamEvents = new WeakMap<ServerEvent, boolean>();
function ingestStreamOnce(event: ServerEvent, ingest: () => boolean): boolean {
  const previous = ingestedStreamEvents.get(event);
  if (previous !== undefined) return previous;
  const visible = ingest();
  ingestedStreamEvents.set(event, visible);
  return visible;
}

function handleStreamingNotification(
  event: ServerEvent,
  state: AppState,
): StreamingNotificationHandling {
  if (event.kind !== "notification") {
    return "state";
  }
  const notification = event.message;
  const params = notification.params as Record<string, unknown> | undefined;
  switch (notification.method) {
    case "item/agentMessage/delta": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      const hasVisibleText = ingestStreamOnce(event, () => appendStreamDelta(params, "text"));
      return streamHandlingForThread(active, hasVisibleText);
    }
    case "item/agentMessage/replace": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      const hasVisibleText = ingestStreamOnce(event, () => replaceStreamText(params, "text"));
      return streamHandlingForThread(active, hasVisibleText);
    }
    case "item/reasoning/delta": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      const hasVisibleText = ingestStreamOnce(event, () => appendStreamDelta(params, "text"));
      return streamHandlingForThread(active, hasVisibleText);
    }
    case "item/reasoning/replace": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      const hasVisibleText = ingestStreamOnce(event, () => replaceStreamText(params, "text"));
      return streamHandlingForThread(active, hasVisibleText);
    }
    case "item/toolCall/delta": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      ingestStreamOnce(event, () => appendStreamDelta(params, "arguments"));
      return active ? "stream" : "background-stream";
    }
    case "item/toolCall/outputDelta": {
      const active = notificationTargetsActiveThread(params, state);
      if (!active && !notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      ingestStreamOnce(event, () => appendStreamDelta(params, "result"));
      return active ? "stream" : "background-stream";
    }
    case "turn/event":
      return "skip";
    case "turn/usage":
      return "skip";
    case "item/started":
    case "item/completed":
      if (!notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      syncStreamItem(params);
      return "state";
    case "item/removed":
      if (!notificationTargetsKnownThread(params, state)) {
        return "skip";
      }
      clearRemovedItemStream(params);
      return "state";
    default:
      return "state";
  }
}

function streamHandlingForThread(
  active: boolean,
  hasVisibleText: boolean,
): StreamingNotificationHandling {
  if (!active) {
    return "background-stream";
  }
  return hasVisibleText ? "stream-state" : "stream";
}

function serverEventShouldRefreshGit(event: ServerEvent): boolean {
  if (event.kind !== "notification") {
    return false;
  }
  return (
    event.message.method === "turn/completed" ||
    event.message.method === "turn/error"
  );
}

function notificationTargetsActiveThread(
  params: Record<string, unknown> | undefined,
  state: AppState,
): boolean {
  const threadID = threadIDFromParams(params);
  return (
    !threadID ||
    threadID === state.thread?.id ||
    threadID === state.secondaryThread?.id
  );
}

function notificationTargetsKnownThread(
  params: Record<string, unknown> | undefined,
  state: AppState,
): boolean {
  const threadID = threadIDFromParams(params);
  return (
    !threadID ||
    threadID === state.thread?.id ||
    threadID === state.secondaryThread?.id ||
    state.threads.some((thread) => thread.id === threadID)
  );
}

function appendStreamDelta(
  params: Record<string, unknown> | undefined,
  field: StreamTextField,
): boolean {
  const turnID = params?.turn_id as string | undefined;
  const itemID = params?.item_id as string | undefined;
  const delta = params?.delta as string | undefined;
  if (!turnID || !itemID || !delta) {
    return false;
  }
  const key = streamTextKey(turnID, itemID, field);
  if (field !== "text") {
    streamTextStore.append(key, delta);
    return false;
  }
  // /\S/ short-circuits at the first visible character; trimming the full
  // accumulated text here made every delta O(message length).
  const hadVisibleText = /\S/.test(streamTextStore.get(key));
  streamTextStore.append(key, delta);
  return !hadVisibleText && /\S/.test(delta);
}

function replaceStreamText(
  params: Record<string, unknown> | undefined,
  field: StreamTextField,
): boolean {
  const turnID = params?.turn_id as string | undefined;
  const itemID = params?.item_id as string | undefined;
  const text = params?.text;
  if (!turnID || !itemID || typeof text !== "string") {
    return false;
  }
  const key = streamTextKey(turnID, itemID, field);
  const wasEmpty = streamTextStore.get(key).trim().length === 0;
  streamTextStore.replace(key, text);
  return field === "text" && wasEmpty && text.trim().length > 0;
}

function syncStreamItem(params: Record<string, unknown> | undefined): void {
  const turnID = params?.turn_id as string | undefined;
  const item = params?.item as ThreadItem | undefined;
  if (!turnID || !item?.id) {
    return;
  }
  const completed = (item.status ?? "in_progress") !== "in_progress";
  const hasFinalText = typeof item.text === "string" && item.text.length > 0;
  const retainTextStream =
    completed &&
    (item.type === "agent_message" || item.type === "reasoning") &&
    !hasFinalText;
  if (typeof item.text === "string") {
    // Snapshots can arrive behind the delta stream, including completed
    // snapshots. Never let an older snapshot shrink the text the user is
    // already reading; release the stream cache only after snapshots catch up.
    if (streamSnapshotCoversCachedValue(turnID, item.id, "text", item.text)) {
      streamTextStore.set(streamTextKey(turnID, item.id, "text"), item.text);
    }
  }
  if (typeof item.arguments === "string") {
    if (
      streamSnapshotCoversCachedValue(
        turnID,
        item.id,
        "arguments",
        item.arguments,
      )
    ) {
      streamTextStore.set(
        streamTextKey(turnID, item.id, "arguments"),
        item.arguments,
      );
    }
  }
  if (typeof item.result === "string") {
    if (streamSnapshotCoversCachedValue(turnID, item.id, "result", item.result)) {
      streamTextStore.set(streamTextKey(turnID, item.id, "result"), item.result);
    }
  }
  if (
    completed &&
    !retainTextStream &&
    itemStreamSnapshotsCoverCachedValues(turnID, item)
  ) {
    window.requestAnimationFrame(() =>
      streamTextStore.clearItem(turnID, item.id),
    );
  }
}

function clearRemovedItemStream(params: Record<string, unknown> | undefined): void {
  const turnID = params?.turn_id as string | undefined;
  const itemID = params?.item_id as string | undefined;
  if (turnID && itemID) {
    streamTextStore.clearItem(turnID, itemID);
  }
}

function streamSnapshotCoversCachedValue(
  turnID: string,
  itemID: string,
  field: StreamTextField,
  snapshotValue: string,
): boolean {
  const key = streamTextKey(turnID, itemID, field);
  if (!streamTextStore.has(key)) {
    return true;
  }
  return snapshotValue.length >= streamTextStore.get(key).length;
}

function itemStreamSnapshotsCoverCachedValues(
  turnID: string,
  item: ThreadItem,
): boolean {
  for (const field of STREAM_TEXT_FIELDS) {
    const key = streamTextKey(turnID, item.id, field);
    if (!streamTextStore.has(key)) {
      continue;
    }
    const value = item[field];
    if (typeof value !== "string") {
      return false;
    }
    if (!streamSnapshotCoversCachedValue(turnID, item.id, field, value)) {
      return false;
    }
  }
  return true;
}

function syncRunningThreadStreamItems(thread: Thread): void {
  for (const turn of thread.turns) {
    if (turn.status !== "in_progress") {
      continue;
    }
    for (const item of turn.items) {
      if ((item.status ?? "in_progress") !== "in_progress") {
        continue;
      }
      syncStreamItem({ turn_id: turn.id, item });
    }
  }
}

function reduceNotification(
  state: AppState,
  notification: AppServerNotification,
): AppState {
  const params = notification.params as Record<string, unknown> | undefined;
  switch (notification.method) {
    case "config/changed": {
      if (!state.initialized || !isConfigChangedNotification(params)) {
        return state;
      }
      return {
        ...state,
        initialized: {
          ...state.initialized,
          provider: params.provider,
          model: params.model,
          effort: params.effort,
          variant: params.variant,
          model_roles: params.model_roles,
          model_aliases: params.model_aliases,
          providers: params.providers,
        },
      };
    }
    case "plugin/inventory/changed": {
      if (!state.initialized || !isPluginInventoryChangedNotification(params)) {
        return state;
      }
      return {
        ...state,
        initialized: {
          ...state.initialized,
          extension_inventory: params.extension_inventory,
        },
      };
    }
    case "thread/started":
    case "thread/resumed": {
      const thread = threadFromRecord(recordValue(params, "thread"));
      if (!thread) {
        return state;
      }
      if (!threadMatchesActiveContext(thread, state.activeContext)) {
        return state;
      }
      const currentThread =
        state.thread?.id === thread.id
          ? state.thread
          : state.secondaryThread?.id === thread.id
            ? state.secondaryThread
            : state.threads.find((item) => item.id === thread.id);
      // thread/start emits its notification before the matching RPC response.
      // If the renderer has already inserted an optimistic first turn, the
      // notification's empty snapshot must not erase it and briefly restore
      // the empty-conversation hero.
      const mergedThread = currentThread
        ? {
            ...thread,
            turns: thread.turns.length > 0 ? thread.turns : currentThread.turns,
            child_agents: thread.child_agents ?? currentThread.child_agents,
          }
        : thread;
      const knownThread = state.threads.some((item) => item.id === thread.id);
      const updatesVisibleThread =
        state.thread?.id === thread.id ||
        state.secondaryThread?.id === thread.id;
      // Auto-activation only ever brings a normal user conversation into the
      // now-empty pane. A read-only subtask session (subagent child) is opened
      // on purpose from the sidebar / agent row — it must never hijack the
      // pane the user is looking at. When a new thread does take over the
      // pane, re-derive the composer running state from that thread instead of
      // inheriting a global flag another session may have set.
      const autoActivateNewThread =
        state.allowThreadAutoActivation &&
        !state.thread &&
        !knownThread &&
        !thread.read_only;
      const activateThread =
        state.thread?.id === thread.id || autoActivateNewThread;
      return {
        ...state,
        thread: activateThread ? mergedThread : state.thread,
        secondaryThread:
          state.secondaryThread?.id === thread.id
            ? mergedThread
            : state.secondaryThread,
        allowThreadAutoActivation: activateThread
          ? true
          : state.allowThreadAutoActivation,
        threads: upsertThread(state.threads, mergedThread),
        sessionTabs: syncThreadSessionTabTitle(state.sessionTabs, mergedThread),
        status: activateThread || updatesVisibleThread ? "ready" : state.status,
        running: autoActivateNewThread
          ? isThreadRunning(mergedThread)
          : state.running,
      };
    }
    case "thread/historyLoaded": {
      const threadID = threadIDFromParams(params);
      if (!threadID || typeof params?.cursor !== "string" || !Array.isArray(params.turns)) return state;
      return updateThreadByID(state, threadID, (thread) => {
        if (thread.history_cursor !== params.cursor) return thread;
        const pages = params.turns as Turn[];
        const currentIDs = new Set(thread.turns.map(turn => turn.id));
        const older = pages.filter(turn => !currentIDs.has(turn.id));
        const retained = thread.turns.map(turn => {
          const earlier = pages.find(page => page.id === turn.id);
          if (!earlier) return turn;
          const ids = new Set(turn.items.map(item => item.id));
          return { ...turn, items: [...earlier.items.filter(item => !ids.has(item.id)), ...turn.items] };
        });
        return { ...thread, turns: [...older, ...retained], history_cursor: typeof params.history_cursor === "string" ? params.history_cursor : undefined };
      });
    }
    case "thread/updated": {
      const thread = threadFromRecord(recordValue(params, "thread"));
      if (!thread || !threadMatchesActiveContext(thread, state.activeContext)) {
        return state;
      }
      return updateThreadByID(state, thread.id, (current) => ({
        ...thread,
        turns: mergeThreadUpdatedTurns(thread.turns, current.turns),
        child_agents: thread.child_agents ?? current.child_agents,
      }));
    }
    case "agent/updated": {
      const threadID = threadIDFromParams(params);
      const agent = agentFromRecord(recordValue(params, "agent"));
      if (!threadID || !agent || !isDirectChildAgent(threadID, agent)) {
        return state;
      }
      return updateThreadByID(state, threadID, (thread) =>
        upsertThreadChildAgent(thread, agent),
      );
    }
    case "turn/started": {
      const turn = turnFromRecord(recordValue(params, "turn"));
      if (!turn) {
        return state;
      }
      const threadID = threadIDFromParams(params);
      const threadIsActive = threadID === activeThreadIDForState(state);
      return updateThreadByID(
        state,
        threadID,
        (thread) => upsertTurn(thread, turn),
        threadIsActive ? { running: true } : {},
      );
    }
    case "item/started":
    case "item/completed": {
      const item = params?.item as ThreadItem | undefined;
      const turnID = params?.turn_id as string | undefined;
      const threadID = threadIDFromParams(params);
      if (!item || !turnID) {
        return state;
      }
      const completedAtMS = params?.completed_at_ms as number | undefined;
      const answerReadyAt =
        notification.method === "item/completed" &&
        item.type === "agent_message" &&
        item.status === "completed" &&
        item.terminal === true &&
        typeof completedAtMS === "number" &&
        Number.isFinite(completedAtMS)
          ? new Date(completedAtMS).toISOString()
          : undefined;
      const next = updateThreadByID(state, threadID, (thread) => {
        const updated = upsertTurnItem(thread, turnID, item);
        return answerReadyAt
          ? markTurnAnswerReady(updated, turnID, answerReadyAt)
          : updated;
      });
      return next;
    }
    case "item/removed": {
      const turnID = params?.turn_id as string | undefined;
      const itemID = params?.item_id as string | undefined;
      const threadID = threadIDFromParams(params);
      if (!turnID || !itemID) {
        return state;
      }
      return updateThreadByID(state, threadID, (thread) =>
        removeTurnItem(thread, turnID, itemID),
      );
    }
    case "item/agentMessage/delta":
      return applyDelta(state, params, "text");
    case "item/agentMessage/replace":
      return applyReplace(state, params, "text");
    case "item/reasoning/delta":
      return applyDelta(state, params, "text");
    case "item/reasoning/replace":
      return applyReplace(state, params, "text");
    case "item/toolCall/delta":
      return applyDelta(state, params, "arguments");
    case "item/toolCall/outputDelta":
      return applyDelta(state, params, "result");
    case "turn/completed":
    case "turn/error": {
      const turn = turnFromRecord(recordValue(params, "turn"));
      const threadID = threadIDFromParams(params);
      if (!turn) {
        return threadID === activeThreadIDForState(state)
          ? { ...state, running: false }
          : state;
      }
      releaseSettledTurnStreams(turn);
      // running/status describe the pane the user is looking at. A background
      // session (another tab, or a subagent child) finishing its turn must not
      // clear the active thread's live running indicator or overwrite its
      // status text, so only scope the patch when the turn belongs to the
      // active thread.
      const threadIsActive = threadID === activeThreadIDForState(state);
      return clearTurnStreamStatus(
        updateThreadByID(
          state,
          threadID,
          (thread) => upsertTurn(thread, turn),
          threadIsActive ? { running: false, status: "ready" } : {},
        ),
        turn.id,
        { clearTransport: true },
      );
    }
    case "turn/usage": {
      const turnID = stringValue(params, "turn_id");
      if (!turnID) {
        return state;
      }
      return appendTurnTokenSample(
        state,
        turnID,
        stringValue(params, "thread_id") ?? "",
        numberValue(params, "input_tokens") ?? 0,
        numberValue(params, "output_tokens") ?? 0,
        numberValue(params, "cache_creation_tokens") ?? 0,
        numberValue(params, "cache_read_tokens") ?? 0,
        Date.now(),
        numberValue(params, "context_window_tokens") ?? 0,
        stringValue(params, "model"),
        numberValue(params, "context_tokens") ?? 0,
      );
    }
    case "turn/event": {
      const turnID = stringValue(params, "turn_id");
      const event = recordValue(params, "event");
      const digest = requestContextDigestFromRecord(
        recordValue(event, "request_context"),
      );
      if (!turnID) {
        return state;
      }
      const providerState = recordValue(event, "provider_state");
      const stateWithTransport = updateTurnStreamTransport(
        state,
        turnID,
        providerState,
      );
      const providerStatus = streamStatusFromProviderState(providerState);
      const stateWithStatus =
        providerStatus === undefined
          ? stateWithTransport
          : setTurnStreamStatus(stateWithTransport, turnID, providerStatus);
      if (!digest) {
        return stateWithStatus;
      }
      return {
        ...stateWithStatus,
        turnRequestContext: withRetainedTurnTelemetry(
          stateWithStatus.turnRequestContext,
          turnID,
          digest,
        ),
      };
    }
    default:
      return state;
  }
}

function isPluginInventoryChangedNotification(
  value: unknown,
): value is PluginInventoryChangedNotification {
  if (!isRecord(value) || typeof value.epoch !== "number" || !Array.isArray(value.extension_inventory)) {
    return false;
  }
  return value.extension_inventory.every((item) =>
    isRecord(item)
    && typeof item.id === "string"
    && typeof item.name === "string"
    && typeof item.kind === "string"
    && typeof item.state === "string"
    && isRecord(item.provenance),
  );
}

function isConfigChangedNotification(
  value: unknown,
): value is ConfigChangedNotification {
  if (!isRecord(value) || typeof value.provider !== "string" || typeof value.model !== "string") {
    return false;
  }
  if (value.effort !== undefined && typeof value.effort !== "string") {
    return false;
  }
  if (value.variant !== undefined && typeof value.variant !== "string") {
    return false;
  }
  return Array.isArray(value.model_roles) && Array.isArray(value.providers);
}

function setTurnStreamStatus(
  state: AppState,
  turnID: string,
  status: TurnStreamStatus | null,
): AppState {
  if (!status || status.text.trim() === "") {
    return clearTurnStreamStatus(state, turnID);
  }
  const existing = state.turnStreamStatus[turnID];
  if (
    existing?.text === status.text &&
    existing.liveProgress === status.liveProgress
  ) {
    return state;
  }
  return {
    ...state,
    turnStreamStatus: {
      ...state.turnStreamStatus,
      [turnID]: status,
    },
  };
}

function clearTurnStreamStatus(
  state: AppState,
  turnID: string,
  options: { clearTransport?: boolean } = {},
): AppState {
  const hasStatus = state.turnStreamStatus[turnID] !== undefined;
  const hasTransport = state.turnStreamTransport[turnID] !== undefined;
  if (!hasStatus && !(options.clearTransport && hasTransport)) {
    return state;
  }
  const nextStatus = hasStatus
    ? { ...state.turnStreamStatus }
    : state.turnStreamStatus;
  if (hasStatus) {
    delete nextStatus[turnID];
  }
  const nextTransport =
    options.clearTransport && hasTransport
      ? { ...state.turnStreamTransport }
      : state.turnStreamTransport;
  if (options.clearTransport && hasTransport) {
    delete nextTransport[turnID];
  }
  return {
    ...state,
    turnStreamStatus: nextStatus,
    turnStreamTransport: nextTransport,
  };
}

function updateTurnStreamTransport(
  state: AppState,
  turnID: string,
  providerState: JsonRecord | undefined,
): AppState {
  const transport = transportFromProviderState(providerState);
  if (!transport || state.turnStreamTransport[turnID] === transport) {
    return state;
  }
  return {
    ...state,
    turnStreamTransport: {
      ...state.turnStreamTransport,
      [turnID]: transport,
    },
  };
}

function transportFromProviderState(
  providerState: JsonRecord | undefined,
): string | undefined {
  if (!providerState) {
    return undefined;
  }
  const fallbackTransport = normalizedTransport(
    stringValue(providerState, "fallback_transport"),
  );
  if (booleanValue(providerState, "fallback_active") === true && fallbackTransport) {
    return fallbackTransport;
  }
  return normalizedTransport(stringValue(providerState, "transport"));
}

function streamStatusFromProviderState(
  providerState: JsonRecord | undefined,
): TurnStreamStatus | undefined {
  if (!providerState) {
    return undefined;
  }
  const diagnostic = stringValue(providerState, "diagnostic");
  const fallbackActive = booleanValue(providerState, "fallback_active") === true;
  if (diagnostic !== "provider_transport_failure" && !fallbackActive) {
    return undefined;
  }
  const failedTransport = failedTransportLabelFromProviderState(providerState);
  const fallbackTransport = transportLabel(
    stringValue(providerState, "fallback_transport"),
  );
  const failurePhase = stringValue(providerState, "transport_failure_phase");
  const eventsEmitted =
    booleanValue(providerState, "events_emitted") === true ||
    failurePhase === "after_message_stream_start";
  if (eventsEmitted) {
    return {
      text: t("appState.streamInterrupted", {
        subject: transportSubject(failedTransport),
      }),
      liveProgress: false,
    };
  }
  if (fallbackTransport) {
    return {
      text: failedTransport
        ? t("appState.transportFallback", {
            failed: failedTransport,
            fallback: fallbackTransport,
          })
        : t("appState.streamFallback", { fallback: fallbackTransport }),
      liveProgress: false,
    };
  }
  return {
    text: t("appState.streamInterrupted", {
      subject: transportSubject(failedTransport),
    }),
    liveProgress: false,
  };
}

function failedTransportLabelFromProviderState(
  providerState: JsonRecord,
): string | undefined {
  const failedTransport = transportLabel(
    stringValue(providerState, "failed_transport"),
  );
  if (failedTransport) {
    return failedTransport;
  }
  const fallbackReason = stringValue(providerState, "fallback_reason")
    ?.trim()
    .toLowerCase();
  if (
    fallbackReason?.includes("websocket") ||
    fallbackReason?.includes("web socket")
  ) {
    return "WebSocket";
  }
  return transportLabel(stringValue(providerState, "transport"));
}

function transportSubject(transport: string | undefined): string {
  return transport
    ? t("appState.namedMessageStream", { transport })
    : t("appState.messageStream");
}

function transportLabel(transport: string | undefined): string | undefined {
  switch (normalizedTransport(transport)) {
    case "websocket":
    case "websocket-cached":
    case "ws":
      return "WebSocket";
    case "sse":
    case "http":
    case "https":
      return "HTTP";
    default:
      return undefined;
  }
}

function normalizedTransport(transport: string | undefined): string | undefined {
  const normalized = transport?.trim().toLowerCase();
  return normalized ? normalized : undefined;
}

function booleanValue(record: JsonRecord, key: string): boolean | undefined {
  const value = record[key];
  return typeof value === "boolean" ? value : undefined;
}

function positiveInteger(value: number | undefined): number | undefined {
  if (value === undefined) {
    return undefined;
  }
  const integer = Math.floor(value);
  return integer > 0 ? integer : undefined;
}

function releaseSettledTurnStreams(turn: Turn): void {
  if (turn.status === "in_progress") {
    return;
  }
  for (const item of turn.items) {
    if (item.status === "in_progress") {
      continue;
    }
    for (const field of STREAM_TEXT_FIELDS) {
      const value = item[field];
      if (
        typeof value === "string" &&
        value.length > 0 &&
        streamSnapshotCoversCachedValue(turn.id, item.id, field, value)
      ) {
        streamTextStore.clearField(turn.id, item.id, field);
      }
    }
  }
}

function requestContextDigestFromRecord(
  record: JsonRecord | undefined,
): TurnRequestContextDigest | undefined {
  if (!record) {
    return undefined;
  }
  const stepIndex = numberValue(record, "step_index");
  const messageCount = numberValue(record, "message_count");
  const stablePrefix = numberValue(record, "stable_prefix");
  const turnPrefix = numberValue(record, "turn_prefix");
  if (
    stepIndex === undefined ||
    messageCount === undefined ||
    stablePrefix === undefined ||
    turnPrefix === undefined
  ) {
    return undefined;
  }
  return {
    stepIndex,
    messageCount,
    stablePrefix,
    turnPrefix,
    transientMessages: numberValue(record, "transient_messages") ?? 0,
    hiddenMessages: numberValue(record, "hidden_messages") ?? 0,
    toolCount: numberValue(record, "tool_count") ?? 0,
    stablePrefixBytes: numberValue(record, "stable_prefix_bytes") ?? 0,
    turnPrefixBytes: numberValue(record, "turn_prefix_bytes") ?? 0,
    messageBytes: numberValue(record, "message_bytes") ?? 0,
    dynamicBytes: numberValue(record, "dynamic_bytes") ?? 0,
    toolSchemaBytes: numberValue(record, "tool_schema_bytes") ?? 0,
    promptCacheKey: stringValue(record, "prompt_cache_key"),
    stablePrefixHash: stringValue(record, "stable_prefix_hash"),
    turnPrefixHash: stringValue(record, "turn_prefix_hash"),
    toolSurfaceHash: stringValue(record, "tool_surface_hash"),
    loadableToolSurfaceHash: stringValue(
      record,
      "loadable_tool_surface_hash",
    ),
  };
}

function applyDelta(
  state: AppState,
  params: Record<string, unknown> | undefined,
  field: "text" | "arguments" | "result",
): AppState {
  const threadID = threadIDFromParams(params);
  const turnID = params?.turn_id as string | undefined;
  const itemID = params?.item_id as string | undefined;
  const delta = params?.delta as string | undefined;
  if (!turnID || !itemID || !delta) {
    return state;
  }
  return updateThreadByID(state, threadID, (thread) =>
    updateTurnItem(thread, turnID, itemID, (item) => ({
      ...item,
      [field]: `${item[field] ?? ""}${delta}`,
    })),
  );
}

function applyReplace(
  state: AppState,
  params: Record<string, unknown> | undefined,
  field: "text" | "arguments" | "result",
): AppState {
  const threadID = threadIDFromParams(params);
  const turnID = params?.turn_id as string | undefined;
  const itemID = params?.item_id as string | undefined;
  const text = params?.text;
  if (!turnID || !itemID || typeof text !== "string") {
    return state;
  }
  return updateThreadByID(state, threadID, (thread) =>
    updateTurnItem(thread, turnID, itemID, (item) => ({
      ...item,
      [field]: text,
    })),
  );
}

function threadIDFromParams(
  params: Record<string, unknown> | undefined,
): string | undefined {
  const threadID = params?.thread_id;
  return typeof threadID === "string" && threadID ? threadID : undefined;
}

function updateThreadByID(
  state: AppState,
  threadID: string | undefined,
  update: (thread: Thread) => Thread,
  activePatch: Partial<Pick<AppState, "running" | "status">> = {},
): AppState {
  if (!threadID) {
    return state;
  }
  const primaryActive = state.thread?.id === threadID;
  const secondaryActive = state.secondaryThread?.id === threadID;
  if (
    (primaryActive && state.thread) ||
    (secondaryActive && state.secondaryThread)
  ) {
    const currentThread = primaryActive ? state.thread : state.secondaryThread;
    if (!currentThread) {
      return state;
    }
    const thread = update(currentThread);
    const patch =
      activeThreadIDForState(state) === threadID ||
      activePatch.running === false
        ? activePatch
        : {};
    return {
      ...state,
      ...patch,
      thread: primaryActive ? thread : state.thread,
      secondaryThread: secondaryActive ? thread : state.secondaryThread,
      threads: upsertThread(state.threads, thread),
      sessionTabs: syncThreadSessionTabTitle(state.sessionTabs, thread),
    };
  }
  let updated = false;
  const threads = state.threads.map((thread) => {
    if (thread.id !== threadID) {
      return thread;
    }
    updated = true;
    return update(thread);
  });
  if (!updated) {
    return state;
  }
  const updatedThread = threads.find((thread) => thread.id === threadID);
  return {
    ...state,
    threads: sortThreads(threads),
    sessionTabs: updatedThread
      ? syncThreadSessionTabTitle(state.sessionTabs, updatedThread)
      : state.sessionTabs,
  };
}

function updateThread(
  state: AppState,
  update: (thread: Thread) => Thread,
): AppState {
  if (!state.thread) {
    return state;
  }
  const thread = update(state.thread);
  return { ...state, thread, threads: upsertThread(state.threads, thread) };
}

/**
 * Salvage locally-known tail turns/items when a thread resume returns a
 * snapshot that lags behind what the client already holds.
 *
 * Switching session tabs re-resumes the target thread and would otherwise
 * replace its turns wholesale with the server snapshot (selectThread in
 * App.tsx). When the user just sent a message and immediately switched away
 * and back, that snapshot can still be missing the just-sent turn — it lives
 * only in client state (an optimistic turn whose turn/start hasn't committed,
 * or a freshly-committed turn the store snapshot lags on) — so the message
 * disappears on return.
 *
 * We only salvage when the resumed turns are a strict prefix of the turns the
 * client already holds — either by whole turns or by extra items at the end of
 * an existing turn. That is the unambiguous "client is ahead" signature. Any
 * other divergence (an edit/fork that truncated history, or a server snapshot
 * that is genuinely ahead of the client) breaks the prefix match, and we defer
 * to the resumed snapshot as authoritative. The overlapping prefix always uses
 * the server's (fresher) turn/item objects; only the client's extra tail
 * carries over.
 */
export function reconcileResumedThreadTurns(
  resumed: Thread,
  local: Thread | undefined,
): Thread {
  const localTurns = local?.turns;
  if (!localTurns || localTurns.length < resumed.turns.length) {
    return resumed;
  }
  for (let index = 0; index < resumed.turns.length; index += 1) {
    if (resumed.turns[index].id !== localTurns[index].id) {
      return resumed;
    }
    if (!turnItemsArePrefix(resumed.turns[index], localTurns[index])) {
      return resumed;
    }
  }
  let changed = false;
  const mergedTurns = resumed.turns.map((turn, index) => {
    const localTurn = localTurns[index];
    if (localTurn.items.length <= turn.items.length) {
      return turn;
    }
    changed = true;
    return {
      ...turn,
      items: [...turn.items, ...localTurn.items.slice(turn.items.length)],
    };
  });
  const salvagedTail = localTurns.slice(resumed.turns.length);
  if (salvagedTail.length > 0) {
    changed = true;
  }
  return changed
    ? { ...resumed, turns: [...mergedTurns, ...salvagedTail] }
    : resumed;
}

// A history edit first rewinds the server thread, then immediately starts a
// replacement turn. Preserve that local placeholder when the rewind's
// thread/updated notification arrives after the optimistic render. A follow-up
// started after the previous answer is ready is the same shape: keep an
// in-progress tail when a stale snapshot still omits it.
function mergeThreadUpdatedTurns(incoming: Turn[], current: Turn[]): Turn[] {
  if (incoming.length === 0) {
    return current;
  }
  if (incoming.length >= current.length) {
    return incoming;
  }
  for (let index = 0; index < incoming.length; index += 1) {
    if (incoming[index].id !== current[index].id) {
      return incoming;
    }
  }
  const localTail = current.slice(incoming.length);
  return localTail.every(
    (turn) =>
      turn.status === "in_progress" ||
      turn.id.startsWith(OPTIMISTIC_TURN_ID_PREFIX),
  )
    ? [...incoming, ...localTail]
    : incoming;
}

function turnItemsArePrefix(resumed: Turn, local: Turn): boolean {
  if (resumed.items.length > local.items.length) {
    return false;
  }
  for (let index = 0; index < resumed.items.length; index += 1) {
    if (resumed.items[index].id !== local.items[index].id) {
      return false;
    }
  }
  return true;
}

function upsertThread(threads: Thread[], thread: Thread | undefined): Thread[] {
  const validThreads = sortThreads(threads);
  if (!isThread(thread)) {
    return validThreads;
  }
  // Archive semantics intentionally retain the thread in `state.threads` so the
  // Settings → Archive page can list every archived session, and so the archive
  // action stays reversible from there. Read-only threads are still real
  // conversations that need to render — they only differ in mutation rights.
  // Filtering archived out of sidebar surfaces is the job of pinnedThreads /
  // projectThreads / scratchThreads, not of this generic upsert.
  const index = validThreads.findIndex((item) => item.id === thread.id);
  if (index < 0) {
    return sortThreads([thread, ...validThreads]);
  }
  const next = validThreads.slice();
  next[index] = thread;
  return sortThreads(next);
}

function conversationPaneThreadsByID(
  threads: Thread[],
  primaryThread?: Thread,
  secondaryThread?: Thread,
): Map<string, Thread> {
  const byID = new Map<string, Thread>();
  for (const thread of threads) {
    byID.set(thread.id, thread);
  }
  if (primaryThread) {
    byID.set(primaryThread.id, primaryThread);
  }
  if (secondaryThread) {
    byID.set(secondaryThread.id, secondaryThread);
  }
  return byID;
}

function sortThreads(threads: Thread[]): Thread[] {
  // Two-section sort. Running threads use `created_at` as the key so that
  // streaming updates (which bump `updated_at`) do not reshuffle them —
  // clicking or switching between two running threads must leave the sidebar
  // order alone. Settled threads keep the recency-first behavior, so the most
  // recently completed conversation bubbles to the top of the settled group.
  // Archived threads stay in the list so the Settings → Archive page can show
  // them; sidebar surfaces must filter them out themselves.
  const valid = threads.filter((thread): thread is Thread => isThread(thread));
  return sortThreadCandidates(valid);
}

function summarizeAgentForSidebar(agent: Agent): Agent {
  return {
    id: agent.id,
    type: agent.type,
    task_name: agent.task_name,
    agent_profile: agent.agent_profile,
    agent_path: agent.agent_path,
    parent_id: agent.parent_id,
    description: agent.description,
    status: agent.status,
    nested_count: agent.nested_count,
    nested_running_count: agent.nested_running_count,
    started_at: agent.started_at,
    completed_at: agent.completed_at,
    pinned: agent.pinned,
    archived: agent.archived,
  };
}

function summarizeTurnForSidebar(turn: Turn): ThreadTurnSummary {
  const answerReady = turn.status === "in_progress" && turnIsAnswerReady(turn);
  return {
    id: turn.id,
    status: answerReady ? "completed" : turn.status,
    started_at: turn.started_at,
    completed_at:
      turn.completed_at ?? (answerReady ? turn.answer_ready_at : undefined),
    duration_ms: turn.duration_ms,
  };
}

function summarizeThreadForSidebar(
  thread: Thread,
  runningThreadIDs?: ReadonlySet<string>,
): ThreadSummary {
  const answerReady = activeTurnIsAnswerReady(thread);
  const status =
    answerReady && thread.status === "in_progress" ? "idle" : thread.status;
  return {
    id: thread.id,
    parent_id: thread.parent_id,
    agent_path: thread.agent_path,
    preview: thread.preview,
    title: thread.title,
    model_provider: thread.model_provider,
    model: thread.model,
    cwd: thread.cwd,
    workspace_id: thread.workspace_id,
    workspace_kind: thread.workspace_kind,
    status: runningThreadIDs?.has(thread.id) && !answerReady ? "in_progress" : status,
    read_only: thread.read_only,
    pinned: thread.pinned,
    folder_id: thread.folder_id,
    archived: thread.archived,
    forked_from_id: thread.forked_from_id,
    forked_from_turn_id: thread.forked_from_turn_id,
    forked_from_item_id: thread.forked_from_item_id,
    worktree: thread.worktree,
    created_at: thread.created_at,
    updated_at: thread.updated_at,
    latest_completed_turn_id: thread.latest_completed_turn_id,
    turns: thread.turns.map(summarizeTurnForSidebar),
    turn_count: thread.turns.length,
    child_agents: thread.child_agents?.map(summarizeAgentForSidebar),
  };
}

function summarizeThreadsForSidebar(
  threads: Thread[],
  runningThreadIDs?: ReadonlySet<string>,
): ThreadSummary[] {
  // Apply sidebar visibility at the shared Thread -> ThreadSummary boundary.
  // Project buckets consume these summaries directly, so leaving filtering to
  // their downstream sort path lets live subagent thread updates leak into the
  // left rail even though pinned and scratch sections hide them correctly.
  // Cross-workdir running state belongs at the same boundary: every sidebar
  // bucket must see it, including ordinary project sections whose cached thread
  // snapshots do not receive the active renderer's turn lifecycle updates.
  return sortThreadSummaries(
    threads.map((thread) => summarizeThreadForSidebar(thread, runningThreadIDs)),
  );
}

function summarizeProjectThreadsForSidebar(
  projectThreadsByProjectID: Record<string, Thread[]>,
  liveThreads: readonly Thread[],
  runningThreadIDs?: ReadonlySet<string>,
): Record<string, ThreadSummary[]> {
  // Project buckets come from the sidebar's own thread cache, which does not
  // receive the active renderer's turn lifecycle updates. Overlay the live
  // thread records before summarizing so a click-time optimistic turn reaches
  // project rows and the bell's running section right away instead of waiting
  // for the main process to report it back through runningThreadIDs. The
  // pinned and scratch buckets already derive from live state; without this
  // overlay project buckets sit one freshness tier behind them.
  //
  // Bucket membership stays owned by the cache: a live record only replaces a
  // cached one that is already in the bucket, so this never invents rows for a
  // project the cache has not listed yet.
  const liveByID = new Map<string, Thread>();
  for (const thread of liveThreads) {
    liveByID.set(thread.id, thread);
  }
  const next: Record<string, ThreadSummary[]> = {};
  for (const [projectID, threads] of Object.entries(projectThreadsByProjectID)) {
    next[projectID] = summarizeThreadsForSidebar(
      threads.map((thread) => liveByID.get(thread.id) ?? thread),
      runningThreadIDs,
    );
  }
  return next;
}

function sortThreadSummaries(threads: ThreadSummary[]): ThreadSummary[] {
  const valid = threads.filter(
    (thread): thread is ThreadSummary =>
      Boolean(thread && typeof thread.id === "string") &&
      !thread.archived &&
      !thread.read_only &&
      // The sidebar lists root sessions of a project. A thread whose
      // parent_id is set is a host-managed worker. Those live
      // under the parent thread's info panel ("子任务"), not in the
      // sidebar navigation list, regardless of pin state.
      !thread.parent_id &&
      // Older/recovered worker records can lose parent_id while retaining
      // their agent_path. agent_path is worker-only metadata, so keep these
      // records out of the root-session rail as well.
      !thread.agent_path,
  );
  return sortThreadCandidates(valid);
}

type ThreadSortCandidate = ThreadRunningCandidate &
  Pick<Thread, "created_at" | "updated_at">;

type ThreadSortEntry<T extends ThreadSortCandidate> = {
  thread: T;
  time: number;
};

function sortThreadCandidates<T extends ThreadSortCandidate>(threads: T[]): T[] {
  const running: ThreadSortEntry<T>[] = [];
  const settled: ThreadSortEntry<T>[] = [];
  for (const thread of threads) {
    const threadRunning = isThreadRunning(thread);
    const entry = {
      thread,
      time: threadRunning
        ? threadCreatedTime(thread)
        : threadTime(thread),
    };
    (threadRunning ? running : settled).push(entry);
  }
  const byNewest = (left: ThreadSortEntry<T>, right: ThreadSortEntry<T>) =>
    right.time - left.time;
  running.sort(byNewest);
  settled.sort(byNewest);
  return [
    ...running.map((entry) => entry.thread),
    ...settled.map((entry) => entry.thread),
  ];
}

function threadCreatedTime(thread: Pick<Thread, "created_at" | "updated_at">): number {
  const createdAt = Date.parse(thread.created_at);
  return Number.isFinite(createdAt) ? createdAt : 0;
}

function mergeListedThreads(current: Thread[], listed: Thread[]): Thread[] {
  const currentByID = new Map(
    current.filter(isThread).map((thread) => [thread.id, thread]),
  );
  // `listed` is built as [...live, ...archived-local-copies], so the same id can
  // appear twice when a listing fetched before a local archive/unarchive flip
  // lands after the optimistic update. Keep the later (local intent) snapshot
  // instead of emitting two rows for one conversation.
  const deduped = new Map<string, Thread>();
  for (const thread of listed) {
    deduped.set(thread.id, thread);
  }
  return sortThreads(
    [...deduped.values()].map((thread) => {
      const existing = currentByID.get(thread.id);
      if (!existing) {
        return thread;
      }
      return mergeListedThread(existing, thread);
    }),
  );
}

function mergeListedThread(existing: Thread, listed: Thread): Thread {
  const turns = mergeListedThreadTurns(existing.turns, listed.turns);
  const listedStatusRegresses =
    listed.status === "in_progress" &&
    existing.status !== "in_progress" &&
    !turns.some((turn) => turn.status === "in_progress");
  return {
    ...listed,
    title: listed.title?.trim() ? listed.title : existing.title,
    preview: listed.preview?.trim() ? listed.preview : existing.preview,
    status: listedStatusRegresses ? existing.status : listed.status,
    turns,
    child_agents:
      listed.child_agents !== undefined
        ? listed.child_agents
        : existing.child_agents,
  };
}

function mergeListedThreadTurns(existing: Turn[], listed: Turn[]): Turn[] {
  if (listed.length === 0) {
    return existing.length === 0 ? listed : existing;
  }

  let merged = listed;
  if (
    existing.length >= listed.length &&
    listed.every(
      (turn, index) =>
        turn.id === existing[index].id && turnItemsArePrefix(turn, existing[index]),
    )
  ) {
    let needsMerge = existing.length > listed.length;
    const listedTurns = listed.map((turn, index) => {
      const local = existing[index];
      if (local.items.length <= turn.items.length) {
        return turn;
      }
      needsMerge = true;
      const mergedTurn = {
        ...turn,
        items: [...turn.items, ...local.items.slice(turn.items.length)],
      };
      const localKeys = Object.keys(local) as Array<keyof Turn>;
      const mergedKeys = Object.keys(mergedTurn);
      const unchanged =
        localKeys.length === mergedKeys.length &&
        localKeys.every((key) => {
          if (key !== "items") {
            return Object.is(local[key], mergedTurn[key]);
          }
          return (
            local.items.length === mergedTurn.items.length &&
            local.items.every((item, itemIndex) => item === mergedTurn.items[itemIndex])
          );
        });
      return unchanged ? local : mergedTurn;
    });
    if (needsMerge) {
      merged = [...listedTurns, ...existing.slice(listed.length)];
    }
  }

  const existingByID = new Map(existing.map((turn) => [turn.id, turn]));
  let result = merged;
  merged.forEach((turn, index) => {
    const local = existingByID.get(turn.id);
    // Terminal state is monotonic for a turn. A thread/list request can start
    // before turn/completed and resolve after it; that stale in-progress
    // snapshot must not resurrect a turn the renderer already settled.
    if (local && local.status !== "in_progress" && turn.status === "in_progress") {
      if (result === merged) {
        result = [...merged];
      }
      result[index] = local;
    }
  });
  // A shorter listed snapshot can preserve the cached tail while reusing the
  // same listed prefix on every reconciliation. Keep the original array once
  // every resulting element is already present at the same position; callers
  // use this identity to stop cache synchronization effects from self-looping.
  if (
    result.length === existing.length &&
    result.every((turn, index) => turn === existing[index])
  ) {
    return existing;
  }
  return result;
}

function reconcileListedThreadState(state: AppState, listed: Thread[]): AppState {
  // thread/list carries live threads only; archived entries live in renderer
  // state and must survive the merge (Settings → Archive reads them).
  const archived = state.threads.filter((thread) => thread.archived);
  const threads = mergeListedThreads(state.threads, [...listed, ...archived]);
  const listedByID = new Map(threads.map((thread) => [thread.id, thread]));
  const thread = state.thread
    ? (listedByID.get(state.thread.id) ?? state.thread)
    : undefined;
  const secondaryThread = state.secondaryThread
    ? (listedByID.get(state.secondaryThread.id) ?? state.secondaryThread)
    : undefined;

  releaseNewlySettledTurnStreams(state.thread, thread);
  releaseNewlySettledTurnStreams(state.secondaryThread, secondaryThread);

  const reconciled = { ...state, threads, thread, secondaryThread };
  const wasRunning = state.running || isThreadRunning(activeThreadForState(state));
  const running = isThreadRunning(activeThreadForState(reconciled));
  return {
    ...reconciled,
    running,
    status: wasRunning && !running ? "ready" : state.status,
  };
}

function releaseNewlySettledTurnStreams(
  previous: Thread | undefined,
  next: Thread | undefined,
): void {
  if (!previous || !next) {
    return;
  }
  const previousByID = new Map(previous.turns.map((turn) => [turn.id, turn]));
  for (const turn of next.turns) {
    if (
      turn.status !== "in_progress" &&
      previousByID.get(turn.id)?.status === "in_progress"
    ) {
      releaseSettledTurnStreams(turn);
    }
  }
}

export function mergeSidebarThread(existing: Thread, incoming: Thread): Thread {
  return mergeListedThread(existing, incoming);
}

function conversationSearchThreadMeta(thread: Thread): string {
  const updatedAt = threadTime(thread);
  const timeLabel =
    updatedAt > 0 ? conversationSearchTimeLabel(updatedAt) : t("appState.unknownTime");
  return thread.pinned
    ? t("appState.pinnedTime", { time: timeLabel })
    : timeLabel;
}

function conversationSearchTimeLabel(atMs: number, nowMs = Date.now()): string {
  const elapsedMs = Math.max(0, nowMs - atMs);
  if (elapsedMs < 60_000) {
    return t("time.justNow");
  }
  if (elapsedMs < 60 * 60_000) {
    const minutes = Math.floor(elapsedMs / 60_000);
    return t(minutes === 1 ? "time.minuteAgo" : "time.minutesAgo", {
      count: formatCurrentNumber(minutes),
    });
  }

  const date = new Date(atMs);
  const now = new Date(nowMs);
  if (sameCalendarDay(date, now)) {
    return t("appState.todayAt", { time: formatHourMinute(date) });
  }

  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameCalendarDay(date, yesterday)) {
    return t("appState.yesterdayAt", { time: formatHourMinute(date) });
  }

  if (date.getFullYear() === now.getFullYear()) {
    return formatCurrentDate(date, { month: "short", day: "numeric" });
  }
  return formatCurrentDate(date, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function sameCalendarDay(left: Date, right: Date): boolean {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  );
}

function formatHourMinute(date: Date): string {
  return formatCurrentDate(date, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

// R4: threads that don't belong to any registered project are almost
// always no-project scratch conversations, whose cwd is an internal
// ~/.wuu/scratch/<date> directory (see allocateNoProjectCwd in
// src/main/projects.ts). Falling back to that directory's basename used to
// surface the raw date-stamped folder name in the search result's context
// label — a wuu implementation detail nobody asked to see. "无项目" reads
// the same way the sidebar's scratch group already does.
function conversationSearchContextLabel(
  thread: Thread,
  projects: DesktopProject[],
): string {
  const projectPath = threadProjectPath(thread);
  const project = projects.find((candidate) => candidate.path === projectPath);
  return project?.name ?? t("appState.noProject");
}

function pinnedThreads(threads: Thread[]): Thread[] {
  return sortThreads(threads).filter((thread) => thread.pinned && !thread.archived);
}

function pinnedThreadSummaries(threads: ThreadSummary[]): ThreadSummary[] {
  return sortThreadSummaries(threads).filter((thread) => thread.pinned);
}

function projectThreads(threads: Thread[]): Thread[] {
  return sortThreads(threads).filter((thread) => !thread.pinned && !thread.archived);
}

function projectThreadSummaries(threads: ThreadSummary[]): ThreadSummary[] {
  return sortThreadSummaries(threads).filter((thread) => !thread.pinned);
}

export function threadProjectPath(
  thread: Pick<Thread, "cwd" | "worktree">,
): string {
  return thread.worktree?.base_repo?.trim() || thread.cwd;
}

export function threadBelongsToProject(
  thread: Pick<Thread, "cwd" | "workspace_id" | "worktree">,
  project: Pick<DesktopProject, "id" | "path">,
): boolean {
  if (thread.workspace_id?.trim()) {
    return thread.workspace_id === project.id;
  }
  return sameDesktopPath(threadProjectPath(thread), project.path);
}

function threadBelongsToAnyProject(
  thread: Pick<Thread, "cwd" | "workspace_id" | "worktree">,
  projects: Pick<DesktopProject, "id" | "path">[],
): boolean {
  return projects.some((project) => threadBelongsToProject(thread, project));
}

function sameDesktopPath(left: string, right: string): boolean {
  return cleanDesktopPath(left) === cleanDesktopPath(right);
}

function cleanDesktopPath(path: string): string {
  const trimmed = path.trim();
  const withoutTrailingSlash = trimmed.replace(/\/+$/, "");
  return withoutTrailingSlash || trimmed;
}

// SCRATCH_PSEUDO_PROJECT_ID is the synthetic project id used to render the
// scratch (no-project) conversation group inside the unified sidebar tree.
// Threads whose cwd does not belong to a registered DesktopProject (i.e.
// isScratchThread returns true) are bucketed under this id so the sidebar
// can render them through the same ProjectList code path as real projects.
// The DesktopProject entry carrying this id is built in App.tsx and never
// sent from the app-server — it lives only on the renderer side.
export const SCRATCH_PSEUDO_PROJECT_ID = "__wuu_scratch__";

export function isScratchThread(
  thread: Pick<Thread, "workspace_kind" | "cwd" | "worktree">,
  projects: DesktopProject[],
): boolean {
  if (threadBelongsToAnyProject(thread, projects)) {
    return false;
  }
  if (thread.workspace_kind === "scratch") {
    return true;
  }
  if (thread.workspace_kind === "project") {
    return false;
  }
  const projectPath = threadProjectPath(thread);
  return !projects.some((project) => project.path === projectPath);
}

/**
 * Resolve the RuntimeContext a thread should open under, independent of
 * whichever sidebar group the user clicked it from. Precedence mirrors
 * isScratchThread:
 * a cwd match against a registered project always wins, even when the
 * thread's own workspace_kind says otherwise (e.g. a thread created before
 * a project was registered at that path). Everything else resolves to a
 * no_project context rooted at the thread's own cwd, which is what makes the
 * workspace panel (file tree /
 * terminal / git) follow the thread instead of staying on the previously
 * active project.
 */
export function resolveThreadRuntimeContext(
  thread: Thread,
  projects: DesktopProject[],
): RuntimeContext {
  const project = projects.find((candidate) =>
    threadBelongsToProject(thread, candidate),
  );
  if (project) {
    return { kind: "project", project_id: project.id, cwd: project.path };
  }
  return { kind: "no_project", cwd: thread.cwd };
}

/**
 * Derive the RuntimeContext the workspace panel's file tree, file preview,
 * and terminal should root at. This is ordinarily just the active
 * RuntimeContext, but a worktree-fork thread's own cwd (Thread.cwd) points
 * at a git worktree directory distinct from the project root that
 * resolveThreadRuntimeContext resolves the thread to (threadProjectPath
 * prefers worktree.base_repo, so the *context* stays pinned to the base
 * project while the *thread* itself runs out of the worktree). When the
 * active thread's cwd differs from the active context's cwd, the panel
 * should follow the thread instead — kind/project_id are preserved so
 * labels (project name, etc.) stay stable; only cwd moves.
 *
 * The diff/review panel deliberately does NOT use this: gitStatus is
 * fetched from the app-server bound to activeContext's own workdir, so
 * feeding it the thread's cwd would silently show diff data for the wrong
 * directory. Callers should keep passing activeContext straight through to
 * the diff view.
 */
export function workspacePanelContext(
  activeContext: RuntimeContext | undefined,
  thread: Thread | undefined,
): RuntimeContext | undefined {
  if (!thread || !thread.cwd || thread.cwd === activeContext?.cwd) {
    return activeContext;
  }
  if (!activeContext) {
    return { kind: "no_project", cwd: thread.cwd };
  }
  return { ...activeContext, cwd: thread.cwd };
}

export function scratchThreads(
  threads: Thread[],
  projects: DesktopProject[],
): Thread[] {
  return sortThreads(threads).filter(
    (thread) =>
      !thread.pinned &&
      !thread.archived &&
      isScratchThread(thread, projects),
  );
}

export function scratchThreadSummaries(
  threads: ThreadSummary[],
  projects: DesktopProject[],
): ThreadSummary[] {
  return sortThreadSummaries(threads).filter(
    (thread) =>
      !thread.pinned &&
      isScratchThread(thread, projects),
  );
}

function createDraftSessionTab(
  id: string,
  context: RuntimeContext,
  draft: ComposerDraftState = emptyComposerDraft(),
): SessionTab {
  return {
    id,
    kind: "draft",
    context,
    title: t("tabs.newConversation"),
    prompt: draft.prompt,
    images: draft.images.map((image) => ({ ...image })),
    files: draft.files.map((file) => ({ ...file })),
    createdAt: Date.now(),
  };
}

function createThreadSessionTab(
  thread: Thread,
  context: RuntimeContext,
  draft: ComposerDraftState = emptyComposerDraft(),
): SessionTab {
  return {
    id: threadSessionTabID(thread.id),
    kind: "thread",
    context,
    threadID: thread.id,
    title: threadDisplayTitle(thread),
    prompt: draft.prompt,
    images: draft.images.map((image) => ({ ...image })),
    files: draft.files.map((file) => ({ ...file })),
  };
}

// Thread tab titles are snapshots taken when the tab is created. The live
// label prefers the current thread (sessionTabLabel), but after a workspace
// switch the thread may only exist in the cross-workspace sidebar cache — or
// nowhere in the renderer state yet. Keep the snapshot fresh whenever the app
// learns a newer thread record so background tabs never regress to the stale
// creation-time placeholder (e.g. "未命名对话") until they are clicked.
function syncThreadSessionTabTitle(
  tabs: SessionTab[],
  thread: Thread,
): SessionTab[] {
  const title = threadDisplayTitle(thread);
  let changed = false;
  const next = tabs.map((tab) => {
    if (tab.kind !== "thread" || tab.threadID !== thread.id || tab.title === title) {
      return tab;
    }
    changed = true;
    return { ...tab, title };
  });
  return changed ? next : tabs;
}

function createSkillsSessionTab(context: RuntimeContext): SessionTab {
  return {
    id: skillsSessionTabID(context),
    kind: "skills",
    context,
    title: "skills",
  };
}

function createChannelRoomSessionTab(
  roomID: string,
  title: string,
  context: RuntimeContext,
): Extract<SessionTab, { kind: "channel-room" }> {
  return {
    id: channelRoomSessionTabID(roomID),
    kind: "channel-room",
    context,
    roomID,
    title,
    prompt: "",
    images: [],
    files: [],
  };
}

function createAgentsSessionTab(
  context: RuntimeContext,
): Extract<SessionTab, { kind: "agents" }> {
  return {
    id: "agents",
    kind: "agents",
    context,
    title: "agents",
  };
}

function createTasksSessionTab(
  context: RuntimeContext,
): Extract<SessionTab, { kind: "tasks" }> {
  return {
    id: "tasks",
    kind: "tasks",
    context,
    title: "tasks",
  };
}

function channelRoomSessionTabID(roomID: string): string {
  return `channel-room:${roomID}`;
}

function reconcileChannelRoomSessionTabs(
  state: AppState,
  rooms: ChannelRoom[],
): AppState {
  const roomsByID = new Map(rooms.map((room) => [room.id, room]));
  let changed = false;
  let sessionTabs = state.sessionTabs.reduce<SessionTab[]>((nextTabs, tab) => {
    if (tab.kind !== "channel-room") {
      nextTabs.push(tab);
      return nextTabs;
    }
    const room = roomsByID.get(tab.roomID);
    if (!room) {
      changed = true;
      return nextTabs;
    }
    if (tab.title === room.name) {
      nextTabs.push(tab);
      return nextTabs;
    }
    changed = true;
    nextTabs.push({ ...tab, title: room.name });
    return nextTabs;
  }, []);
  if (!changed) {
    return state;
  }
  if (
    !state.activeSessionTabID ||
    sessionTabs.some((tab) => tab.id === state.activeSessionTabID)
  ) {
    return { ...state, sessionTabs };
  }

  const removedIndex = state.sessionTabs.findIndex(
    (tab) => tab.id === state.activeSessionTabID,
  );
  if (sessionTabs.length === 0 && state.activeContext) {
    sessionTabs = [
      createDraftSessionTab(
        draftSessionTabIDForContext(state.activeContext),
        state.activeContext,
      ),
    ];
  }
  const fallbackTab =
    sessionTabs[Math.min(Math.max(removedIndex, 0), sessionTabs.length - 1)];
  return {
    ...state,
    sessionTabs,
    activeSessionTabID: fallbackTab?.id,
    secondaryThread: undefined,
    activePane: "primary",
    allowThreadAutoActivation: fallbackTab?.kind === "thread",
    running:
      fallbackTab?.kind === "thread"
        ? isThreadRunning(threadForTab(state, fallbackTab.threadID))
        : false,
  };
}

function threadSessionTabID(threadID: string): string {
  return `thread:${threadID}`;
}

function skillsSessionTabID(context: RuntimeContext): string {
  return `skills:${runtimeContextKey(context)}`;
}

function runtimeContextKey(context: RuntimeContext): string {
  return context.kind === "project"
    ? `project:${context.project_id}`
    : `no_project:${context.cwd}`;
}

function draftSessionTabIDForContext(context: RuntimeContext): string {
  return `${INITIAL_DRAFT_SESSION_TAB_ID}:${runtimeContextKey(context)}`;
}

function draftSessionTabForContext(
  tabs: SessionTab[],
  context: RuntimeContext,
): SessionTab | undefined {
  for (let index = tabs.length - 1; index >= 0; index -= 1) {
    const tab = tabs[index];
    if (tab.kind === "draft" && sameRuntimeContext(tab.context, context)) {
      return tab;
    }
  }
  return undefined;
}

function sessionTabForLoadedRuntime(
  tabs: SessionTab[],
  context: RuntimeContext,
  thread: Thread | undefined,
): SessionTab {
  if (thread) {
    return createThreadSessionTab(
      thread,
      context,
      sessionTabDraftForThreadID(tabs, thread.id),
    );
  }
  return (
    draftSessionTabForContext(tabs, context) ??
    createDraftSessionTab(draftSessionTabIDForContext(context), context)
  );
}

function withLoadedRuntimeSessionTab(
  current: AppState,
  loadedState: Partial<AppState>,
): AppState {
  const next = {
    ...current,
    ...loadedState,
  };
  const context = loadedState.activeContext;
  if (!context) {
    return next;
  }
  const tab = sessionTabForLoadedRuntime(
    current.sessionTabs,
    context,
    loadedState.thread,
  );
  return {
    ...next,
    sessionTabs: ensureSessionTab(current.sessionTabs, tab),
    activeSessionTabID: tab.id,
  };
}

function activeSessionTab(state: AppState): SessionTab | undefined {
  return state.sessionTabs.find((tab) => tab.id === state.activeSessionTabID);
}

function ensureSessionTab(tabs: SessionTab[], tab: SessionTab): SessionTab[] {
  const index = tabs.findIndex((candidate) => candidate.id === tab.id);
  if (index < 0) {
    return [...tabs, tab];
  }
  const next = tabs.slice();
  next[index] = { ...tab };
  return next;
}

function openForkThreadAsPrimary(
  state: AppState,
  {
    sourceThread,
    forkThread,
    context,
    sourceDraft,
    splitDrafts,
  }: {
    sourceThread: Thread;
    forkThread: Thread;
    context: RuntimeContext;
    sourceDraft: ComposerDraftState;
    splitDrafts?: Partial<Record<ConversationPaneID, ComposerDraftState>>;
  },
): AppState {
  const source =
    state.secondaryThread?.id === sourceThread.id
      ? state.secondaryThread
      : state.thread?.id === sourceThread.id
        ? state.thread
        : sourceThread;
  let tabs = state.sessionTabs;
  if (state.thread && state.thread.id !== source.id && splitDrafts?.primary) {
    tabs = ensureSessionTab(
      tabs,
      createThreadSessionTab(state.thread, context, splitDrafts.primary),
    );
  }
  if (
    state.secondaryThread &&
    state.secondaryThread.id !== source.id &&
    splitDrafts?.secondary
  ) {
    tabs = ensureSessionTab(
      tabs,
      createThreadSessionTab(
        state.secondaryThread,
        context,
        splitDrafts.secondary,
      ),
    );
  }
  tabs = ensureSessionTab(
    tabs,
    createThreadSessionTab(source, context, sourceDraft),
  );
  const forkTab = createThreadSessionTab(forkThread, context);
  return {
    ...state,
    thread: forkThread,
    secondaryThread: undefined,
    activePane: "primary",
    allowThreadAutoActivation: true,
    sessionTabs: ensureSessionTab(tabs, forkTab),
    activeSessionTabID: forkTab.id,
    threads: upsertThread(upsertThread(state.threads, source), forkThread),
    running: isThreadRunning(forkThread),
    status: "ready",
  };
}

function removeSessionTab(tabs: SessionTab[], tabID: string): SessionTab[] {
  return tabs.filter((tab) => tab.id !== tabID);
}

function persistActiveSessionTabDraft(
  state: AppState,
  draft: ComposerDraftState,
): AppState {
  const activeTabID = state.activeSessionTabID;
  return {
    ...state,
    sessionTabs: state.sessionTabs.map((tab) =>
      tab.id === activeTabID && (tab.kind === "draft" || tab.kind === "thread")
        ? {
            ...tab,
            prompt: draft.prompt,
            images: draft.images.map((image) => ({ ...image })),
            files: draft.files.map((file) => ({ ...file })),
          }
        : tab,
    ),
  };
}

// composerDraftHasContent tells apart a truly blank draft from one the user
// has started typing into (text, an attached image, or a file). Only the
// latter is worth carrying across a context switch.
function composerDraftHasContent(draft: ComposerDraftState): boolean {
  return (
    draft.prompt.trim().length > 0 ||
    draft.images.length > 0 ||
    draft.files.length > 0
  );
}

/**
 * Applies a project/no-project runtime switch while carrying an in-progress
 * draft along with the user instead of stranding it in the tab they're
 * leaving.
 *
 * The hero-project-pill / ProjectPickerMenu let the user retarget a *draft*
 * conversation at a different project (or at no project) before ever
 * sending anything. If they had already typed a prompt (or attached images
 * / files), silently persisting that text back into the old context's
 * draft tab and showing the new context's (usually empty) draft tab reads
 * as "the app ate what I typed" — the content is technically still there,
 * just one tab away, but the user just watched their composer go blank.
 *
 * When the outgoing tab is a draft with content, this carries that content
 * into whichever tab the switch lands on (overwriting that tab's own prior
 * draft) and leaves the old tab's stored draft untouched, rather than
 * persisting the outgoing content into it. When the outgoing tab is a
 * thread (an actual conversation, not a fresh draft) or the draft is empty,
 * this is exactly the previous persistActiveSessionTabDraft +
 * withLoadedRuntimeSessionTab pairing.
 */
function applyLoadedRuntimeWithDraftCarry(
  current: AppState,
  loadedState: Partial<AppState>,
  outgoingDraft: ComposerDraftState,
): AppState {
  const carry =
    activeSessionTab(current)?.kind === "draft" &&
    composerDraftHasContent(outgoingDraft);
  const persisted = carry
    ? current
    : persistActiveSessionTabDraft(current, outgoingDraft);
  const next = withLoadedRuntimeSessionTab(persisted, loadedState);
  if (!carry) {
    return next;
  }
  const targetTabID = next.activeSessionTabID;
  return {
    ...next,
    sessionTabs: next.sessionTabs.map((tab) =>
      tab.id === targetTabID && (tab.kind === "draft" || tab.kind === "thread")
        ? {
            ...tab,
            prompt: outgoingDraft.prompt,
            images: outgoingDraft.images.map((image) => ({ ...image })),
            files: outgoingDraft.files.map((file) => ({ ...file })),
          }
        : tab,
    ),
  };
}

function bindActiveSessionTabToThread(
  tabs: SessionTab[],
  activeTabID: string,
  thread: Thread,
  context: RuntimeContext,
): SessionTab[] {
  const threadTab = createThreadSessionTab(thread, context);
  const existingThreadTab = tabs.find((tab) => tab.id === threadTab.id);
  if (existingThreadTab) {
    return tabs
      .filter((tab) => tab.id !== activeTabID || tab.id === threadTab.id)
      .map((tab) => (tab.id === threadTab.id ? threadTab : tab));
  }
  return tabs.map((tab) => (tab.id === activeTabID ? threadTab : tab));
}

function sessionTabDraftForThread(
  state: AppState,
  threadID: string,
): ComposerDraftState {
  return sessionTabDraftForThreadID(state.sessionTabs, threadID);
}

function sessionTabDraftForThreadID(
  tabs: SessionTab[],
  threadID: string,
): ComposerDraftState {
  const tab = tabs.find(
    (item) => item.kind === "thread" && item.threadID === threadID,
  );
  return tab ? cloneSessionTabDraft(tab) : emptyComposerDraft();
}

function cloneSessionTabDraft(tab: SessionTab): ComposerDraftState {
  if (tab.kind !== "draft" && tab.kind !== "thread") {
    return emptyComposerDraft();
  }
  return {
    prompt: tab.prompt,
    images: tab.images.map((image) => ({ ...image })),
    files: tab.files.map((file) => ({ ...file })),
  };
}

function threadForTab(state: AppState, threadID: string): Thread | undefined {
  if (state.thread?.id === threadID) {
    return state.thread;
  }
  if (state.secondaryThread?.id === threadID) {
    return state.secondaryThread;
  }
  return state.threads.find((thread) => thread.id === threadID);
}

function threadNeedsResumeOnReselect(state: AppState, threadID: string): boolean {
  const thread = threadForTab(state, threadID);
  return !thread || thread.turns.length === 0;
}

// workspaceNameForContext resolves the display name of the workspace a tab is
// bound to: the registered project's name for a project context, or "对话" for
// the shared no-project workspace. A project context whose project has been
// removed/relocated (so it is no longer in state.projects) falls back to the
// cwd basename so the tab still reads as *something* rather than blank.
function workspaceNameForContext(context: RuntimeContext, state: AppState): string {
  if (context.kind === "no_project") {
    return t("sidebar.conversations");
  }
  const project = state.projects.find(
    (candidate) => candidate.id === context.project_id,
  );
  return project?.name || fileNameFromPath(context.cwd) || t("sidebar.project");
}

function sessionTabLabel(tab: SessionTab, state: AppState): string {
  if (tab.kind === "draft") {
    // A draft (unsent) tab is labelled by its workspace — the project name or
    // "对话" — not the typed prompt. Each workspace has at most one draft tab,
    // so the name is unambiguous; once the draft is sent it becomes a thread
    // tab and switches to the conversation title (below).
    return workspaceNameForContext(tab.context, state);
  }
  if (tab.kind === "skills") {
    return t("skills.title");
  }
  if (tab.kind === "agents") {
    return t("channels.agents");
  }
  if (tab.kind === "tasks") {
    return t("channels.tasks");
  }
  if (tab.kind === "channel-room") {
    return tab.title || t("channels.rooms");
  }
  return threadDisplayTitle(
    threadForTab(state, tab.threadID),
    state.threads,
    tab.title || t("search.untitledConversation"),
  );
}

function fileNameFromPath(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
}

function threadTime(thread: Pick<Thread, "updated_at" | "created_at">): number {
  const updatedAt = Date.parse(thread.updated_at);
  if (Number.isFinite(updatedAt)) {
    return updatedAt;
  }
  const createdAt = Date.parse(thread.created_at);
  return Number.isFinite(createdAt) ? createdAt : 0;
}

function requireThread(result: { thread?: Thread }, message: string): Thread {
  if (!isThread(result.thread)) {
    throw new Error(message);
  }
  // A renderer only consumes high-rate deltas for its active workdir. When a
  // running conversation is resumed after a cross-workspace tab switch, the
  // RPC snapshot therefore may be ahead of the StreamTextStore buffer that
  // was last painted before the switch. Seed the buffer from that authoritative
  // in-progress snapshot before rendering or appending newly arriving deltas;
  // otherwise the pane keeps showing the old partial text until item/completed
  // replaces it with the final response.
  syncRunningThreadStreamItems(result.thread);
  return result.thread;
}

function activeThreadForState(state: AppState): Thread | undefined {
  const tab = activeSessionTab(state);
  if (tab) {
    if (tab.kind !== "thread") return undefined;
    return [state.thread, state.secondaryThread, ...state.threads]
      .find((thread) => thread?.id === tab.threadID);
  }
  if (state.activePane === "secondary" && state.secondaryThread) {
    return state.secondaryThread;
  }
  return state.thread;
}

function threadForPane(
  state: AppState,
  pane: ConversationPaneID,
): Thread | undefined {
  return pane === "secondary" ? state.secondaryThread : state.thread;
}

function queryTextsForThread(thread: Thread | undefined): string[] {
  if (!thread) {
    return [];
  }
  const queries: string[] = [];
  for (const turn of thread.turns) {
    for (const item of turn.items) {
      const text = queryTextForUserItem(item);
      if (text) {
        queries.push(text);
      }
    }
  }
  return queries;
}

function requestedHandoffIntentForThread(thread: Thread | undefined): string | undefined {
  if (!thread) {
    return undefined;
  }
  for (let turnIndex = thread.turns.length - 1; turnIndex >= 0; turnIndex -= 1) {
    const items = thread.turns[turnIndex]?.items ?? [];
    for (let itemIndex = items.length - 1; itemIndex >= 0; itemIndex -= 1) {
      const item = items[itemIndex];
      if (item.type !== "tool_call" || item.status !== "completed") {
        continue;
      }
      const payload = item.result ?? item.text ?? "";
      const intent = parseRequestedHandoffIntent(payload);
      if (intent !== null) {
        return intent;
      }
    }
  }
  return undefined;
}

function queryTextForUserItem(item: ThreadItem): string | undefined {
  if (item.type !== "user_message") {
    return undefined;
  }
  // Process notifications are internal runtime events, not query bubbles.
  if (isInternalUserNotificationItem(item)) {
    return undefined;
  }
  const text = (item.text ?? "").trim();
  if (!text) {
    return undefined;
  }
  return text;
}

function activeThreadIDForState(state: AppState): string | undefined {
  return activeThreadForState(state)?.id;
}

function latestTodoUpdateForThread(
  thread: Thread | undefined,
): TodoUpdate | undefined {
  if (!thread) {
    return undefined;
  }
  for (let turnIndex = thread.turns.length - 1; turnIndex >= 0; turnIndex--) {
    const turn = thread.turns[turnIndex];
    for (let itemIndex = turn.items.length - 1; itemIndex >= 0; itemIndex--) {
      const item = turn.items[itemIndex];
      if (item.display?.capability !== "todo" || !item.arguments) {
        continue;
      }
      const update = parseTodoUpdateArguments(item.arguments);
      if (update) {
        return update;
      }
    }
  }
  return undefined;
}

// Gates `latestTodoUpdateForThread` on the thread still running a turn.
// The floating "跳到最新" pill cluster's progress chip should track only a
// TODO list that is actively in flight — once the turn completes, the message
// flow's own completed-turn action row takes over that vertical space, and
// a stale TODO chip would sit on top of it. `latestTodoUpdateForThread`
// itself stays turn-status-agnostic: the environment side panel (opened
// explicitly by the user) intentionally keeps showing the most recent TODO list
// as a completed checklist after the turn finishes.
function activeTodoUpdateForThread(
  thread: Thread | undefined,
): TodoUpdate | undefined {
  if (!isThreadRunning(thread)) {
    return undefined;
  }
  return latestTodoUpdateForThread(thread);
}

function parseTodoUpdateArguments(
  argumentsJSON: string,
): TodoUpdate | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsJSON);
  } catch {
    return undefined;
  }
  if (!isRecord(parsed) || !Array.isArray(parsed.todos)) {
    return undefined;
  }
  const todos = parsed.todos
    .map((raw): TodoUpdate["todos"][number] | undefined => {
      if (!isRecord(raw)) {
        return undefined;
      }
      const content = stringValue(raw, "content")?.trim();
      const status = stringValue(raw, "status");
      if (
        !content ||
        (status !== "pending" &&
          status !== "in_progress" &&
          status !== "completed")
      ) {
        return undefined;
      }
      return { content, status };
    })
    .filter((item): item is TodoUpdate["todos"][number] => Boolean(item));
  if (todos.length === 0) {
    return undefined;
  }
  const explanation = stringValue(parsed, "explanation")?.trim();
  return explanation ? { explanation, todos } : { todos };
}

function setThreadForPane(
  state: AppState,
  pane: ConversationPaneID,
  thread: Thread | undefined,
): AppState {
  if (pane === "secondary") {
    return { ...state, secondaryThread: thread };
  }
  return { ...state, thread };
}

function activeProjectID(
  context: RuntimeContext | undefined,
): string | undefined {
  return context?.kind === "project" ? context.project_id : undefined;
}

function sameRuntimeContext(
  left: RuntimeContext | undefined,
  right: RuntimeContext | undefined,
): boolean {
  if (!left || !right || left.kind !== right.kind) {
    return false;
  }
  if (left.kind === "project" && right.kind === "project") {
    return left.project_id === right.project_id;
  }
  return left.cwd === right.cwd;
}

function withExtensionInventoryForContext(
  state: AppState,
  sourceContext: RuntimeContext | undefined,
  extensionInventory: ExtensionInventoryRecord[],
): AppState {
  if (!state.initialized || !sameRuntimeContext(state.activeContext, sourceContext)) {
    return state;
  }
  return {
    ...state,
    initialized: {
      ...state.initialized,
      extension_inventory: extensionInventory,
    },
  };
}

function threadMatchesActiveContext(
  thread: Thread,
  context: RuntimeContext | undefined,
): boolean {
  return Boolean(context && threadProjectPath(thread) === context.cwd);
}

function isThread(value: unknown): value is Thread {
  return Boolean(
    value &&
    typeof value === "object" &&
    typeof (value as Thread).id === "string",
  );
}

function isThreadRunning(
  thread: ThreadRunningCandidate | undefined,
): boolean {
  return Boolean(
    thread?.status === "in_progress" ||
    thread?.turns?.some((turn) => turn.status === "in_progress"),
  );
}

function isThreadExecuting(
  thread: ThreadRunningCandidate | undefined,
): boolean {
  return Boolean(
    isThreadRunning(thread) ||
    thread?.child_agents?.some(agentRunning),
  );
}

export function agentRunning(
  agent: Pick<Agent, "status" | "nested_running_count">,
): boolean {
  if ((agent.nested_running_count ?? 0) > 0) {
    return true;
  }
  switch (agent.status.trim().toLowerCase()) {
    case "pending":
    case "queued":
    case "running":
      return true;
    default:
      return false;
  }
}

function activeTurnForThread<T extends Pick<Turn, "status">>(
  thread: { turns?: T[] } | undefined,
): T | undefined {
  const turns = thread?.turns;
  if (!turns) {
    return undefined;
  }
  for (let index = turns.length - 1; index >= 0; index -= 1) {
    const turn = turns[index];
    if (turn.status === "in_progress") {
      return turn;
    }
  }
  return undefined;
}

function turnIsAnswerReady(
  turn:
    | Partial<Pick<Turn, "answer_ready_at" | "items">>
    | undefined,
): boolean {
  return Boolean(
    turn?.answer_ready_at ||
      turn?.items?.some(
        (item) =>
          item.type === "agent_message" &&
          item.status === "completed" &&
          item.terminal === true,
      ),
  );
}

function activeTurnIsAnswerReady(
  thread: { turns?: TurnAnswerReadyCandidate[] } | undefined,
): boolean {
  return turnIsAnswerReady(activeTurnForThread(thread));
}

function isThreadPresentationRunning(
  thread: ThreadRunningCandidate | undefined,
  aggregateRunning = false,
): boolean {
  // The terminal answer is the user-visible completion boundary. Runtime
  // cleanup can briefly leave the turn or child agents marked as executing,
  // but presentation surfaces must stop showing activity at this point.
  return !activeTurnIsAnswerReady(thread) && (isThreadExecuting(thread) || aggregateRunning);
}

function latestCompletedTurnID(thread: {
  latest_completed_turn_id?: string;
  turns: Array<Pick<Turn, "id" | "status" | "answer_ready_at">>;
}): string | undefined {
  // Walk newest → oldest. Answer-ready is the user-visible completion
  // boundary, so an in_progress tail with a terminal answer still counts.
  // A still-running tail without that boundary stays unread-deferred, even
  // if a stale latest_completed_turn_id is sitting on the thread.
  for (let index = thread.turns.length - 1; index >= 0; index -= 1) {
    const turn = thread.turns[index];
    if (turn.status === "in_progress" && !turnIsAnswerReady(turn)) {
      return undefined;
    }
    return turn.id;
  }
  return thread.latest_completed_turn_id;
}

function isThreadUnread(
  thread: (ThreadRunningCandidate & {
    latest_completed_turn_id?: string;
    turns: Array<Pick<Turn, "id" | "status" | "answer_ready_at">>;
  }) | undefined,
  lastViewedTurnID: string | undefined,
): boolean {
  if (!thread) return false;
  const lastTurnID = latestCompletedTurnID(thread);
  if (!lastTurnID) return false;
  if (!lastViewedTurnID) return true;
  return lastTurnID !== lastViewedTurnID;
}

function markThreadTurnsViewed(
  state: AppState,
  threadID: string,
): AppState {
  const thread = threadForTab(state, threadID);
  if (!thread) return state;
  if (isThreadPresentationRunning(thread)) return state;
  const lastTurnID = latestCompletedTurnID(thread);
  if (lastTurnID && state.lastViewedTurnByThreadID[threadID] !== lastTurnID) {
    return {
      ...state,
      lastViewedTurnByThreadID: {
        ...state.lastViewedTurnByThreadID,
        [threadID]: lastTurnID,
      },
    };
  }
  return state;
}

function markThreadSummariesViewed(
  state: AppState,
  threads: readonly ThreadSummary[],
): AppState {
  let nextLastViewed = state.lastViewedTurnByThreadID;
  for (const thread of threads) {
    if (isThreadPresentationRunning(thread)) continue;
    const lastTurnID = latestCompletedTurnID(thread);
    if (!lastTurnID || nextLastViewed[thread.id] === lastTurnID) continue;
    if (nextLastViewed === state.lastViewedTurnByThreadID) {
      nextLastViewed = { ...nextLastViewed };
    }
    nextLastViewed[thread.id] = lastTurnID;
  }
  return nextLastViewed === state.lastViewedTurnByThreadID
    ? state
    : { ...state, lastViewedTurnByThreadID: nextLastViewed };
}

function activeTurnIDForThread(thread: Thread | undefined): string | undefined {
  return activeTurnForThread(thread)?.id;
}

type ComposerRunningAction = "queue" | "steer";

function resolveComposerRunningAction(
  requestedAction: ComposerRunningAction,
  thread: Thread | undefined,
): ComposerRunningAction {
  return requestedAction === "steer" && activeTurnAcceptsSteering(thread)
    ? "steer"
    : "queue";
}

function activeTurnAcceptsSteering(thread: Thread | undefined): boolean {
  const turn = activeTurnForThread(thread);
  return Boolean(
    turn &&
      !turn.items.some(
        (item) =>
          item.type === "agent_message" &&
          item.status === "completed" &&
          item.terminal === true,
      ),
  );
}

function presentationRunningThreadIDs(
  threads: readonly (Thread | undefined)[],
  runningThreadIDs: ReadonlySet<string>,
): ReadonlySet<string> {
  const visible = new Set(runningThreadIDs);
  for (const thread of threads) {
    if (thread && activeTurnIsAnswerReady(thread)) {
      visible.delete(thread.id);
    }
  }
  return visible;
}

function turnStreamStatusForThread(
  state: AppState,
  thread: Thread | undefined,
): TurnStreamStatus | undefined {
  const turnID = activeTurnIDForThread(thread);
  return turnID ? state.turnStreamStatus[turnID] : undefined;
}

function isStateActiveThreadRunning(state: AppState): boolean {
  return Boolean(state.running || isThreadRunning(activeThreadForState(state)));
}

function isAnyThreadRunning(state: AppState): boolean {
  return Boolean(
    state.running ||
    isThreadExecuting(state.thread) ||
    isThreadExecuting(state.secondaryThread) ||
    state.threads.some(isThreadExecuting),
  );
}

function upsertTurn(thread: Thread, turn: Turn): Thread {
  const index = thread.turns.findIndex((item) => item.id === turn.id);
  const status = turn.status === "in_progress" ? "in_progress" : "idle";
  if (index < 0) {
    return threadWithTurnSummary(
      {
        ...thread,
        turns: [...thread.turns, { ...turn, items: orderedTurnItems(turn.items) }],
        status,
      },
      turn,
    );
  }
  const turns = thread.turns.slice();
  turns[index] = { ...turn, items: mergeTurnItemsInOrder(turns[index], turn) };
  return threadWithTurnSummary({ ...thread, turns, status }, turn);
}

function threadWithTurnSummary(thread: Thread, turn: Turn): Thread {
  const preview = hasText(thread.preview) ? thread.preview : turnPreview(turn);
  return {
    ...thread,
    preview,
    latest_completed_turn_id:
      turn.status === "in_progress"
        ? thread.latest_completed_turn_id
        : turn.id,
    updated_at: laterTimestamp(
      thread.updated_at,
      turn.completed_at ?? turn.started_at,
    ),
  };
}

function turnPreview(turn: Turn): string {
  const userItem = turn.items.find((item) => item.type === "user_message");
  if (!userItem) {
    return "";
  }
  const text = userItem.text?.trim();
  if (text) {
    return text;
  }
  const images = userItem.images ?? [];
  if (images.length === 1) {
    return localizedText("appState.imageNumber", { number: 1 });
  }
  if (images.length > 1) {
    return localizedText("appState.images", { count: images.length });
  }
  const files = userItem.files ?? [];
  if (files.length === 1) {
    const name = files[0].filename?.trim();
    if (!name) {
      return localizedText("appState.fileNumber", { number: 1 });
    }
    return localizedText("appState.fileNamed", {
      name,
    });
  }
  if (files.length > 1) {
    return localizedText("appState.files", { count: files.length });
  }
  return "";
}

function hasText(value: string): boolean {
  return value.trim() !== "";
}

function laterTimestamp(
  current: string,
  candidate: string | null | undefined,
): string {
  if (!candidate) {
    return current;
  }
  const currentTime = Date.parse(current);
  const candidateTime = Date.parse(candidate);
  if (!Number.isFinite(candidateTime)) {
    return current;
  }
  return !Number.isFinite(currentTime) || candidateTime > currentTime
    ? candidate
    : current;
}

function updateTurnItem(
  thread: Thread,
  turnID: string,
  itemID: string,
  update: (item: ThreadItem) => ThreadItem,
): Thread {
  const turns = thread.turns.map((turn) => {
    if (turn.id !== turnID) {
      return turn;
    }
    const index = turn.items.findIndex((item) => item.id === itemID);
    if (index < 0) {
      return turn;
    }
    const items = turn.items.slice();
    items[index] = update(items[index]);
    return { ...turn, items };
  });
  return { ...thread, turns };
}

function upsertTurnItem(
  thread: Thread,
  turnID: string,
  item: ThreadItem,
): Thread {
  const turns = thread.turns.map((turn) => {
    if (turn.id !== turnID) {
      return turn;
    }
    return { ...turn, items: upsertTurnItemInOrder(turn, item) };
  });
  return { ...thread, turns };
}

function markTurnAnswerReady(
  thread: Thread,
  turnID: string,
  answerReadyAt: string,
): Thread {
  return {
    ...thread,
    turns: thread.turns.map((turn) =>
      turn.id === turnID && !turn.answer_ready_at
        ? { ...turn, answer_ready_at: answerReadyAt }
        : turn,
    ),
  };
}

function removeTurnItem(thread: Thread, turnID: string, itemID: string): Thread {
  const turns = thread.turns.map((turn) =>
    turn.id === turnID
      ? { ...turn, items: turn.items.filter((item) => item.id !== itemID) }
      : turn,
  );
  return { ...thread, turns };
}

function upsertThreadChildAgent(thread: Thread, agent: Agent): Thread {
  const current = thread.child_agents ?? [];
  const index = current.findIndex((item) => item.id === agent.id);
  const nextAgent = mergeAgentSummary(
    index >= 0 ? current[index] : undefined,
    agent,
  );
  const next = current.slice();
  if (index < 0) {
    next.push(nextAgent);
  } else {
    next[index] = nextAgent;
  }
  return { ...thread, child_agents: sortChildAgents(next) };
}

function mergeAgentSummary(current: Agent | undefined, incoming: Agent): Agent {
  if (!current) {
    return incoming;
  }
  return {
    ...current,
    ...incoming,
    nested_count: incoming.nested_count ?? current.nested_count,
    nested_running_count:
      incoming.nested_running_count ?? current.nested_running_count,
    started_at: incoming.started_at ?? current.started_at,
    completed_at: incoming.completed_at ?? current.completed_at,
  };
}

function threadItemFromRecord(
  record: JsonRecord | undefined,
): ThreadItem | undefined {
  if (
    !record ||
    typeof record.id !== "string" ||
    typeof record.type !== "string"
  ) {
    return undefined;
  }
  return record as ThreadItem;
}

function turnFromRecord(record: JsonRecord | undefined): Turn | undefined {
  if (!record || typeof record.id !== "string") {
    return undefined;
  }
  const items = record.items;
  if (items !== undefined && items !== null && !Array.isArray(items)) {
    return undefined;
  }
  return {
    ...record,
    // Older cores emitted null before a turn's first item. Treat absent and
    // null collections as empty at this untrusted protocol boundary so one
    // malformed event cannot take down the conversation renderer.
    items: Array.isArray(items) ? items : [],
  } as Turn;
}

function threadFromRecord(record: JsonRecord | undefined): Thread | undefined {
  if (
    !record ||
    typeof record.id !== "string" ||
    !Array.isArray(record.turns)
  ) {
    return undefined;
  }
  const turns: Turn[] = [];
  for (const value of record.turns) {
    if (!isRecord(value)) {
      return undefined;
    }
    const turn = turnFromRecord(value);
    if (!turn) {
      return undefined;
    }
    turns.push(turn);
  }
  return { ...record, turns } as Thread;
}

function agentFromRecord(record: JsonRecord | undefined): Agent | undefined {
  const id = stringValue(record, "id");
  const status = stringValue(record, "status");
  if (!id || !status) {
    return undefined;
  }
  return {
    id,
    type: stringValue(record, "type"),
    task_name: stringValue(record, "task_name"),
    agent_profile: stringValue(record, "agent_profile"),
    agent_path: stringValue(record, "agent_path"),
    parent_id: stringValue(record, "parent_id"),
    description: stringValue(record, "description"),
    status,
    result: stringValue(record, "result"),
    error: stringValue(record, "error"),
    input_tokens: numberValue(record, "input_tokens"),
    output_tokens: numberValue(record, "output_tokens"),
    cache_creation_tokens: numberValue(record, "cache_creation_tokens"),
    cache_read_tokens: numberValue(record, "cache_read_tokens"),
    nested_count: numberValue(record, "nested_count"),
    nested_running_count: numberValue(record, "nested_running_count"),
    started_at: stringValue(record, "started_at"),
    completed_at: stringValue(record, "completed_at"),
  };
}

function isDirectChildAgent(threadID: string, agent: Agent): boolean {
  if (agent.parent_id === threadID) {
    return true;
  }
  return agentPathDepth(agent.agent_path) === 2;
}

function agentPathDepth(path: string | undefined): number {
  const trimmed = path?.trim().replace(/^\/+|\/+$/g, "") ?? "";
  return trimmed ? trimmed.split("/").length : 0;
}

function appendTurnTokenSample(
  state: AppState,
  turnID: string,
  threadID: string,
  inputTokens: number,
  outputTokens: number,
  cacheCreationTokens: number,
  cacheReadTokens: number,
  at: number,
  contextWindowTokens: number = 0,
  model?: string,
  contextTokens: number = 0,
): AppState {
  const turnTokenUsage = state.turnTokenUsage ?? {};
  const previous = turnTokenUsage[turnID];
  const cutoff = at - TOKEN_SPEED_WINDOW_MS;
  const hasRealHistory = previous?.speedSource === "real";
  const previousSpeedTokens = hasRealHistory ? previous.speedTokens : 0;
  const speedTokens = hasRealHistory
    ? Math.max(previousSpeedTokens, outputTokens)
    : outputTokens;
  const outputIncreased = outputTokens > previousSpeedTokens;
  const shouldAppendSample = outputTokens > 0 && outputIncreased;
  const samples: TurnTokenSample[] = [];
  if (hasRealHistory) {
    for (const sample of previous.samples) {
      if (!shouldAppendSample || sample.at >= cutoff) {
        samples.push(sample);
      }
    }
  }
  if (shouldAppendSample) {
    samples.push({ tokens: speedTokens, at });
  }
  // A real usage snapshot is the authoritative source for both the speed
  // samples and the context ceiling. If Go omitted the ceiling (zero
  // or undefined) we still want the meter to keep showing the last known
  // value rather than collapse to "unknown" — most providers emit usage
  // and ceiling together, but a transient omission should not erase state.
  const resolvedWindow =
    contextWindowTokens && contextWindowTokens > 0
      ? contextWindowTokens
      : previous?.contextWindowTokens;
  const resolvedModel = model || previous?.model;
  const resolvedContextTokens =
    contextTokens && contextTokens > 0 ? contextTokens : previous?.contextTokens;
  return {
    ...state,
    turnTokenUsage: withRetainedTurnTelemetry<TurnTokenUsage>(
      turnTokenUsage,
      turnID,
      {
        threadID,
        inputTokens,
        outputTokens,
        cacheCreationTokens,
        cacheReadTokens,
        speedTokens,
        speedSource: "real",
        samples,
        ...(resolvedContextTokens
          ? { contextTokens: resolvedContextTokens }
          : {}),
        ...(resolvedWindow ? { contextWindowTokens: resolvedWindow } : {}),
        ...(resolvedModel ? { model: resolvedModel } : {}),
      },
    ),
  };
}

function appendStreamingTokenSample(
  state: AppState,
  params: Record<string, unknown> | undefined,
  at: number,
): AppState {
  const turnID = stringValue(params, "turn_id");
  const delta = stringValue(params, "delta");
  if (!turnID || !delta) {
    return state;
  }
  const estimatedTokens = estimateStreamingOutputTokens(delta);
  if (estimatedTokens <= 0) {
    return state;
  }
  const threadID = stringValue(params, "thread_id") ?? "";
  return appendEstimatedTokenSample(state, turnID, threadID, estimatedTokens, at);
}

/**
 * Deltas arrive far faster than the token-speed gauge needs samples, and a
 * setState per delta re-renders the whole App tree. Callers accumulate the
 * per-turn estimates in a plain Map and flush them into state on a coarse
 * timer instead.
 */
export type PendingStreamingTokenSamples = Map<
  string,
  { threadID: string; tokens: number }
>;

/** Accumulate a delta's estimated tokens; returns true when it recorded any. */
function recordPendingStreamingTokenSample(
  pending: PendingStreamingTokenSamples,
  params: Record<string, unknown> | undefined,
): boolean {
  const turnID = stringValue(params, "turn_id");
  const delta = stringValue(params, "delta");
  if (!turnID || !delta) {
    return false;
  }
  const estimatedTokens = estimateStreamingOutputTokens(delta);
  if (estimatedTokens <= 0) {
    return false;
  }
  const threadID = stringValue(params, "thread_id") ?? "";
  const existing = pending.get(turnID);
  if (existing) {
    existing.tokens += estimatedTokens;
    if (!existing.threadID) {
      existing.threadID = threadID;
    }
    return true;
  }
  pending.set(turnID, { threadID, tokens: estimatedTokens });
  return true;
}

function flushPendingStreamingTokenSamples(
  state: AppState,
  pending: PendingStreamingTokenSamples,
  at: number,
): AppState {
  let next = state;
  for (const [turnID, sample] of pending) {
    next = appendEstimatedTokenSample(
      next,
      turnID,
      sample.threadID,
      sample.tokens,
      at,
    );
  }
  return next;
}

function appendEstimatedTokenSample(
  state: AppState,
  turnID: string,
  threadID: string,
  estimatedTokens: number,
  at: number,
): AppState {
  const turnTokenUsage = state.turnTokenUsage ?? {};
  const previous = turnTokenUsage[turnID];
  const cutoff = at - TOKEN_SPEED_WINDOW_MS;
  const samples: TurnTokenSample[] = [];
  if (previous) {
    for (const sample of previous.samples) {
      if (sample.at >= cutoff) {
        samples.push(sample);
      }
    }
  }
  const speedTokens =
    (previous?.speedTokens ?? previous?.outputTokens ?? 0) + estimatedTokens;
  samples.push({ tokens: speedTokens, at });
  return {
    ...state,
    turnTokenUsage: withRetainedTurnTelemetry<TurnTokenUsage>(
      turnTokenUsage,
      turnID,
      {
        threadID: previous?.threadID || threadID,
        inputTokens: previous?.inputTokens ?? 0,
        outputTokens: previous?.outputTokens ?? 0,
        cacheCreationTokens: previous?.cacheCreationTokens ?? 0,
        cacheReadTokens: previous?.cacheReadTokens ?? 0,
        speedTokens,
        speedSource: "estimated",
        samples,
        ...(previous?.contextTokens
          ? { contextTokens: previous.contextTokens }
          : {}),
        // Streaming estimates are byte-deltas, not real usage; they do not
        // know the context window. Carry the previously-resolved window
        // forward so the meter stays visible while we wait for the next
        // real provider usage snapshot.
        ...(previous?.contextWindowTokens
          ? { contextWindowTokens: previous.contextWindowTokens }
          : {}),
        ...(previous?.model ? { model: previous.model } : {}),
      },
    ),
  };
}

function withRetainedTurnTelemetry<T>(
  record: Readonly<Record<string, T>>,
  turnID: string,
  value: T,
): Record<string, T> {
  const retainedTurnIDs = Object.keys(record)
    .filter((candidate) => candidate !== turnID)
    .slice(-(RETAINED_TURN_TELEMETRY_LIMIT - 1));
  const next: Record<string, T> = {};
  for (const retainedTurnID of retainedTurnIDs) {
    next[retainedTurnID] = record[retainedTurnID];
  }
  next[turnID] = value;
  return next;
}

function activeTurnTokenSpeed(state: AppState, turnID?: string): number {
  return activeTurnTokenSpeedSnapshot(state, turnID).tokensPerSecond;
}

function activeTurnTokenSpeedSnapshot(
  state: AppState,
  turnID?: string,
): TurnTokenSpeedSnapshot {
  if (!turnID) {
    return { tokensPerSecond: 0, source: "none" };
  }
  const usage = state.turnTokenUsage?.[turnID];
  if (!usage || usage.samples.length < 2) {
    return {
      tokensPerSecond: 0,
      source: usage?.speedSource ?? "none",
      sampledAt: usage?.samples.at(-1)?.at,
    };
  }
  const first = usage.samples[0];
  const last = usage.samples[usage.samples.length - 1];
  const delta = last.tokens - first.tokens;
  const elapsed = last.at - first.at;
  if (elapsed <= 0 || delta <= 0) {
    return {
      tokensPerSecond: 0,
      source: usage.speedSource,
      sampledAt: last.at,
    };
  }
  return {
    tokensPerSecond: (delta / elapsed) * 1000,
    source: usage.speedSource,
    sampledAt: last.at,
  };
}

function activeTurnContextUsage(
  state: AppState,
  turnID?: string,
): TurnContextUsage | undefined {
  if (!turnID) {
    return undefined;
  }
  const usage = state.turnTokenUsage?.[turnID];
  if (
    !usage?.contextWindowTokens ||
    usage.contextWindowTokens <= 0 ||
    !usage.contextTokens ||
    usage.contextTokens <= 0
  ) {
    return undefined;
  }
  return {
    turnID,
    used: usage.contextTokens,
    window: usage.contextWindowTokens,
    inputTokens: usage.inputTokens,
    cacheCreationTokens: usage.cacheCreationTokens,
    cacheReadTokens: usage.cacheReadTokens,
    requestContext: state.turnRequestContext?.[turnID],
  };
}

// latestContextUsageForThread walks the thread's turns from newest to
// oldest and returns the most recent retained-context estimate with a
// known context ceiling. Raw provider input/cache usage is intentionally
// ignored here: it can include request-only tool context that should not
// be shown as current conversation occupancy.
//
// When no real usage is available, falls back to the current runtime
// ceiling even before a thread exists, so the meter renders at 0% from
// the moment a model/provider with a trusted limit is picked.
// Returns undefined only when the model has no known ceiling — we'd
// rather hide the meter than mislead the user with a guessed size.
function latestContextUsageForThread(
  state: AppState,
  thread: Thread | undefined,
  fallback: ContextUsageFallback = {},
): TurnContextUsage | undefined {
  const model = thread?.model || fallback.model || "";
  const currentModel = normalizeModelID(model);
  if (thread) {
    for (let i = thread.turns.length - 1; i >= 0; i -= 1) {
      const turn = thread.turns[i];
      const usage = state.turnTokenUsage?.[turn.id];
      if (
        !usage?.contextWindowTokens ||
        usage.contextWindowTokens <= 0 ||
        !usage.contextTokens ||
        usage.contextTokens <= 0
      ) {
        const turnUsageModel = turn.usage_model;
        if (
          turnUsageModel &&
          currentModel &&
          normalizeModelID(turnUsageModel) !== currentModel
        ) {
          continue;
        }
        const contextTokens = turn.context_tokens ?? 0;
        if (contextTokens <= 0) {
          continue;
        }
        const inputTokens = turn.input_tokens ?? 0;
        const cacheCreationTokens = turn.cache_creation_tokens ?? 0;
        const cacheReadTokens = turn.cache_read_tokens ?? 0;
        const turnWindow =
          fallback.contextWindowTokens && fallback.contextWindowTokens > 0
            ? fallback.contextWindowTokens
            : undefined;
        if (!turnWindow) {
          continue;
        }
        return {
          turnID: turn.id,
          used: contextTokens,
          window: turnWindow,
          inputTokens,
          cacheCreationTokens,
          cacheReadTokens,
          requestContext: state.turnRequestContext?.[turn.id],
        };
      }
      if (
        usage.model &&
        currentModel &&
        normalizeModelID(usage.model) !== currentModel
      ) {
        continue;
      }
      return {
        turnID: turn.id,
        used: usage.contextTokens,
        window: usage.contextWindowTokens,
        inputTokens: usage.inputTokens,
        cacheCreationTokens: usage.cacheCreationTokens,
        cacheReadTokens: usage.cacheReadTokens,
        requestContext: state.turnRequestContext?.[turn.id],
      };
    }
  }
  const fallbackWindow =
    fallback.contextWindowTokens && fallback.contextWindowTokens > 0
      ? fallback.contextWindowTokens
      : undefined;
  if (!fallbackWindow) {
    return undefined;
  }
  return {
    turnID: "",
    used: 0,
    window: fallbackWindow,
    inputTokens: 0,
    cacheCreationTokens: 0,
    cacheReadTokens: 0,
  };
}

function normalizeModelID(model: string | undefined): string {
  return (model ?? "").trim().toLowerCase();
}

export {
  activeTodoUpdateForThread,
  activeProjectID,
  activeSessionTab,
  activeThreadForState,
  activeThreadIDForState,
  activeTurnContextUsage,
  latestContextUsageForThread,
  activeTurnForThread,
  activeTurnIDForThread,
  activeTurnTokenSpeed,
  activeTurnTokenSpeedSnapshot,
  turnStreamStatusForThread,
  agentFromRecord,
  applyLoadedRuntimeWithDraftCarry,
  appendStreamingTokenSample,
  appendTurnTokenSample,
  flushPendingStreamingTokenSamples,
  recordPendingStreamingTokenSample,
  bindActiveSessionTabToThread,
  cloneComposerDraft,
  cloneSessionTabDraft,
  composerDraftHasContent,
  conversationPaneThreadsByID,
  conversationSearchContextLabel,
  conversationSearchThreadMeta,
  channelRoomSessionTabID,
  createAgentsSessionTab,
  createChannelRoomSessionTab,
  createDraftSessionTab,
  createSkillsSessionTab,
  createTasksSessionTab,
  createThreadSessionTab,
  draftSessionTabForContext,
  draftSessionTabIDForContext,
  emptyComposerDraft,
  ensureSessionTab,
  handleStreamingNotification,
  syncRunningThreadStreamItems,
  hasText,
  initialSplitComposerDrafts,
  initialState,
  isAnyThreadRunning,
  isStateActiveThreadRunning,
  isThreadExecuting,
  isThreadPresentationRunning,
  isThreadRunning,
  isThreadUnread,
  latestCompletedTurnID,
  latestTodoUpdateForThread,
  markThreadSummariesViewed,
  markThreadTurnsViewed,
  mergeAgentSummary,
  mergeListedThreads,
  reconcileListedThreadState,
  openForkThreadAsPrimary,
  parseTodoUpdateArguments,
  persistActiveSessionTabDraft,
  pinnedThreads,
  pinnedThreadSummaries,
  presentationRunningThreadIDs,
  projectThreads,
  projectThreadSummaries,
  queryTextForUserItem,
  queryTextsForThread,
  requestedHandoffIntentForThread,
  reconcileChannelRoomSessionTabs,
  reduceNotification,
  reduceServerEvent,
  activeTurnAcceptsSteering,
  activeTurnIsAnswerReady,
  turnIsAnswerReady,
  resolveComposerRunningAction,
  removeSessionTab,
  replaceStreamText,
  requireThread,
  runtimeContextKey,
  sameRuntimeContext,
  serverEventShouldRefreshGit,
  serverEventTargetsActiveContext,
  sessionTabDraftForThread,
  sessionTabDraftForThreadID,
  sessionTabForLoadedRuntime,
  sessionTabLabel,
  setThreadForPane,
  skillsSessionTabID,
  sortThreads,
  summarizeProjectThreadsForSidebar,
  summarizeThreadsForSidebar,
  threadItemFromRecord,
  threadForPane,
  threadForTab,
  threadNeedsResumeOnReselect,
  threadFromRecord,
  threadMatchesActiveContext,
  threadSessionTabID,
  threadTime,
  turnFromRecord,
  turnPreview,
  updateThread,
  updateThreadByID,
  updateTurnItem,
  upsertThread,
  upsertThreadChildAgent,
  upsertTurn,
  upsertTurnItem,
  withExtensionInventoryForContext,
  withLoadedRuntimeSessionTab,
};

export type { AppState, ComposerDraftState, ComposerRunningAction, ConversationPaneID, SessionTab };
