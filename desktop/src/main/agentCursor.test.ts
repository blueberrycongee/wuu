import vm from "node:vm";
import { describe, expect, it } from "vitest";
import { agentCursorCommandScript, clearAgentCursorScript, CURSOR_ARRIVE_CAP_MS, cursorRuntimeSource } from "./agentCursor";

type FakeEl = {
  id: string;
  style: { cssText: string; opacity: string; transform: string; left: string };
  innerHTML: string;
  isConnected: boolean;
  parentNode: FakeEl | null;
  children: FakeEl[];
  animations: Array<{ cancelled: boolean }>;
  setAttribute: (name: string, value: string) => void;
  appendChild: (child: FakeEl) => FakeEl;
  removeChild: (child: FakeEl) => void;
  remove: () => void;
  animate: () => { cancel: () => void };
};

type Sample = { t: number; x: number; y: number; heading: number };
type Move = { samples: Sample[]; arrivalT: number; duration: number };

function element(): FakeEl {
  const el: FakeEl = {
    id: "",
    style: { cssText: "", opacity: "", transform: "", left: "" },
    innerHTML: "",
    isConnected: false,
    parentNode: null,
    children: [],
    animations: [],
    setAttribute: () => undefined,
    appendChild: (child) => {
      child.isConnected = el.isConnected;
      child.parentNode = el;
      el.children.push(child);
      return child;
    },
    removeChild: (child) => {
      child.isConnected = false;
      child.parentNode = null;
      el.children = el.children.filter((item) => item !== child);
    },
    remove: () => el.parentNode?.removeChild(el),
    animate: () => {
      const animation = { cancelled: false };
      el.animations.push(animation);
      return { cancel: () => { animation.cancelled = true; } };
    },
  };
  return el;
}

function boot(reduced = false) {
  const frames: Array<(time: number) => void> = [];
  const timers: Array<() => void> = [];
  let now = 0;
  const root = element();
  root.isConnected = true;
  const document = { documentElement: root, createElement: () => element() };
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
    moveTo: (x: number, y: number) => Promise<void>;
    hide: () => void;
    feedback: (hint: { kind: string; x: number; y: number }) => void;
    setViewport: (viewport: { x: number; y: number; scale: number }) => void;
    plan: (start: { x: number; y: number }, end: { x: number; y: number }, heading: number | null) => Move;
    sampleAt: (move: Move, t: number) => Sample;
  };
  const cursor = () => root.children.find((child) => child.id === "__wuu_agent_cursor");
  // Paint position of the tip: the translate plus the hotspot offset that the
  // transform origin pins to the tip.
  const tip = () => {
    const host = cursor()!;
    const origin = host.style.cssText.match(/transform-origin:([\d.]+)px ([\d.]+)px/)!;
    const translate = host.style.transform.match(/translate\(([-\d.e]+)px, ([-\d.e]+)px\)/)!;
    return { x: Number(translate[1]) + Number(origin[1]), y: Number(translate[2]) + Number(origin[2]) };
  };
  const run = (limit = 200) => {
    let guard = 0;
    while (frames.length > 0 && guard < limit) {
      guard += 1;
      now = guard * 16;
      frames.shift()?.(now);
    }
  };
  return {
    runtime,
    root,
    cursor,
    tip,
    run,
    frames,
    timers,
    setNow: (value: number) => {
      now = value;
    },
  };
}

