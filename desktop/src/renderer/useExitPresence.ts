import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Keeps closing content mounted while its exit plays. `present` is true while
 * `open`, and after `open` turns false until `exitMs()` elapses or `release`
 * is called from the exit's own end event, whichever comes first. Reopening
 * mid-exit keeps the same instance. `exitMs` is read when the exit starts, so
 * it follows the current tokens and reduced motion; 0 releases on the next
 * commit. `onExited` runs once for each exit that completes.
 */
export function useExitPresence(
  open: boolean,
  exitMs: () => number,
  onExited?: () => void,
): [present: boolean, release: () => void] {
  const [retained, setRetained] = useState(open);
  const latest = useRef({ exitMs, onExited });
  latest.current = { exitMs, onExited };
  const exiting = useRef(false);

  const release = useCallback((): void => {
    if (!exiting.current) return;
    exiting.current = false;
    setRetained(false);
    latest.current.onExited?.();
  }, []);

  useEffect(() => {
    if (open) {
      exiting.current = false;
      setRetained(true);
      return undefined;
    }
    if (!retained) return undefined;
    exiting.current = true;
    const duration = latest.current.exitMs();
    if (duration <= 0) {
      release();
      return undefined;
    }
    // The end event normally releases first. Disabled transitions, empty
    // bodies, and background windows still need a bounded cleanup path.
    const timer = window.setTimeout(release, duration);
    return () => window.clearTimeout(timer);
  }, [open, retained, release]);

  return [open || retained, release];
}
