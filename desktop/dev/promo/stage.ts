// Shots 6–9, one continuous studio stage (31 s – 59 s):
//   6. A huge blueprint. Alone, Wuu barely builds one piece as hours pass.
//   7. Friends peek in. An idea: a headset drops on; Wuu becomes the coordinator.
//   8. Each friend opens a session; Wuu hands out pieces and watches them all at
//      once. One gets stuck, another helps. The pieces come home.
//   9. Countdown, continuous liftoff, then a push into Wuu's porthole.
import {
  drawBall, drawBlueprint, drawBubble, drawClock, drawFlame, drawPiece, drawPuff, drawShadow,
  drawWindow, extent, INK, pieceCenter, THEMES, TITLE_BAR, WUU, type Ball, type Piece,
} from "./art";
import {
  clamp, eyes, hop, hops, inCubic, inOut, keys, lerp, linear, mixColor, outBack, outCubic, seg, smooth, squash, wobble,
  type EyeKind, type Hop,
} from "./motion";
import {
  agent, arc, BUILDER, CLAUDE, CODEX, CORAL, CREAM, CURSOR, drawCheck,
  look, matchCamera, MINT, OPENCODE, PI, stairs, stand, SUN, toScreen, W, H, withCamera, type Agent, type Camera, type Ctx,
} from "./cast";

export const STAGE_START = 31;
export const STAGE_END = 59;

// ---------------------------------------------------------------------------
// World layout

const HORIZON = 820;
const ROCKET = { x: 960, y: 595, s: 1.55 };
const PAD = { x: 745, y: 798, w: 430, h: 46 };
const BOARD = { x: 150, y: 240, w: 460, h: 480 };
const WUU_R = 100;
const HOME = { x: 1300, floor: 980 };
const WORK = { x: 1125, floor: 965 };
const CENTER = { x: 960, floor: 1000 };

const CAM_OPEN = matchCamera(stand(WUU, HOME.x, HOME.floor, WUU_R));
const CAM_WIDE: Camera = { x: 960, y: 540, zoom: 1 };
const CAM_TEAM: Camera = { x: 960, y: 590, zoom: 0.78 };

// The team, in the order of their sessions: three on the left, three on the right.
interface Member { agent: Agent; piece: Piece; lineup: { x: number; floor: number }; peek: Peek; r: number }
type Peek = { from: [number, number]; to: [number, number]; at: number; floor?: boolean };
const TEAM: Member[] = [
  { agent: CLAUDE, piece: "nose", lineup: { x: 420, floor: 1030 }, r: 76, peek: { from: [-100, 1000], to: [40, 1000], at: 36.6, floor: true } },
  { agent: CODEX, piece: "cabin", lineup: { x: 600, floor: 985 }, r: 72, peek: { from: [430, 400], to: [430, 240], at: 36.85 } },
  { agent: CURSOR, piece: "finL", lineup: { x: 760, floor: 1052 }, r: 72, peek: { from: [250, 1260], to: [250, 1120], at: 37.05 } },
  { agent: OPENCODE, piece: "hull", lineup: { x: 1160, floor: 1052 }, r: 76, peek: { from: [2030, 1000], to: [1880, 1000], at: 36.75, floor: true } },
  { agent: PI, piece: "finR", lineup: { x: 1330, floor: 985 }, r: 72, peek: { from: [2090, 990], to: [2090, 990], at: 99 } },
  { agent: BUILDER, piece: "nozzle", lineup: { x: 1510, floor: 1032 }, r: 68, peek: { from: [2120, 1030], to: [2120, 1030], at: 99 } },
];
const ARRIVE = [39.5, 39.66, 39.82, 39.6, 39.95, 40.12];
const ARRIVE_TIME = 0.62;

// Session windows are laid out on screen around the team camera.
const SW = 460, SH = 272;
const SLOT = [
  { x: 44, y: 64 }, { x: 44, y: 404 }, { x: 44, y: 744 },
  { x: 1416, y: 64 }, { x: 1416, y: 404 }, { x: 1416, y: 744 },
];
const SLOT_CREATURE = { x: 96, floor: SH - TITLE_BAR - 30, r: 50 };
const SLOT_WORK = { x: 312, y: (SH - TITLE_BAR) / 2 - 6, s: 0.6 };

// ---------------------------------------------------------------------------
// Timeline

const BOARD_LAND = 32.25;
const HAMMER = [33.6, 34.1, 34.62, 35.2, 35.9, 36.55];
const IDEA = 38.0;
const HEADSET_LAND = 39.0;
const WINDOWS_OPEN = 41.95;
const JUMP_IN = 42.1;
const LINES = 42.85;
const HAND_OUT = 43.3;
const STUCK = 45.6;
const TOSS = 46.62;
const CATCH = 47.12;
const RETURN: Record<Piece, number> = { nozzle: 50.0, hull: 50.3, finL: 50.6, finR: 50.85, cabin: 51.15, nose: 51.5 };
const RETURN_TIME = 0.55;
const WINDOWS_CLOSE = 53.3;
const BOARD_ROCKET = 54.3;
const LAMPS = [55.1, 55.5, 55.9];
const IGNITION = 56.2;
const LIFTOFF = 56.5;
const PORTRAIT = 58.8;

