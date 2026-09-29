import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initializeDesktopPageZoom, type DesktopZoomAction } from "./DesktopPageZoom";

let dispose: (() => void) | undefined;
let zoom: (action: DesktopZoomAction) => void;
beforeEach(() => localStorage.clear());
afterEach(() => { dispose?.(); vi.restoreAllMocks(); vi.useRealTimers(); });

function mountFrame() {
  let level = 0;
  const frame = {
    getZoomLevel: () => level,
    getZoomFactor: () => 1.2 ** level,
    setZoomLevel: (next: number) => { level = next; },
  };
  dispose = initializeDesktopPageZoom(frame, window, listener => {
    zoom = listener;
    return () => {};
  });
  return frame;
}

describe("desktop page zoom preference", () => {
  it("bounds repeated shortcuts, resets to 100%, and persists without waiting for resize", () => {
    const frame = mountFrame();
    zoom("reset");
    expect(frame.getZoomFactor()).toBe(1);
    zoom("in");
    expect(frame.getZoomFactor()).toBeCloseTo(1.1);
    zoom("out");
    expect(frame.getZoomFactor()).toBeCloseTo(1);
    for (let i = 0; i < 40; i++) zoom("out");
    expect(frame.getZoomFactor()).toBeCloseTo(0.5);
    for (let i = 0; i < 40; i++) zoom("in");
    expect(frame.getZoomFactor()).toBeCloseTo(2);
    dispose?.();
    expect(mountFrame().getZoomFactor()).toBeCloseTo(2);
  });

  it.each([[-10, 0.5], [10, 2]])("clamps a previously saved extreme level %s", (level, factor) => {
    localStorage.setItem("wuu.desktop.pageZoomLevel", JSON.stringify(level));
    expect(mountFrame().getZoomFactor()).toBeCloseTo(factor);
  });

  it("replaces the transient readout and dismisses it after the last shortcut", () => {
    vi.useFakeTimers();
    mountFrame();
    zoom("reset");
    expect(document.querySelector('[role="status"]')?.textContent).toBe("100%");
    vi.advanceTimersByTime(1000);
    zoom("in");
    vi.advanceTimersByTime(500);
    expect(document.querySelector('[role="status"]')?.textContent).toBe("110%");
    vi.advanceTimersByTime(1000);
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it.each([0, 1, -1.5])("retains an explicit zoom choice of %s across reloads without changing typography", level => {
    localStorage.setItem("wuu.appearance.v1", JSON.stringify({ codeSize: 18 }));
    document.documentElement.style.setProperty("--conversation-message-font-size", "16px");
    const frame = mountFrame();
    frame.setZoomLevel(level);
    window.dispatchEvent(new Event("resize"));
    dispose?.();
    const restored = mountFrame();
    expect(restored.getZoomLevel()).toBe(level);
    expect(document.documentElement.style.getPropertyValue("--desktop-page-zoom")).toBe(String(1.2 ** level));
    expect(document.documentElement.style.getPropertyValue("--conversation-message-font-size")).toBe("16px");
    expect(JSON.parse(localStorage.getItem("wuu.appearance.v1")!)).toEqual({ codeSize: 18 });
  });

  it("saves a zoom change before navigation even if resize has not arrived", () => {
    const frame = mountFrame();
    frame.setZoomLevel(0);
    window.dispatchEvent(new Event("pagehide"));
    dispose?.();
    expect(mountFrame().getZoomLevel()).toBe(0);
  });

  it.each(["not json", "null", '"0"', "999"])("recovers from malformed saved zoom %s", value => {
    localStorage.setItem("wuu.desktop.pageZoomLevel", value);
    expect(mountFrame().getZoomFactor()).toBeLessThan(1);
  });

  it("keeps zoom usable when preference storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    const frame = mountFrame();
    frame.setZoomLevel(1);
    window.dispatchEvent(new Event("resize"));
    expect(frame.getZoomLevel()).toBe(1);
    expect(document.documentElement.style.getPropertyValue("--desktop-page-zoom")).toBe("1.2");
  });
});
