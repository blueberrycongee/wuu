// Shots 6–9, one continuous world (31 s – 58 s):
//   6. The cursor selects Wuu and drags out a job many times its size. Alone,
//      Wuu can only puff itself up.
//   7. An idea: Wuu splits into three, then nine. Each split tears the screen
//      into more windows, and eight copies evolve into different harnesses.
//   8. The original grows to fill the middle window and watches eight sessions
//      fill in their part of the job. One gets stuck; a finished one hops over.
//   9. The windows fuse into the finished job, a giant Wuu. The crew drops to
//      the floor, and a click strikes the icon pose.
import { AVATAR_HUES } from "../../src/renderer/DefaultAvatar";
import {
  agentSkin, drawBall, drawBubble, drawCursor, drawLogo, drawRipple, drawShadow, extent,
  ICON_POSE, INK, THEMES, TITLE_BAR, WUU, type Ball, type Skin,
} from "./art";
import {
  clamp, eyes, hop, hops, inCubic, inOut, keys, lerp, mixColor, outBack, outCubic, outElastic, seg, smooth,
  squash, wobble, type EyeKind,
} from "./motion";
import {
  CLAUDE, CODEX, CREAM, CURSOR, drawCheck, drawThinking, fill, H, look, matchCamera, OPENCODE, PI,
  stairs, stand, toScreen, W, withCamera, type Agent, type Camera, type Ctx,
} from "./cast";
import { HANDOFF_CAM, HANDOFF_CURSOR } from "./desk";

export const STAGE_START = 31;
export const STAGE_END = 58;

// ---------------------------------------------------------------------------
// Layout

const FLOOR = 1020;
/** The job the cursor drags out; the finished team fills exactly this circle. */
const GIANT = { x: 960, y: FLOOR - 440, r: 440 };
// The screen tears into a 3×3 grid of 640×360 scene cells.
const COLS = [320, 960, 1600];
const FLOORS = [300, 660, 1020];
// Splits conserve area, so daughters at one spot render exactly as their parent.
const R0 = 100, R1 = 80, R2 = 62;
const r1 = R0 / Math.sqrt(3), r2 = R1 / Math.sqrt(3);
const SAGE = THEMES.wuu.accent;
const GUTTER = "#E4E6E1";
const HELP_SPOT = 790;

const DEVIN: Agent = { engine: "devin", skin: agentSkin(AVATAR_HUES[10], "rounded-square"), theme: THEMES.wuu };
const GROK: Agent = { engine: "grok", skin: agentSkin(AVATAR_HUES[3], "round"), theme: THEMES.wuu };
const HERMES: Agent = { engine: "hermes", skin: agentSkin(AVATAR_HUES[9], "capsule"), theme: THEMES.wuu };

// ---------------------------------------------------------------------------
// Timeline

const SELECT = 32.3;
const GRAB = 33.05;
const STRETCHED = 34.6;
const IDEA = 37.7;
const SPLIT3 = 38.0;
const SPLIT3_END = 38.7;
const SPREAD = 38.72;
const TEAR_COLS = 39.2;
const SPLIT9 = 40.5;
const LAUNCH = 40.95;
const TEAR_ROWS = 41.25;
const LAND = 41.6;
const EVOLVE = 42.0;
const PUSH = 42.9;
const STUCK = 46.1;
const HELP = 47.35;
const HELP_TIME = 0.6;
const RETRACT = 50.6;
const FUSE = 51.0;
const FUSED = 52.0;
const DROP = 52.35;
const CHEER = 54.2;
const CLICK = 56.0;
const BOW = 60.4;

/** [start, end, taps, share of the cell reached by the end]. */
type Run = [number, number, number, number];
interface Worker { agent: Agent; c: number; row: number; lineup: number; runs: Run[] }
// Clockwise from the top, one window each around the original. The lineup
// keeps everyone clear of the giant's silhouette.
const TEAM: Worker[] = [
  { agent: CLAUDE, c: 1, row: 0, lineup: 560, runs: [[43.7, STUCK, 4, 0.45], [48.1, 49.3, 5, 1]] },
  { agent: CURSOR, c: 2, row: 0, lineup: 1510, runs: [[43.8, 48.7, 9, 1]] },
  { agent: PI, c: 2, row: 1, lineup: 1810, runs: [[43.6, 47.5, 7, 1]] },
  { agent: HERMES, c: 2, row: 2, lineup: 1660, runs: [[43.7, 49.5, 10, 1]] },
  { agent: GROK, c: 1, row: 2, lineup: 1360, runs: [[43.8, 48.4, 8, 1]] },
  { agent: DEVIN, c: 0, row: 2, lineup: 260, runs: [[43.9, 49.0, 9, 1]] },
  { agent: OPENCODE, c: 0, row: 1, lineup: 110, runs: [[43.7, 48.1, 8, 1]] },
  { agent: CODEX, c: 0, row: 0, lineup: 410, runs: [[43.6, 45.7, 6, 1]] },
];
const STUCK_ONE = 0;
const HELPER = 7;
/** Shelf-dwellers in the order they fall once the windows fuse. */
const UPPER = [0, 7, 1, 6, 2];
const LOWER = [3, 4, 5];
const CHEER_RANK = TEAM.map((w) => [...TEAM].sort((a, b) => a.lineup - b.lineup).indexOf(w));

