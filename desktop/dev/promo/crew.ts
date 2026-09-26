// Shots 7–10, one page (beats 60–108):
//   7. The cursor drags in a job as big as the page: a dashed outline of a
//      giant Wuu on graph paper. Wuu hops onto it and becomes a stack of
//      cut-outs; three snips, and eight copies are dealt across the page.
//   8. The job tears into a 3 × 3 comic page. Each copy is pasted over as a
//      different harness, and every panel becomes a session.
//   9. Each session collages its part of the outline. One gets stuck; a
//      finished neighbour leaps across the gutter and helps.
//  10. The panels are stamped done, Wuu grows into the middle, and the torn
//      pieces close up into the giant. The crew drops to the floor.
import { drawBall, drawCursor, drawLogo, extent, TITLE_BAR, WUU, type Ball, type Skin } from "./art";
import {
  clamp, hop, inCubic, inOut, lerp, outBack, outCubic, rad, seg, smooth, squash, type EyeKind,
} from "./motion";
import {
  agent, CLAUDE, CODEX, CURSOR, DEVIN, drawScissors, GROK, H, HERMES, look, OPENCODE, PI, stand, W,
  withCamera, type Agent, type Camera, type Ctx,
} from "./cast";
import {
  balloon, burstPath, cutout, dots, focusLines, INK, PAPER, ransom, rng, sheet, speedLines, tape,
  tearLine, TOMATO, WHITE,
} from "./paper";
import { at, beat, cue, type CueId } from "./timeline";

const CREW_START = beat(60);
export const CREW_END = beat(108);

// ---------------------------------------------------------------------------
// Layout

/** The page floor, where Wuu first stands and the crew finally lines up. */
const FLOOR = 1040;
const SHEET = { x: 30, y: 24, w: 1860, h: 1032 };
const XS = [30, 640, 1280, 1890];
const YS = [24, 360, 720, 1056];
const FLOORS = [338, 698, 1034];
/** The job: the outline the crew fills, and the giant it becomes. */
export const GIANT = { x: 960, y: 566, r: 440 };
const GAP = 24;
const DOLL_R = 74;
const CREW_R = 70;
const WUU_R = 90;

interface Member { agent: Agent; c: number; row: number; x: number; lineup: number }
// Clockwise from the top-left; the same order deals, hops and evolves them.
const TEAM: Member[] = [
  { agent: CODEX, c: 0, row: 0, x: 200, lineup: 450 },
  { agent: CLAUDE, c: 1, row: 0, x: 790, lineup: 610 },
  { agent: CURSOR, c: 2, row: 0, x: 1740, lineup: 1470 },
  { agent: PI, c: 2, row: 1, x: 1740, lineup: 1630 },
  { agent: HERMES, c: 2, row: 2, x: 1740, lineup: 1790 },
  { agent: GROK, c: 1, row: 2, x: 1150, lineup: 1310 },
  { agent: DEVIN, c: 0, row: 2, x: 190, lineup: 130 },
  { agent: OPENCODE, c: 0, row: 1, x: 190, lineup: 290 },
];
const STUCK_ONE = 1;
const HELPER = 0;
const cueOf = (kind: string, i: number) => `${kind}.${TEAM[i].agent.engine}` as CueId;

// ---------------------------------------------------------------------------
// Timeline

const JOB_LANDS = at("thump");
const FOLD = at("fold");
const DOLL_SNIPS = cue("doll-snip");
const DEALS = cue("unfold");
const TEARS = cue("tear");
const HOPS = cue("hop");
const EVOLVE = TEAM.map((_, i) => at(cueOf("evolve", i)));
const STUCK = at("stuck");
const LEAP = at("leap");
const HAND = at("hand");
const AHA = at("aha");
const CHECKS = TEAM.map((_, i) => at(cueOf("check", i)));
const ASSEMBLE = at("assemble");
const LOCK = at("lock");
const DROPS = TEAM.map((_, i) => at(cueOf("drop", i)));
const LEAP_START = LEAP - beat(0.45);
const FINAL_CHORD = at("marks");

// Every paste lands in a panel: the member's own, except the helper's taps
// after it has crossed over.
const TAPS: { t: number; member: number; cell: number }[] = TEAM.flatMap((_, i) =>
  cue(cueOf("work", i)).map((t) => ({ t, member: i, cell: i === HELPER && t > HAND ? cellOf(STUCK_ONE) : cellOf(i) })),
).sort((a, b) => a.t - b.t);
function cellOf(i: number) { return TEAM[i].row * 3 + TEAM[i].c; }
const CENTRE = 4;

// ---------------------------------------------------------------------------
// The job sheet and its pieces

