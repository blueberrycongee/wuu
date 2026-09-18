import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initializeDesktopPageZoom } from "./DesktopPageZoom";

let dispose: (() => void) | undefined;
beforeEach(() => localStorage.clear());
afterEach(() => { dispose?.(); vi.restoreAllMocks(); });

function mountFrame() {
  let level = 0;
  const frame = {
    getZoomLevel: () => level,
    getZoomFactor: () => 1.2 ** level,
    setZoomLevel: (next: number) => { level = next; },
  };
  dispose = initializeDesktopPageZoom(frame, window);
  return frame;
}

describe("desktop page zoom preference", () => {
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
