import type { BackgroundEffect } from "./preferences";

export const MAX_BACKGROUND_BYTES = 20 * 1024 * 1024;

export function isBackgroundRaster(bytes: Uint8Array): boolean {
  return (bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47)
    || (bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff)
    || (String.fromCharCode(...bytes.slice(0, 4)) === "RIFF" && String.fromCharCode(...bytes.slice(8, 12)) === "WEBP");
}

// Source-space processing keeps resizing and scrolling free of raster work.
// Ordered Bayer dithering and halftone cells preserve the source alpha channel.
export function applyBackgroundPixels(data: Uint8ClampedArray, width: number, height: number, effect: BackgroundEffect, light: boolean): void {
  if (effect === "none" || effect === "ascii") return;
  const source = data.slice();
  const bayer = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];
  const paper = light ? 255 : 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = (y * width + x) * 4;
      if (effect === "dither") {
        const threshold = (bayer[(y % 4) * 4 + x % 4] / 16 - 0.5) * 64;
        for (let c = 0; c < 3; c++) data[i + c] = Math.round((source[i + c] + threshold) / 85) * 85;
      } else if (effect === "scanlines") {
        if (y % 3 === 0) for (let c = 0; c < 3; c++) data[i + c] = source[i + c] * 0.65 + paper * 0.35;
      } else {
        const sx = Math.min(width - 1, Math.floor(x / 6) * 6 + 3);
        const sy = Math.min(height - 1, Math.floor(y / 6) * 6 + 3);
        const sample = (sy * width + sx) * 4;
        const luma = (source[sample] * 0.2126 + source[sample + 1] * 0.7152 + source[sample + 2] * 0.0722) / 255;
        const radius = 3 * Math.sqrt(light ? 1 - luma : luma);
        const coverage = Math.max(0, Math.min(1, radius + 0.5 - Math.hypot(x % 6 - 2.5, y % 6 - 2.5)));
        for (let c = 0; c < 3; c++) data[i + c] = source[i + c] * 0.5 + (source[sample + c] * coverage + paper * (1 - coverage)) * 0.5;
      }
    }
  }
}