const opened = (i: number, t: number) => outBack(seg(t, TEARS[i] + 0.1, TEARS[i] + 0.38));
const torn = (i: number, t: number) => t >= TEARS[i] + 0.1;
/** Tears 0–1 are the column lines, 2–3 the row lines. */
function offset(c: number, row: number, t: number): [number, number] {
  const assembled = inOut(seg(t, ASSEMBLE, ASSEMBLE + beat(0.8)));
  const dx = c === 0 ? -GAP * opened(0, t) : c === 2 ? GAP * opened(1, t) : 0;
  const dy = row === 0 ? -GAP * opened(2, t) : row === 2 ? GAP * opened(3, t) : 0;
  return [dx * (1 - assembled), dy * (1 - assembled)];
}

/** The cursor pulls the job down like a blind, faster and faster until it slams. */
function drop(t: number) {
  return lerp(0.18, 1, inCubic(seg(t, CREW_START, JOB_LANDS)));
}

/** The piece outline, with torn sides where a tear has passed. */
function piecePath(c: number, row: number, t: number) {
  const x0 = XS[c], x1 = XS[c + 1], y0 = YS[row], y1 = YS[row + 1];
  // Internal edges overlap a hair so untorn neighbours read as one sheet.
  const e = 0.6;
  const edge = (horizontal: boolean, line: number, from: number, to: number, tear: number): [number, number][] => {
    const a: [number, number] = horizontal ? [from, line] : [line, from];
    const b: [number, number] = horizontal ? [to, line] : [line, to];
    if (tear < 0 || !torn(tear, t)) return [a, b];
    const seed = 100 + tear * 10 + (horizontal ? c : row);
    const pts = horizontal ? tearLine(Math.min(from, to), line, Math.max(from, to), line, seed, 8) : tearLine(line, Math.min(from, to), line, Math.max(from, to), seed, 8);
    return from < to ? pts : pts.reverse();
  };
  const top = edge(true, y0 - (row > 0 ? e : 0), x0, x1, row === 0 ? -1 : row === 1 ? 2 : 3);
  const right = edge(false, x1 + (c < 2 ? e : 0), y0, y1, c === 2 ? -1 : c === 0 ? 0 : 1);
  const bottom = edge(true, y1 + (row < 2 ? e : 0), x1, x0, row === 2 ? -1 : row === 0 ? 2 : 3);
  const left = edge(false, x0 - (c > 0 ? e : 0), y1, y0, c === 0 ? -1 : c === 1 ? 0 : 1);
  const p = new Path2D();
  [...top, ...right.slice(1), ...bottom.slice(1), ...left.slice(1)].forEach(([x, y], i) => (i === 0 ? p.moveTo(x, y) : p.lineTo(x, y)));
  p.closePath();
  return p;
}

const GRID = "#D7E3EA";
const GRID_BOLD = "#C3D4DF";

function graphPaper(ctx: Ctx) {
  ctx.fillStyle = "#F8F9F5";
  ctx.fillRect(SHEET.x - 40, SHEET.y - 40, SHEET.w + 80, SHEET.h + 80);
  ctx.lineWidth = 1.5;
  for (let x = SHEET.x; x <= SHEET.x + SHEET.w; x += 40) {
    ctx.strokeStyle = (x - SHEET.x) % 200 === 0 ? GRID_BOLD : GRID;
    ctx.beginPath();
    ctx.moveTo(x, SHEET.y - 40);
    ctx.lineTo(x, SHEET.y + SHEET.h + 40);
    ctx.stroke();
  }
  for (let y = SHEET.y; y <= SHEET.y + SHEET.h; y += 40) {
    ctx.strokeStyle = (y - SHEET.y) % 200 === 0 ? GRID_BOLD : GRID;
    ctx.beginPath();
    ctx.moveTo(SHEET.x - 40, y);
    ctx.lineTo(SHEET.x + SHEET.w + 40, y);
    ctx.stroke();
  }
}

function giantPath() {
  const p = new Path2D();
  p.arc(GIANT.x, GIANT.y, GIANT.r, 0, Math.PI * 2);
  return p;
}

/** The job sheet and its pieces; from the lock on, without the giant drawn over it. */
export function drawJob(ctx: Ctx, t: number) {
  const fall = drop(t);
  const y = lerp(-H - 40, 0, fall);
  const land = squash(t, JOB_LANDS, 0.04, 0.4);
  ctx.save();
  ctx.translate(W / 2, SHEET.y + SHEET.h);
  ctx.scale(land.sx, land.sy);
  ctx.translate(-W / 2, -(SHEET.y + SHEET.h) + y);
  const cells = [0, 1, 2].flatMap((row) => [0, 1, 2].map((c) => ({ c, row })));
  // Shadows first, so no piece's shadow lands on a neighbour it still touches.
  for (const { c, row } of cells) {
    const [dx, dy] = offset(c, row, t);
    ctx.save();
    ctx.translate(dx + 7, dy + 10);
    ctx.fillStyle = "rgba(52, 38, 22, 0.2)";
    ctx.fill(piecePath(c, row, t));
    ctx.restore();
  }
  for (const { c, row } of cells) drawPiece(ctx, c, row, t);
  drawCracks(ctx, t);
  ctx.restore();
}

