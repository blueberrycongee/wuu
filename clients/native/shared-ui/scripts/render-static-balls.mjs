#!/usr/bin/env bun
/** Pre-render AgentAvatarMark-matching idle balls for Android Compose fallback (API 28 / small marks). */
import { blobatar } from "../../../../desktop/vendor/blobatar/src/blobatar.ts";
import { idle as idleExpression } from "../../../../desktop/vendor/blobatar/src/expression.ts";
import { SHAPES } from "../../../../desktop/vendor/blobatar/src/blob.ts";
import approvedIcon from "../../../../assets/app-icon-source.json" assert { type: "json" };
import { writeFileSync, mkdirSync } from "fs";
import { dirname, join } from "path";
import { fileURLToPath } from "url";
import { spawnSync } from "child_process";

const root = join(dirname(fileURLToPath(import.meta.url)), "../../..");
const outDir = join(root, "android/app/src/main/res/drawable-nodpi");
const AVATAR_HUES = [14, 33, 52, 96, 150, 182, 202, 222, 250, 288, 322, 350];
const AGENT_AVATAR_KEYS = Array.from({ length: 9 }, (_, i) => `abstract-${i + 1}`);
const eyeAspect = approvedIcon.eyeHeight / approvedIcon.eyeWidth;
const eyeRadiusRatio = approvedIcon.eyeWidth / (approvedIcon.radius * 2);
const eyeClearance = (approvedIcon.eyeGap - approvedIcon.eyeWidth) / (approvedIcon.radius * 2) - 0.04;
const TRAITS_BASE = {
  shape: 0.1,
  "eye.rx": (eyeRadiusRatio - 0.075) / (0.11 - 0.075),
  "eye.ratio": (eyeAspect - 1.9) / (2.8 - 1.9),
  "eye.gap": (Math.max(0.1, eyeClearance) - 0.1) / (0.24 - 0.1),
  "eye.n": 0, "eye.lean": 0.5,
  "eye.scale": 0.4782608695652174, "eye.stretch": 0.45454545454545453,
  "eye.dy": 0.5, "eye.lean2": 0.5,
};
const expression = {
  ...idleExpression,
  p: { ...idleExpression.p, edx: 0, esx: 1.22, esy: 1.5, esx2: 0, esy2: 0, tilt: 0, tilt2: 0, lock: 1 },
};

mkdirSync(outDir, { recursive: true });
mkdirSync("/tmp/mascot-balls-svg", { recursive: true });
for (let index = 0; index < AGENT_AVATAR_KEYS.length; index++) {
  const key = AGENT_AVATAR_KEYS[index];
  const shape = SHAPES[index % SHAPES.length];
  const hue = AVATAR_HUES[index % AVATAR_HUES.length];
  const svg = blobatar(`agent-avatar:${key}`, {
    size: 128, background: false, hue,
    traits: { ...TRAITS_BASE, shape: shape.trait },
    perspective: { yaw: 0, pitch: 2, strength: 1 },
    expression,
  });
  const svgPath = `/tmp/mascot-balls-svg/${key}.svg`;
  const pngName = `mascot_abstract_${index + 1}.png`;
  writeFileSync(svgPath, svg);
  const r = spawnSync("convert", ["-background", "none", "-density", "288", svgPath, "-resize", "128x128", join(outDir, pngName)], { encoding: "utf8" });
  if (r.status) throw new Error(r.stderr || r.stdout || "convert failed");
  console.log(pngName, shape.id, hue);
}
