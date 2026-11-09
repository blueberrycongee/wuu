import { useEffect, useRef, useState } from "react";
import { formatCompactDuration } from "../shared/workbench";
import { formatCurrentNumber, translateCurrent as t } from "./i18n";

export function useLiveNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) {
      return;
    }
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);

  // `now` is only the render tick. When a session tab is inactive the
  // interval is stopped, so its value can be arbitrarily old when the tab
  // becomes active again. Reading the clock for the value itself avoids one
  // stale frame (for example, showing 1s before jumping to 24s).
  return active ? Date.now() : now;
}

// Last live elapsed value per turn id, frozen when the turn leaves
// in_progress so a paused (interrupted) turn keeps showing how long it had
// been processing instead of dropping the timer to nothing. Kept outside
// the component lifecycle so session-tab unmounts and optimistic-turn id
// swaps do not lose it. Bounded below.
const pausedTurnElapsedMs = new Map<string, number>();
const MAX_PAUSED_TURN_ELAPSED = 1_000;

export function recordPausedTurnElapsed(turnID: string, elapsedMs: number): void {
  if (
    !pausedTurnElapsedMs.has(turnID) &&
    pausedTurnElapsedMs.size >= MAX_PAUSED_TURN_ELAPSED
  ) {
    pausedTurnElapsedMs.clear();
  }
  pausedTurnElapsedMs.set(turnID, elapsedMs);
}

export function pausedTurnElapsed(turnID: string): number | undefined {
  return pausedTurnElapsedMs.get(turnID);
}

export function clearPausedTurnElapsed(turnID: string | undefined): void {
  if (turnID !== undefined) {
    pausedTurnElapsedMs.delete(turnID);
  }
}

// The send flow first renders an optimistic in_progress placeholder under a
// client-minted id and swaps in the real server turn under a different id.
// Move the frozen value across so a turn interrupted before that swap (or
// right after) keeps its elapsed timer instead of losing it with the
// placeholder.
export function transferPausedTurnElapsed(fromTurnID: string, toTurnID: string): void {
  if (fromTurnID === toTurnID) {
    return;
  }
  const value = pausedTurnElapsedMs.get(fromTurnID);
  if (value === undefined) {
    return;
  }
  pausedTurnElapsedMs.delete(fromTurnID);
  pausedTurnElapsedMs.set(toTurnID, value);
}

export function LiveDuration({ startedAtMs }: { startedAtMs: number }): JSX.Element {
  const nodeRef = useRef<HTMLSpanElement | null>(null);

  useEffect(() => {
    const update = (): void => {
      if (nodeRef.current) {
        nodeRef.current.textContent = formatDuration(Math.max(0, Date.now() - startedAtMs));
      }
    };
    update();
    const timer = window.setInterval(update, 1000);
    return () => window.clearInterval(timer);
  }, [startedAtMs]);

  return <span ref={nodeRef}>{formatDuration(Math.max(0, Date.now() - startedAtMs))}</span>;
}

export function formatDuration(ms: number): string {
  return formatCompactDuration(ms);
}

/**
 * Format a millisecond duration as a Chinese-language phrase for product
 * copy. Picks the top two non-zero units and drops trailing zeros. The
 * helper does NOT include "用时" — callers wrap with the surrounding
 * sentence so the same duration portion can be reused in different
 * framings (e.g. "用时 3 秒", "耗时 1 分 30 秒").
 *
 *   3_000       → "3 秒"
 *   90_000      → "1 分 30 秒"
 *   3_900_000   → "1 小时 5 分"
 *   90_000_000  → "1 天 1 小时"
 *
 * Sub-second values round down to "0 秒"; callers handling edge cases
 * explicitly (e.g. "不到 1 秒") should branch on `ms < 1000` themselves.
 */
export function formatChineseDuration(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const days = Math.floor(totalSeconds / 86400);
  const hours = Math.floor((totalSeconds % 86400) / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const parts: string[] = [];
  if (days > 0) {
    parts.push(formatDurationUnit(days, "day"));
    if (hours > 0) parts.push(formatDurationUnit(hours, "hour"));
  } else if (hours > 0) {
    parts.push(formatDurationUnit(hours, "hour"));
    if (minutes > 0) parts.push(formatDurationUnit(minutes, "minute"));
  } else if (minutes > 0) {
    parts.push(formatDurationUnit(minutes, "minute"));
    if (seconds > 0) parts.push(formatDurationUnit(seconds, "second"));
  } else {
    parts.push(formatDurationUnit(seconds, "second"));
  }
  return parts.join(" ");
}

function formatDurationUnit(value: number, unit: "day" | "hour" | "minute" | "second"): string {
  const key = `duration.${unit}${value === 1 ? "One" : ""}` as
    | "duration.day"
    | "duration.dayOne"
    | "duration.hour"
    | "duration.hourOne"
    | "duration.minute"
    | "duration.minuteOne"
    | "duration.second"
    | "duration.secondOne";
  return t(key, { count: formatCurrentNumber(value) });
}
