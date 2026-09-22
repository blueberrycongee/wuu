import { UserRound } from "./WuuIcons";
import type { JSX } from "react";

/*
 * Shared deterministic palette used by blobatars elsewhere in the desktop.
 * Human participants deliberately do not use it: the neutral person mark
 * below keeps people visually distinct from agent characters.
 */

// Muted agent hues carried forward from the previous avatar palette.
export const AVATAR_HUES = [14, 33, 52, 96, 150, 182, 202, 222, 250, 288, 322, 350] as const;

export const DEFAULT_AVATAR_COUNT = AVATAR_HUES.length;

// FNV-1a over the seed. Stable across sessions and machines for a
// given id.
function fnv1a(seed: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/** Hue bucket in [0, DEFAULT_AVATAR_COUNT) for the seed. */
export function avatarHueIndex(seed: string): number {
  return fnv1a(seed) % DEFAULT_AVATAR_COUNT;
}

/** Neutral fallback for a human participant without an uploaded avatar. */
export function HumanAvatarMark(): JSX.Element {
  return (
    <span className="default-avatar human-avatar-mark" aria-hidden="true">
      <UserRound className="human-avatar-figure" />
    </span>
  );
}
