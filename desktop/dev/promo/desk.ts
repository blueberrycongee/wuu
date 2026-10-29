// Shots 1–6, one desk world (beats 0–60):
//   1. In the dark a lamp clicks on; Wuu wakes and says hi.
//   2. The lights come up on a desk pasted with harness clippings. Each one
//      calls, the cursor answers, faster and faster.
//   3. The page splits into 2, 4, 8, then 16 panels until nothing keeps up.
//   4. One silent panel: a marble drops. Wuu has an idea: scissors.
//   5. Wuu rides the scissors around every clipping and pastes them into one app.
//   6. Inside the app: switch sessions, add one on another engine, send.
import {
  drawBall, drawCursor, drawLines, drawRipple, drawToken, drawWindow, ICON_POSE,
  THEMES, TITLE_BAR, WUU, type Ball,
} from "./art";
import {
  clamp, eyes, hop, inCubic, inOut, lerp, outBack, outCubic, rad, seg, smooth, squash, wobble,
  type EyeKind,
} from "./motion";
import {
  agent, arc, CLAUDE, CODEX, CURSOR, drawDaze, drawScissors, drawSpinner, H, look, NIGHT, OPENCODE, PI,
  stand, W, withCamera, type Agent, type Camera, type Ctx,
} from "./cast";
import {
  balloon, boil, clipping, cutout, focusLines, INK, KRAFT, MUSTARD, PAPER, ransom, rng, sheet, speedLines,
  tape, TOMATO, thought,
} from "./paper";
import score from "./cues.json";
import { at, beat, count, cue, type CueId } from "./timeline";

export const DESK_END = beat(60);

// ---------------------------------------------------------------------------
// Layout

interface Clip { agent: Agent; x: number; y: number; w: number; h: number; tilt: number; lines: number[]; seed: number }
const CLIPS: Clip[] = [
  { agent: CLAUDE, x: 120, y: 92, w: 600, h: 380, tilt: -2.4, lines: [250, 180, 290, 140, 230], seed: 3 },
  { agent: CODEX, x: 810, y: 64, w: 560, h: 370, tilt: 1.8, lines: [210, 270, 150, 240, 120], seed: 5 },
  { agent: CURSOR, x: 230, y: 566, w: 560, h: 360, tilt: 1.4, lines: [230, 160, 260, 190, 110], seed: 7 },
  { agent: OPENCODE, x: 940, y: 506, w: 580, h: 378, tilt: -1.8, lines: [260, 200, 170, 280, 130], seed: 9 },
];
/** The creature inside each clipping, in content coordinates. */
const CREATURE = { x: 118, r: 64, above: 46 };
const CORNER = { x: 1745, floor: 1012, r: 118 };
/** After the scissors ride Wuu stays a little smaller, out of the app's way. */
const HOME = { x: 1700, floor: 1000, r: 84 };
const LAMP = { x: 1668, y: 486 };

const APP = { x: 250, y: 100, w: 1390, h: 850 };
const SIDEBAR = 332;
const APP_BODY = APP.h - TITLE_BAR;
const COMPOSER = { x: SIDEBAR + 34, y: APP_BODY - 128, w: APP.w - SIDEBAR - 68, h: 96 };
const PILL = { x: COMPOSER.x + 22, y: COMPOSER.y + 24, w: 118, h: 48 };
const SEND = { x: COMPOSER.x + COMPOSER.w - 50, y: COMPOSER.y + COMPOSER.h / 2 };
const PLUS = { x: 18, y: 18, w: 52, h: 52 };
const rowRect = (i: number) => ({ x: 14, y: 90 + i * 82, w: SIDEBAR - 28, h: 70 });
const app = (x: number, y: number): [number, number] => [APP.x + x, APP.y + TITLE_BAR + y];
const SESSIONS: Agent[] = [CLAUDE, CODEX, CURSOR, OPENCODE, PI];
const MENU = ["wuu", "codex", "claude", "cursor", "devin", "grok", "hermes", "pi", "opencode", "antigravity"];
const MENU_BOX = { x: PILL.x - 6, y: COMPOSER.y - 214, w: 470, h: 196 };
const menuSlot = (i: number): [number, number] => [MENU_BOX.x + 58 + (i % 5) * 88, MENU_BOX.y + 56 + Math.floor(i / 5) * 86];
const PI_SLOT = MENU.indexOf("pi");

/** A point in a clipping's own coordinates, placed on the desk. */
function place(c: Clip, lx: number, ly: number): [number, number] {
  const a = rad(c.tilt), cx = c.x + c.w / 2, cy = c.y + c.h / 2;
  const dx = lx - c.w / 2, dy = ly - c.h / 2;
  return [cx + dx * Math.cos(a) - dy * Math.sin(a), cy + dx * Math.sin(a) + dy * Math.cos(a)];
}
const creatureAt = (i: number) => place(CLIPS[i], CREATURE.x, CLIPS[i].h - CREATURE.above - CREATURE.r);
const balloonAt = (i: number): [number, number] => {
  const [x, y] = creatureAt(i);
  return [x + 96, y - 118];
};

// ---------------------------------------------------------------------------
// Timeline

const LIGHTS = at("lights");
const SPLITS = cue("split");
const OVERLOAD = at("overload");
const HUSH = at("hush");
const IDEA = at("idea");
const RIDE = at("scissors");
const PEELS = cue("peel");
const APP_LANDS = at("app");
const PASTES = cue("paste");
const SELECT = cue("select");
const PLUS_CLICK = at("plus");
const PILL_CLICK = at("pill");
const HOVERS = cue("hover");
const PICK = at("pick");
const SEND_CLICK = at("send");
const PI_ARRIVES = at("pi-arrives");
const CUT_STARTS = [RIDE, ...PEELS.slice(0, 3).map((p) => p + beat(0.5))];
const SNIPS = [RIDE, ...cue("snip")];

// Every call waits for the next free click; the ones the cursor misses stay
// up until the hush.
interface Request { clip: number; ask: number; answer: number }
const CALLS = CLIPS.flatMap((c, clip) => cue(`call.${c.agent.engine}` as CueId).map((ask) => ({ clip, ask })))
  .sort((a, b) => a.ask - b.ask);
const ANSWERS = cue("answer");
const REQUESTS: Request[] = (() => {
  const open: { clip: number; ask: number }[] = [];
  const out: Request[] = [];
  let next = 0;
  for (const answer of ANSWERS) {
    while (next < CALLS.length && CALLS[next].ask < answer) open.push(CALLS[next++]);
    const r = open.shift();
    if (r) out.push({ ...r, answer });
  }
  return [...out, ...open, ...CALLS.slice(next)].map((r) => ({ answer: HUSH, ...r }));
})();
/** Where each click lands: the call it answers, or the next caller it rushes to. */
const CLICK_TARGETS = ANSWERS.map((answer) => {
  const r = REQUESTS.find((q) => q.answer === answer);
  return r ? r.clip : (CALLS.find((c) => c.ask > answer) ?? CALLS[CALLS.length - 1]).clip;
});

