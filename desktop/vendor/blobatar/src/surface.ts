import type { FacePerspective } from "./render";
import { superellipse, superellipseSegs, type Superellipse } from "./shape";

export type Point = [number, number];
type Vec3 = [number, number, number];
export type FaceBody = Pick<Superellipse, "cx" | "cy" | "rx" | "ry">;

// Normalize the projected limb to radius one. The front-facing authored chart
// then has exactly the same dimensions as the body's SVG ellipse.
const DISTANCE = 4;
const FOCAL = Math.sqrt(DISTANCE * DISTANCE - 1);
const HORIZON = 1 / DISTANCE;
const clamp = (n: number, low: number, high: number) => Math.max(low, Math.min(high, n));
const finite = (n: number | undefined, fallback = 0) => Number.isFinite(n) ? n! : fallback;
const normalizeTurn = (degrees: number) => ((degrees + 180) % 360 + 360) % 360 - 180;

/** Strength scales rotation, never interpolates already projected coordinates. */
export function faceAngles(view?: FacePerspective): { yaw: number; pitch: number } {
  const strength = clamp(finite(view?.strength), 0, 1);
  return {
    yaw: clamp(finite(view?.yaw), -55, 55) * strength,
    pitch: clamp(finite(view?.pitch), -45, 45) * strength,
  };
}

/** A camera-facing chart lifted by ray/sphere intersection, then rigidly turned. */
export function faceSurface(body: FaceBody, view?: FacePerspective, yawOffset = 0) {
  const angles = faceAngles(view);
  // The authored camera stays clamped while a transient turn may cross the rear hemisphere.
  const yaw = normalizeTurn(angles.yaw + finite(yawOffset)) * Math.PI / 180;
  const pitch = angles.pitch * Math.PI / 180;
  const cy = Math.cos(yaw), sy = Math.sin(yaw);
  const cp = Math.cos(pitch), sp = Math.sin(pitch);
  const lift = ([px, py]: Point): Vec3 => {
    let x = (px - body.cx) / body.rx;
    let y = (py - body.cy) / body.ry;
    // Expressions outside the chart settle on its limb, rather than producing
    // imaginary depth or jumping to a second surface on the back of the head.
    const radius = Math.hypot(x, y);
    if (radius > 1) { x /= radius; y /= radius; }
    const a = 1 + (x * x + y * y) / (FOCAL * FOCAL);
    const t = (DISTANCE - Math.sqrt(Math.max(0, DISTANCE * DISTANCE - a * FOCAL * FOCAL))) / a;
    x *= t / FOCAL;
    y *= t / FOCAL;
    const z = DISTANCE - t;
    const turnedX = x * cy + z * sy;
    const turnedZ = -x * sy + z * cy;
    return [turnedX, y * cp - turnedZ * sp, y * sp + turnedZ * cp];
  };
  const project = ([x, y, z]: Vec3): Point => {
    const scale = FOCAL / (DISTANCE - z);
    return [body.cx + body.rx * x * scale, body.cy + body.ry * y * scale];
  };
  return { lift, project, at: (p: Point) => project(lift(p)) };
}

function outline(eye: Superellipse): Point[] {
  return superellipseSegs(eye).flatMap((s) => Array.from({ length: 16 }, (_, i): Point => {
    const t = i / 16, u = 1 - t;
    return [0, 1].map((axis) =>
      u * u * u * s[0]![axis]! + 3 * u * u * t * s[1]![axis]! +
      3 * u * t * t * s[2]![axis]! + t * t * t * s[3]![axis]!,
    ) as Point;
  }));
}

/**
 * Project every contour point, hiding the rear hemisphere. A crossing closes
 * along the camera's silhouette arc, so the clipped eye cannot fold over or
 * remain painted on the back of the ball. The returned contour measures the
 * actual visible mark; rx/ry are only its screen-space bounding half-extents.
 */
export function surfaceEye<E extends Superellipse>(eye: E, body: FaceBody, view?: FacePerspective, yawOffset = 0) {
  const surface = faceSurface(body, view, yawOffset);
  const authored = outline(eye);
  const lifted = authored.map(surface.lift);
  const visible = lifted.map((p) => p[2] >= HORIZON);
  const contour: Point[] = [];
  const intersection = (a: Point, b: Point): Point => {
    let lo = 0, hi = 1;
    const fromVisible = surface.lift(a)[2] >= HORIZON;
    for (let i = 0; i < 24; i++) {
      const t = (lo + hi) / 2;
      const point: Point = [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t];
      if ((surface.lift(point)[2] >= HORIZON) === fromVisible) lo = t;
      else hi = t;
    }
    const t = (lo + hi) / 2;
    return surface.at([a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t]);
  };
  // Start at an entry so each invisible run has its exit and re-entry together.
  const entry = visible.findIndex((v, i) => v && !visible[(i + visible.length - 1) % visible.length]);
  const start = entry < 0 ? 0 : entry;
  let exit: Point | undefined;
  for (let j = 0; j < authored.length; j++) {
    const i = (start + j) % authored.length;
    const next = (i + 1) % authored.length;
    if (visible[i]) contour.push(surface.project(lifted[i]!));
    if (visible[i] === visible[next]) continue;
    const crossing = intersection(authored[i]!, authored[next]!);
    if (visible[i]) { contour.push(crossing); exit = crossing; }
    else if (exit) {
      const angle = (p: Point) => Math.atan2((p[1] - body.cy) / body.ry, (p[0] - body.cx) / body.rx);
      const from = angle(exit);
      // Eye patches occupy less than a hemisphere; the shorter limb arc is the
      // boundary of their visible portion, including across the angle seam.
      const delta = Math.atan2(Math.sin(angle(crossing) - from), Math.cos(angle(crossing) - from));
      const steps = Math.max(1, Math.ceil(Math.abs(delta) / 0.025));
      for (let k = 1; k <= steps; k++) {
        const a = from + delta * k / steps;
        contour.push([body.cx + body.rx * Math.cos(a), body.cy + body.ry * Math.sin(a)]);
      }
      exit = undefined;
    }
  }
  const center = surface.at([eye.cx, eye.cy]);
  const xs = contour.map((p) => p[0]), ys = contour.map((p) => p[1]);
  const angles = faceAngles(view);
  const yaw = normalizeTurn(angles.yaw + finite(yawOffset));
  const pitch = angles.pitch;
  const round = (v: number) => Math.round(v * 1000) / 1000;
  const path = contour.length === 0 ? "" : yaw === 0 && pitch === 0 &&
    authored.every((p) => Math.hypot((p[0] - body.cx) / body.rx, (p[1] - body.cy) / body.ry) <= 1)
    ? superellipse(eye)
    : contour.map((p, i) => `${i ? "L" : "M"}${round(p[0])} ${round(p[1])}`).join("") + "Z";
  return {
    ...eye, cx: center[0], cy: center[1],
    rx: contour.length ? (Math.max(...xs) - Math.min(...xs)) / 2 : 0,
    ry: contour.length ? (Math.max(...ys) - Math.min(...ys)) / 2 : 0,
    rot: 0, path, contour,
  };
}
