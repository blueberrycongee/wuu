// Shots 2–5, one continuous desk world (5 s – 31 s):
//   2. Four harnesses in four windows keep calling; the cursor wears itself out.
//   3. Close on Wuu: an idea. Wuu leaps out of frame.
//   4. Wuu lands, unfolds one app, and every window hops into its sidebar.
//   5. Inside that app: switch sessions, pick another engine, all run at once.
import {
  drawBall, drawBubble, drawBurst, drawCursor, drawLines, drawPuff, drawRipple, drawSparkle, drawSweat,
  drawToken, drawWindow, extent, ICON_POSE, INK, TITLE_BAR, THEMES, WUU, type Ball,
} from "./art";
import {
  clamp, eyes, hop, hops, inCubic, inOut, keys, lerp, linear, outBack, outCubic, seg, smooth, squash, wobble,
  type EyeKind,
} from "./motion";
import {
  agent, arc, CLAUDE, CODEX, CONFETTI, CORAL, CREAM, CURSOR, dotGrid, drawDaze, drawSpinner, drawThinking, fill,
  irisClose, look, MINT, OPENCODE, PI, stand, SUN, toScreen, W, H, withCamera, type Agent, type Camera, type Ctx,
} from "./cast";

export const DESK_START = 5;
export const DESK_END = 31;

// ---------------------------------------------------------------------------
// Layout

interface DeskWindow { agent: Agent; x: number; y: number; w: number; h: number; tilt: number; lines: number[] }
const DESK: DeskWindow[] = [
  { agent: CLAUDE, x: 110, y: 96, w: 610, h: 390, tilt: -2.4, lines: [250, 180, 290, 140, 230] },
  { agent: CODEX, x: 820, y: 58, w: 560, h: 380, tilt: 1.8, lines: [210, 270, 150, 240, 120] },
  { agent: CURSOR, x: 230, y: 560, w: 570, h: 370, tilt: 1.4, lines: [230, 160, 260, 190, 110] },
  { agent: OPENCODE, x: 950, y: 500, w: 590, h: 390, tilt: -1.8, lines: [260, 200, 170, 280, 130] },
];
const CREATURE = { x: 118, r: 64 };

/** Wuu's usual spot, watching from the corner of the desk. */
const CORNER = { x: 1745, floor: 1012, r: 118 };
const MIDDLE = { x: 960, floor: 880 };

const APP = { x: 250, y: 100, w: 1390, h: 850 };
const SIDEBAR = 332;
const APP_BODY = APP.h - TITLE_BAR;
const COMPOSER = { x: SIDEBAR + 34, y: APP_BODY - 128, w: APP.w - SIDEBAR - 68, h: 96 };
const PILL = { x: COMPOSER.x + 22, y: COMPOSER.y + 24, w: 118, h: 48 };
const SEND = { x: COMPOSER.x + COMPOSER.w - 50, y: COMPOSER.y + COMPOSER.h / 2 };
const PLUS = { x: 18, y: 18, w: 52, h: 52 };
const rowRect = (i: number) => ({ x: 14, y: 90 + i * 82, w: SIDEBAR - 28, h: 70 });
/** App-local coordinates (below the title bar) to world coordinates. */
const app = (x: number, y: number): [number, number] => [APP.x + x, APP.y + TITLE_BAR + y];

const SESSIONS: Agent[] = [CLAUDE, CODEX, CURSOR, OPENCODE, PI];
const MENU = ["wuu", "codex", "claude", "cursor", "devin", "grok", "hermes", "pi", "opencode", "antigravity"];
const MENU_BOX = { x: PILL.x - 6, y: COMPOSER.y - 214, w: 470, h: 196 };
const menuSlot = (i: number): [number, number] => [MENU_BOX.x + 58 + (i % 5) * 88, MENU_BOX.y + 56 + Math.floor(i / 5) * 86];

// ---------------------------------------------------------------------------
// Timeline

// Shot 2: the cursor answers windows faster and faster until it cannot keep up.
const CLICKS: [number, number][] = [
  [5.9, 0], [6.75, 1], [7.5, 3], [8.15, 2], [8.7, 0], [9.15, 1], [9.55, 3], [9.9, 2], [10.2, 0], [10.45, 1], [10.68, 3],
];
const LANDING = 15.85;
const REQUESTS = [
  ...CLICKS.map(([at, win]) => ({ win, ask: Math.max(5.2, at - 0.85), answer: at })),
  ...[2, 0, 3, 1].map((win, i) => ({ win, ask: 10.95 + i * 0.08, answer: LANDING })),
];
const WAKE = 20.8;

// Shot 4: windows fly into the sidebar one by one.
const COLLECT = [18.35, 18.95, 19.55, 20.15];
const COLLECT_TIME = 0.78;

