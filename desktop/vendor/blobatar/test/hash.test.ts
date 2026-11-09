import { describe, expect, test } from "bun:test";
import { normalizeSeed } from "../src/hash";
import { traits } from "../src/traits";
import { blobatar } from "../src/blobatar";

const KEYS = [
  "hue", "head.y", "head.rx", "head.ry", "head.n", "hair", "hair.lift", "hair.pad",
  "eyes", "eye.y", "eye.gap", "eye.r", "eye.squash", "eye.skew", "ears", "ear.r",
  "brows", "brow.gap", "brow.w", "brow.th", "brow.rot", "mouth", "mouth.y", "mouth.w",
];

const vector = (seed: string) => KEYS.map(k => traits(seed)(k));

describe("determinism", () => {
  test("same seed produces identical output", () => {
    expect(blobatar("alain")).toBe(blobatar("alain"));
  });

  test("different seeds produce different output", () => {
    expect(blobatar("alain")).not.toBe(blobatar("bob"));
  });
});

describe("normalization", () => {
  test("case, whitespace and NFC form are equivalent", () => {
    const base = blobatar("Alain@Example.com");
    expect(blobatar("alain@example.com")).toBe(base);
    expect(blobatar("  ALAIN@EXAMPLE.COM  ")).toBe(base);
  });

  test("decomposed and precomposed accents agree", () => {
    // "café": U+00E9 vs "e" + U+0301
    expect(blobatar("café")).toBe(blobatar("café"));
    expect(normalizeSeed("café")).toBe("café");
  });

  test("normalize: false hashes the raw string", () => {
    expect(blobatar("Alain", { normalize: false })).not.toBe(blobatar("alain", { normalize: false }));
  });
});

describe("non-ascii", () => {
  test("handles multi-byte and astral-plane seeds", () => {
    for (const seed of ["日本語", "Ελλάδα", "🦊🐻", "أحمد", "🇫🇷"]) {
      const svg = blobatar(seed);
      expect(svg).toStartWith("<svg");
      expect(svg).not.toContain("NaN");
    }
  });

  test("emoji differing only in the low surrogate are distinguished", () => {
    // U+1F98A and U+1F98B share a high surrogate; hashing UTF-16 units naively
    // still separates these, but hashing UTF-8 bytes must too.
    expect(vector("🦊")).not.toEqual(vector("🦋"));
  });

  test("astral seeds are stable across repeated calls", () => {
    expect(blobatar("🦊🐻")).toBe(blobatar("🦊🐻"));
  });
});

describe("avalanche", () => {
  test("a one-character change redraws most traits", () => {
    const a = vector("alain");
    const b = vector("alaim");
    const moved = a.filter((v, i) => Math.abs(v - b[i]!) > 0.2).length;
    expect(moved).toBeGreaterThan(KEYS.length * 0.6);
  });

  test("sequential seeds do not cluster", () => {
    // The classic failure: user-1..user-100 all coming out near-identical.
    const hues = Array.from({ length: 100 }, (_, i) => traits(`user-${i}`)("hue"));
    const buckets = new Set(hues.map(h => Math.floor(h * 10)));
    expect(buckets.size).toBe(10);
  });
});

describe("trait independence", () => {
  test("adding a new trait key leaves existing keys untouched", () => {
    const before = vector("alain");
    const t = traits("alain");
    t("freckles"); // a hypothetical v1.1 trait
    t.num("freckles.size", 1, 3);
    expect(vector("alain")).toEqual(before);
  });

  test("keys are independent of read order", () => {
    const t = traits("alain");
    const reversed = [...KEYS].reverse().map(k => t(k)).reverse();
    expect(reversed).toEqual(vector("alain"));
  });
});

describe("distribution", () => {
  test("floats stay in range and cover the space", () => {
    const vals = Array.from({ length: 5000 }, (_, i) => traits(`s${i}`)("hue"));
    expect(Math.min(...vals)).toBeGreaterThanOrEqual(0);
    expect(Math.max(...vals)).toBeLessThan(1);
    const mean = vals.reduce((a, b) => a + b) / vals.length;
    expect(mean).toBeCloseTo(0.5, 1);
  });

  test("pick is uniform across options", () => {
    const counts = [0, 0, 0, 0];
    for (let i = 0; i < 4000; i++) counts[traits(`s${i}`).int("eyes", 0, 3)]!++;
    for (const c of counts) expect(c).toBeGreaterThan(800);
  });
});
