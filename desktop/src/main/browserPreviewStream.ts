import type { BrowserDebuggerHandle } from "./browserHostWindows";
import type { NativePreviewFrame } from "../shared/nativePreview";

const MAX_JPEG_BYTES = 128 * 1024;
const FRAME_INTERVAL_MS = 1_000 / 6;
type Subscriber = { frame(frame: NativePreviewFrame): void; error(message: string): void };

// The browser PiP owns a live view, not encoded pixels. CDP supplies a separate
// JPEG stream without moving the tab, changing its viewport, or taking focus.
export class BrowserPreviewStream {
  private readonly subscribers = new Set<Subscriber>();
  private timer?: ReturnType<typeof setTimeout>;
  private closed = false;
  private sequence = 0;
  private latest?: NativePreviewFrame;
  private lastSentAt = 0;
  private pending?: NativePreviewFrame;
  private readonly acknowledgements: number[] = [];

  constructor(private readonly debuggerHandle: BrowserDebuggerHandle, private readonly onClosed: () => void) {}

  subscribe(frame: Subscriber["frame"], error: Subscriber["error"]): () => void {
    if (this.closed) throw new Error("Browser preview is closed");
    const subscriber = { frame, error };
    this.subscribers.add(subscriber);
    if (this.subscribers.size === 1) this.start();
    else if (this.latest) frame(this.latest);
    return () => {
      this.subscribers.delete(subscriber);
      if (!this.subscribers.size) this.stop();
    };
  }

  stop(message?: string): void {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.timer);
    this.timer = undefined;
    this.pending = undefined;
    this.acknowledgements.length = 0;
    this.latest = undefined;
    this.debuggerHandle.removeListener("message", this.onMessage);
    this.debuggerHandle.removeListener("detach", this.onDetach);
    if (this.debuggerHandle.isAttached()) void this.debuggerHandle.sendCommand("Page.stopScreencast").catch(() => undefined);
    const subscribers = [...this.subscribers];
    this.subscribers.clear();
    this.onClosed();
    if (message) for (const subscriber of subscribers) subscriber.error(message);
  }

  private start(): void {
    this.debuggerHandle.on("message", this.onMessage);
    this.debuggerHandle.on("detach", this.onDetach);
    void this.debuggerHandle.sendCommand("Page.startScreencast", {
      format: "jpeg", quality: 60, maxWidth: 960, maxHeight: 960, everyNthFrame: 1,
    }).catch(error => this.stop(error instanceof Error ? error.message : String(error)));
    // start/stop commands share this debugger's ordered CDP transport. Never
    // send another stop from a late start completion: a newer subscriber may
    // already own the page's screencast by then.
  }

  private readonly onDetach = (): void => this.stop("Browser preview debugger detached");

  private readonly onMessage = (_event: unknown, method: unknown, input: unknown): void => {
    if (this.closed || method !== "Page.screencastFrame") return;
    const params = input as { sessionId?: number; data?: string; metadata?: { timestamp?: number } };
    if (!Number.isInteger(params.sessionId) || typeof params.data !== "string") { this.stop("Invalid browser preview frame"); return; }
    if (params.data.length > Math.ceil(MAX_JPEG_BYTES / 3) * 4) { this.stop("Preview frame exceeds the bandwidth limit"); return; }
    const data = Buffer.from(params.data, "base64");
    const dimensions = jpegDimensions(data);
    if (!dimensions || data.length > MAX_JPEG_BYTES) { this.stop("Invalid browser preview image"); return; }
    // CDP can have several frames already in flight. Retain only their latest
    // JPEG, but release one capture credit per tick. ACKing replaced frames
    // immediately would keep a hidden animation encoding at desktop rates.
    if (this.acknowledgements.length >= 8) { this.stop("Browser capture exceeded its frame window"); return; }
    this.acknowledgements.push(params.sessionId!);
    this.pending = {
      sequence: ++this.sequence, generation: 1, configuration: 1, codec: "jpeg", keyframe: true,
      timestamp: Math.max(0, Math.round((params.metadata?.timestamp ?? Date.now() / 1000) * 1_000_000)),
      ...dimensions, data,
    };
    if (this.timer) return;
    const delay = Math.max(0, FRAME_INTERVAL_MS - (Date.now() - this.lastSentAt));
    if (!delay) this.flush();
    else this.timer = setTimeout(() => this.flush(), delay);
  };

  private flush(): void {
    this.timer = undefined;
    const next = this.pending;
    this.pending = undefined;
    if (this.closed) return;
    this.lastSentAt = Date.now();
    if (next) {
      this.latest = next;
      for (const subscriber of this.subscribers) subscriber.frame(next);
    }
    const sessionID = this.acknowledgements.shift();
    if (sessionID !== undefined) this.ack(sessionID);
    if (this.acknowledgements.length) this.timer = setTimeout(() => this.flush(), FRAME_INTERVAL_MS);
  }

  private ack(sessionID: number): void {
    if (this.closed) return;
    void this.debuggerHandle.sendCommand("Page.screencastFrameAck", { sessionId: sessionID })
      .catch(error => this.stop(error instanceof Error ? error.message : String(error)));
  }
}

// Read only the small JPEG header. Decoding the image in Electron's main
// process would duplicate the phone's work and interrupt chat/agent handling.
function jpegDimensions(data: Buffer): { width: number; height: number } | undefined {
  if (data.length < 4 || data.readUInt16BE(0) !== 0xffd8) return undefined;
  let offset = 2;
  while (offset + 4 <= data.length) {
    if (data[offset++] !== 0xff) return undefined;
    while (data[offset] === 0xff) offset++;
    const marker = data[offset++];
    if (marker === 0xd9 || marker === 0xda || offset + 2 > data.length) return undefined;
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7)) continue;
    const length = data.readUInt16BE(offset);
    if (length < 2 || offset + length > data.length) return undefined;
    if ([0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker)) {
      if (length < 8) return undefined;
      const height = data.readUInt16BE(offset + 3), width = data.readUInt16BE(offset + 5);
      return width > 0 && width <= 960 && height > 0 && height <= 960 ? { width, height } : undefined;
    }
    offset += length;
  }
  return undefined;
}
