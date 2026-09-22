import vm from "node:vm";
import { describe, expect, it } from "vitest";
import { agentCursorCommandScript, clearAgentCursorScript, cursorRuntimeSource } from "./agentCursor";

type FakeEl = {
  id: string;
  style: { cssText: string; opacity: string; transform: string };
  innerHTML: string;
  isConnected: boolean;
  parentNode: { removeChild: (child: FakeEl) => void } | null;
  setAttribute: (name: string, value: string) => void;
};

function boot(reduced = false) {
  const appended: FakeEl[] = [];
  const frames: Array<(time: number) => void> = [];
  const timers: Array<() => void> = [];
  let now = 0;
  const host: FakeEl = {
    id: "",
    style: { cssText: "", opacity: "", transform: "" },
    innerHTML: "",
    isConnected: false,
    parentNode: null,
    setAttribute: () => undefined,
  };
  const document = {
    documentElement: {
      appendChild: (child: FakeEl) => {
        child.isConnected = true;
        child.parentNode = document.documentElement;
        appended.push(child);
        return child;
      },
      removeChild: (child: FakeEl) => {
        child.isConnected = false;
        child.parentNode = null;
      },
    },
    createElement: () => host,
  };
  const context = vm.createContext({
    document,
    performance: { now: () => now },
    requestAnimationFrame: (callback: (time: number) => void) => {
      frames.push(callback);
      return frames.length;
    },
    cancelAnimationFrame: () => {
      frames.length = 0;
    },
    setTimeout: (callback: () => void) => {
      timers.push(callback);
      return timers.length;
    },
    clearTimeout: () => undefined,
    Promise,
    innerWidth: 800,
    innerHeight: 600,
    matchMedia: () => ({ matches: reduced }),
  });
  (context as { window: unknown }).window = context;
  const runtime = vm.runInContext(`(${cursorRuntimeSource})()`, context) as {
    moveTo: (x: number, y: number, press: boolean) => Promise<void>;
    hide: () => void;
    plan: (start: { x: number; y: number }, end: { x: number; y: number }) => {
      mode: string;
      control?: { x: number; y: number };
      start: { x: number; y: number };
      end: { x: number; y: number };
    };
    poseAt: (
      move: ReturnType<ReturnType<typeof boot>["runtime"]["plan"]>,
      t: number,
      opacity: number,
    ) => { x: number; y: number; rotation: number };
  };
  return {
    runtime,
    host,
    frames,
    timers,
    setNow: (value: number) => {
      now = value;
    },
  };
}

function distanceToSegment(
  point: { x: number; y: number },
  start: { x: number; y: number },
  end: { x: number; y: number },
): number {
  const dx = end.x - start.x;
  const dy = end.y - start.y;
  const len = dx * dx + dy * dy || 1;
  const t = Math.max(0, Math.min(1, ((point.x - start.x) * dx + (point.y - start.y) * dy) / len));
  const x = start.x + dx * t;
  const y = start.y + dy * t;
  return Math.hypot(point.x - x, point.y - y);
}

describe("agent cursor travel", () => {
  it("eases a short move along the straight line and arcs a long one", () => {
    const { runtime } = boot();
    const scoot = runtime.plan({ x: 10, y: 10 }, { x: 80, y: 40 });
    const scootMid = runtime.poseAt(scoot, 0.5, 1);
    expect(scoot.mode).toBe("scoot");
    expect(distanceToSegment(scootMid, scoot.start, scoot.end)).toBeLessThan(1);

    const arc = runtime.plan({ x: 0, y: 0 }, { x: 520, y: 40 });
    const arcMid = runtime.poseAt(arc, 0.5, 1);
    expect(arc.mode).toBe("arc");
    expect(distanceToSegment(arcMid, arc.start, arc.end)).toBeGreaterThan(8);
    expect(Math.abs(arcMid.rotation)).toBeGreaterThan(20);
  });

  it("lands on the target and keeps the pointer from taking clicks", async () => {
    const { runtime, host, frames, setNow } = boot();
    const arrived = runtime.moveTo(120, 80, true);
    let guard = 0;
    while (frames.length > 0 && guard < 80) {
      const callback = frames.shift();
      setNow(guard * 16 + 16);
      callback?.(guard * 16 + 16);
      guard += 1;
    }
    await arrived;
    expect(host.style.cssText).toContain("pointer-events:none");
    expect(host.style.transform).toContain("translate(118.8px, 78.9px)");
    expect(host.style.opacity).toBe("1");
    expect(host.innerHTML).toContain("<svg");
  });

  it("snaps when motion is reduced", async () => {
    const { runtime, host, frames } = boot(true);
    await runtime.moveTo(40, 50, false);
    expect(frames).toHaveLength(0);
    expect(host.style.transform).toContain("translate(38.8px, 48.9px)");
  });

  it("settles a pending arrival when hidden and never paints again", async () => {
    const { runtime, host, frames, timers, setNow } = boot();
    const arrived = runtime.moveTo(40, 50, false);
    setNow(16);
    frames.shift()?.(16);
    expect(host.isConnected).toBe(true);
    runtime.hide();
    await arrived;
    for (const timer of timers) timer();
    expect(frames).toHaveLength(0);
    expect(host.isConnected).toBe(false);
  });

  it("settles an interrupted move and continues from its painted position", async () => {
    const { runtime, host, frames, setNow } = boot();
    const first = runtime.moveTo(100, 50, false);
    setNow(96);
    frames.shift()?.(96);
    const painted = host.style.transform.match(/translate\(([^p]+)px, ([^p]+)px\)/)!;
    const second = runtime.moveTo(700, 500, false);
    await first;
    frames.shift()?.(96);
    const retargeted = host.style.transform.match(/translate\(([^p]+)px, ([^p]+)px\)/)!;
    expect(Number(retargeted[1])).toBeCloseTo(Number(painted[1]));
    expect(Number(retargeted[2])).toBeCloseTo(Number(painted[2]));
    runtime.hide();
    await second;
  });

  it("removes the pointer when hidden", () => {
    const { runtime, host } = boot();
    runtime.hide();
    expect(host.isConnected).toBe(false);
    expect(clearAgentCursorScript()).toContain("__wuuAgentCursor");
    expect(agentCursorCommandScript(3, 4, false)).toContain("moveTo(3, 4, false)");
  });
});
