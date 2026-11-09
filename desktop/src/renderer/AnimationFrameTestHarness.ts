import { act } from "react";
import { vi } from "vitest";

/** Advance distinct paints without timers or sleeps in renderer behavior tests. */
export function mockAnimationFrames(): { flush: () => Promise<void> } {
  const frames = new Map<number, FrameRequestCallback>();
  let nextFrame = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation(callback => {
    frames.set(++nextFrame, callback);
    return nextFrame;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(id => {
    frames.delete(id);
  });
  return {
    async flush() {
      await act(async () => { await Promise.resolve(); await Promise.resolve(); });
      for (let paint = 0; paint < 4; paint += 1) {
        await act(async () => {
          const callbacks = [...frames.values()];
          frames.clear();
          callbacks.forEach(callback => callback(paint * 16));
        });
      }
    },
  };
}
