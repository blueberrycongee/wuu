export const WINDOW_RESIZING_CLASS = "window-resizing";
export const LAYOUT_MOTION_CLASS = "layout-motion-active";
export const WINDOW_RESIZE_SETTLE_DELAY_MS = 160;

let settleFlushDepth = 0;
const pendingSettle = new Set<WindowResizeSettleScheduler>();

function freezeClassesActive(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  const root = document.documentElement;
  return (
    root.classList.contains(WINDOW_RESIZING_CLASS) ||
    root.classList.contains(LAYOUT_MOTION_CLASS)
  );
}

export function isWindowResizing(): boolean {
  // Callers flush deferred geometry while the freeze class is still on so the
  // catch-up cannot animate after transitions are re-enabled.
  if (settleFlushDepth > 0) {
    return false;
  }
  return freezeClassesActive();
}

export type WindowResizeSettleScheduler = {
  schedule: () => void;
  cancel: () => void;
  flush: () => void;
};

export function createWindowResizeSettleScheduler(
  callback: () => void,
): WindowResizeSettleScheduler {
  let timer: number | undefined;
  const scheduler: WindowResizeSettleScheduler = {
    schedule: () => {
      scheduler.cancel();
      pendingSettle.add(scheduler);
      timer = window.setTimeout(() => {
        timer = undefined;
        if (isWindowResizing()) {
          scheduler.schedule();
          return;
        }
        pendingSettle.delete(scheduler);
        callback();
      }, WINDOW_RESIZE_SETTLE_DELAY_MS);
    },
    cancel: () => {
      if (timer !== undefined) {
        window.clearTimeout(timer);
        timer = undefined;
      }
      pendingSettle.delete(scheduler);
    },
    flush: () => {
      if (timer === undefined && !pendingSettle.has(scheduler)) {
        return;
      }
      scheduler.cancel();
      callback();
    },
  };
  return scheduler;
}

export function flushWindowResizeSettle(): void {
  if (typeof window === "undefined" || pendingSettle.size === 0) {
    return;
  }
  settleFlushDepth += 1;
  try {
    let guard = 0;
    while (pendingSettle.size > 0 && guard < 4) {
      guard += 1;
      const batch = [...pendingSettle];
      for (const scheduler of batch) {
        scheduler.flush();
      }
    }
  } finally {
    settleFlushDepth -= 1;
  }
}

/** Drop a freeze class. If nothing else is holding the freeze, apply deferred layout first. */
export function releaseWindowResizeClass(className: string): void {
  if (typeof document === "undefined") {
    return;
  }
  const root = document.documentElement;
  const other =
    className === WINDOW_RESIZING_CLASS ? LAYOUT_MOTION_CLASS : WINDOW_RESIZING_CLASS;
  if (!root.classList.contains(other)) {
    flushWindowResizeSettle();
  }
  root.classList.remove(className);
}
