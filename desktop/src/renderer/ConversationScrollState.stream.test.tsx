/**
 * Stress test: simulate a high-frequency token stream and verify the
 * conversation pane's scroll position stays pinned to the bottom edge.
 *
 * The setup:
 * - The conversation has more content than the viewport, so scrollTop
 *   is at scrollHeight - clientHeight when "at the bottom".
 * - We simulate 120 stream ticks in a row, each one bumping
 *   scrollHeight by a few pixels. For each tick we:
 *     1. bump the stubbed layout (the "commit" of the new content);
 *     2. call scheduleStreamScroll (what the server-event path does);
 *     3. fire the RAF callback (what the browser does next frame);
 *     4. dispatch a scroll event (what the browser does after
 *        scrollTop assignment);
 *     5. assert the resulting scroll position is still the bottom.
 *
 * If the scroll position drifts above scrollHeight - clientHeight, the
 * high-frequency stream is racing itself and we need to fix it.
 */
import { act, createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useConversationScrollState } from "./ConversationScrollState";
import type { Turn } from "../shared/protocol";
import { AUTO_FOLLOW_NESTED_SCROLL_ATTR } from "./AutoFollowScroll";
import { WINDOW_RESIZING_CLASS } from "./WindowResizeState";

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

function makeLongTurnsSnapshot(version: number): Turn[] {
  return [
    {
      id: "turn-1",
      items: [],
      items_view: "full",
      status: version % 2 === 0 ? "in_progress" : "completed",
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
      // Real browsers clamp scrollTop into [0, scrollHeight - clientHeight]
      // when scrollTop > scrollHeight - clientHeight. Reproduce that here
      // so the test exercises the same boundary the hook would face in
      // Chromium.
      const max = Math.max(0, layout.scrollHeight - layout.clientHeight);
      layout.scrollTop = Math.max(0, Math.min(v, max));
    },
  });
  return layout;
}

type HookHandle = ReturnType<typeof useConversationScrollState> & {
  conversationScrollRef: { current: HTMLDivElement | null };
};

function Probe({
  turns,
  showConversation = true,
  onReady
}: {
  turns?: Turn[];
  showConversation?: boolean;
  onReady: (handle: HookHandle, node: HTMLDivElement) => void;
}): ReactNode {
  const handle = useConversationScrollState({
    activeThreadID: "thread-1",
    activePane: "primary",
    splitConversation: false,
    primaryTurns: turns ?? makeLongTurns(),
    secondaryTurns: undefined,
    emptyConversation: false,
    initialized: true,
  });
  if (!showConversation) {
    return createElement("section", {
      "data-testid": "settings-placeholder",
    });
  }
  return createElement(
    "div",
    {
      ref: (node: HTMLDivElement | null) => {
        handle.conversationScrollRef.current = node;
        if (node) onReady(handle, node);
      },
      onScroll: () => handle.handleConversationScroll(),
      "data-testid": "scroll-container",
    },
    createElement(
      "div",
      {
        ref: (node: HTMLDivElement | null) => {
          if (node) handle.scrollContentRef.current = node;
        },
        "data-testid": "scroll-content",
      },
      createElement("span", { "data-testid": "message-text" }, "selectable message text"),
    ),
    createElement("div", {
      [AUTO_FOLLOW_NESTED_SCROLL_ATTR]: "true",
      "data-testid": "nested-scroll",
    }),
  );
}

type MockResizeObserverRecord = {
  callback: ResizeObserverCallback;
  observed: Set<Element>;
  disconnected: boolean;
};

