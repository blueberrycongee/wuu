import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { BrowserPiPHostReporter, hostLayoutMutationMatters, readBrowserPiPHostLayout } from "./BrowserPiPHostReporter";

describe("browser preview host layout", () => {
  it("publishes resize geometry in the layout frame without waiting for another animation frame", () => {
    document.body.innerHTML = `<div data-pip-anchor-host="conversation" id="host"></div><div id="reporter"></div>`;
    const host = document.getElementById("host")!;
    const report = vi.fn();
    const originalWuu = window.wuu;
    Object.assign(window, { wuu: { reportBrowserPiPHostLayout: report } });
    let onResize!: ResizeObserverCallback;
    const frames = new Map<number, FrameRequestCallback>();
    let frameID = 0;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      frames.set(++frameID, callback);
      return frameID;
    });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: ResizeObserverCallback) { onResize = callback; }
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    const root = createRoot(document.getElementById("reporter")!);
    try {
      viRect(host, { x: 10, y: 20, width: 800, height: 600 });
      act(() => root.render(<BrowserPiPHostReporter />));
      for (const [id, callback] of frames) { frames.delete(id); callback(0); }
      report.mockClear();
      for (let step = 1; step <= 5; step++) {
        const rect = { x: 10, y: 20, width: 800 + step * 20, height: 600 + step * 10 };
        viRect(host, rect);
        window.dispatchEvent(new Event("resize"));
        onResize([], {} as ResizeObserver);
        expect(report).toHaveBeenLastCalledWith({ host: rect, obstacles: [] });
        expect(frames.size).toBe(0);
      }
      expect(report).toHaveBeenCalledTimes(5);
      onResize([], {} as ResizeObserver);
      expect(report).toHaveBeenCalledTimes(5);
      vi.stubGlobal("innerWidth", window.innerWidth + 100);
      window.dispatchEvent(new Event("resize"));
      for (const [id, callback] of frames) { frames.delete(id); callback(0); }
      expect(report).toHaveBeenCalledTimes(6);
    } finally {
      act(() => root.unmount());
      expect(report).toHaveBeenLastCalledWith(null);
      Object.assign(window, { wuu: originalWuu });
      vi.unstubAllGlobals();
    }
  });

  it("reads the conversation column and ignores hidden obstacles", () => {
    document.body.innerHTML = `
      <div data-pip-anchor-host="conversation" id="host"></div>
      <div data-pip-obstacle="composer" id="composer"></div>
      <div data-pip-obstacle="hidden" id="hidden" hidden></div>
    `;
    const host = document.getElementById("host")!;
    const composer = document.getElementById("composer")!;
    viRect(host, { x: 10, y: 20, width: 400, height: 300 });
    viRect(composer, { x: 10, y: 280, width: 400, height: 40 });
    expect(readBrowserPiPHostLayout(document)).toEqual({
      host: { x: 10, y: 20, width: 400, height: 300 },
      obstacles: [{ x: 10, y: 280, width: 400, height: 40 }],
    });
  });

  it("ignores message mutations that do not touch the column or an obstacle", () => {
    document.body.innerHTML = `<div data-pip-anchor-host="conversation" id="host"></div>`;
    const message = document.createElement("p");
    message.textContent = "token";
    const record = { type: "childList", target: document.body, addedNodes: [message], removedNodes: [] } as unknown as MutationRecord;
    expect(hostLayoutMutationMatters([record])).toBe(false);
    const host = document.getElementById("host")!;
    const added = { type: "childList", target: document.body, addedNodes: [host], removedNodes: [] } as unknown as MutationRecord;
    expect(hostLayoutMutationMatters([added])).toBe(true);
  });
});

function viRect(element: HTMLElement, rect: { x: number; y: number; width: number; height: number }): void {
  element.getBoundingClientRect = () => ({
    ...rect,
    top: rect.y,
    left: rect.x,
    right: rect.x + rect.width,
    bottom: rect.y + rect.height,
    toJSON: () => ({}),
  });
}
