import { useLayoutEffect, useRef, type RefObject } from "react";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";

const WAVE_SPEED_PX_PER_SECOND = 90;
const WAVE_GAP_MS = 4000;
const WAVE_HIGHLIGHT_WIDTH_PX = 16;

function updateWaveTiming(element: HTMLElement): void {
  const travelMs =
    ((element.getBoundingClientRect().width + WAVE_HIGHLIGHT_WIDTH_PX) /
      WAVE_SPEED_PX_PER_SECOND) *
    1000;
  const cycleMs = travelMs + WAVE_GAP_MS;
  const travelStop = (travelMs / cycleMs) * 100;

  element.style.setProperty("--wuu-live-text-wave-duration", `${cycleMs}ms`);
  element.style.setProperty("--wuu-live-text-wave-travel-stop", `${travelStop}%`);
}

/** Keeps the shared text highlight at one visual speed regardless of label width. */
export function useLiveTextWave<T extends HTMLElement>(
  active: boolean,
): RefObject<T | null> {
  const ref = useRef<T>(null);

  useLayoutEffect(() => {
    const element = ref.current;
    if (!active || !element) return undefined;

    updateWaveTiming(element);
    if (typeof ResizeObserver === "undefined") return undefined;

    const settle = createWindowResizeSettleScheduler(() => {
      updateWaveTiming(element);
    });
    const observer = new ResizeObserver(() => {
      // Follow mode is already changing this line's box every chunk.
      // Measuring it again here feeds the resize loop the stream is in.
      if (document.documentElement.hasAttribute("data-stream-following")) return;
      if (isWindowResizing()) {
        settle.schedule();
        return;
      }
      updateWaveTiming(element);
    });
    observer.observe(element);
    return () => {
      settle.cancel();
      observer.disconnect();
    };
  }, [active]);

  return ref;
}
