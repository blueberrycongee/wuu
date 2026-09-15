import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(__dirname, "scroll-fade.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

// jsdom has no scroll-timeline/layout engine. These are stylesheet contract
// checks, not visual assertions; see docs/scroll-fade.md for the runtime gate.
describe("scroll fade stylesheet contract", () => {
  it("is loaded by the renderer and gated to engines with scroll ranges", () => {
    const entry = readFileSync(resolve(__dirname, "../styles.css"), "utf8");
    expect(entry).toContain('@import "./styles/scroll-fade.css"');
    expect(css).toContain("@supports (animation-timeline: scroll(self y)) and (animation-range: 0px 1px)");
  });

  it.each(["top", "bottom"])("starts %s fully legible and isolates nested scrollers", (edge) => {
    const registration = css.match(new RegExp(`@property --scroll-fade-${edge}\\s*\\{([^}]+)\\}`))?.[1];
    expect(registration).toContain('syntax: "<length>"');
    expect(registration).toContain("inherits: false");
    expect(registration).toContain("initial-value: 0px");
  });

  it("uses pixel ranges at both ends instead of fading a percentage of a long conversation", () => {
    expect(css).toMatch(/animation-range:\s*0px var\(--scroll-fade-size\),\s*calc\(100% - var\(--scroll-fade-size\)\) 100%/);
    expect(css).toMatch(/@keyframes scroll-fade-top\s*\{\s*from \{ --scroll-fade-top: 0px; \}\s*to \{ --scroll-fade-top: var\(--scroll-fade-size\); \}/);
    expect(css).toMatch(/@keyframes scroll-fade-bottom\s*\{\s*from \{ --scroll-fade-bottom: var\(--scroll-fade-size\); \}\s*to \{ --scroll-fade-bottom: 0px; \}/);
    expect(css.indexOf("\n    animation-timeline:")).toBeGreaterThan(css.indexOf("\n    animation:"));
    expect(css).toContain("animation-timeline: scroll(self y), scroll(self y)");
  });

  it("only masks alpha, caps tiny viewports, and does not change layout or input", () => {
    expect(css).toContain("mask-mode: alpha");
    expect(css).toContain("min(var(--scroll-fade-top), 50%)");
    expect(css).toContain("min(var(--scroll-fade-bottom), 50%)");
    expect(css).not.toMatch(/(?:^|[;{}])\s*(?:overflow[\w-]*|position|pointer-events|height|padding|scroll-behavior)\s*:/m);
    expect(css).not.toMatch(/::before|::after/);
  });

  it("removes the mask for accessibility and print rather than freezing a fade", () => {
    expect(css).toMatch(/:root\[data-appearance-motion="reduce"\] \[data-scroll-fade\]\s*\{\s*animation: none;\s*mask-image: none;/);
    expect(css).toMatch(/@media \(forced-colors: active\), \(prefers-reduced-motion: reduce\), print\s*\{\s*\[data-scroll-fade\]\s*\{\s*animation: none;\s*mask-image: none;/);
  });
});