// ---------------------------------------------------------------------------

export function deskShot(ctx: Ctx, t: number) {
  if (t < LIGHTS) return opening(ctx, t);
  if (t >= SPLITS[0] && t < HUSH) return montage(ctx, t);
  if (t >= HUSH && t < RIDE) return hush(ctx, t);
  withCamera(ctx, camera(t), () => world(ctx, t));
}

function camera(t: number): Camera {
  const wide = { x: 960, y: 540, zoom: 1 };
  if (t < SPLITS[0]) {
    const k = smooth(seg(t, beat(16), beat(24)));
    return { x: 960, y: lerp(540, 560, k), zoom: lerp(1, 1.05, k) };
  }
  // The scissors ride needs room above the top clippings; the app then fills the frame.
  const ride = { x: 960, y: 500, zoom: 0.8 };
  const inApp = { x: 945, y: 545, zoom: 1.12 };
  const a = smooth(seg(t, PEELS[3], APP_LANDS + 0.2));
  const b = inOut(seg(t, beat(48), beat(51.5)));
  const mid = { x: lerp(ride.x, wide.x, a), y: lerp(ride.y, wide.y, a), zoom: lerp(ride.zoom, wide.zoom, a) };
  return { x: lerp(mid.x, inApp.x, b), y: lerp(mid.y, inApp.y, b), zoom: lerp(mid.zoom, inApp.zoom, b) };
}

// ---------------------------------------------------------------------------
// Shot 1: the lamp

function opening(ctx: Ctx, t: number) {
  const lit = t >= at("lamp-on");
  const push = smooth(seg(t, 0, LIGHTS));
  const cam = { x: 1700, y: 740, zoom: lerp(1.58, 1.7, push) };
  ctx.fillStyle = NIGHT;
  ctx.fillRect(0, 0, W, H);
  if (!lit) return;
  withCamera(ctx, cam, () => {
    world(ctx, t, false);
    // Everything outside the cone stays dark; the cone itself is warm.
    const cone = new Path2D();
    cone.moveTo(LAMP.x - 58, LAMP.y + 6);
    cone.lineTo(LAMP.x + 70, LAMP.y - 10);
    cone.lineTo(CORNER.x + 330, CORNER.floor + 60);
    cone.lineTo(CORNER.x - 300, CORNER.floor + 60);
    cone.closePath();
    const dark = new Path2D();
    dark.rect(-1000, -1000, 4000, 3200);
    dark.addPath(cone);
    ctx.fillStyle = "rgba(21, 22, 26, 0.94)";
    ctx.fill(dark, "evenodd");
    ctx.fillStyle = "rgba(255, 224, 150, 0.12)";
    ctx.fill(cone);
    drawLamp(ctx);
    drawWuu(ctx, t);
  });
  ransom(ctx, "click", 800, 240, 84, t, at("lamp-on"), 41, { hold: 0.6, angle: -6 });
}

