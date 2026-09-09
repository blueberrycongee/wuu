import { act, createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  useConversationScrollState,
  wheelDeltaPixels,
} from "./ConversationScrollState";
import type { Turn } from "../shared/protocol";

function makeLongTurns(): Turn[] {
  return [
    {
      id: "turn-1",
      items: [],
      items_view: "full",
      status: "completed",
    },
  ];
}

type StubbedLayout = {
  scrollHeight: number;
  clientHeight: number;
  scrollTop: number;
};

function stubLayout(node: HTMLElement, opts: Partial<StubbedLayout>): StubbedLayout {
  const layout = {
    scrollHeight: opts.scrollHeight ?? 1000,
    clientHeight: opts.clientHeight ?? 600,
    scrollTop: opts.scrollTop ?? 0,
  };
  Object.defineProperty(node, "scrollHeight", {
    configurable: true,
    get: () => layout.scrollHeight,
  });
  Object.defineProperty(node, "clientHeight", {
    configurable: true,
    get: () => layout.clientHeight,
  });
  Object.defineProperty(node, "scrollTop", {
    configurable: true,
    get: () => layout.scrollTop,
    set: (v: number) => {
      const max = Math.max(0, layout.scrollHeight - layout.clientHeight);
      layout.scrollTop = Math.max(0, Math.min(v, max));
    },
  });
  return layout;
}

function controlAnimationFrames(): (now: number) => void {
  const pending = new Map<number, FrameRequestCallback>();
  let id = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
    pending.set(++id, callback);
    return id;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation((key) => { pending.delete(key); });
  return (now) => act(() => {
    const callbacks = [...pending.values()];
    pending.clear();
    callbacks.forEach((callback) => callback(now));
  });
}

function Probe({
  activeThreadID,
  nativeScrollBounce,
}: {
  activeThreadID?: string;
  nativeScrollBounce?: boolean;
}): ReactNode {
  const h = useConversationScrollState({
    activeThreadID,
    activePane: "primary",
    splitConversation: false,
    primaryTurns: makeLongTurns(),
    secondaryTurns: undefined,
    emptyConversation: false,
    initialized: true,
    nativeScrollBounce,
  });
  return createElement(
    "div",
    {
      ref: (node: HTMLDivElement | null) => {
        h.conversationScrollRef.current = node;
      },
      onScroll: () => h.handleConversationScroll(),
      "data-testid": "scroll-container",
    },
    createElement("button", {
      type: "button",
      onClick: h.requestSubmittedQueryScroll,
      "data-testid": "request-submitted-query-scroll",
    }),
    createElement("div", {
      ref: (node: HTMLDivElement | null) => {
        h.scrollContentRef.current = node;
      },
      "data-testid": "scroll-content",
    }),
  );
}