// Shots 4–5: the rested cursor drives the unified app.
const SELECT: [number, number][] = [[21.9, 0], [23.6, 1], [24.3, 2], [25.0, 3], [25.7, 4]];
const PILL_CLICK = 26.25;
const PICK = 27.15;
const SEND_CLICK = 27.6;

// ---------------------------------------------------------------------------

export function deskShot(ctx: Ctx, t: number) {
  const cam = camera(t);
  fill(ctx, CREAM);
  withCamera(ctx, cam, () => {
    dotGrid(ctx, cam.x - W / cam.zoom, cam.y - H / cam.zoom, cam.x + W / cam.zoom, cam.y + H / cam.zoom);
    drawApp(ctx, t);
    drawDeskWindows(ctx, t);
    drawTravellers(ctx, t);
    drawWuu(ctx, t);
    drawTheCursor(ctx, t);
  });
  // Shot 5 closes on Wuu, and shot 6 opens from the same spot.
  if (t > 30.2) {
    const [sx, sy] = toScreen(cam, CORNER.x, CORNER.floor - CORNER.r);
    const hold = CORNER.r * cam.zoom * 1.45;
    irisClose(ctx, sx, sy, keys(t, [[30.2, 1500], [30.62, hold, outCubic], [30.78, hold], [31, 0, inCubic]]));
  }
}

function camera(t: number): Camera {
  const drift = t < 12.3 ? wobble(t, 3, 0.5) * 6 : 0;
  const wide = { x: 960 + drift, y: 540, zoom: 1 };
  const close = { x: CORNER.x - 40, y: CORNER.floor - CORNER.r - 40, zoom: 2.05 };
  const inApp = { x: 945, y: 540, zoom: 1.13 };
  const onWuu = { x: CORNER.x - 60, y: CORNER.floor - 200, zoom: 1.35 };
  // [time, camera]: each entry is reached at that time.
  const track: [number, Camera][] = [
    [12.3, wide], [13.0, close], [14.75, close], [15.35, wide],
    [21.6, wide], [23.2, inApp], [28.1, inApp], [29.0, { x: 960, y: 530, zoom: 1.0 }], [29.8, { x: 960, y: 530, zoom: 1.0 }], [30.4, onWuu],
  ];
  if (t <= track[0][0]) return wide;
  for (let i = 1; i < track.length; i++) {
    const [t1, c1] = track[i];
    if (t <= t1) {
      const [t0, c0] = track[i - 1];
      const e = inOut(seg(t, t0, t1));
      let shake = 0;
      if (t > LANDING && t < LANDING + 0.4) shake = Math.sin((t - LANDING) * 70) * 9 * (1 - (t - LANDING) / 0.4);
      return { x: lerp(c0.x, c1.x, e), y: lerp(c0.y, c1.y, e) + shake, zoom: lerp(c0.zoom, c1.zoom, e) };
    }
  }
  return track[track.length - 1][1];
}

// ---------------------------------------------------------------------------
// The old desk windows

function lastClick(win: number, t: number) {
  let last = -1;
  for (const [at, w] of CLICKS) if (w === win && at <= t) last = at;
  return last;
}

function asking(win: number, t: number) {
  for (const r of REQUESTS) if (r.win === win && t >= r.ask && t < r.answer) return r;
  return undefined;
}

/** Bubble scale: pops in, and pops away when answered. */
function bubbleScale(win: number, t: number) {
  let s = 0;
  for (const r of REQUESTS) {
    if (r.win !== win) continue;
    const grow = outBack(seg(t, r.ask, r.ask + 0.28));
    const gone = seg(t, r.answer, r.answer + 0.12);
    s = Math.max(s, grow * (1 - gone));
  }
  return s;
}

function collectProgress(win: number, t: number) {
  return seg(t, COLLECT[win], COLLECT[win] + COLLECT_TIME);
}

function deskWindowPose(i: number, t: number) {
  const d = DESK[i];
  let x = d.x, y = d.y, tilt = d.tilt, scale = 1;
  const ask = asking(i, t);
  if (ask && t - ask.ask < 0.4) tilt += Math.sin((t - ask.ask) * 38) * 1.6 * (1 - (t - ask.ask) / 0.4);
  const click = lastClick(i, t);
  if (click > 0) scale *= squash(t, click, 0.06, 0.35).sx;
  // The landing jolts every window.
  if (t > LANDING && t < LANDING + 0.5) {
    const k = (t - LANDING) / 0.5;
    y -= Math.abs(Math.sin(k * Math.PI * 2.5)) * 26 * (1 - k);
    tilt += Math.sin(k * 20 + i) * 2 * (1 - k);
  }
  return { x, y, w: d.w, h: d.h, tilt, scale };
}