/** Shared piece progress in the team's sessions. */
function progress(piece: Piece, t: number) {
  switch (piece) {
    case "nose": return stairs(seg(t, 44.3, 46.1), 7);
    case "cabin": return t < CATCH ? 0.42 * stairs(seg(t, 44.4, STUCK), 4) : lerp(0.42, 1, stairs(seg(t, CATCH + 0.1, 49.8), 6));
    case "finL": return stairs(seg(t, 44.5, 48.6), 9);
    case "hull": return stairs(seg(t, 44.3, 49.2), 10);
    case "finR": return stairs(seg(t, 44.6, 48.9), 9);
    // The builder picks up where Wuu's lonely attempt stopped.
    case "nozzle": return lerp(SOLO_DONE, 1, stairs(seg(t, 44.4, 48.2), 4));
  }
}
const DONE: Record<Piece, number> = { nose: 46.1, cabin: 49.8, finL: 48.6, hull: 49.2, finR: 48.9, nozzle: 48.2 };

/** How far the lonely first attempt got: a tenth of the nozzle per hammer tap. */
const SOLO_STEP = 0.1;
const SOLO_DONE = HAMMER.length * SOLO_STEP;
function soloBuild(t: number) {
  const landed = HAMMER.filter((h) => t > h + 0.4).length;
  const current = HAMMER.find((h) => t > h + 0.4 && t < h + 0.7);
  return (landed + (current ? smooth(seg(t, current + 0.4, current + 0.7)) : 0) - (current ? 1 : 0)) * SOLO_STEP;
}

// ---------------------------------------------------------------------------

export function stageShot(ctx: Ctx, t: number, showPortrait = true) {
  const cam = camera(t);
  ctx.fillStyle = CREAM;
  ctx.fillRect(0, 0, W, H);
  withCamera(ctx, cam, () => {
    drawGround(ctx, t, cam);
    drawCodexPeek(ctx, t);
    drawPlan(ctx, t);
    drawPad(ctx, t);
    drawSmoke(ctx, t, true);
    drawRocket(ctx, t);
    drawSmoke(ctx, t, false);
    drawTeamOnGround(ctx, t);
    drawWuu(ctx, t);
  });
  drawSessions(ctx, t, cam);
  drawTimeClock(ctx, t);
  if (showPortrait && t >= PORTRAIT) drawBall(ctx, launchPortrait(t));
}

function camera(t: number): Camera {
  if (t < 31.4) return CAM_OPEN;
  if (t < 40.9) {
    const e = smooth(seg(t, 31.4, 32.6));
    return mixCam(CAM_OPEN, CAM_WIDE, e);
  }
  if (t < LIFTOFF + 0.3) {
    const shake = t > 55.6 ? wobble(t, 5, 18) * lerp(0.5, 2, seg(t, 55.6, IGNITION)) : 0;
    return { ...mixCam(CAM_WIDE, CAM_TEAM, inOut(seg(t, 40.9, 41.8))), x: 960 + shake };
  }
  // Ease into a tracking shot before pushing in. A screen-space target avoids
  // overtaking the rocket and making it appear to fall back down during the pan.
  const followStart = LIFTOFF + 0.3;
  const startY = toScreen(CAM_TEAM, ROCKET.x, ROCKET.y - rise(followStart) - 63 * ROCKET.s)[1];
  const push = smooth(seg(t, 57.8, 59.6));
  const screenY = keys(t, [[followStart, startY], [57.35, startY - 55, outCubic], [57.8, startY - 55], [59.6, 480, smooth]]);
  const zoom = lerp(CAM_TEAM.zoom, 210 / (24 * ROCKET.s), push);
  return {
    x: ROCKET.x,
    y: ROCKET.y - rise(t) - 63 * ROCKET.s - (screenY - H / 2) / zoom,
    zoom,
  };
}

/** The same passenger pose continues into the end card; there is no second landing. */
export function launchPortrait(t: number): Ball {
  const cam = camera(t);
  const [x, y] = toScreen(cam, ROCKET.x, ROCKET.y - rise(t) - 63 * ROCKET.s);
  return { x, y, r: 24 * ROCKET.s * cam.zoom, skin: WUU, pitch: 0.2, eyes: { kind: "open", open: 1 } };
}

const mixCam = (a: Camera, b: Camera, e: number): Camera => ({ x: lerp(a.x, b.x, e), y: lerp(a.y, b.y, e), zoom: lerp(a.zoom, b.zoom, e) });

