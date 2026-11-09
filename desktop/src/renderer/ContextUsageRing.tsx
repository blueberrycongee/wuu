import type { SVGProps } from "react";
import "./styles/context-usage-ring.css";

const RING_RADIUS = 9;
const RING_STROKE_WIDTH = 2;
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS;

/** Shared context meter geometry; callers choose the meaning and color of the fill. */
export function ContextUsageRing({
  ratio,
  className = "",
  ...props
}: SVGProps<SVGSVGElement> & { ratio: number }): JSX.Element {
  const fill = Math.min(1, Math.max(0, ratio));
  // Round caps add a stroke width to the visible arc. Compensate so that
  // near-full readings retain the correct gap, including tiny retained tails.
  const paintedLength = Math.max(0, RING_CIRCUMFERENCE * fill - RING_STROKE_WIDTH);
  const dashOffset = fill >= 1 ? 0 : RING_CIRCUMFERENCE - paintedLength;
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      className={`context-usage-ring ${className}`}
      aria-hidden="true"
      {...props}
    >
      <circle cx="12" cy="12" r={RING_RADIUS} fill="none" strokeWidth={RING_STROKE_WIDTH} className="context-usage-ring-track" />
      <circle
        cx="12" cy="12" r={RING_RADIUS} fill="none"
        strokeWidth={RING_STROKE_WIDTH} strokeLinecap="round"
        strokeDasharray={RING_CIRCUMFERENCE} strokeDashoffset={dashOffset}
        transform="rotate(-90 12 12)"
        className="context-usage-ring-progress"
        opacity={fill === 0 ? 0 : undefined}
      />
    </svg>
  );
}
