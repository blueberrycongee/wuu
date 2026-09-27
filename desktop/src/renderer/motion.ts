/* The single JS-side bridge to the motion tokens in styles/base.css.
 * WUU_TEST_MARKER
 * CSS owns the values. JS reads them at call time, so a timeout that waits
 * for a transition always matches the stylesheet, and reduced motion (which
 * zeroes the token ladder) collapses the JS waits with it.
 * Fallbacks only cover environments without real computed styles (jsdom). */
import { useSyncExternalStore } from "react";

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

function reducedMotionMedia(): MediaQueryList | undefined {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return undefined;
  }
  return window.matchMedia(REDUCED_MOTION_QUERY);
}

/**
 * Whether motion should be reduced: the OS setting or the in-app Motion
 * preference (`data-appearance-motion="reduce"`). This is the same pair of
 * sources that sets `--motion-reduced` in base.css, so frame loops, WAAPI,
 * and scroll behavior agree with the stylesheet.
 */
export function prefersReducedMotion(): boolean {
  if (typeof document !== "undefined" && document.documentElement.dataset.appearanceMotion === "reduce") {
    return true;
  }
  return reducedMotionMedia()?.matches ?? false;
}

// Every subscriber shares one media listener and one attribute observer:
// sortable rows and process surfaces subscribe per instance.
const reducedMotionListeners = new Set<(reduced: boolean) => void>();
let reducedMotion = false;
let stopWatchingReducedMotion: (() => void) | undefined;

function watchReducedMotion(): () => void {
  const media = reducedMotionMedia();
  const check = (): void => {
    const next = prefersReducedMotion();
    if (next === reducedMotion) return;
    reducedMotion = next;
    for (const listener of [...reducedMotionListeners]) listener(next);
  };
  media?.addEventListener("change", check);
  const observer = new MutationObserver(check);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-appearance-motion"],
  });
  return () => {
    media?.removeEventListener("change", check);
    observer.disconnect();
  };
}

/**
 * Calls `listener` with the new value whenever `prefersReducedMotion()`
 * changes, from either source. Returns the unsubscribe function.
 */
export function subscribeReducedMotion(listener: (reduced: boolean) => void): () => void {
  if (reducedMotionListeners.size === 0) {
    reducedMotion = prefersReducedMotion();
    stopWatchingReducedMotion = watchReducedMotion();
  }
  // A wrapper per subscription, so one function subscribed twice needs two
  // unsubscribes.
  const entry = (reduced: boolean): void => listener(reduced);
  reducedMotionListeners.add(entry);
  return () => {
    if (!reducedMotionListeners.delete(entry) || reducedMotionListeners.size > 0) return;
    stopWatchingReducedMotion?.();
    stopWatchingReducedMotion = undefined;
  };
}

/** `prefersReducedMotion()` as React state that follows both sources. */
export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribeReducedMotion, prefersReducedMotion, () => false);
}

/** A token's computed text on the root, or "" where styles never resolved. */
function readMotionToken(token: string): string {
  if (
    typeof window === "undefined" ||
    typeof window.getComputedStyle !== "function"
  ) {
    return "";
  }
  return window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(token)
    .trim();
}

export function motionDurationMs(token: string, fallbackMs: number): number {
  const raw = readMotionToken(token);
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
  const raw = readMotionToken(token);
  if (raw === "") {
    return fallback;
  }
  return parseEasing(raw) ?? fallback;
}

/**
 * The CSS text of an easing token, for APIs that take a timing function as
 * a string, such as `Element.animate`. Falls back when the token never
 * resolved (jsdom), like [`motionDurationMs`].
 */
export function motionCurve(token: string, fallback: string): string {
  return readMotionToken(token) || fallback;
}
