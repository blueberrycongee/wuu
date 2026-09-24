import { _layout } from "blobatar";
import { superellipse } from "../../vendor/blobatar/src/shape";
import icon from "../../../assets/app-icon-source.json";
import { ENGINE_ICON_EVENODD, ENGINE_ICON_PATHS } from "../../src/renderer/EngineIcons";
import { WUU_MASCOT_NAME, WUU_MASCOT_TRAITS } from "../../src/renderer/wuu-mascot-spec";
import { clamp, outBack, outCubic, rad, smooth, type Eyes } from "./motion";

type Ctx = CanvasRenderingContext2D;

export const INK = "#2A2B2E";
export const PAPER = "#FFFDF8";

// ---------------------------------------------------------------------------
// Characters

export type Shape = "round" | "squircle" | "capsule" | "diamond";
export type Gear = "headset" | "hardhat" | "beanie" | "leaf" | "magnifier";
export interface Skin { body: string; hi: string; lo: string; eye: string; shape: Shape }

/** The approved app icon palette: the only charcoal character in the film. */
export const WUU: Skin = {
  body: icon.bodyColor, hi: icon.bodyHighlight, lo: icon.bodyShadow, eye: icon.eyeColor, shape: "round",
};

/** Agent bodies follow the product's hue wheel with dark eyes, like in-app avatars. */
export function pastel(hue: number, shape: Shape): Skin {
  return {
    body: `hsl(${hue} 72% 72%)`, hi: `hsl(${hue} 90% 85%)`, lo: `hsl(${hue} 48% 58%)`,
    eye: "#2B2A33", shape,
  };
}

// Bodies are authored at radius 100 and scaled to the requested radius.
const identity = _layout(WUU_MASCOT_NAME, { traits: WUU_MASCOT_TRAITS });
const body = identity.body;
const SHAPES: Record<Shape, { path: Path2D; rx: number; ry: number }> = {
  round: identityShape(),
  squircle: shape(0.94, 0.94, 4),
  capsule: shape(0.84, 1.08, 2.6),
  diamond: shape(1.14, 1.14, 1.6),
};
/** Half-extents of a body relative to its radius, for standing it on a floor. */
export const extent = (skin: Skin) => SHAPES[skin.shape];
function shape(rx: number, ry: number, n: number) {
  return { path: new Path2D(superellipse({ cx: 0, cy: 0, rx: rx * 100, ry: ry * 100, n })), rx, ry };
}
/** Wuu's own outline, recentred and scaled so its half-width is 100. */
function identityShape() {
  const path = new Path2D();
  path.addPath(new Path2D(body.path), new DOMMatrix().scale(100 / body.rx).translate(-body.cx, -body.cy));
  return { path, rx: 1, ry: body.ry / body.rx };
}

// Face proportions come straight from the approved icon, so the film's
// character is the icon's character when posed like the icon.
const EYE_W = icon.eyeWidth / icon.radius;
const EYE_H = icon.eyeHeight / icon.radius;
const EYE_GAP = icon.eyeGap / icon.radius;
const REACH = 0.86;
const REST_Y = 0.04;
export const ICON_POSE = {
  yaw: Math.asin(icon.faceX / REACH),
  pitch: Math.asin((REST_Y - icon.faceY) / REACH),
  roll: icon.tilt,
  markAngle: icon.fanAngle,
};
const foreshorten = (a: number) => 0.7 + 0.3 * Math.cos(a);

export interface Ball {
  x: number; y: number; r: number;
  sx?: number; sy?: number; tilt?: number;
  yaw?: number; pitch?: number; roll?: number;
  eyes?: Eyes;
  marks?: number; markAngle?: number; markColor?: string;
  skin?: Skin; gear?: Gear; engine?: string; sway?: number;
  blush?: number; sweat?: number; alpha?: number;
  /** Floor height for the contact shadow; omitted when the ball floats. */
  ground?: number;
  /** Body opacity alone, so eyes can glow in the dark before the lights come on. */
  bodyAlpha?: number;
  /** Vertical offset of the accessory, for dropping it onto the head. */
  gearY?: number;
}

