import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import { PresentationFrameCapture } from "./presentationFrame";

function png(width: number, height: number): Buffer {
  const data = Buffer.alloc(24);
  Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]).copy(data);
  data.write("IHDR", 12); data.writeUInt32BE(width, 16); data.writeUInt32BE(height, 20);
  return data;
}
function fixture() {
  const image = { isEmpty: () => false, getScaleFactors: vi.fn(() => [1]), getSize: vi.fn(() => ({ width: 800, height: 600 })), toPNG: vi.fn(() => png(800, 600)) };
  const contents = Object.assign(new EventEmitter(), { mainFrame: {}, isDestroyed: vi.fn(() => false), capturePage: vi.fn(async () => image) });
  const registered = vi.fn(() => true);
  const capture = new PresentationFrameCapture(registered);
  const event = { sender: contents, senderFrame: contents.mainFrame } as unknown as Parameters<typeof capture.capture>[0];
  return { image, contents, registered, capture, event };
}

describe("ephemeral own-window presentation capture", () => {
  it("coalesces requests, returns bounded local pixels, and retains no completed cache", async () => {
    const f = fixture();
    const first = f.capture.capture(f.event), second = f.capture.capture(f.event);
    expect(second).toBe(first);
    await expect(first).resolves.toEqual({ data: new Uint8Array(png(800, 600)), width: 800, height: 600, scaleFactor: 1 });
    expect(f.contents.capturePage).toHaveBeenCalledTimes(1);
    expect(f.contents.capturePage).toHaveBeenCalledWith();
    expect(f.image.toPNG).toHaveBeenCalledWith({ scaleFactor: 1 });
    await f.capture.capture(f.event);
    expect(f.contents.capturePage).toHaveBeenCalledTimes(2);
    expect(f.contents.listenerCount("did-start-navigation")).toBe(0);
  });

  it("rejects subframes and unregistered windows without capturing", async () => {
    const f = fixture();
    await expect(f.capture.capture({ ...f.event, senderFrame: {} } as typeof f.event)).rejects.toThrow(/main frame/);
    f.registered.mockReturnValue(false);
    await expect(f.capture.capture(f.event)).rejects.toThrow(/main frame/);
    expect(f.contents.capturePage).not.toHaveBeenCalled();
  });

  it.each(["navigation", "unregister", "destroy", "replace-frame"])("discards pixels after %s while capture is pending", async kind => {
    const f = fixture();
    let finish!: (image: typeof f.image) => void;
    f.contents.capturePage.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const result = f.capture.capture(f.event);
    if (kind === "navigation") f.contents.emit("did-start-navigation");
    if (kind === "unregister") f.registered.mockReturnValue(false);
    if (kind === "destroy") f.contents.isDestroyed.mockReturnValue(true);
    if (kind === "replace-frame") f.contents.mainFrame = {};
    finish(f.image);
    await expect(result).resolves.toBeUndefined();
    expect(f.image.toPNG).not.toHaveBeenCalled();
  });

  it("bounds dimensions before encoding and encoded bytes before delivery", async () => {
    const f = fixture();
    f.image.getSize.mockReturnValue({ width: 8193, height: 1 });
    await expect(f.capture.capture(f.event)).resolves.toBeUndefined();
    expect(f.image.toPNG).not.toHaveBeenCalled();
    f.image.getSize.mockReturnValue({ width: 800, height: 600 });
    f.image.toPNG.mockReturnValue(Buffer.alloc(16 * 1024 * 1024 + 1));
    await expect(f.capture.capture(f.event)).resolves.toBeUndefined();
  });

  it("retains Retina density and verifies encoded dimensions rather than trusting logical size", async () => {
    const f = fixture();
    f.image.getScaleFactors.mockReturnValue([1, 2]);
    f.image.toPNG.mockReturnValue(png(1600, 1200));
    await expect(f.capture.capture(f.event)).resolves.toMatchObject({ width: 1600, height: 1200, scaleFactor: 2 });
    expect(f.image.toPNG).toHaveBeenCalledWith({ scaleFactor: 2 });
    f.image.toPNG.mockReturnValue(png(8193, 1200));
    await expect(f.capture.capture(f.event)).resolves.toBeUndefined();
  });

  it("expires a stalled physical capture for every coalesced consumer without starting another", async () => {
    vi.useFakeTimers();
    try {
      const f = fixture();
      let finish!: (image: typeof f.image) => void;
      f.contents.capturePage.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
      const original = f.capture.capture(f.event);
      vi.advanceTimersByTime(121);
      const newer = f.capture.capture(f.event);
      expect(newer).toBe(original);
      expect(f.contents.capturePage).toHaveBeenCalledTimes(1);
      finish(f.image);
      await expect(original).resolves.toBeUndefined();
      await expect(newer).resolves.toBeUndefined();
      expect(f.image.toPNG).not.toHaveBeenCalled();
      f.contents.capturePage.mockResolvedValue(f.image);
      await expect(f.capture.capture(f.event)).resolves.toBeDefined();
      expect(f.contents.capturePage).toHaveBeenCalledTimes(2);
    } finally { vi.useRealTimers(); }
  });

  it("discards a frame that expires during synchronous PNG encoding", async () => {
    vi.useFakeTimers();
    try {
      const f = fixture();
      f.image.toPNG.mockImplementation(() => { vi.advanceTimersByTime(121); return png(800, 600); });
      await expect(f.capture.capture(f.event)).resolves.toBeUndefined();
    } finally { vi.useRealTimers(); }
  });

  it("treats native capture failure as unavailable and releases listeners", async () => {
    const f = fixture();
    f.contents.capturePage.mockRejectedValue(new Error("capture unavailable"));
    await expect(f.capture.capture(f.event)).resolves.toBeUndefined();
    expect(f.contents.listenerCount("did-start-navigation")).toBe(0);
    expect(f.contents.listenerCount("render-process-gone")).toBe(0);
  });
});
