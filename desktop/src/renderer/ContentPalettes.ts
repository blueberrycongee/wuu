import type { ExtensionThemeDescriptor } from "../shared/protocol";

export type ContentTheme = ExtensionThemeDescriptor & { key: string; legacyKeys: string[] };

// Color endpoints only: palettes preserve the user's typography and geometry.
// Each mode derives a coordinated surface ladder from its own canvas and ink.
const palettes = [
  { id: "linear", name: "Linear", light: ["#fcfcfd", "#1b1b1b", "#5e6ad2"], dark: ["#0f0f11", "#e3e4e6", "#606acc"] },
  { id: "codex", name: "Codex", light: ["#ffffff", "#0d0d0d", "#0169cc"], dark: ["#111111", "#fcfcfc", "#0169cc"] },
  { id: "absolutely", name: "Absolutely", light: ["#f9f9f7", "#2d2d2b", "#cc7d5e"], dark: ["#2d2d2b", "#f9f9f7", "#cc7d5e"] },
  { id: "github", name: "GitHub", light: ["#ffffff", "#1f2328", "#0969da"], dark: ["#0d1117", "#e6edf3", "#1f6feb"] },
  { id: "notion", name: "Notion", light: ["#ffffff", "#37352f", "#3183d8"], dark: ["#191919", "#d9d9d8", "#3183d8"] },
  { id: "one", name: "One", light: ["#fafafa", "#383a42", "#526fff"], dark: ["#282c34", "#abb2bf", "#4d78cc"] },
] as const;

export function builtInContentThemes(): ContentTheme[] {
  return palettes.flatMap(palette => (["light", "dark"] as const).map(base => {
    const [canvas, ink, accent] = palette[base];
    const mix = (color: string, amount: number) => `color-mix(in srgb, ${color} ${amount}%, ${canvas})`;
    // Choose the higher-contrast endpoint for text on filled accent controls.
    const channels = accent.slice(1).match(/.{2}/g)!.map(value => {
      const channel = parseInt(value, 16) / 255;
      return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
    });
    const luminance = channels[0]! * 0.2126 + channels[1]! * 0.7152 + channels[2]! * 0.0722;
    const onAccent = luminance > 0.179 ? "#000000" : "#ffffff";
    const link = `color-mix(in srgb, ${ink} ${base === "light" ? 35 : 45}%, ${accent})`;
    return {
      id: palette.id,
      key: `builtin:${palette.id}:${base}`,
      legacyKeys: [],
      name: palette.name,
      base,
      tokens: {
        "--wuu-color-canvas": canvas,
        "--wuu-color-text": ink,
        "--wuu-color-text-muted": mix(ink, base === "light" ? 72 : 82),
        "--wuu-color-surface": mix(ink, base === "light" ? 4 : 5),
        "--wuu-color-surface-muted": mix(ink, 9),
        "--wuu-color-surface-elevated": base === "light" ? mix("#ffffff", 55) : mix(ink, 8),
        "--wuu-color-border-subtle": mix(ink, 16),
        "--wuu-color-border-strong": mix(ink, 38),
        "--wuu-color-accent": accent,
        "--wuu-color-focus": accent,
        "--wuu-nav-item-hover-background": mix(ink, base === "light" ? 6 : 9),
        "--wuu-color-on-accent": onAccent,
        "--wuu-color-link": link,
        "--wuu-color-link-hover": link,
        "--wuu-message-user-background": mix(ink, base === "light" ? 7 : 10),
        "--wuu-message-user-color": ink,
      },
    };
  }));
}
