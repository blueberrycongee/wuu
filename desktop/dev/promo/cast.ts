import { agentSkin, drawSparkle, extent, THEMES, type Ball, type Gear, type Skin, type Theme } from "./art";
import { AVATAR_HUES } from "../../src/renderer/DefaultAvatar";
import { clamp, lerp, rad, type Hop } from "./motion";
import { cutout, TOMATO, WHITE } from "./paper";

export type Ctx = CanvasRenderingContext2D;
export const W = 1920;
export const H = 1080;

export const NIGHT = "#15161A";
const CORAL = "#CA9A86";
const SUN = "#CCB780";
const MINT = "#90AFA0";
const SKY = "#9AAEBF";

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
export const DEVIN: Agent = { engine: "devin", skin: agentSkin(AVATAR_HUES[10], "rounded-square"), theme: THEMES.wuu };
export const GROK: Agent = { engine: "grok", skin: agentSkin(AVATAR_HUES[3], "round"), theme: THEMES.wuu };
export const HERMES: Agent = { engine: "hermes", skin: agentSkin(AVATAR_HUES[9], "capsule"), theme: THEMES.wuu };

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


/** Little stars circling a dazed head. */
export function drawDaze(ctx: Ctx, x: number, y: number, rx: number, t: number, alpha: number) {
  if (alpha <= 0) return;
  for (let i = 0; i < 3; i++) {
    const a = t * 4 + (i / 3) * Math.PI * 2;
    drawSparkle(ctx, x + Math.cos(a) * rx, y + Math.sin(a) * rx * 0.32, 11 + 3 * Math.sin(a), [SUN, CORAL, SKY][i], alpha * (0.6 + 0.4 * Math.sin(a)));
  }
}


const STEEL = "#C9CDD1";
const PIVOT = "#4A4B50";

/**
 * Paper scissors pointing along +x from the pivot at (x, y). `open` is the
 * angle between the blades in degrees; 0 is shut.
 */
export function drawScissors(ctx: Ctx, x: number, y: number, size: number, angle: number, open: number) {
  ctx.save();
  ctx.translate(x, y);
  ctx.rotate(rad(angle));
  ctx.scale(size / 100, size / 100);
  for (const side of [-1, 1]) {
    ctx.save();
    ctx.rotate(rad((side * open) / 2));
    const blade = new Path2D();
    blade.moveTo(-6, side * 5);
    blade.quadraticCurveTo(40, side * 9, 98, side * 1.5);
    blade.lineTo(98, 0);
    blade.quadraticCurveTo(40, -side * 2, -6, -side * 4);
    blade.closePath();
    cutout(ctx, blade, STEEL, { rim: 2.5, dx: 2, dy: 3 });
    ctx.strokeStyle = PIVOT;
    ctx.lineWidth = 1.6;
    ctx.stroke(blade);
    // Handle: a finger loop on a short shank, cut from red card.
    const handle = new Path2D();
    handle.moveTo(-4, side * 2);
    handle.lineTo(-22, side * 12);
    handle.lineTo(-16, side * 18);
    handle.lineTo(2, side * 7);
    handle.closePath();
    handle.ellipse(-36, side * 18, 20, 14, rad(side * -18), 0, Math.PI * 2);
    cutout(ctx, handle, TOMATO, { rim: 2.5, dx: 2, dy: 3, grain: 0.6 });
    // The finger hole keeps its white margin, as a cut-out would.
    ctx.fillStyle = WHITE;
    ctx.beginPath();
    ctx.ellipse(-36, side * 18, 10, 6, rad(side * -18), 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }
  ctx.fillStyle = PIVOT;
  ctx.beginPath();
  ctx.arc(0, 0, 4.5, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}
