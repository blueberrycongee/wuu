import { act, createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAutoFollowScrollContainer } from "./AutoFollowScroll";
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

  function paint(): void {
    const pending = [...frames.values()];
    frames.clear();
    act(() => pending.forEach((callback) => callback(0)));
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
      paint();
      expect(layout.scrollTop).toBe(contentHeight - height);
      act(() => scrollNode!.dispatchEvent(new Event("scroll")));
      expect(handle?.autoFollowRef.current).toBe(true);
    }
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