describe("useConversationScrollState — thread scroll snapshots", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;
  let scrollNode: HTMLDivElement | null = null;
  let layout: StubbedLayout | null = null;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(() => {
    act(() => {
      root?.unmount();
    });
    root = null;
    scrollNode = null;
    layout = null;
    document.body.removeChild(container);
    vi.restoreAllMocks();
  });

  function mount(opts: {
    scrollHeight: number;
    clientHeight: number;
    initialScrollTop: number;
    activeThreadID?: string;
    nativeScrollBounce?: boolean;
  }): HTMLDivElement {
    act(() => {
      root = createRoot(container);
      root.render(
        createElement(Probe, {
          activeThreadID: opts.activeThreadID ?? "thread-1",
          nativeScrollBounce: opts.nativeScrollBounce,
        }),
      );
    });

    const node = container.querySelector(
      "[data-testid='scroll-container']",
    ) as HTMLDivElement | null;
    if (!node) throw new Error("Probe did not render");
    scrollNode = node;
    // Install layout stubs after React has finished its mount-time
    // useLayoutEffect, which has already attempted to scroll the
    // unstubbed element. Subsequent scroll events (and our manual
    // dispatchEvent) will read the stubbed values.
    layout = stubLayout(node, {
      scrollHeight: opts.scrollHeight,
      clientHeight: opts.clientHeight,
      scrollTop: opts.initialScrollTop,
    });
    return node;
  }

  function switchThread(activeThreadID?: string): void {
    if (!root) throw new Error("not mounted");
    act(() => {
      root!.render(createElement(Probe, { activeThreadID }));
    });
  }

  function fireScroll(): void {
    if (!scrollNode) throw new Error("not mounted");
    act(() => {
      scrollNode!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
  }

  function fireUserScroll(): void {
    if (!scrollNode) throw new Error("not mounted");
    act(() => {
      scrollNode!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -80 }));
      scrollNode!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
  }

  function setScrollTop(top: number): void {
    if (!layout) throw new Error("not mounted");
    layout.scrollTop = top;
  }

  it("snaps a shorter thread to its bottom when switching sessions", () => {
    mount({
      activeThreadID: "thread-long",
      scrollHeight: 2400,
      clientHeight: 600,
      initialScrollTop: 2400 - 600,
    });
    fireScroll();

    if (!layout) throw new Error("not mounted");
    layout.scrollHeight = 900;
    switchThread("thread-short");
    expect(layout.scrollTop).toBe(300);

    fireScroll();
  });

  it("restores a thread's saved away-from-bottom position when switching back", () => {
    mount({
      activeThreadID: "thread-a",
      scrollHeight: 2400,
      clientHeight: 600,
      initialScrollTop: 2400 - 600,
    });
    fireScroll();

    setScrollTop(520);
    fireUserScroll();

    if (!layout) throw new Error("not mounted");
    layout.scrollHeight = 1200;
    switchThread("thread-b");
    expect(layout.scrollTop).toBe(600);
    fireScroll();

    layout.scrollHeight = 2400;
    switchThread("thread-a");
    expect(layout.scrollTop).toBe(520);
    fireScroll();
  });

  it("keeps a thread's scroll snapshot while a non-conversation tab is active", () => {
    mount({
      activeThreadID: "thread-a",
      scrollHeight: 2200,
      clientHeight: 600,
      initialScrollTop: 2200 - 600,
    });
    fireScroll();

    setScrollTop(480);
    fireUserScroll();

    switchThread(undefined);
    if (!layout) throw new Error("not mounted");
    layout.scrollTop = 0;
    fireScroll();

    switchThread("thread-a");
    expect(layout.scrollTop).toBe(480);
    fireScroll();
  });

  it("finishes one submit motion while the diff receipt shrinks and layout signals keep arriving", () => {
    mount({
      activeThreadID: "thread-a",
      scrollHeight: 1600,
      clientHeight: 600,
      initialScrollTop: 1000,
    });
    fireScroll();
    const frame = controlAnimationFrames();

    act(() => {
      container
        .querySelector<HTMLButtonElement>(
          "[data-testid='request-submitted-query-scroll']",
        )
        ?.click();
    });

    // An unchanged old-bottom frame must not finish before the new turn exists.
    frame(0);
    fireScroll();
    if (!layout || !root) throw new Error("not mounted");
    const tops: number[] = [];
    for (const [time, height] of [[40, 2100], [90, 2000], [150, 1940], [230, 1900]]) {
      layout.scrollHeight = height;
      act(() => { root!.render(createElement(Probe, { activeThreadID: "thread-a" })); });
      frame(time);
      fireScroll();
      tops.push(layout.scrollTop);
    }
    // Motion starts before the card finishes, and reaches the final bottom on
    // the original deadline despite repeated render/resize follow requests.
    expect(tops[0]).toBeGreaterThan(1000);
    expect(tops[0]).toBeLessThan(1500);
    expect(tops.at(-1)).toBe(1300);
    frame(450);
    expect(layout.scrollTop).toBe(1300);
    layout.scrollHeight = 2000;
    act(() => { root!.render(createElement(Probe, { activeThreadID: "thread-a" })); });
    expect(layout.scrollTop).toBe(1400);
  });

  it("follows immediately when reduced motion is enabled", () => {
    mount({ activeThreadID: "thread-a", scrollHeight: 1600, clientHeight: 600, initialScrollTop: 1000 });
    fireScroll();
    const frame = controlAnimationFrames();
    vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true } as MediaQueryList);
    if (!layout || !root) throw new Error("not mounted");
    layout.scrollHeight = 1900;
    act(() => { container.querySelector<HTMLButtonElement>("[data-testid='request-submitted-query-scroll']")!.click(); });
    expect(layout.scrollTop).toBe(1300);
    expect(window.requestAnimationFrame).not.toHaveBeenCalled();
    frame(230);
    expect(layout.scrollTop).toBe(1300);
  });

  it("lets an upward user scroll interrupt a submit motion", () => {
    const node = mount({
      activeThreadID: "thread-a", scrollHeight: 1600, clientHeight: 600, initialScrollTop: 1000,
    });
    fireScroll();
    const frame = controlAnimationFrames();
    act(() => { container.querySelector<HTMLButtonElement>("[data-testid='request-submitted-query-scroll']")!.click(); });
    if (!layout || !root) throw new Error("not mounted");
    layout.scrollHeight = 2100;
    frame(0);
    frame(80);
    act(() => { node.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -100 })); });
    layout.scrollTop -= 100;
    fireScroll();
    const stoppedAt = layout.scrollTop;
    frame(230);
    layout.scrollHeight = 2200;
    act(() => { root!.render(createElement(Probe, { activeThreadID: "thread-a" })); });
    frame(450);
    expect(layout.scrollTop).toBe(stoppedAt);
  });

  it("cancels submit frames when switching conversations", () => {
    mount({ activeThreadID: "thread-a", scrollHeight: 1600, clientHeight: 600, initialScrollTop: 1000 });
    fireScroll();
    const frame = controlAnimationFrames();
    act(() => { container.querySelector<HTMLButtonElement>("[data-testid='request-submitted-query-scroll']")!.click(); });
    if (!layout) throw new Error("not mounted");
    layout.scrollHeight = 2100;
    frame(0);
    frame(80);
    switchThread("thread-b");
    const restoredTop = layout.scrollTop;
    frame(230);
    expect(layout.scrollTop).toBe(restoredTop);
  });

  it("keeps a hard boundary on platforms without native scroll bounce", () => {
    vi.useFakeTimers();
    try {
      const node = mount({
        activeThreadID: "thread-a",
        scrollHeight: 2400,
        clientHeight: 600,
        initialScrollTop: 2400 - 600,
      });
      fireScroll();
      setScrollTop(520);
      fireUserScroll();

      const content = container.querySelector(
        "[data-testid='scroll-content']",
      ) as HTMLDivElement | null;
      if (!content || !layout) throw new Error("not mounted");

      const max = layout.scrollHeight - layout.clientHeight;
      act(() => {
        layout!.scrollTop = max - 40;
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 120, deltaMode: 0 }),
        );
        layout!.scrollTop = max;
        node.dispatchEvent(new Event("scroll", { bubbles: false }));
      });

      expect(content.style.transform).toBe("");

      act(() => {
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 160, deltaMode: 0 }),
        );
      });
      expect(content.style.transform).toBe("");

      act(() => {
        const momentumEvent = new WheelEvent("wheel", {
          bubbles: true,
          deltaY: 100,
          deltaMode: 0,
        });
        Object.defineProperty(momentumEvent, "momentum", { value: true });
        node.dispatchEvent(momentumEvent);
      });
      expect(content.style.transform).toBe("");

      act(() => {
        vi.advanceTimersByTime(32);
      });
      expect(content.style.transform).toBe("");

      act(() => {
        vi.advanceTimersByTime(440);
      });
      expect(content.style.transform).toBe("");

      act(() => {
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 160, deltaMode: 0 }),
        );
      });
      expect(content.style.transform).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("leaves native rubber-band control active until the trackpad gesture ends", () => {
    vi.useFakeTimers();
    try {
      const node = mount({
        activeThreadID: "thread-a",
        scrollHeight: 2400,
        clientHeight: 600,
        initialScrollTop: 1800,
        nativeScrollBounce: true,
      });
      const content = container.querySelector(
        "[data-testid='scroll-content']",
      ) as HTMLDivElement | null;
      if (!content || !layout) throw new Error("not mounted");

      act(() => {
        layout!.scrollTop = 520;
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
        );
        node.dispatchEvent(new Event("scroll", { bubbles: false }));
        layout!.scrollTop = 1800;
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 160 }),
        );
        node.dispatchEvent(new Event("scroll", { bubbles: false }));
        vi.advanceTimersByTime(2_000);
      });

      expect(node.style.overscrollBehaviorY).toBe("contain");
      expect(content.style.transform).toBe("");

      act(() => {
        node.dispatchEvent(new Event("scrollend"));
      });
      expect(node.style.overscrollBehaviorY).toBe("none");

      act(() => {
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 160 }),
        );
      });
      expect(node.style.overscrollBehaviorY).toBe("none");
      expect(content.style.transform).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not synthesize overscroll after returning from older content", () => {
    vi.useFakeTimers();
    try {
      const node = mount({
        activeThreadID: "thread-a",
        scrollHeight: 2400,
        clientHeight: 600,
        initialScrollTop: 2400 - 600,
      });
      fireScroll();
      setScrollTop(520);
      fireUserScroll();

      const content = container.querySelector(
        "[data-testid='scroll-content']",
      ) as HTMLDivElement | null;
      if (!content || !layout) throw new Error("not mounted");

      const max = layout.scrollHeight - layout.clientHeight;
      act(() => {
        layout!.scrollTop = max - 40;
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 40, deltaMode: 0 }),
        );
        layout!.scrollTop = max;
        node.dispatchEvent(new Event("scroll", { bubbles: false }));
      });

      expect(content.style.transform).toBe("");

      act(() => {
        node.dispatchEvent(
          new WheelEvent("wheel", { bubbles: true, deltaY: 80, deltaMode: 0 }),
        );
      });
      expect(content.style.transform).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not rubber-band a downward wheel that is already following latest", () => {
    const node = mount({
      activeThreadID: "thread-a",
      scrollHeight: 2400,
      clientHeight: 600,
      initialScrollTop: 2400 - 600,
    });
    fireScroll();

    const content = container.querySelector(
      "[data-testid='scroll-content']",
    ) as HTMLDivElement | null;
    if (!content) throw new Error("not mounted");

    act(() => {
      node.dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: 120, deltaMode: 0 }),
      );
    });
    expect(content.style.transform).toBe("");
  });
});