const evolveAt = (i: number) => EVOLVE + 0.08 * i;
const done = (w: Worker) => w.runs[w.runs.length - 1][1];
const beats = ([start, end, n]: Run, offset = 0) => Array.from({ length: n }, (_, k) => start + ((end - start) * (k + offset)) / n);
// The helper taps between the stuck one's beats, so the shared window fills twice as fast.
const TAPS = TEAM.map((w, i) => [...w.runs.flatMap((run) => beats(run)), ...(i === HELPER ? beats(TEAM[STUCK_ONE].runs[1], 0.5) : [])]);
const dropStart = (i: number) => DROP + 0.05 * UPPER.indexOf(i);
const dropTime = (i: number) => 0.35 + (0.25 * (FLOOR - FLOORS[TEAM[i].row])) / 720;

// ---------------------------------------------------------------------------
// Camera and windows

const CAM_START = matchCamera(stand(WUU, 960, FLOOR, R0));
const CAM_MID: Camera = { x: 960, y: 830, zoom: 1.3 };
const CAM_WIDE: Camera = { x: 960, y: 540, zoom: 1 };
const mixCam = (a: Camera, b: Camera, k: number): Camera => ({ x: lerp(a.x, b.x, k), y: lerp(a.y, b.y, k), zoom: lerp(a.zoom, b.zoom, k) });

function camera(t: number): Camera {
  return mixCam(mixCam(CAM_START, CAM_MID, smooth(seg(t, 31.3, 32.2))), CAM_WIDE, inOut(seg(t, GRAB, 34.7)));
}

const toScene = (cam: Camera, x: number, y: number): [number, number] => [cam.x + (x - W / 2) / cam.zoom, cam.y + (y - H / 2) / cam.zoom];

const closing = (t: number) => inOut(seg(t, FUSE, FUSED));
const rowOpen = (t: number) => outCubic(seg(t, TEAR_ROWS, LAND)) * (1 - closing(t));

type Cell = [number, number];
type Span = [number, number];
interface Group { cells: Cell[]; x: number; y: number; w: number; h: number; ox: number; oy: number; radii: number[] }

function layout(t: number) {
  const col = outCubic(seg(t, TEAR_COLS, 39.65)) * (1 - closing(t));
  const row = rowOpen(t);
  const s = lerp(1, 0.9185, col);
  const gx = 20 * col, gy = 20 * row;
  return { col, row, s, gx, gy, x0: (W - W * s - 2 * gx) / 2, y0: (H - H * s - 2 * gy) / 2 };
}

/** Window groups on screen; neighbours merge back into one once their gutter closes. */
function groups(t: number): { s: number; list: Group[] } {
  const { col, row, s, gx, gy, x0, y0 } = layout(t);
  const split = (gap: number): Span[] => (gap < 0.5 ? [[0, 3]] : [[0, 1], [1, 2], [2, 3]]);
  const list: Group[] = [];
  for (const [ra, rb] of split(gy)) {
    for (const [a, b] of split(gx)) {
      const cells: Cell[] = [];
      for (let r = ra; r < rb; r++) for (let c = a; c < b; c++) cells.push([c, r]);
      // Outer edges open with the columns; inner row edges open with the rows.
      const top = 20 * Math.min(col, ra === 0 ? col : row);
      const bottom = 20 * Math.min(col, rb === 3 ? col : row);
      list.push({
        cells,
        x: x0 + a * (640 * s + gx), y: y0 + ra * (360 * s + gy),
        w: (b - a) * 640 * s + (b - a - 1) * gx, h: (rb - ra) * 360 * s + (rb - ra - 1) * gy,
        ox: x0 + a * gx, oy: y0 + ra * gy, radii: [top, top, bottom, bottom],
      });
    }
  }
  return { s, list };
}

const cellWorker = (c: number, row: number) => TEAM.findIndex((w) => w.c === c && w.row === row);
const isCentre = ([c, row]: Cell) => c === 1 && row === 1;

function windowOpen([c, row]: Cell, t: number) {
  const i = cellWorker(c, row);
  const at = i < 0 ? 41.9 : evolveAt(i);
  return smooth(seg(t, at, at + 0.35)) * (1 - smooth(seg(t, RETRACT, RETRACT + 0.5)));
}

function drawGroup(ctx: Ctx, t: number, g: Group, s: number, cam: Camera) {
  const single = g.cells.length === 1 ? g.cells[0] : undefined;
  const win = single ? windowOpen(single, t) : 0;
  const outline = () => { ctx.beginPath(); ctx.roundRect(g.x, g.y, g.w, g.h, g.radii); };
  ctx.save();
  ctx.fillStyle = mixColor(CREAM, "#FFFFFF", win);
  outline();
  if (win > 0) {
    ctx.save();
    ctx.globalAlpha *= win;
    ctx.shadowColor = "rgba(30, 35, 34, 0.045)";
    ctx.shadowBlur = 16;
    ctx.shadowOffsetY = 6;
    ctx.fill();
    ctx.restore();
  }
  ctx.fill();
  ctx.save();
  ctx.clip();
  ctx.save();
  ctx.translate(g.ox, g.oy);
  ctx.scale(s, s);
  withCamera(ctx, cam, () => drawScene(ctx, t, g.cells, s * cam.zoom));
  ctx.restore();
  if (single && win > 0) drawBar(ctx, t, g, single, win);
  ctx.restore();
  if (win > 0) {
    ctx.globalAlpha *= win;
    ctx.strokeStyle = THEMES.wuu.line;
    ctx.lineWidth = 1.5;
    outline();
    ctx.stroke();
  }
  ctx.restore();
}

