import { describe, expect, it } from "vitest";
import {
  createScrollGlide,
  GLIDE_FRAME_MS,
  GLIDE_MAX_CATCH_UP_FRAMES,
  GLIDE_MAX_VIEWPORTS,
  GLIDE_RETAIN,
  GLIDE_SNAP_PX,
} from "./ScrollGlide";

/** Advance a glide toward a fixed destination, one reference frame at a time. */
function approach(distance: number, viewportSize = 0) {
  const glide = createScrollGlide();
  glide.start(0);
  const steps: number[] = [];
  let position = 0;
  for (let frame = 0; frame < 600; frame += 1) {
    const next = glide.step(frame * GLIDE_FRAME_MS, distance, viewportSize);
    steps.push(next.position - position);
    position = next.position;
    if (next.done) return { position, steps, landed: true };
  }
  return { position, steps, landed: false };
}

describe("createScrollGlide", () => {
  it("approaches without reversal or overshoot and lands exactly", () => {
    const { position, steps, landed } = approach(600);
    expect(landed).toBe(true);
    expect(position).toBe(600);
    // The final step is the landing: it closes whatever remained inside the
    // snap distance, so only the gliding steps must be decelerating.
    for (const [index, step] of steps.slice(0, -1).entries()) {
      expect(step).toBeGreaterThan(0);
      if (index > 0) expect(step).toBeLessThan(steps[index - 1] + 1e-9);
    }
    expect(steps[steps.length - 1]).toBeGreaterThan(0);
    expect(steps[steps.length - 1]).toBeLessThanOrEqual(GLIDE_SNAP_PX);
    let covered = 0;
    for (const step of steps) {
      covered += step;
      expect(covered).toBeLessThanOrEqual(600);
    }
  });

  it("lands a short nudge sooner than a long jump without changing the rate", () => {
    const nudge = approach(24);
    const jump = approach(1200);
    expect(nudge.steps.length).toBeLessThan(jump.steps.length);
    // The first frame of both motions covers the same fraction of its own
    // distance: the shape is distance-independent, only the tail is longer.
    expect(nudge.steps[0]).toBeCloseTo(24 * (1 - GLIDE_RETAIN), 6);
    expect(jump.steps[0]).toBeCloseTo(1200 * (1 - GLIDE_RETAIN), 6);
  });

  it("covers the same distance by wall-clock time at any refresh rate", () => {
    // 200ms of motion, sampled as 12 or 24 frames of exactly equal length.
    const run = (frames: number): number => {
      const glide = createScrollGlide();
      glide.start(0);
      let position = 0;
      for (let frame = 0; frame <= frames; frame += 1) {
        position = glide.step((frame * 200) / frames, 1000).position;
      }
      return position;
    };
    expect(run(24)).toBeCloseTo(run(12), 6);
  });

  it("does not step the trajectory when a commit re-anchors it at the same timestamp", () => {
    const glide = createScrollGlide();
    glide.start(0);
    const first = glide.step(0, 1000).position;
    expect(glide.step(0, 1000).position).toBe(first);
  });

  it("bounds a dropped frame instead of teleporting", () => {
    const glide = createScrollGlide();
    glide.start(0);
    const started = glide.step(0, 1000).position;
    const { position, done } = glide.step(10_000, 1000);
    expect(done).toBe(false);
    // A hitch catches up over the capped frame count, never in one jump.
    expect(position - started).toBeLessThanOrEqual(
      (1000 - started) * (1 - GLIDE_RETAIN ** GLIDE_MAX_CATCH_UP_FRAMES) + 1e-9,
    );
    expect(position).toBeGreaterThan(started);
  });

  it("closes the distance beyond its reach in one step and glides the rest", () => {
    const viewportSize = 500;
    const reach = GLIDE_MAX_VIEWPORTS * viewportSize;
    const glide = createScrollGlide();
    glide.start(0);
    const { position, done } = glide.step(0, 10 * viewportSize, viewportSize);
    expect(done).toBe(false);
    expect(position).toBeGreaterThanOrEqual(10 * viewportSize - reach);
    expect(position).toBeLessThan(10 * viewportSize);
  });

  it("adopts a retargeted destination at the current rate instead of replaying the curve", () => {
    const glide = createScrollGlide();
    glide.start(0, 0);
    let position = 0;
    for (let frame = 1; frame <= 6; frame += 1) {
      position = glide.step(frame * GLIDE_FRAME_MS, 900).position;
    }
    // A reflow moves the destination mid-flight: the next frame still covers
    // one reference frame's fraction — of the new distance.
    const retargeted = glide.step(7 * GLIDE_FRAME_MS, 300).position;
    expect(retargeted - position).toBeCloseTo((300 - position) * (1 - GLIDE_RETAIN), 6);
  });

  it("lands a remainder inside the snap distance in a single step", () => {
    const glide = createScrollGlide();
    glide.start(0);
    const near = glide.step(0, GLIDE_SNAP_PX / 2);
    expect(near.done).toBe(true);
    expect(near.position).toBe(GLIDE_SNAP_PX / 2);
    expect(glide.step(GLIDE_FRAME_MS, 0)).toEqual({ position: 0, done: true });
  });
});
