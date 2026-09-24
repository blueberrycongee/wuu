import { drawSparkle, extent, pastel, THEMES, WUU, type Ball, type Gear, type Skin, type Theme } from "./art";
import { clamp, lerp, outBack, rad, smooth, type Hop } from "./motion";

export type Ctx = CanvasRenderingContext2D;
export const W = 1920;
export const H = 1080;

export const CREAM = "#F7F2E8";
export const NIGHT = "#15161A";
export const CORAL = "#FF8A7A";
export const SUN = "#FFD166";
export const MINT = "#8FD8C0";
export const SKY = "#8EC5FF";
export const LILAC = "#C8B5F5";
export const CONFETTI = [CORAL, SUN, MINT, SKY, LILAC];

// Each harness is a Wuu-family creature: a pastel body, dark eyes, and an
// antenna carrying the engine's own mark, so no caption is needed.
export interface Agent { engine: string; skin: Skin; theme: Theme; gear?: Gear }
const MINT_THEME: Theme = {
  bg: "#F7FFFB", bar: "#DDF3EA", ink: "#2F5446", line: "rgba(40,110,80,0.14)", text: "#D6EDE3", accent: "#5FC79E",
};
export const CLAUDE: Agent = { engine: "claude", skin: pastel(16, "round"), theme: THEMES.paper };
export const CODEX: Agent = { engine: "codex", skin: pastel(205, "capsule"), theme: THEMES.terminal };
export const CURSOR: Agent = { engine: "cursor", skin: pastel(266, "squircle"), theme: THEMES.lilac };
export const OPENCODE: Agent = { engine: "opencode", skin: pastel(44, "diamond"), theme: THEMES.butter };
export const PI: Agent = { engine: "pi", skin: pastel(156, "round"), theme: MINT_THEME, gear: "leaf" };
export const BUILDER: Agent = { engine: "wuu", skin: WUU, theme: THEMES.wuu, gear: "hardhat" };

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

/** A classic cartoon iris: everything outside the circle goes dark. */
export function iris(ctx: Ctx, x: number, y: number, r: number, inside: () => void) {
  ctx.save();
  ctx.fillStyle = NIGHT;
  ctx.fillRect(0, 0, W, H);
  if (r > 0) {
    ctx.beginPath();
    ctx.arc(x, y, r, 0, Math.PI * 2);
    ctx.clip();
    inside();
  }
  ctx.restore();
}

export function fill(ctx: Ctx, color: string) {
  ctx.fillStyle = color;
  ctx.fillRect(0, 0, W, H);
}

/** Paper-dot texture drawn in world space so it moves with the camera. */
export function dotGrid(ctx: Ctx, x0: number, y0: number, x1: number, y1: number, color = "rgba(120, 96, 60, 0.13)", step = 48) {
  ctx.fillStyle = color;
  ctx.beginPath();
  for (let y = Math.floor(y0 / step) * step; y < y1; y += step) {
    for (let x = Math.floor(x0 / step) * step; x < x1; x += step) {
      ctx.moveTo(x + 2.6, y);
      ctx.arc(x, y, 2.6, 0, Math.PI * 2);
    }
  }
  ctx.fill();
}

/** Darken everything outside a circle, over whatever is already drawn. */
export function irisClose(ctx: Ctx, x: number, y: number, r: number) {
  ctx.save();
  ctx.fillStyle = NIGHT;
  ctx.beginPath();
  ctx.rect(0, 0, W, H);
  if (r > 0) ctx.arc(x, y, r, 0, Math.PI * 2, true);
  ctx.fill("evenodd");
  ctx.restore();
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

export function drawOrb(ctx: Ctx, x: number, y: number, r: number, t: number) {
  ctx.save();
  const glow = ctx.createRadialGradient(x, y, 0, x, y, r * 2.4);
  glow.addColorStop(0, "rgba(255, 236, 160, 0.9)");
  glow.addColorStop(1, "rgba(255, 236, 160, 0)");
  ctx.fillStyle = glow;
  ctx.beginPath();
  ctx.arc(x, y, r * 2.4, 0, Math.PI * 2);
  ctx.fill();
  ctx.fillStyle = SUN;
  ctx.beginPath();
  ctx.arc(x, y, r, 0, Math.PI * 2);
  ctx.fill();
  drawSparkle(ctx, x, y, r * 0.8, "#FFFFFF", 0.9);
  drawSparkle(ctx, x + Math.cos(t * 9) * r * 1.7, y + Math.sin(t * 9) * r * 1.7, r * 0.45, "#FFFFFF", 0.8);
  ctx.restore();
}
