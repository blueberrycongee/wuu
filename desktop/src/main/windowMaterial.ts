export const MAC_WINDOW_VIBRANCY = "under-window" as const;
export const MAC_CLEAR_WINDOW_BACKGROUND = "#00000000";

export type MacWindowVibrancy = typeof MAC_WINDOW_VIBRANCY | null;

export type MacWindowMaterial = {
  backgroundColor: string;
  vibrancy: MacWindowVibrancy;
  /**
   * Put the opaque fill on before removing the blur, and put the blur back
   * before clearing the fill. The other order flashes the desktop through
   * the transparent window.
   */
  backgroundBeforeVibrancy: boolean;
};

/** Live macOS vibrancy resamples on every resize frame. An opaque fill does not. */
export function macWindowMaterial(
  resizing: boolean,
  opaqueFill: string,
): MacWindowMaterial {
  if (resizing) {
    return {
      backgroundColor: opaqueFill,
      vibrancy: null,
      backgroundBeforeVibrancy: true,
    };
  }
  return {
    backgroundColor: MAC_CLEAR_WINDOW_BACKGROUND,
    vibrancy: MAC_WINDOW_VIBRANCY,
    backgroundBeforeVibrancy: false,
  };
}
