/*
 * Test helpers for Tooltip-wrapped elements.
 *
 * Tests used to read hover hints off the native `title` attribute; with the
 * Tooltip component the hint lives in a portal that opens after a delay, so
 * asserting it requires simulating a hover and letting the delay elapse.
 * These helpers hide both the timer mode (fake vs real) and the portal
 * lookup, and close any previously opened tooltip first so repeated calls
 * within one test observe exactly one layer.
 */
import { act } from "react";
import { vi } from "vitest";
import { TOOLTIP_OPEN_DELAY_MS } from "./Tooltip";

let lastHoveredWrapper: Element | null = null;

/** Close whatever tooltip is currently open, if any. */
export function unhoverTooltip(): void {
  if (!lastHoveredWrapper) {
    return;
  }
  const wrapper = lastHoveredWrapper;
  lastHoveredWrapper = null;
  if (vi.isFakeTimers()) {
    act(() => {
      wrapper.dispatchEvent(new Event("pointerout", { bubbles: true }));
      vi.advanceTimersByTime(1);
    });
  } else {
    act(() => {
      wrapper.dispatchEvent(new Event("pointerout", { bubbles: true }));
    });
  }
}

/**
 * Hover the tooltip trigger wrapping `element` and return the portaled
 * tooltip's text once it has opened. Returns null when the element is not
 * wrapped in an active trigger (or no tooltip opened).
 */
export async function hoverTooltipText(element: Element | null): Promise<string | null> {
  unhoverTooltip();
  const wrapper = element?.closest("[data-wuu-tooltip-trigger]");
  if (!wrapper) {
    return null;
  }
  lastHoveredWrapper = wrapper;
  if (vi.isFakeTimers()) {
    act(() => {
      wrapper.dispatchEvent(new Event("pointerover", { bubbles: true }));
      vi.advanceTimersByTime(TOOLTIP_OPEN_DELAY_MS + 20);
    });
  } else {
    await act(async () => {
      wrapper.dispatchEvent(new Event("pointerover", { bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, TOOLTIP_OPEN_DELAY_MS + 20));
    });
  }
  return document.querySelector(".tooltip-layer")?.textContent ?? null;
}
