// The film and its score share one clock. `cues.json` places every sounding
// event on the beat grid; the shots read their times from here and
// `score/score.py` places a recording on each of them, so picture and sound
// cannot drift apart.
import score from "./cues.json";

export type CueId = keyof typeof score.cues;

export const BEAT = 60 / score.bpm;
export const END = score.beats * BEAT;

/** Seconds at a (fractional) beat from the start of the film. */
export const beat = (n: number) => n * BEAT;

/** Every moment of a cue, in seconds. */
export const cue = (id: CueId): number[] => score.cues[id].at.map(beat);

/** The first (often only) moment of a cue, in seconds. */
export const at = (id: CueId) => beat(score.cues[id].at[0]);

/** How many of a cue's moments have happened by `t`. */
export function count(id: CueId, t: number) {
  let n = 0;
  for (const b of score.cues[id].at) if (beat(b) <= t) n++;
  return n;
}
