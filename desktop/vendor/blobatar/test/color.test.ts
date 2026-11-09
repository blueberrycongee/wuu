import { describe, expect, test } from "bun:test";
import {
  FLOORS,
  contrast,
  ensureContrast,
  fromHex,
  TINTS,
  tinted,
  mixHex,
  palette,
  ramp,
} from "../src/color";
import { traits } from "../src/traits";

/** Every hue, at 1° resolution — the guarantee is only worth stating if it holds everywhere. */
const HUES = Array.from({ length: 360 }, (_, i) => i);
describe("the ramp", () => {
  const floors = FLOORS;

  test.each(floors)("%s clears %s at %f:1 across every hue", (fg, bg, min) => {
    for (const h of HUES) {
      const r = ramp(h);
      expect(contrast(r[fg]!, r[bg]!)).toBeGreaterThanOrEqual(min - 1e-9);
    }
  });

  test("the authored ramp clears the floors before enforcement runs", () => {
    // The guarantee is meant to be structural — fixed L/C ramps that are safe
    // at every hue — with ensureContrast as a net, not a load-bearing step.
    // If this ever fails, the ramp constants drifted and need re-authoring.
    for (const h of HUES) {
      const r = ramp(h, false);
      for (const [fg, bg, min] of floors) {
        expect(contrast(r[fg]!, r[bg]!)).toBeGreaterThanOrEqual(min);
      }
    }
  });

  test("every hue resolves to a valid 6-digit hex", () => {
    for (const h of HUES) {
      for (const hex of Object.values(palette(h))) {
        expect(hex).toMatch(/^#[0-9a-f]{6}$/);
      }
    }
  });

  test("hues stay distinguishable rather than collapsing to grey", () => {
    // If chroma reduction were too aggressive the whole wheel would flatten.
    expect(
      new Set(HUES.map((h) => palette(h).head)).size,
    ).toBeGreaterThan(200);
  });

  test("500 real seeds all satisfy the guarantee", () => {
    for (let i = 0; i < 500; i++) {
      const r = ramp(traits(`user-${i}`).num("hue", 0, 360));
      for (const [fg, bg, min] of floors) {
        expect(contrast(r[fg]!, r[bg]!)).toBeGreaterThanOrEqual(min - 1e-9);
      }
    }
  });
});

describe("the palette", () => {
  test("fills the slots the renderer uses", () => {
    expect(Object.keys(palette(0)).sort()).toEqual([
      "bg",
      "eye",
      "head",
    ]);
  });

  const TONES = [0.1, 0.3, 0.5, 0.7, 0.88, 0.97];

  test("eye polarity follows the body across every tone", () => {
    for (const h of HUES) {
      for (const tone of TONES) {
        const r = ramp(h, true, tone);
        // Light body gets dark eyes, dark body gets light eyes. Without the
        // flip the ink swatch would render an invisible face.
        expect(r.eye!.l < 0.5).toBe(r.head!.l >= 0.5);
        expect(contrast(r.eye!, r.head!)).toBeGreaterThanOrEqual(4.5 - 1e-9);
      }
    }
  });

  test("the tone set spans pale to dark", () => {
    const ls = TONES.map((t) => ramp(0, false, t).head!.l);
    expect(Math.min(...ls)).toBeLessThan(0.4);
    expect(Math.max(...ls)).toBeGreaterThan(0.85);
    expect(new Set(ls).size).toBe(TONES.length);
  });

  test("every tone stays visible on a dark host surface", () => {
    // `blob` has no backdrop of its own, so the body lands directly on the page.
    // The ink tone once scored 1.03:1 against a near-black surface and the
    // silhouette disappeared, leaving two eyes floating in the void.
    const ground = { l: 0.145, c: 0, h: 0 }; // ≈ #0a0a0b
    for (const h of HUES) {
      for (const tone of TONES) {
        expect(
          contrast(ramp(h, true, tone).head!, ground),
        ).toBeGreaterThanOrEqual(1.5 - 1e-9);
      }
    }
  });

  test("pale tones survive enforcement rather than being darkened away", () => {
    // The weak body/backdrop floor exists so soft swatches stay soft.
    for (const h of HUES) {
      expect(ramp(h, true, 0.3).head!.l).toBeGreaterThan(0.85);
    }
  });
});

describe("the tinted palettes", () => {
  /**
   * A tint that lands on top of a contrast-checked palette is not itself
   * contrast-checked, and the guarantee is either kept or dropped — quietly
   * half-keeping it is the bad outcome. `mad` moves the whole body toward red,
   * so this is the other half of that decision.
   *
   * Checked across the mix rather than only at its ends, because the ends are
   * not where it would fail: both are authored, and it is the middle of a
   * 240ms transition — where the body has travelled further than the eye — that
   * nobody would ever look at with a contrast checker.
   *
   * Asserted on the serialized hexes, since those are what ship and what a
   * browser reads. `tinted()` holds a slightly wider internal floor so that
   * rounding to a byte per channel cannot land it under the real one.
   *
   * **Every target, not just the red one.** `hot()` became `tinted(…, Tint)` so
   * that a second tinting pose costs its numbers rather than a second copy of
   * the walk — and the whole value of having one walk is lost if only one of the
   * endpoints it is asked for is ever verified. Adding a `Tint` to `TINTS` is
   * what enrolls it here.
   */
  const HEATS = Array.from({ length: 11 }, (_, i) => i / 10);
  const TONES = [0.1, 0.3, 0.5, 0.7, 0.88, 0.97];
  const ground = { l: 0.145, c: 0, h: 0 }; // ≈ #0a0a0b

  test("the eye clears the body at 4.5:1 at every heat, hue, tone and target", () => {
    for (const [name, tint] of TINTS) {
      for (const h of HUES) {
        for (const tone of TONES) {
          const p = palette(h, true, tone);
          const [hotHead, hotEye] = tinted(p.head!, p.eye!, tint);
          for (const t of HEATS) {
            expect(
              contrast(
                fromHex(mixHex(p.eye!, hotEye, t)),
                fromHex(mixHex(p.head!, hotHead, t)),
              ),
              `${name} hue ${h} tone ${tone} heat ${t}`,
            ).toBeGreaterThanOrEqual(4.5);
          }
        }
      }
    }
  });

  test("a tinted body stays visible on a dark host surface", () => {
    // `blob` ships with its backdrop off, so the same floor the ramp enforces
    // applies to everything any tint can turn the body into.
    for (const [name, tint] of TINTS) {
      for (const h of HUES) {
        for (const tone of TONES) {
          const p = palette(h, true, tone);
          const [hotHead] = tinted(p.head!, p.eye!, tint);
          for (const t of HEATS) {
            expect(
              contrast(fromHex(mixHex(p.head!, hotHead, t)), ground),
              `${name} hue ${h} tone ${tone} heat ${t}`,
            ).toBeGreaterThanOrEqual(1.5);
          }
        }
      }
    }
  });

  test("heat 0 is the palette untouched", () => {
    // The morph runs *through* here on the way out, so a tint that does not
    // resolve to exactly the resting colour would leave the blobatar a shade off
    // its own identity every time it stopped being angry.
    for (const [name, tint] of TINTS) {
      for (const h of HUES) {
        const p = palette(h);
        const [hotHead, hotEye] = tinted(p.head!, p.eye!, tint);
        expect(mixHex(p.head!, hotHead, 0), name).toBe(p.head!);
        expect(mixHex(p.eye!, hotEye, 0), name).toBe(p.eye!);
      }
    }
  });

  test("the tone set survives the trip rather than collapsing onto one colour", () => {
    // Half the point of deriving the pair per seed. If every angry blobatar
    // converged on the same colour, the grid would stop reading as a crowd at
    // precisely the moment it is loudest.
    for (const [name, tint] of TINTS) {
      const heads = TONES.map((t) => {
        const p = palette(200, true, t);
        return fromHex(tinted(p.head!, p.eye!, tint)[0]).l;
      });
      expect(new Set(heads.map((l) => l.toFixed(3))).size, name).toBe(TONES.length);
      // `BLUSH` pulls only 0.4 of the way, so it keeps *more* of the tone set
      // than the loud targets do — the floor is what every target has to clear,
      // not what each one happens to score.
      expect(Math.max(...heads) - Math.min(...heads), name).toBeGreaterThan(0.2);
    }
  });

  test("a tinted body actually arrives at its target hue", () => {
    // Every guarantee above is satisfiable by not moving, or by desaturating to
    // grey — `ensureContrast` and the gamut mapper both reach for lightness and
    // chroma, and either could quietly turn "angry" into "beige". So: the hue
    // lands where the target said, and there is chroma left for it to land in.
    for (const [name, tint] of TINTS) {
      for (const tone of TONES) {
        for (const h of HUES) {
          const p = palette(h, true, tone);
          const c = fromHex(tinted(p.head!, p.eye!, tint)[0]);
          const off = Math.abs(((c.h - tint.h + 540) % 360) - 180);
          expect(off, `${name} hue ${h} tone ${tone}`).toBeLessThan(6);
          expect(c.c, `${name} hue ${h} tone ${tone}`).toBeGreaterThan(0.06);
        }
      }
    }
  });
});

describe("ensureContrast", () => {
  test("rescues a pair that starts far too close", () => {
    const bg = { l: 0.6, c: 0.1, h: 240 };
    const fg = { l: 0.62, c: 0.1, h: 240 };
    expect(contrast(fg, bg)).toBeLessThan(1.2);
    expect(contrast(ensureContrast(fg, bg, 4.5), bg)).toBeGreaterThanOrEqual(
      4.5,
    );
  });

  test("keeps the direction it is already leaning", () => {
    const bg = { l: 0.9, c: 0.05, h: 30 };
    const fg = { l: 0.85, c: 0.05, h: 30 };
    expect(ensureContrast(fg, bg, 4.5).l).toBeLessThan(fg.l); // darker, not flipped
  });

  test("flips direction when the lean runs out of range", () => {
    const bg = { l: 0.05, c: 0.02, h: 30 };
    const fg = { l: 0.04, c: 0.02, h: 30 };
    expect(contrast(ensureContrast(fg, bg, 7), bg)).toBeGreaterThanOrEqual(7);
  });

  test("leaves an already-passing pair untouched", () => {
    const bg = { l: 0.95, c: 0.02, h: 30 };
    const fg = { l: 0.1, c: 0.02, h: 30 };
    expect(ensureContrast(fg, bg, 4.5)).toBe(fg);
  });
});