describe("useConversationScrollState — high-frequency stream", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;
  let handle: HookHandle | null = null;
  let node: HTMLDivElement | null = null;
  let layout: StubbedLayout | null = null;
  let realRequestAnimationFrame: typeof window.requestAnimationFrame;
  let realCancelAnimationFrame: typeof window.cancelAnimationFrame;
  let rafCallbacks: Map<number, FrameRequestCallback>;
  let nextRafID = 1;
  let resizeObserverGlobal: typeof globalThis & {
    ResizeObserver?: typeof ResizeObserver;
  };
  let realResizeObserver: typeof ResizeObserver | undefined;
  let resizeObservers: MockResizeObserverRecord[];

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    realRequestAnimationFrame = window.requestAnimationFrame;
    realCancelAnimationFrame = window.cancelAnimationFrame;
    rafCallbacks = new Map();
    nextRafID = 1;
    window.requestAnimationFrame = ((callback: FrameRequestCallback) => {
      const handleID = nextRafID;
      nextRafID += 1;
      rafCallbacks.set(handleID, callback);
      return handleID;
    }) as typeof window.requestAnimationFrame;
    window.cancelAnimationFrame = ((handleID: number) => {
      rafCallbacks.delete(handleID);
    }) as typeof window.cancelAnimationFrame;

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
          disconnected: false,
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
        this.record.disconnected = true;
        this.record.observed.clear();
      }
    }
    resizeObserverGlobal.ResizeObserver =
      MockResizeObserver as typeof ResizeObserver;
  });

  afterEach(() => {
    document.getSelection()?.removeAllRanges();
    act(() => {
      root?.unmount();
    });
    root = null;
    handle = null;
    node = null;
    layout = null;
    window.requestAnimationFrame = realRequestAnimationFrame;
    window.cancelAnimationFrame = realCancelAnimationFrame;
    if (realResizeObserver) {
      resizeObserverGlobal.ResizeObserver = realResizeObserver;
    } else {
      Reflect.deleteProperty(resizeObserverGlobal, "ResizeObserver");
    }
    document.body.removeChild(container);
  });

  function mount(opts: {
    scrollHeight: number;
    clientHeight: number;
    turns?: Turn[];
  }): void {
    act(() => {
      root = createRoot(container);
      root.render(
        createElement(Probe, {
          turns: opts.turns,
          onReady: (h, n) => {
            handle = h;
            node = n;
          },
        }),
      );
    });
    if (!node) throw new Error("Probe did not render");
    layout = stubLayout(node, {
      scrollHeight: opts.scrollHeight,
      clientHeight: opts.clientHeight,
      // Land at the bottom of the initial scroll area.
      scrollTop: opts.scrollHeight - opts.clientHeight,
    });
  }

  function rerenderTurns(turns: Turn[]): void {
    if (!root) throw new Error("not mounted");
    act(() => {
      root!.render(
        createElement(Probe, {
          turns,
          onReady: (h, n) => {
            handle = h;
            node = n;
          },
        }),
      );
    });
  }

  function rerenderProbe(opts: {
    turns?: Turn[];
    showConversation?: boolean;
  }): void {
    if (!root) throw new Error("not mounted");
    act(() => {
      root!.render(
        createElement(Probe, {
          turns: opts.turns,
          showConversation: opts.showConversation,
          onReady: (h, n) => {
            handle = h;
            node = n;
          },
        }),
      );
    });
  }

  function flushAnimationFrames(): void {
    const callbacks = Array.from(rafCallbacks.values());
    rafCallbacks.clear();
    const timestamp = performance.now();
    for (const callback of callbacks) {
      callback(timestamp);
    }
  }

  function flushResizeObservers(): void {
    for (const observer of resizeObservers) {
      if (!observer.disconnected) {
        const entries = Array.from(observer.observed).map((target) => {
          const height =
            target === node && layout ? layout.clientHeight : 0;
          return {
            target,
            contentRect: {
              x: 0,
              y: 0,
              top: 0,
              left: 0,
              right: 0,
              bottom: height,
              width: 0,
              height,
              toJSON: () => ({}),
            },
            borderBoxSize: [{ blockSize: height, inlineSize: 0 }],
            contentBoxSize: [{ blockSize: height, inlineSize: 0 }],
            devicePixelContentBoxSize: [{ blockSize: height, inlineSize: 0 }],
          } as unknown as ResizeObserverEntry;
        });
        observer.callback(entries, observer as unknown as ResizeObserver);
      }
    }
  }

  function flushScheduledScroll(): void {
    if (!node) throw new Error("not mounted");
    act(() => {
      flushAnimationFrames();
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
  }

  function fireUserScroll(): void {
    if (!node) throw new Error("not mounted");
    act(() => {
      node!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -80 }));
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
  }

  function nestedScrollNode(): HTMLElement {
    const nested = container.querySelector(
      "[data-testid='nested-scroll']",
    ) as HTMLElement | null;
    if (!nested) throw new Error("nested scroll node not rendered");
    return nested;
  }

  function selectMessageText(collapsed = false): void {
    const text = container.querySelector("[data-testid='message-text']")?.firstChild;
    const selection = document.getSelection();
    if (!text || !selection) throw new Error("message text not rendered");
    const range = document.createRange();
    range.setStart(text, 0);
    range.setEnd(text, collapsed ? 0 : 10);
    selection.removeAllRanges();
    selection.addRange(range);
    document.dispatchEvent(new Event("selectionchange"));
  }

  it("stays pinned to the bottom across 120 fast stream ticks", () => {
    // Start: 2000px of content in a 600px viewport. We are at the
    // bottom (scrollTop = 1400).
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    // 120 ticks, each adding 8px of streamed content. This is
    // representative of a fast stream; 120 frames at 60fps is 2 seconds
    // of streaming.
    for (let tick = 0; tick < 120; tick += 1) {
      act(() => {
        // (1) The server pushed a delta; React commits new content
        //     and the layout grows.
        layout!.scrollHeight += 8;
        // (2) The server-event path calls scheduleStreamScroll. This
        //     registers a RAF, which we then run synchronously below
        //     to simulate the next browser frame.
        handle!.scheduleStreamScroll();
      });
      // (3) The browser runs the RAF callback, which sets
      //     scrollTop = scrollHeight. (4) Then it dispatches the
      //     scroll event after that assignment.
      flushScheduledScroll();

      const bottom = layout.scrollHeight - layout.clientHeight;
      // (5) The scroll position must still be at the bottom.
      expect(layout.scrollTop).toBe(bottom);
    }
  });

  it("stops following streamed growth once message text is selected", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => selectMessageText());
    act(() => {
      // Native scroll anchoring can move scrollTop down with content growth.
      // That passive movement must not count as an explicit return to latest.
      layout!.scrollHeight += 8;
      layout!.scrollTop += 8;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
    const anchoredAt = layout.scrollTop;
    act(() => {
      layout!.scrollHeight += 80;
      handle!.scheduleStreamScroll();
    });
    flushResizeObservers();
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(anchoredAt);
    expect(layout.scrollTop).toBeLessThan(layout.scrollHeight - layout.clientHeight);
  });

  it("resumes following after an explicit downward scroll reaches latest", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => selectMessageText());
    act(() => {
      layout!.scrollHeight += 80;
      node!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: 80 }));
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
      layout!.scrollHeight += 40;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("does not treat a pointer press without scrollbar movement as return intent", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => selectMessageText());
    act(() => {
      node!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      window.dispatchEvent(new Event("pointerup"));
      layout!.scrollHeight += 8;
      layout!.scrollTop += 8;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
      layout!.scrollHeight += 40;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(2000 - 600 + 8);
    expect(layout.scrollTop).toBeLessThan(layout.scrollHeight - layout.clientHeight);
  });

  it("resumes after an active pointer gesture actually scrolls toward latest", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => selectMessageText());
    act(() => {
      node!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      layout!.scrollHeight += 80;
      flushResizeObservers();
    });
    rerenderTurns(makeLongTurnsSnapshot(1));
    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
      window.dispatchEvent(new Event("pointerup"));
      layout!.scrollHeight += 40;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("keeps following when the conversation selection is collapsed", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => selectMessageText(true));
    act(() => {
      layout!.scrollHeight += 80;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("re-anchors immediately when streamed content grows after the stream frame", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      // Markdown block promotion, syntax highlighting, or media layout can
      // change the scrollHeight after the original stream frame has already
      // run. The conversation content observer is the durable signal that
      // the bottom needs to be re-anchored.
      layout!.scrollHeight += 120;
      flushResizeObservers();
    });

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("does not accept a stale programmatic bottom after content grows before the scroll event", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      handle!.scheduleStreamScroll();
      flushAnimationFrames();
      // Chromium can deliver the scroll event from the programmatic bottom
      // assignment after React has already committed more streamed content.
      // The old bottom is no longer the latest view and must not be accepted
      // as "handled".
      layout!.scrollHeight += 96;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("keeps one settle frame for content that grows after the first stream scroll", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      handle!.scheduleStreamScroll();
      flushAnimationFrames();
      // No scroll event or ResizeObserver callback has fired yet, but the
      // text commit lands just after the first scheduled bottom scroll.
      layout!.scrollHeight += 96;
      flushAnimationFrames();
    });

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("re-anchors during the turn snapshot commit before the next animation frame", () => {
    mount({
      scrollHeight: 2000,
      clientHeight: 600,
      turns: makeLongTurnsSnapshot(0),
    });
    if (!layout || !node) throw new Error("not mounted");
    flushScheduledScroll();

    // New non-token content, such as a gray process row after commentary,
    // grows the layout as part of the React commit. Waiting until the next
    // RAF lets the browser paint one frame at the old scrollTop.
    layout.scrollHeight += 96;
    rerenderTurns(makeLongTurnsSnapshot(1));

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("does not re-anchor a turn snapshot commit after the user scrolls away", () => {
    mount({
      scrollHeight: 2000,
      clientHeight: 600,
      turns: makeLongTurnsSnapshot(0),
    });
    if (!layout || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 120;
    });
    fireUserScroll();

    layout.scrollHeight += 96;
    rerenderTurns(makeLongTurnsSnapshot(1));

    expect(layout.scrollTop).toBe(2000 - 600 - 120);
  });

  it("disengages auto-follow when an upward scroll event leaves the bottom without preflight intent", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1400);

    act(() => {
      // Native scrollbar drags and some platform scroll paths can deliver
      // only the scroll event. Once scrollTop has moved upward away from
      // the latest content, auto-follow must stop before final-answer settle
      // frames or fold-collapse re-anchors can pull the user back down.
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 120;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(2000 - 600 - 120);

    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(2000 - 600 - 120);
  });

  it("disengages auto-follow on the first scroll after the conversation remounts with taller content", () => {
    mount({
      scrollHeight: 2000,
      clientHeight: 600,
      turns: makeLongTurnsSnapshot(0),
    });
    if (!layout || !node || !handle) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1400);

    rerenderProbe({
      turns: makeLongTurnsSnapshot(1),
      showConversation: false,
    });
    expect(
      container.querySelector("[data-testid='settings-placeholder']"),
    ).not.toBeNull();

    rerenderProbe({
      turns: makeLongTurnsSnapshot(1),
      showConversation: true,
    });
    node = container.querySelector(
      "[data-testid='scroll-container']",
    ) as HTMLDivElement | null;
    if (!node) throw new Error("conversation did not remount");
    layout = stubLayout(node, {
      scrollHeight: 2400,
      clientHeight: 600,
      scrollTop: 2400 - 600,
    });

    act(() => {
      node!.dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
      );
      layout!.scrollTop = 1700;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(1700);

    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(1700);
  });

  it("disengages scroll-only upward movement after remounting with a stale bottom baseline", () => {
    mount({
      scrollHeight: 2000,
      clientHeight: 600,
      turns: makeLongTurnsSnapshot(0),
    });
    if (!layout || !node || !handle) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1400);

    rerenderProbe({
      turns: makeLongTurnsSnapshot(1),
      showConversation: false,
    });
    expect(
      container.querySelector("[data-testid='settings-placeholder']"),
    ).not.toBeNull();

    rerenderProbe({
      turns: makeLongTurnsSnapshot(1),
      showConversation: true,
    });
    node = container.querySelector(
      "[data-testid='scroll-container']",
    ) as HTMLDivElement | null;
    if (!node) throw new Error("conversation did not remount");
    layout = stubLayout(node, {
      scrollHeight: 2400,
      clientHeight: 600,
      scrollTop: 2400 - 600,
    });

    act(() => {
      // The old baseline is below the remounted bottom. A bare scroll event
      // can still be a real upward user move even when current scrollTop is
      // numerically larger than the stale last scrollTop.
      layout!.scrollTop = 1700;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(1700);

    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(1700);
  });

  it("disengages auto-follow the moment the user scrolls up — no 16px dead zone", () => {
    // Regression gate for the scroll-up "resistance" bug: any wheel-up
    // from the bottom must take auto-follow off immediately, including
    // deltas well inside the old 16px threshold band. Re-arming only
    // on a downward / settled scroll is what makes the scroll feel
    // responsive instead of springing back the moment something
    // triggers `scheduleStreamScroll`.
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    // Park at the bottom and dispatch a scroll event so lastRef is
    // primed to the max position.
    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();
    // Wheel up 8px — well inside the old 16px band. Auto-follow must drop.
    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 8;
    });
    fireUserScroll();

    // Wheel up another 9px (17px total). Follow remains disengaged.
    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 17;
    });
    fireUserScroll();

    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(2000 - 600 - 17);
  });

  it("lets an upward wheel take control before the following scroll event", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      // Model the browser's wheel/default-scroll timing while output is
      // streaming: an auto-follow frame is already queued, the wheel intent
      // arrives, and the compositor moves the viewport before the DOM scroll
      // event is delivered.
      layout!.scrollHeight += 24;
      handle!.scheduleStreamScroll();
      node!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaY: -8 }));
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 8;
    });

    flushScheduledScroll();

    expect(layout.scrollTop).toBe(2000 + 24 - 600 - 8);
  });

  it.each(["keyboard", "touch", "scrollbar"])(
    "yields streaming follow to %s before native scroll delivery",
    (input) => {
      mount({ scrollHeight: 2000, clientHeight: 600 });
      if (!layout || !handle || !node) throw new Error("not mounted");
      act(() => handle!.scheduleStreamScroll());
      flushScheduledScroll();

      act(() => {
        handle!.scheduleStreamScroll();
        if (input === "keyboard") {
          node!.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true }));
        } else if (input === "touch") {
          node!.dispatchEvent(new TouchEvent("touchstart", { touches: [{ clientY: 100 } as Touch] }));
          node!.dispatchEvent(new TouchEvent("touchmove", { touches: [{ clientY: 120 } as Touch] }));
        } else {
          node!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
        }
        // Output commits before the browser's first native scroll step.
        layout!.scrollHeight += 24;
        flushResizeObservers();
        flushAnimationFrames();
      });
      expect(layout.scrollTop).toBe(1400);

      act(() => {
        // The compositor moves before the DOM scroll event. A turn commit
        // and the queued settle frame must not overwrite that movement.
        layout!.scrollTop = 1392;
      });
      rerenderTurns(makeLongTurnsSnapshot(1));
      flushScheduledScroll();
      act(() => {
        layout!.scrollHeight += 80;
        handle!.scheduleStreamScroll();
        flushResizeObservers();
      });
      flushScheduledScroll();
      expect(layout.scrollTop).toBe(1392);

      act(() => {
        node!.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
        layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight;
        node!.dispatchEvent(new Event("scroll"));
        window.dispatchEvent(new Event("pointerup"));
        layout!.scrollHeight += 40;
        handle!.scheduleStreamScroll();
      });
      flushScheduledScroll();
      expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
    },
  );

  it.each([600, 2000])("resumes after a scroll-surface click without movement (height %i)", (height) => {
    mount({ scrollHeight: height, clientHeight: 600 });
    act(() => handle!.scheduleStreamScroll());
    flushScheduledScroll();
    act(() => {
      node!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      layout!.scrollHeight += 800;
      flushResizeObservers();
    });
    rerenderTurns(makeLongTurnsSnapshot(1));
    flushScheduledScroll();
    expect(layout!.scrollTop).toBe(height - 600);
    act(() => window.dispatchEvent(new Event("pointerup")));
    flushScheduledScroll();
    expect(layout!.scrollTop).toBe(layout!.scrollHeight - layout!.clientHeight);
    act(() => {
      layout!.scrollHeight += 40;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();
    expect(layout!.scrollTop).toBe(layout!.scrollHeight - layout!.clientHeight);
  });

  it.each(["paused", "drag", "wheel", "cancel", "submission"])("does not resume a scroll-surface gesture after %s", (reason) => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    act(() => handle!.scheduleStreamScroll());
    flushScheduledScroll();
    act(() => {
      if (reason === "paused") handle!.disableConversationAutoFollow();
      node!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      if (reason === "drag") layout!.scrollTop -= 8;
      if (reason === "wheel") node!.dispatchEvent(new WheelEvent("wheel", { deltaY: -20 }));
      if (reason === "submission") handle!.requestSubmittedQueryScroll("next-message");
      // A drag's native scroll event may still be pending on pointer release.
      window.dispatchEvent(new Event(reason === "cancel" ? "pointercancel" : "pointerup"));
      layout!.scrollHeight += 40;
      flushResizeObservers();
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();
    expect(layout!.scrollTop).toBe(reason === "drag" ? 1392 : 1400);
    if (reason === "submission") expect(handle!.captureConversationScrollPosition()?.submissionPhase).toBe("pending");
  });

  it("keeps auto-follow disabled during smooth jump startup near the bottom", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      handle!.disableConversationAutoFollow();
      // Closing the popover or layout settling can produce a scroll event
      // before smooth scrolling has moved the viewport at all. That event is
      // still at the latest view, but it must not re-arm follow mode.
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));

      // A query-history jump uses smooth scrolling. Chromium can emit an
      // early upward scroll event while the viewport is still inside the
      // bottom threshold band. That must not re-arm follow mode.
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 8;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });
    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(2000 - 600 - 8);
  });

  it("does not let content resize re-enable follow after the user scrolls away", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight - 80;
    });
    fireUserScroll();

    act(() => {
      layout!.scrollHeight += 200;
      flushResizeObservers();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(2000 - 600 - 80);
  });

  it("does not treat nested reasoning scroll as leaving the conversation bottom", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !node || !handle) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1400);

    act(() => {
      nestedScrollNode().dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
      );
    });
    expect(layout.scrollTop).toBe(1400);

    act(() => {
      layout!.scrollHeight += 80;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("keeps following while keyboard and touch input stay inside a nested scroller", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle) throw new Error("not mounted");
    act(() => handle!.scheduleStreamScroll());
    flushScheduledScroll();
    act(() => {
      const nested = nestedScrollNode();
      nested.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true }));
      nested.dispatchEvent(new TouchEvent("touchstart", { touches: [{ clientY: 100 } as Touch], bubbles: true }));
      nested.dispatchEvent(new TouchEvent("touchmove", { touches: [{ clientY: 120 } as Touch], bubbles: true }));
      nested.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      layout!.scrollHeight += 40;
      flushResizeObservers();
    });
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("disengages auto-follow when a nested scroll chains upward into the conversation", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !node || !handle) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1400);

    act(() => {
      nestedScrollNode().dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: -80 }),
      );
      layout!.scrollTop = 1180;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(1180);

    act(() => {
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(1180);
  });

  it("keeps following when layout shrink clamps the viewport to the new bottom", () => {
    mount({ scrollHeight: 2200, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1600);

    act(() => {
      // A completed process fold can shrink above the viewport. Chromium
      // clamps scrollTop from the old max to the new max and emits a scroll
      // event even though the user did not scroll up.
      layout!.scrollHeight = 1700;
      layout!.scrollTop = 1100;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    act(() => {
      layout!.scrollHeight += 120;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });

  it("does not re-enable follow when layout shrink clamps an already-away viewport to the new bottom", () => {
    mount({ scrollHeight: 2200, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();
    expect(layout.scrollTop).toBe(1600);

    act(() => {
      layout!.scrollTop = 1200;
    });
    fireUserScroll();

    act(() => {
      // The process fold can collapse after final text settles. If the
      // user was already reading above the bottom, Chromium may still clamp
      // scrollTop to the new max and emit a scroll event. That layout clamp
      // must not mean the user asked to resume following latest content.
      layout!.scrollHeight = 1700;
      layout!.scrollTop = 1100;
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(layout.scrollTop).toBe(1100);

    act(() => {
      layout!.scrollHeight += 120;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(1100);
  });

  it("follows keyboard-sized viewport changes without flashing scroll feedback", () => {
    mount({ scrollHeight: 1600, clientHeight: 600 });
    if (!node || !layout || !handle) throw new Error("not mounted");
    const scrollNode = node;
    scrollNode.classList.remove("scrollbar-visible");
    for (const height of [540, 420, 300, 420, 540, 600]) {
      act(() => {
        layout!.clientHeight = height;
        flushResizeObservers();
        scrollNode.dispatchEvent(new Event("scroll"));
      });
      expect(layout.scrollTop).toBe(layout.scrollHeight - height);
      expect(handle.captureConversationScrollPosition()?.autoFollow).toBe(true);
      expect(scrollNode.classList.contains("scrollbar-visible")).toBe(false);
    }
    act(() => {
      scrollNode.dispatchEvent(new WheelEvent("wheel", { deltaY: -30 }));
      layout!.scrollTop -= 30;
      scrollNode.dispatchEvent(new Event("scroll"));
    });
    expect(scrollNode.classList.contains("scrollbar-visible")).toBe(true);
    expect(handle.captureConversationScrollPosition()?.autoFollow).toBe(false);
  });

  it("keeps native viewport clamps quiet even when scroll arrives before resize", () => {
    mount({ scrollHeight: 1600, clientHeight: 300 });
    if (!node || !layout || !handle) throw new Error("not mounted");
    act(() => {
      flushResizeObservers();
      node!.dispatchEvent(new Event("scroll"));
    });
    node.classList.remove("scrollbar-visible");
    act(() => {
      layout!.clientHeight = 600;
      node!.scrollTop = layout!.scrollTop;
      node!.dispatchEvent(new Event("scroll"));
    });
    expect(node.classList.contains("scrollbar-visible")).toBe(false);
    expect(handle.captureConversationScrollPosition()?.autoFollow).toBe(true);
    act(() => flushResizeObservers());
    expect(layout.scrollTop).toBe(1000);
    expect(node.classList.contains("scrollbar-visible")).toBe(false);
  });

  it("preserves history reading through keyboard opening, dismissal and new output", () => {
    mount({ scrollHeight: 1600, clientHeight: 600 });
    if (!node || !layout || !handle) throw new Error("not mounted");
    act(() => {
      node!.dispatchEvent(new WheelEvent("wheel", { deltaY: -200 }));
      layout!.scrollTop = 100;
      node!.dispatchEvent(new Event("scroll"));
    });
    for (const height of [450, 300, 450, 600]) {
      act(() => {
        layout!.clientHeight = height;
        layout!.scrollHeight += 20;
        flushResizeObservers();
        handle!.scheduleStreamScroll();
        flushAnimationFrames();
      });
      expect(layout.scrollTop).toBe(100);
      expect(handle.captureConversationScrollPosition()?.autoFollow).toBe(false);
    }
  });

  it("settles reflow before paint without following into reserved tail space", () => {
    mount({ scrollHeight: 2200, clientHeight: 600 });
    if (!layout || !node || !handle) throw new Error("not mounted");
    node.style.setProperty("--session-tail-space", "180px");
    flushResizeObservers();
    expect(layout.scrollTop).toBe(1420);

    document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
    for (const [height, contentHeight] of [[400, 2400], [700, 2000], [500, 2300]]) {
      layout.clientHeight = height;
      layout.scrollHeight = contentHeight;
      act(() => flushResizeObservers());
      // ResizeObserver runs after rAF and layout, before paint. Waiting for
      // another rAF would paint one frame with the previous scroll offset.
      expect(layout.scrollTop).toBe(contentHeight - height - 180);
      act(() => flushAnimationFrames());
      expect(layout.scrollTop).toBe(contentHeight - height - 180);
    }

    act(() => handle!.disableConversationAutoFollow());
    layout.scrollTop = 300;
    layout.scrollHeight = 2600;
    act(() => flushResizeObservers());
    expect(layout.scrollTop).toBe(300);
  });

  it("keeps latest content continuously pinned during live window resize", () => {
    // User parked at the bottom of a tall conversation. Reflow changes the
    // real bottom while the window is being dragged, so coalesce observer
    // notifications into one scroll update on the next animation frame.
    mount({ scrollHeight: 2200, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();
    // Fire the ResizeObserver once on mount (class OFF) so the hook
    // captures the pre-resize baseline (600px). If we skip this, the
    // first observer fire happens after the resize has already started
    // and the "baseline" would be the post-shrink height — making the
    // shift math always land at zero.
    flushResizeObservers();
    expect(layout.scrollTop).toBe(1600);

    // Simulate a live window drag: the main process / window emits the
    // resizing marker, the viewport shrinks, and the ResizeObserver
    // fires while we are still inside the drag.
    act(() => {
      document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
      layout!.clientHeight = 400;
      flushResizeObservers();
    });

    // The message container stays in normal flow and is pinned before paint.
    expect(layout.scrollTop).toBe(2200 - 400);
    act(() => flushAnimationFrames());
    expect(layout.scrollTop).toBe(2200 - 400);
    // The user is still following, so the next stream tick sticks to the
    // new bottom.

    // The drag ends: the resizing marker drops, the ResizeObserver
    // fires one more time.
    act(() => {
      document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
      flushResizeObservers();
    });

    expect(layout.scrollTop).toBe(2200 - 400);
  });

  it("does not read scroll metrics when a layout scroll event fires during live window resize", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !node) throw new Error("not mounted");
    flushScheduledScroll();

    let liveResizeMetricReads = 0;
    Object.defineProperty(node, "scrollHeight", {
      configurable: true,
      get: () => {
        if (document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)) {
          liveResizeMetricReads += 1;
        }
        return layout!.scrollHeight;
      },
    });
    Object.defineProperty(node, "clientHeight", {
      configurable: true,
      get: () => {
        if (document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)) {
          liveResizeMetricReads += 1;
        }
        return layout!.clientHeight;
      },
    });

    act(() => {
      document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    expect(liveResizeMetricReads).toBe(0);

    act(() => {
      document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
    });
  });

  it("keeps following at the hard boundary after downward inertia", () => {
    mount({ scrollHeight: 2000, clientHeight: 600 });
    if (!layout || !handle || !node) throw new Error("not mounted");
    flushScheduledScroll();

    act(() => {
      layout!.scrollTop = 900;
    });
    fireUserScroll();

    act(() => {
      layout!.scrollTop = layout!.scrollHeight - layout!.clientHeight;
      node!.dispatchEvent(
        new WheelEvent("wheel", { bubbles: true, deltaY: 64, deltaMode: 0 }),
      );
      node!.dispatchEvent(new Event("scroll", { bubbles: false }));
    });

    act(() => {
      layout!.scrollHeight += 80;
      handle!.scheduleStreamScroll();
    });
    flushScheduledScroll();

    expect(layout.scrollTop).toBe(layout.scrollHeight - layout.clientHeight);
  });
});