function drawBar(ctx: Ctx, t: number, g: Group, [c, row]: Cell, open: number) {
  const i = cellWorker(c, row);
  const y = g.y - TITLE_BAR * (1 - open);
  const mid = y + TITLE_BAR / 2;
  ctx.fillStyle = THEMES.wuu.bar;
  ctx.fillRect(g.x, y, g.w, TITLE_BAR);
  ctx.fillStyle = THEMES.wuu.text;
  for (let k = 0; k < 3; k++) {
    ctx.beginPath();
    ctx.arc(g.x + 24 + k * 20, mid, 6.5, 0, Math.PI * 2);
    ctx.fill();
  }
  drawLogo(ctx, i < 0 ? "wuu" : TEAM[i].agent.engine, g.x + g.w / 2, mid, 22, INK);
  const finished = i < 0 ? 49.6 : done(TEAM[i]);
  drawCheck(ctx, g.x + g.w - 30, mid, 12, seg(t, finished, finished + 0.5));
}

// ---------------------------------------------------------------------------

export function stageShot(ctx: Ctx, t: number) {
  ctx.save();
  fill(ctx, GUTTER);
  const cam = camera(t);
  const { s, list } = groups(t);
  for (const g of list) drawGroup(ctx, t, g, s, cam);
  drawHelper(ctx, t);
  drawSelection(ctx, t);
  drawPointer(ctx, t);
  ctx.restore();
}

/** Everything inside one window group, in scene coordinates. */
function drawScene(ctx: Ctx, t: number, cells: Cell[], scale: number) {
  const has = (c: number, row: number) => cells.some(([a, b]) => a === c && b === row);
  drawJob(ctx, t, cells, scale);
  if (t < SPLIT3) {
    drawBall(ctx, hero(t));
    drawThinking(ctx, 960 + R0 * 0.75, FLOOR - R0 * 2.25, 1, seg(t, 37.0, 37.45) * (1 - seg(t, 37.65, 37.75)));
  } else if (t < SPLIT3_END) {
    const { blobs, faces } = split3(t);
    drawBlobs(ctx, blobs, faces, blobs);
  } else if (t < SPLIT9) {
    trio(t).forEach((b, c) => { if (has(c, 2)) drawBall(ctx, b); });
  } else if (t < TEAR_ROWS) {
    for (let c = 0; c < 3; c++) {
      if (!has(c, 2)) continue;
      const { blobs, faces } = column(t, c);
      drawShadow(ctx, COLS[c], FLOOR, lerp(R1, r2, inOut(seg(t, SPLIT9, LAUNCH))), 0);
      drawBlobs(ctx, blobs, faces);
    }
  } else {
    if (has(1, 1)) drawBall(ctx, giant(t));
    TEAM.forEach((w, i) => {
      if (i === HELPER && t >= HELP && t < HELP + HELP_TIME) return;
      const [c, row] = i === HELPER && t >= HELP ? [1, 0] : [w.c, w.row];
      if (has(c, row)) drawBall(ctx, worker(i, t));
    });
    if (has(1, 0)) {
      const b = worker(STUCK_ONE, t);
      const pop = outBack(seg(t, STUCK + 0.15, STUCK + 0.45)) * (1 - smooth(seg(t, 47.85, 48.05)));
      drawBubble(ctx, b.x + 70, b.y - 60, 1.1 * pop, t);
    }
  }
}

// ---------------------------------------------------------------------------
// The job: a dashed outline that the team fills in, window by window

/** How many times Wuu's size the dragged-out job is. */
const stretch = (t: number) => lerp(1, GIANT.r / R0, inOut(seg(t, GRAB, STRETCHED)));

function progress(w: Worker, t: number) {
  let p = 0;
  for (const [start, end, n, reached] of w.runs) p = lerp(p, reached, stairs(seg(t, start, end), n));
  return p;
}

/** Distance from the job's centre to the nearest point of a cell. */
function nearest(c: number, row: number) {
  const dx = Math.max(640 * c - GIANT.x, 0, GIANT.x - 640 * (c + 1));
  const dy = Math.max(360 * row - GIANT.y, 0, GIANT.y - 360 * (row + 1));
  return Math.hypot(dx, dy);
}

/** Each outer window grows the job's disc from its nearest point out to the outline. */
const fillRadius = (i: number, t: number) => lerp(nearest(TEAM[i].c, TEAM[i].row), GIANT.r, progress(TEAM[i], t));