function memberIn(cell: number) {
  return TEAM.findIndex((m) => m.row * 3 + m.c === cell);
}

function drawPiece(ctx: Ctx, c: number, row: number, t: number) {
  const cell = row * 3 + c;
  const i = memberIn(cell);
  const [dx, dy] = offset(c, row, t);
  const path = piecePath(c, row, t);
  const closing = seg(t, ASSEMBLE, ASSEMBLE + beat(0.8));
  ctx.save();
  ctx.translate(dx, dy);
  // Torn edges show the paper's white core.
  if (TEARS.some((_, k) => torn(k, t))) {
    ctx.strokeStyle = WHITE;
    ctx.lineWidth = 7 * (1 - closing);
    ctx.lineJoin = "round";
    if (closing < 1) ctx.stroke(path);
  }
  ctx.save();
  ctx.clip(path);
  graphPaper(ctx);
  if (i >= 0 && t >= EVOLVE[i]) {
    // Each session's panel takes a halftone tint of its harness.
    ctx.globalAlpha = 0.5 * smooth(seg(t, EVOLVE[i], EVOLVE[i] + 0.3)) * (1 - closing);
    ctx.fillStyle = dots(ctx, TEAM[i].agent.skin.body, 16, 3.2);
    ctx.fillRect(XS[c], YS[row], XS[c + 1] - XS[c], YS[row + 1] - YS[row]);
    ctx.globalAlpha = 1;
  }
  if (t < LOCK) {
    ctx.save();
    ctx.setLineDash([18, 14]);
    ctx.lineDashOffset = -t * 30;
    ctx.strokeStyle = "rgba(38, 39, 43, 0.55)";
    ctx.lineWidth = 5;
    ctx.stroke(giantPath());
    // The brief's eyes, where Wuu's own will end up.
    for (const side of [-1, 1]) {
      ctx.beginPath();
      ctx.roundRect(GIANT.x + side * GIANT.r * 0.205 - GIANT.r * 0.095, GIANT.y + GIANT.r * 0.04 - GIANT.r * 0.2, GIANT.r * 0.19, GIANT.r * 0.4, GIANT.r * 0.095);
      ctx.stroke();
    }
    ctx.restore();
  }
  if (t < LOCK) {
    drawPatches(ctx, cell, t);
    if (cell === CENTRE) drawCore(ctx, t);
  }
  if (i === STUCK_ONE) {
    // Stuck: the panel drains to grey until help arrives.
    const k = smooth(seg(t, STUCK, STUCK + 0.25)) * (1 - smooth(seg(t, AHA, AHA + 0.2)));
    if (k > 0) {
      ctx.globalAlpha = 0.55 * k;
      ctx.fillStyle = dots(ctx, "#8A8C90", 10, 3.6);
      ctx.fillRect(XS[c], YS[row], XS[c + 1] - XS[c], YS[row + 1] - YS[row]);
      ctx.globalAlpha = 1;
    }
  }
  ctx.restore();
  if (i >= 0 && t >= EVOLVE[i]) drawBar(ctx, c, row, i, t, closing);
  if (cell === CENTRE && t >= EVOLVE[0]) drawBar(ctx, c, row, -1, t, closing);
  ctx.restore();
}

/** While a tear runs, it is an inked crack racing across the sheet. */
function drawCracks(ctx: Ctx, t: number) {
  TEARS.forEach((at0, k) => {
    const p = seg(t, at0, at0 + 0.1);
    if (p <= 0 || p >= 1) return;
    const vertical = k < 2;
    const line = vertical ? XS[k + 1] : YS[k - 1];
    const pts: [number, number][] = [];
    for (let s = 0; s < 3; s++) {
      const seed = 100 + k * 10 + s;
      const seg0 = vertical ? tearLine(line, YS[s], line, YS[s + 1], seed, 8) : tearLine(XS[s], line, XS[s + 1], line, seed, 8);
      pts.push(...(s === 0 ? seg0 : seg0.slice(1)));
    }
    const n = Math.max(2, Math.round(pts.length * p));
    ctx.save();
    ctx.strokeStyle = INK;
    ctx.lineWidth = 4;
    ctx.lineJoin = "round";
    ctx.beginPath();
    pts.slice(0, n).forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)));
    ctx.stroke();
    ctx.restore();
  });
  // Each tear shouts where it starts.
  TEARS.forEach((at0, k) => {
    const [x, y] = k < 2 ? [XS[k + 1] + 70, 96] : [150, YS[k - 1] - 44];
    ransom(ctx, "rrip", x, y, 58, t, at0, 140 + k, { hold: 0.4, angle: k % 2 ? 7 : -6 });
  });
}

