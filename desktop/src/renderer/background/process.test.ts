import { expect, it } from "vitest";
import { applyBackgroundPixels, isBackgroundRaster } from "./process";

it("rejects executable/vector and renamed non-image bytes before decoding", () => {
  expect(isBackgroundRaster(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"/>'))).toBe(false);
  expect(isBackgroundRaster(new Uint8Array())).toBe(false);
  expect(isBackgroundRaster(new TextEncoder().encode("not actually a JPEG"))).toBe(false);
});

it.each(["dither", "halftone", "scanlines"] as const)("%s handles partial edge cells without discarding transparency", effect => {
  const pixels = new Uint8ClampedArray(7 * 5 * 4);
  for (let i = 0; i < pixels.length; i += 4) pixels.set([80, 140, 220, (i * 7) % 256], i);
  const before = pixels.slice();
  applyBackgroundPixels(pixels, 7, 5, effect, true);
  expect(pixels).not.toEqual(before);
  expect(pixels.filter((_, i) => i % 4 === 3)).toEqual(before.filter((_, i) => i % 4 === 3));
});

it("uses theme-aware texture without altering the original mode", () => {
  const original = new Uint8ClampedArray([70, 140, 210, 128]);
  const light = original.slice(), dark = original.slice(), plain = original.slice();
  applyBackgroundPixels(light, 1, 1, "scanlines", true);
  applyBackgroundPixels(dark, 1, 1, "scanlines", false);
  applyBackgroundPixels(plain, 1, 1, "none", true);
  expect(light[0]).toBeGreaterThan(original[0]);
  expect(dark[0]).toBeLessThan(original[0]);
  expect(plain).toEqual(original);
});
