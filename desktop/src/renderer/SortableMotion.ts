import type { DropAnimation } from "@dnd-kit/core";
import type { UseSortableArguments } from "@dnd-kit/sortable";
import { useMemo } from "react";
import { motionCurve, motionDurationMs, useReducedMotion } from "./motion";

const EASE_OUT_FALLBACK = "cubic-bezier(0.16, 1, 0.3, 1)";

/*
 * dnd-kit writes its motion as inline styles and WAAPI calls with literal
 * timings, which the stylesheet's reduced-motion rules cannot reach. Sortable
 * rows and drag overlays take their timing from the ladder here instead, and
 * drop it entirely when motion is reduced.
 */

/** Sibling shifts while a sortable item is dragged over them. */
export function useSortableTransition(): UseSortableArguments["transition"] {
  const reduced = useReducedMotion();
  return useMemo(() => (reduced ? null : {
    duration: motionDurationMs("--motion-base", 180),
    easing: motionCurve("--ease-out", EASE_OUT_FALLBACK),
  }), [reduced]);
}

/** The overlay settling into the dropped item's slot. */
export function useDropAnimation(): DropAnimation | null {
  const reduced = useReducedMotion();
  return useMemo(() => (reduced ? null : {
    duration: motionDurationMs("--motion-fast", 120),
    easing: motionCurve("--ease-out", EASE_OUT_FALLBACK),
  }), [reduced]);
}