// ---------------------------------------------------------------------------
// Sky and ground

function drawGround(ctx: Ctx, t: number, cam: Camera) {
  ctx.save();
  ctx.globalAlpha *= 1 - smooth(seg(t, 57.2, 58.4));
  ctx.fillStyle = "#E9E9E3";
  ctx.fillRect(cam.x - 3000, HORIZON, 6000, 3000);
  ctx.restore();
}

function drawPad(ctx: Ctx, t: number) {
  ctx.fillStyle = "#BFC6BD";
  ctx.beginPath();
  ctx.roundRect(PAD.x, PAD.y, PAD.w, PAD.h, 4);
  ctx.fill();
  LAMPS.forEach((at, i) => {
    const on = seg(t, at, at + 0.1) * (1 - seg(t, 57.2, 57.6));
    const x = PAD.x + PAD.w / 2 + (i - 1) * 58, y = PAD.y + 29;
    ctx.fillStyle = mixColor("#92988F", i === 2 ? "#537E68" : SUN, on);
    ctx.beginPath();
    ctx.arc(x, y - 6, 8, 0, Math.PI * 2);
    ctx.fill();
  });
}

function drawPlan(ctx: Ctx, t: number) {
  const land = seg(t, 31.7, BOARD_LAND);
  const alpha = smooth(land) * (1 - smooth(seg(t, 41, WINDOWS_OPEN)));
  if (alpha <= 0) return;
  ctx.save();
  ctx.globalAlpha *= alpha;
  ctx.translate(BOARD.x, BOARD.y + 40 * (1 - smooth(land)));
  drawBlueprint(ctx, 0, 0, BOARD.w, BOARD.h);
  ctx.translate(BOARD.w / 2, BOARD.h / 2 + 60);
  ctx.scale(1.12, 1.12);
  for (const id of ["finL", "finR", "nozzle", "hull", "cabin", "nose"] as Piece[]) drawPiece(ctx, id, 0, { ghost: "#7E9387" });
  ctx.restore();
}

// ---------------------------------------------------------------------------
// The rocket

const ORDER: Piece[] = ["finL", "finR", "nozzle", "hull", "cabin", "nose"];

function rise(t: number) {
  return 650 * Math.max(0, t - LIFTOFF) ** 2;
}

function drawRocket(ctx: Ctx, t: number) {
  const ghost = seg(t, 32.45, 32.9);
  if (ghost <= 0) return;
  const rumble = t > 55.6 && t < LIFTOFF + 0.4 ? wobble(t, 9, 40) * 3 : 0;
  const y = ROCKET.y - rise(t);
  const snap = Math.max(0, ...ORDER.map((id) => (t > RETURN[id] + RETURN_TIME ? 1 - seg(t, RETURN[id] + RETURN_TIME, RETURN[id] + RETURN_TIME + 0.3) : 0)));
  ctx.save();
  ctx.translate(ROCKET.x + rumble, y);
  ctx.scale(ROCKET.s * (1 + snap * 0.03), ROCKET.s * (1 - snap * 0.02));
  ctx.globalAlpha *= ghost * (1 - smooth(seg(t, PORTRAIT, 60)));
  if (t > IGNITION) drawFlame(ctx, 0, 128, clamp(seg(t, IGNITION, IGNITION + 0.25)) * (1 + seg(t, LIFTOFF, 57.2) * 0.5), t);
  for (const id of ORDER) {
    const state = pieceState(id, t);
    if (state === "away" || state === "flying") {
      drawPiece(ctx, id, 0, { ghost: "rgba(126, 147, 135, 0.45)" });
      continue;
    }
    const build = state === "home" ? 1 : id === "nozzle" ? soloBuild(t) : 0;
    drawPiece(ctx, id, build, { porthole: id === "cabin" && t < PORTRAIT ? (c) => drawPassenger(c, t) : undefined });
  }
  ctx.restore();
}

type PieceState = "rocket" | "away" | "flying" | "home";
function pieceState(id: Piece, t: number): PieceState {
  const out = HAND_OUT + TEAM.findIndex((m) => m.piece === id) * 0.1;
  if (t < out) return "rocket";
  if (t < RETURN[id]) return "away";
  if (t < RETURN[id] + RETURN_TIME) return "flying";
  return "home";
}

/** Wuu's face in the porthole, after hopping aboard. */
function drawPassenger(ctx: Ctx, t: number) {
  if (t < BOARD_ROCKET + 0.6) return;
  drawBall(ctx, {
    x: 0, y: -63, r: 24, skin: WUU,
    eyes: eyes(t, [[0, "open"], [IGNITION, "squeeze"], [LIFTOFF + 0.5, "happy"], [58.3, "open"]], [55.3]), pitch: 0.2,
  });
}

