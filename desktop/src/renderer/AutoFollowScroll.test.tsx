import { act, createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  atLatestScrollView,
  latestFollowScrollTop,
  measureActiveConversationForRestore,
  measureLatestConversationTurns,
  scrollTopForDistanceFromLatest,
  sessionTailSpacePx,
  useAutoFollowScrollContainer,
} from "./AutoFollowScroll";
import { WINDOW_RESIZING_CLASS } from "./WindowResizeState";

interface StubbedLayout {
  scrollHeight: number;
  clientHeight: number;
  scrollTop: number;
}

function stubLayout(node: HTMLElement): StubbedLayout {
  const layout = {
    scrollHeight: 1200,
    clientHeight: 400,
    scrollTop: 800,
  };
  Object.defineProperties(node, {
    scrollHeight: {
      configurable: true,
      get: () => layout.scrollHeight,
    },
    clientHeight: {
      configurable: true,
      get: () => layout.clientHeight,
    },
    scrollTop: {
      configurable: true,
      get: () => layout.scrollTop,
      set: (value: number) => {
        layout.scrollTop = Math.max(
          0,
          Math.min(value, layout.scrollHeight - layout.clientHeight),
        );
      },
    },
  });
  return layout;
}

type HookHandle = ReturnType<typeof useAutoFollowScrollContainer>;

function Probe({ onReady }: { onReady: (handle: HookHandle) => void }): ReactNode {
  const handle = useAutoFollowScrollContainer();
  onReady(handle);
  return createElement("div", { ref: handle.scrollRef });
}