function drawDeskWindows(ctx: Ctx, t: number) {
  const order = DESK.map((_, i) => i).sort((a, b) => lastClick(a, t) - lastClick(b, t) || a - b);
  for (const i of order) {
    const d = DESK[i];
    const p = collectProgress(i, t);
    if (p >= 1) continue;
    const pose = deskWindowPose(i, t);
    const e = inOut(p);
    const row = rowRect(i);
    const [rx, ry] = app(row.x + row.w / 2, row.y + row.h / 2);
    const cx = lerp(pose.x + pose.w / 2, rx, e);
    const cy = lerp(pose.y + pose.h / 2, ry, e) - Math.sin(e * Math.PI) * 80;
    const scale = pose.scale * lerp(1, row.w / pose.w, e);
    drawWindow(ctx, {
      x: cx - pose.w / 2, y: cy - pose.h / 2, w: pose.w, h: pose.h, theme: d.agent.theme,
      engine: d.agent.engine, tilt: lerp(pose.tilt, 0, e), scale, alpha: 1 - smooth(seg(p, 0.7, 1)),
    }, (c, _w, h) => {
      c.globalAlpha *= 1 - seg(p, 0, 0.35);
      drawLines(c, 232, 44, d.lines, typing(i, t), [d.agent.theme.text, d.agent.theme.accent], { gap: 34, thick: 14 });
      // Once the creature leaves for the sidebar, its window is empty.
      if (p === 0) drawDeskCreature(c, i, t, h);
    });
  }
}

function typing(i: number, t: number) {
  return ((t - DESK_START) * 0.3 + i * 0.27) % 1.25;
}

function drawDeskCreature(ctx: Ctx, i: number, t: number, h: number) {
  const a = DESK[i].agent;
  const floor = h - 46;
  const ask = asking(i, t);
  const answered = lastClick(i, t);
  const startled = t > LANDING && t < 17.2;
  let kind: EyeKind = "open";
  let hopState = { lift: 0, sx: 1 + Math.sin(t * 15 + i) * 0.02, sy: 1 - Math.sin(t * 15 + i) * 0.03 };
  let face = { yaw: 0.42, pitch: -0.22 };
  if (ask) {
    kind = "wide";
    hopState = hops(t, [ask.ask, ask.ask + 0.55, ask.ask + 1.1, ask.ask + 1.65, ask.ask + 2.2, ask.ask + 2.75, ask.ask + 3.3, ask.ask + 3.85], 0.5);
    face = { yaw: -0.1, pitch: 0.18 };
  } else if (answered > 0 && t - answered < 0.5) {
    kind = "happy";
    face = { yaw: 0, pitch: 0.1 };
  }
  if (startled) {
    kind = t < LANDING + 0.5 ? "wide" : "open";
    // Everyone turns toward the newcomer in the middle of the desk.
    const pose = deskWindowPose(i, t);
    face = look(pose.x + CREATURE.x, pose.y + h * 0.7, MIDDLE.x, MIDDLE.floor - 100, 1.2);
  }
  const ball = agent(a, CREATURE.x, floor, CREATURE.r, hopState, 34);
  drawBall(ctx, { ...ball, ...face, eyes: { kind, open: blinkFor(t, i) }, sway: Math.sin(t * 5 + i) * 0.35 + (ask ? Math.sin(t * 22) * 0.4 : 0) });
  drawBubble(ctx, CREATURE.x + 58, floor - CREATURE.r * 2 - 44, bubbleScale(i, t) * 1.15, t, "#FFFFFF", INK);
}

function blinkFor(t: number, seed: number) {
  const period = 2.6 + seed * 0.37;
  const p = ((t + seed * 0.9) % period) / 0.18;
  return p > 0 && p < 1 ? (p < 0.4 ? 1 - 0.92 * smooth(p / 0.4) : 0.08 + 0.92 * smooth((p - 0.4) / 0.6)) : 1;
}

// ---------------------------------------------------------------------------
// The unified Wuu app

function unfold(t: number) {
  return outBack(seg(t, 16.95, 17.6));
}

function drawApp(ctx: Ctx, t: number) {
  const u = unfold(t);
  if (u <= 0) return;
  const wuu = wuuPose(t);
  const cx = lerp(wuu.x, APP.x + APP.w / 2, clamp(u));
  const cy = lerp(wuu.y, APP.y + APP.h / 2, clamp(u));
  drawWindow(ctx, { x: cx - APP.w / 2, y: cy - APP.h / 2, w: APP.w, h: APP.h, theme: THEMES.wuu, engine: "wuu", scale: u }, (c, w, h) => {
    c.fillStyle = "#FBF8F3";
    c.fillRect(0, 0, SIDEBAR, h);
    c.fillStyle = "rgba(40,40,40,0.07)";
    c.fillRect(SIDEBAR - 1.5, 0, 1.5, h);
    drawPlus(c, t);
    for (let i = 0; i < SESSIONS.length; i++) drawRow(c, i, t);
    drawMainPane(c, w, h, t);
  });
}

