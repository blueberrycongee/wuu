import { describe, expect, test } from "bun:test";
import { _layout } from "../src/blobatar";
import { blobatar } from "../src/blobatar";
import { superellipse, blobPath } from "../src/shape";
import * as blob from "../src/styles/blob";
import { traits } from "../src/traits";
import { BLOB_KEYS } from "./keys";
import { SHAPES } from "../src/styles/silhouette";

/**
 * These are the checks that replace eyeballing the grid one cell at a time.
 * Staring at 400 blobatars tells you whether the ranges are *tasteful*; these
 * tell you whether any seed in the space is outright broken — an eye off the
 * cheek, a body clipped by the frame, two capsules fused into one.
 */

const SEEDS = Array.from({ length: 6000 }, (_, i) => `seed-${i}`);

/** Signed containment: <= 1 is inside the superellipse. */
const inside = (
  px: number,
  py: number,
  s: { cx: number; cy: number; rx: number; ry: number; n: number },
) => Math.pow(Math.abs((px - s.cx) / s.rx), s.n) + Math.pow(Math.abs((py - s.cy) / s.ry), s.n);

/** The four corners of a rotated box — a conservative hull for a capsule. */
function corners(e: { cx: number; cy: number; rx: number; ry: number; rot: number }) {
  const t = (e.rot * Math.PI) / 180;
  const c = Math.cos(t);
  const s = Math.sin(t);
  return [
    [1, 1],
    [1, -1],
    [-1, 1],
    [-1, -1],
  ].map(([sx, sy]) => [
    e.cx + sx! * e.rx * c - sy! * e.ry * s,
    e.cy + sx! * e.rx * s + sy! * e.ry * c,
  ]);
}

describe("the frame", () => {
  test("all geometry stays inside the viewBox", () => {
    for (const s of SEEDS) {
      const svg = blobatar(s, { background: false });
      for (const m of svg.matchAll(/ d="([^"]+)"/g)) {
        for (const n of m[1]!.match(/-?\d+\.?\d*/g)!.map(Number)) {
          expect(n).toBeGreaterThanOrEqual(0);
          expect(n).toBeLessThanOrEqual(100);
        }
      }
    }
  });
});

describe("blob", () => {
  const layouts = SEEDS.map(s => blob.layout(traits(s)));

  test("compact Wuu eyes keep their crisp capsule footprint", () => {
    const l = _layout("wuu", { traits: { shape: 0.1 } });
    for (const eye of l.eyes) {
      expect(eye.rx / l.body.rx).toBeGreaterThan(0.07);
      expect(eye.rx / l.body.rx).toBeLessThan(0.115);
      expect(eye.ry / eye.rx).toBeGreaterThanOrEqual(1.9);
      expect(eye.ry / eye.rx).toBeLessThanOrEqual(2.9);
    }
  });

  test("eyes sit inside the body core", () => {
    for (const l of layouts) {
      const core = {
        cx: l.body.cx,
        cy: l.body.cy,
        rx: l.body.rx,
        ry: l.body.ry,
        n: 2,
      };
      for (const e of l.eyes) {
        for (const [x, y] of corners(e)) expect(inside(x!, y!, core)).toBeLessThan(1);
      }
    }
  });

  test("eyes never fuse into each other", () => {
    for (const l of layouts) {
      const [a, b] = l.eyes as [(typeof l.eyes)[0], (typeof l.eyes)[0]];
      // Separating axis on x is conservative: clearing it proves no overlap.
      const reach = (e: typeof a) => {
        const t = (e.rot * Math.PI) / 180;
        return Math.abs(e.rx * Math.cos(t)) + Math.abs(e.ry * Math.sin(t));
      };
      expect(Math.abs(b.cx - a.cx)).toBeGreaterThan(reach(a) + reach(b));
    }
  });

  test("every shape in the vocabulary is reachable", () => {
    const seen = new Set(layouts.map(l => l.shape));
    expect(seen).toEqual(new Set(SHAPES.map(shape => shape.id)));
  });

});

/**
 * The same invariants, under configuration rather than under seeds.
 *
 * This is the test that makes trait overrides safe to expose. Hashing spreads
 * values out, so 6000 seeds sample the interior of the space densely and its
 * corners barely at all — but a caller writing an override map goes straight to
 * the corners, because "biggest eyes, widest gap, roundest body" is the first
 * thing anyone tries. Every combination here is one an editor's sliders can
 * produce in a single drag.
 */
