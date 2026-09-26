/**
 * Tests for JumpToLatestPill — the self-contained "跳到最新" pill (issue #5).
 * The pill owns its scroll listener against the containerRef it is given,
 * shows only when the container is scrolled away from the bottom, and
 * smooth-scrolls that same container (and only it) on click. jsdom does no
 * layout, so scroll geometry is stubbed directly on the container node.
 */
import { act } from "react";
import { createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { JumpToLatestPill } from "./JumpToLatestPill";
import { readBrowserPiPHostLayout } from "./BrowserPiPHostReporter";
import {
  WINDOW_RESIZE_SETTLE_DELAY_MS,
  WINDOW_RESIZING_CLASS,
} from "./WindowResizeState";

let mountedRoots: Root[] = [];
let mountedContainers: HTMLElement[] = [];
let restoreResizeObserver: (() => void) | undefined;

afterEach(() => {
  for (const root of mountedRoots) {
    act(() => {
      root.unmount();
    });
  }
  for (const container of mountedContainers) {
    container.remove();
  }
  mountedRoots = [];
  mountedContainers = [];
  restoreResizeObserver?.();
  restoreResizeObserver = undefined;
  document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
  vi.useRealTimers();
});

type ScrollGeometry = {
  scrollHeight: number;
  clientHeight: number;
  scrollTop: number;
};

function scrollContainer(geometry: ScrollGeometry): {
  node: HTMLDivElement;
  scrollTo: ReturnType<typeof vi.fn>;
  setScrollTop: (top: number) => void;
} {
  const node = document.createElement("div");
  document.body.appendChild(node);
  mountedContainers.push(node);
  let scrollTop = geometry.scrollTop;
  Object.defineProperty(node, "scrollHeight", {
    configurable: true,
    get: () => geometry.scrollHeight,
  });
  Object.defineProperty(node, "clientHeight", {
    configurable: true,
    get: () => geometry.clientHeight,
  });
  Object.defineProperty(node, "scrollTop", {
    configurable: true,
    get: () => scrollTop,
    set: (value: number) => {
      scrollTop = value;
    },
  });
  const scrollTo = vi.fn();
  Object.defineProperty(node, "scrollTo", {
    configurable: true,
    value: scrollTo,
  });
  return {
    node,
    scrollTo,
    setScrollTop: (top: number) => {
      scrollTop = top;
    },
  };
}

function stubRect(node: HTMLElement, rect: Partial<DOMRect>): void {
  node.getBoundingClientRect = () =>
    ({
      left: 0,
      top: 0,
      right: 0,
      bottom: 0,
      width: 0,
      height: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
      ...rect,
    }) as DOMRect;
}

it("keeps an inline companion mounted while adding and removing the history jump action", () => {
  const { node, setScrollTop, scrollTo } = scrollContainer({ scrollHeight: 1500, clientHeight: 500, scrollTop: 1000 });
  const host = document.createElement("div");
  document.body.append(host);
  mountedContainers.push(host);
  const root = createRoot(host);
  mountedRoots.push(root);
  const open = vi.fn();
  act(() => root.render(createElement(JumpToLatestPill, {
    containerRef: { current: node }, bottomAnchor: null, inline: true,
    companion: createElement("button", { onClick: open, "data-companion": true }, "Running sessions"),
  })));
  const companion = host.querySelector<HTMLButtonElement>("[data-companion]")!;
  expect(host.querySelectorAll("button")).toHaveLength(1);
  act(() => { setScrollTop(100); node.dispatchEvent(new Event("scroll")); });
  expect(host.querySelectorAll("button")).toHaveLength(2);
  expect(host.querySelector("[data-companion]")).toBe(companion);
  act(() => host.querySelector<HTMLButtonElement>(".jump-to-latest-pill")!.click());
  expect(scrollTo).toHaveBeenCalledWith({ top: 1000, behavior: "smooth" });
  act(() => { setScrollTop(1000); node.dispatchEvent(new Event("scroll")); });
  expect(host.querySelectorAll("button")).toHaveLength(1);
  expect(host.querySelector("[data-companion]")).toBe(companion);
  act(() => companion.click());
  expect(open).toHaveBeenCalledOnce();
});

it("does not displace the browser preview when the jump action appears", () => {
  const { node, setScrollTop } = scrollContainer({ scrollHeight: 1500, clientHeight: 500, scrollTop: 1000 });
  node.setAttribute("data-pip-anchor-host", "conversation");
  stubRect(node, { left: 0, top: 0, width: 800, height: 600 });
  mountPill(node);
  const before = readBrowserPiPHostLayout(document);
  act(() => { setScrollTop(100); node.dispatchEvent(new Event("scroll")); });
  const pill = document.querySelector<HTMLElement>(".jump-to-latest-pill")!;
  expect(pill).not.toBeNull();
  stubRect(pill, { left: 650, top: 550, width: 120, height: 30 });
  expect(readBrowserPiPHostLayout(document)).toEqual(before);
});

function mountPill(node: HTMLElement): HTMLElement {
  const host = document.createElement("div");
  node.appendChild(host);
  const anchor = document.createElement("div");
  document.body.appendChild(anchor);
  mountedContainers.push(anchor);
  const root = createRoot(host);
  act(() => {
    root.render(
      createElement(JumpToLatestPill, {
        containerRef: { current: node },
        bottomAnchor: anchor,
      }),
    );
  });
  mountedRoots.push(root);
  return document.body;
}

type MockResizeObserverRecord = {
  callback: ResizeObserverCallback;
  observed: Set<Element>;
};

function installMockResizeObserver(): MockResizeObserverRecord[] {
  const records: MockResizeObserverRecord[] = [];
  const globalWithResizeObserver = globalThis as typeof globalThis & {
    ResizeObserver?: typeof ResizeObserver;
  };
  const realResizeObserver = globalWithResizeObserver.ResizeObserver;
  class MockResizeObserver {
    private readonly record: MockResizeObserverRecord;

    constructor(callback: ResizeObserverCallback) {
      this.record = { callback, observed: new Set() };
      records.push(this.record);
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
  globalWithResizeObserver.ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;
  restoreResizeObserver = () => {
    if (realResizeObserver) {
      globalWithResizeObserver.ResizeObserver = realResizeObserver;
    } else {
      Reflect.deleteProperty(globalWithResizeObserver, "ResizeObserver");
    }
  };
  return records;
}

function flushResizeObservers(
  records: MockResizeObserverRecord[],
  target?: Element,
): void {
  for (const record of records) {
    const observedTargets = target
      ? Array.from(record.observed).filter((observed) => observed === target)
      : Array.from(record.observed);
    if (observedTargets.length === 0) {
      continue;
    }
    const entries = observedTargets.map((observed) => ({
      target: observed,
      contentRect: {
        x: 0,
        y: 0,
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        width: 0,
        height: 0,
        toJSON: () => ({}),
      },
    })) as ResizeObserverEntry[];
    record.callback(entries, {} as ResizeObserver);
  }
}

describe("JumpToLatestPill", () => {
  it("stays hidden at the bottom and appears when scrolled away", () => {
    const { node, setScrollTop } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 600, // exactly at the bottom
    });
    const host = mountPill(node);
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();

    act(() => {
      setScrollTop(0); // 600px from the bottom > 80px threshold
      node.dispatchEvent(new Event("scroll"));
    });
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();
  });

  it("shows immediately when mounted into an already scrolled-away container", () => {
    const { node } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 0,
    });
    const host = mountPill(node);
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();
  });

  it("smooth-scrolls its own container to the bottom on click", () => {
    const { node, scrollTo } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 0,
    });
    const host = mountPill(node);
    act(() => {
      host
        .querySelector<HTMLButtonElement>(".jump-to-latest-pill")
        ?.click();
    });
    expect(scrollTo).toHaveBeenCalledWith({ top: 600, behavior: "smooth" });
  });

  it("treats leftover submission tail as already at latest", () => {
    const { node, setScrollTop, scrollTo } = scrollContainer({
      scrollHeight: 2000,
      clientHeight: 600,
      scrollTop: 920,
    });
    const content = document.createElement("div");
    content.className = "scroll-region-content";
    content.style.paddingBottom = "480px";
    node.append(content);
    const host = document.createElement("div");
    content.append(host);
    const root = createRoot(host);
    mountedRoots.push(root);
    act(() => {
      root.render(
        createElement(JumpToLatestPill, {
          containerRef: { current: node },
          bottomAnchor: null,
          inline: true,
        }),
      );
    });
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();

    act(() => {
      setScrollTop(0);
      node.dispatchEvent(new Event("scroll"));
    });
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();
    act(() => {
      host.querySelector<HTMLButtonElement>(".jump-to-latest-pill")?.click();
    });
    expect(scrollTo).toHaveBeenCalledWith({ top: 920, behavior: "smooth" });
  });

  it("hides again once the container scrolls back within the threshold", () => {
    const { node, setScrollTop } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 0,
    });
    const host = mountPill(node);
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();
    act(() => {
      setScrollTop(560); // 40px from the bottom, inside the 80px threshold
      node.dispatchEvent(new Event("scroll"));
    });
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();
  });

  it("clears a transient scroll-away state when folded content settles", () => {
    const resizeObservers = installMockResizeObserver();
    const geometry = {
      scrollHeight: 500,
      clientHeight: 600,
      scrollTop: 0,
    };
    const { node } = scrollContainer(geometry);
    const content = document.createElement("div");
    node.appendChild(content);
    const host = mountPill(node);
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();

    act(() => {
      geometry.scrollHeight = 900;
      node.dispatchEvent(new Event("scroll"));
    });
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();

    act(() => {
      geometry.scrollHeight = 500;
      flushResizeObservers(resizeObservers, content);
    });
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();
  });

  it("observes content nodes inserted after the pill mounts", async () => {
    const resizeObservers = installMockResizeObserver();
    const geometry = {
      scrollHeight: 500,
      clientHeight: 600,
      scrollTop: 0,
    };
    const { node } = scrollContainer(geometry);
    const host = mountPill(node);
    const lateContent = document.createElement("div");

    await act(async () => {
      node.appendChild(lateContent);
      await Promise.resolve();
    });

    act(() => {
      geometry.scrollHeight = 900;
      flushResizeObservers(resizeObservers, lateContent);
    });
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();

    act(() => {
      geometry.scrollHeight = 500;
      flushResizeObservers(resizeObservers, lateContent);
    });
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();
  });

  it("tracks queued drawers and expanded input in the shared composer slot", () => {
    vi.useFakeTimers();
    const resizeObservers = installMockResizeObserver();
    const { node } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 0, // scrolled away → visible
    });
    // The scroll region occupies only the top portion of the viewport, so the
    // pill must follow the composer rather than the scroll region's bottom.
    stubRect(node, { left: 100, top: 56, bottom: 800, width: 600, height: 744 });
    const anchor = document.createElement("div");
    document.body.appendChild(anchor);
    mountedContainers.push(anchor);
    // The footer is taller than the input frame because a queued-message
    // drawer sits above it.
    stubRect(anchor, { left: 100, top: 620, bottom: 780, width: 600, height: 160 });
    const queueDrawer = document.createElement("div");
    queueDrawer.className = "composer-pending-drawer";
    anchor.appendChild(queueDrawer);
    const frame = document.createElement("div");
    frame.className = "composer-frame";
    anchor.appendChild(frame);
    stubRect(frame, { left: 150, top: 700, bottom: 780, width: 400, height: 80 });
    Object.defineProperty(window, "innerHeight", {
      configurable: true,
      value: 900,
    });

    const host = document.createElement("div");
    node.appendChild(host);
    const root = createRoot(host);
    const containerRef = { current: node };
    act(() => {
      root.render(
        createElement(JumpToLatestPill, {
          containerRef,
          bottomAnchor: anchor,
        }),
      );
    });
    mountedRoots.push(root);

    // Portaled to <body>, NOT rendered inside the scroll container.
    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();
    const pill = document.body.querySelector<HTMLButtonElement>(
      ".jump-to-latest-pill-anchored",
    );
    expect(pill).not.toBeNull();
    // The composer can move independently when the environment panel reserves
    // room on the right, so the pill follows the frame rather than the unchanged
    // scroll-container bounds: 150 + 400 / 2.
    expect(pill?.style.left).toBe("350px");
    // The queued drawer is part of the same visual height used by the progress
    // pill, so the jump pill clears it instead of overlapping it.
    expect(pill?.style.bottom).toBe("288px");

    act(() => {
      document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
      stubRect(node, { left: 100, top: 56, bottom: 800, width: 400, height: 744 });
      stubRect(frame, { left: 100, top: 700, bottom: 780, width: 400, height: 80 });
      flushResizeObservers(resizeObservers, node);
      vi.advanceTimersToNextFrame();
    });
    // Width follows the composer live: 100 + 400 / 2, without waiting for
    // the resize-settle timer that protects scroll and height measurements.
    expect(pill?.style.left).toBe("300px");

    act(() => {
      document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
      vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    });

    act(() => {
      // A panel padding transition moves the frame without resizing either
      // observed box. The transition-end signal must still settle the pill.
      stubRect(frame, { left: 60, top: 700, bottom: 780, width: 400, height: 80 });
      node.dispatchEvent(new Event("transitionend"));
      vi.advanceTimersToNextFrame();
    });
    expect(pill?.style.left).toBe("260px");

    act(() => {
      stubRect(frame, { left: 100, top: 640, bottom: 780, width: 600, height: 140 });
      frame.style.setProperty("--composer-expanded-offset", "60px");
      flushResizeObservers(resizeObservers, frame);
      vi.runAllTimers();
    });
    expect(pill?.style.bottom).toBe("348px");

    act(() => {
      root.render(
        createElement(JumpToLatestPill, {
          containerRef,
          bottomAnchor: null,
        }),
      );
    });
    expect(
      document.body.querySelector(".jump-to-latest-pill-anchored"),
    ).toBeNull();
  });

  it("notifies parent via onScrolledAwayChange when the scroll-away boolean flips", () => {
    // Regression (2026-07-10): the main conversation used to keep the jump
    // pill and the active-plan progress pill in two stacked slots, because
    // the jump pill's scrolled-away state lived inside the component. The
    // parent needs that signal to swap a sibling progress pill in and out of
    // the same composer-adjacent slot. Verify the callback fires on mount
    // AND whenever the boolean flips via a real scroll event.
    const { node, setScrollTop } = scrollContainer({
      scrollHeight: 1000,
      clientHeight: 400,
      scrollTop: 0, // distanceFromBottom = 600 > 80 → scrolledAway = true
    });
    const onScrolledAwayChange = vi.fn();
    const host = document.createElement("div");
    node.appendChild(host);
    const root = createRoot(host);
    act(() => {
      root.render(
        createElement(JumpToLatestPill, {
          containerRef: { current: node },
          bottomAnchor: null,
          onScrolledAwayChange,
        }),
      );
    });
    mountedRoots.push(root);
    expect(onScrolledAwayChange).toHaveBeenLastCalledWith(true);

    // Scroll back to the bottom — the boolean should flip to false.
    setScrollTop(600);
    act(() => {
      node.dispatchEvent(new Event("scroll"));
    });
    expect(onScrolledAwayChange).toHaveBeenLastCalledWith(false);

    // Scroll away again — should flip back to true.
    setScrollTop(0);
    act(() => {
      node.dispatchEvent(new Event("scroll"));
    });
    expect(onScrolledAwayChange).toHaveBeenLastCalledWith(true);
  });

  it("defers container resize measurement while the window is resizing", () => {
    vi.useFakeTimers();
    const resizeObservers = installMockResizeObserver();
    let clientHeight = 100;
    let liveResizeMetricReads = 0;
    const { node } = scrollContainer({
      scrollHeight: 1000,
      clientHeight,
      scrollTop: 0,
    });
    Object.defineProperty(node, "scrollHeight", {
      configurable: true,
      get: () => {
        if (document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)) {
          liveResizeMetricReads += 1;
        }
        return 1000;
      },
    });
    Object.defineProperty(node, "clientHeight", {
      configurable: true,
      get: () => {
        if (document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)) {
          liveResizeMetricReads += 1;
        }
        return clientHeight;
      },
    });
    const host = mountPill(node);
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();

    act(() => {
      document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
      clientHeight = 1000;
      flushResizeObservers(resizeObservers);
    });

    expect(liveResizeMetricReads).toBe(0);
    expect(host.querySelector(".jump-to-latest-pill")).not.toBeNull();

    act(() => {
      document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
      vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    });

    expect(host.querySelector(".jump-to-latest-pill")).toBeNull();
  });
});
