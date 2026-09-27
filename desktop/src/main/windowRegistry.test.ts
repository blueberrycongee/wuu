import type { BrowserWindow } from "electron";
import { describe, expect, it } from "vitest";
import type { RuntimeContext } from "../shared/protocol";
import { createWindowRegistry } from "./windowRegistry";

type MockListeners = {
  fire(event: string): void;
  listenerCount(event: string): number;
};

function makeWindow(
  id: number,
  options: { destroyed?: boolean } = {},
): BrowserWindow & MockListeners {
  const destroyed = options.destroyed ?? false;
  const webContents = {
    id,
    isDestroyed: () => destroyed,
  };
  const listeners = new Map<string, Set<() => void>>();
  const record = (event: string, cb: () => void): void => {
    let set = listeners.get(event);
    if (!set) {
      set = new Set();
      listeners.set(event, set);
    }
    set.add(cb);
  };
  return {
    webContents,
    isDestroyed: () => destroyed,
    on: (event: string, cb: () => void) => {
      record(event, cb);
    },
    fire: (event: string): void => {
      const set = listeners.get(event);
      if (set) for (const cb of set) cb();
    },
    listenerCount: (event: string): number =>
      listeners.get(event)?.size ?? 0,
  } as unknown as BrowserWindow & MockListeners;
}

