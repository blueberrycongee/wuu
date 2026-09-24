// Shot 1 (0–5 s): two eyes open in the dark, the lights come on, and Wuu
// strikes its icon pose and says hi.
// Shot 10 (58–64 s): the frame closes around the giant Wuu until it is the
// app icon, with the crew still standing beside it.
import icon from "../../../assets/app-icon-source.json";
import { drawBall, extent, ICON_POSE, WUU } from "./art";
import { eyes, inOut, keys, lerp, mixColor, outBack, outCubic, seg, smooth, squash } from "./motion";
import {
  CREAM, DESK_HOME, fill, H, NIGHT, stand, W,
  withCamera, type Camera, type Ctx,
} from "./cast";
import { drawCrew, giant, STAGE_END } from "./stage";

export const OPENING_END = 5;
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

// The desktop icon's geometry, rendered in the film's flat palette.
const TILE = { x: 660, y: 240, s: 600 };
const ICON = { x: TILE.x + TILE.s * (icon.bodyX / 1024), y: TILE.y + TILE.s * (icon.bodyY / 1024), r: TILE.s * (icon.radius / 1024) };

export function endCard(ctx: Ctx, t: number) {
  fill(ctx, CREAM);
  const f = inOut(seg(t, STAGE_END + 0.2, STAGE_END + 2.2));
  const g = giant(t);
  const k = lerp(1, ICON.r / g.r, f);
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(lerp(0, TILE.x, f), lerp(0, TILE.y, f), lerp(W, TILE.s, f), lerp(H, TILE.s, f), TILE.s * 0.22 * f);
  ctx.fillStyle = mixColor(CREAM, icon.background, f);
  ctx.fill();
  ctx.clip();
  ctx.translate(lerp(g.x, ICON.x, f), lerp(g.y, ICON.y, f));
  ctx.scale(k, k);
  ctx.translate(-g.x, -g.y);
  drawBall(ctx, { ...g, shadow: 1 - f });
  ctx.restore();
  drawCrew(ctx, t);
}