describe("conversation wheel helpers", () => {
  it("normalizes wheel deltas from line and page modes", () => {
    expect(
      wheelDeltaPixels({ deltaY: 80, deltaMode: 0 } as WheelEvent, 600),
    ).toBe(80);
    expect(
      wheelDeltaPixels({ deltaY: 3, deltaMode: 1 } as WheelEvent, 600),
    ).toBe(48);
    expect(
      wheelDeltaPixels({ deltaY: 1, deltaMode: 2 } as WheelEvent, 600),
    ).toBe(600);
  });
});

type MockResizeObserverRecord = {
  callback: ResizeObserverCallback;
  observed: Set<Element>;
};

describe("useConversationScrollState — dock composer height", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;
  let resizeObserverGlobal: typeof globalThis & {
    ResizeObserver?: typeof ResizeObserver;
  };
  let realResizeObserver: typeof ResizeObserver | undefined;
  let resizeObservers: MockResizeObserverRecord[];

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    resizeObserverGlobal = globalThis as typeof globalThis & {
      ResizeObserver?: typeof ResizeObserver;
    };
    realResizeObserver = resizeObserverGlobal.ResizeObserver;
    resizeObservers = [];

    class MockResizeObserver {
      private readonly record: MockResizeObserverRecord;

      constructor(callback: ResizeObserverCallback) {
        this.record = {
          callback,
          observed: new Set<Element>(),
        };
        resizeObservers.push(this.record);
      }

      observe(target: Element): void {
        this.record.observed.add(target);
      }

      unobserve(target: Element): void {
        this.record.observed.delete(target);
      }

      disconnect(): void {
        this.record.observed.clear();
      }
    }

    resizeObserverGlobal.ResizeObserver =
      MockResizeObserver as typeof ResizeObserver;
  });

  afterEach(() => {
    act(() => {
      root?.unmount();
    });
    root = null;
    if (realResizeObserver) {
      resizeObserverGlobal.ResizeObserver = realResizeObserver;
    } else {
      Reflect.deleteProperty(resizeObserverGlobal, "ResizeObserver");
    }
    document.body.removeChild(container);
  });

  function DockComposerProbe(): ReactNode {
    const h = useConversationScrollState({
      activeThreadID: "thread-1",
      activePane: "primary",
      splitConversation: false,
      primaryTurns: makeLongTurns(),
      secondaryTurns: undefined,
      emptyConversation: false,
      initialized: true,
    });
    return createElement(
      "section",
      {
        ref: (node: HTMLElement | null) => {
          h.conversationPaneRef.current = node;
        },
        "data-testid": "conversation-pane",
      },
      createElement("div", {
        ref: (node: HTMLDivElement | null) => {
          h.conversationScrollRef.current = node;
        },
        "data-testid": "scroll-container",
      }),
      createElement(
        "footer",
        {
          ref: h.dockComposerRef,
          "data-testid": "dock-composer",
        },
        createElement("div", {
          className: "composer-frame",
          "data-testid": "composer-frame",
        }),
      ),
    );
  }

  function mountDockComposerProbe(): {
    pane: HTMLElement;
    dockComposer: HTMLElement;
    frame: HTMLElement;
  } {
    act(() => {
      root = createRoot(container);
      root.render(createElement(DockComposerProbe));
    });

    const pane = container.querySelector<HTMLElement>(
      "[data-testid='conversation-pane']",
    );
    const dockComposer = container.querySelector<HTMLElement>(
      "[data-testid='dock-composer']",
    );
    const frame = container.querySelector<HTMLElement>(
      "[data-testid='composer-frame']",
    );
    if (!pane || !dockComposer || !frame) {
      throw new Error("DockComposerProbe did not render");
    }
    return { pane, dockComposer, frame };
  }

  function stubRectHeight(node: HTMLElement, height: number): void {
    node.getBoundingClientRect = () =>
      ({
        x: 0,
        y: 0,
        top: 0,
        right: 0,
        bottom: height,
        left: 0,
        width: 800,
        height,
        toJSON: () => ({}),
      }) as DOMRect;
  }

  function flushResizeObserversFor(target: Element): void {
    act(() => {
      for (const observer of resizeObservers) {
        if (observer.observed.has(target)) {
          observer.callback([], observer as unknown as ResizeObserver);
        }
      }
    });
  }

  it("includes the expanded composer offset in the dock composer height token", () => {
    const { pane, dockComposer, frame } = mountDockComposerProbe();
    stubRectHeight(dockComposer, 168);

    flushResizeObserversFor(dockComposer);
    expect(pane.style.getPropertyValue("--dock-composer-height")).toBe("168px");

    frame.style.setProperty("--composer-expanded-offset", "284px");
    flushResizeObserversFor(frame);

    expect(pane.style.getPropertyValue("--dock-composer-height")).toBe("452px");
  });
});
