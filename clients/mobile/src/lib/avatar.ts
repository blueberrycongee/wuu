// Default-avatar assignment, mirrored from the desktop DefaultAvatar.tsx so
// the same participant renders the same blobatar on both ends. Pure logic
// only (no react-native imports) so vitest can cover it without mocks.

import { blobatar } from "blobatar";

// The 12 muted hues the desktop pins blobatar colors to, preserved from the
// old mascot tint palette (--avatar-N in desktop default-avatar.css).
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

// --- React Native rendering ---------------------------------------------------
// RN's <Image> cannot decode SVG, so the blobatar string API output is parsed
// into the parts react-native-svg needs. The serialization is part of
// blobatar's public output: an optional plate <path fill=…> (when a
// background is set), then <g fill=head> holding the blob path (plus any
// petal circles), then <g fill=eye> holding the eye paths.

export interface BlobatarSvgParts {
  plate?: { d: string; fill: string };
  head: { fill: string; paths: string[]; circles: { cx: number; cy: number; r: number }[] };
  eyes: { fill: string; paths: string[] };
}

const PLATE_RE = /<path d="([^"]*)" fill="([^"]*)"\s*\/?>/;
const GROUP_RE = /<g fill="([^"]*)">([\s\S]*?)<\/g>/g;
const PATH_RE = /<path d="([^"]*)"\s*\/?>/g;
const CIRCLE_RE = /<circle cx="([\d.-]+)" cy="([\d.-]+)" r="([\d.-]+)"\s*\/?>/g;

export function blobatarSvgToParts(svg: string): BlobatarSvgParts {
  const parts: BlobatarSvgParts = {
    head: { fill: "", paths: [], circles: [] },
    eyes: { fill: "", paths: [] },
  };
  const plateMatch = svg.match(PLATE_RE);
  if (plateMatch) parts.plate = { d: plateMatch[1], fill: plateMatch[2] };
  const groups = [...svg.matchAll(GROUP_RE)];
  const [headGroup, eyeGroup] = groups;
  if (headGroup) {
    parts.head.fill = headGroup[1];
    parts.head.paths = [...headGroup[2].matchAll(PATH_RE)].map((match) => match[1]);
    parts.head.circles = [...headGroup[2].matchAll(CIRCLE_RE)].map((match) => ({
      cx: Number(match[1]),
      cy: Number(match[2]),
      r: Number(match[3]),
    }));
  }
  if (eyeGroup) {
    parts.eyes.fill = eyeGroup[1];
    parts.eyes.paths = [...eyeGroup[2].matchAll(PATH_RE)].map((match) => match[1]);
  }
  return parts;
}

/** The blobatar SVG parts for a participant, ready for react-native-svg. */
export function avatarSvgParts(id?: string, name?: string): BlobatarSvgParts {
  const seed = avatarSeed(id, name);
  return blobatarSvgToParts(
    blobatar(seed, { hue: AVATAR_HUES[avatarHueIndex(seed)], background: "circle" }),
  );
}
