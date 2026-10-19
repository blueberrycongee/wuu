import type { AppLocale } from "./protocol";

// Presentation inherited from the workbench, never a second set of preferences.
export interface ActivityPreviewAppearance {
  reducedMotion: boolean;
  fontSize: number;
  fontFamily: string;
  locale: AppLocale;
  surface: string;
  text: string;
  muted: string;
}
