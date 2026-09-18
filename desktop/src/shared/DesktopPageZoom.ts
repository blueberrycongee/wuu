type PageZoomFrame = {
  getZoomLevel: () => number;
  getZoomFactor: () => number;
  setZoomLevel: (level: number) => void;
};

const storageKey = "wuu.desktop.pageZoomLevel";
const defaultLevel = -0.5;

/** Restore shell zoom independently of UI/code font preferences. */
export function initializeDesktopPageZoom(frame: PageZoomFrame, host: Window): () => void {
  let savedLevel = defaultLevel;
  try {
    const raw = host.localStorage.getItem(storageKey);
    const value: unknown = raw === null ? null : JSON.parse(raw);
    if (typeof value === "number" && Number.isFinite(value) && value >= -10 && value <= 10) {
      savedLevel = value;
    }
  } catch {
    // Missing/blocked storage still gets the first-run layout.
  }
  frame.setZoomLevel(savedLevel);

  function sync(): void {
    // Native window controls use DIP while content uses zoomed CSS pixels.
    host.document.documentElement?.style.setProperty("--desktop-page-zoom", String(frame.getZoomFactor()));
    const level = frame.getZoomLevel();
    if (!Number.isFinite(level) || level === savedLevel) return;
    try {
      host.localStorage.setItem(storageKey, JSON.stringify(level));
      savedLevel = level;
    } catch {
      // A storage failure must not undo the user's current zoom choice.
    }
  }
  sync();
  host.addEventListener("DOMContentLoaded", sync, { once: true });
  host.addEventListener("resize", sync);
  host.addEventListener("pagehide", sync);
  return () => {
    host.removeEventListener("DOMContentLoaded", sync);
    host.removeEventListener("resize", sync);
    host.removeEventListener("pagehide", sync);
  };
}
