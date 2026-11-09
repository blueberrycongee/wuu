import { useEffect, useRef, useSyncExternalStore } from "react";

export interface CanvasArtwork {
  /** A processed user picture, or a plugin skin image URL. */
  source: Blob | string;
  dots?: { pitch: number; size: number; shape?: "round" | "square" };
  overlay?: string;
  /** Cover-crop alignment in [0, 1] on each axis. */
  align: readonly [number, number];
  /** A string source can die when its asset cache evicts it; reload it. */
  onError?: () => void;
}

// AppBackground resolves the selected skin; the pane only paints it.
let published: CanvasArtwork | null = null;
const listeners = new Set<() => void>();
export function publishCanvasArtwork(artwork: CanvasArtwork | null): void {
  published = artwork;
  for (const listener of listeners) listener();
}
function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

type Picture = { image: CanvasImageSource; width: number; height: number; close?: () => void };
async function decode(source: Blob | string): Promise<Picture> {
  if (source instanceof Blob) {
    const bitmap = await createImageBitmap(source);
    return { image: bitmap, width: bitmap.width, height: bitmap.height, close: () => bitmap.close() };
  }
  // An element keeps SVG artwork vector until it is drawn at its final size.
  const image = new Image();
  image.src = source;
  await image.decode();
  return { image, width: image.naturalWidth, height: image.naturalHeight };
}

/** Paints canvas-placement artwork behind the main pane, cropped to the window
    so it stays still while sidebars resize the pane. A dot screen samples one
    color per cell on whole device pixels, so every cell has the same footprint. */
export function ShellCanvasArtwork(): JSX.Element | null {
  const artwork = useSyncExternalStore(subscribe, () => published);
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas || !artwork) return;
    let picture: Picture | undefined;
    let cells: { key: string; canvas: OffscreenCanvas } | undefined;
    let frame = 0;
    let disposed = false;
    // The exact device-pixel size avoids resampling a fractional CSS box,
    // which would blur the cell edges.
    let deviceSize: readonly [number, number] | undefined;
    const paint = () => {
      frame = 0;
      const context = canvas.getContext("2d");
      if (!picture || !context) return;
      const ratio = window.devicePixelRatio || 1;
      const rect = canvas.getBoundingClientRect();
      const [width, height] = deviceSize ?? [Math.round(rect.width * ratio), Math.round(rect.height * ratio)];
      if (!width || !height) return;
      if (canvas.width !== width) canvas.width = width;
      if (canvas.height !== height) canvas.height = height;
      const viewWidth = Math.round(window.innerWidth * ratio), viewHeight = Math.round(window.innerHeight * ratio);
      const scale = Math.max(viewWidth / picture.width, viewHeight / picture.height);
      const drawWidth = picture.width * scale, drawHeight = picture.height * scale;
      const left = (viewWidth - drawWidth) * artwork.align[0], top = (viewHeight - drawHeight) * artwork.align[1];
      const offsetX = -Math.round(rect.left * ratio), offsetY = -Math.round(rect.top * ratio);
      context.globalCompositeOperation = "source-over";
      context.clearRect(0, 0, width, height);
      const dots = artwork.dots;
      const pitch = dots ? Math.max(2, Math.round(dots.pitch * ratio)) : 1;
      if (!dots) {
        context.imageSmoothingEnabled = true;
        context.imageSmoothingQuality = "high";
        context.drawImage(picture.image, left + offsetX, top + offsetY, drawWidth, drawHeight);
      } else {
        const columns = Math.ceil(viewWidth / pitch), rows = Math.ceil(viewHeight / pitch);
        const key = `${columns}:${rows}:${pitch}`;
        if (cells?.key !== key) {
          const sampled = new OffscreenCanvas(columns, rows);
          const sampler = sampled.getContext("2d")!;
          sampler.imageSmoothingQuality = "high";
          sampler.drawImage(picture.image, left / pitch, top / pitch, drawWidth / pitch, drawHeight / pitch);
          cells = { key, canvas: sampled };
        }
        context.imageSmoothingEnabled = false;
        context.drawImage(cells.canvas, offsetX, offsetY, columns * pitch, rows * pitch);
      }
      if (artwork.overlay) {
        context.fillStyle = artwork.overlay;
        context.fillRect(0, 0, width, height);
      }
      if (dots) {
        const size = Math.min(pitch, Math.max(1, Math.round(dots.size * ratio)));
        const tile = new OffscreenCanvas(pitch, pitch);
        const shape = tile.getContext("2d")!;
        if (dots.shape === "square") {
          const inset = Math.floor((pitch - size) / 2);
          shape.fillRect(inset, inset, size, size);
        } else {
          shape.beginPath();
          shape.arc(pitch / 2, pitch / 2, size / 2, 0, Math.PI * 2);
          shape.fill();
        }
        // Keep only the cells; the pane's skin background shows in the gaps.
        const pattern = context.createPattern(tile, "repeat")!;
        pattern.setTransform(new DOMMatrix([1, 0, 0, 1, offsetX, offsetY]));
        context.globalCompositeOperation = "destination-in";
        context.fillStyle = pattern;
        context.fillRect(0, 0, width, height);
        context.globalCompositeOperation = "source-over";
      }
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(paint); };
    void decode(artwork.source).then(decoded => {
      if (disposed) decoded.close?.();
      else { picture = decoded; schedule(); }
    }, () => artwork.onError?.());
    const observer = new ResizeObserver(([entry]) => {
      const box = entry?.devicePixelContentBoxSize?.[0];
      if (box) deviceSize = [box.inlineSize, box.blockSize];
      schedule();
    });
    try { observer.observe(canvas, { box: "device-pixel-content-box" }); } catch { observer.observe(canvas); }
    // A docked panel opens by moving into view at its final size, which the
    // ResizeObserver never reports; the change in visibility does.
    const visibility = new IntersectionObserver(schedule, { threshold: [0, 0.25, 0.5, 0.75, 1] });
    visibility.observe(canvas);
    window.addEventListener("resize", schedule);
    return () => {
      disposed = true;
      cancelAnimationFrame(frame);
      observer.disconnect();
      visibility.disconnect();
      window.removeEventListener("resize", schedule);
      picture?.close?.();
    };
  }, [artwork]);
  return artwork ? <canvas ref={ref} className="shell-canvas-artwork" aria-hidden="true" /> : null;
}