describe("useAutoFollowScrollContainer", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;
  let handle: HookHandle | null = null;
  let scrollNode: HTMLDivElement | null = null;
  let layout: StubbedLayout | null = null;
  let notifyResize: () => void;
  let frames: Map<number, FrameRequestCallback>;

  function paint(now = 0): void {
    const pending = [...frames.values()];
    frames.clear();
    act(() => pending.forEach((callback) => callback(now)));
  }

  /** Run pending frames until the arrival scroll stops scheduling them. */
  function settle(from: number): void {
    for (let time = from; frames.size > 0 && time <= from + 4000; time += 20) paint(time);
  }

  beforeEach(() => {
    frames = new Map();
    let nextFrame = 0;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
      frames.set(++nextFrame, callback);
      return nextFrame;
    });
    vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: () => void) { notifyResize = callback; }
      observe() {}
      disconnect() {}
    });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => {
      root?.render(createElement(Probe, { onReady: (next) => { handle = next; } }));
    });
    scrollNode = container.firstElementChild as HTMLDivElement;
    layout = stubLayout(scrollNode);
    act(() => {
      scrollNode?.dispatchEvent(new Event("scroll"));
    });
  });

  afterEach(() => {
    act(() => root?.unmount());
    document.body.removeChild(container);
    document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("stays at the bottom during continuous window resizing before settling", () => {
    if (!layout || !scrollNode) throw new Error("probe not mounted");
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    for (const [height, contentHeight] of [[300, 1200], [200, 1600], [500, 1300]]) {
      layout.clientHeight = height;
      layout.scrollHeight = contentHeight;
      act(() => {
        window.dispatchEvent(new Event("resize"));
        notifyResize();
      });
      expect(layout.scrollTop).toBe(contentHeight - height);
      paint();
      expect(layout.scrollTop).toBe(contentHeight - height);
      act(() => scrollNode!.dispatchEvent(new Event("scroll")));
      expect(handle?.autoFollowRef.current).toBe(true);
    }
  });

  it("restores a paused conversation before paint and cancels the outgoing arrival", () => {
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    paint(0); paint(180);
    expect(layout!.scrollTop).toBeGreaterThan(800);
    act(() => handle!.restoreScrollPosition(320, false));
    expect(layout!.scrollTop).toBe(320);
    expect(handle!.autoFollowRef.current).toBe(false);
    settle(200);
    act(() => { notifyResize(); scrollNode!.dispatchEvent(new Event("scroll")); });
    expect(layout!.scrollTop).toBe(320);
    act(() => handle!.restoreScrollPosition(0, true));
    expect(layout!.scrollTop).toBe(1200);
    expect(handle!.autoFollowRef.current).toBe(true);
  });

  it("uses one continuous arrival scroll across resize and reconciliation writes", () => {
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    expect(layout!.scrollTop).toBe(800);
    paint(0); paint(180);
    const middle = layout!.scrollTop;
    expect(middle).toBeGreaterThan(800);
    expect(middle).toBeLessThan(1200);
    layout!.scrollHeight += 100;
    act(() => { notifyResize(); handle!.scrollToBottom(); });
    expect(layout!.scrollTop).toBe(middle);
    paint(180);
    expect(layout!.scrollTop).toBe(middle);
    // The new bottom extends the same trajectory: it advances without either
    // restarting or stepping backwards.
    paint(200);
    expect(layout!.scrollTop).toBeGreaterThan(middle);
    settle(220);
    expect(layout!.scrollTop).toBe(1300);
    expect(frames.size).toBe(0);
  });

  it.each(["wheel", "pointerdown", "touchstart", "keydown"])("yields an arrival to %s before another frame or resize can write", event => {
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    paint(0); paint(80);
    const position = layout!.scrollTop;
    act(() => {
      scrollNode!.dispatchEvent(event === "wheel" ? new WheelEvent(event, { deltaY: 20 })
        : event === "keydown" ? new KeyboardEvent(event, { key: "PageUp" })
        : event === "touchstart" ? new TouchEvent(event, { touches: [] }) : new Event(event));
      notifyResize();
    });
    paint(500);
    expect(layout!.scrollTop).toBe(position);
    expect(handle!.autoFollowRef.current).toBe(false);
  });

  it("keeps incoming arrivals paused until a new local send explicitly resumes following", () => {
    act(() => handle!.pauseAutoFollow());
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    paint(0); paint(500);
    expect(layout!.scrollTop).toBe(800);
    act(() => handle!.scrollToBottom({ force: true, animate: true }));
    expect(layout!.scrollTop).toBe(800);
    paint(600);
    expect(layout!.scrollTop).toBeGreaterThan(800);
    settle(620);
    expect(layout!.scrollTop).toBe(1200);
  });

  it("settles an active arrival before hiding and never leaves a background scroll running", () => {
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    paint(0); paint(80);
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    act(() => document.dispatchEvent(new Event("visibilitychange")));
    expect(layout!.scrollTop).toBe(1200);
    expect(frames.size).toBe(0);
  });

  it("positions immediately instead of animating when reduced motion is requested", () => {
    vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true } as MediaQueryList);
    layout!.scrollHeight += 400;
    act(() => handle!.scrollToBottom({ animate: true }));
    expect(layout!.scrollTop).toBe(1200);
    expect(frames.size).toBe(0);
  });

  it("respects reading history after the stream mounts on opening or returning from setup", () => {
    function ConditionalProbe({ open }: { open: boolean }): ReactNode {
      handle = useAutoFollowScrollContainer({ open, observeKey: "same-room" });
      return open ? createElement("div", { ref: handle.scrollRef }) : null;
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      act(() => root?.render(createElement(ConditionalProbe, { open: false })));
      act(() => root?.render(createElement(ConditionalProbe, { open: true })));
      scrollNode = container.firstElementChild as HTMLDivElement;
      layout = stubLayout(scrollNode);
      act(() => {
        scrollNode!.dispatchEvent(new WheelEvent("wheel", { deltaY: -100 }));
        layout!.scrollTop = 300;
        scrollNode!.dispatchEvent(new Event("scroll"));
      });
      expect(handle?.autoFollowRef.current).toBe(false);
      layout.scrollHeight += 200;
      act(() => notifyResize());
      expect(layout.scrollTop).toBe(300);
    }
  });

  it("does not pull history to the bottom when a resize frame is pending", () => {
    if (!layout || !scrollNode) throw new Error("probe not mounted");
    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    layout.clientHeight = 200;
    act(() => {
      notifyResize();
      scrollNode!.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
      layout!.scrollTop = 500;
      scrollNode!.dispatchEvent(new Event("scroll"));
    });
    paint();
    expect(layout.scrollTop).toBe(500);
    expect(handle?.autoFollowRef.current).toBe(false);
    act(() => notifyResize());
    paint();
    expect(layout.scrollTop).toBe(500);
  });

  it("stops following as soon as the user wheels upward", () => {
    act(() => {
      scrollNode?.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
    });

    expect(handle?.autoFollowRef.current).toBe(false);
    if (!layout || !handle) throw new Error("probe not mounted");
    layout.scrollHeight = 1400;
    handle.scrollToBottom();
    expect(layout.scrollTop).toBe(800);
  });

  it.each(["keyboard", "touch", "scrollbar"])(
    "yields streaming follow to %s before native scroll delivery",
    (input) => {
      act(() => {
        handle!.scrollToBottom();
        handle!.scheduleScrollToBottom();
        if (input === "keyboard") {
          scrollNode!.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp" }));
        } else if (input === "touch") {
          scrollNode!.dispatchEvent(new TouchEvent("touchstart", { touches: [{ clientY: 100 } as Touch] }));
          scrollNode!.dispatchEvent(new TouchEvent("touchmove", { touches: [{ clientY: 120 } as Touch] }));
        } else {
          scrollNode!.dispatchEvent(new Event("pointerdown"));
        }
        layout!.scrollHeight += 24;
        notifyResize();
      });
      paint();
      expect(layout!.scrollTop).toBe(800);

      act(() => {
        layout!.scrollTop = 792;
        handle!.scrollToBottom();
        scrollNode!.dispatchEvent(new Event("scroll"));
        layout!.scrollHeight += 80;
        notifyResize();
      });
      paint();
      expect(layout!.scrollTop).toBe(792);

      act(() => {
        scrollNode!.dispatchEvent(new KeyboardEvent("keydown", { key: "End" }));
        layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight;
        scrollNode!.dispatchEvent(new Event("scroll"));
        window.dispatchEvent(new Event("pointerup"));
        layout!.scrollHeight += 40;
        notifyResize();
      });
      paint();
      expect(layout!.scrollTop).toBe(layout!.scrollHeight - layout!.clientHeight);
    },
  );

  it.each([400, 1200])("resumes after a scroll-surface click without movement (height %i)", (height) => {
    layout!.scrollHeight = height;
    act(() => {
      handle!.scrollToBottom();
      scrollNode!.dispatchEvent(new Event("pointerdown"));
      layout!.scrollHeight += 600;
      notifyResize();
    });
    paint();
    expect(layout!.scrollTop).toBe(height - 400);
    act(() => window.dispatchEvent(new Event("pointerup")));
    paint();
    expect(layout!.scrollTop).toBe(layout!.scrollHeight - layout!.clientHeight);
    act(() => {
      layout!.scrollHeight += 40;
      notifyResize();
    });
    paint();
    expect(layout!.scrollTop).toBe(layout!.scrollHeight - layout!.clientHeight);
  });

  it.each(["paused", "drag", "wheel", "cancel"])("does not resume a scroll-surface gesture after %s", (reason) => {
    act(() => {
      handle!.scrollToBottom();
      if (reason === "paused") handle!.pauseAutoFollow();
      scrollNode!.dispatchEvent(new Event("pointerdown"));
      if (reason === "drag") layout!.scrollTop -= 8;
      if (reason === "wheel") scrollNode!.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
      // A drag's native scroll event may still be pending on pointer release.
      window.dispatchEvent(new Event(reason === "cancel" ? "pointercancel" : "pointerup"));
      layout!.scrollHeight += 40;
      notifyResize();
    });
    paint();
    expect(layout!.scrollTop).toBe(reason === "drag" ? 792 : 800);
  });

  it("keeps automatic viewport following quiet and still reveals user scrolling", () => {
    if (!layout || !handle || !scrollNode) throw new Error("probe not mounted");
    scrollNode.classList.remove("scrollbar-visible");
    for (const height of [300, 200, 300, 400]) {
      layout.clientHeight = height;
      act(() => {
        handle!.scrollToBottom();
        scrollNode!.dispatchEvent(new Event("scroll"));
      });
      expect(layout.scrollTop).toBe(layout.scrollHeight - height);
      expect(handle.autoFollowRef.current).toBe(true);
      expect(scrollNode.classList.contains("scrollbar-visible")).toBe(false);
    }
    act(() => {
      layout!.scrollTop -= 30;
      scrollNode!.dispatchEvent(new Event("scroll"));
    });
    expect(handle.autoFollowRef.current).toBe(false);
    expect(scrollNode.classList.contains("scrollbar-visible")).toBe(true);
  });

  it("stops following when a scrollbar drag moves upward without preflight input", () => {
    if (!layout) throw new Error("probe not mounted");
    layout.scrollTop = 500;
    act(() => {
      scrollNode?.dispatchEvent(new Event("scroll"));
    });

    expect(handle?.autoFollowRef.current).toBe(false);
  });

  it("does not resume following when keyboard dismissal clamps history to the bottom", () => {
    if (!layout || !handle || !scrollNode) throw new Error("probe not mounted");
    act(() => {
      layout!.scrollTop = 600;
      scrollNode!.dispatchEvent(new Event("scroll"));
    });
    expect(handle.autoFollowRef.current).toBe(false);
    scrollNode.classList.remove("scrollbar-visible");
    act(() => {
      layout!.clientHeight = 700;
      // The browser clamps to the new maximum before ResizeObserver runs.
      scrollNode!.scrollTop = layout!.scrollTop;
      scrollNode!.dispatchEvent(new Event("scroll"));
    });
    expect(handle.autoFollowRef.current).toBe(false);
    expect(scrollNode.classList.contains("scrollbar-visible")).toBe(false);
    layout.scrollHeight += 200;
    handle.scrollToBottom();
    expect(layout.scrollTop).toBe(500);
  });

  it("does not restore the bottom after a user-triggered layout expansion", () => {
    if (!layout || !handle || !scrollNode) throw new Error("probe not mounted");

    handle.pauseAutoFollow();
    layout.scrollHeight = 1600;
    act(() => {
      scrollNode?.dispatchEvent(new Event("scroll"));
    });
    handle.scrollToBottom();

    expect(handle.autoFollowRef.current).toBe(false);
    expect(layout.scrollTop).toBe(800);
  });
});