function drawSmoke(ctx: Ctx, t: number, back: boolean) {
  const k = seg(t, IGNITION, IGNITION + 2.2);
  if (k <= 0) return;
  for (let i = 0; i < 7; i++) {
    const side = i % 2 ? 1 : -1;
    if ((i % 3 === 0) !== back) continue;
    const p = clamp(k * 1.6 - i * 0.07);
    const x = ROCKET.x + side * (60 + 260 * outCubic(p) * (0.5 + (i % 4) * 0.2));
    const y = PAD.y + 10 - 40 * p - (i % 3) * 18;
    drawPuff(ctx, x, y, (40 + i * 7) * (0.5 + p), 0.65 * (1 - seg(k, 0.55, 1)), back ? "#DEE3DB" : "#FAFBF7");
  }
}

// ---------------------------------------------------------------------------
// Wuu

const EXPRESSION: [number, EyeKind][] = [
  [0, "open"], [31.9, "wide"], [32.5, "open"], [34.9, "flat"], [35.7, "sleepy"], [37.3, "wide"], [IDEA, "wide"],
  [HEADSET_LAND - 0.02, "squeeze"], [HEADSET_LAND + 0.25, "happy"], [40.6, "open"], [JUMP_IN + 0.5, "content"],
  [44.2, "open"], [STUCK + 0.3, "wide"], [CATCH + 0.1, "happy"], [47.7, "open"], [50.0, "content"], [52.2, "happy"],
];
const BLINKS = [32.9, 33.9, 34.7, 40.4, 43.6, 45.2, 48.4, 49.5, 53.0];

interface Pose { x: number; y: number; floor: number; ball: Ball }

function wuuPose(t: number): Pose {
  const ry = extent(WUU).ry;
  // Hop to the rocket and climb aboard.
  if (t > BOARD_ROCKET) {
    const p = seg(t, BOARD_ROCKET, BOARD_ROCKET + 0.6);
    if (p >= 1) return { x: -999, y: -999, floor: CENTER.floor, ball: { x: -999, y: -999, r: 0, alpha: 0 } };
    const [px, py] = [ROCKET.x, ROCKET.y - 63 * ROCKET.s];
    const [x, y] = arc(inOut(p), CENTER.x, CENTER.floor - WUU_R * ry, px, py, 260);
    const r = lerp(WUU_R, 24 * ROCKET.s, inCubic(p));
    return { x, y, floor: CENTER.floor, ball: { x, y, r, skin: WUU, gear: "headset", ground: p < 0.3 ? CENTER.floor : undefined, sx: 0.92, sy: 1.1 } };
  }
  if (t < 32.95) {
    const h = hop(t, 32.0, 0.5);
    const b = stand(WUU, HOME.x, HOME.floor, WUU_R, h, 70);
    return { x: HOME.x, y: b.y, floor: HOME.floor, ball: b };
  }
  if (t < 33.5) {
    const p = seg(t, 32.95, 33.5);
    const h = hop(t, 32.95, 0.55);
    const x = lerp(HOME.x, WORK.x, inOut(p)), floor = lerp(HOME.floor, WORK.floor, inOut(p));
    const b = stand(WUU, x, floor, WUU_R, h, 110);
    return { x, y: b.y, floor, ball: b };
  }
  if (t < 39.3) {
    // Hammering hops that slow down, then slumping into a nap.
    let h: Hop = hops(t, HAMMER, 0.45);
    const sit = smooth(seg(t, 36.9, 37.2)) * (1 - smooth(seg(t, 37.3, 37.5)));
    const breathe = sit > 0 ? Math.sin(t * 3) * 0.02 : 0;
    const pop = squash(t, IDEA, 0.24, 0.5);
    const land = squash(t, HEADSET_LAND, 0.2, 0.45);
    h = { lift: h.lift, sx: h.sx * (1 + sit * 0.1) * pop.sx * land.sx, sy: h.sy * (1 - sit * 0.13 + breathe) * pop.sy * land.sy };
    const b = stand(WUU, WORK.x, WORK.floor, WUU_R, h, 80);
    return { x: WORK.x, y: b.y, floor: WORK.floor, ball: { ...b, tilt: -sit * 6 } };
  }
  if (t < 39.95) {
    const p = seg(t, 39.3, 39.95);
    const h = hop(t, 39.3, 0.65);
    const x = lerp(WORK.x, CENTER.x, inOut(p)), floor = lerp(WORK.floor, CENTER.floor, inOut(p));
    const b = stand(WUU, x, floor, WUU_R, h, 150);
    return { x, y: b.y, floor, ball: b };
  }
  // Coordinating: little nods toward whoever it is talking to.
  const nods = [...LOOKS.map(([at]) => at), 52.3, 52.75];
  const h = hops(t, nods, 0.34);
  const b = stand(WUU, CENTER.x, CENTER.floor, WUU_R, { lift: h.lift * 0.4, sx: h.sx, sy: h.sy }, 60);
  return { x: CENTER.x, y: b.y, floor: CENTER.floor, ball: b };
}

