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
// Keep the mascot's *authored* eyes as long portrait capsules, symmetric and
// upright. `WuuMascot` can reshape that pair on top (round, squint, wink, and
// so on) without adding new marks, but the identity geometry stays anchored
// here.
export const WUU_MASCOT_TRAITS = {
  shape: 0.2,
  "body.ratio": 0.5,
  "body.n": 1 / 6,
  "eye.ratio": 1,
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
