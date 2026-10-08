import { showErrorToast } from "./Toast";
import { ChevronRight, CircleAlert, TriangleAlert } from "./WuuIcons";
import { useEffect, useId, useState, useSyncExternalStore, type CSSProperties } from "react";
import type { ThreadItem, ThreadItemStatus, TurnError } from "../shared/protocol";
import { isUnchangedContextCompaction, type TurnEventDisplay } from "./TurnEvents";
import { userFacingErrorForMessage, type UserFacingErrorDisplay, type UserFacingErrorTone } from "./UserFacingErrors";
import { formatCurrentNumber, translateCurrent as t } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import { ProcessSurfaceMascot } from "./ProcessSurface";
import { useLiveTextWave } from "./LiveTextWave";
import { CollapsibleDetails } from "./CollapsibleMotion";
import type { TurnStreamStatus } from "./AppState";
import type { SettingsPage } from "./SettingsView";

/** Window event a conversation notice sends to open Settings on a page. */
export const OPEN_SETTINGS_EVENT = "wuu:open-settings";
export type OpenSettingsDetail = { page: SettingsPage };

/**
 * Window event a failure notice sends to continue an interrupted reply. The
 * thread already holds the partial reply, so the text is an ordinary
 * follow-up in that conversation. A handler calls preventDefault to claim
 * the request and reports through done once the message was sent or not.
 */
export const CONTINUE_TURN_EVENT = "wuu:continue-turn";
export type ContinueTurnDetail = { threadID: string; text: string; done: (sent: boolean) => void };

export type SystemEventDisplay = {
  label: string;
  detail?: string;
  tone?: UserFacingErrorTone;
  state?: "settled" | "in_progress";
  expandedDetail?: string;
};

export function SystemEventNotice({
  event,
  className,
}: {
  event: SystemEventDisplay;
  className?: string;
}): JSX.Element {
  const tone = event.tone ?? "neutral";
  const inProgress = event.state === "in_progress";
  const [expanded, setExpanded] = useState(false);
  const description = event.detail
    ? `${event.label} — ${event.detail}`
    : event.label;
  const summaryText = event.detail
    ? `${event.label} · ${event.detail}`
    : event.label;
  const waveRef = useLiveTextWave<HTMLSpanElement>(inProgress);
  const hasExpandedDetail = Boolean(event.expandedDetail);
  return (
    <aside
      className={`turn-notice process-surface system-event-notice${inProgress ? " is-progress" : ""}${className ? ` ${className}` : ""}`}
      role={tone === "error" || tone === "auth" ? "alert" : "status"}
      aria-label={description}
      aria-live={inProgress ? "polite" : undefined}
    >
      <ProcessSurfaceFold
        summary={
          <span className="process-surface-summary-line" aria-label={description}>
            {inProgress ? (
              <ProcessSurfaceMascot active activity="idle" />
            ) : null}
            <span
              ref={waveRef}
              className={`process-surface-summary-text system-event-copy${inProgress ? " wuu-live-text-wave" : ""}`}
              data-text={summaryText}
            >
              <span className="process-surface-segment system-event-title">
                {event.label}
              </span>
              {event.detail ? (
                <>
                  <span className="process-surface-separator">·</span>
                  <span className="process-surface-segment system-event-detail">
                    {event.detail}
                  </span>
                </>
              ) : null}
            </span>
          </span>
        }
        disabled={!hasExpandedDetail}
        open={expanded}
        onToggle={(toggleEvent) => setExpanded(toggleEvent.currentTarget.open)}
        rowClassName={inProgress ? " is-live-gray is-streaming" : ""}
      >
        <div className="system-event-expanded-detail">{event.expandedDetail}</div>
      </ProcessSurfaceFold>
    </aside>
  );
}

export function SystemEventDivider({
  text,
  className,
}: {
  text: string;
  className?: string;
}): JSX.Element {
  return <SystemEventNotice event={{ label: text }} className={className} />;
}