// Whom Wuu is attending to during the team build: [time, session index].
const LOOKS: [number, number][] = [
  [HAND_OUT, 0], [HAND_OUT + 0.2, 3], [44.3, 1], [44.7, 4], [45.1, 2], [45.45, 5],
  [STUCK + 0.3, 1], [46.2, 0], [CATCH, 1], [47.7, 3], [48.1, 5], [48.5, 2], [48.85, 4], [49.3, 3], [49.75, 1],
];

function attending(t: number) {
  let s = -1;
  for (const [at, i] of LOOKS) if (t >= at) s = i;
  return s;
}

function drawWuu(ctx: Ctx, t: number) {
  const pose = wuuPose(t);
  if (pose.ball.alpha === 0) return;
  let face = { yaw: -0.25, pitch: 0.05 };
  if (t < 31.9) face = { yaw: -0.1, pitch: 0.05 };
  else if (t < 32.5) face = look(pose.x, pose.y, BOARD.x + BOARD.w / 2, BOARD.y + 200, 1.2);
  else if (t < 33.5) face = look(pose.x, pose.y, ROCKET.x, ROCKET.y - 100, 1.2);
  else if (t < 36.9) face = look(pose.x, pose.y, ROCKET.x + 40, PAD.y - 30, 1.4);
  else if (t < 37.3) face = { yaw: -0.2, pitch: -0.35 };
  else if (t < IDEA) face = { yaw: keys(t, [[37.3, -0.2], [37.45, -0.75], [37.62, -0.75], [37.72, 0.7], [37.85, 0.7], [37.97, 0]]), pitch: 0.05 };
  else if (t < 40.6) face = { yaw: 0, pitch: t < HEADSET_LAND ? 0.4 * seg(t, 38.35, 38.6) * (1 - seg(t, 38.9, 39.0)) : 0.05 };
  else if (t < HAND_OUT) face = { yaw: 0, pitch: 0.05 };
  else if (t < 52.2) {
    const s = attending(t);
    const [cx, cy] = slotCenter(s);
    const [wx, wy] = toScreen(CAM_TEAM, pose.x, pose.y);
    face = look(wx, wy, cx, cy, 1.25);
  } else face = { yaw: 0, pitch: 0.1 };
  const marks = Math.max(
    outCubic(seg(t, IDEA, IDEA + 0.3)) * (1 - seg(t, 38.5, 38.8)),
    seg(t, 39.55, 39.8) * (1 - seg(t, 40.4, 40.7)),
    seg(t, 52.2, 52.5),
  );
  const sweat = seg(t, 34.6, 34.9) * (1 - seg(t, IDEA, IDEA + 0.2));
  const gear = t > 38.35 ? "headset" : undefined;
  const gearY = t < HEADSET_LAND ? -700 * (1 - outCubic(seg(t, 38.35, HEADSET_LAND))) ** 2 : 0;
  drawBall(ctx, { ...pose.ball, ...face, eyes: eyes(t, EXPRESSION, BLINKS), marks, sweat, gear, gearY });
}

// ---------------------------------------------------------------------------
// Friends: peeking, gathering, and waiting for the launch

/** Codex peeks over the top of the blueprint, so it is drawn behind the board. */
function drawCodexPeek(ctx: Ctx, t: number) {
  if (t < TEAM[1].peek.at || t >= ARRIVE[1]) return;
  drawMember(ctx, 1, t, ...memberGround(1, t));
}

/** Where a member stands on the field (x, floor) before the sessions open, or after they close. */
function memberGround(i: number, t: number): [number, number] {
  const m = TEAM[i];
  if (t >= WINDOWS_CLOSE) return [m.lineup.x, m.lineup.floor];
  if (t < ARRIVE[i]) {
    const e = outBack(seg(t, m.peek.at, m.peek.at + 0.45));
    return [lerp(m.peek.from[0], m.peek.to[0], e), lerp(m.peek.from[1], m.peek.to[1], e)];
  }
  return [m.lineup.x, m.lineup.floor];
}

function memberFace(t: number, x: number, y: number) {
  const pose = wuuPose(t);
  if (t > IGNITION - 0.8) return { yaw: (ROCKET.x - x) / 1400, pitch: 0.45 };
  if (t > 40.8 && t < JUMP_IN) return { yaw: 0, pitch: 0.05 };
  return look(x, y, pose.x, pose.y, 1.3);
}

function memberEyes(i: number, t: number): EyeKind {
  if (t > IGNITION && t < IGNITION + 0.5) return "squeeze";
  if (t > IGNITION + 0.5) return "happy";
  if (t > IDEA && t < IDEA + 0.5) return "wide";
  if (t >= ARRIVE[i] + ARRIVE_TIME && t < ARRIVE[i] + ARRIVE_TIME + 0.5) return "happy";
  return i === 1 && t < ARRIVE[i] ? "wide" : "open";
}