function drawBar(ctx: Ctx, c: number, row: number, i: number, t: number, closing: number) {
  const born = i >= 0 ? EVOLVE[i] : EVOLVE[0];
  const k = outBack(seg(t, born, born + 0.25)) * (1 - smooth(seg(closing, 0, 0.6)));
  if (k <= 0) return;
  const x = XS[c] + 18, w = XS[c + 1] - XS[c] - 36, y = YS[row] + 14;
  ctx.save();
  ctx.translate(x + w / 2, y + TITLE_BAR / 2);
  ctx.scale(k, k);
  ctx.translate(-(x + w / 2), -(y + TITLE_BAR / 2));
  const strip = new Path2D();
  strip.roundRect(x, y, w, TITLE_BAR, 12);
  cutout(ctx, strip, "#F1F2F0", { rim: 3, dx: 3, dy: 4, grain: 0.6 });
  ctx.fillStyle = "#D9DBD6";
  for (let d = 0; d < 3; d++) {
    ctx.beginPath();
    ctx.arc(x + 24 + d * 20, y + TITLE_BAR / 2, 6.5, 0, Math.PI * 2);
    ctx.fill();
  }
  drawLogo(ctx, i >= 0 ? TEAM[i].agent.engine : "wuu", x + w / 2, y + TITLE_BAR / 2, 22, INK);
  tape(ctx, x + 6, y + 6, 60, -40, 70 + c + row * 3, 0.9);
  if (i >= 0) drawStamp(ctx, x + w - 34, y + TITLE_BAR / 2 + 4, t, CHECKS[i], 120 + i);
  ctx.restore();
}

