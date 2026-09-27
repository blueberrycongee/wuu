import type { ReactNode } from "react";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { useExitPresence } from "./useExitPresence";

const COLLAPSE_MOTION_FALLBACK_MS = 440;
const COLLAPSED_CONTENT_RELEASE_BUFFER_MS = 32;

export function CollapsibleDetails({
  children,
  className,
  expanded,
  id,
  innerClassName,
}: {
  children: ReactNode;
  className?: string;
  expanded: boolean;
  id?: string;
  innerClassName?: string;
}): JSX.Element {
  // Keep the body mounted through the close motion, then release the hidden
  // Markdown/tool tree. Long conversations otherwise retain every completed
  // process row even though the folds are collapsed.
  const [shouldRenderChildren] = useExitPresence(expanded, () => {
    const motionDuration = prefersReducedMotion()
      ? 0
      : motionDurationMs("--collapse-motion-duration", COLLAPSE_MOTION_FALLBACK_MS);
    return motionDuration > 0 ? motionDuration + COLLAPSED_CONTENT_RELEASE_BUFFER_MS : 0;
  });
  const detailsClassName = [
    "collapsible-details",
    expanded ? "expanded" : "collapsed",
    className,
  ]
    .filter(Boolean)
    .join(" ");
  const innerClassNames = ["collapsible-details-inner", innerClassName]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={detailsClassName} id={id} aria-hidden={!expanded}>
      <div className={innerClassNames}>{shouldRenderChildren ? children : null}</div>
    </div>
  );
}
