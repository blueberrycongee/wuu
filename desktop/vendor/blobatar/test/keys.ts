/**
 * Every trait key `styles/blob.ts` reads, including the indexed families it
 * only reaches for some shapes.
 *
 * Kept as a list rather than derived, because the point of the tests that use
 * it is to sweep the configuration surface as a *caller* sees it — a list
 * scraped from the implementation would agree with the implementation by
 * construction, including where the implementation is wrong.
 */
export const BLOB_KEYS = [
  "shape",
  "hue",
  "tone",
  "gaze.x",
  "gaze.y",
  "eye.rx",
  "eye.ratio",
  "eye.scale",
  "eye.stretch",
  "eye.gap",
  "eye.n",
  "eye.lean",
  "eye.lean2",
  "eye.dy",
];
