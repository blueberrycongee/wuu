/** The document timeline is shared by WAAPI entrances and rAF scroll motion. */
export function messageMotionTime(): number | undefined {
  const time = document.timeline?.currentTime;
  return typeof time === "number" ? time : undefined;
}

const ease = (progress: number): number => {
  const p = Math.max(0, Math.min(1, progress));
  return p * p * (3 - 2 * p);
};

/** A bounded scroll with continuous corrections when the layout target moves. */
export function createMessageScrollMotion(from: number, target: number, maxDuration: number) {
  const duration = Math.max(0, Math.min(maxDuration, 140 + Math.sqrt(Math.abs(target - from)) * 8));
  let startedAt = messageMotionTime();
  let previousTarget = target;
  let settledCorrection = 0;
  const corrections: { delta: number; start: number }[] = [];
  return (now: number, liveTarget: number): { top: number; done: boolean } => {
    startedAt ??= now;
    if (duration === 0) return { top: liveTarget, done: true };
    if (liveTarget !== previousTarget) {
      // Add a zero-position, zero-velocity correction instead of multiplying
      // the new target by progress (which jumps near the end of an entrance).
      corrections.push({ delta: liveTarget - previousTarget, start: now });
      previousTarget = liveTarget;
    }
    const correctionDuration = Math.min(140, duration);
    while (corrections.length && now - corrections[0].start >= correctionDuration) {
      settledCorrection += corrections.shift()!.delta;
    }
    let top = from + (target - from) * ease((now - startedAt) / duration) + settledCorrection;
    for (const correction of corrections) top += correction.delta * ease((now - correction.start) / correctionDuration);
    const done = now - startedAt >= duration && corrections.length === 0;
    return { top: done ? liveTarget : top, done };
  };
}
