// Shot 1 (0–5 s): two eyes open in the dark, the lights come on, and Wuu
// strikes its icon pose and says hi.
// Shot 10 (59–64 s): the porthole portrait continues into the app icon.
import icon from "../../../assets/app-icon-source.json";
import { drawBall, extent, ICON_POSE, WUU } from "./art";
import { eyes, inOut, keys, lerp, mixColor, outBack, outCubic, seg, smooth, squash } from "./motion";
import {
  CREAM, DESK_HOME, fill, NIGHT, stand,
  withCamera, type Camera, type Ctx,
} from "./cast";
import { launchPortrait, stageShot } from "./stage";

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
  const handoff = smooth(seg(t, 4.05, OPENING_END));
  // Drift slightly up-left while pulling out so the greeting marks land in frame.
  const cam: Camera = {
    x: lerp(lerp(960, 935, e), 960, handoff),
    y: lerp(lerp(560, 505, e), 540, handoff),
    zoom: lerp(lerp(1.25, 0.8, e), 1, handoff),
  };
  const lit = seg(t, LIGHTS, 3.5);
  fill(ctx, mixColor(NIGHT, CREAM, smooth(lit)));
  withCamera(ctx, cam, () => {
    const yaw = keys(t, [
      [1.45, 0], [1.72, -0.55], [1.95, -0.55], [2.2, 0.55], [2.5, 0.55], [2.85, 0], [3.5, 0], [4.0, ICON_POSE.yaw, outBack],
    ]);
    const pitch = keys(t, [[1.45, 0.05], [1.72, -0.08], [1.95, -0.08], [2.2, 0.12], [2.5, 0.12], [2.85, 0.05], [3.5, 0.05], [4.0, ICON_POSE.pitch, outBack]]);
    const roll = keys(t, [[3.5, 0], [4.0, ICON_POSE.roll, outBack]]);
    const pop = squash(t, 3.95, 0.12, 0.4);
    const floor = lerp(HERO_FLOOR, DESK_HOME.floor, handoff);
    const b = stand(WUU, lerp(HERO.x, DESK_HOME.x, handoff), floor, lerp(HERO.r, DESK_HOME.r, handoff), { lift: 0, ...pop });
    const look = eyes(t, [[0, "open"]], [1.05, 2.75]);
    // Eyes first open as slits, then fully; the body stays dark until the lights.
    const wake = seg(t, 0.6, 0.95);
    drawBall(ctx, {
      ...b, yaw, pitch, roll,
      eyes: { ...look, open: Math.min(look.open, lerp(0.08, 1, smooth(wake))) },
      alpha: smooth(seg(t, 0.3, 0.6)),
      bodyAlpha: smooth(seg(t, 2.3, LIGHTS + 0.1)),
      ground: lit > 0 ? floor : undefined,
      marks: outCubic(seg(t, 3.95, 4.5)),
      markAngle: ICON_POSE.markAngle,
    });
  });
}

// ---------------------------------------------------------------------------
// Shot 10

const POSE = 59.8;
const FOLD = 60.3;
const FOLDED = 62.2;
// The desktop icon's geometry, rendered in the film's flat palette.
const TILE = { x: 660, y: 240, s: 600 };
const ICON = { x: TILE.x + TILE.s * (icon.bodyX / 1024), y: TILE.y + TILE.s * (icon.bodyY / 1024), r: TILE.s * (icon.radius / 1024) };

export function endCard(ctx: Ctx, t: number) {
  if (t < 60) stageShot(ctx, t, false);
  else fill(ctx, CREAM);
  const passenger = launchPortrait(t);
  const fold = inOut(seg(t, FOLD, FOLDED));
  const x = lerp(passenger.x, ICON.x, fold);
  const y = lerp(passenger.y, ICON.y, fold);
  const r = lerp(passenger.r, ICON.r, fold);
  const pose = smooth(seg(t, POSE, POSE + 0.85));
  const tileSize = lerp(880, TILE.s, fold);
  const tileAlpha = smooth(seg(t, FOLD, FOLD + 0.8));
  const tileX = 960 - tileSize / 2;
  const tileY = 540 - tileSize / 2;
  if (tileAlpha > 0) {
    ctx.save();
    ctx.globalAlpha = tileAlpha;
    ctx.fillStyle = icon.background;
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
    x, y, r, skin: WUU, yaw: ICON_POSE.yaw * pose,
    pitch: lerp(passenger.pitch!, ICON_POSE.pitch, pose), roll: ICON_POSE.roll * pose,
    eyes: eyes(t, [[END_START, "open"]], [60.05]),
    marks: smooth(seg(t, 60.4, 61.1)),
    markAngle: ICON_POSE.markAngle,
  });
  ctx.restore();
}
