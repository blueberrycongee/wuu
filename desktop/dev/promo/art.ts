import { _layout, palette } from "blobatar";
import { SHAPES as AVATAR_SHAPES, type Shape } from "blobatar/blob";
import icon from "../../../assets/app-icon-source.json";
import { ENGINE_ICON_EVENODD, ENGINE_ICON_PATHS } from "../../src/renderer/EngineIcons";
import { WUU_MASCOT_NAME, WUU_MASCOT_TRAITS } from "../../src/renderer/wuu-mascot-spec";
import { clamp, mixColor, outBack, outCubic, rad, smooth, type Eyes } from "./motion";
import { cutout } from "./paper";

type Ctx = CanvasRenderingContext2D;

export const INK = "#2A2B2E";

// ---------------------------------------------------------------------------
// Characters

export type Gear = "leaf";
export interface Skin { body: string; lo: string; eye: string; shape: Shape }

/** The approved app icon palette. */
export const WUU: Skin = {
  body: icon.bodyColor, lo: icon.bodyShadow, eye: icon.eyeColor, shape: "round",
};

/** Product identity colours without simulated surface lighting. */
export function agentSkin(hue: number, shape: Shape): Skin {
  const colors = palette(hue);
  return {
    body: colors.head!, lo: mixColor(colors.head!, "#101112", 0.16),
    eye: colors.eye!, shape,
  };
}

// Bodies are authored at radius 100 and scaled to the requested radius.
const identity = _layout(WUU_MASCOT_NAME, { traits: WUU_MASCOT_TRAITS });
const SHAPES = Object.fromEntries(AVATAR_SHAPES.map(({ id, trait }) => {
  const { body } = _layout(WUU_MASCOT_NAME, { traits: { ...WUU_MASCOT_TRAITS, shape: trait } });
  const path = new Path2D();
  // One scale preserves the product's optical sizing across different shapes.
  path.addPath(new Path2D(body.path), new DOMMatrix().scale(100 / identity.body.rx).translate(-body.cx, -body.cy));
  return [id, { path, rx: body.rx / identity.body.rx, ry: body.ry / identity.body.rx }];
})) as Record<Shape, { path: Path2D; rx: number; ry: number }>;
/** Half-extents of a body relative to its radius, for standing it on a floor. */
export const extent = (skin: Skin) => SHAPES[skin.shape];

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
  marks?: number | number[]; markAngle?: number; markColor?: string;
  skin?: Skin; gear?: Gear; engine?: string; sway?: number;
  sweat?: number; alpha?: number;
  /** Floor height for the contact shadow; omitted when the ball floats. */
  ground?: number;
  /** Body opacity alone, so eyes can glow in the dark before the lights come on. */
  bodyAlpha?: number;
  /** Contact shadow opacity, so a character can leave the floor it stood on. */
  shadow?: number;
  /** Antenna growth from 0 (none) to 1 (full), for characters that sprout one. */
  antenna?: number;
  /** Width of the white paper margin in pixels; 0 for a bare shape. */
  rim?: number;
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
  if (b.ground !== undefined && (b.shadow ?? 1) > 0) {
    ctx.save();
    ctx.globalAlpha *= b.shadow ?? 1;
    drawShadow(ctx, x, b.ground, r * form.rx, (b.ground - y - bottom) / (r * 2.4));
    ctx.restore();
  }
  // Squash and stretch pivot on the contact point, like a real soft ball.
  ctx.translate(x, y + bottom);
  ctx.rotate(rad(b.tilt ?? 0));
  ctx.scale(b.sx ?? 1, b.sy ?? 1);
  ctx.translate(0, -bottom);
  if (b.engine && (b.antenna ?? 1) > 0) drawAntenna(ctx, r, b.engine, b.sway ?? 0, skin, form.ry, b.antenna ?? 1);
  ctx.save();
  ctx.globalAlpha *= b.bodyAlpha ?? 1;
  ctx.scale(r / 100, r / 100);
  // Bodies are paper cut-outs: a scissor margin and a hard shadow sized in
  // screen pixels, whatever the character's radius.
  const k = 100 / r;
  const rim = b.rim ?? Math.min(7, 2 + r * 0.05);
  cutout(ctx, form.path, skin.body, { rim: rim * k, dx: rim * 0.7 * k, dy: rim * k, grain: 0.45 });
  ctx.restore();
  drawFace(ctx, r, b, skin);
  if (b.gear) drawGear(ctx, r, form.ry);
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