describe("windowRegistry", () => {
  describe("activity windows", () => {
    it("round-trips an Activity window and clears it on unregister", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(9);
      registry.registerWindow(win, "activity", { activityID: "activity-1" });
      registry.setActivityWindow("activity-1", 9);
      expect(registry.activityHostWindowID("activity-1")).toBe(9);
      expect(registry.activityWindow("activity-1")).toBe(win);
      registry.unregisterWindow(9);
      expect(registry.activityHostWindowID("activity-1")).toBeUndefined();
      expect(registry.activityWindow("activity-1")).toBeNull();
    });

    it("clearActivityWindow keeps the registered window alive", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(10);
      registry.registerWindow(win, "activity", { activityID: "activity-2" });
      registry.setActivityWindow("activity-2", 10);
      registry.clearActivityWindow("activity-2");
      expect(registry.activityWindow("activity-2")).toBeNull();
      expect(registry.allWindows()).toContain(win);
    });
  });

  describe("registerWindow / mainWindow / allWindows", () => {
    it("registers a main window and returns it from mainWindow()", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(1);
      registry.registerWindow(win, "main");
      expect(registry.mainWindow()).toBe(win);
      expect(registry.allWindows()).toEqual([win]);
      expect(registry.windowForID(1)).toBe(win);
    });

    it("returns null from mainWindow() when only popped-out windows are registered", () => {
      const registry = createWindowRegistry();
      registry.registerWindow(makeWindow(1), "popped-out");
      expect(registry.mainWindow()).toBeNull();
      expect(registry.allWindows()).toHaveLength(1);
    });

    it("lists main and popped-out windows in allWindows() in registration order", () => {
      const registry = createWindowRegistry();
      const main = makeWindow(1);
      const poppedA = makeWindow(2);
      const poppedB = makeWindow(3);
      registry.registerWindow(main, "main");
      registry.registerWindow(poppedA, "popped-out");
      registry.registerWindow(poppedB, "popped-out");
      expect(registry.mainWindow()).toBe(main);
      expect(registry.allWindows()).toEqual([main, poppedA, poppedB]);
    });

    it("overwrites a previous entry for the same webContents.id", () => {
      const registry = createWindowRegistry();
      const first = makeWindow(1);
      const second = makeWindow(1);
      registry.registerWindow(first, "main");
      registry.registerWindow(second, "main");
      expect(registry.mainWindow()).toBe(second);
      expect(registry.allWindows()).toEqual([second]);
    });

    it("stores popped-out thread identity and runtime context", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(2);
      const context: RuntimeContext = {
        kind: "project",
        project_id: "project-1",
        cwd: "/tmp/project",
      };
      registry.registerWindow(win, "popped-out", {
        runtimeContext: context,
        threadID: "thread-1",
      });
      expect(registry.runtimeContextForWindow(2)).toBe(context);
      expect(registry.threadForWindow(2)).toBe("thread-1");
    });

  });

  describe("unregisterWindow", () => {
    it("removes the window from mainWindow() and allWindows()", () => {
      const registry = createWindowRegistry();
      registry.registerWindow(makeWindow(1), "main");
      registry.unregisterWindow(1);
      expect(registry.mainWindow()).toBeNull();
      expect(registry.allWindows()).toEqual([]);
    });

    it("is a no-op for unknown windowIDs", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(1);
      registry.registerWindow(win, "main");
      expect(() => registry.unregisterWindow(999)).not.toThrow();
      expect(registry.mainWindow()).toBe(win);
    });

    it("clears thread → window mappings pointing at the unregistered window", () => {
      const registry = createWindowRegistry();
      const poppedB = makeWindow(3);
      registry.registerWindow(makeWindow(2), "popped-out");
      registry.registerWindow(poppedB, "popped-out");
      registry.setThreadWindow("t-victim", 2);
      registry.setThreadWindow("t-survivor", 3);
      registry.unregisterWindow(2);
      expect(registry.threadHostWindowID("t-victim")).toBeUndefined();
      expect(registry.threadHostWindowID("t-survivor")).toBe(3);
      expect(registry.popOutWindowForThread("t-victim")).toBeNull();
      expect(registry.popOutWindowForThread("t-survivor")).toBe(poppedB);
    });
  });

  describe("setThreadWindow / clearThreadWindow / popOutWindowForThread / threadHostWindowID", () => {
    it("round-trips a thread → window mapping", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(7);
      registry.registerWindow(win, "popped-out");
      registry.setThreadWindow("t-x", 7);
      expect(registry.threadHostWindowID("t-x")).toBe(7);
      expect(registry.popOutWindowForThread("t-x")).toBe(win);
    });

    it("returns null from popOutWindowForThread when the thread is unknown", () => {
      const registry = createWindowRegistry();
      expect(registry.popOutWindowForThread("missing")).toBeNull();
    });

    it("returns null from popOutWindowForThread after the host window is unregistered", () => {
      const registry = createWindowRegistry();
      registry.registerWindow(makeWindow(7), "popped-out");
      registry.setThreadWindow("t-x", 7);
      registry.unregisterWindow(7);
      expect(registry.popOutWindowForThread("t-x")).toBeNull();
    });

    it("clearThreadWindow drops the mapping without touching the window entry", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(7);
      registry.registerWindow(win, "popped-out");
      registry.setThreadWindow("t-x", 7);
      registry.clearThreadWindow("t-x");
      expect(registry.threadHostWindowID("t-x")).toBeUndefined();
      expect(registry.popOutWindowForThread("t-x")).toBeNull();
      expect(registry.allWindows()).toEqual([win]);
    });

    it("distinct threads map to distinct windows and to the same window", () => {
      const registry = createWindowRegistry();
      const a = makeWindow(2);
      const b = makeWindow(3);
      registry.registerWindow(a, "popped-out");
      registry.registerWindow(b, "popped-out");
      registry.setThreadWindow("t-1", 2);
      registry.setThreadWindow("t-2", 2);
      registry.setThreadWindow("t-3", 3);
      expect(registry.popOutWindowForThread("t-1")).toBe(a);
      expect(registry.popOutWindowForThread("t-2")).toBe(a);
      expect(registry.popOutWindowForThread("t-3")).toBe(b);
    });

    it("re-registering the same windowID for the same thread just overwrites", () => {
      const registry = createWindowRegistry();
      const first = makeWindow(5);
      const second = makeWindow(5);
      registry.registerWindow(first, "popped-out");
      registry.registerWindow(second, "popped-out");
      registry.setThreadWindow("t-rereg", 5);
      registry.setThreadWindow("t-rereg", 5);
      expect(registry.popOutWindowForThread("t-rereg")).toBe(second);
    });
  });

  describe("destroyed BrowserWindow", () => {
    it("returns the destroyed reference so callers can observe isDestroyed()", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(1, { destroyed: true });
      registry.registerWindow(win, "main");
      const returned = registry.mainWindow();
      expect(returned?.isDestroyed()).toBe(true);
      expect(registry.allWindows()[0]?.isDestroyed()).toBe(true);
    });

    it("popOutWindowForThread on a destroyed host returns the destroyed reference", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(7, { destroyed: true });
      registry.registerWindow(win, "popped-out");
      registry.setThreadWindow("t-doomed", 7);
      const returned = registry.popOutWindowForThread("t-doomed");
      expect(returned?.isDestroyed()).toBe(true);
    });
  });

  describe("attachResizeHandlers", () => {
    it("attaches will-resize, resize, and resized listeners to a window", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(1);
      const phases: Array<"live" | "end"> = [];
      registry.attachResizeHandlers(win, (phase) => {
        phases.push(phase);
      });
      expect(win.listenerCount("will-resize")).toBe(1);
      expect(win.listenerCount("resize")).toBe(1);
      expect(win.listenerCount("resized")).toBe(1);

      win.fire("will-resize");
      win.fire("resize");
      win.fire("resized");
      expect(phases).toEqual(["live", "live", "end"]);
    });

    it("does not register the window with the registry", () => {
      const registry = createWindowRegistry();
      const win = makeWindow(1);
      registry.attachResizeHandlers(win, () => {});
      expect(registry.allWindows()).toEqual([]);
    });
  });

});