describe("agent cursor travel", () => {
  it("bends a move to the natural side and leads with the tip", () => {
    const { runtime } = boot();
    const move = runtime.plan({ x: 0, y: 300 }, { x: 600, y: 300 }, null);
    const mid = runtime.sampleAt(move, move.arrivalT / 2);
    expect(mid.y).toBeLessThan(280);
    expect(Math.abs(mid.heading - Math.PI / 4)).toBeGreaterThan(1);
    const end = move.samples[move.samples.length - 1];
    expect(end).toMatchObject({ x: 600, y: 300, heading: Math.PI / 4 });
  });

  it("arrives within the input cap at any distance and settles afterward", () => {
    const { runtime } = boot();
    for (const distance of [12, 120, 600, 2400, 6000]) {
      const move = runtime.plan({ x: 0, y: 0 }, { x: distance, y: distance / 3 }, null);
      expect(move.arrivalT * 1000).toBeLessThan(CURSOR_ARRIVE_CAP_MS);
      expect(move.duration).toBeGreaterThan(move.arrivalT);
      const arrived = runtime.sampleAt(move, move.arrivalT);
      expect(Math.hypot(arrived.x - distance, arrived.y - distance / 3)).toBeLessThanOrEqual(1);
    }
  });

  it("lands on the target, stays out of hit testing and floats at rest", async () => {
    const { runtime, cursor, tip, run } = boot();
    const arrived = runtime.moveTo(120, 80);
    run();
    await arrived;
    const host = cursor()!;
    expect(host.style.cssText).toContain("pointer-events:none");
    expect(tip().x).toBeCloseTo(120);
    expect(tip().y).toBeCloseTo(80);
    expect(host.style.opacity).toBe("1");
    expect(host.children[0].innerHTML).toContain("<svg");
    expect(host.children[0].animations.some((animation) => !animation.cancelled)).toBe(true);
  });

  it("resolves at arrival while the settle keeps playing", async () => {
    const { runtime, frames, setNow } = boot();
    let resolved = false;
    void runtime.moveTo(600, 300).then(() => { resolved = true; });
    const move = runtime.plan({ x: 464, y: 330 }, { x: 600, y: 300 }, null);
    let time = 0;
    while (!resolved && frames.length > 0 && time < 2000) {
      time += 16;
      setNow(time);
      frames.shift()?.(time);
      await Promise.resolve();
    }
    expect(resolved).toBe(true);
    expect(time).toBeLessThan(move.duration * 1000);
    expect(frames.length).toBeGreaterThan(0);
  });

  it("snaps when motion is reduced", async () => {
    const { runtime, tip, frames, cursor } = boot(true);
    await runtime.moveTo(40, 50);
    expect(frames).toHaveLength(0);
    expect(tip()).toEqual({ x: 40, y: 50 });
    expect(cursor()!.children[0].animations).toHaveLength(0);
  });

  it("keeps the pointer tip on the page point as a preview resizes", async () => {
    const { runtime, tip, cursor } = boot(true);
    runtime.setViewport({ x: 0, y: 20, scale: 0.26 });
    await runtime.moveTo(500, 250);
    expect(tip().x).toBeCloseTo(130);
    expect(tip().y).toBeCloseTo(85);
    runtime.setViewport({ x: 10, y: 0, scale: 0.5 });
    expect(tip().x).toBeCloseTo(260);
    expect(tip().y).toBeCloseTo(125);
    expect(cursor()!.style.transform).toContain("scale(1)");
  });

  it("settles a pending arrival when hidden and never paints again", async () => {
    const { runtime, cursor, root, frames, timers, setNow } = boot();
    const arrived = runtime.moveTo(40, 50);
    setNow(16);
    frames.shift()?.(16);
    const host = cursor()!;
    expect(host.isConnected).toBe(true);
    runtime.hide();
    await arrived;
    for (const timer of timers) timer();
    expect(frames).toHaveLength(0);
    expect(host.isConnected).toBe(false);
    expect(root.children).toHaveLength(0);
  });

  it("arrives on time when the page never runs a frame", async () => {
    const { runtime, tip, timers } = boot();
    const arrived = runtime.moveTo(300, 200);
    for (const timer of timers) timer();
    await arrived;
    expect(tip()).toEqual({ x: 300, y: 200 });
  });

  it("settles an interrupted move and continues from its painted position", async () => {
    const { runtime, tip, frames, setNow } = boot();
    const first = runtime.moveTo(100, 50);
    setNow(96);
    frames.shift()?.(96);
    const painted = tip();
    const second = runtime.moveTo(700, 500);
    await first;
    frames.shift()?.(96);
    const retargeted = tip();
    expect(retargeted.x).toBeCloseTo(painted.x);
    expect(retargeted.y).toBeCloseTo(painted.y);
    runtime.hide();
    await second;
  });

  it("squishes the pointer on a click and recovers", async () => {
    const { runtime, cursor, run, setNow, frames } = boot();
    const arrived = runtime.moveTo(200, 120);
    run();
    await arrived;
    setNow(10_000);
    runtime.feedback({ kind: "click", x: 200, y: 120 });
    setNow(10_060);
    frames.shift()?.(10_060);
    expect(cursor()!.style.transform).toMatch(/scale\(0\.8\d*\)/);
    setNow(11_000);
    frames.shift()?.(11_000);
    expect(cursor()!.style.transform).toContain("scale(1)");
    expect(frames).toHaveLength(0);
  });

  it("removes the pointer when hidden", () => {
    const { runtime, cursor } = boot();
    runtime.hide();
    expect(cursor()).toBeUndefined();
    expect(clearAgentCursorScript()).toContain("__wuuAgentCursor");
    expect(agentCursorCommandScript(3, 4)).toContain("moveTo(3, 4)");
  });
});