/** A rubber-stamped tick: slams down big and settles. */
function drawStamp(ctx: Ctx, x: number, y: number, t: number, at0: number, seed: number) {
  if (t < at0) return;
  const k = seg(t, at0, at0 + 0.14);
  const s = lerp(2.2, 1, outCubic(k));
  const r = rng(seed);
  ctx.save();
  ctx.translate(x, y);
  ctx.rotate(rad(-12 + r() * 10));
  ctx.scale(s, s);
  ctx.globalAlpha *= 0.35 + 0.65 * k;
  ctx.strokeStyle = TOMATO;
  ctx.lineWidth = 5;
  ctx.beginPath();
  ctx.arc(0, 0, 25, 0, Math.PI * 2);
  ctx.stroke();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.lineWidth = 7;
  ctx.beginPath();
  ctx.moveTo(-11, 1);
  ctx.lineTo(-3, 10);
  ctx.lineTo(13, -10);
  ctx.stroke();
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Collage patches: every paste adds one scrap of charcoal paper.

interface Patch { path: Path2D; color: string; cx: number; cy: number; tilt: number; t: number; halftone: boolean }
const PATCH_COLORS = ["#353637", "#2E2F31", "#3B3C3E", "#323336"];

const PATCHES: Patch[][] = Array.from({ length: 9 }, (_, cell) => {
  const taps = TAPS.filter((p) => p.cell === cell).map((p) => p.t);
  if (!taps.length) return [];
  const c = cell % 3, row = Math.floor(cell / 3);
  const x0 = Math.max(XS[c], GIANT.x - GIANT.r), x1 = Math.min(XS[c + 1], GIANT.x + GIANT.r);
  const y0 = Math.max(YS[row], GIANT.y - GIANT.r), y1 = Math.min(YS[row + 1], GIANT.y + GIANT.r);
  const r = rng(cell * 31 + 5);
  // Strips across the region, pasted in a shuffled order.
  const vertical = row === 1;
  const n = taps.length;
  const order = Array.from({ length: n }, (_, k) => k).sort(() => r() - 0.5);
  return order.map((k, j) => {
    const a = k / n, b = (k + 1) / n;
    const p = new Path2D();
    const over = 16;
    const pts: [number, number][] = vertical
      ? [
        ...tearLine(x0 - over, lerp(y0, y1, a) - over, x1 + over, lerp(y0, y1, a) - over, cell * 97 + k, 6),
        ...tearLine(x1 + over, lerp(y0, y1, b) + over, x0 - over, lerp(y0, y1, b) + over, cell * 97 + k + 50, 6),
      ]
      : [
        ...tearLine(lerp(x0, x1, a) - over, y0 - over, lerp(x0, x1, a) - over, y1 + over, cell * 97 + k, 6),
        ...tearLine(lerp(x0, x1, b) + over, y1 + over, lerp(x0, x1, b) + over, y0 - over, cell * 97 + k + 50, 6),
      ];
    pts.forEach(([x, y], i) => (i === 0 ? p.moveTo(x, y) : p.lineTo(x, y)));
    p.closePath();
    const cx = vertical ? (x0 + x1) / 2 : lerp(x0, x1, (a + b) / 2);
    const cy = vertical ? lerp(y0, y1, (a + b) / 2) : (y0 + y1) / 2;
    return { path: p, color: PATCH_COLORS[Math.floor(r() * PATCH_COLORS.length)], cx, cy, tilt: (r() - 0.5) * 4, t: taps[j], halftone: r() < 0.2 };
  });
});

/** Every pasted scrap at once, for the end card to fade over the clean giant. */
export function drawSeams(ctx: Ctx, t: number) {
  for (let cell = 0; cell < 9; cell++) drawPatches(ctx, cell, t);
}

function drawPatches(ctx: Ctx, cell: number, t: number) {
  const list = PATCHES[cell];
  if (!list.length) return;
  ctx.save();
  ctx.clip(giantPath());
  for (const p of list) {
    if (t < p.t) continue;
    const k = seg(t, p.t, p.t + 0.12);
    const s = lerp(1.18, 1, outCubic(k));
    ctx.save();
    ctx.translate(p.cx, p.cy);
    ctx.rotate(rad(p.tilt * (1 - k * 0.6)));
    ctx.scale(s, s);
    ctx.translate(-p.cx, -p.cy);
    cutout(ctx, p.path, p.color, { rim: 0, dx: 2, dy: 3, grain: 0.35 });
    if (p.halftone) {
      ctx.save();
      ctx.clip(p.path);
      ctx.fillStyle = dots(ctx, "#4A4B4E", 12, 3.4);
      ctx.fill(p.path);
      ctx.restore();
    }
    ctx.restore();
  }
  ctx.restore();
}

// ---------------------------------------------------------------------------
// Wuu in the centre: stands, stacks, deals copies, then grows into the core.

/** Wuu grows one step with every stamp, and the last step as the pieces close. */
function coreRadius(t: number) {
  const steps = [...CHECKS, ASSEMBLE].reduce((n, at0) => n + outBack(seg(t, at0, at0 + 0.22)), 0);
  return lerp(WUU_R, GIANT.r, steps / (CHECKS.length + 1));
}

function corePose(t: number): Ball {
  const r = coreRadius(t);
  const grow = (r - WUU_R) / (GIANT.r - WUU_R);
  const [dx, dy] = offset(1, 1, t);
  const b = stand(WUU, GIANT.x + dx, FLOORS[1] + dy, r);
  return { ...b, y: lerp(b.y, GIANT.y, grow) };
}

function drawCore(ctx: Ctx, t: number) {
  // Drawn inside the centre piece, so the growing body is cut by its edges.
  if (t < FOLD) return;
  const [dx, dy] = offset(1, 1, t);
  ctx.save();
  ctx.translate(-dx, -dy);
  const eyes = t >= LOCK ? giant(t).eyes : { kind: coreEyes(t), open: 1 };
  drawBall(ctx, { ...corePose(t), ...coreFace(t), eyes, rim: 0 });
  ctx.restore();
}

function coreEyes(t: number): EyeKind {
  if (t >= STUCK && t < AHA) return "open";
  if (t >= CHECKS[0]) return "happy";
  if (t >= DOLL_SNIPS[0] && t < DEALS[0]) return "squeeze";
  return "open";
}

function coreFace(t: number) {
  const c = corePose(t);
  if (t >= STUCK && t < AHA) {
    const target = t < LEAP_START ? memberPos(STUCK_ONE, t) : memberPos(HELPER, t);
    return look(c.x, c.y, target[0], target[1], 1.2);
  }
  if (t >= CHECKS[0]) return { yaw: 0, pitch: 0 };
  // Otherwise it glances at whoever pasted last.
  let last = -1;
  for (const p of TAPS) if (p.t <= t) last = p.member;
  if (last < 0 || t < TAPS[0].t) return { yaw: 0, pitch: 0.05 };
  const [x, y] = memberPos(last, t);
  return look(c.x, c.y, x, y, 0.8);
}

// ---------------------------------------------------------------------------
// The crew

function rest(i: number, t: number): [number, number] {
  const m = TEAM[i];
  const [dx, dy] = offset(m.c, m.row, t);
  return [m.x + dx, FLOORS[m.row] + dy];
}

/** Where member i stands (x, floor) at t. */
function memberPos(i: number, t: number): [number, number] {
  if (i === HELPER && t >= LEAP_START && t < DROPS[i]) {
    const [x1, y1] = rest(STUCK_ONE, t);
    const target: [number, number] = [x1 + 170, y1];
    if (t >= HAND) return target;
    const [x0, y0] = rest(HELPER, t);
    const p = seg(t, LEAP_START, HAND);
    return [lerp(x0, target[0], p), lerp(y0, target[1], p) - Math.sin(p * Math.PI) * 180];
  }
  if (t >= DROPS[i] - beat(0.5)) {
    const from = i === HELPER ? [rest(STUCK_ONE, t)[0] + 170, rest(STUCK_ONE, t)[1]] : rest(i, t);
    const p = seg(t, DROPS[i] - beat(0.5), DROPS[i]);
    return [lerp(from[0], TEAM[i].lineup, inOut(p)), lerp(from[1], FLOOR, inCubic(p)) - Math.sin(p * Math.PI) * 60];
  }
  return rest(i, t);
}

function dealt(i: number, t: number) {
  return seg(t, DEALS[i] - beat(0.5), DEALS[i]);
}

function memberSkin(i: number, t: number): { skin: Skin; agent?: Agent; slap: number } {
  if (t < EVOLVE[i]) return { skin: WUU, slap: 0 };
  return { skin: TEAM[i].agent.skin, agent: TEAM[i].agent, slap: seg(t, EVOLVE[i], EVOLVE[i] + 0.22) };
}

function memberEyes(i: number, t: number): EyeKind {
  if (i === STUCK_ONE && t >= STUCK && t < AHA) return "flat";
  if (t >= CHECKS[i] && t < CHECKS[i] + 0.5) return "happy";
  if (t >= DROPS[i] && t < DROPS[i] + 0.5) return "wide";
  if (t >= beat(106)) return "happy";
  if (t >= EVOLVE[i] && t < EVOLVE[i] + 0.35) return "wide";
  if (t >= HOPS[i] - 0.2 && t < HOPS[i] + 0.25) return "happy";
  return "open";
}

function tapHop(i: number, t: number) {
  // Each paste is a little lean-and-stamp toward the job.
  for (const p of TAPS) if (p.member === i && t >= p.t - 0.08 && t < p.t + 0.2) return squash(t, p.t, 0.16, 0.28);
  return { sx: 1, sy: 1 };
}

function drawMember(ctx: Ctx, i: number, t: number) {
  const m = TEAM[i];
  if (t < DEALS[i] - beat(0.5)) return;
  const [x, floor] = memberPos(i, t);
  const d = dealt(i, t);
  const { skin, agent: a, slap } = memberSkin(i, t);
  const home = stand(WUU, GIANT.x, FLOORS[1], DOLL_R);
  let ball: Ball;
  if (d < 1) {
    // Dealt from the stack: flies out flipping, like a card.
    const e = inOut(d);
    const bx = lerp(home.x, x, e), by = lerp(home.y, floor - DOLL_R * extent(WUU).ry, e) - Math.sin(e * Math.PI) * 120;
    ball = { x: bx, y: by, r: DOLL_R, skin: WUU, sx: Math.cos(e * Math.PI * 2) * 0.9 + 0.1, sy: 1 };
    speedLines(ctx, bx, by, (Math.atan2(by - home.y, bx - home.x) * 180) / Math.PI, 120, 60, 4, i * 7, 0.4 * (1 - e));
  } else {
    const r = a ? CREW_R : DOLL_R;
    const cheer = FINAL_CHORD - 0.3 + i * 0.04;
    // hop() touches down 80% of the way through, so each hop lands on its cue.
    const h = t >= HOPS[i] - 0.35 && t < HOPS[i] + 0.1 ? hop(t, HOPS[i] - 0.32, 0.4) : hop(t, cheer, 0.45);
    const tap = tapHop(i, t);
    const land = squash(t, DEALS[i], 0.18, 0.35);
    const dropLand = squash(t, DROPS[i], 0.25, 0.45);
    const base = a ? agent(a, x, floor, r, h, 60) : stand(skin, x, floor, r, h, 60);
    const pop = slap > 0 && slap < 1 ? lerp(1.35, 1, outBack(slap)) : 1;
    ball = {
      ...base, sx: base.sx! * tap.sx * land.sx * dropLand.sx * pop, sy: base.sy! * tap.sy * land.sy * dropLand.sy * pop,
      antenna: a ? outBack(seg(t, EVOLVE[i] + 0.05, EVOLVE[i] + 0.35)) : 0, sway: a ? Math.sin(t * 3 + i) * 0.12 : 0,
    };
  }
  const target = t < CHECKS[0] ? [GIANT.x, GIANT.y] : t < CREW_END ? [W / 2, H * 0.7] : [W / 2, 380];
  const face = look(ball.x, ball.y, target[0], target[1], 0.9);
  drawBall(ctx, { ...ball, ...face, eyes: { kind: memberEyes(i, t), open: blink(t, i) } });
  if (slap > 0 && slap < 1) {
    // A ring of spikes marks the new costume being slapped on.
    ctx.save();
    ctx.globalAlpha *= 1 - slap;
    ctx.strokeStyle = m.agent.skin.body;
    ctx.lineWidth = 6;
    ctx.stroke(burstPath(ball.x, ball.y, ball.r * (1.5 + slap), 12, 40 + i));
    ctx.restore();
  }
}

function blink(t: number, seed: number) {
  const period = 2.9 + seed * 0.31;
  const p = ((t + seed * 0.7) % period) / 0.18;
  return p > 0 && p < 1 ? (p < 0.4 ? 1 - 0.92 * smooth(p / 0.4) : 0.08 + 0.92 * smooth((p - 0.4) / 0.6)) : 1;
}

// ---------------------------------------------------------------------------

function camera(t: number): Camera {
  const wide = { x: W / 2, y: H / 2, zoom: 1 };
  // Lean in on the stuck panel and its neighbour, then back out.
  const close = { x: 700, y: 330, zoom: 1.45 };
  const k = inOut(seg(t, STUCK - beat(0.25), STUCK + beat(0.5))) * (1 - inOut(seg(t, AHA + beat(1), AHA + beat(2.5))));
  return { x: lerp(wide.x, close.x, k), y: lerp(wide.y, close.y, k), zoom: lerp(1, close.zoom, k) };
}

/** `page` and `crew` let the end card reuse the torn job without its backdrop or cast. */
export function crewShot(ctx: Ctx, t: number, o: { page?: boolean; crew?: boolean } = {}) {
  withCamera(ctx, camera(t), () => {
    if (o.page ?? true) sheet(ctx, -400, -400, W + 800, H + 800, PAPER);
    drawJob(ctx, t);
    if (t >= LOCK) {
      // Closed up, the giant is one body with the crew's scraps still on it.
      drawBall(ctx, giant(t));
      drawSeams(ctx, t);
    }
    drawOpening(ctx, t);
    if (o.crew ?? true) for (let i = 0; i < TEAM.length; i++) drawMember(ctx, i, t);
    drawHelp(ctx, t);
    drawFinale(ctx, t);
  });
}

/** Before the sheet lands, Wuu stands alone on the page; then it hops on and stacks. */
function drawOpening(ctx: Ctx, t: number) {
  if (t >= FOLD) {
    drawStack(ctx, t);
    return;
  }
  const hopUp = seg(t, FOLD - beat(0.6), FOLD);
  const e = inOut(hopUp);
  const floor = lerp(FLOOR, FLOORS[1], e);
  const b = stand(WUU, GIANT.x, floor, WUU_R, { lift: Math.sin(e * Math.PI) * 1.5, sx: 1, sy: 1 }, 100);
  const up = t >= CREW_START + 0.1 && t < JOB_LANDS ? { yaw: 0, pitch: 0.55 } : t < FOLD - beat(0.8) ? { yaw: 0.1, pitch: 0.4 } : { yaw: 0, pitch: 0 };
  const kind: EyeKind = t < JOB_LANDS ? "wide" : t < JOB_LANDS + beat(1.2) ? "wide" : "open";
  drawBall(ctx, { ...b, ...up, ground: hopUp === 0 ? FLOOR : undefined, eyes: { kind, open: 1 }, sweat: seg(t, JOB_LANDS + beat(1.2), JOB_LANDS + beat(1.5)) });
  // The user's cursor hauls the job down by its bottom edge, then lets go.
  if (t < JOB_LANDS + beat(1)) {
    const y = lerp(-H - 40, 0, drop(t)) + SHEET.y + SHEET.h - 18;
    const leave = seg(t, JOB_LANDS, JOB_LANDS + beat(1));
    drawCursor(ctx, 1240 + leave * 260, y - inCubic(leave) * 900, { scale: 2.2, press: t < JOB_LANDS ? 1 : 0 });
  }
  if (t >= JOB_LANDS && t < JOB_LANDS + 0.4) {
    focusLines(ctx, GIANT.x, FLOOR - 120, 520, 1500, 80, 9, INK, 0.7 * (1 - seg(t, JOB_LANDS, JOB_LANDS + 0.4)));
  }
  ransom(ctx, "thump", 440, 900, 96, t, JOB_LANDS, 61, { hold: 0.6, angle: -5 });
}

/** The stack: Wuu over the copies it is about to deal, cut in three snips. */
function drawStack(ctx: Ctx, t: number) {
  if (t >= DEALS[DEALS.length - 1]) return;
  const left = TEAM.length - DEALS.filter((d) => d - beat(0.5) <= t).length;
  for (let k = left; k >= 1; k--) {
    drawBall(ctx, { ...stand(WUU, GIANT.x + k * 5, FLOORS[1] - k * 4, DOLL_R), eyes: { kind: "open", open: 1 }, rim: 5 });
  }
  // Three snips cut the stack out; the scissors circle it once.
  const s = seg(t, DOLL_SNIPS[0] - 0.12, DOLL_SNIPS[2] + 0.1);
  if (s > 0 && s < 1) {
    const a = rad(-90 + s * 360);
    const cx = GIANT.x, cy = FLOORS[1] - DOLL_R;
    let open = 34;
    for (const sn of DOLL_SNIPS) open = Math.min(open, 34 * smooth(clamp(Math.abs(t - sn) / 0.1)));
    drawScissors(ctx, cx + Math.cos(a) * 118, cy + Math.sin(a) * 118, 170, (a * 180) / Math.PI + 90, open);
    for (const sn of DOLL_SNIPS) ransom(ctx, "snip", cx + 190, cy - 170, 44, t, sn, Math.round(sn * 77), { hold: 0.22, angle: 8, stagger: 0.02 });
  }
}

/** Stuck, the leap across the gutter, and the hand-over. */
function drawHelp(ctx: Ctx, t: number) {
  const [sx, sfloor] = memberPos(STUCK_ONE, t);
  // A question balloon while stuck; an exclamation burst when help lands.
  const q = outBack(seg(t, STUCK, STUCK + 0.25)) * (1 - seg(t, AHA, AHA + 0.1));
  balloon(ctx, sx + 120, sfloor - 230, 96, 80, sx + 40, sfloor - 150, q, () => {
    ctx.fillStyle = INK;
    ctx.font = "900 56px Georgia, serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText("?", sx + 120, sfloor - 226);
  });
  const aha = seg(t, AHA, AHA + 0.5);
  if (aha > 0 && aha < 1) {
    const burst = burstPath(sx + 110, sfloor - 220, 70 * outBack(clamp(aha * 3)), 14, 77);
    cutout(ctx, burst, "#FFE08A", { rim: 0, dx: 4, dy: 5 });
    ctx.strokeStyle = INK;
    ctx.lineWidth = 4;
    ctx.stroke(burst);
    ctx.fillStyle = TOMATO;
    ctx.font = "900 64px Impact, 'Arial Black', sans-serif";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillText("!", sx + 110, sfloor - 216);
  }
  // Breaking through the gutter scatters paper bits.
  const brk = seg(t, LEAP, LEAP + 0.45);
  if (brk > 0 && brk < 1) {
    const gx = XS[1] + offset(0, 0, t)[0] / 2, gy = FLOORS[0] - 160;
    const r = rng(9);
    for (let k = 0; k < 9; k++) {
      const a = r() * Math.PI * 2, d = 40 + 160 * outCubic(brk) * (0.5 + r());
      const bit = new Path2D();
      const bx = gx + Math.cos(a) * d, by = gy + Math.sin(a) * d + 120 * brk * brk;
      bit.moveTo(bx, by);
      bit.lineTo(bx + 14 + r() * 10, by + 4);
      bit.lineTo(bx + 4, by + 16 + r() * 8);
      bit.closePath();
      ctx.globalAlpha = 1 - brk;
      cutout(ctx, bit, "#F8F9F5", { rim: 0, dx: 2, dy: 3 });
      ctx.globalAlpha = 1;
    }
    const [hx, hy] = memberPos(HELPER, t);
    speedLines(ctx, hx, hy - 80, 0, 220, 90, 6, 31, 0.6 * (1 - brk));
    ransom(ctx, "rrip", gx, gy - 90, 58, t, LEAP, 91, { hold: 0.35, angle: -9 });
  }
  // The hand-over: a scrap passes from helper to the stuck one.
  const give = seg(t, HAND, AHA);
  if (give > 0 && give < 1) {
    const [hx, hy] = memberPos(HELPER, t);
    const x = lerp(hx - 60, sx + 60, inOut(give)), y = lerp(hy - 100, sfloor - 110, inOut(give)) - Math.sin(give * Math.PI) * 60;
    const scrap = new Path2D();
    scrap.rect(x - 22, y - 16, 44, 32);
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(rad(give * 200));
    ctx.translate(-x, -y);
    cutout(ctx, scrap, "#353637", { rim: 3, dx: 3, dy: 4 });
    ctx.restore();
  }
}

// ---------------------------------------------------------------------------
// Finale: the giant, and the letters on the lock

/** The giant Wuu once the pieces have closed, for the end card to shrink. */
export function giant(t: number): Ball {
  return { ...stand(WUU, GIANT.x, GIANT.y + GIANT.r, GIANT.r), eyes: { kind: t >= beat(106) && t < beat(106.6) ? "happy" : "open", open: blink(t, 11) }, rim: 0 };
}

function drawFinale(ctx: Ctx, t: number) {
  if (t >= LOCK && t < LOCK + 0.5) focusLines(ctx, GIANT.x, GIANT.y, GIANT.r + 40, 1500, 90, 17, INK, 0.5 * (1 - seg(t, LOCK, LOCK + 0.5)));
}

/** The crew as it stands at the end, for the end card. */
export function drawCrew(ctx: Ctx, t: number) {
  for (let i = 0; i < TEAM.length; i++) drawMember(ctx, i, t);
}
