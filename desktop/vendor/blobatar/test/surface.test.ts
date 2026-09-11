import { describe, expect, test } from "bun:test";
import { _layout, _parts, blobatar } from "../src/blobatar";
import { bakePose, happy, idle, mad, scared, sleepy, wink } from "../src/expression";
import { faceSurface, surfaceEye } from "../src/surface";

const body = { cx: 50, cy: 50, rx: 39, ry: 39 };
const eye = { cx: 62, cy: 46, rx: 4, ry: 11, rot: 0, n: 4 };

describe("curved face geometry", () => {
  test("the front camera recovers the authored chart from the sphere", () => {
    const surface = faceSurface(body);
    for (let x = -0.9; x <= 0.9; x += 0.1) {
      for (let y = -0.9; y <= 0.9; y += 0.1) {
        if (Math.hypot(x, y) >= 1) continue;
        const point: [number, number] = [50 + x * 39, 50 + y * 39];
        const lifted = surface.lift(point);
        expect(Math.hypot(...lifted)).toBeCloseTo(1, 10);
        const projected = surface.at(point);
        expect(projected[0]).toBeCloseTo(point[0], 10);
        expect(projected[1]).toBeCloseTo(point[1], 10);
      }
    }
  });

  test("a straight meridian bends under a turn instead of remaining a tilted flat line", () => {
    const surface = faceSurface(body, { yaw: 35, pitch: 0, strength: 1 });
    const top = surface.at([62, 30]), mid = surface.at([62, 50]), bottom = surface.at([62, 70]);
    expect(Math.abs(mid[0] - (top[0] + bottom[0]) / 2)).toBeGreaterThan(1);
    expect(top[1] + bottom[1]).toBeCloseTo(100, 10);
  });

  test("turning toward the limb foreshortens and finally hides an eye", () => {
    const frontal = surfaceEye(eye, body, { strength: 1 });
    const turned = surfaceEye(eye, body, { yaw: 45, strength: 1 });
    expect(turned.rx).toBeLessThan(frontal.rx * 0.6);
    const rear = surfaceEye({ ...eye, cx: 76, rx: 2, ry: 4 }, body, { yaw: 55, strength: 1 });
    expect(rear.path).toBe("");
    expect(rear.contour).toHaveLength(0);
  });

  test("partial eyes close along the silhouette and stay inside it during clipping", () => {
    let partial = false;
    for (let yaw = 35; yaw <= 55; yaw += 0.25) {
      const result = surfaceEye({ ...eye, cx: 70 }, body, { yaw, pitch: 18, strength: 1 });
      expect(result.path).not.toMatch(/NaN|Infinity/);
      for (const [x, y] of result.contour) {
        const radius = Math.hypot((x - 50) / 39, (y - 50) / 39);
        expect(radius).toBeLessThanOrEqual(1.00000001);
        if (Math.abs(radius - 1) < 1e-8) partial = true;
      }
    }
    expect(partial).toBe(true);
  });

  test("expression deformation happens before projection in the exported SVG", () => {
    const traits = { shape: 0.2, "body.ratio": 0.5, "body.n": 1 / 6 };
    const perspective = { yaw: 38, pitch: -24, strength: 1 };
    const authored = _layout("wuu", { traits });
    for (const expression of [idle, happy, mad, scared, sleepy, wink]) {
      const expected = bakePose(authored, expression.p).l.eyes.map((e) => surfaceEye(e, authored.body, perspective));
      const svg = blobatar("wuu", { traits, perspective, expression });
      for (const projected of expected) expect(svg).toContain(`d="${projected.path}"`);
    }
  });

  test("camera and expression changes preserve the animated subtree", () => {
    const before = _parts("wuu", { animate: "always", perspective: { yaw: -45, strength: 1 }, expression: happy });
    const after = _parts("wuu", { animate: "always", perspective: { yaw: 45, strength: 1 }, expression: wink });
    expect(after.inner).toBe(before.inner);
    expect(after.vars).not.toEqual(before.vars);
  });
});
