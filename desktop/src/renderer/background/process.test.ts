import { expect, it } from "vitest";
import { isBackgroundRaster } from "./process";

it("rejects executable/vector and renamed non-image bytes before decoding", () => {
  expect(isBackgroundRaster(new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"/>'))).toBe(false);
  expect(isBackgroundRaster(new Uint8Array())).toBe(false);
  expect(isBackgroundRaster(new TextEncoder().encode("not actually a JPEG"))).toBe(false);
});