function drawMember(ctx: Ctx, i: number, t: number, x: number, floor: number, extra: Partial<Ball> = {}) {
  const m = TEAM[i];
  const h = hop(t, LIFTOFF + 0.15 + i * 0.07, 0.6);
  const b = agent(m.agent, x, floor, m.r, h, 24);
  const face = memberFace(t, b.x, b.y);
  drawBall(ctx, {
    ...b, ...face, sway: Math.sin(t * 2 + i) * 0.08,
    eyes: eyes(t, [[0, memberEyes(i, t)]], [37.9 + i * 0.13, 41.2 + i * 0.1, 55.0 + i * 0.1]), ...extra,
  });
}

function drawTeamOnGround(ctx: Ctx, t: number) {
  for (let i = 0; i < TEAM.length; i++) {
    if (i === 1 && t < ARRIVE[i]) continue;
    const m = TEAM[i];
    // Before the sessions open: peeking, then hopping into the lineup.
    if (t < JUMP_IN + i * 0.1) {
      if (t < m.peek.at && t < ARRIVE[i]) continue;
      if (t < ARRIVE[i]) {
        const [x, floor] = memberGround(i, t);
        drawMember(ctx, i, t, x, floor);
        continue;
      }
      const p = seg(t, ARRIVE[i], ARRIVE[i] + ARRIVE_TIME);
      const [x0, f0] = [m.peek.at < 90 ? m.peek.to[0] : m.peek.from[0], m.peek.at < 90 ? m.peek.to[1] : m.peek.from[1]];
      if (p < 1) {
        const [x, f] = arc(inOut(p), x0, f0, m.lineup.x, m.lineup.floor, 180);
        drawShadow(ctx, x, lerp(f0, m.lineup.floor, inOut(p)), m.r * extent(m.agent.skin).rx, Math.sin(p * Math.PI));
        drawMember(ctx, i, t, x, f, { ground: undefined, sx: 0.94, sy: 1.08 });
      } else drawMember(ctx, i, t, m.lineup.x, m.lineup.floor);
      continue;
    }
    // After the sessions close: back on the field for the launch.
    if (t > WINDOWS_CLOSE + i * 0.06) {
      const p = seg(t, WINDOWS_CLOSE + i * 0.06, WINDOWS_CLOSE + i * 0.06 + 0.6);
      if (p < 1) continue; // still flying; drawn by the session layer in screen space
      drawMember(ctx, i, t, m.lineup.x, m.lineup.floor);
    }
  }
}

// ---------------------------------------------------------------------------
// Sessions: one window per friend, all running at once

function slotCenter(i: number): [number, number] {
  if (i < 0) return [W / 2, H / 2];
  return [SLOT[i].x + SW / 2, SLOT[i].y + SH / 2];
}

function windowScale(i: number, t: number) {
  const open = outBack(seg(t, WINDOWS_OPEN + i * 0.07, WINDOWS_OPEN + i * 0.07 + 0.35));
  const close = inCubic(seg(t, WINDOWS_CLOSE + i * 0.06 + 0.15, WINDOWS_CLOSE + i * 0.06 + 0.45));
  return open * (1 - close);
}

/** Line anchor on each window's inner edge, and on Wuu's headset. */
function lineEnds(i: number, t: number): [[number, number], [number, number]] {
  const s = SLOT[i];
  const end: [number, number] = [i < 3 ? s.x + SW : s.x, s.y + SH / 2];
  const pose = wuuPose(t);
  const [wx, wy] = toScreen(CAM_TEAM, pose.x, pose.y - WUU_R * 0.3);
  return [[wx + (i < 3 ? -30 : 30), wy], end];
}

function creatureScreen(i: number): [number, number] {
  const s = SLOT[i];
  return [s.x + SLOT_CREATURE.x, s.y + TITLE_BAR + SLOT_CREATURE.floor];
}

