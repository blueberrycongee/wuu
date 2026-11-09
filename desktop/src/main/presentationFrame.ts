import { BrowserWindow, type IpcMainInvokeEvent, type WebContents } from "electron";
import type { PresentationFrame } from "../shared/protocol";
import { MAX_PRESENTATION_FRAME_EDGE as MAX_EDGE, MAX_PRESENTATION_FRAME_PIXELS as MAX_PIXELS } from "../shared/PresentationFrameLimits";

const MAX_CAPTURE_AGE_MS = 120;
const MAX_PNG_BYTES = 16 * 1024 * 1024;

/** Own-window, in-memory capture only. No caller-supplied target or capture options. */
export class PresentationFrameCapture {
  private readonly pending = new WeakMap<WebContents, Promise<PresentationFrame | undefined>>();
  constructor(private readonly registered: (contents: WebContents) => boolean) {}

  capture(event: Pick<IpcMainInvokeEvent, "sender" | "senderFrame">): Promise<PresentationFrame | undefined> {
    const contents = event.sender;
    const frame = event.senderFrame;
    const valid = () => !contents.isDestroyed() && this.registered(contents) && frame === contents.mainFrame;
    if (!valid()) return Promise.reject(new Error("Presentation capture requires an application main frame"));
    const window = BrowserWindow.fromWebContents(contents);
    if (!window || window.isDestroyed()) return Promise.resolve(undefined);
    const [logicalWidth, logicalHeight] = window.getContentSize();
    const zoom = contents.getZoomFactor();
    if (logicalWidth < 1 || logicalHeight < 1 || !Number.isFinite(zoom) || zoom <= 0) return Promise.resolve(undefined);
    const geometryCurrent = () => {
      if (window.isDestroyed()) return false;
      const [width, height] = window.getContentSize();
      return width === logicalWidth && height === logicalHeight && contents.getZoomFactor() === zoom;
    };
    const existing = this.pending.get(contents);
    if (existing) return existing;
    const started = performance.now();
    const expired = () => performance.now() - started >= MAX_CAPTURE_AGE_MS;
    let replaced = false;
    const invalidate = () => { replaced = true; };
    contents.on("did-start-navigation", invalidate);
    contents.on("render-process-gone", invalidate);
    const pending = (async (): Promise<PresentationFrame | undefined> => {
      try {
        const image = await contents.capturePage();
        if (expired() || replaced || !valid() || !geometryCurrent() || image.isEmpty()) return undefined;
        const factors = image.getScaleFactors().filter(value => Number.isFinite(value) && value >= 1 && value <= 4);
        const scaleFactor = Math.max(1, ...factors);
        const size = image.getSize(scaleFactor);
        const { width, height } = size;
        if (!Number.isSafeInteger(width) || !Number.isSafeInteger(height) || width < 1 || height < 1
          || width > MAX_EDGE || height > MAX_EDGE || width * height > MAX_PIXELS) return undefined;
        const png = image.toPNG({ scaleFactor });
        if (png.byteLength < 24 || png.byteLength > MAX_PNG_BYTES || expired() || replaced || !valid() || !geometryCurrent()) return undefined;
        if (!png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
          || png.toString("ascii", 12, 16) !== "IHDR") return undefined;
        // NativeImage size semantics vary by representation; the encoded PNG is
        // authoritative. Retain physical pixels and expose their actual scale.
        const pixelWidth = png.readUInt32BE(16), pixelHeight = png.readUInt32BE(20);
        if (pixelWidth < 1 || pixelHeight < 1 || pixelWidth > MAX_EDGE || pixelHeight > MAX_EDGE
          || pixelWidth * pixelHeight > MAX_PIXELS) return undefined;
        // capturePage may encode device pixels while NativeImage labels them 1x.
        // Derive CSS pixel density from this owning window, including page zoom.
        const cssScale = pixelWidth / logicalWidth * zoom;
        if (Math.abs(pixelHeight / cssScale - logicalHeight / zoom) > 1) return undefined;
        return { data: new Uint8Array(png), width: pixelWidth, height: pixelHeight, scaleFactor: cssScale };
      } catch {
        // Optional presentation aid: capture failure must not block navigation.
        return undefined;
      } finally {
        contents.removeListener("did-start-navigation", invalidate);
        contents.removeListener("render-process-gone", invalidate);
      }
    })();
    this.pending.set(contents, pending);
    void pending.finally(() => { if (this.pending.get(contents) === pending) this.pending.delete(contents); });
    return pending;
  }
}