export function TurnEventNotice({
  event,
}: {
  event: TurnEventDisplay;
}): JSX.Element {
  if (event.presentation === "context_compaction") {
    return <ContextCompactionNotice text={event.text} reason={event.reason} status={event.status} summary={event.summary} />;
  }
  return <TurnNotice display={event.notice} />;
}

export function TurnNotice({
  display,
}: {
  display: UserFacingErrorDisplay;
}): JSX.Element {
  return (
    <SystemEventNotice
      event={{
        label: display.title,
        expandedDetail: [display.detail, display.diagnostic].filter(Boolean).join("\n\n"),
        tone: display.tone,
      }}
    />
  );
}

export function StreamStatusNotice({
  status,
}: {
  status: TurnStreamStatus;
}): JSX.Element {
  return (
    <SystemEventNotice
      event={{
        label: status.text,
        state: status.liveProgress ? "in_progress" : "settled",
      }}
      className="stream-status-notice"
    />
  );
}

/**
 * A stream the core is still retrying. Inside a turn, TurnView renders the
 * same item through TurnFailureNotice so the card survives into failure.
 */
export function StreamReconnectNotice({
  item,
}: {
  item: ThreadItem;
}): JSX.Element | null {
  if (item.status !== "in_progress") return null;
  return <TurnFailureNotice reconnect={item} />;
}

type FailureKind = "auth" | "quota" | "model" | "rateLimit" | "unavailable" | "timeout" | "network" | "dropped" | "offline" | "context";

// Human words for the failures people actually meet. Anything else keeps the
// classifier's title and a generic next step.
// A body only carries a step the title and the actions leave out; the
// reader already knows what a rate limit or a dropped connection means.
const FAILURE_COPY: Record<FailureKind, { title: TranslationKey; body?: TranslationKey }> = {
  auth: { title: "turnFailure.auth" },
  quota: { title: "turnFailure.quota" },
  model: { title: "turnFailure.model", body: "turnFailure.modelBody" },
  rateLimit: { title: "turnFailure.rateLimit" },
  unavailable: { title: "turnFailure.unavailable" },
  timeout: { title: "turnFailure.timeout" },
  network: { title: "turnFailure.network", body: "turnFailure.networkBody" },
  dropped: { title: "turnFailure.dropped" },
  offline: { title: "turnFailure.offline" },
  context: { title: "turnFailure.context", body: "turnFailure.contextBody" },
};

// Transport failures that the device being offline explains better.
const OFFLINE_EXPLAINS = new Set<FailureKind | undefined>(["network", "dropped", "timeout", "unavailable"]);

/**
 * Structured recovery facts win: the core names the failure category for
 * both live turns and reconnect items. The HTTP status and the classifier's
 * category cover history recorded before those fields existed.
 */
function failureKind(
  error: TurnError | undefined,
  reason: string | undefined,
  display: UserFacingErrorDisplay | undefined,
  message: string | undefined,
): FailureKind | undefined {
  switch (reason ?? error?.recovery?.failure_category) {
    case "authentication":
      return "auth";
    case "quota":
      return "quota";
    case "rate_limit":
      return "rateLimit";
    case "overloaded":
    case "server":
      return "unavailable";
    case "deadline":
      return "timeout";
    case "network":
      return "network";
    case "incomplete_stream":
      return "dropped";
    case "context_overflow":
      return "context";
  }
  const status = failureStatusCode(error, message);
  if (display?.category === "auth" || status === 401 || status === 403) return "auth";
  if (status === 404 || error?.code === "model_not_found") return "model";
  if (status === 429) return "rateLimit";
  if (status === 408 || status === 504) return "timeout";
  if (status !== undefined && status >= 500) return "unavailable";
  if (display?.category === "network") {
    const lower = (message ?? "").toLowerCase();
    if (/timeout|deadline exceeded/.test(lower)) return "timeout";
    // The connection was up and the reply had started; the address is fine.
    if (/\beof\b|before response\.completed/.test(lower)) return "dropped";
    return "network";
  }
  return undefined;
}