describe("blob under trait overrides", () => {
  /** All extremes, all midpoints, and every single-key extreme against them. */
  const MAPS: Record<string, number>[] = [];
  for (const v of [0, 0.5, 0.999999]) {
    const all: Record<string, number> = {};
    for (const k of BLOB_KEYS) all[k] = v;
    MAPS.push(all);
    // One key pushed to each end while the rest sit together — the pairwise
    // corners that `fit` and the lean bound exist to survive.
    for (const k of BLOB_KEYS) MAPS.push({ ...all, [k]: 0 }, { ...all, [k]: 0.999999 });
  }
  // And a deterministic scatter, for corners no single-key sweep reaches.
  let s = 1;
  for (let i = 0; i < 400; i++) {
    const m: Record<string, number> = {};
    for (const k of BLOB_KEYS) {
      s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
      m[k] = s / 4294967296;
    }
    MAPS.push(m);
  }

  const layouts = MAPS.map(m => blob.layout(traits("cfg", true, m)));

  test("eyes sit inside the body core", () => {
    for (const l of layouts) {
      const core = {
        cx: l.body.cx,
        cy: l.body.cy,
        rx: l.body.rx,
        ry: l.body.ry,
        n: 2,
      };
      for (const e of l.eyes) {
        for (const [x, y] of corners(e)) expect(inside(x!, y!, core)).toBeLessThan(1);
      }
    }
  });

  test("eyes never fuse into each other", () => {
    for (const l of layouts) {
      const [a, b] = l.eyes as [(typeof l.eyes)[0], (typeof l.eyes)[0]];
      const reach = (e: typeof a) => {
        const t = (e.rot * Math.PI) / 180;
        return Math.abs(e.rx * Math.cos(t)) + Math.abs(e.ry * Math.sin(t));
      };
      expect(Math.abs(b.cx - a.cx)).toBeGreaterThan(reach(a) + reach(b));
    }
  });

  test("all geometry stays inside the viewBox", () => {
    for (const m of MAPS) {
      const svg = blobatar("cfg", { traits: m, background: false });
      expect(svg).not.toContain("NaN");
      for (const g of svg.matchAll(/ d="([^"]+)"/g)) {
        for (const n of g[1]!.match(/-?\d+\.?\d*/g)!.map(Number)) {
          expect(n).toBeGreaterThanOrEqual(0);
          expect(n).toBeLessThanOrEqual(100);
        }
      }
    }
  });
});

describe("path emission", () => {
  test("superellipse coordinates stay finite for the whole n range", () => {
    for (let n = 1.6; n <= 8; n += 0.1) {
      const d = superellipse({ cx: 50, cy: 50, rx: 30, ry: 30, n });
      expect(d).not.toContain("NaN");
      for (const v of d.match(/-?\d+(\.\d+)?/g)!) expect(Number.isFinite(+v)).toBe(true);
    }
  });

  test("the 45-degree control constant matches the circle case exactly", () => {
    // n=2 must reproduce the standard 0.5523 kappa, or the derivation is wrong.
    expect(superellipse({ cx: 0, cy: 0, rx: 100, ry: 100, n: 2 })).toContain("55.23");
  });

  test("control points never overshoot the bounding box", () => {
    for (let n = 1.6; n <= 8; n += 0.1) {
      for (const v of superellipse({ cx: 50, cy: 50, rx: 40, ry: 40, n }).match(/-?\d+(\.\d+)?/g)!) {
        expect(+v).toBeGreaterThanOrEqual(9.9);
        expect(+v).toBeLessThanOrEqual(90.1);
      }
    }
  });

  test("blobPath interpolates its vertices exactly", () => {
    // Catmull-Rom passes through its points, which is what makes the radii
    // mean what they say and containment predictable.
    const d = blobPath(50, 50, 20, 20, [1, 1, 1, 1], 0);
    expect(d).toStartWith("M70 50");
    expect(d).toContain("50 70");
    expect(d).toContain("30 50");
  });

  test("blobPath closes and stays within its radii", () => {
    const radii = [1.1, 0.9, 1.05, 0.95, 1.12, 0.88];
    const d = blobPath(50, 50, 20, 20, radii, 0);
    expect(d).toEndWith("Z");
    for (const v of d.match(/-?\d+(\.\d+)?/g)!) {
      expect(+v).toBeGreaterThan(50 - 20 * 1.5);
      expect(+v).toBeLessThan(50 + 20 * 1.5);
    }
  });
});