function drawSessions(ctx: Ctx, t: number, cam: Camera) {
  if (t < WINDOWS_OPEN || t > WINDOWS_CLOSE + 1.2) return;
  // Connection lines from Wuu to every session, with tasks and pings along them.
  for (let i = 0; i < TEAM.length; i++) {
    const draw = inOut(seg(t, LINES + i * 0.05, LINES + i * 0.05 + 0.4)) * (1 - seg(t, WINDOWS_CLOSE, WINDOWS_CLOSE + 0.3));
    if (draw <= 0) continue;
    const [[x0, y0], [x1, y1]] = lineEnds(i, t);
    const stuck = i === 1 && t > STUCK && t < CATCH;
    ctx.save();
    ctx.strokeStyle = stuck ? CORAL : "rgba(70, 80, 70, 0.2)";
    ctx.lineWidth = stuck ? 3 : 2;
    ctx.lineCap = "round";
    ctx.beginPath();
    ctx.moveTo(x0, y0);
    ctx.lineTo(lerp(x0, x1, draw), lerp(y0, y1, draw));
    ctx.stroke();
    ctx.restore();
    // Pings travel outward whenever Wuu checks in on a session.
    for (const [at, s] of LOOKS) {
      if (s !== i) continue;
      const p = seg(t, at, at + 0.45);
      if (p > 0 && p < 1) {
        ctx.fillStyle = TEAM[i].agent.skin.body;
        ctx.beginPath();
        ctx.arc(lerp(x0, x1, p), lerp(y0, y1, p), 7, 0, Math.PI * 2);
        ctx.fill();
        ctx.strokeStyle = "#FFFFFF";
        ctx.lineWidth = 3;
        ctx.stroke();
      }
    }
  }
  for (let i = 0; i < TEAM.length; i++) drawSession(ctx, i, t);
  drawHandOff(ctx, t, cam);
  drawOrbToss(ctx, t);
  drawJumps(ctx, t, cam);
}

function drawSession(ctx: Ctx, i: number, t: number) {
  const scale = windowScale(i, t);
  if (scale <= 0) return;
  const m = TEAM[i];
  const s = SLOT[i];
  const stuck = i === 1 && t > STUCK && t < CATCH;
  // These sessions now share one host; identity lives in the cast and engine marks.
  const theme = THEMES.wuu;
  drawWindow(ctx, { x: s.x, y: s.y, w: SW, h: SH, theme, engine: m.agent.engine, scale }, (c, w, h) => {
    const piece = m.piece;
    const arrived = t > HAND_OUT + i * 0.1 + 0.6;
    const building = arrived ? progress(piece, t) : 0;
    // The piece sits on a small blueprint card while it is being made.
    c.fillStyle = "rgba(126,147,135,0.07)";
    c.beginPath();
    c.roundRect(SLOT_WORK.x - 110, 16, 220, h - 32, 18);
    c.fill();
    if (arrived && t < RETURN[piece]) {
      c.save();
      c.translate(SLOT_WORK.x, SLOT_WORK.y);
      c.scale(SLOT_WORK.s, SLOT_WORK.s);
      const [px, py] = pieceCenter(piece);
      c.translate(-px, -py);
      drawPiece(c, piece, building, { ghost: "#7E9387" });
      c.restore();
    }
    // Progress bar.
    c.fillStyle = theme.text;
    c.beginPath();
    c.roundRect(200, h - 22, 230, 9, 4.5);
    c.fill();
    c.fillStyle = stuck ? CORAL : m.agent.skin.body;
    c.beginPath();
    c.roundRect(200, h - 22, Math.max(9, 230 * building), 9, 4.5);
    c.fill();
    drawCheck(c, w - 40, 26, 17, seg(t, DONE[piece], DONE[piece] + 0.5));
    // The worker.
    if (t >= JUMP_IN + i * 0.1 + 0.55 && t < WINDOWS_CLOSE + i * 0.06) drawWorker(c, i, t);
  });
}

function drawWorker(ctx: Ctx, i: number, t: number) {
  const m = TEAM[i];
  const piece = m.piece;
  const working = t > HAND_OUT + 0.6 && t < DONE[piece];
  const stuck = i === 1 && t > STUCK && t < CATCH;
  const done = t >= DONE[piece];
  const land = squash(t, JUMP_IN + i * 0.1 + 0.55, 0.25, 0.45);
  let h: Hop = { lift: 0, sx: land.sx, sy: land.sy };
  if (working && !stuck) {
    const beat = Math.sin(t * 5 + i * 1.3);
    h = { lift: 0, sx: 1 - beat * 0.012, sy: 1 + beat * 0.015 };
  }
  if (done) h = hop(t, DONE[piece] + 0.05, 0.55);
  if (i === 0 && t > TOSS - 0.3 && t < TOSS + 0.3) h = hop(t, TOSS - 0.3, 0.6);
  const b = agent(m.agent, SLOT_CREATURE.x, SLOT_CREATURE.floor, SLOT_CREATURE.r, h, 36);
  let kind: EyeKind = done ? "happy" : "open";
  if (stuck) kind = "flat";
  if (i === 1 && t >= CATCH && t < CATCH + 0.5) kind = "wide";
  if (done && t > DONE[piece] + 0.6 && t < 52.2) kind = "content";
  const face = working ? { yaw: 0.55, pitch: -0.12 } : { yaw: 0.1, pitch: 0.1 };
  if (i === 0 && t > 46.2 && t < CATCH) Object.assign(face, { yaw: 0.2, pitch: -0.3 });
  drawBall(ctx, { ...b, ...face, eyes: { kind, open: 1 }, sway: Math.sin(t * 2 + i) * 0.08 });
  if (stuck) {
    drawBubble(ctx, SLOT_CREATURE.x + 56, SLOT_CREATURE.floor - SLOT_CREATURE.r * 2 - 10, outBack(seg(t, STUCK + 0.2, STUCK + 0.45)), t, "#FFFFFF", INK);
  }
}

