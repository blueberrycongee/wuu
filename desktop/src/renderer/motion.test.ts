import { afterEach, describe, expect, it, vi } from "vitest";
import { cubicBezier, motionEasing, prefersReducedMotion, subscribeReducedMotion } from "./motion";

afterEach(() => {
  vi.restoreAllMocks();
  delete document.documentElement.dataset.appearanceMotion;
});

/** An OS reduced-motion query the test can flip, as the platform would. */
function stubSystemReducedMotion(initial: boolean): (matches: boolean) => void {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
  };
  vi.spyOn(window, "matchMedia").mockReturnValue(media as unknown as MediaQueryList);
  return (matches) => {
    media.matches = matches;
    for (const listener of listeners) listener();
  };
}

const flushMutations = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

describe("reduced motion", () => {
  it("follows the OS setting while the in-app preference follows the system", () => {
    stubSystemReducedMotion(true);
    document.documentElement.dataset.appearanceMotion = "system";
    expect(prefersReducedMotion()).toBe(true);
  });

  it("reduces motion from the in-app preference even when the OS does not", () => {
    stubSystemReducedMotion(false);
    expect(prefersReducedMotion()).toBe(false);
    document.documentElement.dataset.appearanceMotion = "reduce";
    expect(prefersReducedMotion()).toBe(true);
  });

  it("notifies once per change from either source and stops after unsubscribing", async () => {
    const setSystem = stubSystemReducedMotion(false);
    const listener = vi.fn();
    const unsubscribe = subscribeReducedMotion(listener);

    document.documentElement.dataset.appearanceMotion = "reduce";
    await flushMutations();
    expect(listener).toHaveBeenLastCalledWith(true);

    // Still reduced by the preference: the OS turning on changes nothing.
    setSystem(true);
    expect(listener).toHaveBeenCalledTimes(1);

    document.documentElement.dataset.appearanceMotion = "system";
    await flushMutations();
    expect(listener).toHaveBeenCalledTimes(1);

    setSystem(false);
    expect(listener).toHaveBeenLastCalledWith(false);
    expect(listener).toHaveBeenCalledTimes(2);

    unsubscribe();
    document.documentElement.dataset.appearanceMotion = "reduce";
    await flushMutations();
    expect(listener).toHaveBeenCalledTimes(2);
  });
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
    // The split-pane bottom-follow used to hand-roll this curve as 1-(1-p)^3.
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
