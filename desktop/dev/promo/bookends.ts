// Shot 1 (0–5 s): two eyes open in the dark, the lights come on, and Wuu
// strikes its icon pose and says hi.
// Shot 10 (59–64 s): Wuu drops back in, poses, and the world folds into the
// app icon while the team pops up to wave.
import { drawBall, drawSparkle, extent, ICON_POSE, WUU } from "./art";
import { eyes, hop, hops, inOut, keys, lerp, outBack, outCubic, seg, smooth, squash } from "./motion";
import {
  agent, BUILDER, CLAUDE, CODEX, CONFETTI, CREAM, CURSOR, dotGrid, fill, NIGHT, OPENCODE, PI, stand, W, H,
  withCamera, type Camera, type Ctx,
} from "./cast";

export const OPENING_END = 5;
export const END_START = 59;
export const END = 64;

// ---------------------------------------------------------------------------
// Shot 1

const HERO = { x: 960, r: 240 };
const HERO_FLOOR = 560 + HERO.r * extent(WUU).ry;
const LIGHTS = 2.7;

export function opening(ctx: Ctx, t: number) {
  const e = inOut(seg(t, LIGHTS, 4.1));
  // Drift slightly up-left while pulling out so the greeting marks land in frame.
  const cam: Camera = { x: lerp(960, 935, e), y: lerp(560, 505, e), zoom: lerp(1.25, 0.8, e) };
  fill(ctx, NIGHT);
  const lit = seg(t, LIGHTS, 3.5);
  withCamera(ctx, cam, () => {
    if (lit > 0) {
      // The lights come on as a circle opening from Wuu.
      const radius = lerp(HERO.r * 1.08, 2400, outCubic(lit));
      ctx.save();
      ctx.beginPath();
      ctx.arc(HERO.x, 560, radius, 0, Math.PI * 2);
      ctx.clip();
      ctx.fillStyle = CREAM;
      ctx.fillRect(-1000, -1000, 4000, 3000);
      dotGrid(ctx, -600, -400, 2600, 1600);
      ctx.restore();
    }
    const yaw = keys(t, [
      [1.45, 0], [1.72, -0.55], [1.95, -0.55], [2.2, 0.55], [2.5, 0.55], [2.85, 0], [3.5, 0], [4.0, ICON_POSE.yaw, outBack],
    ]);
    const pitch = keys(t, [[1.45, 0.05], [1.72, -0.08], [1.95, -0.08], [2.2, 0.12], [2.5, 0.12], [2.85, 0.05], [3.5, 0.05], [4.0, ICON_POSE.pitch, outBack]]);
    const roll = keys(t, [[3.5, 0], [4.0, ICON_POSE.roll, outBack]]);
    const h = hop(t, 4.35, 0.6);
    const pop = squash(t, 3.95, 0.12, 0.4);
    const b = stand(WUU, HERO.x, HERO_FLOOR, HERO.r, { lift: h.lift, sx: h.sx * pop.sx, sy: h.sy * pop.sy }, 110);
    const look = eyes(t, [[0, "open"], [4.38, "happy"]], [1.05, 2.75]);
    // Eyes first open as slits, then fully; the body stays dark until the lights.
    const wake = seg(t, 0.6, 0.95);
    drawBall(ctx, {
      ...b, yaw, pitch, roll,
      eyes: { ...look, open: Math.min(look.open, lerp(0.08, 1, smooth(wake))) },
      alpha: smooth(seg(t, 0.3, 0.6)),
      bodyAlpha: smooth(seg(t, 2.3, LIGHTS + 0.1)),
      ground: lit > 0 ? HERO_FLOOR : undefined,
      marks: outCubic(seg(t, 3.95, 4.5)),
      markAngle: ICON_POSE.markAngle,
    });
  });
}

// ---------------------------------------------------------------------------
// Shot 10

const LAND = 59.6;
const POSE = 59.9;
const FOLD = 61.1;
const FOLDED = 62.3;
const FALL_R = 210;
const FALL_FLOOR = 900;
// The desktop icon: the approved 1024 artwork in a rounded tile (corner 0.22).
const TILE = { x: 660, y: 240, s: 600 };
const ICON = { x: TILE.x + TILE.s * (718 / 1024), y: TILE.y + TILE.s * (718 / 1024), r: TILE.s * (450 / 1024) };
const WAVERS = [CLAUDE, CODEX, CURSOR, BUILDER, OPENCODE, PI];