/** Pieces travel along the lines: out as tasks, home as finished parts. */
function drawHandOff(ctx: Ctx, t: number, cam: Camera) {
  TEAM.forEach((m, i) => {
    const id = m.piece;
    const [rcx, rcy] = pieceCenter(id);
    const [rx, ry] = toScreen(cam, ROCKET.x + rcx * ROCKET.s, ROCKET.y + rcy * ROCKET.s);
    const rs = ROCKET.s * cam.zoom;
    const [wx, wy] = [SLOT[i].x + SLOT_WORK.x, SLOT[i].y + TITLE_BAR + SLOT_WORK.y];
    const out = seg(t, HAND_OUT + i * 0.1, HAND_OUT + i * 0.1 + 0.6);
    const back = seg(t, RETURN[id], RETURN[id] + RETURN_TIME);
    let p: number, build: number;
    if (out > 0 && out < 1) [p, build] = [inOut(out), progress(id, t)];
    else if (back > 0 && back < 1) [p, build] = [1 - inOut(back), 1];
    else return;
    const [x, y] = arc(p, rx, ry, wx, wy, 120);
    const s = lerp(rs, SLOT_WORK.s, p);
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(Math.sin(p * Math.PI) * (i < 3 ? -0.5 : 0.5));
    ctx.scale(s, s);
    ctx.translate(-rcx, -rcy);
    drawPiece(ctx, id, build, { ghost: "#7E9387" });
    ctx.restore();
  });
}

function drawOrbToss(ctx: Ctx, t: number) {
  const p = seg(t, TOSS, CATCH);
  if (p <= 0 || p >= 1) return;
  const [x0, y0] = creatureScreen(0);
  const [x1, y1] = creatureScreen(1);
  const e = inOut(p);
  // A friendly lob that arcs out between the windows and back.
  const cx = 640, cy = (y0 + y1) / 2 - 60;
  const sx = y0 - SLOT_CREATURE.r * 1.3, ex = y1 - SLOT_CREATURE.r * 1.1;
  const x = (1 - e) * (1 - e) * (x0 + 40) + 2 * (1 - e) * e * cx + e * e * (x1 + 30);
  const y = (1 - e) * (1 - e) * sx + 2 * (1 - e) * e * cy + e * e * ex;
  ctx.fillStyle = MINT;
  ctx.beginPath();
  ctx.arc(x, y, 16, 0, Math.PI * 2);
  ctx.fill();
}

/** Friends hop from the field into their sessions, and back out for the launch. */
function drawJumps(ctx: Ctx, t: number, cam: Camera) {
  TEAM.forEach((m, i) => {
    const [gx, gfloor] = toScreen(cam, m.lineup.x, m.lineup.floor);
    const [sx, sfloor] = creatureScreen(i);
    const rIn = SLOT_CREATURE.r, rOut = m.r * cam.zoom;
    const inP = seg(t, JUMP_IN + i * 0.1, JUMP_IN + i * 0.1 + 0.55);
    const outP = seg(t, WINDOWS_CLOSE + i * 0.06, WINDOWS_CLOSE + i * 0.06 + 0.6);
    let p: number;
    if (inP > 0 && inP < 1) p = inOut(inP);
    else if (outP > 0 && outP < 1) p = 1 - inOut(outP);
    else return;
    const [x, floor] = arc(p, gx, gfloor, sx, sfloor, 200);
    const r = lerp(rOut, rIn, p);
    const b = agent(m.agent, x, floor, r);
    drawBall(ctx, { ...b, ground: undefined, eyes: { kind: "happy", open: 1 }, sx: 0.94, sy: 1.08, tilt: Math.sin(p * Math.PI) * (i < 3 ? -14 : 14) });
  });
}

// ---------------------------------------------------------------------------
// Time

function drawTimeClock(ctx: Ctx, t: number) {
  const pop = outBack(seg(t, 32.9, 33.2)) * (1 - inCubic(seg(t, 53.4, 53.7)));
  if (pop <= 0) return;
  // Hours race by during the solo build, and barely move with the team.
  const hours = keys(t, [[33.2, 9], [36.6, 23, linear], [IDEA + 0.1, 23], [IDEA + 0.9, 33], [44.2, 33], [52.0, 34.3, linear]]);
  ctx.save();
  ctx.translate(960, 116);
  ctx.scale(pop, pop);
  drawClock(ctx, 0, 0, 52, hours);
  ctx.restore();
}
