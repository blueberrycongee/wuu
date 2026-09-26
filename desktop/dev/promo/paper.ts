// The collage and comic kit: paper, cut-out stickers, torn edges, tape,
// halftone, bursts, speed lines and ransom-note lettering. Every random
// choice comes from a seed, so each frame redraws identically.
import { clamp, lerp, outBack, rad, smooth } from "./motion";

type Ctx = CanvasRenderingContext2D;

export const INK = "#26272B";
export const PAPER = "#F3EEE4";
export const WHITE = "#FFFDF8";
export const KRAFT = "#D9BD94";
const SHADOW = "rgba(52, 38, 22, 0.2)";
// Magazine colours for lettering scraps and bursts.
export const TOMATO = "#E2583E";
export const MUSTARD = "#EDBE45";
const TEAL = "#3E9C9A";
const BLUSH = "#F2B8B0";
const BLUE = "#4F7CD6";

/** Deterministic PRNG in [0, 1). */
export function rng(seed: number) {
  let a = (seed * 2654435761) >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Hand-made jitter that changes a few times a second, like re-posed stop motion. */
export function boil(t: number, seed: number, amount = 1, rate = 8) {
  const r = rng(Math.floor(t * rate) * 131 + seed * 7919);
  return { x: (r() - 0.5) * 2 * amount, y: (r() - 0.5) * 2 * amount, a: (r() - 0.5) * 2 * amount * 0.35 };
}

// ---------------------------------------------------------------------------
// Paper surfaces

const grains = new WeakMap<Ctx, CanvasPattern>();
const GRAIN = (() => {
  const size = 384;
  const c = document.createElement("canvas");
  c.width = c.height = size;
  const g = c.getContext("2d")!;
  const r = rng(7);
  for (let i = 0; i < 5200; i++) {
    const dark = r() < 0.55;
    g.fillStyle = dark ? `rgba(70, 50, 30, ${0.03 + r() * 0.05})` : `rgba(255, 255, 255, ${0.05 + r() * 0.08})`;
    g.fillRect(r() * size, r() * size, 1 + r() * 1.6, 1 + r() * 1.6);
  }
  // Fibres.
  g.lineCap = "round";
  for (let i = 0; i < 140; i++) {
    const x = r() * size, y = r() * size, a = r() * Math.PI, l = 6 + r() * 18;
    g.strokeStyle = `rgba(90, 70, 45, ${0.04 + r() * 0.05})`;
    g.lineWidth = 0.6 + r() * 0.6;
    g.beginPath();
    g.moveTo(x, y);
    g.quadraticCurveTo(x + Math.cos(a) * l * 0.5 + (r() - 0.5) * 4, y + Math.sin(a) * l * 0.5, x + Math.cos(a) * l, y + Math.sin(a) * l);
    g.stroke();
  }
  return c;
})();

function grainPattern(ctx: Ctx) {
  let p = grains.get(ctx);
  if (!p) grains.set(ctx, (p = ctx.createPattern(GRAIN, "repeat")!));
  return p;
}

/** Paper grain over whatever is inside the current path or clip. */
export function grain(ctx: Ctx, x: number, y: number, w: number, h: number, alpha = 1) {
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = grainPattern(ctx);
  ctx.fillRect(x, y, w, h);
  ctx.restore();
}

/** A sheet of paper filling a rectangle, grain included. */
export function sheet(ctx: Ctx, x: number, y: number, w: number, h: number, color = PAPER) {
  ctx.fillStyle = color;
  ctx.fillRect(x, y, w, h);
  grain(ctx, x, y, w, h);
}

export interface Cut { rim?: number; shadow?: number; dx?: number; dy?: number; grain?: number; rimColor?: string }

/**
 * A shape cut from paper and stuck down: a hard offset shadow, a white
 * scissor margin, the coloured face and its grain.
 */
export function cutout(ctx: Ctx, path: Path2D, fill: string | CanvasPattern, o: Cut = {}) {
  const { rim = 7, shadow = 1, dx = 5, dy = 7, rimColor = WHITE } = o;
  ctx.save();
  ctx.lineJoin = "round";
  if (shadow > 0) {
    ctx.save();
    ctx.translate(dx, dy);
    ctx.globalAlpha *= shadow;
    ctx.fillStyle = SHADOW;
    ctx.strokeStyle = SHADOW;
    ctx.lineWidth = rim * 2;
    ctx.fill(path);
    if (rim > 0) ctx.stroke(path);
    ctx.restore();
  }
  if (rim > 0) {
    ctx.strokeStyle = rimColor;
    ctx.lineWidth = rim * 2;
    ctx.stroke(path);
  }
  ctx.fillStyle = fill;
  ctx.fill(path);
  if ((o.grain ?? 1) > 0) {
    ctx.save();
    ctx.clip(path);
    ctx.globalAlpha *= o.grain ?? 1;
    ctx.fillStyle = grainPattern(ctx);
    ctx.fill(path);
    ctx.restore();
  }
  ctx.restore();
}

/** A rectangle with scissor-cut (slightly uneven) straight edges. */
export function clipping(x: number, y: number, w: number, h: number, seed: number, wander = 2.2) {
  const r = rng(seed);
  const p = new Path2D();
  const corners: [number, number][] = [[x, y], [x + w, y], [x + w, y + h], [x, y + h]];
  corners.forEach(([cx, cy], i) => {
    const px = cx + (r() - 0.5) * wander * 2, py = cy + (r() - 0.5) * wander * 2;
    if (i === 0) p.moveTo(px, py);
    else p.lineTo(px, py);
  });
  p.closePath();
  return p;
}

/** Points along a torn edge from (x0, y0) to (x1, y1); the tear wanders to one side. */
export function tearLine(x0: number, y0: number, x1: number, y1: number, seed: number, amp = 9, step = 14): [number, number][] {
  const r = rng(seed);
  const len = Math.hypot(x1 - x0, y1 - y0);
  const n = Math.max(2, Math.round(len / step));
  const nx = -(y1 - y0) / len, ny = (x1 - x0) / len;
  const pts: [number, number][] = [];
  let drift = 0;
  for (let i = 0; i <= n; i++) {
    const k = i / n;
    drift = i === 0 || i === n ? 0 : clamp(drift + (r() - 0.5) * amp * 0.9, -amp, amp);
    const jag = i === 0 || i === n ? 0 : (r() - 0.5) * amp * 0.7;
    pts.push([lerp(x0, x1, k) + nx * (drift + jag), lerp(y0, y1, k) + ny * (drift + jag)]);
  }
  return pts;
}


/** A strip of masking tape, slightly see-through, with zig-zag ends. */
export function tape(ctx: Ctx, x: number, y: number, w: number, angle: number, seed: number, alpha = 1) {
  if (alpha <= 0) return;
  const r = rng(seed);
  const h = 34;
  ctx.save();
  ctx.translate(x, y);
  ctx.rotate(rad(angle));
  ctx.globalAlpha *= alpha;
  const p = new Path2D();
  p.moveTo(-w / 2, -h / 2);
  for (let i = 1; i <= 5; i++) p.lineTo(-w / 2 + (i % 2 ? 4 : 0) + (r() - 0.5) * 3, -h / 2 + (h * i) / 5);
  p.lineTo(w / 2, h / 2);
  for (let i = 4; i >= 0; i--) p.lineTo(w / 2 - (i % 2 ? 4 : 0) + (r() - 0.5) * 3, -h / 2 + (h * i) / 5);
  p.closePath();
  ctx.fillStyle = "rgba(236, 222, 180, 0.72)";
  ctx.fill(p);
  ctx.clip(p);
  ctx.fillStyle = grainPattern(ctx);
  ctx.fill(p);
  ctx.fillStyle = "rgba(255,255,255,0.18)";
  ctx.fillRect(-w / 2, -h / 2, w, h * 0.3);
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Print and comic devices

const dotTiles = new Map<string, HTMLCanvasElement>();
const dotPatterns = new WeakMap<Ctx, Map<string, CanvasPattern>>();

/** A Ben-Day dot tint, screened at 45° like a comic print. */
export function dots(ctx: Ctx, color: string, spacing = 14, radius = 3.4): CanvasPattern {
  const key = `${color}/${spacing}/${radius}`;
  let tile = dotTiles.get(key);
  if (!tile) {
    tile = document.createElement("canvas");
    tile.width = tile.height = spacing;
    const g = tile.getContext("2d")!;
    g.fillStyle = color;
    for (const [cx, cy] of [[0, 0], [spacing, 0], [0, spacing], [spacing, spacing], [spacing / 2, spacing / 2]]) {
      g.beginPath();
      g.arc(cx, cy, radius, 0, Math.PI * 2);
      g.fill();
    }
    dotTiles.set(key, tile);
  }
  let byCtx = dotPatterns.get(ctx);
  if (!byCtx) dotPatterns.set(ctx, (byCtx = new Map()));
  let p = byCtx.get(key);
  if (!p) {
    p = ctx.createPattern(tile, "repeat")!;
    p.setTransform(new DOMMatrix().rotate(45));
    byCtx.set(key, p);
  }
  return p;
}

/** Spiky comic burst. */
export function burstPath(x: number, y: number, r: number, spikes: number, seed: number, depth = 0.34) {
  const rand = rng(seed);
  const p = new Path2D();
  for (let i = 0; i < spikes * 2; i++) {
    const a = (i / (spikes * 2)) * Math.PI * 2 - Math.PI / 2 + (rand() - 0.5) * 0.12;
    const rr = i % 2 === 0 ? r * (0.92 + rand() * 0.2) : r * (1 - depth) * (0.9 + rand() * 0.15);
    const px = x + Math.cos(a) * rr, py = y + Math.sin(a) * rr * 0.86;
    if (i === 0) p.moveTo(px, py);
    else p.lineTo(px, py);
  }
  p.closePath();
  return p;
}

/** Concentration lines closing in on a point, leaving `clear` radius empty. */
export function focusLines(ctx: Ctx, x: number, y: number, clear: number, reach: number, n: number, seed: number, color = INK, alpha = 1) {
  if (alpha <= 0) return;
  const r = rng(seed);
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.fillStyle = color;
  for (let i = 0; i < n; i++) {
    const a = (i / n) * Math.PI * 2 + (r() - 0.5) * 0.08;
    const inner = clear * (0.85 + r() * 0.5);
    const w = (0.004 + r() * 0.012) * Math.PI;
    ctx.beginPath();
    ctx.moveTo(x + Math.cos(a) * inner, y + Math.sin(a) * inner);
    ctx.lineTo(x + Math.cos(a - w) * reach, y + Math.sin(a - w) * reach);
    ctx.lineTo(x + Math.cos(a + w) * reach, y + Math.sin(a + w) * reach);
    ctx.closePath();
    ctx.fill();
  }
  ctx.restore();
}

/** Motion streaks trailing behind something moving along `angle`. */
export function speedLines(ctx: Ctx, x: number, y: number, angle: number, length: number, spread: number, n: number, seed: number, alpha = 1, color = INK) {
  if (alpha <= 0) return;
  const r = rng(seed);
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.translate(x, y);
  ctx.rotate(rad(angle));
  ctx.strokeStyle = color;
  ctx.lineCap = "round";
  for (let i = 0; i < n; i++) {
    const off = (r() - 0.5) * spread;
    const l = length * (0.45 + r() * 0.55);
    const start = r() * length * 0.25;
    ctx.lineWidth = 2 + r() * 3;
    ctx.beginPath();
    ctx.moveTo(-start, off);
    ctx.lineTo(-start - l, off);
    ctx.stroke();
  }
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Lettering

const FACES = [
  "900 {s}px Impact, 'Arial Black', sans-serif",
  "italic 700 {s}px Georgia, serif",
  "800 {s}px Futura, 'Avenir Next', sans-serif",
  "700 {s}px 'American Typewriter', 'Courier New', monospace",
  "700 {s}px Didot, 'Bodoni 72', serif",
  "700 {s}px Rockwell, Georgia, serif",
  "900 {s}px 'Arial Black', Impact, sans-serif",
  "700 {s}px 'Courier New', monospace",
];
const SCRAPS = [INK, TOMATO, MUSTARD, WHITE, TEAL, BLUSH, WHITE, BLUE];

/**
 * Ransom-note lettering: every letter cut from a different magazine. Letters
 * pop in from `at`, `stagger` seconds apart, and peel away after `hold`.
 */
export function ransom(ctx: Ctx, text: string, x: number, y: number, size: number, t: number, at: number, seed: number, o: {
  stagger?: number; hold?: number; angle?: number; scrap?: boolean;
} = {}) {
  const { stagger = 0.03, hold = 0.7, angle = -4, scrap = true } = o;
  if (t < at) return;
  const gone = hold === Infinity ? 0 : smooth(seg01(t, at + hold, at + hold + 0.22));
  if (gone >= 1) return;
  const r = rng(seed);
  const letters = [...text].map((ch) => {
    const face = FACES[Math.floor(r() * FACES.length)];
    const bg = SCRAPS[Math.floor(r() * SCRAPS.length)];
    return { ch, face: face.replace("{s}", String(Math.round(size * (0.85 + r() * 0.35)))), bg, tilt: (r() - 0.5) * 16, lift: (r() - 0.5) * size * 0.16 };
  });
  ctx.save();
  ctx.translate(x, y);
  ctx.rotate(rad(angle));
  ctx.textBaseline = "middle";
  ctx.textAlign = "center";
  const widths = letters.map((l) => { ctx.font = l.face; return Math.max(size * 0.42, ctx.measureText(l.ch).width) + size * 0.22; });
  const total = widths.reduce((a, b) => a + b, 0);
  let cx = -total / 2;
  letters.forEach((l, i) => {
    const w = widths[i];
    const k = clamp((t - at - i * stagger) / 0.2);
    cx += w / 2;
    if (k > 0 && l.ch !== " ") {
      const s = outBack(k) * (1 - gone);
      ctx.save();
      ctx.translate(cx, l.lift - gone * size * 0.4);
      ctx.rotate(rad(l.tilt + gone * 20));
      ctx.scale(s, s);
      ctx.font = l.face;
      const dark = l.bg === INK || l.bg === TOMATO || l.bg === TEAL || l.bg === BLUE;
      if (scrap) {
        const h = size * 1.08;
        cutout(ctx, clipping(-w / 2, -h / 2, w, h, seed + i * 17, 3), l.bg, { rim: 0, dx: 3, dy: 4, grain: 0.8 });
      }
      ctx.fillStyle = scrap ? (dark ? WHITE : INK) : l.bg;
      ctx.fillText(l.ch, 0, size * 0.04);
      ctx.restore();
    }
    cx += w / 2;
  });
  ctx.restore();
}

const seg01 = (t: number, a: number, b: number) => clamp((t - a) / (b - a));

// ---------------------------------------------------------------------------
// Balloons

/** A comic thought cloud with trailing bubbles toward (tx, ty). */
export function thought(ctx: Ctx, x: number, y: number, r: number, tx: number, ty: number, p: number, seed: number) {
  if (p <= 0) return;
  const s = outBack(clamp(p));
  const rand = rng(seed);
  const cloud = new Path2D();
  const lobes = 9;
  for (let i = 0; i < lobes; i++) {
    const a = (i / lobes) * Math.PI * 2;
    const lr = r * (0.36 + rand() * 0.1);
    cloud.moveTo(x + Math.cos(a) * r * 0.78 + lr, y + Math.sin(a) * r * 0.58);
    cloud.arc(x + Math.cos(a) * r * 0.78, y + Math.sin(a) * r * 0.58, lr, 0, Math.PI * 2);
  }
  cloud.moveTo(x + r * 0.85, y);
  cloud.ellipse(x, y, r * 0.85, r * 0.62, 0, 0, Math.PI * 2);
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(s, s);
  ctx.translate(-x, -y);
  for (let i = 0; i < 3; i++) {
    const k = (i + 1) / 4;
    const br = r * (0.13 - i * 0.03);
    const b = new Path2D();
    b.arc(lerp(x, tx, 0.55 + k * 0.4), lerp(y + r * 0.5, ty, 0.3 + k * 0.6), br, 0, Math.PI * 2);
    cutout(ctx, b, WHITE, { rim: 0, dx: 3, dy: 4 });
    ctx.strokeStyle = INK;
    ctx.lineWidth = 4;
    ctx.stroke(b);
  }
  cutout(ctx, cloud, WHITE, { rim: 0 });
  ctx.strokeStyle = INK;
  ctx.lineWidth = 5;
  ctx.stroke(cloud);
  ctx.fillStyle = WHITE;
  ctx.beginPath();
  ctx.ellipse(x, y, r * 0.86, r * 0.6, 0, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

/** A speech balloon with a tail pointing at (tx, ty); `draw` fills its content. */
export function balloon(ctx: Ctx, x: number, y: number, w: number, h: number, tx: number, ty: number, s: number, draw?: () => void, fill = WHITE) {
  if (s <= 0.01) return;
  ctx.save();
  ctx.translate(tx, ty);
  ctx.scale(s, s);
  ctx.translate(-tx, -ty);
  const p = new Path2D();
  p.ellipse(x, y, w / 2, h / 2, 0, 0, Math.PI * 2);
  const a = Math.atan2(ty - y, tx - x);
  const tail = new Path2D();
  tail.moveTo(x + Math.cos(a - 0.35) * w * 0.36, y + Math.sin(a - 0.35) * h * 0.36);
  tail.lineTo(tx, ty);
  tail.lineTo(x + Math.cos(a + 0.35) * w * 0.36, y + Math.sin(a + 0.35) * h * 0.36);
  tail.closePath();
  const all = new Path2D();
  all.addPath(p);
  all.addPath(tail);
  cutout(ctx, all, fill, { rim: 0, dx: 4, dy: 5 });
  ctx.strokeStyle = INK;
  ctx.lineWidth = 4.5;
  ctx.lineJoin = "round";
  ctx.stroke(p);
  ctx.stroke(tail);
  ctx.fillStyle = fill;
  ctx.fill(p);
  draw?.();
  ctx.restore();
}
