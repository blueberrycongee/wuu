import { isBackgroundRaster, MAX_BACKGROUND_BYTES } from "./process";

self.onmessage = async (event: MessageEvent<{ image: Blob; purpose: "import" | "light" | "dark" }>) => {
  let bitmap: ImageBitmap | undefined;
  try {
    const { image, purpose } = event.data;
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
    if (purpose !== "import") {
      ctx.fillStyle = purpose === "light" ? "#f3f3f4" : "#181819";
      ctx.fillRect(0, 0, width, height);
    }
    ctx.drawImage(bitmap, 0, 0, width, height);
    bitmap.close();
    bitmap = undefined;
    if (purpose !== "import") {
      const sigma = Math.max(1, Math.min(width, height) * 0.025);
      const pad = Math.ceil(sigma * 3);
      const padded = new OffscreenCanvas(width + pad * 2, height + pad * 2);
      const source = padded.getContext("2d");
      if (!source) throw new Error("unavailable");
      // Extend edge pixels before Gaussian convolution so the shell stays
      // opaque at its edges and the native ribbon can share the exact raster.
      const slices = (length: number) => [
        [0, 1, 0, pad], [0, length, pad, length], [length - 1, 1, pad + length, pad],
      ];
      for (const [sx, sw, dx, dw] of slices(width)) {
        for (const [sy, sh, dy, dh] of slices(height)) {
          source.drawImage(canvas, sx, sy, sw, sh, dx, dy, dw, dh);
        }
      }
      ctx.clearRect(0, 0, width, height);
      ctx.filter = `blur(${sigma}px)`;
      ctx.drawImage(padded, -pad, -pad);
      ctx.filter = "none";
      // Keep the original color wherever it already supports shell text.
      // Correct only pixels outside the contrast range in linear RGB.
      const pixels = ctx.getImageData(0, 0, width, height);
      const linear = Array.from({ length: 256 }, (_, value) => {
        const channel = value / 255;
        return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
      });
      for (let i = 0; i < pixels.data.length; i += 4) {
        const rgb = [linear[pixels.data[i]], linear[pixels.data[i + 1]], linear[pixels.data[i + 2]]];
        const luminance = rgb[0] * 0.2126 + rgb[1] * 0.7152 + rgb[2] * 0.0722;
        const target = purpose === "light" ? Math.max(0.24, luminance) : Math.min(0.12, luminance);
        if (target === luminance) continue;
        for (let c = 0; c < 3; c++) {
          const value = purpose === "light"
            ? rgb[c] + (1 - rgb[c]) * (target - luminance) / (1 - luminance)
            : rgb[c] * target / luminance;
          pixels.data[i + c] = 255 * (value <= 0.0031308 ? value * 12.92 : 1.055 * value ** (1 / 2.4) - 0.055);
        }
      }
      ctx.putImageData(pixels, 0, 0);
    }
    self.postMessage({ image: await canvas.convertToBlob({ type: "image/png" }) });
  } catch {
    self.postMessage({ error: true });
  } finally { bitmap?.close(); }
};
