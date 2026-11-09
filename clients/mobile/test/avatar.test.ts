// Avatar assignment must be identical to desktop DefaultAvatar.tsx — the
// same participant gets the same hue bucket (and therefore the same
// blobatar, since blobatar itself is deterministic per name) on both ends.
// The pinned values below were computed from the desktop algorithm; if this
// test ever needs regenerating, the two implementations have diverged and
// that is the bug.

import { describe, expect, it } from "vitest";

import { blobatar } from "blobatar";

import {
  avatarHueIndex,
  avatarSeed,
  blobatarSvgToParts,
  fnv1a,
} from "../src/lib/avatar";

describe("fnv1a", () => {
  it("matches the desktop hash for pinned seeds", () => {
    expect(fnv1a("")).toBe(2166136261);
    expect(fnv1a("andy")).toBe(3183330573);
    expect(fnv1a("participant-42")).toBe(3254981055);
    expect(fnv1a("墨白")).toBe(3064423518);
    expect(fnv1a("shitou")).toBe(3280474227);
  });
});

describe("avatar assignment", () => {
  it("hue spans all 12 muted hues", () => {
    expect(avatarHueIndex("andy")).toBe(9);
    expect(avatarHueIndex("participant-42")).toBe(3);
    expect(avatarHueIndex("墨白")).toBe(6);
  });

  it("seeds from id first, then display name", () => {
    expect(avatarSeed("id-1", "Name")).toBe("id-1");
    expect(avatarSeed("  ", "Name")).toBe("Name");
    expect(avatarSeed(undefined, " Name ")).toBe("Name");
    expect(avatarSeed(undefined, undefined)).toBe("");
  });
});

describe("blobatarSvgToParts", () => {
  it("parses the plate, head blob, and eye paths out of a circle-background blobatar", () => {
    const parts = blobatarSvgToParts(blobatar("andy", { background: "circle" }));
    expect(parts.plate).toBeDefined();
    expect(parts.plate!.d).toContain("M100 50");
    expect(parts.plate!.fill).toMatch(/^#[0-9a-f]{6}$/i);
    expect(parts.head.fill).toMatch(/^#[0-9a-f]{6}$/i);
    expect(parts.head.paths).toHaveLength(1);
    expect(parts.eyes.fill).toMatch(/^#[0-9a-f]{6}$/i);
    expect(parts.eyes.paths).toHaveLength(2);
  });

  it("omits the plate when no background is drawn", () => {
    const parts = blobatarSvgToParts(blobatar("andy", { background: false }));
    expect(parts.plate).toBeUndefined();
    expect(parts.head.paths).toHaveLength(1);
    expect(parts.eyes.paths).toHaveLength(2);
  });
});