describe("latest follow position", () => {
  it("excludes unconsumed submission tail from the follow target", () => {
    const pane = document.createElement("main");
    pane.className = "conversation-pane";
    pane.style.setProperty("--session-tail-space", "480px");
    const node = document.createElement("div");
    pane.append(node);
    Object.defineProperties(node, {
      scrollHeight: { configurable: true, get: () => 2000 },
      clientHeight: { configurable: true, get: () => 600 },
      scrollTop: { configurable: true, get: () => 920, set: () => undefined },
    });

    expect(sessionTailSpacePx(node)).toBe(480);
    expect(latestFollowScrollTop(node)).toBe(920);
    expect(atLatestScrollView(node, 16)).toBe(true);

    Object.defineProperty(node, "scrollTop", {
      configurable: true,
      get: () => 1400,
    });
    expect(atLatestScrollView(node, 16)).toBe(true);

    Object.defineProperty(node, "scrollTop", {
      configurable: true,
      get: () => 400,
    });
    expect(atLatestScrollView(node, 16)).toBe(false);
  });

  it("maps a distance from latest content back to the same reading point after height growth", () => {
    const node = document.createElement("div");
    const layout = {
      scrollHeight: 2000,
      clientHeight: 600,
      scrollTop: 800,
    };
    Object.defineProperties(node, {
      scrollHeight: { configurable: true, get: () => layout.scrollHeight },
      clientHeight: { configurable: true, get: () => layout.clientHeight },
      scrollTop: {
        configurable: true,
        get: () => layout.scrollTop,
        set: (value: number) => {
          layout.scrollTop = value;
        },
      },
    });

    const distance = latestFollowScrollTop(node) - layout.scrollTop;
    layout.scrollHeight = 2600;
    expect(scrollTopForDistanceFromLatest(node, distance)).toBe(1400);
  });

  it("forces the last conversation turns to layout before a restore", () => {
    const node = document.createElement("div");
    const turns = Array.from({ length: 7 }, () => {
      const turn = document.createElement("article");
      turn.className = "turn";
      node.append(turn);
      return turn;
    });
    const measured: HTMLElement[] = [];
    for (const turn of turns) {
      Object.defineProperty(turn, "offsetHeight", {
        configurable: true,
        get() {
          measured.push(turn);
          return 40;
        },
      });
    }

    measureLatestConversationTurns(node);
    expect(measured).toEqual(turns.slice(-5));
  });

  it("records real heights for every turn in the active pane before restore", () => {
    const node = document.createElement("div");
    const active = document.createElement("div");
    active.className = "cached-conversation-pane";
    active.dataset.active = "true";
    const hidden = document.createElement("div");
    hidden.className = "cached-conversation-pane";
    hidden.dataset.active = "false";
    const activeTurns = Array.from({ length: 8 }, () => {
      const turn = document.createElement("article");
      turn.className = "turn";
      active.append(turn);
      return turn;
    });
    const hiddenTurn = document.createElement("article");
    hiddenTurn.className = "turn";
    hidden.append(hiddenTurn);
    node.append(active, hidden);
    for (const turn of [...activeTurns, hiddenTurn]) {
      Object.defineProperty(turn, "offsetHeight", {
        configurable: true,
        get: () => (turn.closest('[data-active="true"]') ? 80 : 260),
      });
    }

    measureActiveConversationForRestore(node);

    expect(active.classList.contains("is-reveal-measure")).toBe(false);
    for (const turn of activeTurns) {
      expect(turn.style.containIntrinsicBlockSize).toBe("auto 80px");
    }
    expect(hiddenTurn.style.containIntrinsicBlockSize).toBe("");
  });
});
