import { _layout } from "blobatar";
import { describe, expect, it } from "vitest";
import approvedIcon from "../../../assets/app-icon-source.json";
import { AVATAR_HUES } from "./DefaultAvatar";
import { providerMascotHue, WUU_MASCOT_ACTIVITY_EXPRESSIONS, WUU_MASCOT_ACTIVITY_PROP_LAYOUT } from "./WuuMascot";
import {
  WUU_MASCOT_ACTIVITY_PERSPECTIVES,
  WUU_MASCOT_NAME,
  WUU_MASCOT_TRAITS,
  type WuuMascotActivity,
} from "./wuu-mascot-spec";

describe("vendored mascot geometry", () => {
  const flat = _layout(WUU_MASCOT_NAME, { traits: WUU_MASCOT_TRAITS });
  const forActivity = (activity: WuuMascotActivity) =>
    _layout(WUU_MASCOT_NAME, {
      traits: WUU_MASCOT_TRAITS,
      perspective: WUU_MASCOT_ACTIVITY_PERSPECTIVES[activity],
    });

  it("authors matching long eyes, then foreshortens them across the sphere", () => {
    const read = forActivity("read");

    expect(flat.eyes[1]!.rx).toBeCloseTo(flat.eyes[0]!.rx, 10);
    expect(flat.eyes[1]!.ry).toBeCloseTo(flat.eyes[0]!.ry, 10);
    expect(flat.eyes[1]!.rot).toBeCloseTo(flat.eyes[0]!.rot, 10);
    for (const eye of read.eyes) {
      expect(eye.rx / read.body.rx).toBeGreaterThan(0.055);
    }
    expect(read.eyes[1]!.ry).not.toBeCloseTo(read.eyes[0]!.ry, 3);
  });

  it("derives the capsule eyes from the approved icon and retains their portrait outline in every activity", () => {
    const aspect = approvedIcon.eyeHeight / approvedIcon.eyeWidth;
    for (const eye of flat.eyes) {
      expect(eye.ry / eye.rx).toBeCloseTo(aspect, 6);
      expect(eye.rx / flat.body.rx).toBeCloseTo(approvedIcon.eyeWidth / (approvedIcon.radius * 2), 6);
    }
    for (const [activity, expression] of Object.entries(WUU_MASCOT_ACTIVITY_EXPRESSIONS)) {
      const posed = expression!.bake(flat, expression!.p).l;
      for (const eye of posed.eyes) {
        expect(eye.ry / eye.rx, activity).toBeGreaterThan(aspect * 0.85);
        expect(eye.ry / eye.rx, activity).toBeLessThan(aspect * 1.12);
        expect(Math.abs(eye.rot), activity).toBeLessThan(5);
      }
    }
  });

  it("carries a distinct, naturally projected gaze in every activity", () => {
    const pairCenter = (l: typeof flat) => ({
      x: (l.eyes[0]!.cx + l.eyes[1]!.cx) / 2 - l.body.cx,
      y: (l.eyes[0]!.cy + l.eyes[1]!.cy) / 2 - l.body.cy,
    });
    const flatCenter = pairCenter(flat);

    for (const [activity, perspective] of Object.entries(WUU_MASCOT_ACTIVITY_PERSPECTIVES)) {
      const layout = forActivity(activity as WuuMascotActivity);
      expect(layout.eyes.map((e) => [e.rx, e.ry, e.rot]), activity).not.toEqual(
        flat.eyes.map((e) => [e.rx, e.ry, e.rot]),
      );
      const center = pairCenter(layout);
      // The pair moves the way the perspective names: yaw sideways, pitch
      // vertically (SVG y grows downward, so a positive pitch looks up).
      if (perspective.yaw > 0) expect(center.x, activity).toBeGreaterThan(flatCenter.x);
      if (perspective.yaw < 0) expect(center.x, activity).toBeLessThan(flatCenter.x);
      if (perspective.pitch > 0) expect(center.y, activity).toBeLessThan(flatCenter.y);
      if (perspective.pitch < 0) expect(center.y, activity).toBeGreaterThan(flatCenter.y);
    }
  });

  it("aims the read pose at the open book on the lower right", () => {
    expect(WUU_MASCOT_ACTIVITY_PERSPECTIVES.read).toEqual({ yaw: 12, pitch: -8, strength: 1 });
    expect(WUU_MASCOT_ACTIVITY_PERSPECTIVES.compose.pitch).toBeLessThan(0);
  });

  it("keeps status props off the live eyes on the process-row canvas", () => {
    const authoredRadius: Record<keyof typeof WUU_MASCOT_ACTIVITY_PROP_LAYOUT, number> = {
      thinking: 14,
      search: 10,
      edit: 16,
      command: 16,
      read: 14,
      tool: 13,
    };

    for (const activity of Object.keys(WUU_MASCOT_ACTIVITY_PROP_LAYOUT) as (keyof typeof WUU_MASCOT_ACTIVITY_PROP_LAYOUT)[]) {
      const layout = WUU_MASCOT_ACTIVITY_PROP_LAYOUT[activity];
      const projected = forActivity(activity);
      const propRadius = authoredRadius[activity] * layout.s;
      for (const [index, eye] of projected.eyes.entries()) {
        const liveX = eye.cx;
        const liveY = eye.cy;
        const dist = Math.hypot(layout.x - liveX, layout.y - liveY);
        const eyeRadius = Math.max(eye.rx, eye.ry);
        expect(dist, `${activity} eye ${index}`).toBeGreaterThan(propRadius + eyeRadius * 0.55);
      }
    }
  });

  it("looks toward the draft when composing without a caller-specific pose inversion", () => {
    const idle = forActivity("idle");
    const compose = forActivity("compose");
    const idleY = (idle.eyes[0]!.cy + idle.eyes[1]!.cy) / 2;
    const composeY = (compose.eyes[0]!.cy + compose.eyes[1]!.cy) / 2;
    expect(composeY).toBeGreaterThan(idleY);
  });
});

describe("providerMascotHue", () => {
  it("gives configured providers distinct colours until the palette is exhausted", () => {
    const providers = Array.from({ length: AVATAR_HUES.length + 1 }, (_, index) => `provider-${index + 1}`);
    const firstPalette = providers
      .slice(0, AVATAR_HUES.length)
      .map((provider) => providerMascotHue(provider, providers));

    expect(new Set(firstPalette).size).toBe(AVATAR_HUES.length);
    expect(providerMascotHue(providers.at(-1), providers)).toBe(firstPalette[0]);
  });

  it("normalizes provider names when assigning a configured colour", () => {
    const providers = ["OpenAI", "Anthropic"];

    expect(providerMascotHue(" openai ", providers)).toBe(providerMascotHue("OpenAI", providers));
    expect(providerMascotHue("ANTHROPIC", providers)).not.toBe(providerMascotHue("OpenAI", providers));
  });

  it("uses the next free colour for a provider missing from the catalog", () => {
    const providers = ["OpenAI", "Anthropic"];

    expect(providerMascotHue("custom", providers)).not.toBe(providerMascotHue("OpenAI", providers));
    expect(providerMascotHue("custom", providers)).not.toBe(providerMascotHue("Anthropic", providers));
  });

  it("keeps a deterministic fallback when the provider catalog is unavailable", () => {
    expect(providerMascotHue("custom-provider")).toBe(providerMascotHue("CUSTOM-PROVIDER"));
  });
});
