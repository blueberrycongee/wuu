// Shot 11 (beats 108–120): the page closes around the giant until it is the
// app icon, the name is pasted under it letter by letter, and the lamp clicks
// off, leaving the icon's eyes in the dark as the film began.
import icon from "../../../assets/app-icon-source.json";
import { drawBall, ICON_POSE } from "./art";
import { inOut, lerp, seg, smooth } from "./motion";
import { fill, H, NIGHT, W, type Ctx } from "./cast";
import { CREW_END, drawCrew, drawJob, drawSeams, giant, GIANT } from "./crew";
import { PAPER, ransom, sheet } from "./paper";
import { at, beat, cue } from "./timeline";

const TILE = { x: 700, y: 118, s: 520 };
const ICON = { x: TILE.x + TILE.s * (icon.bodyX / 1024), y: TILE.y + TILE.s * (icon.bodyY / 1024), r: TILE.s * (icon.radius / 1024) };
const SHRINK = [CREW_END, beat(110.5)];

export function coverShot(ctx: Ctx, t: number) {
  const f = inOut(seg(t, SHRINK[0], SHRINK[1]));
  const k = lerp(1, ICON.r / GIANT.r, f);
  const g = giant(t);
  sheet(ctx, 0, 0, W, H, PAPER);
  // The job sheet closes into the icon tile; the giant's scraps fade to one clean body.
  ctx.save();
  ctx.beginPath();
  ctx.roundRect(lerp(30, TILE.x, f), lerp(24, TILE.y, f), lerp(1860, TILE.s, f), lerp(1032, TILE.s, f), TILE.s * 0.22 * f);
  ctx.clip();
  ctx.fillStyle = icon.background;
  ctx.fill();
  ctx.translate(lerp(GIANT.x, ICON.x, f), lerp(GIANT.y, ICON.y, f));
  ctx.scale(k, k);
  ctx.translate(-GIANT.x, -GIANT.y);
  const fade = smooth(seg(t, SHRINK[0] + beat(0.5), SHRINK[1]));
  ctx.save();
  ctx.globalAlpha *= 1 - fade;
  drawJob(ctx, CREW_END);
  ctx.restore();
  // The giant turns into the icon's pose, and the greeting lands on the last chord.
  const marks = at("marks");
  const pose = { yaw: lerp(0, ICON_POSE.yaw, f), pitch: lerp(0, ICON_POSE.pitch, f), roll: lerp(0, ICON_POSE.roll, f) };
  drawBall(ctx, { ...g, ...pose, marks: seg(t, marks, marks + 0.45), markAngle: icon.fanAngle });
  ctx.save();
  ctx.globalAlpha *= 1 - fade;
  drawSeams(ctx, CREW_END);
  ctx.restore();
  ctx.restore();
  drawCrew(ctx, t);
  ransom(ctx, "wuu", W / 2, 800, 150, t, cue("letter")[0], 88, { stagger: beat(0.5), hold: Infinity, angle: -3 });
  lightsOut(ctx, t, g, f, k);
}

function lightsOut(ctx: Ctx, t: number, g: ReturnType<typeof giant>, f: number, k: number) {
  const off = at("lamp-off");
  if (t < off) return;
  fill(ctx, NIGHT);
  ransom(ctx, "click", W / 2 + 330, 240, 84, t, off, 41, { hold: 0.6, angle: 6 });
  ctx.save();
  ctx.translate(lerp(GIANT.x, ICON.x, f), lerp(GIANT.y, ICON.y, f));
  ctx.scale(k, k);
  ctx.translate(-GIANT.x, -GIANT.y);
  drawBall(ctx, { ...g, bodyAlpha: 0, ground: undefined, yaw: ICON_POSE.yaw, pitch: ICON_POSE.pitch, roll: ICON_POSE.roll });
  ctx.restore();
}
