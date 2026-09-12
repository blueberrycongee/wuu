import { EventEmitter } from "node:events";
import type { BrowserWindow, Rectangle } from "electron";
import { describe, expect, it } from "vitest";
import { createSessionInspectorExpansion } from "./sessionInspectorExpansion";

function makeWindow(initial: Partial<Rectangle> = {}) {
  let bounds = { x: 100, y: 80, width: 900, height: 700, ...initial };
  const events = new EventEmitter();
  const contents = new EventEmitter();
  const flags = { maximized: false, fullscreen: false, destroyed: false, zoom: 1, refuse: false };
  const writes: Rectangle[] = [];
  const window = {
    isDestroyed: () => flags.destroyed,
    isMaximized: () => flags.maximized,
    isFullScreen: () => flags.fullscreen,
    isResizable: () => true,
    getBounds: () => ({ ...bounds }),
    getMinimumSize: () => [600, 400],
    getMaximumSize: () => [0, 0],
    setBounds: (next: Rectangle) => {
      writes.push(next);
      if (!flags.refuse) bounds = { ...next };
    },
    on: events.on.bind(events),
    webContents: Object.assign(contents, { getZoomFactor: () => flags.zoom }),
  } as unknown as BrowserWindow;
  return { window, flags, writes, events, contents };
}

const area = { x: 0, y: 0, width: 2200, height: 1200 };

describe("native session inspector right-edge expansion", () => {
  it("extends only the invoking window's right edge and is idempotent", () => {
    const controller = createSessionInspectorExpansion(() => area);
    const a = makeWindow();
    const b = makeWindow();
    expect(controller.set(a.window, { open: true })).toEqual({ expanded: true, panelWidth: 480 });
    expect(a.window.getBounds()).toEqual({ x: 100, y: 80, width: 1380, height: 700 });
    controller.set(a.window, { open: true, width: 700 });
    expect(a.writes).toHaveLength(1);
    expect(b.writes).toHaveLength(0);
    controller.set(a.window, { open: false });
    controller.set(a.window, { open: false });
    expect(a.window.getBounds().width).toBe(900);
    expect(a.writes).toHaveLength(2);
  });

  it("accounts for renderer zoom and native rounding", () => {
    const a = makeWindow();
    a.flags.zoom = 1.25;
    const controller = createSessionInspectorExpansion(() => area);
    expect(controller.set(a.window, { open: true, width: 481 })).toEqual({ expanded: true, panelWidth: 481.6 });
    expect(a.window.getBounds().width).toBe(1502);
  });

  it("refuses insufficient right-edge space even if room exists to the left", () => {
    const a = makeWindow({ x: 1100 });
    const controller = createSessionInspectorExpansion(() => area);
    expect(controller.set(a.window, { open: true }).reason).toBe("insufficient-space");
    expect(a.writes).toHaveLength(0);
  });

  it("uses the selected display's coordinates, including negative origins", () => {
    const a = makeWindow({ x: -1800 });
    const controller = createSessionInspectorExpansion(() => ({ ...area, x: -2200 }));
    expect(controller.set(a.window, { open: true }).expanded).toBe(true);
    expect(a.window.getBounds().x).toBe(-1800);
  });

  it.each(["maximized", "fullscreen"] as const)("refuses %s windows", flag => {
    const a = makeWindow();
    a.flags[flag] = true;
    const controller = createSessionInspectorExpansion(() => area);
    expect(controller.set(a.window, { open: true }).reason).toBe("unsupported-window");
    expect(a.writes).toHaveLength(0);
  });

  it("removes only the added width after user moves and resizes", () => {
    const a = makeWindow();
    const controller = createSessionInspectorExpansion(() => area);
    controller.set(a.window, { open: true });
    a.window.setBounds({ x: 250, y: 150, width: 1600, height: 800 });
    expect(controller.unexpandedBounds(a.window)).toEqual({ x: 250, y: 150, width: 1120, height: 800 });
    controller.set(a.window, { open: false });
    expect(a.window.getBounds()).toEqual({ x: 250, y: 150, width: 1120, height: 800 });
  });

  it("does not shrink below the native minimum after user resizing", () => {
    const a = makeWindow();
    const controller = createSessionInspectorExpansion(() => area);
    controller.set(a.window, { open: true });
    a.window.setBounds({ x: 250, y: 150, width: 700, height: 800 });
    controller.set(a.window, { open: false });
    expect(a.window.getBounds().width).toBe(600);
  });

  it("cleans up full reloads but leaves in-page navigation and subframes alone", () => {
    const a = makeWindow();
    const controller = createSessionInspectorExpansion(() => area);
    controller.set(a.window, { open: true });
    a.contents.emit("did-start-navigation", {}, "url", true, true);
    a.contents.emit("did-start-navigation", {}, "url", false, false);
    expect(a.window.getBounds().width).toBe(1380);
    a.contents.emit("did-start-navigation", {}, "url", false, true);
    expect(a.window.getBounds().width).toBe(900);
    controller.set(a.window, { open: true });
    expect(a.contents.listenerCount("did-start-navigation")).toBe(1);
    a.contents.emit("render-process-gone");
    expect(a.window.getBounds().width).toBe(900);
  });

  it.each(["maximized", "fullscreen"] as const)("defers cleanup while %s without restoring stale bounds", flag => {
    const a = makeWindow();
    const controller = createSessionInspectorExpansion(() => area);
    controller.set(a.window, { open: true });
    a.flags[flag] = true;
    controller.set(a.window, { open: false });
    expect(a.writes).toHaveLength(1);
    a.flags[flag] = false;
    a.events.emit(flag === "maximized" ? "unmaximize" : "leave-full-screen");
    expect(a.window.getBounds().width).toBe(900);
  });

  it("fails closed when the OS refuses exact expansion", () => {
    const a = makeWindow();
    a.flags.refuse = true;
    const controller = createSessionInspectorExpansion(() => area);
    expect(controller.set(a.window, { open: true }).reason).toBe("unsupported-window");
    expect(a.window.getBounds().width).toBe(900);
  });

  it.each([null, {}, { open: 1 }, { open: true, width: -1 }, { open: true, width: Infinity }])("rejects malformed requests: %j", payload => {
    const a = makeWindow();
    const controller = createSessionInspectorExpansion(() => area);
    expect(() => controller.set(a.window, payload)).toThrow();
    expect(a.writes).toHaveLength(0);
  });
});