function failureStatusCode(error: TurnError | undefined, message: string | undefined): number | undefined {
  if (typeof error?.status_code === "number" && error.status_code > 0) return Math.trunc(error.status_code);
  const match = message?.match(/\bHTTP (\d{3})\b/);
  return match ? Number(match[1]) : undefined;
}

// The status sits beside the title, so the title keeps only words:
// "400 请求无效" and "请求失败 · HTTP 400" both read without the number.
function titleWithoutStatus(title: string | undefined, status: number | undefined): string {
  if (!title || !status) return title ?? "";
  return title.replace(new RegExp(`^${status}\\s+|\\s*·\\s*HTTP ${status}$`), "").trim();
}

function subscribeOnline(onChange: () => void): () => void {
  window.addEventListener("online", onChange);
  window.addEventListener("offline", onChange);
  return () => {
    window.removeEventListener("online", onChange);
    window.removeEventListener("offline", onChange);
  };
}

/**
 * The turn's answer did not arrive. One row covers the whole recovery: what
 * happened reads on the left, the next step sits in the trailing slot. While
 * the core retries, that slot counts down; when recovery gives up, the same
 * row takes the error tint and the countdown gives its place to the action.
 * The status code, retry count and the provider's own message wait behind
 * Details. Actions come from the caller, which offers them only on the
 * latest turn.
 */
