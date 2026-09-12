import { makeBlobatar, type BlobatarOptions } from "./render";
import * as style from "./styles/blob";

export type { BlobatarOptions };
export type { Shape } from "./styles/blob";
export { SHAPES } from "./styles/silhouette";
export type { TraitOverrides } from "./traits";

/**
 * The renderer alone, without the colour and trait utilities the barrel also
 * carries. Import this when all you do is render.
 */
export const blobatar = makeBlobatar(style);

/**
 * The numeric layout for a set of traits, without resolving a palette or
 * rendering. Exposed for callers that need a seed's `shape` in bulk — filtering
 * thousands of seeds down to the rare silhouettes costs a hash and some
 * arithmetic this way, where going through `_layout` would also resolve an
 * OKLCh palette per candidate.
 */
export { layout } from "./styles/blob";