function drawLamp(ctx: Ctx) {
  ctx.save();
  // The arm reaches in from the top of the desk.
  ctx.strokeStyle = "#3B3C41";
  ctx.lineCap = "round";
  ctx.lineWidth = 16;
  ctx.beginPath();
  ctx.moveTo(LAMP.x + 10, LAMP.y - 84);
  ctx.lineTo(LAMP.x + 150, LAMP.y - 300);
  ctx.lineTo(LAMP.x + 60, LAMP.y - 640);
  ctx.stroke();
  ctx.fillStyle = "#3B3C41";
  ctx.beginPath();
  ctx.arc(LAMP.x + 150, LAMP.y - 300, 17, 0, Math.PI * 2);
  ctx.fill();
  ctx.translate(LAMP.x, LAMP.y);
  ctx.rotate(rad(-8));
  const shade = new Path2D();
  shade.moveTo(-40, -92);
  shade.lineTo(40, -92);
  shade.quadraticCurveTo(62, -40, 74, 0);
  shade.lineTo(-74, 0);
  shade.quadraticCurveTo(-62, -40, -40, -92);
  shade.closePath();
  cutout(ctx, shade, MUSTARD, { rim: 5 });
  ctx.fillStyle = "#FFF3C8";
  ctx.beginPath();
  ctx.ellipse(0, 0, 70, 12, 0, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Shot 3: the page splits into more and more panels

interface Panel { x: number; y: number; w: number; h: number; tilt: number }

function grid(n: number): Panel[] {
  const [cols, rows] = n === 2 ? [2, 1] : n === 4 ? [2, 2] : n === 8 ? [4, 2] : [4, 4];
  const margin = 34, gap = 22;
  const w = (W - margin * 2 - gap * (cols - 1)) / cols;
  const h = (H - margin * 2 - gap * (rows - 1)) / rows;
  const r = rng(n * 13);
  const mess = n === 2 ? 1 : n === 4 ? 1.4 : n === 8 ? 2 : 2.8;
  return Array.from({ length: n }, (_, i) => ({
    x: margin + (i % cols) * (w + gap) + (r() - 0.5) * mess * 6,
    y: margin + Math.floor(i / cols) * (h + gap) + (r() - 0.5) * mess * 6,
    w, h, tilt: (r() - 0.5) * mess * 2,
  }));
}

type Target = "cursor" | "wuu" | 0 | 1 | 2 | 3;
const SHOTS: Record<number, [Target, number][]> = {
  2: [["wuu", 1.8], ["cursor", 1.6]],
  4: [[0, 2.2], ["cursor", 2.3], [1, 2.2], ["wuu", 2.1]],
  8: [[0, 2.6], [1, 2.8], ["cursor", 2.6], [3, 2.7], ["wuu", 2.5], [2, 2.7], ["cursor", 1.9], [0, 3.4]],
  16: [[0, 3], ["cursor", 3.2], [1, 3], [2, 3.1], ["wuu", 2.9], [3, 3.2], [1, 3.6], [0, 3.1],
    ["cursor", 2.6], [3, 3.1], [2, 3.4], ["wuu", 3.4], [0, 3.6], [1, 3.1], ["cursor", 3.6], [3, 3]],
};

/** A panel frames its subject where it was when the panel appeared, and holds. */
function targetPoint(target: Target, since: number): [number, number] {
  if (target === "cursor") {
    const c = cursorState(since);
    return [c.x + 20, c.y + 30];
  }
  if (target === "wuu") return [CORNER.x - 20, CORNER.floor - CORNER.r * 1.1];
  const [x, y] = creatureAt(target);
  return [x + 30, y - 20];
}

function montage(ctx: Ctx, t: number) {
  const k = count("split", t) - 1;
  const n = [2, 4, 8, 16][k];
  sheet(ctx, 0, 0, W, H, PAPER);
  const shake = smooth(seg(t, OVERLOAD, HUSH));
  grid(n).forEach((p, i) => {
    const [target, zoom] = SHOTS[n][i];
    const [x, y] = targetPoint(target, SPLITS[k]);
    const jx = wobble(t, i * 3.1, 9) * 9 * shake, jy = wobble(t, i * 5.7, 9) * 9 * shake;
    drawPanel(ctx, { ...p, x: p.x + jx, y: p.y + jy, tilt: p.tilt + wobble(t, i, 7) * 1.6 * shake }, { x, y, zoom }, (c) => world(c, t));
  });
  // The overload peaks in a ring of concentration lines around the page.
  focusLines(ctx, W / 2, H / 2, 470 - 90 * shake, 1400, 90, 5, INK, 0.5 * shake);
}

function drawPanel(ctx: Ctx, p: Panel, cam: Camera, draw: (ctx: Ctx) => void) {
  ctx.save();
  ctx.translate(p.x + p.w / 2, p.y + p.h / 2);
  ctx.rotate(rad(p.tilt));
  const frame = new Path2D();
  frame.rect(-p.w / 2, -p.h / 2, p.w, p.h);
  cutout(ctx, frame, KRAFT, { rim: 5, grain: 0 });
  ctx.save();
  ctx.clip(frame);
  ctx.scale(cam.zoom, cam.zoom);
  ctx.translate(-cam.x, -cam.y);
  draw(ctx);
  ctx.restore();
  ctx.strokeStyle = INK;
  ctx.lineWidth = 5;
  ctx.stroke(frame);
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Shot 4: the silent panel

const HUSH_PANEL: Panel = { x: 550, y: 250, w: 820, h: 560, tilt: -1.2 };
const MARBLE_R = 24;
// The marble lands on the cut, then touches down on every bounce of the
// recording the score plays here; each hop is as high as its airtime allows.
const BOUNCES = [0, ...score.cues.hush.bounces];
const GRAVITY = 2600; // px/s² in the desk world

function marble(t: number) {
  const d = t - HUSH;
  const roll = outCubic(clamp(d / BOUNCES[BOUNCES.length - 1]));
  const x = lerp(CORNER.x - 205, CORNER.x - 128, roll);
  let y = CORNER.floor - MARBLE_R;
  for (let i = 0; i < BOUNCES.length - 1; i++) {
    const [a, b] = [BOUNCES[i], BOUNCES[i + 1]];
    if (d >= a && d < b) y -= (GRAVITY / 2) * (d - a) * (b - d);
  }
  return { x, y, spin: roll * 5 };
}

function drawMarble(ctx: Ctx, x: number, y: number, spin: number) {
  const p = new Path2D();
  p.arc(x, y, MARBLE_R, 0, Math.PI * 2);
  cutout(ctx, p, "#8FC6D4", { rim: 3, dx: 3, dy: 4 });
  ctx.save();
  ctx.clip(p);
  ctx.strokeStyle = "#2F7F9A";
  ctx.lineWidth = 6;
  ctx.beginPath();
  ctx.arc(x, y, MARBLE_R * 0.55, spin, spin + 2.2);
  ctx.stroke();
  ctx.fillStyle = "rgba(255,255,255,0.75)";
  ctx.beginPath();
  ctx.ellipse(x - 6, y - 7, 5, 3, rad(-35), 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

function hush(ctx: Ctx, t: number) {
  sheet(ctx, 0, 0, W, H, PAPER);
  const p = HUSH_PANEL;
  const face = { x: CORNER.x - 30, y: CORNER.floor - CORNER.r * 1.05 };
  drawPanel(ctx, p, { x: face.x, y: face.y + 30, zoom: 1.6 }, (c) => {
    world(c, t);
    const m = marble(t);
    drawMarble(c, m.x, m.y, m.spin);
  });
  const toScreen = (x: number, y: number): [number, number] => {
    const a = rad(p.tilt), dx = (x - face.x) * 1.6, dy = (y - face.y - 30) * 1.6;
    return [p.x + p.w / 2 + dx * Math.cos(a) - dy * Math.sin(a), p.y + p.h / 2 + dx * Math.sin(a) + dy * Math.cos(a)];
  };
  const [mx, my] = toScreen(marble(HUSH).x, CORNER.floor);
  ransom(ctx, "tok", mx + 10, my - 120, 64, t, HUSH, 23, { hold: 0.7, angle: 5 });
  // The idea breaks out over the panel border as a thought.
  const [hx, hy] = toScreen(CORNER.x + 40, CORNER.floor - CORNER.r * 2);
  const cx = p.x + p.w - 30, cy = p.y + 20;
  const pop = seg(t, IDEA, IDEA + 0.28);
  const snips = cue("test-snip");
  const burst = seg(t, snips[0] - 0.08, snips[0]);
  if (burst < 1) {
    thought(ctx, cx, cy, 140, hx, hy, pop * (1 - burst), 12);
    ctx.save();
    ctx.translate(cx, cy);
    ctx.scale(outBack(pop) * (1 - burst), outBack(pop) * (1 - burst));
    drawScissors(ctx, -70, 18, 150, -14, 30);
    ctx.restore();
  }
  // Then the scissors are real, and they work.
  if (t >= snips[0] - 0.08) {
    const k = outBack(seg(t, snips[0] - 0.08, snips[0] + 0.12));
    ctx.save();
    ctx.translate(p.x + 150, p.y + p.h - 30);
    ctx.scale(k, k);
    drawScissors(ctx, 0, 0, 470, -30, snipAngle(t, snips, 40));
    ctx.restore();
    ransom(ctx, "snip", p.x + 150, p.y + 120, 76, t, snips[0], 31, { hold: 0.4, angle: -8 });
    ransom(ctx, "snip", p.x + 420, p.y + 40, 76, t, snips[1], 37, { hold: Infinity, angle: 6 });
  }
}

/** Blades shut exactly on each snip and spring open between them. */
function snipAngle(t: number, snips: number[], open: number) {
  let d = Infinity;
  for (const s of snips) d = Math.min(d, t >= s - 0.12 ? Math.abs(t - s) : Infinity);
  return open * (d === Infinity ? 1 : smooth(clamp(d / 0.12)));
}

// ---------------------------------------------------------------------------
// The desk world

function world(ctx: Ctx, t: number, withWuu = true) {
  sheet(ctx, -1000, -1000, 4000, 3200, KRAFT);
  drawGhosts(ctx, t);
  drawApp(ctx, t);
  const order = CLIPS.map((_, i) => i).sort((a, b) => clipOrder(a, t) - clipOrder(b, t));
  for (const i of order) drawClip(ctx, i, t);
  if (t < HUSH || t >= RIDE) drawLamp(ctx);
  // On the ride, Wuu stands on top of the scissors.
  const riding = t >= RIDE && t < PEELS[3];
  if (withWuu && !riding) drawWuu(ctx, t);
  drawRide(ctx, t);
  if (withWuu && riding) drawWuu(ctx, t);
  drawTheCursor(ctx, t);
}

/** Clippings answered most recently sit on top; lifted ones float above the app. */
function clipOrder(i: number, t: number) {
  if (t >= PEELS[i]) return 1000 + i;
  let last = -1;
  for (const r of REQUESTS) if (r.clip === i && r.answer <= t) last = r.answer;
  return last;
}

function lift(i: number, t: number) {
  return outBack(seg(t, PEELS[i], PEELS[i] + 0.3));
}

function flight(i: number, t: number) {
  return seg(t, PASTES[i] - beat(0.9), PASTES[i]);
}

function drawGhosts(ctx: Ctx, t: number) {
  // Where a clipping was lifted, the desk under it is unfaded, with the cut line.
  CLIPS.forEach((c, i) => {
    if (t < PEELS[i]) return;
    ctx.save();
    ctx.translate(c.x + c.w / 2, c.y + c.h / 2);
    ctx.rotate(rad(c.tilt));
    const hole = clipping(-c.w / 2 - 12, -c.h / 2 - 12, c.w + 24, c.h + 24, c.seed, 4);
    ctx.fillStyle = "#E4CCA6";
    ctx.fill(hole);
    ctx.setLineDash([14, 10]);
    ctx.strokeStyle = "rgba(38, 39, 43, 0.35)";
    ctx.lineWidth = 3;
    ctx.stroke(hole);
    ctx.setLineDash([]);
    ctx.restore();
  });
}

function drawClip(ctx: Ctx, i: number, t: number) {
  const c = CLIPS[i];
  const f = flight(i, t);
  if (f >= 1) return;
  const l = t >= PEELS[i] ? lift(i, t) : 0;
  const j = boil(t, c.seed, 0.7);
  const e = inOut(f);
  const row = rowRect(i);
  const [rx, ry] = app(row.x + row.w / 2, row.y + row.h / 2);
  const bob = Math.sin((t - PEELS[i]) * 4 + i) * 6 * l * (1 - e);
  const cx = lerp(c.x + c.w / 2, rx, e) + j.x;
  const cy = lerp(c.y + c.h / 2 - 16 * l, ry, e) - Math.sin(e * Math.PI) * 120 + bob + j.y;
  const scale = lerp(1 + 0.03 * l, row.w / c.w, e);
  const tilt = lerp(c.tilt + 2.5 * l * Math.sin(i + 1), 0, e) + j.a;
  ctx.save();
  if (l > 0) {
    // Lifted paper throws a longer shadow.
    ctx.save();
    ctx.globalAlpha *= 0.5 * l * (1 - e);
    ctx.translate(cx + 18 * l, cy + 26 * l);
    ctx.rotate(rad(tilt));
    ctx.scale(scale, scale);
    ctx.fillStyle = "rgba(52, 38, 22, 0.35)";
    ctx.beginPath();
    ctx.roundRect(-c.w / 2, -c.h / 2, c.w, c.h, 20);
    ctx.fill();
    ctx.restore();
  }
  drawWindow(ctx, {
    x: cx - c.w / 2, y: cy - c.h / 2, w: c.w, h: c.h, theme: c.agent.theme, engine: c.agent.engine,
    tilt, scale, alpha: 1 - smooth(seg(f, 0.8, 1)), rim: 8,
  }, (g, _w, h) => {
    drawLines(g, 232, 44, c.lines, typing(i, t), [c.agent.theme.text, c.agent.theme.accent], { gap: 34, thick: 14 });
    drawClipCreature(g, i, t, h);
  });
  if (l === 0 && f === 0) {
    // Two strips of tape hold each clipping down until it is cut free.
    const [ax, ay] = place(c, 40, -4);
    const [bx, by] = place(c, c.w - 30, c.h + 2);
    tape(ctx, ax, ay, 120, c.tilt - 32, c.seed);
    tape(ctx, bx, by, 110, c.tilt - 28, c.seed + 1);
  }
  ctx.restore();
}

function typing(i: number, t: number) {
  return ((t - LIGHTS) * 0.3 + i * 0.27) % 1.25;
}

function asking(i: number, t: number) {
  for (const r of REQUESTS) if (r.clip === i && t >= r.ask && t < r.answer) return r;
  return undefined;
}

function lastAnswer(i: number, t: number) {
  let last = -Infinity;
  for (const r of REQUESTS) if (r.clip === i && r.answer <= t && r.answer < HUSH) last = r.answer;
  return last;
}

function drawClipCreature(ctx: Ctx, i: number, t: number, h: number) {
  const a = CLIPS[i].agent;
  const floor = h - CREATURE.above;
  const ask = asking(i, t);
  const answered = t - lastAnswer(i, t);
  let kind: EyeKind = "open";
  let h0 = { lift: 0, sx: 1, sy: 1 };
  let face = { yaw: 0.42, pitch: -0.22 };
  if (ask) {
    kind = "wide";
    h0 = hop(t, ask.ask - 0.05, 0.42);
    face = { yaw: -0.1, pitch: 0.18 };
  } else if (answered < 0.45) {
    kind = "happy";
    face = { yaw: 0, pitch: 0.1 };
  }
  if (t >= RIDE) {
    // Everyone watches the scissors come round.
    const [sx, sy] = ridePose(t).pivot;
    const [px, py] = creatureAt(i);
    face = look(px, py, sx, sy, 1.2);
    kind = t >= PEELS[i] ? "happy" : "open";
  }
  const ball = agent(a, CREATURE.x, floor, CREATURE.r, h0, 34);
  drawBall(ctx, { ...ball, ...face, eyes: { kind, open: blinkFor(t, i) }, sway: Math.sin(t * 2 + i) * 0.08 });
  // A call is a comic balloon with a shout in it.
  let s = 0;
  for (const r of REQUESTS) {
    if (r.clip !== i) continue;
    s = Math.max(s, outBack(seg(t, r.ask, r.ask + 0.22)) * (1 - seg(t, r.answer, r.answer + 0.1)));
  }
  const bx = CREATURE.x + 96, by = floor - CREATURE.r * 2 - 70;
  balloon(ctx, bx, by, 92, 74, CREATURE.x + 34, floor - CREATURE.r * 1.7, s, () => drawShout(ctx, bx, by, 1));
}

function drawShout(ctx: Ctx, x: number, y: number, s: number) {
  ctx.save();
  ctx.translate(x, y);
  ctx.scale(s, s);
  ctx.fillStyle = TOMATO;
  ctx.beginPath();
  ctx.moveTo(-7, -24);
  ctx.lineTo(7, -24);
  ctx.lineTo(4, 7);
  ctx.lineTo(-4, 7);
  ctx.closePath();
  ctx.fill();
  ctx.beginPath();
  ctx.arc(0, 18, 6, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

function blinkFor(t: number, seed: number) {
  const period = 2.6 + seed * 0.37;
  const p = ((t + seed * 0.9) % period) / 0.18;
  return p > 0 && p < 1 ? (p < 0.4 ? 1 - 0.92 * smooth(p / 0.4) : 0.08 + 0.92 * smooth((p - 0.4) / 0.6)) : 1;
}

// ---------------------------------------------------------------------------
// Shot 5: the scissors ride

const RIM = 14;

/** Position along a clipping's outline, clockwise from its top-left corner. */
function around(c: Clip, u: number): { x: number; y: number; angle: number } {
  const corners = [place(c, -RIM, -RIM), place(c, c.w + RIM, -RIM), place(c, c.w + RIM, c.h + RIM), place(c, -RIM, c.h + RIM)];
  const sides = corners.map((p, i) => Math.hypot(corners[(i + 1) % 4][0] - p[0], corners[(i + 1) % 4][1] - p[1]));
  let d = clamp(u) * sides.reduce((a, b) => a + b, 0);
  for (let i = 0; i < 4; i++) {
    if (d <= sides[i] || i === 3) {
      const [ax, ay] = corners[i], [bx, by] = corners[(i + 1) % 4];
      const k = clamp(d / sides[i]);
      return { x: lerp(ax, bx, k), y: lerp(ay, by, k), angle: (Math.atan2(by - ay, bx - ax) * 180) / Math.PI };
    }
    d -= sides[i];
  }
  return { x: corners[0][0], y: corners[0][1], angle: 0 };
}

interface Ride { pivot: [number, number]; angle: number; open: number; air: number }

function ridePose(t: number): Ride {
  for (let k = 0; k < CLIPS.length; k++) {
    const start = CUT_STARTS[k], end = PEELS[k];
    if (k > 0 && t < start) {
      // Hop from the last clipping's corner to this one's.
      const from = around(CLIPS[k - 1], 1), to = around(CLIPS[k], 0);
      const p = inOut(seg(t, PEELS[k - 1], start));
      const [x, y] = arc(p, from.x, from.y, to.x, to.y, 140);
      return { pivot: [x, y], angle: lerp(from.angle, to.angle - 360, p), open: 34, air: Math.sin(p * Math.PI) };
    }
    if (t < end) {
      const p = around(CLIPS[k], seg(t, start, end));
      return { pivot: [p.x, p.y], angle: p.angle, open: snipAngle(t, SNIPS, 34), air: 0 };
    }
  }
  // Done: the scissors spin away while Wuu hops home.
  const from = around(CLIPS[3], 1);
  const p = seg(t, PEELS[3], PEELS[3] + beat(1));
  return { pivot: [lerp(from.x, 2300, inCubic(p)), lerp(from.y, -300, inCubic(p))], angle: 720 * p, open: 34, air: 1 };
}

function drawRide(ctx: Ctx, t: number) {
  if (t < RIDE || t > PEELS[3] + beat(1)) return;
  const r = ridePose(t);
  const [x, y] = r.pivot;
  // The line still to cut, then the scissors on top of it.
  const k = CUT_STARTS.findIndex((s, i) => t >= s && t < PEELS[i]);
  if (k >= 0) {
    const c = CLIPS[k];
    const u = seg(t, CUT_STARTS[k], PEELS[k]);
    ctx.save();
    ctx.setLineDash([14, 10]);
    ctx.strokeStyle = INK;
    ctx.lineWidth = 3;
    ctx.beginPath();
    for (let s = 0; s <= 40; s++) {
      const p = around(c, lerp(u, 1, s / 40));
      if (s === 0) ctx.moveTo(p.x, p.y);
      else ctx.lineTo(p.x, p.y);
    }
    ctx.stroke();
    ctx.restore();
    speedLines(ctx, x, y, r.angle, 150, 60, 5, k * 11 + Math.floor(t * 8), 0.45);
  }
  drawScissors(ctx, x, y, 210, r.angle, r.open);
  for (const s of SNIPS) ransom(ctx, "snip", x + 60, y - 190, 50, t, s, Math.round(s * 100), { hold: 0.25, angle: -10, stagger: 0.02 });
}

// ---------------------------------------------------------------------------
// The unified app

function landed(t: number) {
  return seg(t, APP_LANDS - 0.2, APP_LANDS);
}

function drawApp(ctx: Ctx, t: number) {
  const d = landed(t);
  if (d <= 0) return;
  const fall = inCubic(d);
  const slap = squash(t, APP_LANDS, 0.05, 0.4);
  const scale = lerp(1.12, 1, fall) * slap.sx;
  drawWindow(ctx, {
    x: APP.x, y: APP.y - (1 - fall) * 60, w: APP.w, h: APP.h, theme: THEMES.wuu, engine: "wuu",
    scale, alpha: smooth(clamp(d * 3)), rim: 9,
  }, (c, w, h) => {
    c.fillStyle = "#FBF8F3";
    c.fillRect(0, 0, SIDEBAR, h);
    c.fillStyle = "rgba(40,40,40,0.07)";
    c.fillRect(SIDEBAR - 1.5, 0, 1.5, h);
    drawPlus(c, t);
    for (let i = 0; i < SESSIONS.length; i++) drawRow(c, i, t);
    drawMainPane(c, w, h, t);
  });
  if (d < 1) return;
  focusLines(ctx, APP.x + APP.w / 2, APP.y + APP.h / 2, 760, 1300, 70, 3, INK, 0.6 * (1 - seg(t, APP_LANDS, APP_LANDS + 0.3)));
}

function drawPlus(ctx: Ctx, t: number) {
  const press = squash(t, PLUS_CLICK, 0.18, 0.3);
  ctx.save();
  ctx.translate(PLUS.x + PLUS.w / 2, PLUS.y + PLUS.h / 2);
  ctx.scale(press.sx, press.sy);
  ctx.fillStyle = "#FFFFFF";
  ctx.strokeStyle = "rgba(40,40,40,0.12)";
  ctx.lineWidth = 2;
  ctx.beginPath();
  ctx.roundRect(-PLUS.w / 2, -PLUS.h / 2, PLUS.w, PLUS.h, 16);
  ctx.fill();
  ctx.stroke();
  ctx.strokeStyle = INK;
  ctx.lineWidth = 5;
  ctx.lineCap = "round";
  ctx.beginPath();
  ctx.moveTo(-11, 0); ctx.lineTo(11, 0);
  ctx.moveTo(0, -11); ctx.lineTo(0, 11);
  ctx.stroke();
  ctx.restore();
  drawLines(ctx, PLUS.x + PLUS.w + 18, PLUS.y + 20, [150], 1, ["#E9E3D9"], { thick: 12 });
}

const rowBorn = (i: number) => (i < 4 ? PASTES[i] : PLUS_CLICK);

function selected(t: number) {
  let s = -1;
  SELECT.forEach((at, i) => { if (t >= at) s = i; });
  if (t >= PLUS_CLICK) s = 4;
  return s;
}
const selectedAt = (s: number) => (s < 4 ? SELECT[s] : PLUS_CLICK);

function drawRow(ctx: Ctx, i: number, t: number) {
  const born = rowBorn(i);
  if (t < born) return;
  const r = rowRect(i);
  const pop = i < 4 ? 1 : outBack(seg(t, born, born + 0.35));
  ctx.save();
  ctx.translate(r.x + r.w / 2, r.y + r.h / 2);
  const slap = squash(t, born, 0.12, 0.35);
  ctx.scale(lerp(0.6, 1, pop) * slap.sx, lerp(0.6, 1, pop) * slap.sy);
  ctx.globalAlpha *= clamp(pop * 2);
  ctx.translate(-r.w / 2, -r.h / 2);
  if (selected(t) === i) {
    const s = smooth(seg(t, selectedAt(i), selectedAt(i) + 0.2));
    ctx.fillStyle = `rgba(232, 224, 212, ${s})`;
    ctx.beginPath();
    ctx.roundRect(0, 0, r.w, r.h, 18);
    ctx.fill();
  }
  const a = SESSIONS[i];
  const hasAgent = i < 4 || t >= PI_ARRIVES;
  const ax = 40, ay = r.h / 2;
  if (hasAgent) {
    const k = i < 4 ? 1 : outBack(seg(t, PI_ARRIVES, PI_ARRIVES + 0.35));
    const bob = Math.sin(t * 9 + i * 1.7) * 0.035;
    drawBall(ctx, {
      ...stand(a.skin, ax, ay + 22 * k, 22 * k), sx: 1 + bob, sy: 1 - bob, rim: 3,
      eyes: { kind: t - born < 0.6 ? "happy" : "open", open: blinkFor(t, i + 3) }, yaw: 0.25, pitch: -0.1,
    });
  } else {
    ctx.setLineDash([5, 5]);
    ctx.strokeStyle = "rgba(40,40,40,0.25)";
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.arc(ax, ay, 21, 0, Math.PI * 2);
    ctx.stroke();
    ctx.setLineDash([]);
  }
  drawLines(ctx, 78, ay - 17, [i === 4 && !hasAgent ? 70 : [150, 120, 138, 110, 128][i], [96, 130, 84, 118, 100][i]], 1, ["#DDD6CB", "#ECE7DF"], { gap: 22, thick: 12 });
  if (hasAgent) {
    drawToken(ctx, a.engine, r.w - 72, ay, 14, "#E6E0D6");
    if (t > PASTES[3] + 0.4) drawSpinner(ctx, r.w - 30, ay, 9, t + i * 0.4, a.skin.lo);
  }
  ctx.restore();
  // The tape that pasted it in stays on.
  if (i < 4) tape(ctx, r.x + r.w - 8, r.y + 8, 64, 38, 60 + i, clamp((t - born) * 12));
}

function drawMainPane(ctx: Ctx, w: number, h: number, t: number) {
  const s = selected(t);
  ctx.save();
  ctx.beginPath();
  ctx.rect(SIDEBAR, 0, w - SIDEBAR, h);
  ctx.clip();
  if (s >= 0) {
    const since0 = selectedAt(s);
    const k = outCubic(seg(t, since0, since0 + 0.3));
    ctx.save();
    ctx.globalAlpha *= k;
    ctx.translate(0, (1 - k) * 40);
    drawConversation(ctx, s, t, since0, w);
    ctx.restore();
  }
  drawComposer(ctx, t);
  if (t > PILL_CLICK && t < PICK + 0.3) drawMenu(ctx, t);
  ctx.restore();
}

function drawConversation(ctx: Ctx, s: number, t: number, since0: number, w: number) {
  const a = SESSIONS[s];
  const left = SIDEBAR + 64;
  const hasAgent = s < 4 || t >= PI_ARRIVES;
  const sending = s < 4 ? 1 : seg(t, SEND_CLICK, SEND_CLICK + 0.3);
  if (sending > 0) {
    const sent = outBack(sending);
    const bw = 330, bx = w - 64 - bw, by = s < 4 ? 58 : lerp(COMPOSER.y - 40, 58, clamp(sent));
    ctx.fillStyle = "#EFEAE2";
    ctx.beginPath();
    ctx.roundRect(bx, by, bw, 92, 26);
    ctx.fill();
    drawLines(ctx, bx + 28, by + 26, [250, 170], 1, ["#D5CDBF"], { gap: 26, thick: 13 });
  }
  if (!hasAgent) return;
  const k = s < 4 ? 1 : outBack(seg(t, PI_ARRIVES, PI_ARRIVES + 0.4));
  const beat0 = Math.sin(t * 12) * 0.03;
  drawBall(ctx, {
    ...agent(a, left + 40, 250, 40 * k), sx: 1 + beat0, sy: 1 - beat0, rim: 4,
    eyes: { kind: t - since0 < 0.45 ? "happy" : "open", open: blinkFor(t, s + 7) }, yaw: 0.45, pitch: -0.15,
    sway: Math.sin(t * 5) * 0.3,
  });
  const written = s < 4 ? 0.35 + ((t - since0) * 0.45) % 0.8 : (t - PI_ARRIVES) * 0.5;
  drawLines(ctx, left + 110, 205, [420, 520, 360, 470, 300], written, [a.theme === THEMES.terminal ? "#CFD8E6" : "#E6E0D6", a.skin.body], { gap: 36, thick: 15 });
}

function drawComposer(ctx: Ctx, t: number) {
  const box = new Path2D();
  box.roundRect(COMPOSER.x, COMPOSER.y, COMPOSER.w, COMPOSER.h, 30);
  cutout(ctx, box, "#FFFFFF", { rim: 0, dx: 3, dy: 5, grain: 0 });
  ctx.strokeStyle = "rgba(40,40,40,0.12)";
  ctx.lineWidth = 2;
  ctx.stroke(box);
  const s = selected(t);
  const engine = s < 0 ? "wuu" : s < 4 ? SESSIONS[s].engine : t >= PICK ? "pi" : "wuu";
  const bump = t > PICK ? squash(t, PICK, 0.2, 0.4) : squash(t, PILL_CLICK, 0.12, 0.3);
  ctx.save();
  ctx.translate(PILL.x + PILL.w / 2, PILL.y + PILL.h / 2);
  ctx.scale(bump.sx, bump.sy);
  ctx.fillStyle = t > PILL_CLICK && t < PICK ? "#EDE7DD" : "#F5F1EA";
  ctx.beginPath();
  ctx.roundRect(-PILL.w / 2, -PILL.h / 2, PILL.w, PILL.h, PILL.h / 2);
  ctx.fill();
  drawToken(ctx, engine, -PILL.w / 2 + 26, 0, 16, "#E2DBD0");
  ctx.strokeStyle = "#8F877B";
  ctx.lineWidth = 3.5;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  ctx.moveTo(12, -4); ctx.lineTo(20, 4); ctx.lineTo(28, -4);
  ctx.stroke();
  ctx.restore();
  drawLines(ctx, PILL.x + PILL.w + 26, COMPOSER.y + COMPOSER.h / 2 - 7, [260], s === 4 && t < SEND_CLICK ? seg(t, PICK, SEND_CLICK - 0.05) : 0, ["#E3DDD3"], { thick: 14 });
  const press = squash(t, SEND_CLICK, 0.2, 0.35);
  ctx.save();
  ctx.translate(SEND.x, SEND.y);
  ctx.scale(press.sx, press.sy);
  ctx.fillStyle = INK;
  ctx.beginPath();
  ctx.arc(0, 0, 27, 0, Math.PI * 2);
  ctx.fill();
  ctx.strokeStyle = "#FFFFFF";
  ctx.lineWidth = 4.5;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  ctx.moveTo(0, 11); ctx.lineTo(0, -10);
  ctx.moveTo(-9, -2); ctx.lineTo(0, -11); ctx.lineTo(9, -2);
  ctx.stroke();
  ctx.restore();
}

const HOVERED = [0, 2, 5];

function drawMenu(ctx: Ctx, t: number) {
  const open = outBack(seg(t, PILL_CLICK, PILL_CLICK + 0.25)) * (1 - inCubic(seg(t, PICK + 0.05, PICK + 0.25)));
  if (open <= 0) return;
  ctx.save();
  ctx.translate(MENU_BOX.x + 40, MENU_BOX.y + MENU_BOX.h);
  ctx.scale(open, open);
  ctx.translate(-MENU_BOX.x - 40, -MENU_BOX.y - MENU_BOX.h);
  const box = new Path2D();
  box.roundRect(MENU_BOX.x, MENU_BOX.y, MENU_BOX.w, MENU_BOX.h, 26);
  cutout(ctx, box, "#FFFFFF", { rim: 0, dx: 5, dy: 8, grain: 0 });
  let h = -1;
  HOVERS.forEach((at, i) => { if (t >= at) h = HOVERED[i]; });
  if (t >= PICK) h = PI_SLOT;
  MENU.forEach((engine, i) => {
    const k = outBack(seg(t, PILL_CLICK + 0.06 + i * 0.025, PILL_CLICK + 0.3 + i * 0.025));
    if (k <= 0) return;
    const [x, y] = menuSlot(i);
    const lift0 = i === h ? 8 : 0;
    const picked = i === PI_SLOT && t > PICK ? outBack(seg(t, PICK, PICK + 0.2)) * 0.25 : 0;
    ctx.save();
    ctx.translate(x, y - lift0);
    ctx.scale(k * (1 + picked), k * (1 + picked));
    if (i === h) {
      ctx.fillStyle = "#F3EEE6";
      ctx.beginPath();
      ctx.arc(0, 0, 38, 0, Math.PI * 2);
      ctx.fill();
    }
    drawToken(ctx, engine, 0, 0, 27, "#E4DDD2");
    ctx.restore();
  });
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Wuu

interface Pose { x: number; y: number; ball: Ball }

function wuuPose(t: number): Pose {
  if (t >= RIDE && t < PEELS[3] + beat(1)) {
    const r = ridePose(t);
    // Wuu stands on the pivot, upright however the scissors turn.
    const land = t >= PEELS[3] ? seg(t, PEELS[3], PEELS[3] + beat(1)) : 0;
    const from = r.pivot;
    const hop0 = hop(t, PEELS[3], beat(1));
    const x = t >= PEELS[3] ? lerp(around(CLIPS[3], 1).x, HOME.x, inOut(land)) : from[0];
    const floor = t >= PEELS[3] ? lerp(around(CLIPS[3], 1).y - 26, HOME.floor, inOut(land)) : from[1] - 26 - r.air * 20;
    const b = stand(WUU, x, floor, HOME.r, t >= PEELS[3] ? hop0 : { lift: 0, sx: 1, sy: 1 }, 260);
    return { x: b.x, y: b.y, ball: b };
  }
  if (t >= RIDE) {
    const h = hops(t, [beat(49.5), PI_ARRIVES + 0.1]);
    const b = stand(WUU, HOME.x, HOME.floor, HOME.r, h, 60);
    return { x: b.x, y: b.y, ball: { ...b, ground: HOME.floor } };
  }
  const shiver = t > OVERLOAD ? wobble(t, 2, 30) * 2.5 * seg(t, OVERLOAD, HUSH) : 0;
  const b = stand(WUU, CORNER.x + shiver, CORNER.floor, CORNER.r, { lift: 0, ...squash(t, at("wake"), 0.06, 0.5) });
  return { x: b.x, y: b.y, ball: { ...b, ground: CORNER.floor } };
}

function hops(t: number, starts: number[]) {
  for (const s of starts) if (t > s && t < s + 0.5) return hop(t, s, 0.5);
  return { lift: 0, sx: 1, sy: 1 };
}

const HELLO = cue("hello");
const EXPRESSION: [number, EyeKind][] = [
  [0, "content"], [at("wake"), "wide"], [at("wake") + 0.35, "open"], [HELLO[0], "happy"], [beat(4.6), "open"],
  [beat(20), "flat"], [SPLITS[0], "squeeze"], [HUSH, "wide"], [HUSH + beat(2.2), "open"], [IDEA, "star"],
  [at("test-snip"), "happy"], [RIDE, "squeeze"], [PEELS[3], "happy"], [APP_LANDS + 0.5, "open"], [PI_ARRIVES, "happy"],
];
const BLINKS = [beat(5.6), beat(12.5), HUSH + beat(1.2), beat(46), beat(53.5)];

function wuuFace(t: number, pose: Pose) {
  const cur = cursorState(t);
  const atCursor = look(pose.x, pose.y, cur.x, cur.y, 1.1);
  if (t < HELLO[0] - 0.1) {
    const upAtLamp = smooth(seg(t, at("wake") + 0.2, at("wake") + 0.45));
    return { yaw: lerp(0, -0.35, upAtLamp), pitch: lerp(-0.1, 0.45, upAtLamp), roll: 0 };
  }
  if (t < beat(4.6)) {
    const k = outBack(seg(t, HELLO[0] - 0.1, HELLO[0] + 0.15));
    return { yaw: lerp(-0.35, ICON_POSE.yaw, k), pitch: lerp(0.45, ICON_POSE.pitch, k), roll: lerp(0, ICON_POSE.roll, k) };
  }
  if (t < LIGHTS) {
    const k = smooth(seg(t, beat(4.6), beat(5.1)));
    return { yaw: lerp(ICON_POSE.yaw, -0.62, k), pitch: lerp(ICON_POSE.pitch, 0.05, k), roll: lerp(ICON_POSE.roll, 0, k) };
  }
  if (t < HUSH) return { ...atCursor, roll: 0 };
  if (t < RIDE) {
    // Stares out of the silent panel, follows the marble down, looks back up.
    const m = marble(t);
    const down = look(pose.x, pose.y, m.x, m.y, 1.3);
    const k = smooth(seg(t, HUSH + 0.15, HUSH + 0.35)) * (1 - smooth(seg(t, HUSH + beat(2.2), HUSH + beat(2.5))));
    return { yaw: lerp(0, down.yaw, k), pitch: lerp(0.05, down.pitch, k), roll: 0 };
  }
  if (t < PEELS[3] + beat(1)) {
    const k = CUT_STARTS.findIndex((s, i) => t >= s && t < PEELS[i]);
    return { yaw: k >= 0 ? 0.35 * Math.cos(rad(ridePose(t).angle)) : 0, pitch: -0.15, roll: 0 };
  }
  return { ...atCursor, roll: 0 };
}

function drawWuu(ctx: Ctx, t: number) {
  const pose = wuuPose(t);
  const face = wuuFace(t, pose);
  const hello = HELLO.map((h) => seg(t, h, h + 0.2) * (1 - seg(t, beat(5.3), beat(5.7))));
  const sweat = seg(t, beat(20), beat(20.5)) * (1 - seg(t, at("test-snip"), at("test-snip") + 0.2));
  drawBall(ctx, {
    ...pose.ball, ...face, eyes: eyes(t, EXPRESSION, BLINKS), sweat,
    marks: t < LIGHTS ? hello : undefined, markAngle: ICON_POSE.markAngle,
  });
}

// ---------------------------------------------------------------------------
// The user's cursor

interface CursorState { x: number; y: number; rot: number; press: number; daze: number; alpha: number }

function glide(t: number, stops: [number, number, number][]): [number, number] {
  if (t <= stops[0][0]) return [stops[0][1], stops[0][2]];
  for (let i = 1; i < stops.length; i++) {
    const [t1, x1, y1] = stops[i];
    if (t <= t1) {
      const [t0, x0, y0] = stops[i - 1];
      const e = inOut(seg(t, t0, t1));
      const bend = Math.sin(e * Math.PI) * Math.min(60, Math.hypot(x1 - x0, y1 - y0) * 0.12);
      return [lerp(x0, x1, e) + bend * 0.3, lerp(y0, y1, e) - bend];
    }
  }
  const last = stops[stops.length - 1];
  return [last[1], last[2]];
}

const REST: [number, number] = [900, 760];
const FALLEN: [number, number] = [860, 985];
const clickPoint = (i: number): [number, number] => {
  const [x, y] = balloonAt(i);
  return [x - 20, y + 10];
};
const DESK_STOPS: [number, number, number][] = [
  [LIGHTS, 900, 1250], [beat(7.5), ...REST],
  ...ANSWERS.flatMap((answer, k): [number, number, number][] => {
    const [x, y] = clickPoint(CLICK_TARGETS[k]);
    const prev = k === 0 ? beat(7.5) : ANSWERS[k - 1];
    const lead = Math.min(0.12, (answer - prev) * 0.35);
    return [[Math.max(prev + 0.04, answer - lead), x, y], [answer + 0.03, x, y]];
  }),
];

const rowPoint = (i: number): [number, number] => { const r = rowRect(i); return app(r.x + r.w * 0.55, r.y + r.h * 0.6); };
const APP_CLICKS: [number, [number, number]][] = [
  ...SELECT.map((at, i): [number, [number, number]] => [at, rowPoint(i)]),
  [PLUS_CLICK, app(PLUS.x + PLUS.w * 0.6, PLUS.y + PLUS.h * 0.6)],
  [PILL_CLICK, app(PILL.x + 60, PILL.y + 30)],
  ...HOVERS.map((at, i): [number, [number, number]] => [at, app(...menuSlot(HOVERED[i]))]),
  [PICK, app(...menuSlot(PI_SLOT))],
  [SEND_CLICK, app(SEND.x + 6, SEND.y + 8)],
];
const PRESSES = [...ANSWERS, ...SELECT, PLUS_CLICK, PILL_CLICK, PICK, SEND_CLICK];
const WAKES = beat(48.5);
const APP_STOPS: [number, number, number][] = [
  [WAKES + 0.3, FALLEN[0], FALLEN[1] - 30],
  ...APP_CLICKS.flatMap(([at, [x, y]], k): [number, number, number][] => {
    const prev = k === 0 ? WAKES + 0.3 : APP_CLICKS[k - 1][0];
    const lead = Math.min(0.14, (at - prev) * 0.4);
    return [[Math.max(prev + 0.04, at - lead), x, y], [at + 0.03, x, y]];
  }),
  [DESK_END, 1330, 660],
];

function cursorState(t: number): CursorState {
  const press = Math.max(0, ...PRESSES.map((at) => 1 - Math.abs(t - at) / 0.07));
  if (t < OVERLOAD) {
    const [x, y] = glide(t, DESK_STOPS);
    return { x, y, rot: 0, press, daze: 0, alpha: 1 };
  }
  if (t < HUSH) {
    // Nothing left to click: it spins in place, faster and faster.
    const [x0, y0] = glide(OVERLOAD, DESK_STOPS);
    const k = seg(t, OVERLOAD, HUSH);
    return { x: lerp(x0, 900, k) + wobble(t, 1, 6) * 160 * k, y: lerp(y0, 560, k) + wobble(t, 2, 6) * 110 * k, rot: 900 * k * k, press: 0, daze: k, alpha: 1 };
  }
  if (t < WAKES) return { x: FALLEN[0], y: FALLEN[1], rot: 104, press: 0, daze: 1, alpha: 1 };
  const up = outBack(seg(t, WAKES, WAKES + 0.3));
  if (t < WAKES + 0.3) return { x: FALLEN[0], y: FALLEN[1] - 30 * up, rot: lerp(104, 0, up), press: 0, daze: 1 - up, alpha: 1 };
  const [x, y] = glide(t, APP_STOPS);
  return { x, y, rot: 0, press, daze: 0, alpha: 1 };
}

function drawTheCursor(ctx: Ctx, t: number) {
  if (t >= HUSH && t < RIDE) return;
  const c = cursorState(t);
  for (const at of PRESSES) {
    const p = seg(t, at, at + 0.35);
    if (p <= 0 || p >= 1) continue;
    drawRipple(ctx, c.x, c.y, p, at < HUSH ? TOMATO : INK);
    // Comic click ticks around the tip.
    if (p < 0.4) {
      ctx.save();
      ctx.strokeStyle = INK;
      ctx.lineWidth = 4;
      ctx.lineCap = "round";
      ctx.globalAlpha *= 1 - p / 0.4;
      for (const a of [-150, -105, -60]) {
        const r0 = 20 + 24 * p, r1 = r0 + 14;
        ctx.beginPath();
        ctx.moveTo(c.x + Math.cos(rad(a)) * r0, c.y + Math.sin(rad(a)) * r0);
        ctx.lineTo(c.x + Math.cos(rad(a)) * r1, c.y + Math.sin(rad(a)) * r1);
        ctx.stroke();
      }
      ctx.restore();
    }
  }
  drawCursor(ctx, c.x, c.y, { rot: c.rot, press: c.press, alpha: c.alpha, scale: 1.9 });
  drawDaze(ctx, c.x + 18, c.y - 30, 58, t, c.daze);
}
