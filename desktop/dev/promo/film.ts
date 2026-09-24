import { endCard, END, opening, OPENING_END } from "./bookends";
import { W, H, type Ctx } from "./cast";
import { deskShot, DESK_END } from "./desk";
import { stageShot, STAGE_END } from "./stage";
import { seg, smooth } from "./motion";

export const DURATION = END;
const transition = document.createElement("canvas");
transition.width = W;
transition.height = H;
const transitionContext = transition.getContext("2d")!;

/** Draws the frame at time `t` (seconds). Shots share no state between frames. */
export function renderFilm(ctx: Ctx, t: number) {
  ctx.save();
  if (t < OPENING_END) opening(ctx, t);
  else if (t >= DESK_END - 0.7 && t < DESK_END + 0.3) {
    // Composite whole scenes, not individual primitives, to keep the matched
    // character opaque while only its surroundings change.
    deskShot(ctx, DESK_END - 0.7);
    stageShot(transitionContext, DESK_END);
    ctx.globalAlpha = smooth(seg(t, DESK_END - 0.7, DESK_END + 0.3));
    ctx.drawImage(transition, 0, 0);
  } else if (t < DESK_END) deskShot(ctx, t);
  else if (t < STAGE_END) stageShot(ctx, t);
  else endCard(ctx, Math.min(t, END));
  ctx.restore();
}