export function endCard(ctx: Ctx, t: number) {
  fill(ctx, CREAM);
  dotGrid(ctx, 0, 0, W, H);
  drawConfetti(ctx, t);
  const fold = inOut(seg(t, FOLD, FOLDED));
  const ry = extent(WUU).ry;
  // Falling in from the launch, landing, then folding into the icon.
  const fall = seg(t, END_START, LAND);
  const standY = FALL_FLOOR - FALL_R * ry;
  const land = squash(t, LAND, 0.22, 0.5);
  const breathe = t > FOLDED ? 1 + Math.sin((t - FOLDED) * 2.6) * 0.012 : 1;
  const x = lerp(960, ICON.x, fold);
  const y = lerp(lerp(-FALL_R * 1.4, standY, fall * fall), ICON.y, fold);
  const r = lerp(FALL_R, ICON.r, fold) * breathe;
  const yaw = keys(t, [[POSE, 0], [POSE + 0.5, ICON_POSE.yaw, outBack]]);
  const pitch = keys(t, [[END_START, -0.35], [LAND, -0.35], [LAND + 0.15, 0.1], [POSE, 0.1], [POSE + 0.5, ICON_POSE.pitch, outBack]]);
  const roll = keys(t, [[POSE, 0], [POSE + 0.5, ICON_POSE.roll, outBack]]);
  const tileSize = lerp(1700, TILE.s, fold);
  const tileAlpha = smooth(seg(t, FOLD, FOLD + 0.35));
  const tileX = 960 - tileSize / 2 + (TILE.x + TILE.s / 2 - 960) * fold;
  const tileY = 540 - tileSize / 2 + (TILE.y + TILE.s / 2 - 540) * fold;
  if (tileAlpha > 0) {
    ctx.save();
    ctx.globalAlpha = tileAlpha;
    ctx.shadowColor = "rgba(80, 58, 30, 0.16)";
    ctx.shadowBlur = 60 * fold;
    ctx.shadowOffsetY = 24 * fold;
    ctx.fillStyle = "#FFFFFF";
    ctx.beginPath();
    ctx.roundRect(tileX, tileY, tileSize, tileSize, tileSize * 0.22);
    ctx.fill();
    ctx.restore();
  }
  ctx.save();
  if (tileAlpha > 0) {
    ctx.beginPath();
    ctx.roundRect(tileX, tileY, tileSize, tileSize, tileSize * 0.22);
    ctx.clip();
  }
  drawBall(ctx, {
    x, y, r, skin: WUU, yaw, pitch, roll,
    sx: fall < 1 ? 0.9 : land.sx, sy: fall < 1 ? 1.12 : land.sy,
    eyes: eyes(t, [[END_START, "squeeze"], [LAND + 0.12, "open"]], [60.9, 63.1]),
    ground: fall >= 1 && fold < 1 ? lerp(FALL_FLOOR, ICON.y + ICON.r, fold) : undefined,
    alpha: 1,
    marks: outCubic(seg(t, 60.3, 60.7)),
    markAngle: ICON_POSE.markAngle,
  });
  ctx.restore();
  drawWavers(ctx, t);
  // One last glint on the finished icon.
  const glint = seg(t, FOLDED + 0.2, FOLDED + 0.8);
  if (glint > 0 && glint < 1) drawSparkle(ctx, TILE.x + TILE.s - 70, TILE.y + 70, 30 * Math.sin(glint * Math.PI), "#FFD166");
  // The launch's white flash fades out over the new scene.
  const flash = 1 - seg(t, END_START, END_START + 0.4);
  if (flash > 0) {
    ctx.globalAlpha = smooth(flash);
    fill(ctx, "#FFFDF8");
    ctx.globalAlpha = 1;
  }
}

/** Confetti from the fireworks drifts down over the landing. */
function drawConfetti(ctx: Ctx, t: number) {
  const k = t - END_START;
  const fade = 1 - seg(t, FOLD, FOLD + 0.6);
  if (fade <= 0) return;
  ctx.save();
  ctx.globalAlpha = fade;
  for (let i = 0; i < 46; i++) {
    const x = ((i * 211.7) % (W + 200)) - 100 + Math.sin(k * 2 + i) * 30;
    const y = -60 - ((i * 97.3) % 500) + k * (330 + (i % 5) * 70);
    if (y > H + 40) continue;
    const a = k * (2 + (i % 3)) + i;
    ctx.fillStyle = CONFETTI[i % CONFETTI.length];
    ctx.save();
    ctx.translate(x, y);
    ctx.rotate(a);
    ctx.scale(1, Math.abs(Math.cos(a * 1.3)) * 0.8 + 0.2);
    ctx.beginPath();
    ctx.roundRect(-14, -6, 28, 12, 6);
    ctx.fill();
    ctx.restore();
  }
  ctx.restore();
}

/** The team pops up under the icon to wave goodbye. */
function drawWavers(ctx: Ctx, t: number) {
  WAVERS.forEach((a, i) => {
    const at = FOLDED + 0.05 + i * 0.07;
    const up = outBack(seg(t, at, at + 0.4));
    if (up <= 0) return;
    const x = 960 + (i - 2.5) * 150;
    const floor = 1040 + (1 - up) * 240;
    const h = hops(t, [at + 0.6, at + 1.15], 0.42);
    const b = agent(a, x, floor, 44, h, 34);
    drawBall(ctx, {
      ...b, yaw: (960 - x) / 1600, pitch: 0.35,
      eyes: eyes(t, [[0, "happy"]], []), sway: Math.sin(t * 5 + i) * 0.35,
    });
  });
}
