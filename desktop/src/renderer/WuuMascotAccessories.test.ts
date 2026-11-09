import { describe, expect, it } from "vitest";
import { measureAccessoryFit } from "./WuuMascotAccessories";

const BODY = { cx: 50, cy: 50, rx: 32, ry: 32 };

function occupancyLayer(inside: (x: number, y: number) => boolean): SVGGElement {
  const cursor = { x: 0, y: 0 };
  const path = {
    tagName: "path",
    isPointInFill(point: { x: number; y: number }) {
      return inside((point.x - BODY.cx) * 32 / BODY.rx, (point.y - BODY.cy) * 32 / BODY.ry);
    },
  };
  return {
    children: [path],
    ownerSVGElement: { createSVGPoint() { return cursor; } },
  } as unknown as SVGGElement;
}

function circleInside(x: number, y: number): boolean {
  return x * x + y * y <= 32 * 32;
}

function triangleInside(x: number, y: number): boolean {
  const top = -32;
  const bottom = 27.2;
  if (y < top || y > bottom) return false;
  const t = (y - top) / (bottom - top);
  return Math.abs(x) <= 32 * t;
}

describe("measureAccessoryFit headset ear", () => {
  it("anchors the earpiece at eye level on a circular body", () => {
    const fit = measureAccessoryFit(occupancyLayer(circleInside), BODY);
    expect(circleInside(fit.earX - 0.5, 0)).toBe(true);
    expect(circleInside(fit.earX + 1, 0)).toBe(false);
  });

  it("follows a tapered body instead of placing the cup at its widest point", () => {
    const fit = measureAccessoryFit(occupancyLayer(triangleInside), BODY);
    expect(triangleInside(fit.earX - 0.5, 0)).toBe(true);
    expect(triangleInside(fit.earX + 1, 0)).toBe(false);
  });
});
