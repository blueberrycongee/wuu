import { endCard, END, opening, OPENING_END } from "./bookends";
import type { Ctx } from "./cast";
import { deskShot, DESK_END } from "./desk";
import { stageShot, STAGE_END } from "./stage";

export const DURATION = END;

/** Draws the frame at time `t` (seconds). Shots share no state between frames. */
export function renderFilm(ctx: Ctx, t: number) {
  ctx.save();
  if (t < OPENING_END) opening(ctx, t);
  else if (t < DESK_END) deskShot(ctx, t);
  else if (t < STAGE_END) stageShot(ctx, t);
  else endCard(ctx, Math.min(t, END));
  ctx.restore();
}
