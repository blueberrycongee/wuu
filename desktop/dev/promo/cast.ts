import { agentSkin, drawSparkle, extent, THEMES, type Ball, type Gear, type Skin, type Theme } from "./art";
import { AVATAR_HUES } from "../../src/renderer/DefaultAvatar";
import { clamp, lerp, outBack, rad, smooth, type Hop } from "./motion";

export type Ctx = CanvasRenderingContext2D;
export const W = 1920;
export const H = 1080;
export const DESK_HOME = { x: 1745, floor: 1012, r: 118 };

export const CREAM = "#F5F5F2";
export const NIGHT = "#15161A";
export const CORAL = "#CA9A86";
export const SUN = "#CCB780";
export const MINT = "#90AFA0";
export const SKY = "#9AAEBF";

// Each harness uses the product's shape and palette rules, with an
// antenna carrying the engine's own mark, so no caption is needed.
export interface Agent { engine: string; skin: Skin; theme: Theme; gear?: Gear }
const MINT_THEME: Theme = {
  bg: "#FBFCFB", bar: "#EBF0EC", ink: "#35423C", line: "rgba(40,40,40,0.1)", text: "#DCE5DF", accent: MINT,
};
export const CLAUDE: Agent = { engine: "claude", skin: agentSkin(AVATAR_HUES[0], "round"), theme: THEMES.paper };
export const CODEX: Agent = { engine: "codex", skin: agentSkin(AVATAR_HUES[6], "capsule"), theme: THEMES.terminal };
export const CURSOR: Agent = { engine: "cursor", skin: agentSkin(AVATAR_HUES[8], "rounded-square"), theme: THEMES.lilac };
export const OPENCODE: Agent = { engine: "opencode", skin: agentSkin(AVATAR_HUES[2], "diamond"), theme: THEMES.butter };
export const PI: Agent = { engine: "pi", skin: agentSkin(AVATAR_HUES[4], "triangle"), theme: MINT_THEME, gear: "leaf" };

// ---------------------------------------------------------------------------
// Staging

const REST: Hop = { lift: 0, sx: 1, sy: 1 };

/** A body resting on (or hopping above) a floor line, with its contact shadow. */
export function stand(skin: Skin, x: number, floor: number, r: number, h: Hop = REST, height = r * 1.4): Ball {
  return { x, y: floor - r * extent(skin).ry - h.lift * height, r, sx: h.sx, sy: h.sy, ground: floor, skin };
}

export function agent(a: Agent, x: number, floor: number, r: number, h: Hop = REST, height?: number): Ball {
  return { ...stand(a.skin, x, floor, r, h, height), engine: a.engine, gear: a.gear };
}

/** Face direction toward a point, as yaw/pitch for drawBall. */
export function look(x: number, y: number, tx: number, ty: number, k = 1) {
  return {
    yaw: clamp((tx - x) / 700, -1, 1) * 0.62 * k,
    pitch: clamp((y - ty) / 700, -1, 1) * 0.5 * k,
  };
}

/** A point along a hop arc from one spot to another. */
export function arc(p: number, x0: number, y0: number, x1: number, y1: number, height: number): [number, number] {
  return [lerp(x0, x1, p), lerp(y0, y1, p) - Math.sin(p * Math.PI) * height];
}

/** Steady work that lands in small beats, like hammer taps. */
export function stairs(u: number, n: number) {
  const k = clamp(u) * n;
  const whole = Math.floor(k);
  return Math.min(1, (whole + smooth(k - whole)) / n);
}

// ---------------------------------------------------------------------------
// Camera and transitions

export interface Camera { x: number; y: number; zoom: number; rot?: number }

/** Match the outgoing and incoming hero on screen while the setting dissolves. */
export function matchCamera(ball: Ball): Camera {
  const zoom = 135 / ball.r;
  return { x: ball.x - 100 / zoom, y: ball.y - 160 / zoom, zoom };
}

export function withCamera(ctx: Ctx, cam: Camera, draw: () => void) {
  ctx.save();
  ctx.translate(W / 2, H / 2);
  ctx.rotate(rad(cam.rot ?? 0));
  ctx.scale(cam.zoom, cam.zoom);
  ctx.translate(-cam.x, -cam.y);
  draw();
  ctx.restore();
}

export function toScreen(cam: Camera, x: number, y: number): [number, number] {
  return [W / 2 + (x - cam.x) * cam.zoom, H / 2 + (y - cam.y) * cam.zoom];
}

export function fill(ctx: Ctx, color: string) {
  ctx.fillStyle = color;
  ctx.fillRect(0, 0, W, H);
}

// ---------------------------------------------------------------------------
// Small shared props

export function drawSpinner(ctx: Ctx, x: number, y: number, r: number, t: number, color: string) {
  ctx.save();
  ctx.strokeStyle = color;
  ctx.lineWidth = r * 0.38;
  ctx.lineCap = "round";
  const a = t * 6;
  ctx.beginPath();
  ctx.arc(x, y, r, a, a + Math.PI * 1.3);
  ctx.stroke();
  ctx.restore();
}

export function drawCheck(ctx: Ctx, x: number, y: number, r: number, p: number) {
  if (p <= 0) return;
  const s = outBack(clamp(p * 1.6));
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(s, s);
  ctx.fillStyle = "#5FC79E";
  ctx.beginPath();
  ctx.arc(0, 0, r, 0, Math.PI * 2);
  ctx.fill();
  const draw = clamp(p * 2 - 0.5);
  ctx.strokeStyle = "#FFFFFF";
  ctx.lineWidth = r * 0.3;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  ctx.moveTo(-r * 0.42, 0);
  const [mx, my] = [-r * 0.1, r * 0.32];
  const [ex, ey] = [r * 0.46, -r * 0.34];
  if (draw < 0.4) ctx.lineTo(lerp(-r * 0.42, mx, draw / 0.4), lerp(0, my, draw / 0.4));
  else {
    ctx.lineTo(mx, my);
    ctx.lineTo(lerp(mx, ex, (draw - 0.4) / 0.6), lerp(my, ey, (draw - 0.4) / 0.6));
  }
  ctx.stroke();
  ctx.restore();
}

/** Little stars circling a dazed head. */
export function drawDaze(ctx: Ctx, x: number, y: number, rx: number, t: number, alpha: number) {
  if (alpha <= 0) return;
  for (let i = 0; i < 3; i++) {
    const a = t * 4 + (i / 3) * Math.PI * 2;
    drawSparkle(ctx, x + Math.cos(a) * rx, y + Math.sin(a) * rx * 0.32, 11 + 3 * Math.sin(a), [SUN, CORAL, SKY][i], alpha * (0.6 + 0.4 * Math.sin(a)));
  }
}

/** Three rising dots: the wordless "hmm…". */
export function drawThinking(ctx: Ctx, x: number, y: number, s: number, p: number) {
  [0, 1, 2].forEach((i) => {
    const k = clamp(p * 3 - i);
    if (k <= 0) return;
    const r = (9 + i * 6) * s * outBack(k);
    ctx.fillStyle = "#FFFFFF";
    ctx.strokeStyle = "rgba(60, 50, 40, 0.18)";
    ctx.lineWidth = 3 * s;
    ctx.beginPath();
    ctx.arc(x + i * 34 * s, y - i * 40 * s, r, 0, Math.PI * 2);
    ctx.fill();
    ctx.stroke();
  });
}
