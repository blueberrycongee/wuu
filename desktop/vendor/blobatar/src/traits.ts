import { seedState, stream } from "./hash";

/**
 * A trait reader. Every value is addressed by a string key rather than drawn
 * from a sequential stream, so trait keys are an append-only namespace:
 * introducing `t.num("freckles.size", ...)` in a later version leaves every
 * other trait — and therefore every existing blobatar — untouched.
 *
 * The one thing that is NOT free to change is the contents of a `pick` array,
 * since option index is part of the mapping. Those are frozen per major.
 */
export interface Traits {
  /** Uniform float in [0, 1). */
  (key: string): number;
  /** Uniform float in [min, max). */
  num(key: string, min: number, max: number): number;
  /** Uniform integer in [min, max]. */
  int(key: string, min: number, max: number): number;
  /** Uniform choice. Appending to `options` remaps existing seeds — frozen per major. */
  pick<T>(key: string, options: readonly T[]): T;
  /** True with probability `p`. */
  bool(key: string, p?: number): boolean;
  /** Symmetric jitter in [-amount, amount). */
  jitter(key: string, amount: number): number;
}

/**
 * Fixed values for individual traits, keyed exactly as the layout reads them —
 * `{ "eye.gap": 0.82 }`.
 *
 * Every value is the position in [0, 1) that the hash would otherwise have
 * produced, which is what makes this a complete configuration surface rather
 * than a set of escape hatches: the ranges, the derived clamps and the
 * categorical thresholds all live in the layout, so an override is read in the
 * same units, in the same place, under the same guarantees as a hashed value.
 * There is nothing a seed can express that an override cannot, and nothing an
 * override can express that a seed could not have.
 *
 * The corollary is that this is the *only* configuration seam. The layout
 * function itself stays private: its return shape, its containment arithmetic
 * and its per-shape decoration would all become public API, and a caller who
 * replaced it would be one arithmetic slip away from eyes outside the body.
 * Overriding the input keeps every invariant in `styles/blob.ts` running.
 *
 * Sparse by design. Keys you omit still come from the seed, so
 * `{ shape: 0.95 }` means "always a sun, everything else per seed".
 *
 * Trait keys are an append-only namespace (see `Traits` above), so an override
 * map keeps meaning what it meant. The numeric *ranges* those keys are read
 * into are what a stated position is relative to, which makes them part of the
 * same frozen-per-major contract as a `pick` array's contents — retuning
 * `t.num("eye.gap", 0.1, 0.24)` moves every blobatar, seeded or configured.
 */
export type TraitOverrides = Record<string, number>;

/**
 * Overrides are clamped rather than trusted.
 *
 * `t.pick` and `t.int` index and floor, so a value of exactly 1 selects one
 * past the end of a `pick` array and one past `max` — `undefined` options and
 * out-of-range counts, from an input that looks entirely reasonable to whoever
 * typed it. NaN falls to 0 through the same comparison, so a bad parse renders
 * a blobatar instead of a stack of `NaN`s in the path data.
 */
export function traits(
  seed: string,
  normalize = true,
  overrides?: TraitOverrides,
): Traits {
  const state = seedState(seed, normalize);
  // Checked per read rather than merged up front: the map is usually absent and
  // usually tiny when present, and a lookup miss costs less than materializing
  // 30-odd hashed values a layout may never ask for.
  const t = ((key: string) => {
    const o = overrides?.[key];
    // Not `??` on the whole expression: an override of 0 is a legitimate value
    // — it is the bottom of every range — and must not fall through to the hash.
    // The clamp is inlined rather than named because a helper survives
    // minification as a binding, and this is the one branch every trait read in
    // the library goes through.
    return o === undefined ? stream(state, key) : o > 0 ? (o < 1 ? o : 0.999999) : 0;
  }) as Traits;

  t.num = (key, min, max) => min + t(key) * (max - min);
  t.int = (key, min, max) => min + Math.floor(t(key) * (max - min + 1));
  t.pick = (key, options) => options[Math.floor(t(key) * options.length)]!;
  t.bool = (key, p = 0.5) => t(key) < p;
  t.jitter = (key, amount) => (t(key) * 2 - 1) * amount;

  return t;
}
