// Pure timing helpers. Every frame is a function of time alone, so the preview
// scrubber and the frame-by-frame export always draw identical images.

export type Ease = (t: number) => number;

export const clamp = (t: number, lo = 0, hi = 1) => Math.max(lo, Math.min(hi, t));
export const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
export const rad = (deg: number) => (deg * Math.PI) / 180;
/** Normalised progress of `t` through [a, b]. */
export const seg = (t: number, a: number, b: number) => clamp((t - a) / (b - a));

export const linear: Ease = (t) => t;
export const smooth: Ease = (t) => t * t * (3 - 2 * t);
export const inOut: Ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
export const outCubic: Ease = (t) => 1 - Math.pow(1 - t, 3);
export const inCubic: Ease = (t) => t * t * t;
export const outQuart: Ease = (t) => 1 - Math.pow(1 - t, 4);
export const outBack: Ease = (t) => {
  const c = 1.70158, u = t - 1;
  return 1 + (c + 1) * u * u * u + c * u * u;
};
export const inBack: Ease = (t) => 2.70158 * t * t * t - 1.70158 * t * t;
/** Settles with a couple of soft wobbles; good for things that snap into place. */
export const outElastic: Ease = (t) =>
  t <= 0 ? 0 : t >= 1 ? 1 : Math.pow(2, -9 * t) * Math.sin((t * 10 - 0.75) * ((2 * Math.PI) / 3.2)) + 1;

/**
 * Piecewise keyframes: [[time, value, ease?], ...]. The ease on a key shapes
 * the segment that arrives at it. Values hold before the first and after the
 * last key.
 */
export type Key = [number, number, Ease?];
export function keys(t: number, frames: Key[]): number {
  if (t <= frames[0][0]) return frames[0][1];
  for (let i = 1; i < frames.length; i++) {
    const [t1, v1, ease = inOut] = frames[i];
    if (t <= t1) {
      const [t0, v0] = frames[i - 1];
      return lerp(v0, v1, ease(seg(t, t0, t1)));
    }
  }
  return frames[frames.length - 1][1];
}

export interface Hop { lift: number; sx: number; sy: number }
const REST: Hop = { lift: 0, sx: 1, sy: 1 };

/** Crouch, stretch through the air, squash on touchdown. `lift` is 0..1 of the apex. */
export function hop(t: number, start: number, duration: number): Hop {
  const p = (t - start) / duration;
  if (p <= 0 || p >= 1) return REST;
  const crouch = p < 0.2 ? smooth(p / 0.2) : p < 0.28 ? 1 - smooth((p - 0.2) / 0.08) : 0;
  const air = p < 0.28 || p > 0.8 ? 0 : (p - 0.28) / 0.52;
  const land = p < 0.8 ? 0 : p < 0.9 ? smooth((p - 0.8) / 0.1) : 1 - smooth((p - 0.9) / 0.1);
  const stretch = air > 0 ? Math.sin(air * Math.PI) : 0;
  const sy = 1 - 0.2 * crouch - 0.17 * land + 0.1 * Math.sin(Math.min(1, air * 2.2) * Math.PI) * (1 - air);
  return { lift: stretch, sx: 1 + (1 - sy) * 0.6, sy };
}

/** Combine several hops; only one is expected to be active at a time. */
export function hops(t: number, starts: number[], duration: number): Hop {
  for (const s of starts) if (t > s && t < s + duration) return hop(t, s, duration);
  return REST;
}

/** A single squash impulse, e.g. landing or being clicked. */
export function squash(t: number, at: number, amount = 0.18, duration = 0.45): { sx: number; sy: number } {
  const p = (t - at) / duration;
  if (p <= 0 || p >= 1) return { sx: 1, sy: 1 };
  const k = Math.exp(-5 * p) * Math.cos(p * Math.PI * 3.2);
  return { sx: 1 + amount * 0.7 * k, sy: 1 - amount * k };
}

/** Eyelid openness for a list of blink moments. */
export function blinks(t: number, moments: number[], duration = 0.18): number {
  for (const m of moments) {
    const p = (t - m) / duration;
    if (p > 0 && p < 1) return p < 0.4 ? 1 - 0.92 * smooth(p / 0.4) : 0.08 + 0.92 * smooth((p - 0.4) / 0.6);
  }
  return 1;
}

export type EyeKind = "open" | "happy" | "content" | "flat" | "squeeze" | "dizzy" | "star" | "sleepy" | "wide";
export interface Eyes { kind: EyeKind; open: number; spin?: number }

/**
 * Expression track: [[time, kind], ...]. Each change hides behind a quick
 * blink so faces never cross-fade between shapes.
 */
export function eyes(t: number, track: [number, EyeKind][], blinkAt: number[] = []): Eyes {
  let kind = track[0][1];
  let open = blinks(t, blinkAt);
  for (let i = 0; i < track.length; i++) {
    if (t >= track[i][0]) kind = track[i][1];
    if (i > 0) {
      const d = Math.abs(t - track[i][0]);
      if (d < 0.09) open = Math.min(open, 0.08 + 0.92 * smooth(d / 0.09));
    }
  }
  return { kind, open, spin: t * 7 };
}

/** Deterministic noise in [-1, 1] for jitter that must replay identically. */
export function wobble(t: number, seed: number, speed = 1): number {
  const x = t * speed + seed * 17.13;
  return (Math.sin(x * 2.1) * 0.6 + Math.sin(x * 3.7 + 1.3) * 0.3 + Math.sin(x * 7.3 + 2.1) * 0.1);
}

export function mixColor(a: string, b: string, t: number): string {
  const pa = parse(a), pb = parse(b);
  const c = pa.map((v, i) => Math.round(lerp(v, pb[i], clamp(t))));
  return `rgb(${c[0]},${c[1]},${c[2]})`;
}

function parse(color: string): number[] {
  if (color.startsWith("#")) return [1, 3, 5].map((i) => parseInt(color.slice(i, i + 2), 16));
  return color.match(/\d+/g)!.slice(0, 3).map(Number);
}