function drawJob(ctx: Ctx, t: number, cells: Cell[], scale: number) {
  const outer = cells.filter((cell) => !isCentre(cell));
  const reach = (outer.length ? outer : TEAM.map((w): Cell => [w.c, w.row])).map(([c, row]) => fillRadius(cellWorker(c, row), t));
  const k = stretch(t);
  const ghost = seg(k, 1.05, 1.3) * (1 - seg(Math.min(...reach), GIANT.r - 10, GIANT.r));
  if (ghost > 0) {
    ctx.save();
    ctx.globalAlpha *= ghost;
    ctx.beginPath();
    ctx.arc(GIANT.x, FLOOR - R0 * k, R0 * k, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(144, 164, 157, 0.08)";
    ctx.fill();
    ctx.strokeStyle = SAGE;
    ctx.lineWidth = 4 / scale;
    ctx.setLineDash([14 / scale, 12 / scale]);
    ctx.stroke();
    ctx.restore();
  }
  // The finished job's floor shadow arrives with the fusion. The giant draws
  // its own once it shares a window with the floor.
  if (closing(t) > 0 && !cells.some(isCentre) && cells.some(([, row]) => row === 2)) {
    ctx.save();
    ctx.globalAlpha *= closing(t);
    drawShadow(ctx, GIANT.x, FLOOR, GIANT.r, 0);
    ctx.restore();
  }
  if (outer.length !== 1 || cells.length !== 1) return;
  const [c, row] = outer[0];
  if (reach[0] <= nearest(c, row)) return;
  ctx.fillStyle = WUU.body;
  ctx.beginPath();
  ctx.arc(GIANT.x, GIANT.y, reach[0], 0, Math.PI * 2);
  ctx.fill();
}

// ---------------------------------------------------------------------------
// Shot 6: one Wuu against the job

// One expression track follows the original through every split.
const HERO_EYES: [number, EyeKind][] = [
  [31, "open"], [33.35, "wide"], [34.9, "open"], [35.15, "squeeze"], [36.75, "flat"], [37.25, "open"],
  [37.72, "wide"], [37.97, "squeeze"], [38.6, "open"], [40.05, "happy"], [40.45, "squeeze"], [LAUNCH, "wide"], [41.75, "open"],
];
const HERO_BLINKS = [31.9, 34.1, 37.35];

type Face = { yaw: number; pitch: number };
const FORWARD: Face = { yaw: 0, pitch: 0.05 };
const mixFace = (a: Face, b: Face, k: number): Face => ({ yaw: lerp(a.yaw, b.yaw, k), pitch: lerp(a.pitch, b.pitch, k) });
type Aim = Face | ((t: number) => Face);

/** Gaze track: [time, target], each reached with a quick saccade. */
function gaze(t: number, track: [number, Aim][], duration = 0.2): Face {
  const value = (a: Aim) => (typeof a === "function" ? a(t) : a);
  let i = 0;
  while (i + 1 < track.length && t >= track[i + 1][0]) i++;
  const k = i === 0 ? 1 : smooth(seg(t, track[i][0], track[i][0] + duration));
  return k >= 1 ? value(track[i][1]) : mixFace(value(track[i - 1][1]), value(track[i][1]), k);
}

function hero(t: number): Ball {
  // Three hard puffs, each gaining less, then all the air goes out.
  const r = keys(t, [
    [35.2, R0], [35.45, 124, outBack], [35.7, 126], [35.95, 146, outBack], [36.2, 148], [36.45, 166, outBack], [36.75, 168], [37.2, R0, outElastic],
  ]);
  const strain = seg(r, R0, 168);
  const pop = squash(t, SELECT, 0.12, 0.4), idea = squash(t, IDEA, 0.14, 0.3);
  const b = stand(WUU, 960 + wobble(t, 3, 30) * 2.5 * strain, FLOOR, r, { lift: 0, sx: pop.sx * idea.sx, sy: pop.sy * idea.sy });
  return {
    ...b, ...heroAim(t, b), tilt: wobble(t, 5, 24) * 1.5 * strain,
    eyes: eyes(t, HERO_EYES, HERO_BLINKS),
    sweat: seg(t, 36.8, 37.1) * (1 - seg(t, 37.4, 37.6)),
    marks: seg(t, IDEA, 37.85) * (1 - seg(t, 37.9, SPLIT3)), markAngle: ICON_POSE.markAngle,
  };
}

function heroAim(t: number, b: Ball): Face {
  const rest = { yaw: -0.1, pitch: 0.05 };
  const atCursor = (at: number) => {
    const p = pointer(at)!;
    return look(b.x, b.y, ...toScene(camera(at), p.x, p.y), 1.1);
  };
  if (t < 31.3) return rest;
  if (t < 34.75) return mixFace(rest, atCursor(t), smooth(seg(t, 31.3, 31.7)));
  // Up at the job, down while puffing, deflated, then up at the thought.
  const from = atCursor(34.75);
  return {
    yaw: keys(t, [[34.75, from.yaw], [35.0, 0], [36.95, 0], [37.25, 0.3], [37.6, 0.3], [37.75, 0]]),
    pitch: keys(t, [
      [34.75, from.pitch], [35.0, 0.45], [35.1, 0.45], [35.3, 0.05], [36.75, 0.05], [36.95, -0.1], [37.2, -0.1],
      [37.4, 0.35], [37.6, 0.35], [37.75, 0.05],
    ]),
  };
}

// The selection box sits a few pixels outside the body; the cursor holds its corner handle.
const BOX_PAD = 6;

function selection(t: number) {
  const cam = camera(t);
  const k = stretch(t);
  const [ax, ay] = toScreen(cam, 960 - R0 * k, FLOOR - 2 * R0 * k);
  const [bx, by] = toScreen(cam, 960 + R0 * k, FLOOR);
  return { ax: ax - BOX_PAD, ay: ay - BOX_PAD, bx: bx + BOX_PAD, by: by + BOX_PAD };
}

function drawSelection(ctx: Ctx, t: number) {
  const appear = seg(t, SELECT, SELECT + 0.25);
  const alpha = appear * (1 - seg(t, 34.7, 35.0));
  if (alpha <= 0) return;
  const { ax, ay, bx, by } = selection(t);
  const pop = lerp(0.9, 1, outBack(appear));
  const cx = (ax + bx) / 2, cy = (ay + by) / 2;
  const hw = ((bx - ax) / 2) * pop, hh = ((by - ay) / 2) * pop;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.strokeStyle = SAGE;
  ctx.lineWidth = 2.5;
  ctx.strokeRect(cx - hw, cy - hh, hw * 2, hh * 2);
  ctx.fillStyle = "#FFFFFF";
  for (const [x, y] of [[cx - hw, cy - hh], [cx + hw, cy - hh], [cx + hw, cy + hh], [cx - hw, cy + hh]]) {
    ctx.beginPath();
    ctx.rect(x - 7, y - 7, 14, 14);
    ctx.fill();
    ctx.stroke();
  }
  ctx.restore();
}

// ---------------------------------------------------------------------------
// The user's cursor, in screen space

interface Pointer { x: number; y: number; scale: number; press: number; rot: number }
const HANDOFF = toScreen(HANDOFF_CAM, ...HANDOFF_CURSOR);
const HANDOFF_SCALE = 1.9 * HANDOFF_CAM.zoom;
const PARK: [number, number] = [1270, 330];

function travel(t: number, t0: number, t1: number, from: [number, number], to: [number, number], ease = inOut): [number, number] {
  const e = ease(seg(t, t0, t1));
  const bend = Math.sin(e * Math.PI) * Math.min(60, Math.hypot(to[0] - from[0], to[1] - from[1]) * 0.12);
  return [lerp(from[0], to[0], e) + bend * 0.3, lerp(from[1], to[1], e) - bend];
}

function pointer(t: number): Pointer | undefined {
  const click = (at: number) => Math.max(0, 1 - Math.abs(t - at) / 0.09);
  const at = (x: number, y: number, o: Partial<Pointer> = {}): Pointer => ({ x, y, scale: 1.9, press: 0, rot: 0, ...o });
  const handle = (): [number, number] => { const box = selection(t); return [box.ax, box.ay]; };
  if (t < 31.3) return at(...HANDOFF, { scale: HANDOFF_SCALE });
  const body = toScreen(camera(t), 905, 955);
  if (t < 32.2) return at(...travel(t, 31.3, 32.2, HANDOFF, body), { scale: lerp(HANDOFF_SCALE, 1.9, smooth(seg(t, 31.3, 32.2))) });
  if (t < 32.45) return at(...body, { press: click(SELECT) });
  if (t < GRAB) return at(...travel(t, 32.45, 32.95, body, handle()), { press: smooth(seg(t, 32.96, GRAB)) });
  if (t < 34.75) return at(...handle(), { press: 1 - smooth(seg(t, STRETCHED, 34.7)) });
  if (t < 35.3) {
    const box = selection(34.75);
    return at(...travel(t, 34.75, 35.3, [box.ax, box.ay], [200, -120], inCubic));
  }
  if (t < 55.2 || t > 57.5) return undefined;
  if (t < 55.95) return at(...travel(t, 55.2, 55.95, [2010, 1130], PARK));
  if (t < 56.85) {
    const wiggle = t > 56.15 ? Math.sin((t - 56.15) * 10) * 10 * (1 - seg(t, 56.35, 56.75)) : 0;
    return at(...PARK, { press: click(CLICK), rot: wiggle });
  }
  return at(...travel(t, 56.85, 57.5, PARK, [2050, 200], inCubic));
}

function drawPointer(ctx: Ctx, t: number) {
  for (const at of [SELECT, CLICK]) {
    const p = pointer(at)!;
    drawRipple(ctx, p.x, p.y, seg(t, at, at + 0.4));
  }
  const p = pointer(t);
  if (p) drawCursor(ctx, p.x, p.y, p);
}

// ---------------------------------------------------------------------------
// Shot 7: splitting

interface Blob { x: number; y: number; r: number }

/**
 * Marching-squares outline of soft circles. Blobs more than 3r apart come out
 * as exact circles, and every piece is clockwise and filled once, so shared
 * cell edges never show a seam.
 */
function blobPath(blobs: Blob[], step = 3): Path2D {
  const field = (x: number, y: number) => {
    let v = -1;
    for (const b of blobs) {
      const d2 = (x - b.x) ** 2 + (y - b.y) ** 2;
      const q = Math.sqrt(d2) / (3 * b.r);
      if (q < 1) v += Math.min(50, (b.r * b.r) / d2) * (1 - smooth(seg(q, 0.6, 1)));
    }
    return v;
  };
  const x0 = Math.floor(Math.min(...blobs.map((b) => b.x - 3 * b.r)) / step) * step;
  const y0 = Math.floor(Math.min(...blobs.map((b) => b.y - 3 * b.r)) / step) * step;
  const nx = Math.ceil((Math.max(...blobs.map((b) => b.x + 3 * b.r)) - x0) / step);
  const ny = Math.ceil((Math.max(...blobs.map((b) => b.y + 3 * b.r)) - y0) / step);
  const v = new Float64Array((nx + 1) * (ny + 1));
  for (let j = 0; j <= ny; j++) for (let i = 0; i <= nx; i++) v[j * (nx + 1) + i] = field(x0 + i * step, y0 + j * step);
  const path = new Path2D();
  for (let j = 0; j < ny; j++) {
    let run = -1;
    for (let i = 0; i <= nx; i++) {
      const at = j * (nx + 1) + i;
      // The extra column past the grid closes any open run.
      const corners = i < nx ? [v[at], v[at + 1], v[at + nx + 2], v[at + nx + 1]] : [-1, -1, -1, -1];
      if (corners.every((value) => value >= 0)) {
        if (run < 0) run = i;
        continue;
      }
      // Fully covered cells merge into one rectangle per row.
      if (run >= 0) {
        path.rect(x0 + run * step, y0 + j * step, (i - run) * step, step);
        run = -1;
      }
      if (corners.every((value) => value < 0)) continue;
      const x = x0 + i * step, y = y0 + j * step;
      const points: [number, number][] = [[x, y], [x + step, y], [x + step, y + step], [x, y + step]];
      const outline: [number, number][] = [];
      for (let k = 0; k < 4; k++) {
        const a = corners[k], b = corners[(k + 1) % 4];
        const [ax, ay] = points[k], [bx, by] = points[(k + 1) % 4];
        if (a >= 0) outline.push([ax, ay]);
        if (a >= 0 !== b >= 0) {
          const s = a / (a - b);
          outline.push([ax + (bx - ax) * s, ay + (by - ay) * s]);
        }
      }
      path.moveTo(...outline[0]);
      for (const point of outline.slice(1)) path.lineTo(...point);
      path.closePath();
    }
  }
  return path;
}

function drawBlobs(ctx: Ctx, blobs: Blob[], faces: Ball[], shadow?: Blob[]) {
  if (shadow) {
    // The same field squashed onto the floor, so the contact shadow splits too.
    ctx.save();
    ctx.translate(0, FLOOR);
    ctx.scale(1, 0.16 / 0.88);
    ctx.fillStyle = "rgba(35, 38, 37, 0.1)";
    ctx.fill(blobPath(shadow.map((b) => ({ x: b.x, y: 0, r: b.r * 0.88 }))));
    ctx.restore();
  }
  ctx.fillStyle = WUU.body;
  ctx.fill(blobPath(blobs));
  for (const face of faces) drawBall(ctx, { ...face, skin: WUU, bodyAlpha: 0 });
}

/** Newborn eyes: barely open. */
const SLIT = { kind: "open" as const, open: 0.08 };

function split3(t: number) {
  const u = inOut(seg(t, SPLIT3, SPLIT3_END));
  const d = 240 * u;
  const y = lerp(FLOOR - R0, FLOOR - r1, u);
  const blobs = [-1, 0, 1].map((side) => ({ x: 960 + side * d, y, r: r1 }));
  const lids = eyes(t, HERO_EYES);
  const reveal = smooth(seg(d, 2.9 * r1, 3.4 * r1));
  const faces: Ball[] = [
    { x: 960, y, r: lerp(R0, r1, u), ...FORWARD, eyes: t < 38.6 ? lids : { ...lids, open: Math.min(lids.open, SLIT.open) } },
    ...[-1, 1].map((side) => ({ x: 960 + side * d, y, r: r1, ...FORWARD, eyes: SLIT, alpha: reveal })),
  ];
  return { blobs, faces };
}

function trio(t: number): Ball[] {
  const r = lerp(r1, R1, inOut(seg(t, 39.6, 40.1)));
  const h = hop(t, SPREAD, 0.55);
  const slide = smooth(seg(seg(t, SPREAD, SPREAD + 0.55), 0.28, 0.8));
  const snap = squash(t, SPLIT3_END, 0.12, 0.4);
  const wake = lerp(SLIT.open, 1, smooth(seg(t, 39.35, 39.65)));
  const lids = eyes(t, HERO_EYES);
  return [-1, 0, 1].map((side, c) => {
    const x = side === 0 ? 960 : lerp(960 + side * 240, COLS[c], slide);
    const b = stand(WUU, x, FLOOR, r, side === 0 ? { lift: 0, ...snap } : h, 110);
    // Three of me? Each glances across the new gutters at the others.
    const yaw = side === 0
      ? keys(t, [[39.55, 0], [39.7, -0.5], [39.85, -0.5], [39.98, 0.5], [40.1, 0.5], [40.3, 0]])
      : keys(t, [[39.5, 0], [39.65, -side * 0.55], [40.1, -side * 0.55], [40.3, 0]]);
    return { ...b, yaw, pitch: FORWARD.pitch, eyes: { ...lids, open: lids.kind === "open" ? Math.min(lids.open, wake) : lids.open } };
  });
}

/** Height of the bead that ends up in `row` while a column splits and launches. */
function beadY(t: number, row: number) {
  const u = inOut(seg(t, SPLIT9, LAUNCH));
  const k = 2 - row;
  const rest = lerp(FLOOR - R1, FLOOR - r2, u) - 150 * k * u;
  if (k === 0 || t <= LAUNCH) return rest;
  return keys(t, [[LAUNCH, rest], [TEAR_ROWS, k === 1 ? 510 : 150, outCubic], [LAND, FLOORS[row] - r2, inCubic]]);
}

function column(t: number, c: number) {
  const u = inOut(seg(t, SPLIT9, LAUNCH));
  const x = COLS[c];
  const blobs = [2, 1, 0].map((row) => ({ x, y: beadY(t, row), r: r2 }));
  const faces: Ball[] = [
    { x, y: beadY(t, 1), r: lerp(R1, r2, u), ...FORWARD, eyes: eyes(t, HERO_EYES) },
    ...[0, 2].map((row) => ({ x, y: beadY(t, row), r: r2, ...FORWARD, eyes: SLIT, alpha: smooth(seg(u, 0.9, 1)) })),
  ];
  return { blobs, faces };
}

// ---------------------------------------------------------------------------
// Shots 7–9: the coordinator and its crew

/** Where the coordinator's face points to watch a window, kept clear of its own window's edges. */
const cellAim = (c: number, row: number, k = 1): Face => ({ yaw: (c - 1) * 0.34 * k, pitch: [0.12, 0.02, -0.06][row] * k });

/** Watching the evolution run clockwise around its window. */
function ring(t: number): Face {
  const at = clamp((t - EVOLVE - 0.1) / 0.08, 0, 7);
  const a = Math.floor(at), b = Math.min(7, a + 1);
  return mixFace(cellAim(TEAM[a].c, TEAM[a].row, 1.5), cellAim(TEAM[b].c, TEAM[b].row, 1.5), at - a);
}

const COORD_GAZE: [number, Aim][] = [
  [0, FORWARD], [EVOLVE, ring], [42.75, FORWARD],
  [43.7, cellAim(0, 0)], [44.3, cellAim(2, 1)], [44.9, cellAim(1, 2)], [45.4, cellAim(0, 0)], [45.9, cellAim(2, 0)],
  [46.45, cellAim(1, 0)], [46.95, cellAim(0, 0)],
  [47.4, (t) => mixFace(cellAim(0, 0), cellAim(1, 0), smooth(seg(t, 47.45, HELP + HELP_TIME)))],
  [48.3, cellAim(2, 2)], [48.8, cellAim(0, 2)], [49.3, cellAim(1, 0)], [49.75, FORWARD],
  // After the fusion: watching the crew fall left, hop right, and line up.
  [52.3, { yaw: -0.35, pitch: -0.3 }], [52.9, { yaw: 0.35, pitch: -0.3 }], [53.4, { yaw: 0, pitch: -0.15 }],
  [55.8, look(GIANT.x, GIANT.y, ...PARK)],
];
const COORD_EYES: [number, EyeKind][] = [
  ...HERO_EYES, [PUSH, "squeeze"], [PUSH + 0.65, "open"], [49.9, "happy"], [51.3, "open"], [54.1, "happy"], [55.6, "open"],
];
const COORD_BLINKS = [44.3, 45.4, 46.45, 47.4, 48.8, 52.2, 53.4, 61.8];

/** The original: a bead, then the face that fills the middle window, then the finished job. */
export function giant(t: number): Ball {
  const small = lerp(r2, R2, inOut(seg(t, 41.7, 42.1)));
  const push = inOut(seg(t, PUSH, PUSH + 0.7));
  const r = lerp(small, GIANT.r, push);
  const y = t < LAND ? beadY(t, 1) : lerp(FLOORS[1] - small, GIANT.y, push);
  const face = gaze(t, COORD_GAZE, 0.18);
  const pose = outBack(seg(t, CLICK, CLICK + 0.5));
  const land = squash(t, LAND, 0.2, 0.45), click = squash(t, CLICK, 0.06, 0.5);
  // The shelf shadow fades as the body outgrows it; the floor shadow returns with the fusion.
  const onShelf = t < PUSH + 0.2;
  return {
    x: GIANT.x, y, r, sx: land.sx * click.sx, sy: land.sy * click.sy, skin: WUU,
    ground: onShelf ? FLOORS[1] : FLOOR,
    shadow: onShelf ? rowOpen(t) * (1 - smooth(seg(t, PUSH, PUSH + 0.2))) : closing(t),
    yaw: lerp(face.yaw, ICON_POSE.yaw, pose), pitch: lerp(face.pitch, ICON_POSE.pitch, pose), roll: ICON_POSE.roll * pose,
    eyes: eyes(t, COORD_EYES, COORD_BLINKS),
    marks: outCubic(seg(t, CLICK + 0.05, CLICK + 0.6)), markAngle: ICON_POSE.markAngle,
  };
}

/** WUU's charcoal shifts into the harness colours while the eyes are shut; the shape swaps mid-blink. */
function skinAt(w: Worker, e: number, t: number): Skin {
  const k = smooth(seg(t, e + 0.04, e + 0.2));
  if (k <= 0) return WUU;
  if (k >= 1) return w.agent.skin;
  const to = w.agent.skin;
  return {
    body: mixColor(WUU.body, to.body, k), lo: mixColor(WUU.lo, to.lo, k), eye: mixColor(WUU.eye, to.eye, k),
    shape: t < e + 0.12 ? WUU.shape : to.shape,
  };
}

const EYE_TRACKS = TEAM.map((w, i) => {
  const e = evolveAt(i);
  const track: [number, EyeKind][] = [
    ...(w.row === 1 ? HERO_EYES : [[0, "open"] as [number, EyeKind]]),
    [e + 0.12, "open"], [e + 0.25, "happy"], [e + 1.0, "open"], [done(w), "happy"],
  ];
  if (i === STUCK_ONE) track.push([STUCK, "flat"], [48.0, "happy"], [48.3, "open"]);
  if (i === HELPER) track.push([46.5, "open"]);
  if (UPPER.includes(i)) track.push([52.1, "wide"], [dropStart(i) + dropTime(i) + 0.15, "open"]);
  else track.push([52.3, "open"]);
  track.push([CHEER + 0.07 * CHEER_RANK[i] - 0.05, "happy"], [55.5, "open"]);
  return track.sort((a, b) => a[0] - b[0]);
});

/** A damped wiggle for antennas after a tap. */
function kick(t: number, at: number, duration: number) {
  const p = (t - at) / duration;
  return p > 0 && p < 1 ? Math.sin(p * Math.PI * 3) * Math.exp(-4 * p) : 0;
}

function worker(i: number, t: number): Ball {
  const w = TEAM[i];
  const e = evolveAt(i);
  const skin = skinAt(w, e, t);
  const r = lerp(r2, R2, inOut(seg(t, 41.7, 42.1)));
  const shelf = w.row < 2;
  const home = i === HELPER && t >= HELP ? HELP_SPOT : COLS[w.c];
  const floor = FLOORS[w.row];
  const rank = CHEER_RANK[i];
  const pulses = [squash(t, e, 0.22, 0.5), ...TAPS[i].map((at) => squash(t, at, 0.1, 0.3))];
  let x = home, ground = floor, shadow = shelf ? rowOpen(t) : 1, fall = 0, height = r * 1.4;
  let h = hops(t, [CHEER + 0.07 * rank, BOW + 0.05 * rank], 0.5);
  if (shelf) {
    // The shelves vanish with the windows: hang, look down, drop into the lineup.
    const start = dropStart(i), end = start + dropTime(i);
    fall = seg(t, start, end);
    x = lerp(home, w.lineup, fall);
    if (fall > 0) { ground = FLOOR; shadow = fall; }
    pulses.push(squash(t, LAND, 0.2, 0.45), squash(t, end, 0.25, 0.45));
  } else {
    const start = 52.5 + 0.06 * LOWER.indexOf(i);
    x = lerp(home, w.lineup, smooth(seg(seg(t, start, start + 0.5), 0.28, 0.8)));
    if (t < start + 0.5) { h = hop(t, start, 0.5); height = 90; }
  }
  const sq = pulses.reduce((a, p) => ({ sx: a.sx * p.sx, sy: a.sy * p.sy }), { sx: h.sx, sy: h.sy });
  const b: Ball = { ...stand(skin, x, ground, r, { lift: h.lift, ...sq }, height), shadow };
  if (fall > 0 && fall < 1) b.y = lerp(floor, FLOOR, fall * fall) - r * extent(skin).ry;
  else if (shelf && t < LAND) b.y = beadY(t, w.row);
  const lids = eyes(t, EYE_TRACKS[i], [61.8 + 0.04 * rank]);
  // Newborns open their eyes a beat after landing in their own window.
  if (w.row !== 1 && lids.kind === "open") lids.open = Math.min(lids.open, lerp(SLIT.open, 1, smooth(seg(t, 41.75, 42.05))));
  const evolved = t >= e + 0.12;
  return {
    ...b, ...workerAim(i, t, b),
    eyes: lids,
    engine: evolved ? w.agent.engine : undefined, gear: evolved ? w.agent.gear : undefined,
    antenna: outBack(seg(t, e + 0.12, e + 0.5)),
    sway: TAPS[i].reduce((sum, at) => sum + kick(t, at, 0.5) * 0.3, 0) + kick(t, e + 0.2, 0.9) * 0.5
      + (shelf ? kick(t, dropStart(i) + dropTime(i), 0.6) * 0.5 : 0),
    sweat: i === STUCK_ONE ? seg(t, 46.4, 46.7) * (1 - seg(t, 47.9, 48.1)) : 0,
  };
}

function workerAim(i: number, t: number, b: Ball): Face {
  const w = TEAM[i];
  const e = evolveAt(i);
  const toward = (x: number, y: number): Aim => () => look(b.x, b.y, x, y);
  // Watch the edge of the fill; the soft norm keeps the gaze steady as the
  // edge rises past the worker.
  const fillEdge = (cell: number): Aim => () => {
    const reach = fillRadius(cell, t);
    const dx = b.x - GIANT.x, dy = b.y - GIANT.y, d = Math.hypot(dx, dy);
    const ex = GIANT.x + (dx / d) * reach - b.x, ey = GIANT.y + (dy / d) * reach - b.y;
    const n = Math.max(Math.hypot(ex, ey), 160);
    return { yaw: (ex / n) * 0.5, pitch: (-ey / n) * 0.4 + 0.05 };
  };
  const centre = toward(GIANT.x, GIANT.y);
  const track: [number, Aim][] = [
    [0, FORWARD], [e + 0.4, { yaw: 0, pitch: 0.42 }], [e + 0.95, FORWARD], [w.runs[0][0] - 0.25, fillEdge(i)], [done(w), centre],
  ];
  if (i === STUCK_ONE) track.push([47.75, toward(HELP_SPOT - 100, b.y)], [48.1, fillEdge(i)]);
  if (i === HELPER) {
    // A nod to the coordinator, a hop over, and a hand with the stuck window.
    track.push(
      [47.05, { yaw: 0.5, pitch: -0.45 }], [47.2, centre], [HELP - 0.02, { yaw: 0.55, pitch: 0.1 }],
      [HELP + HELP_TIME + 0.05, fillEdge(STUCK_ONE)], [done(TEAM[STUCK_ONE]), centre],
    );
  }
  if (UPPER.includes(i)) track.push([52.1, { yaw: 0, pitch: -0.45 }]);
  else track.push([52.15, toward(GIANT.x, 240)]);
  track.push([53.1, toward(GIANT.x, 360)], [58.5, toward(1080, 620)]);
  return gaze(t, track.sort((a, c) => a[0] - c[0]));
}

/** The helper's hop crosses a gutter, so it is drawn over the windows in screen space. */
function drawHelper(ctx: Ctx, t: number) {
  if (t < HELP || t >= HELP + HELP_TIME) return;
  const { s, gx, x0, y0 } = layout(t);
  const from = x0 + s * COLS[0], to = x0 + gx + s * HELP_SPOT;
  const b = worker(HELPER, t);
  const x = lerp(from, to, smooth(seg(seg(t, HELP, HELP + HELP_TIME), 0.28, 0.8)));
  drawBall(ctx, { ...b, ...stand(b.skin!, x, y0 + s * FLOORS[0], b.r * s, hop(t, HELP, HELP_TIME), 100 * s) });
}

/** The eight harnesses where the film leaves them, for the end card. */
export function drawCrew(ctx: Ctx, t: number) {
  TEAM.forEach((_, i) => drawBall(ctx, worker(i, t)));
}
