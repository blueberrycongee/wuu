/* The single JS-side bridge to the motion tokens in styles/base.css.
 * WUU_TEST_MARKER
 * CSS owns the values. JS reads them at call time, so a timeout that waits
 * for a transition always matches the stylesheet, and prefers-reduced-motion
 * (which zeroes the token ladder) collapses the JS waits with it.
 * Fallbacks only cover environments without real computed styles (jsdom). */

export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function motionDurationMs(token: string, fallbackMs: number): number {
  if (
    typeof window === "undefined" ||
    typeof window.getComputedStyle !== "function"
  ) {
    return fallbackMs;
  }
  const raw = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(token)
    .trim();
  if (raw === "") {
    return fallbackMs;
  }
  const value = Number.parseFloat(raw);
  if (!Number.isFinite(value)) {
    return fallbackMs;
  }
  if (raw.endsWith("ms")) {
    return value;
  }
  return raw.endsWith("s") ? value * 1000 : value;
}

/**
 * Elapsed time on the document timeline. Frame-driven motion reads this same
 * clock as WAAPI entrances, so a trajectory can hand off to (or take over
 * from) a keyframe animation without a seam — and every sampled step matches
 * the frame the compositor actually presented.
 */
export function messageMotionTime(): number | undefined {
  const time = document.timeline?.currentTime;
  return typeof time === "number" ? time : undefined;
}

export type Easing = (progress: number) => number;

type BezierControls = [number, number, number, number];

/**
 * A CSS `cubic-bezier(x1, y1, x2, y2)` timing function, evaluated exactly:
 * solving x(t) = progress by Newton iteration with a bisection fallback. A
 * CSS transition and a JS-sampled trajectory given the same curve then agree
 * frame for frame, which a hand-rolled polynomial approximation never does.
 */
export function cubicBezier(x1: number, y1: number, x2: number, y2: number): Easing {
  const coefficients = (a: number, b: number): [number, number, number] => {
    const c = 3 * a;
    const second = 3 * (b - a) - c;
    return [1 - c - second, second, c];
  };
  const [ax, bx, cx] = coefficients(x1, x2);
  const [ay, by, cy] = coefficients(y1, y2);
  const sampleX = (t: number): number => ((ax * t + bx) * t + cx) * t;
  const sampleY = (t: number): number => ((ay * t + by) * t + cy) * t;
  const sampleDerivativeX = (t: number): number => (3 * ax * t + 2 * bx) * t + cx;
  return (progress: number): number => {
    if (!(progress > 0)) return 0;
    if (progress >= 1) return 1;
    let t = progress;
    let solved = false;
    for (let attempt = 0; attempt < 8; attempt += 1) {
      const error = sampleX(t) - progress;
      if (Math.abs(error) < 1e-6) {
        solved = true;
        break;
      }
      const derivative = sampleDerivativeX(t);
      if (Math.abs(derivative) < 1e-6) break;
      t -= error / derivative;
    }
    if (!solved) {
      // x(t) is monotonic for a valid CSS curve, so a bracket survives the
      // Newton steps that wandered.
      let low = 0;
      let high = 1;
      for (let attempt = 0; attempt < 32; attempt += 1) {
        const middle = (low + high) / 2;
        if (sampleX(middle) < progress) low = middle;
        else high = middle;
      }
      t = (low + high) / 2;
    }
    const value = sampleY(t);
    // Floating-point solve error can land a hair outside the curve's own
    // bounds; callers map this straight onto opacity and transforms.
    return value < 0 ? 0 : value > 1 ? 1 : value;
  };
}

const NAMED_EASINGS: Record<string, BezierControls> = {
  linear: [0, 0, 1, 1],
  ease: [0.25, 0.1, 0.25, 1],
  "ease-in": [0.42, 0, 1, 1],
  "ease-out": [0, 0, 0.58, 1],
  "ease-in-out": [0.42, 0, 0.58, 1],
};

const easingCache = new Map<string, Easing>();

function parseEasing(value: string): Easing | undefined {
  const cached = easingCache.get(value);
  if (cached) return cached;
  let easing: Easing | undefined;
  const named = NAMED_EASINGS[value];
  if (named) {
    easing = cubicBezier(...named);
  } else {
    const match = /^cubic-bezier\(([^)]*)\)$/.exec(value);
    const controls = match?.[1].split(",").map((part) => Number.parseFloat(part));
    if (controls?.length === 4 && controls.every((control) => Number.isFinite(control))) {
      easing = cubicBezier(controls[0], controls[1], controls[2], controls[3]);
    }
  }
  if (easing) easingCache.set(value, easing);
  return easing;
}

/**
 * The curve a CSS token names, as a callable timing function, so frame-driven
 * motion rides the same cubic-bezier the stylesheet uses. Falls back when the
 * token is missing or the environment never resolved it (jsdom), exactly like
 * [`motionDurationMs`].
 */
export function motionEasing(token: string, fallback: Easing): Easing {
  if (
    typeof window === "undefined" ||
    typeof window.getComputedStyle !== "function"
  ) {
    return fallback;
  }
  const raw = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(token)
    .trim();
  if (raw === "") {
    return fallback;
  }
  return parseEasing(raw) ?? fallback;
}