/**
 * The icon's three greeting strokes, popping out one after another. A list
 * gives each stroke its own progress, so each can land on its own note.
 */
function drawMarks(ctx: Ctx, r: number, angle: number, amount: number | number[], color: string) {
  const k = r / icon.radius;
  ctx.fillStyle = color;
  icon.marks.forEach((mark, i) => {
    const p = typeof amount === "number" ? clamp(amount * 1.6 - i * 0.3) : clamp(amount[i]);
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

function drawAntenna(ctx: Ctx, r: number, engine: string, sway: number, skin: Skin, ry: number, grow: number) {
  const baseY = -r * ry * 0.86;
  const tipX = sway * r * 0.42 * grow;
  const tipY = baseY + (-r * ry - r * 0.5 + Math.abs(sway) * r * 0.08 - baseY) * grow;
  ctx.strokeStyle = skin.lo;
  ctx.lineWidth = r * 0.075;
  ctx.lineCap = "round";
  ctx.beginPath();
  ctx.moveTo(0, baseY);
  ctx.quadraticCurveTo(sway * r * 0.04, (baseY + tipY) / 2, tipX, tipY);
  ctx.stroke();
  const disc = r * 0.38 * grow;
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

function drawGear(ctx: Ctx, r: number, ry: number) {
  const top = -r * ry;
  ctx.save();
  ctx.lineCap = "round";
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
  ctx.restore();
}

function drawShadow(ctx: Ctx, x: number, y: number, rx: number, lift: number) {
  const k = 1 - clamp(lift) * 0.55;
  ctx.fillStyle = `rgba(35, 38, 37, ${0.1 * k})`;
  ctx.beginPath();
  ctx.ellipse(x, y, rx * 0.88 * k, rx * 0.16 * k, 0, 0, Math.PI * 2);
  ctx.fill();
}

function drawSweat(ctx: Ctx, x: number, y: number, s: number, alpha: number) {
  if (alpha <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = "#8EA6B6";
  ctx.beginPath();
  ctx.moveTo(x, y - s);
  ctx.bezierCurveTo(x + s * 0.2, y - s * 0.4, x + s * 0.72, y + s * 0.1, x, y + s * 0.62);
  ctx.bezierCurveTo(x - s * 0.72, y + s * 0.1, x - s * 0.2, y - s * 0.4, x, y - s);
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Props and interface pieces

function sparklePath(ctx: Ctx, x: number, y: number, outer: number, inner: number) {
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

export interface Theme { bg: string; bar: string; ink: string; line: string; text: string; accent: string }
export const THEMES: Record<string, Theme> = {
  paper: { bg: "#FDFCFB", bar: "#F0EEEB", ink: INK, line: "rgba(40,40,40,0.1)", text: "#DEDCD7", accent: "#C59A83" },
  terminal: { bg: "#23262C", bar: "#2F333A", ink: "#E9E6DF", line: "rgba(0,0,0,0.35)", text: "#3F4550", accent: "#7FD8A6" },
  lilac: { bg: "#FCFCFD", bar: "#ECECEF", ink: INK, line: "rgba(40,40,40,0.1)", text: "#DEDEE5", accent: "#A6A1BB" },
  butter: { bg: "#FDFCF9", bar: "#F0EEE7", ink: INK, line: "rgba(40,40,40,0.1)", text: "#E1DDD2", accent: "#C0AC7B" },
  wuu: { bg: "#FFFFFF", bar: "#F1F2F0", ink: INK, line: "rgba(40,40,40,0.1)", text: "#E4E6E2", accent: "#90A49D" },
};
export const TITLE_BAR = 44;

export interface Win {
  x: number; y: number; w: number; h: number; theme: Theme;
  engine?: string; scale?: number; tilt?: number; alpha?: number;
  /** Paper margin in screen pixels. */
  rim?: number;
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
  const frame = new Path2D();
  frame.roundRect(0, 0, w, h, radius);
  cutout(ctx, frame, theme.bg, { rim: (win.rim ?? 7) / scale, dx: 6 / scale, dy: 9 / scale, grain: 0.7 });
  ctx.save();
  ctx.clip(frame);
  ctx.fillStyle = theme.bar;
  ctx.fillRect(0, 0, w, TITLE_BAR);
  [0, 1, 2].forEach((i) => {
    ctx.fillStyle = theme.text;
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
