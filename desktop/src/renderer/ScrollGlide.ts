/**
 * The submit glide — one decelerating trajectory for every programmatic
 * scroll in the conversation.
 *
 * Instead of a deadline computed from the distance, each frame covers a fixed
 * fraction of the distance still to travel. That shape is what makes a send
 * feel settled rather than flung: the rate is identical for a 40px nudge and a
 * two-screen jump (~96% covered in ~350ms, then a sub-pixel landing), the long
 * tail never whips, and a target that moves mid-flight — streaming output, a
 * composer collapsing, a viewport resize — is adopted by the same trajectory
 * instead of restarting it.
 *
 * The trajectory models a *position* rather than re-deriving the remaining
 * distance from the live layout every frame. The placement compensates a reflow
 * during the React commit, before paint, at a timestamp where no time has
 * elapsed; a step proportional to elapsed time would move nothing there and let
 * the bubble visibly shift until the next frame. Holding the position and
 * re-reading only the target keeps the screen trajectory invariant under
 * reflow.
 */

/** Reference frame for the fixed-ratio integration (60fps). */
export const GLIDE_FRAME_MS = 1000 / 60;
/** Fraction of the remaining distance kept per reference frame (~90% covered
 * in ~230ms). Higher retains more glide; lower lands harder. */
export const GLIDE_RETAIN = 0.85;
/** Land exactly within this distance — a sub-pixel remainder is not motion. */
export const GLIDE_SNAP_PX = 1;
/** A dropped frame catches up instead of teleporting; the cap bounds how much
 * one tick may cover so a hitch reads as a slow frame, not a jump. */
export const GLIDE_MAX_CATCH_UP_FRAMES = 8;
/** Beyond this many viewports the excess is closed in one step and the rest
 * glides: a long jump arrives promptly instead of whipping at the glide rate. */
export const GLIDE_MAX_VIEWPORTS = 2.5;

/** A glide toward the live bottom hands over to following within this many
 * viewports. Streamed output keeps moving that target, and a trajectory that
 * keeps a fraction of the remaining distance only approaches a moving target:
 * without the hand-over the newest line would stay just out of view. */
export const GLIDE_FOLLOW_HANDOFF_VIEWPORTS = 0.1;

export type ScrollGlideStep = {
  /** Position the trajectory has reached this frame. */
  position: number;
  /** The trajectory has landed exactly on its target. */
  done: boolean;
};

export type ScrollGlideOptions = {
  retain?: number;
  snapPx?: number;
  maxViewports?: number;
};

/**
 * One frame of the glide: where a trajectory at `position` has moved to, given
 * an elapsed `frames` count in reference frames and a live `target`. Pure, so
 * the trajectory is testable without a clock or a window.
 */
export function glideStep(
  position: number,
  target: number,
  frames: number,
  viewportSize: number,
  options: ScrollGlideOptions = {},
): ScrollGlideStep {
  const { retain = GLIDE_RETAIN, snapPx = GLIDE_SNAP_PX, maxViewports = GLIDE_MAX_VIEWPORTS } =
    options;
  const reach = viewportSize > 0 && maxViewports > 0 ? maxViewports * viewportSize : 0;
  const remaining = target - position;
  const approach =
    reach > 0 && Math.abs(remaining) > reach
      ? position + Math.sign(remaining) * (Math.abs(remaining) - reach)
      : position;
  const distance = target - approach;
  if (Math.abs(distance) <= snapPx) return { position: target, done: true };
  return { position: approach + distance * (1 - retain ** frames), done: false };
}

/**
 * A glide bound to one motion: it owns the time base and the current position,
 * never the target, so retargeting mid-flight costs nothing and a stalled or
 * hidden viewport resumes at the cruise rate instead of replaying a curve.
 */
export function createScrollGlide({
  maxCatchUpFrames = GLIDE_MAX_CATCH_UP_FRAMES,
  ...options
}: ScrollGlideOptions & { maxCatchUpFrames?: number } = {}) {
  let position = 0;
  let lastTick: number | undefined;
  return {
    /** Begin the trajectory at `at`, optionally continuing an existing clock. */
    start(at: number, now?: number): void {
      position = at;
      lastTick = now;
    },
    /**
     * Advance one frame toward a live target. The first step after a start
     * counts as a single reference frame, so a glide always begins at the
     * designed rate rather than at whatever time elapsed since it was armed.
     */
    step(now: number, target: number, viewportSize = 0): ScrollGlideStep {
      const frames =
        lastTick === undefined
          ? 1
          : Math.min(Math.max((now - lastTick) / GLIDE_FRAME_MS, 0), maxCatchUpFrames);
      lastTick = now;
      const next = glideStep(position, target, frames, viewportSize, options);
      position = next.position;
      return next;
    },
  };
}
