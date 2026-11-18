import { type Ctx } from "./cast";
import { coverShot } from "./cover";
import { crewShot, CREW_END } from "./crew";
import { deskShot, DESK_END } from "./desk";
import { END } from "./timeline";

export const DURATION = END;

/**
 * Draws the frame at time `t` (seconds). Shots share no state between frames,
 * and every change of shot is a hard cut on a cue in `cues.json`.
 */
export function renderFilm(ctx: Ctx, t: number) {
  ctx.save();
  if (t < DESK_END) deskShot(ctx, t);
  else if (t < CREW_END) crewShot(ctx, t);
  else coverShot(ctx, Math.min(t, END));
  ctx.restore();
}
