import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SCROLLBAR_REVEAL_CLASS,
  SCROLLBAR_REVEAL_HIDE_DELAY_MS,
  markScrollbarRevealSelfManaged,
  revealScrollbar,
  startScrollbarReveal,
} from "./ScrollbarReveal";

function scrollRegion({ overflows = true } = {}): HTMLDivElement {
  const node = document.createElement("div");
  Object.defineProperties(node, {
    scrollHeight: { configurable: true, get: () => (overflows ? 1200 : 400) },
    clientHeight: { configurable: true, get: () => 400 },
  });
  document.body.appendChild(node);
  return node;
}

const revealed = (node: HTMLElement): boolean => node.classList.contains(SCROLLBAR_REVEAL_CLASS);

describe("ScrollbarReveal", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    document.body.replaceChildren();
  });

  it("paints the thumb while scrolling and hides it after the delay", () => {
    const node = scrollRegion();
    revealScrollbar(node);
    expect(revealed(node)).toBe(true);
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS - 1);
    expect(revealed(node)).toBe(true);
    vi.advanceTimersByTime(1);
    expect(revealed(node)).toBe(false);
  });

  it("extends the window instead of expiring mid-scroll", () => {
    const node = scrollRegion();
    revealScrollbar(node);
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS - 100);
    revealScrollbar(node);
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS - 100);
    expect(revealed(node)).toBe(true);
    vi.advanceTimersByTime(100);
    expect(revealed(node)).toBe(false);
  });

  it("leaves containers without overflow hidden", () => {
    const node = scrollRegion({ overflows: false });
    revealScrollbar(node);
    expect(revealed(node)).toBe(false);
  });

  it("reveals the scrolled element from the document listener", () => {
    const stop = startScrollbarReveal();
    const node = scrollRegion();
    // `scroll` does not bubble; the listener depends on the capture path.
    node.dispatchEvent(new Event("scroll"));
    expect(revealed(node)).toBe(true);
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS);
    node.dispatchEvent(new Event("scroll"));
    expect(revealed(node)).toBe(true);
    stop();
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS);
    expect(revealed(node)).toBe(false);
    node.dispatchEvent(new Event("scroll"));
    expect(revealed(node)).toBe(false);
  });

  it("reveals only the nested scroller and keeps each container's timeout independent", () => {
    const stop = startScrollbarReveal();
    const outer = scrollRegion();
    const inner = scrollRegion();
    outer.appendChild(inner);
    inner.dispatchEvent(new Event("scroll"));
    expect(revealed(inner)).toBe(true);
    expect(revealed(outer)).toBe(false);
    vi.advanceTimersByTime(300);
    outer.dispatchEvent(new Event("scroll"));
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS - 300);
    expect(revealed(inner)).toBe(false);
    expect(revealed(outer)).toBe(true);
    vi.advanceTimersByTime(300);
    expect(revealed(outer)).toBe(false);
    stop();
  });

  it("reveals horizontal overflow even without vertical overflow", () => {
    const stop = startScrollbarReveal();
    const node = scrollRegion({ overflows: false });
    Object.defineProperties(node, {
      scrollWidth: { get: () => 1200 },
      clientWidth: { get: () => 400 },
    });
    node.dispatchEvent(new Event("scroll"));
    expect(revealed(node)).toBe(true);
    vi.advanceTimersByTime(SCROLLBAR_REVEAL_HIDE_DELAY_MS);
    expect(revealed(node)).toBe(false);
    stop();
  });

  it("leaves containers whose controller owns the reveal", () => {
    const stop = startScrollbarReveal();
    const node = scrollRegion();
    markScrollbarRevealSelfManaged(node);
    node.dispatchEvent(new Event("scroll"));
    expect(revealed(node)).toBe(false);
    stop();
  });
});
