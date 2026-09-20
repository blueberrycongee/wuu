import { applyBackgroundPixels, isBackgroundRaster, MAX_BACKGROUND_BYTES } from "./process";
import type { BackgroundEffect } from "./preferences";

self.onmessage = async (event: MessageEvent<{ image: Blob; effect: BackgroundEffect; light: boolean }>) => {
  let bitmap: ImageBitmap | undefined;
  try {
    const { image, effect, light } = event.data;
    if (!image.size || image.size > MAX_BACKGROUND_BYTES || !isBackgroundRaster(new Uint8Array(await image.slice(0, 12).arrayBuffer()))) {
      throw new Error("invalid");
    }
    bitmap = await createImageBitmap(image);
    if (bitmap.width * bitmap.height > 64_000_000) throw new Error("invalid");
    const scale = Math.min(1, 2048 / Math.max(bitmap.width, bitmap.height));
    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = new OffscreenCanvas(width, height);
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("unavailable");
    ctx.drawImage(bitmap, 0, 0, width, height);
    bitmap.close();
    bitmap = undefined;
    const pixels = ctx.getImageData(0, 0, width, height);
    if (effect === "ascii") {
      const glyphs = " .:-=+*#%@";
      ctx.clearRect(0, 0, width, height);
      ctx.font = "10px monospace";
      ctx.textBaseline = "top";
      for (let y = 0; y < height; y += 12) for (let x = 0; x < width; x += 8) {
        const i = (Math.min(height - 1, y + 6) * width + Math.min(width - 1, x + 4)) * 4;
        const [r, g, b, a] = pixels.data.subarray(i, i + 4);
        const luma = (r * 0.2126 + g * 0.7152 + b * 0.0722) / 255;
        ctx.fillStyle = `rgba(${r},${g},${b},${a / 255})`;
        ctx.fillText(glyphs[Math.min(9, Math.floor((light ? 1 - luma : luma) * 10))], x, y);
      }
    } else {
      applyBackgroundPixels(pixels.data, width, height, effect, light);
      ctx.putImageData(pixels, 0, 0);
    }
    self.postMessage({ image: await canvas.convertToBlob({ type: "image/png" }) });
  } catch {
    self.postMessage({ error: true });
  } finally { bitmap?.close(); }
};
