import { describe, expect, it, vi } from "vitest";
import type { BrowserWindow } from "electron";
import { PopOutCompactMode } from "./popOutCompactMode";

// Recovery must preserve the user's expanded geometry across repeated requests,
// compact resizing, and a renderer reload. A removed display must not strand it.
function windowFixture() {
  let bounds = { x: 100, y: 80, width: 900, height: 700 };
  let maximized = false;
  const win = {
    getBounds: () => ({ ...bounds }),
    getNormalBounds: () => ({ ...bounds }),
    setBounds: vi.fn((next) => { bounds = { ...next }; }),
    isFullScreen: () => false,
    isMaximized: () => maximized,
    unmaximize: () => { maximized = false; },
    maximize: () => { maximized = true; },
  };
  return win as unknown as BrowserWindow;
}
const workArea = { x: 0, y: 0, width: 1440, height: 900 };

describe("pop-out compact geometry recovery", () => {
  it("keeps the first expanded bounds through repeated requests and compact resizing", () => {
    const modes = new PopOutCompactMode();
    const win = windowFixture();
    const original = win.getBounds();
    expect(modes.set(win, true, () => workArea)).toBe(true);
    const compact = win.getBounds();
    expect(compact.width).toBeLessThan(original.width);
    modes.set(win, true, () => workArea);
    win.setBounds({ x: 300, y: 100, width: 460, height: 520 });
    expect(modes.isCompact(win)).toBe(true);
    modes.set(win, false, () => workArea);
    expect(win.getBounds()).toEqual(original);
    expect(modes.isCompact(win)).toBe(false);
    modes.set(win, false, () => workArea);
    expect(win.getBounds()).toEqual(original);
  });

  it("recovers onto the available display after the expanded display disappears", () => {
    const modes = new PopOutCompactMode();
    const win = windowFixture();
    win.setBounds({ x: 1800, y: 100, width: 900, height: 700 });
    modes.set(win, true, () => workArea);
    modes.set(win, false, () => ({ x: 0, y: 0, width: 800, height: 600 }));
    expect(win.getBounds()).toEqual({ x: 0, y: 0, width: 800, height: 600 });
  });

  it("restores maximized presentation without replacing the window", () => {
    const modes = new PopOutCompactMode();
    const win = windowFixture();
    win.maximize();
    modes.set(win, true, () => workArea);
    expect(win.isMaximized()).toBe(false);
    modes.set(win, false, () => workArea);
    expect(win.isMaximized()).toBe(true);
  });

  it("leaves fullscreen geometry untouched so the user can exit fullscreen first", () => {
    const modes = new PopOutCompactMode();
    const win = windowFixture();
    win.isFullScreen = () => true;
    expect(() => modes.set(win, true, () => workArea)).toThrow();
    expect(win.setBounds).not.toHaveBeenCalled();
    expect(modes.isCompact(win)).toBe(false);
  });
});
