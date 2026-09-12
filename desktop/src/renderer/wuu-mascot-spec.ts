import approvedIcon from "../../../assets/app-icon-source.json";

// Hero surfaces share the approved app icon’s exact palette.
export const WUU_MASCOT_BRAND_COLORS = {
  head: approvedIcon.bodyColor,
  eye: approvedIcon.eyeColor,
} as const;

/**
 * The mascot's blobatar identity, shared between the component and its test so
 * the render contract can never drift from what the UI actually draws.
 */
export const WUU_MASCOT_NAME = "wuu";
export const WUU_MASCOT_DEFAULT_HUE = 14;
export const WUU_MASCOT_EYE_ASPECT = approvedIcon.eyeHeight / approvedIcon.eyeWidth;
const eyeRadiusRatio = approvedIcon.eyeWidth / (approvedIcon.radius * 2);
// Blobatar measures clearance beyond an eye radius plus a 0.04-body-radius
// safety margin. Keep its minimum clearance when the icon's pair is tighter.
const eyeClearance = (approvedIcon.eyeGap - approvedIcon.eyeWidth) / (approvedIcon.radius * 2) - 0.04;
export const WUU_MASCOT_TRAITS = {
  shape: 0.2,
  "body.ratio": 0.5,
  "body.n": 1 / 6,
  "eye.rx": (eyeRadiusRatio - 0.075) / (0.11 - 0.075),
  "eye.ratio": (WUU_MASCOT_EYE_ASPECT - 1.9) / (2.8 - 1.9),
  "eye.gap": (Math.max(0.1, eyeClearance) - 0.1) / (0.24 - 0.1),
  "eye.n": 0,
  "eye.lean": 0.5,
  // These normalized trait positions resolve both second-eye multipliers to 1.
  "eye.scale": 0.4782608695652174,
  "eye.stretch": 0.45454545454545453,
  "eye.dy": 0.5,
  "eye.lean2": 0.5,
} as const;

export type WuuMascotActivity =
  | "idle"
  | "compose"
  | "thinking"
  | "compact"
  | "search"
  | "edit"
  | "command"
  | "read"
  | "tool";

/**
 * Where the mascot looks in each activity, layered on top of the expression.
 * Yaw looks left/right, pitch up/down. Both static and live rendering project
 * the complete eye contours through this camera after applying the expression.
 * Live changes interpolate angles and update paths without replacing nodes.
 *
 * - idle greets with its gaze lowered toward the composer (or the status text
 *   under the launch view): an invitation, not a stare.
 * - compose lifts its head to face the user the moment a draft exists.
 * - thinking glances up and aside; compact watches the hole descend overhead.
 * - search/edit/command/tool look down into the work unfolding below the row.
 * - read follows the open book carried at the lower-right edge of the body.
 */
export const WUU_MASCOT_ACTIVITY_PERSPECTIVES: Readonly<
  Record<WuuMascotActivity, { yaw: number; pitch: number; strength: number }>
> = {
  idle: { yaw: 8, pitch: -16, strength: 1 },
  compose: { yaw: 0, pitch: 2, strength: 1 },
  thinking: { yaw: 22, pitch: 14, strength: 1 },
  compact: { yaw: -12, pitch: 12, strength: 1 },
  search: { yaw: -16, pitch: -10, strength: 1 },
  edit: { yaw: 14, pitch: -16, strength: 1 },
  command: { yaw: -10, pitch: -12, strength: 1 },
  read: { yaw: 12, pitch: -8, strength: 1 },
  tool: { yaw: 12, pitch: -10, strength: 1 },
};

export const WUU_MASCOT_IDENTITY_PERSPECTIVE =
  WUU_MASCOT_ACTIVITY_PERSPECTIVES.idle;