export function drawBall(ctx: Ctx, b: Ball) {
  const skin = b.skin ?? WUU;
  const form = SHAPES[skin.shape];
  const alpha = b.alpha ?? 1;
  if (alpha <= 0 || b.r < 0.5) return;
  const { x, y, r } = b;
  const bottom = r * form.ry;
  ctx.save();
  ctx.globalAlpha *= alpha;
  if (b.ground !== undefined) drawShadow(ctx, x, b.ground, r * form.rx, (b.ground - y - bottom) / (r * 2.4));
  // Squash and stretch pivot on the contact point, like a real soft ball.
  ctx.translate(x, y + bottom);
  ctx.rotate(rad(b.tilt ?? 0));
  ctx.scale(b.sx ?? 1, b.sy ?? 1);
  ctx.translate(0, -bottom);
  if (b.engine) drawAntenna(ctx, r, b.engine, b.sway ?? 0, skin, form.ry);
  ctx.save();
  ctx.globalAlpha *= b.bodyAlpha ?? 1;
  ctx.scale(r / 100, r / 100);
  const light = ctx.createRadialGradient(-25, -45, 0, -25, -45, 150);
  light.addColorStop(0, skin.hi);
  light.addColorStop(0.52, skin.body);
  light.addColorStop(1, skin.lo);
  ctx.fillStyle = light;
  ctx.fill(form.path);
  ctx.restore();
  drawFace(ctx, r, b, skin);
  if (b.gear) {
    ctx.translate(0, b.gearY ?? 0);
    drawGear(ctx, r, b.gear, form.ry);
  }
  ctx.restore();
  if (b.marks) {
    ctx.save();
    ctx.globalAlpha *= alpha;
    ctx.translate(x, y);
    drawMarks(ctx, r * form.rx, b.markAngle ?? icon.fanAngle, b.marks, b.markColor ?? icon.markColor);
    ctx.restore();
  }
  if (b.sweat) drawSweat(ctx, x + r * 0.82, y - r * 0.5 + r * 0.25 * b.sweat, r * 0.2, clamp(b.sweat * 3) * alpha);
}

