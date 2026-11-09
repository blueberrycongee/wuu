// Default-avatar assignment, mirrored from the desktop DefaultAvatar.tsx so
// the same participant renders the same avatar on both ends. Pure logic only;
// the shared renderer owns presentation.

// The 12 muted hues the desktop pins blobatar colors to, preserved from the
// old mascot tint palette.
export const AVATAR_HUES = [14, 33, 52, 96, 150, 182, 202, 222, 250, 288, 322, 350] as const;

export const DEFAULT_AVATAR_COUNT = AVATAR_HUES.length;

/** FNV-1a 32-bit over UTF-16 code units — identical to the desktop. */
export function fnv1a(seed: string): number {
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

/** Stable identity seed: participant id where available, else the name. */
export function avatarSeed(id?: string, name?: string): string {
  const trimmedId = id?.trim() ?? "";
  if (trimmedId !== "") return trimmedId;
  return name?.trim() ?? "";
}
