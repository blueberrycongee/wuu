import { useEffect, useState } from "react";
import type { ThreadItem, ThreadItemStatus } from "../shared/protocol";
import { isUnchangedContextCompaction, type TurnEventDisplay } from "./TurnEvents";
import type { UserFacingErrorDisplay, UserFacingErrorTone } from "./UserFacingErrors";
import { formatCurrentNumber, translateCurrent as t } from "./i18n";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import { ProcessSurfaceMascot } from "./ProcessSurface";
import { useLiveTextWave } from "./LiveTextWave";
import type { TurnStreamStatus } from "./AppState";

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

export function StreamReconnectNotice({
  item,
}: {
  item: ThreadItem;
}): JSX.Element {
  const inProgress = item.status === "in_progress";
  const failed = item.status === "failed";
  const waitText = useRetryCountdown(inProgress ? item.retry_at_ms : undefined);
  // Settled failure rows must read as over ("已停止 · cause"), never as a
  // frozen snapshot of the retry loop ("第 n/m 次重试").
  const detail = failed
    ? streamReconnectTitle(item)
    : [streamReconnectRetryText(item), waitText].filter(Boolean).join(" · ") ||
      undefined;
  return (
    <SystemEventNotice
      event={{
        label: failed ? t("error.cancelledTitle") : streamReconnectTitle(item),
        detail,
        tone: failed ? "error" : undefined,
        state: inProgress ? "in_progress" : "settled",
      }}
      className="stream-reconnect-notice"
    />
  );
}

function streamReconnectRetryText(item: ThreadItem): string | undefined {
  const retryCount = item.retry_count ?? 0;
  if (retryCount <= 0) {
    return undefined;
  }
  return t("appState.retryOrdinal", { count: formatCurrentNumber(retryCount) });
}

/**
 * Short localized title for the failure that triggered a stream reconnect.
 * Prefers the item's structured `reason` (the provider's failure category);
 * the redacted cause text is only consulted for app-servers that predate the
 * category field, and anything unmapped reads as a generic request failure.
 */
function streamReconnectTitle(item: ThreadItem): string {
  const fromCategory = streamReconnectCategoryTitle(item.reason);
  if (fromCategory) {
    return fromCategory;
  }
  const reason = (item.text ?? "").toLowerCase();
  if (reason.includes("authentication") || reason.includes("unauthorized")) {
    return t("error.authTitle");
  }
  if (reason.includes("rate limit") || reason.includes("too many requests")) {
    return `429 ${t("error.http429")}`;
  }
  if (reason.includes("overloaded")) {
    return t("error.upstreamOverloaded");
  }
  if (reason.includes("timeout") || reason.includes("deadline")) {
    return t("error.requestTimeout");
  }
  return t("error.requestFailedTitle");
}

function streamReconnectCategoryTitle(
  category: string | undefined,
): string | undefined {
  switch (category) {
    case "authentication":
      return t("error.authTitle");
    case "rate_limit":
    case "quota":
      return `429 ${t("error.http429")}`;
    case "overloaded":
      return t("error.upstreamOverloaded");
    case "server":
      return t("error.providerTitle");
    case "deadline":
      return t("error.requestTimeout");
    case "context_overflow":
      return t("error.contextOverflowTitle");
    case "request_too_large":
      return t("error.requestTooLargeTitle");
    case "network":
    case "incomplete_stream":
      return t("error.networkTitle");
    default:
      return undefined;
  }
}

function useRetryCountdown(retryAtMs: number | undefined): string | undefined {
  const [, setTick] = useState(0);

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
    timer = window.setInterval(update, 1_000);
    document.addEventListener("visibilitychange", update);
    return () => {
      if (timer !== undefined) window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, [retryAtMs]);

  if (retryAtMs === undefined) return undefined;
  const remainingMs = retryAtMs - Date.now();
  if (remainingMs <= 0) return t("appState.retryNow");
  if (remainingMs < 60_000) {
    const seconds = Math.max(1, Math.ceil(remainingMs / 1_000));
    return t(seconds === 1 ? "appState.retrySecond" : "appState.retrySeconds", {
      count: formatCurrentNumber(seconds),
    });
  }
  const minutes = Math.ceil(remainingMs / 60_000);
  return t(minutes === 1 ? "appState.retryMinute" : "appState.retryMinutes", {
    count: formatCurrentNumber(minutes),
  });
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
  const expandedDetail = failed ? normalized : summary || normalized;
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