function drawFace(ctx: Ctx, r: number, b: Ball, skin: Skin) {
  const yaw = b.yaw ?? 0, pitch = b.pitch ?? 0;
  // The face slides around the sphere; it fades as it turns past the rim.
  const visible = smooth(clamp(Math.cos(yaw) / 0.35));
  if (visible <= 0) return;
  const e = b.eyes ?? { kind: "open", open: 1 };
  const grow = e.kind === "wide" ? 1.16 : 1;
  const w = r * EYE_W * foreshorten(yaw) / foreshorten(ICON_POSE.yaw) * grow;
  const h = r * EYE_H * foreshorten(pitch) / foreshorten(ICON_POSE.pitch) * grow;
  const gap = r * EYE_GAP * Math.cos(yaw) / Math.cos(ICON_POSE.yaw);
  ctx.save();
  ctx.globalAlpha *= visible;
  ctx.translate(r * REACH * Math.sin(yaw), r * (REST_Y - REACH * Math.sin(pitch)));
  ctx.rotate(rad(b.roll ?? 0));
  if (b.blush) {
    ctx.fillStyle = `rgba(255, 138, 160, ${0.55 * b.blush})`;
    for (const side of [-1, 1]) {
      ctx.beginPath();
      ctx.ellipse(side * gap * 0.92, h * 0.62, w * 0.8, w * 0.42, 0, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  ctx.fillStyle = skin.eye;
  ctx.strokeStyle = skin.eye;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  for (const side of [-1, 1]) {
    ctx.save();
    ctx.translate(side * gap / 2, 0);
    drawEye(ctx, e, side, w, h);
    ctx.restore();
  }
  ctx.restore();
}

function drawEye(ctx: Ctx, e: Eyes, side: number, w: number, h: number) {
  if (e.kind === "open" || e.kind === "wide") {
    const height = Math.max(w * 0.34, h * e.open);
    capsule(ctx, 0, 0, w, height);
    return;
  }
  ctx.scale(1, Math.max(0.12, e.open));
  ctx.beginPath();
  switch (e.kind) {
    case "happy":
      ctx.lineWidth = w * 0.6;
      ctx.arc(0, h * 0.16, w * 0.58, rad(195), rad(345));
      ctx.stroke();
      break;
    case "content":
      ctx.lineWidth = w * 0.56;
      ctx.arc(0, -h * 0.1, w * 0.56, rad(25), rad(155));
      ctx.stroke();
      break;
    case "flat":
      ctx.roundRect(-w * 0.8, -w * 0.3, w * 1.6, w * 0.6, w * 0.3);
      ctx.fill();
      break;
    case "squeeze": {
      const dir = -side;
      ctx.lineWidth = w * 0.66;
      ctx.moveTo(-w * 0.45 * dir, -h * 0.28);
      ctx.lineTo(w * 0.45 * dir, 0);
      ctx.lineTo(-w * 0.45 * dir, h * 0.28);
      ctx.stroke();
      break;
    }
    case "dizzy":
      ctx.lineWidth = w * 0.3;
      for (let i = 0; i <= 44; i++) {
        const k = i / 44;
        const a = k * Math.PI * 3.4 + (e.spin ?? 0) * side;
        const rr = w * (0.06 + 0.66 * k);
        if (i === 0) ctx.moveTo(Math.cos(a) * rr, Math.sin(a) * rr);
        else ctx.lineTo(Math.cos(a) * rr, Math.sin(a) * rr);
      }
      ctx.stroke();
      break;
    case "star":
      sparklePath(ctx, 0, 0, h * 0.46, w * 0.2);
      ctx.fill();
      break;
    case "sleepy":
      ctx.rect(-w, -h * 0.02, w * 2, h);
      ctx.clip();
      capsule(ctx, 0, 0, w, h * 0.86);
      break;
  }
}

function capsule(ctx: Ctx, x: number, y: number, w: number, h: number) {
  ctx.beginPath();
  ctx.roundRect(x - w / 2, y - h / 2, w, h, Math.min(w, h) / 2);
  ctx.fill();
}

/** The icon's three greeting strokes, popping out one after another. */
export function drawMarks(ctx: Ctx, r: number, angle: number, amount: number, color: string) {
  const k = r / icon.radius;
  ctx.fillStyle = color;
  icon.marks.forEach((mark, i) => {
    const p = clamp(amount * 1.6 - i * 0.3);
    if (p <= 0) return;
    const e = outBack(p);
    const a = rad(angle + (i - 1) * icon.fanSpread);
    const length = Math.max(mark.width * k, mark.length * k * e);
    const width = mark.width * k * Math.min(1, p * 3);
    const distance = r + (icon.fanGap + mark.gap) * k * (0.4 + 0.6 * e) + length / 2;
    ctx.save();
    ctx.translate(Math.cos(a) * distance, Math.sin(a) * distance);
    ctx.rotate(a + rad(mark.angle));
    ctx.beginPath();
    ctx.roundRect(-length / 2, -width / 2, length, width, width / 2);
    ctx.fill();
    ctx.restore();
  });
}

function drawAntenna(ctx: Ctx, r: number, engine: string, sway: number, skin: Skin, ry: number) {
  const baseY = -r * ry * 0.86;
  const tipX = sway * r * 0.42;
  const tipY = -r * ry - r * 0.5 + Math.abs(sway) * r * 0.08;
  ctx.strokeStyle = skin.lo;
  ctx.lineWidth = r * 0.075;
  ctx.lineCap = "round";
  ctx.beginPath();
  ctx.moveTo(0, baseY);
  ctx.quadraticCurveTo(sway * r * 0.04, (baseY + tipY) / 2, tipX, tipY);
  ctx.stroke();
  const disc = r * 0.38;
  drawToken(ctx, engine, tipX + sway * r * 0.08, tipY - disc * 0.85, disc, skin.lo);
}

/** A white badge carrying an engine's mono mark, as the product renders it. */
export function drawToken(ctx: Ctx, engine: string, x: number, y: number, r: number, ring: string, fill = "#fff", ink = INK) {
  ctx.save();
  ctx.fillStyle = fill;
  ctx.strokeStyle = ring;
  ctx.lineWidth = r * 0.14;
  ctx.beginPath();
  ctx.arc(x, y, r, 0, Math.PI * 2);
  ctx.fill();
  ctx.stroke();
  drawLogo(ctx, engine, x, y, r * 1.18, ink);
  ctx.restore();
}

const logoCache = new Map<string, Path2D>();
export function drawLogo(ctx: Ctx, engine: string, x: number, y: number, size: number, color: string) {
  let path = logoCache.get(engine);
  if (!path) logoCache.set(engine, (path = new Path2D(ENGINE_ICON_PATHS[engine])));
  ctx.save();
  ctx.translate(x - size / 2, y - size / 2);
  ctx.scale(size / 24, size / 24);
  ctx.fillStyle = color;
  ctx.fill(path, ENGINE_ICON_EVENODD.has(engine) ? "evenodd" : "nonzero");
  ctx.restore();
}

function drawGear(ctx: Ctx, r: number, gear: Gear, ry: number) {
  ctx.save();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  if (gear === "headset") {
    ctx.strokeStyle = "#FF8A7A";
    ctx.lineWidth = r * 0.12;
    ctx.beginPath();
    ctx.arc(0, -r * 0.02, r * 1.07, rad(192), rad(348));
    ctx.stroke();
    ctx.fillStyle = "#FF7466";
    for (const side of [-1, 1]) {
      ctx.beginPath();
      ctx.roundRect(side * r * 1.0 - r * 0.15, -r * 0.3, r * 0.3, r * 0.52, r * 0.14);
      ctx.fill();
    }
    ctx.lineWidth = r * 0.065;
    ctx.beginPath();
    ctx.moveTo(r * 1.02, r * 0.14);
    ctx.quadraticCurveTo(r * 1.02, r * 0.66, r * 0.56, r * 0.72);
    ctx.stroke();
    ctx.beginPath();
    ctx.arc(r * 0.54, r * 0.72, r * 0.1, 0, Math.PI * 2);
    ctx.fill();
  } else if (gear === "hardhat") {
    const top = -r * ry;
    ctx.fillStyle = "#FFC93C";
    ctx.beginPath();
    ctx.ellipse(0, top + r * 0.36, r * 0.66, r * 0.5, 0, Math.PI, 0);
    ctx.fill();
    ctx.fillStyle = "#FFE089";
    ctx.beginPath();
    ctx.roundRect(-r * 0.09, top - r * 0.14, r * 0.18, r * 0.5, r * 0.09);
    ctx.fill();
    ctx.fillStyle = "#F2AE1C";
    ctx.beginPath();
    ctx.roundRect(-r * 0.92, top + r * 0.3, r * 1.84, r * 0.17, r * 0.085);
    ctx.fill();
  } else if (gear === "beanie") {
    const top = -r * ry;
    ctx.fillStyle = "#7FA7FF";
    ctx.beginPath();
    ctx.moveTo(-r * 0.86, top + r * 0.5);
    ctx.bezierCurveTo(-r * 0.86, top - r * 0.12, r * 0.86, top - r * 0.12, r * 0.86, top + r * 0.5);
    ctx.fill();
    ctx.fillStyle = "#5F8BF0";
    ctx.beginPath();
    ctx.roundRect(-r * 0.95, top + r * 0.4, r * 1.9, r * 0.24, r * 0.12);
    ctx.fill();
    ctx.fillStyle = "#fff";
    ctx.beginPath();
    ctx.arc(0, top - r * 0.08, r * 0.16, 0, Math.PI * 2);
    ctx.fill();
  } else if (gear === "leaf") {
    const top = -r * ry;
    ctx.strokeStyle = "#5FAE5A";
    ctx.lineWidth = r * 0.06;
    ctx.beginPath();
    ctx.moveTo(0, top + r * 0.06);
    ctx.quadraticCurveTo(r * 0.02, top - r * 0.14, r * 0.06, top - r * 0.26);
    ctx.stroke();
    ctx.fillStyle = "#7BC96F";
    for (const side of [-1, 1]) {
      ctx.save();
      ctx.translate(r * 0.06 + side * r * 0.14, top - r * 0.26);
      ctx.rotate(rad(side * 32));
      ctx.beginPath();
      ctx.ellipse(0, 0, r * 0.17, r * 0.08, 0, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  } else if (gear === "magnifier") {
    ctx.strokeStyle = "#6B5A4A";
    ctx.lineWidth = r * 0.1;
    ctx.beginPath();
    ctx.moveTo(r * 1.08, r * 0.62);
    ctx.lineTo(r * 1.34, r * 0.92);
    ctx.stroke();
    ctx.fillStyle = "rgba(210, 236, 255, 0.75)";
    ctx.strokeStyle = "#8C7A68";
    ctx.lineWidth = r * 0.08;
    ctx.beginPath();
    ctx.arc(r * 0.9, r * 0.4, r * 0.3, 0, Math.PI * 2);
    ctx.fill();
    ctx.stroke();
  }
  ctx.restore();
}

export function drawShadow(ctx: Ctx, x: number, y: number, rx: number, lift: number) {
  const k = 1 - clamp(lift) * 0.55;
  ctx.fillStyle = `rgba(74, 56, 34, ${0.13 * k})`;
  ctx.beginPath();
  ctx.ellipse(x, y, rx * 0.88 * k, rx * 0.16 * k, 0, 0, Math.PI * 2);
  ctx.fill();
}

export function drawSweat(ctx: Ctx, x: number, y: number, s: number, alpha: number) {
  if (alpha <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = "#A9DBFF";
  ctx.beginPath();
  ctx.moveTo(x, y - s);
  ctx.bezierCurveTo(x + s * 0.2, y - s * 0.4, x + s * 0.72, y + s * 0.1, x, y + s * 0.62);
  ctx.bezierCurveTo(x - s * 0.72, y + s * 0.1, x - s * 0.2, y - s * 0.4, x, y - s);
  ctx.fill();
  ctx.fillStyle = "rgba(255,255,255,0.8)";
  ctx.beginPath();
  ctx.arc(x - s * 0.16, y + s * 0.18, s * 0.13, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Props and interface pieces

export function sparklePath(ctx: Ctx, x: number, y: number, outer: number, inner: number) {
  ctx.moveTo(x, y - outer);
  ctx.quadraticCurveTo(x + inner, y - inner, x + outer, y);
  ctx.quadraticCurveTo(x + inner, y + inner, x, y + outer);
  ctx.quadraticCurveTo(x - inner, y + inner, x - outer, y);
  ctx.quadraticCurveTo(x - inner, y - inner, x, y - outer);
  ctx.closePath();
}

export function drawSparkle(ctx: Ctx, x: number, y: number, r: number, color: string, alpha = 1) {
  if (alpha <= 0 || r <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = color;
  ctx.beginPath();
  sparklePath(ctx, x, y, r, r * 0.22);
  ctx.fill();
  ctx.restore();
}

/** Capsules radiating from a point: impacts, snaps and fireworks. */
export function drawBurst(ctx: Ctx, x: number, y: number, p: number, o: {
  count?: number; inner?: number; outer?: number; width?: number; colors?: string[]; spin?: number;
}) {
  if (p <= 0 || p >= 1) return;
  const { count = 8, inner = 30, outer = 90, width = 10, colors = [INK], spin = 0 } = o;
  const travel = outCubic(p);
  const length = (outer - inner) * 0.45 * (1 - p) + width;
  ctx.save();
  for (let i = 0; i < count; i++) {
    const a = spin + (i / count) * Math.PI * 2;
    const d = inner + (outer - inner) * travel;
    ctx.fillStyle = colors[i % colors.length];
    ctx.save();
    ctx.translate(x + Math.cos(a) * d, y + Math.sin(a) * d);
    ctx.rotate(a);
    ctx.beginPath();
    ctx.roundRect(-length / 2, -width / 2 * (1 - p * 0.5), length, width * (1 - p * 0.5), width / 2);
    ctx.fill();
    ctx.restore();
  }
  ctx.restore();
}

export function drawPuff(ctx: Ctx, x: number, y: number, r: number, alpha: number, color = "#FFFFFF") {
  if (alpha <= 0 || r <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = color;
  ctx.beginPath();
  ctx.arc(x, y, r, 0, Math.PI * 2);
  ctx.arc(x - r * 0.75, y + r * 0.25, r * 0.7, 0, Math.PI * 2);
  ctx.arc(x + r * 0.78, y + r * 0.22, r * 0.66, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

export interface Theme { bg: string; bar: string; ink: string; line: string; text: string; accent: string }
export const THEMES: Record<string, Theme> = {
  paper: { bg: "#FFFDF8", bar: "#F3ECE0", ink: "#4A423A", line: "rgba(90,70,40,0.14)", text: "#E6DDCD", accent: "#F4B49C" },
  terminal: { bg: "#23262C", bar: "#2F333A", ink: "#E9E6DF", line: "rgba(0,0,0,0.35)", text: "#3F4550", accent: "#7FD8A6" },
  lilac: { bg: "#FCFAFF", bar: "#ECE6FA", ink: "#4B4262", line: "rgba(80,60,120,0.14)", text: "#E4DCF5", accent: "#B79CF0" },
  butter: { bg: "#FFFCF1", bar: "#F6EDC6", ink: "#51462A", line: "rgba(110,90,30,0.14)", text: "#EDE3C0", accent: "#F0C44C" },
  wuu: { bg: "#FFFFFF", bar: "#F6F3EE", ink: INK, line: "rgba(40,40,40,0.1)", text: "#ECE8E1", accent: "#8FD8C0" },
};
export const TITLE_BAR = 44;

export interface Win {
  x: number; y: number; w: number; h: number; theme: Theme;
  engine?: string; scale?: number; tilt?: number; alpha?: number;
}

/**
 * Rounded app window. `content` draws in window space below the title bar and
 * is clipped to the window, so whole windows can scale and tumble together.
 */
export function drawWindow(ctx: Ctx, win: Win, content?: (ctx: Ctx, w: number, h: number) => void) {
  const { w, h, theme } = win;
  const scale = win.scale ?? 1;
  const alpha = win.alpha ?? 1;
  if (scale <= 0.001 || alpha <= 0) return;
  const radius = 20;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.translate(win.x + w / 2, win.y + h / 2);
  ctx.rotate(rad(win.tilt ?? 0));
  ctx.scale(scale, scale);
  ctx.translate(-w / 2, -h / 2);
  ctx.save();
  ctx.shadowColor = "rgba(80, 58, 30, 0.16)";
  ctx.shadowBlur = 38;
  ctx.shadowOffsetY = 14;
  ctx.fillStyle = theme.bg;
  ctx.beginPath();
  ctx.roundRect(0, 0, w, h, radius);
  ctx.fill();
  ctx.restore();
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(0, 0, w, h, radius);
  ctx.clip();
  ctx.fillStyle = theme.bar;
  ctx.fillRect(0, 0, w, TITLE_BAR);
  ["#FF8A7A", "#FFCB5C", "#7FD3A8"].forEach((color, i) => {
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(24 + i * 20, TITLE_BAR / 2, 6.5, 0, Math.PI * 2);
    ctx.fill();
  });
  if (win.engine) drawLogo(ctx, win.engine, w / 2, TITLE_BAR / 2, 22, theme.ink);
  if (content) {
    ctx.translate(0, TITLE_BAR);
    content(ctx, w, h - TITLE_BAR);
  }
  ctx.restore();
  ctx.strokeStyle = theme.line;
  ctx.lineWidth = 1.5;
  ctx.beginPath();
  ctx.roundRect(0, 0, w, h, radius);
  ctx.stroke();
  ctx.restore();
}

/** Text as rounded bars, typed out left to right, row by row. */
export function drawLines(ctx: Ctx, x: number, y: number, widths: number[], progress: number, colors: string[], o: {
  gap?: number; thick?: number; indent?: number[];
} = {}) {
  const { gap = 24, thick = 11, indent = [] } = o;
  const total = widths.reduce((a, b) => a + b, 0);
  let budget = total * clamp(progress);
  widths.forEach((width, i) => {
    if (budget <= 0) return;
    const shown = Math.min(width, budget);
    budget -= width;
    ctx.fillStyle = colors[i % colors.length];
    ctx.beginPath();
    ctx.roundRect(x + (indent[i] ?? 0), y + i * gap, Math.max(thick, shown), thick, thick / 2);
    ctx.fill();
  });
}

const CURSOR = new Path2D("M0 0L0 30.5L7.6 23.4L12.6 34.8L17.8 32.5L12.9 21.3L22.6 21.3Z");
export function drawCursor(ctx: Ctx, x: number, y: number, o: { scale?: number; rot?: number; press?: number; alpha?: number } = {}) {
  const { scale = 1.7, rot = 0, press = 0, alpha = 1 } = o;
  if (alpha <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.translate(x, y);
  ctx.rotate(rad(rot));
  const s = scale * (1 - 0.14 * press);
  ctx.scale(s, s);
  ctx.shadowColor = "rgba(40, 30, 20, 0.22)";
  ctx.shadowBlur = 6;
  ctx.shadowOffsetY = 2;
  ctx.fillStyle = "#FFFFFF";
  ctx.fill(CURSOR);
  ctx.shadowColor = "transparent";
  ctx.strokeStyle = INK;
  ctx.lineWidth = 2.2;
  ctx.lineJoin = "round";
  ctx.stroke(CURSOR);
  ctx.restore();
}

export function drawRipple(ctx: Ctx, x: number, y: number, p: number, color = INK) {
  if (p <= 0 || p >= 1) return;
  ctx.save();
  ctx.globalAlpha *= 1 - p;
  ctx.strokeStyle = color;
  ctx.lineWidth = 5 * (1 - p) + 1;
  ctx.beginPath();
  ctx.arc(x, y, 10 + 44 * outCubic(p), 0, Math.PI * 2);
  ctx.stroke();
  ctx.restore();
}

/** Speech bubble with waiting dots: an agent asking for attention. */
export function drawBubble(ctx: Ctx, x: number, y: number, s: number, time: number, color = "#FFFFFF", dot = INK) {
  if (s <= 0.01) return;
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(s, s);
  ctx.shadowColor = "rgba(80, 58, 30, 0.18)";
  ctx.shadowBlur = 12;
  ctx.shadowOffsetY = 4;
  ctx.fillStyle = color;
  ctx.beginPath();
  ctx.roundRect(-38, -52, 76, 42, 21);
  ctx.moveTo(-10, -12);
  ctx.lineTo(-18, 0);
  ctx.lineTo(4, -12);
  ctx.fill();
  ctx.shadowColor = "transparent";
  ctx.fillStyle = dot;
  for (let i = 0; i < 3; i++) {
    const bounce = Math.max(0, Math.sin(time * 9 - i * 0.9)) * 5;
    ctx.beginPath();
    ctx.arc(-16 + i * 16, -31 - bounce, 5, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.restore();
}

export function drawClock(ctx: Ctx, x: number, y: number, r: number, hours: number, alpha = 1) {
  if (alpha <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.translate(x, y);
  ctx.fillStyle = PAPER;
  ctx.strokeStyle = INK;
  ctx.lineWidth = r * 0.1;
  ctx.beginPath();
  ctx.arc(0, 0, r, 0, Math.PI * 2);
  ctx.fill();
  ctx.stroke();
  ctx.lineCap = "round";
  for (let i = 0; i < 12; i++) {
    const a = (i / 12) * Math.PI * 2;
    ctx.lineWidth = r * (i % 3 === 0 ? 0.07 : 0.04);
    ctx.beginPath();
    ctx.moveTo(Math.cos(a) * r * 0.72, Math.sin(a) * r * 0.72);
    ctx.lineTo(Math.cos(a) * r * 0.82, Math.sin(a) * r * 0.82);
    ctx.stroke();
  }
  const hand = (angle: number, length: number, width: number, color: string) => {
    ctx.strokeStyle = color;
    ctx.lineWidth = width;
    ctx.beginPath();
    ctx.moveTo(0, 0);
    ctx.lineTo(Math.cos(angle - Math.PI / 2) * length, Math.sin(angle - Math.PI / 2) * length);
    ctx.stroke();
  };
  hand((hours / 12) * Math.PI * 2, r * 0.45, r * 0.1, INK);
  hand(hours * Math.PI * 2, r * 0.66, r * 0.07, "#FF8A7A");
  ctx.fillStyle = INK;
  ctx.beginPath();
  ctx.arc(0, 0, r * 0.08, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// The rocket: the big job, authored as six pieces in rocket space.

export type Piece = "nose" | "cabin" | "hull" | "finL" | "finR" | "nozzle";
export const PIECES: Piece[] = ["nose", "cabin", "hull", "finL", "finR", "nozzle"];
const CORAL = "#FF8A7A";
const HULL = "#FFF4E6";
const PIECE_ART: Record<Piece, { d: string; fill: string; box: [number, number, number, number] }> = {
  nose: { d: "M-68 -118C-66 -170 -40 -212 0 -238C40 -212 66 -170 68 -118Z", fill: CORAL, box: [-68, -238, 68, -118] },
  cabin: { d: "M-68 -118L68 -118L70 -8L-70 -8Z", fill: HULL, box: [-70, -118, 70, -8] },
  hull: { d: "M-70 -8L70 -8L62 92L-62 92Z", fill: HULL, box: [-70, -8, 70, 92] },
  finL: { d: "M-67 20C-100 40 -124 82 -126 128L-110 132C-96 112 -80 102 -63 96Z", fill: CORAL, box: [-126, 20, -63, 132] },
  finR: { d: "M67 20C100 40 124 82 126 128L110 132C96 112 80 102 63 96Z", fill: CORAL, box: [63, 20, 126, 132] },
  nozzle: { d: "M-44 92L44 92L56 130L-56 130Z", fill: "#8E97A8", box: [-56, 92, 56, 130] },
};
const piecePaths = new Map<Piece, Path2D>();
const piecePath = (id: Piece) => {
  let path = piecePaths.get(id);
  if (!path) piecePaths.set(id, (path = new Path2D(PIECE_ART[id].d)));
  return path;
};

export function pieceCenter(id: Piece): [number, number] {
  const [x0, y0, x1, y1] = PIECE_ART[id].box;
  return [(x0 + x1) / 2, (y0 + y1) / 2];
}

/**
 * Draws one piece in rocket space. build 0 is a dashed blueprint outline,
 * 1 is finished; in between, colour sweeps up from the bottom.
 */
export function drawPiece(ctx: Ctx, id: Piece, build: number, o: { ghost?: string; glow?: number; porthole?: (ctx: Ctx) => void } = {}) {
  const art = PIECE_ART[id];
  const path = piecePath(id);
  const [x0, y0, x1, y1] = art.box;
  ctx.save();
  ctx.lineJoin = "round";
  if (build < 1) {
    ctx.setLineDash([9, 8]);
    ctx.strokeStyle = o.ghost ?? "#6F95E6";
    ctx.lineWidth = 3.5;
    ctx.stroke(path);
    ctx.setLineDash([]);
  }
  if (build > 0) {
    ctx.save();
    const top = y1 - (y1 - y0 + 8) * clamp(build);
    ctx.beginPath();
    ctx.rect(x0 - 10, top, x1 - x0 + 20, y1 - top + 10);
    ctx.clip();
    fillPiece(ctx, id, path, o.porthole);
    ctx.restore();
    if (build < 1) {
      ctx.fillStyle = "rgba(255,255,255,0.9)";
      ctx.beginPath();
      ctx.roundRect(x0 - 6, y1 - (y1 - y0 + 8) * build - 3, x1 - x0 + 12, 6, 3);
      ctx.fill();
    }
  }
  if (o.glow) {
    ctx.globalAlpha = o.glow;
    ctx.strokeStyle = "#FFFFFF";
    ctx.lineWidth = 10;
    ctx.stroke(path);
  }
  ctx.restore();
}

function fillPiece(ctx: Ctx, id: Piece, path: Path2D, porthole?: (ctx: Ctx) => void) {
  const art = PIECE_ART[id];
  ctx.fillStyle = art.fill;
  ctx.fill(path);
  if (id === "hull") {
    ctx.save();
    ctx.clip(path);
    ctx.fillStyle = "#8FD8C0";
    ctx.fillRect(-80, 22, 160, 22);
    ctx.restore();
  }
  if (id === "cabin") {
    ctx.fillStyle = "#8EC5FF";
    ctx.beginPath();
    ctx.arc(0, -63, 36, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#DDEFFF";
    ctx.beginPath();
    ctx.arc(0, -63, 27, 0, Math.PI * 2);
    ctx.fill();
    if (porthole) {
      ctx.save();
      ctx.beginPath();
      ctx.arc(0, -63, 27, 0, Math.PI * 2);
      ctx.clip();
      porthole(ctx);
      ctx.restore();
    }
    ctx.strokeStyle = "rgba(255,255,255,0.9)";
    ctx.lineWidth = 5;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.arc(0, -63, 18, rad(200), rad(250));
    ctx.stroke();
  }
  ctx.strokeStyle = "#3A3440";
  ctx.lineWidth = 4;
  ctx.stroke(path);
}

export function drawFlame(ctx: Ctx, x: number, y: number, s: number, time: number) {
  if (s <= 0) return;
  const flicker = 1 + Math.sin(time * 40) * 0.08 + Math.sin(time * 23) * 0.06;
  const layers: [string, number, number][] = [["#FF8A5B", 1, 1], ["#FFC24B", 0.72, 0.8], ["#FFF3C4", 0.42, 0.6]];
  for (const [color, width, length] of layers) {
    const w = 46 * width * s, l = 150 * length * s * flicker;
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.moveTo(x - w, y);
    ctx.quadraticCurveTo(x - w * 0.9, y + l * 0.55, x, y + l);
    ctx.quadraticCurveTo(x + w * 0.9, y + l * 0.55, x + w, y);
    ctx.closePath();
    ctx.fill();
  }
}

export function drawBlueprint(ctx: Ctx, x: number, y: number, w: number, h: number) {
  ctx.save();
  ctx.shadowColor = "rgba(40, 70, 140, 0.18)";
  ctx.shadowBlur = 30;
  ctx.shadowOffsetY = 12;
  ctx.fillStyle = "#D9E8FF";
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, 26);
  ctx.fill();
  ctx.restore();
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, 26);
  ctx.clip();
  ctx.strokeStyle = "rgba(255,255,255,0.75)";
  ctx.lineWidth = 2;
  for (let gx = x + 30; gx < x + w; gx += 34) {
    ctx.beginPath();
    ctx.moveTo(gx, y);
    ctx.lineTo(gx, y + h);
    ctx.stroke();
  }
  for (let gy = y + 30; gy < y + h; gy += 34) {
    ctx.beginPath();
    ctx.moveTo(x, gy);
    ctx.lineTo(x + w, gy);
    ctx.stroke();
  }
  ctx.restore();
  ctx.strokeStyle = "#A9C6F5";
  ctx.lineWidth = 4;
  ctx.beginPath();
  ctx.roundRect(x, y, w, h, 26);
  ctx.stroke();
}
