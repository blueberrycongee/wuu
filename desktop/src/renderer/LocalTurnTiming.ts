import type { Turn } from "../shared/protocol";

type Timing = { startedAt: number; elapsed: number; finished: boolean };
// Local waiting time is presentation state, never a replacement for server
// execution timestamps. Aliases share one clock across RPC/event delivery.
const timings = new Map<string, Timing>();

export function beginLocalTurnTiming(turnID: string, now: number): void {
  if (timings.size >= 2000) {
    for (const [id, timing] of timings) {
      if (timing.finished) timings.delete(id);
    }
  }
  timings.set(turnID, { startedAt: now, elapsed: 0, finished: false });
}

export function bindLocalTurnTiming(fromID: string, toID: string): void {
  const timing = timings.get(fromID);
  if (timing) timings.set(toID, timing);
}

export function localTurnTiming(turn: Turn, now = Date.now()): Timing | undefined {
  const timing = timings.get(turn.id);
  if (!timing) return undefined;
  if (!timing.finished) {
    timing.elapsed = Math.max(timing.elapsed, now - timing.startedAt, 0);
    timing.finished = turn.status !== "in_progress" || Boolean(turn.answer_ready_at);
  }
  return timing;
}

export function forgetLocalTurnTiming(turnID: string): void {
  const timing = timings.get(turnID);
  if (!timing) return;
  for (const [id, value] of timings) {
    if (value === timing) timings.delete(id);
  }
}
