// Design tokens, inherited from the desktop paper-ink system (desktop
// DESIGN.md): a near-monochrome base with exactly one saturated accent.
// Identity comes from a handful of signature moments (mascot empty states,
// the vermillion accent, the user bubble's tucked corner) — never from
// decorating the base.

import { useColorScheme } from "react-native";

export type Palette = {
  paper: string;
  ink: string;
  inkStrong: string;
  inkSoft: string;
  inkMuted: string;
  inkFaint: string;
  overlay4: string;
  overlay6: string;
  overlay8: string;
  hairline: string;
  accent: string;
  accentPress: string;
  userBubble: string;
  success: string;
  warning: string;
  danger: string;
};

export const lightPalette: Palette = {
  paper: "#ffffff",
  ink: "#1f2328",
  inkStrong: "#111315",
  inkSoft: "#5b6066",
  inkMuted: "#8a8f94",
  inkFaint: "#b0b6bb",
  overlay4: "rgba(31,35,40,0.04)",
  overlay6: "rgba(31,35,40,0.06)",
  overlay8: "rgba(31,35,40,0.08)",
  hairline: "rgba(31,35,40,0.12)",
  accent: "#ff3d00",
  accentPress: "#e53600",
  userBubble: "#e8e8e4",
  success: "#1f9d55",
  warning: "#b8860b",
  danger: "#c0392b",
};

export const darkPalette: Palette = {
  paper: "#1d2024",
  ink: "#e4e6e8",
  inkStrong: "#f2f3f4",
  inkSoft: "#9aa0a6",
  inkMuted: "#7d838a",
  inkFaint: "#5f656c",
  overlay4: "rgba(228,230,232,0.05)",
  overlay6: "rgba(228,230,232,0.08)",
  overlay8: "rgba(228,230,232,0.11)",
  hairline: "rgba(228,230,232,0.14)",
  accent: "#ff5a26",
  accentPress: "#ff6d3f",
  userBubble: "#2a2e33",
  success: "#2fbf6b",
  warning: "#d4a017",
  danger: "#e06050",
};

export function usePalette(): Palette {
  return useColorScheme() === "dark" ? darkPalette : lightPalette;
}

// The empty-state serif moment (desktop --empty-home-title-font). CJK falls
// back to the platform serif family.
export const serifFamily = "serif" as const;
