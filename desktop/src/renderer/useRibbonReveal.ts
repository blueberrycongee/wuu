import { useLayoutEffect, useRef } from "react";

/** One timeline drives the shell and native surface, including interrupted exits. */
export function useRibbonReveal(
  identity: string | undefined,
  expanded: boolean,
  element: () => HTMLElement | null,
  paint: (fraction: number) => void,
): void {
  const progress = useRef(0);
  const previousIdentity = useRef(identity);
  const latest = useRef({ element, paint });
  latest.current = { element, paint };
  useLayoutEffect(() => {
    if (previousIdentity.current !== identity) {
      previousIdentity.current = identity;
      progress.current = 0;
    }
    if (!identity) return;
    const node = latest.current.element();
    if (!node) return;
    const from = progress.current, to = expanded ? 1 : 0;
    // Production CSS can minify milliseconds to seconds.
    const rawDuration = getComputedStyle(node).getPropertyValue("--ribbon-motion-duration").trim();
    const duration = (Number.parseFloat(rawDuration) * (rawDuration.endsWith("ms") ? 1 : 1000) || 0) * Math.abs(to - from);
    const start = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const elapsed = duration ? Math.max(0, Math.min(1, (now - start) / duration)) : 1;
      progress.current = elapsed === 1 ? to : from + (to - from) * (1 - Math.pow(1 - elapsed, 3));
      node.style.setProperty("--ribbon-reveal", String(progress.current));
      latest.current.paint(progress.current);
      if (elapsed < 1) frame = requestAnimationFrame(tick);
    };
    tick(start);
    return () => cancelAnimationFrame(frame);
  }, [identity, expanded]);
}
