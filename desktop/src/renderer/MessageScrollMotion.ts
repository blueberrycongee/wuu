/** The document timeline is shared by WAAPI entrances and rAF scroll motion. */
export function messageMotionTime(): number | undefined {
  const time = document.timeline?.currentTime;
  return typeof time === "number" ? time : undefined;
}

/** One decelerating trajectory. Retargeting preserves position, velocity and deadline. */
export function createMessageScrollMotion(from: number, target: number, maxDuration: number) {
  const duration = Math.max(0, Math.min(maxDuration, 140 + Math.sqrt(Math.abs(target - from)) * 8));
  let startedAt = messageMotionTime();
  let segmentStart = 0;
  let segmentDuration = duration;
  let origin = from;
  let destination = target;
  let velocity = duration > 0 ? 3 * (target - from) / duration : 0;
  const evaluate = (elapsed: number) => {
    const p = Math.max(0, Math.min(1, (elapsed - segmentStart) / segmentDuration));
    const distance = destination - origin;
    const tangent = velocity * segmentDuration;
    // Cubic Hermite interpolation ends at rest. Initially this is cubic ease-out;
    // a new destination replaces the remaining segment, never adds another one.
    return {
      position: origin + distance * p * p * (3 - 2 * p) + tangent * p * (1 - p) ** 2,
      velocity: (6 * distance * p * (1 - p) + tangent * (1 - 4 * p + 3 * p * p)) / segmentDuration,
    };
  };
  return (now: number, liveTarget: number): { position: number; done: boolean } => {
    startedAt ??= now;
    const elapsed = Math.max(0, now - startedAt);
    if (elapsed >= duration) return { position: liveTarget, done: true };
    const current = evaluate(elapsed);
    if (liveTarget !== destination) {
      origin = current.position;
      velocity = current.velocity;
      destination = liveTarget;
      segmentStart = elapsed;
      segmentDuration = duration - elapsed;
    }
    return { position: current.position, done: false };
  };
}
