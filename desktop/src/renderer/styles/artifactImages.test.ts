import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(__dirname, "turns.css"), "utf8");
const rule = (selector: string): string => {
  const start = css.indexOf(`${selector} {`);
  expect(start).toBeGreaterThanOrEqual(0);
  return css.slice(start, css.indexOf("}", start) + 1);
};

describe("inline artifact image layout", () => {
  it("hugs the image rather than stretching across the conversation", () => {
    const card = rule(".turn-artifact-inline-image");
    expect(card).toContain("align-self: flex-start");
    expect(card).toContain("width: fit-content");
    expect(card).toContain("max-width: min(100%, 480px)");
    expect(rule(".turn-artifact-inline-image figcaption")).toContain("contain: inline-size");
  });

  it("bounds large previews without upscaling small images or cropping QR codes", () => {
    const image = rule(".turn-artifact-inline-image img");
    expect(image).toContain("width: auto");
    expect(image).toContain("height: auto");
    expect(image).toContain("max-width: 100%");
    expect(image).toContain("max-height: 280px");
    expect(image).toContain("object-fit: contain");
    expect(rule(".turn-artifact-inline-image button")).not.toMatch(/\swidth:\s*100%/);
  });

  it("keeps filenames to a quiet single line", () => {
    const name = rule(".turn-artifact-image-name");
    expect(name).toContain("min-width: 0");
    expect(name).toContain("text-overflow: ellipsis");
    expect(name).toContain("white-space: nowrap");
  });
});