function drawPlus(ctx: Ctx, t: number) {
  const press = t > SELECT[4][0] - 0.05 && t < SELECT[4][0] + 0.12 ? 0.9 : 1;
  ctx.save();
  ctx.translate(PLUS.x + PLUS.w / 2, PLUS.y + PLUS.h / 2);
  ctx.scale(press, press);
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

/** When each sidebar row starts to exist. */
function rowBorn(i: number) {
  return i < 4 ? COLLECT[i] + COLLECT_TIME * 0.72 : SELECT[4][0];
}

function selected(t: number) {
  let s = -1;
  for (const [at, i] of SELECT) if (t >= at) s = i;
  return s;
}

/** The newest session gets its engine (and creature) only once it is sent. */
const piArrives = SEND_CLICK + 0.55;

function drawRow(ctx: Ctx, i: number, t: number) {
  const born = rowBorn(i);
  if (t < born) return;
  const r = rowRect(i);
  const pop = outBack(seg(t, born, born + 0.35));
  ctx.save();
  ctx.translate(r.x + r.w / 2, r.y + r.h / 2);
  ctx.scale(lerp(0.6, 1, pop), lerp(0.6, 1, pop));
  ctx.globalAlpha *= clamp(pop * 2);
  ctx.translate(-r.w / 2, -r.h / 2);
  if (selected(t) === i) {
    const s = smooth(seg(t, SELECT.find(([, j]) => j === i)![0], SELECT.find(([, j]) => j === i)![0] + 0.2));
    ctx.fillStyle = `rgba(232, 224, 212, ${s})`;
    ctx.beginPath();
    ctx.roundRect(0, 0, r.w, r.h, 18);
    ctx.fill();
  }
  const a = SESSIONS[i];
  const hasAgent = i < 4 || t >= piArrives;
  const ax = 40, ay = r.h / 2;
  if (hasAgent) {
    const k = i < 4 ? 1 : outBack(seg(t, piArrives, piArrives + 0.35));
    const bob = Math.sin(t * 9 + i * 1.7) * 0.035;
    drawBall(ctx, {
      ...stand(a.skin, ax, ay + 22 * k, 22 * k), sx: 1 + bob, sy: 1 - bob, ground: undefined,
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
    drawSpinner(ctx, r.w - 30, ay, 9, t + i * 0.4, a.skin.lo);
  }
  ctx.restore();
}

function drawMainPane(ctx: Ctx, w: number, h: number, t: number) {
  const s = selected(t);
  ctx.save();
  ctx.beginPath();
  ctx.rect(SIDEBAR, 0, w - SIDEBAR, h);
  ctx.clip();
  if (s >= 0) {
    const at = SELECT.find(([, j]) => j === s)![0];
    const k = outCubic(seg(t, at, at + 0.35));
    ctx.save();
    ctx.globalAlpha *= k;
    ctx.translate(0, (1 - k) * 40);
    drawConversation(ctx, s, t, at, w);
    ctx.restore();
  }
  drawComposer(ctx, t);
  if (t > PILL_CLICK && t < PICK + 0.3) drawMenu(ctx, t);
  ctx.restore();
}

function drawConversation(ctx: Ctx, s: number, t: number, since: number, w: number) {
  const a = SESSIONS[s];
  const left = SIDEBAR + 64;
  const hasAgent = s < 4 || t >= piArrives;
  // The user's message, right-aligned.
  const sending = s < 4 ? 1 : seg(t, SEND_CLICK, SEND_CLICK + 0.4);
  if (sending > 0) {
    const sent = outBack(sending);
    const bw = 330, bx = w - 64 - bw, by = s < 4 ? 58 : lerp(COMPOSER.y - 40, 58, clamp(sent));
    ctx.save();
    ctx.fillStyle = "#EFEAE2";
    ctx.beginPath();
    ctx.roundRect(bx, by, bw, 92, 26);
    ctx.fill();
    drawLines(ctx, bx + 28, by + 26, [250, 170], 1, ["#D5CDBF"], { gap: 26, thick: 13 });
    ctx.restore();
  }
  if (!hasAgent) return;
  // The engine's creature answers in the thread.
  const k = s < 4 ? 1 : outBack(seg(t, piArrives, piArrives + 0.4));
  const r = 40 * k;
  const beat = Math.sin(t * 12) * 0.03;
  drawBall(ctx, {
    ...agent(a, left + 40, 250, r), ground: undefined, sx: 1 + beat, sy: 1 - beat,
    eyes: { kind: t - since < 0.45 ? "happy" : "open", open: blinkFor(t, s + 7) }, yaw: 0.45, pitch: -0.15,
    sway: Math.sin(t * 5) * 0.3,
  });
  const written = s < 4 ? 0.35 + ((t - since) * 0.45) % 0.8 : (t - piArrives) * 0.5;
  drawLines(ctx, left + 110, 205, [420, 520, 360, 470, 300], written, [a.theme === THEMES.terminal ? "#CFD8E6" : "#E6E0D6", a.skin.body], { gap: 36, thick: 15 });
}

function drawComposer(ctx: Ctx, t: number) {
  ctx.save();
  ctx.shadowColor = "rgba(80,58,30,0.1)";
  ctx.shadowBlur = 20;
  ctx.shadowOffsetY = 6;
  ctx.fillStyle = "#FFFFFF";
  ctx.beginPath();
  ctx.roundRect(COMPOSER.x, COMPOSER.y, COMPOSER.w, COMPOSER.h, 30);
  ctx.fill();
  ctx.restore();
  ctx.strokeStyle = "rgba(40,40,40,0.1)";
  ctx.lineWidth = 2;
  ctx.beginPath();
  ctx.roundRect(COMPOSER.x, COMPOSER.y, COMPOSER.w, COMPOSER.h, 30);
  ctx.stroke();
  // Engine pill: the session's harness, or the one being picked.
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
  drawLines(ctx, PILL.x + PILL.w + 26, COMPOSER.y + COMPOSER.h / 2 - 7, [260], s === 4 && t < SEND_CLICK ? seg(t, 25.9, 26.2) : 0, ["#E3DDD3"], { thick: 14 });
  // Send button with an upward arrow.
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

function menuOpen(t: number) {
  return outBack(seg(t, PILL_CLICK, PILL_CLICK + 0.3)) * (1 - inCubic(seg(t, PICK + 0.05, PICK + 0.25)));
}

function hovered(t: number) {
  // The cursor wanders across a few engines before settling on one.
  const path = [[26.6, 0], [26.75, 2], [26.9, 5], [27.02, 7]] as const;
  let h = -1;
  for (const [at, i] of path) if (t >= at) h = i;
  return h;
}

function drawMenu(ctx: Ctx, t: number) {
  const open = menuOpen(t);
  if (open <= 0) return;
  ctx.save();
  ctx.translate(MENU_BOX.x + 40, MENU_BOX.y + MENU_BOX.h);
  ctx.scale(open, open);
  ctx.translate(-MENU_BOX.x - 40, -MENU_BOX.y - MENU_BOX.h);
  ctx.save();
  ctx.shadowColor = "rgba(80,58,30,0.18)";
  ctx.shadowBlur = 30;
  ctx.shadowOffsetY = 10;
  ctx.fillStyle = "#FFFFFF";
  ctx.beginPath();
  ctx.roundRect(MENU_BOX.x, MENU_BOX.y, MENU_BOX.w, MENU_BOX.h, 26);
  ctx.fill();
  ctx.restore();
  const h = hovered(t);
  MENU.forEach((engine, i) => {
    const k = outBack(seg(t, PILL_CLICK + 0.08 + i * 0.03, PILL_CLICK + 0.35 + i * 0.03));
    if (k <= 0) return;
    const [x, y] = menuSlot(i);
    const lift = i === h ? 8 : 0;
    const picked = i === 7 && t > PICK ? outBack(seg(t, PICK, PICK + 0.2)) * 0.25 : 0;
    ctx.save();
    ctx.translate(x, y - lift);
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
// Creatures hopping from their old windows into the sidebar

function drawTravellers(ctx: Ctx, t: number) {
  for (let i = 0; i < 4; i++) {
    const p = collectProgress(i, t);
    if (p <= 0 || p >= 1) continue;
    const pose = deskWindowPose(i, COLLECT[i]);
    const floor = pose.y + TITLE_BAR + pose.h - TITLE_BAR - 46;
    const start = { x: pose.x + CREATURE.x, y: floor - CREATURE.r };
    const row = rowRect(i);
    const [ex, ey] = app(row.x + 40, row.y + row.h / 2);
    const e = inOut(p);
    const [x, y] = arc(e, start.x, start.y, ex, ey, 260);
    const r = lerp(CREATURE.r, 22, e);
    const spin = Math.sin(p * Math.PI) * 18;
    const a = DESK[i].agent;
    drawBall(ctx, { x, y, r, skin: a.skin, engine: a.engine, tilt: spin, eyes: { kind: "happy", open: 1 }, sy: 1 + Math.sin(p * Math.PI) * 0.12, sx: 1 - Math.sin(p * Math.PI) * 0.08 });
    // A soft trail of sparkles marks the path.
    for (let k = 1; k <= 3; k++) {
      const q = e - k * 0.06;
      if (q <= 0) continue;
      const [tx, ty] = arc(q, start.x, start.y, ex, ey, 260);
      drawSparkle(ctx, tx, ty, 10 - k * 2, CONFETTI[(i + k) % CONFETTI.length], 0.8 - k * 0.2);
    }
  }
  // A little flash as each creature settles into its row.
  for (let i = 0; i < 4; i++) {
    const row = rowRect(i);
    const [x, y] = app(row.x + 40, row.y + row.h / 2);
    drawBurst(ctx, x, y, seg(t, COLLECT[i] + COLLECT_TIME, COLLECT[i] + COLLECT_TIME + 0.4), { count: 8, inner: 30, outer: 62, width: 7, colors: [SUN, CORAL, MINT] });
  }
}

// ---------------------------------------------------------------------------
// Wuu

interface Pose { x: number; y: number; ball: Ball }

function wuuPose(t: number): Pose {
  const r = CORNER.r;
  const ry = extent(WUU).ry;
  // Shot 3: the leap out of frame, across the desk, and back down.
  if (t >= 14.35 && t < LANDING) {
    const crouch = seg(t, 14.35, 14.55);
    if (t < 14.55) {
      const b = stand(WUU, CORNER.x, CORNER.floor, r, { lift: 0, sx: 1 + 0.14 * smooth(crouch), sy: 1 - 0.2 * smooth(crouch) });
      return { x: b.x, y: b.y, ball: b };
    }
    const x = keys(t, [[14.55, CORNER.x], [15.5, MIDDLE.x, linear]]);
    const y = keys(t, [[14.55, CORNER.floor - r * ry], [15.0, -420, outCubic], [15.45, -420], [LANDING, MIDDLE.floor - r * ry, inCubic]]);
    const ground = t < 14.8 ? CORNER.floor : t > 15.45 ? MIDDLE.floor : undefined;
    const b = { ...stand(WUU, x, MIDDLE.floor, r), y, sx: 0.88, sy: 1.16, ground };
    return { x, y, ball: b };
  }
  if (t >= LANDING && t < 17.35 + 0.65) {
    const land = squash(t, LANDING, 0.34, 0.6);
    // Hop in place to unfold the app, then hop over to the corner.
    if (t >= 17.35) {
      const p = seg(t, 17.35, 18.0);
      const h = hop(t, 17.35, 0.65);
      const x = lerp(MIDDLE.x, CORNER.x, inOut(seg(p, 0.25, 0.85)));
      const floor = lerp(MIDDLE.floor, CORNER.floor, inOut(seg(p, 0.25, 0.85)));
      const b = stand(WUU, x, floor, r, h, 240);
      return { x, y: b.y, ball: b };
    }
    const h = hop(t, 16.7, 0.65);
    const b = stand(WUU, MIDDLE.x, MIDDLE.floor, r, t < 16.7 ? { lift: 0, ...land } : h, 150);
    return { x: MIDDLE.x, y: b.y, ball: b };
  }
  // Corner: small hops to call each window home, a happy bounce at the end.
  const calls = COLLECT.map((c) => c - 0.3);
  const cheers = [20.5, 28.4, 28.9];
  const h = hops(t, [...calls, ...cheers], 0.42);
  const pop = squash(t, 13.95, 0.22, 0.5);
  const b = stand(WUU, CORNER.x, CORNER.floor, r, { lift: h.lift, sx: h.sx * pop.sx, sy: h.sy * pop.sy }, 60);
  return { x: CORNER.x, y: b.y, ball: b };
}

// Expression changes hide behind blinks, so faces never cross-fade.
const EXPRESSION: [number, EyeKind][] = [
  [0, "open"], [9.6, "wide"], [10.9, "flat"], [11.9, "squeeze"], [12.3, "flat"], [13.5, "open"], [13.95, "star"],
  [14.35, "squeeze"], [LANDING + 0.1, "wide"], [16.0, "open"], [16.65, "happy"], [18.0, "open"],
  [COLLECT[3] + COLLECT_TIME, "happy"], [21.3, "open"], [28.2, "happy"], [29.8, "open"],
];
const BLINKS = [6.3, 8.9, 13.2, 16.3, 21.9, 24.9, 30.62];

type Face = { yaw: number; pitch: number };
const mixFace = (a: Face, b: Face, k: number): Face => ({ yaw: lerp(a.yaw, b.yaw, k), pitch: lerp(a.pitch, b.pitch, k) });

function wuuFace(t: number, pose: Pose): Face {
  const cur = cursorState(t);
  const atCursor = look(pose.x, pose.y, cur.x, cur.y, 1.1);
  if (t < 13.5) return atCursor;
  if (t < 13.95) return mixFace(atCursor, { yaw: 0.3, pitch: 0.42 }, smooth(seg(t, 13.5, 13.75)));
  if (t < 14.35) return mixFace({ yaw: 0.3, pitch: 0.42 }, { yaw: 0, pitch: 0.05 }, smooth(seg(t, 13.9, 14.02)));
  if (t < LANDING) return { yaw: 0, pitch: 0.3 };
  if (t < 18.0) return { yaw: keys(t, [[LANDING + 0.25, 0], [16.2, -0.7], [16.45, 0.7], [16.65, 0]]), pitch: 0.05 };
  if (t < 20.8) {
    // Calling each window home: glance at the one being collected.
    const i = COLLECT.findIndex((c) => t < c + COLLECT_TIME);
    const d = DESK[i >= 0 ? i : 3];
    return look(pose.x, pose.y, d.x + d.w / 2, d.y + d.h / 2, 1.2);
  }
  if (t < 29.8) return atCursor;
  // Turn to the camera in the icon pose before the iris closes.
  return mixFace(atCursor, ICON_POSE, outBack(seg(t, 29.8, 30.25)));
}

function drawWuu(ctx: Ctx, t: number) {
  const pose = wuuPose(t);
  const face = wuuFace(t, pose);
  const flash = (at: number, hold: number) => seg(t, at, at + 0.2) * (1 - seg(t, at + hold, at + hold + 0.25));
  const marks = Math.max(
    outCubic(seg(t, 13.95, 14.25)) * (1 - seg(t, 14.4, 14.6)),
    flash(16.95, 0.5),
    ...COLLECT.map((c) => flash(c - 0.3, 0.3)),
    seg(t, 28.4, 28.7),
  );
  const sweat = seg(t, 11.3, 11.6) * (1 - seg(t, 13.4, 13.6));
  const blush = t < 20.2 ? 0 : t < 28.2 ? 0.6 * seg(t, 20.2, 20.6) : lerp(0.6, 1, seg(t, 28.2, 28.6));
  const roll = t > 29.8 ? ICON_POSE.roll * outBack(seg(t, 29.8, 30.25)) : 0;
  drawBall(ctx, { ...pose.ball, ...face, roll, eyes: eyes(t, EXPRESSION, BLINKS), marks, sweat, blush });
  // Shot 3 flourishes around the close-up.
  if (t > 13.4 && t < 14.3) drawThinking(ctx, pose.x + CORNER.r * 0.7, pose.y - CORNER.r * 1.25, 1.1, seg(t, 13.4, 13.85) * (1 - seg(t, 13.9, 14.0)));
  drawBurst(ctx, pose.x, pose.y, seg(t, 13.95, 14.55), { count: 12, inner: CORNER.r * 1.25, outer: CORNER.r * 2.1, width: 12, colors: [SUN, CORAL, MINT], spin: 0.26 });
  if (t > 13.95 && t < 14.6) {
    for (let i = 0; i < 5; i++) {
      const a = i * 1.26 + 0.4;
      const k = seg(t, 13.95 + i * 0.04, 14.5);
      drawSparkle(ctx, pose.x + Math.cos(a) * CORNER.r * (1.4 + k * 0.5), pose.y + Math.sin(a) * CORNER.r * (1.3 + k * 0.4), 16 * Math.sin(k * Math.PI), SUN);
    }
  }
  // Landing dust.
  const dust = seg(t, LANDING, LANDING + 0.7);
  if (dust > 0 && dust < 1) {
    for (const side of [-1, 1]) {
      drawPuff(ctx, MIDDLE.x + side * (CORNER.r + 70 * outCubic(dust)), MIDDLE.floor - 10 - 30 * dust, 34 * (1 - dust * 0.4), 0.9 * (1 - dust));
    }
  }
}

// ---------------------------------------------------------------------------
// The user's cursor: the only "human" in the film.

interface CursorState { x: number; y: number; rot: number; press: number; sweat: number; daze: number; alpha: number }

/** [arrival time, x, y]; the cursor glides between consecutive stops. */
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

const deskTarget = (i: number): [number, number] => [DESK[i].x + CREATURE.x + 70, DESK[i].y + DESK[i].h - 150];
const REST_SPOT: [number, number] = [790, 575];

const DESK_STOPS: [number, number, number][] = [
  [5.0, 760, 1180], [5.45, 700, 720],
  ...CLICKS.flatMap(([at, i], k): [number, number, number][] => {
    const [x, y] = deskTarget(i);
    const prev = k === 0 ? 5.45 : CLICKS[k - 1][0];
    return [[Math.max(prev + 0.08, at - 0.08), x, y], [at + 0.05, x, y]];
  }),
];

const appPoint = (x: number, y: number): [number, number] => app(x, y);
const rowPoint = (i: number): [number, number] => { const r = rowRect(i); return appPoint(r.x + r.w * 0.55, r.y + r.h * 0.6); };
const APP_CLICKS: [number, [number, number]][] = [
  ...SELECT.slice(0, 4).map(([at, i]): [number, [number, number]] => [at, rowPoint(i)]),
  [SELECT[4][0], appPoint(PLUS.x + PLUS.w * 0.6, PLUS.y + PLUS.h * 0.6)],
  [PILL_CLICK, appPoint(PILL.x + 60, PILL.y + 30)],
  [26.6, appPoint(...menuSlot(0))], [26.75, appPoint(...menuSlot(2))], [26.9, appPoint(...menuSlot(5))], [27.02, appPoint(...menuSlot(7))],
  [PICK, appPoint(...menuSlot(7))],
  [SEND_CLICK, appPoint(SEND.x + 6, SEND.y + 8)],
];
const PRESSES = [...CLICKS.map(([at]) => at), ...SELECT.map(([at]) => at), PILL_CLICK, PICK, SEND_CLICK];

const APP_STOPS: [number, number, number][] = [
  [WAKE + 0.5, REST_SPOT[0], REST_SPOT[1]],
  ...APP_CLICKS.flatMap(([at, [x, y]], k): [number, number, number][] => {
    const prev = k === 0 ? WAKE + 0.5 : APP_CLICKS[k - 1][0];
    const hover = at - prev < 0.2;
    return hover ? [[at - 0.02, x, y]] : [[Math.max(prev + 0.1, at - 0.07), x, y], [at + 0.05, x, y]];
  }),
  [28.4, 1330, 660], [29.6, 1370, 640],
];

function cursorState(t: number): CursorState {
  const press = Math.max(0, ...PRESSES.map((at) => 1 - Math.abs(t - at) / 0.09));
  if (t < 10.75) {
    const [x, y] = glide(t, DESK_STOPS);
    return { x, y, rot: 0, press, sweat: seg(t, 9.3, 9.6), daze: 0, alpha: 1 };
  }
  if (t < 11.45) {
    // Can't keep up: frantic zig-zags between every window at once.
    const k = seg(t, 10.75, 10.9);
    const [x0, y0] = deskTarget(3);
    const x = lerp(x0, 830 + wobble(t, 1, 7) * 470, k);
    const y = lerp(y0, 500 + wobble(t, 2, 7) * 300, k);
    return { x, y, rot: wobble(t, 4, 6) * 18, press: 0, sweat: 1, daze: 0, alpha: 1 };
  }
  if (t < WAKE) {
    // Spin, wobble, and topple over.
    const spin = keys(t, [[11.45, 0], [12.05, 720, outCubic], [12.4, 720 + 118, outBack]]);
    const [xa, ya] = [830 + wobble(11.45, 1, 7) * 470, 500 + wobble(11.45, 2, 7) * 300];
    const x = lerp(xa, REST_SPOT[0], inOut(seg(t, 11.45, 12.05)));
    const y = lerp(ya, REST_SPOT[1], inOut(seg(t, 11.45, 12.05))) + outCubic(seg(t, 12.05, 12.4)) * 28 - Math.sin(seg(t, 12.05, 12.4) * Math.PI) * 30;
    return { x, y, rot: spin, press: 0, sweat: 1 - seg(t, 17.5, 18), daze: seg(t, 11.9, 12.3) * (1 - seg(t, 20.2, 20.8)), alpha: 1 };
  }
  // Rested and happy again: back on its feet, then driving the unified app.
  const up = outBack(seg(t, WAKE, WAKE + 0.4));
  const bounce = Math.sin(seg(t, WAKE, WAKE + 0.4) * Math.PI) * 40;
  if (t < WAKE + 0.5) return { x: REST_SPOT[0], y: REST_SPOT[1] + 28 * (1 - up) - bounce, rot: lerp(720 + 118, 720, up), press: 0, sweat: 0, daze: 0, alpha: 1 };
  const [x, y] = glide(t, APP_STOPS);
  const wiggle = t > 28.4 ? Math.sin((t - 28.4) * 10) * 10 * (1 - seg(t, 29, 29.6)) : 0;
  return { x, y, rot: wiggle, press, sweat: 0, daze: 0, alpha: 1 };
}

function drawTheCursor(ctx: Ctx, t: number) {
  const c = cursorState(t);
  for (const at of PRESSES) drawRipple(ctx, c.x, c.y, seg(t, at, at + 0.4), at < 11 ? CORAL : INK);
  drawCursor(ctx, c.x, c.y, { rot: c.rot, press: c.press, alpha: c.alpha, scale: 1.9 });
  if (c.sweat > 0) drawSweat(ctx, c.x + 58, c.y - 6 + 6 * Math.sin(t * 8), 17, c.sweat);
  drawDaze(ctx, c.x + 18, c.y - 30, 58, t, c.daze);
}