export function TurnFailureNotice({
  display,
  error,
  reconnect,
  arriving = false,
  onRetry,
  onContinue,
  onOpenSettings,
}: {
  /** Absent while a reconnect item is still retrying. */
  display?: UserFacingErrorDisplay;
  error?: TurnError;
  /** The turn's stream_reconnect item, retrying or given up. */
  reconnect?: ThreadItem;
  /** The failure happened in front of the reader rather than in history. */
  arriving?: boolean;
  /** Sends the turn's message again; offered when the turn left nothing. */
  onRetry?: () => void | Promise<void>;
  /** Picks up after the partial reply the turn left, keeping it. */
  onContinue?: () => void | Promise<void>;
  onOpenSettings?: () => void;
}): JSX.Element {
  const retrying = reconnect?.status === "in_progress";
  const countdown = useRetryCountdown(retrying ? reconnect?.retry_at_ms : undefined);
  const online = useSyncExternalStore(subscribeOnline, () => navigator.onLine, () => true);
  const message = error?.message ?? display?.diagnostic ?? (retrying ? reconnect?.text : undefined);
  const classified = failureKind(error, reconnect ? reconnectReason(reconnect) : undefined, display, message);
  const kind = !online && OFFLINE_EXPLAINS.has(classified) ? "offline" : classified;
  const status = failureStatusCode(error, message);
  const title = kind
    ? t(FAILURE_COPY[kind].title)
    : reconnect && (retrying || !error)
      ? reconnectFallbackTitle(reconnect)
      : titleWithoutStatus(display?.title, status) || t("error.requestFailedTitle");
  const retries = error?.recovery?.retry_count ?? reconnect?.retry_count ?? 0;
  const bodyKey = kind ? FAILURE_COPY[kind].body : undefined;
  const body = !retrying && bodyKey ? t(bodyKey) : "";
  const settingsFirst = kind === "auth" || kind === "quota" || kind === "model";
  const openSettings = settingsFirst ? onOpenSettings : undefined;
  // Replaying an oversized conversation fails the same way; the body names
  // the step that helps instead.
  const recover = kind === "context" ? undefined : onContinue ?? onRetry;
  const continues = recover !== undefined && recover === onContinue;
  const [recoverPending, setRecoverPending] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const detailsID = useId();
  const diagnostic = retrying ? "" : message?.trim() ?? "";
  const facts = retrying
    ? []
    : [status && !diagnostic.includes(String(status)) ? `HTTP ${status}` : "", retries > 0 ? t("turnFailure.retried", { count: formatCurrentNumber(retries) }) : ""].filter(Boolean);
  const hasDetails = Boolean(diagnostic) || facts.length > 0;

  async function runRecover(): Promise<void> {
    if (!recover || recoverPending) return;
    setRecoverPending(true);
    try {
      await recover();
    } catch (recoverError) {
      showErrorToast(recoverError);
    } finally {
      setRecoverPending(false);
    }
  }

  const Icon = retrying ? TriangleAlert : CircleAlert;
  return (
    <aside
      className={`turn-notice turn-failure${retrying ? " is-retrying" : ""}${arriving ? " is-arriving" : ""}`}
      role={retrying ? "status" : "alert"}
      aria-label={title}
    >
      <Icon key={retrying ? "retrying" : "failed"} size={16} aria-hidden="true" className="turn-failure-icon" />
      <div className="turn-failure-main">
        <div className="turn-failure-line">
          <p className="turn-failure-message">
            <span className="turn-failure-title">{title}</span>
            {body ? <span className="turn-failure-body">{body}</span> : null}
            {body && hasDetails ? " " : null}
            {hasDetails ? (
              <button
                type="button"
                className="turn-failure-details-toggle"
                aria-expanded={detailsOpen}
                aria-controls={detailsID}
                onClick={() => setDetailsOpen((open) => !open)}
              >
                {t("turnFailure.details")}
                <ChevronRight className="turn-failure-details-chevron icon-xs" aria-hidden="true" />
              </button>
            ) : null}
          </p>
          {retrying ? (
            <div className="turn-failure-recovery">
              <span
                key="countdown"
                className="turn-failure-pill turn-failure-countdown"
                style={{ "--retry-progress": countdown.progress } as CSSProperties}
                role={countdown.waiting ? "progressbar" : undefined}
                aria-label={countdown.text}
                aria-valuemin={countdown.waiting ? 0 : undefined}
                aria-valuemax={countdown.waiting ? 100 : undefined}
                aria-valuenow={countdown.waiting ? Math.round(countdown.progress * 100) : undefined}
              >
                {countdown.text}
              </span>
            </div>
          ) : recover || openSettings ? (
            <div className="turn-failure-recovery">
              {recover ? (
                <button
                  type="button"
                  className="turn-failure-pill turn-failure-action"
                  title={continues ? t("turnFailure.continueHint") : undefined}
                  disabled={recoverPending}
                  onClick={() => void runRecover()}
                >
                  {continues ? t("turnFailure.continue") : t(recoverPending ? "appState.retryNow" : "appState.retryAction")}
                </button>
              ) : null}
              {openSettings ? (
                <button type="button" className="turn-failure-pill turn-failure-action is-primary" onClick={openSettings}>
                  {t("turnFailure.openSettings")}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
        {hasDetails ? (
          <CollapsibleDetails id={detailsID} expanded={detailsOpen} className="turn-failure-details">
            {facts.length > 0 ? <p className="turn-failure-facts">{facts.join(" · ")}</p> : null}
            {diagnostic ? <p className="turn-failure-diagnostic">{diagnostic}</p> : null}
          </CollapsibleDetails>
        ) : null}
      </div>
    </aside>
  );
}

// Reconnect items from app-servers that predate the structured reason carry
// only the redacted cause text.
function reconnectReason(item: ThreadItem): string | undefined {
  if (item.reason) return item.reason;
  const text = (item.text ?? "").toLowerCase();
  if (text.includes("authentication") || text.includes("unauthorized")) return "authentication";
  if (text.includes("rate limit") || text.includes("too many requests")) return "rate_limit";
  if (text.includes("overloaded")) return "overloaded";
  if (text.includes("timeout") || text.includes("deadline")) return "deadline";
  return undefined;
}

// Only failures without their own copy above reach this title.
function reconnectFallbackTitle(item: ThreadItem): string {
  return t(item.reason === "request_too_large" ? "error.requestTooLargeTitle" : "error.requestFailedTitle");
}

function useRetryCountdown(retryAtMs: number | undefined) {
  const [, setTick] = useState(0);
  // The event exposes only the deadline. Measure the visible wait from
  // first receipt, and reset only when a new retry deadline arrives.
  const [schedule, setSchedule] = useState(() => ({ deadline: retryAtMs, start: Date.now() }));
  if (schedule.deadline !== retryAtMs) {
    setSchedule({ deadline: retryAtMs, start: Date.now() });
  }

  useEffect(() => {
    if (retryAtMs === undefined || retryAtMs <= Date.now()) return;
    let timer: number | undefined;
    const update = (): void => {
      setTick((value) => value + 1);
      if (retryAtMs <= Date.now() && timer !== undefined) {
        window.clearInterval(timer);
        timer = undefined;
      }
    };
    timer = window.setInterval(update, 100);
    document.addEventListener("visibilitychange", update);
    return () => {
      if (timer !== undefined) window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, [retryAtMs]);

  const now = Date.now();
  const remainingMs = Math.max(0, (retryAtMs ?? now) - now);
  const duration = Math.max(1, (retryAtMs ?? now) - schedule.start);
  const elapsed = Math.min(duration, Math.max(0, now - schedule.start));
  let text = t("appState.retryNow");
  if (remainingMs > 0) {
    const seconds = Math.max(1, Math.ceil(remainingMs / 1_000));
    // Nearest minute: rounding up would call a 61-second wait "2 minutes"
    // and then drop to "59 seconds" a moment later.
    const minutes = Math.round(remainingMs / 60_000);
    text = remainingMs < 60_000
      ? t(seconds === 1 ? "appState.retrySecond" : "appState.retrySeconds", { count: formatCurrentNumber(seconds) })
      : t(minutes === 1 ? "appState.retryMinute" : "appState.retryMinutes", { count: formatCurrentNumber(minutes) });
  }
  return { text, waiting: remainingMs > 0, progress: elapsed / duration };
}

export function ContextCompactionNotice({
  text,
  reason,
  status,
  summary,
}: {
  text?: string;
  reason?: string;
  status?: ThreadItemStatus;
  /**
   * Replacement context shown in the collapsed detail panel after success.
   * Failures show their diagnostic instead, never an uninstalled summary.
   */
  summary?: string;
}): JSX.Element | null {
  const normalized = normalizeContextCompactionText(text);
  const failed = status === "failed" || isFailedCompactNotice(normalized);
  const inProgress = status === "in_progress";
  const title = inProgress
    ? contextCompactionProgressTitle(text, reason)
    : contextCompactionTitle(text, reason, status);
  const detail = inProgress ? undefined : contextCompactionDetail(text, reason, status);
  const state = failed ? "failed" : inProgress ? "in_progress" : "completed";
  const description = detail ? `${title} — ${detail}` : title;
  // A recognized outcome line is already told by the title and detail; only a
  // replacement summary or an unfamiliar diagnostic is worth unfolding.
  const recognizedOutcome = /^Compacted history$/i.test(normalized) || parseContextCompactionNotice(normalized) !== undefined;
  const expandedDetail = failed ? normalized : summary || (recognizedOutcome ? "" : normalized);
  const hasSummary = !inProgress && Boolean(expandedDetail);
  const [expanded, setExpanded] = useState(false);
  const waveRef = useLiveTextWave<HTMLSpanElement>(inProgress);
  const handleToggle = (
    event: React.SyntheticEvent<HTMLDetailsElement>,
  ): void => {
    setExpanded(event.currentTarget.open);
  };
  if (reason === "context_note" || (!inProgress && isUnchangedContextCompaction(text))) {
    return null;
  }
  return (
    <aside
      className={`process-surface context-compaction-notice ${state}`}
      role={failed ? "alert" : "status"}
      aria-label={description}
      aria-live={inProgress ? "polite" : undefined}
    >
      <ProcessSurfaceFold
        summary={
          <span
            className="process-surface-summary-line"
            aria-label={description}
          >
            {inProgress ? (
              <span className="context-compaction-mascot" aria-hidden="true">
                <ProcessSurfaceMascot active activity="compact" />
              </span>
            ) : null}
            <span
              ref={waveRef}
              className={`process-surface-summary-text context-compaction-copy${inProgress ? " wuu-live-text-wave" : ""}`}
              data-text={title}
            >
              <span className="process-surface-segment context-compaction-title">
                {title}
              </span>
              {detail ? (
                <>
                  <span className="process-surface-separator">·</span>
                  <span className="process-surface-segment context-compaction-detail">
                    {detail}
                  </span>
                </>
              ) : null}
            </span>
          </span>
        }
        disabled={!hasSummary}
        open={expanded}
        onToggle={handleToggle}
        rowClassName={inProgress ? " is-live-gray is-streaming" : ""}
      >
        <div className="context-compaction-summary">{expandedDetail}</div>
      </ProcessSurfaceFold>
    </aside>
  );
}

function contextCompactionProgressTitle(text?: string, reason?: string): string {
  if (isManualCompact(reason, normalizeContextCompactionText(text))) {
    return t("compaction.compacting");
  }
  return t("compaction.autoCompacting");
}

function contextCompactionTitle(
  text?: string,
  reason?: string,
  status?: ThreadItemStatus,
): string {
  const normalized = normalizeContextCompactionText(text);
  if (status === "failed") {
    return t("compaction.failed");
  }
  if (isFailedCompactNotice(normalized)) {
    return t("compaction.failed");
  }
  if (isUnchangedContextCompaction(normalized)) {
    return t("compaction.notNeeded");
  }
  if (isManualCompact(reason, normalized)) {
    return t("compaction.manualComplete");
  }
  return t("compaction.complete");
}

function contextCompactionDetail(
  text?: string,
  reason?: string,
  status?: ThreadItemStatus,
): string {
  const normalized = normalizeContextCompactionText(text);
  if (status === "failed") {
    return t("compaction.failedDetail");
  }
  if (!normalized) {
    return t("compaction.completeDetail");
  }
  if (isFailedCompactNotice(normalized)) {
    return t("compaction.failedDetail");
  }
  if (isUnchangedContextCompaction(normalized)) {
    return t("compaction.notNeededDetail");
  }
  if (/^Compacted history$/i.test(normalized)) {
    return t("compaction.completeDetail");
  }
  const compactNotice = parseContextCompactionNotice(normalized);
  if (compactNotice) {
    return compactNotice;
  }
  // Unknown server diagnostics belong in the fold, not the status line.
  return "";
}

function normalizeContextCompactionText(text?: string): string {
  return (text ?? "").trim().replace(/^[✦*•]\s*/, "");
}

function isFailedCompactNotice(text: string): boolean {
  return /^(?:(?:Manual context compaction|Context compaction|Proactive compact|Context-overflow compact|Compact) failed\b|Fresh context could not be installed\b)/i.test(
    text,
  );
}

function isManualCompact(reason: string | undefined, text: string): boolean {
  return (
    reason === "manual" ||
    /^Manual(?:ly)?\s+(?:context\s+)?compact/i.test(text)
  );
}

function parseContextCompactionNotice(text: string): string | undefined {
  const match = text.match(
    /^(?:Recovered from context overflow\s+[—-]\s+compacted|Manually compacted|Compacted|Started a fresh context window with)\s+history:\s*\d+\s*(?:→|->)\s*\d+\s+messages(?:\s+\(([^)]+)\))?$/i,
  );
  if (!match) {
    return undefined;
  }
  const [, tokenDetail] = match;
  const tokenRange = tokenDetail?.match(
    /^~?([\d.]+[kM]?)\s*(?:→|->)\s*~?([\d.]+[kM]?)\s+tokens$/i,
  );
  if (tokenRange) {
    return `${tokenRange[1]} → ${tokenRange[2]}`;
  }
  return t("compaction.completeDetail");
}
