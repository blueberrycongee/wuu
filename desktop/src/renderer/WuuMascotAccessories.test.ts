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

function pathPoints(d: string): Array<[number, number]> {
  return [...d.matchAll(/[ML](-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)/g)].map((match) => [Number(match[1]), Number(match[2])]);
}

function triangleInside(x: number, y: number): boolean {
  const top = -32;
  const bottom = 27.2;
  if (y < top || y > bottom) return false;
  const t = (y - top) / (bottom - top);
  return Math.abs(x) <= 32 * t;
}

describe("measureAccessoryFit headset band", () => {
  it("keeps a circular body on the authored band instead of shrinking it through the crown", () => {
    const fit = measureAccessoryFit(occupancyLayer((x, y) => x * x + y * y <= 32 * 32), BODY);
    const points = pathPoints(fit.headsetBand);
    expect(points.length).toBeGreaterThan(8);
    const first = points[0]!;
    const last = points[points.length - 1]!;
    expect(Math.hypot(first[0] + 30, first[1] + 8)).toBeLessThan(2);
    expect(Math.hypot(last[0] - 32, last[1] - 1)).toBeLessThan(2);
    expect(Math.hypot(fit.headsetEarX, fit.headsetEarY)).toBeLessThan(2);
  });

  it("walks a tapered crown instead of letting the rear silhouette hide a circular chord", () => {
    const fit = measureAccessoryFit(occupancyLayer(triangleInside), BODY);
    const points = pathPoints(fit.headsetBand);
    expect(points.length).toBeGreaterThan(8);
    expect(points.some(([x, y]) => Math.abs(x) < 8 && y < -28)).toBe(true);
    expect(points.every(([x, y]) => !(triangleInside(x, y) && y < -6 && y > -26 && Math.abs(x) < 8))).toBe(true);
  });
});
