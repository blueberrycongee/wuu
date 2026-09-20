import { afterEach, describe, expect, it, vi } from "vitest";
import { cubicBezier, motionEasing } from "./motion";

afterEach(() => {
  vi.restoreAllMocks();
});

function stubComputedValue(value: string): void {
  vi.spyOn(window, "getComputedStyle").mockReturnValue({
    getPropertyValue: () => value,
  } as unknown as CSSStyleDeclaration);
}

describe("cubicBezier", () => {
  it("pins its endpoints and stays monotone inside the unit interval", () => {
    const curve = cubicBezier(0.16, 1, 0.3, 1);
    expect(curve(-1)).toBe(0);
    expect(curve(0)).toBe(0);
    expect(curve(1)).toBe(1);
    expect(curve(2)).toBe(1);
    let previous = 0;
    for (let step = 0; step <= 1.0001; step += 0.005) {
      const value = curve(step);
      expect(value).toBeGreaterThanOrEqual(0);
      expect(value).toBeLessThanOrEqual(1);
      expect(value).toBeGreaterThanOrEqual(previous - 1e-9);
      previous = value;
    }
  });

  it("reproduces the shape a stylesheet author wrote", () => {
    expect(cubicBezier(0, 0, 1, 1)(0.25)).toBeCloseTo(0.25, 6);
    // Front-loaded curves are already most of the way at their midpoint.
    expect(cubicBezier(0.16, 1, 0.3, 1)(0.5)).toBeGreaterThan(0.75);
    // An in-out pair is symmetric about its midpoint.
    expect(cubicBezier(0.42, 0, 0.58, 1)(0.5)).toBeCloseTo(0.5, 3);
  });
});

describe("motionEasing", () => {
  const linear = cubicBezier(0, 0, 1, 1);

  it("falls back when the environment never resolved the token", () => {
    expect(motionEasing("--missing-easing", linear)(0.25)).toBeCloseTo(0.25, 6);
  });

  it("evaluates the curve a token names, matching the CSS transition", () => {
    stubComputedValue("cubic-bezier(0.333333, 1, 0.666667, 1)");
    const easing = motionEasing("--query-submit-easing", linear);
    // The collaboration follow used to hand-roll this curve as 1-(1-p)^3.
    for (const progress of [0.1, 0.25, 0.5, 0.75, 0.9]) {
      expect(easing(progress)).toBeCloseTo(1 - (1 - progress) ** 3, 2);
    }
  });

  it("accepts a keyword and ignores a value that is not a curve", () => {
    stubComputedValue("ease-in-out");
    expect(motionEasing("--token", linear)(0.5)).toBeCloseTo(0.5, 3);
    stubComputedValue("42px");
    expect(motionEasing("--other-token", linear)(0.25)).toBeCloseTo(0.25, 6);
  });
});
