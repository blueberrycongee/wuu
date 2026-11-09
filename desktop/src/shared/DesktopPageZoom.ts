type PageZoomFrame = {
  getZoomLevel: () => number;
  getZoomFactor: () => number;
  setZoomLevel: (level: number) => void;
};

const storageKey = "wuu.desktop.pageZoomLevel";
const defaultLevel = 0;
const minimumLevel = Math.log(0.5) / Math.log(1.2);
const maximumLevel = Math.log(2) / Math.log(1.2);

export type DesktopZoomAction = "in" | "out" | "reset";

/** Restore shell zoom independently of UI/code font preferences. */
export function initializeDesktopPageZoom(
  frame: PageZoomFrame,
  host: Window,
  subscribe: (listener: (action: DesktopZoomAction) => void) => () => void,
): () => void {
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
  frame.setZoomLevel(Math.max(minimumLevel, Math.min(maximumLevel, savedLevel)));

  let readout: HTMLDivElement | undefined;
  let dismissTimer: number | undefined;
  const unsubscribe = subscribe(action => {
    const percent = Math.round(frame.getZoomFactor() * 100);
    const next = action === "reset" ? 100 : action === "in"
      ? (Math.floor(percent / 10) + 1) * 10
      : (Math.ceil(percent / 10) - 1) * 10;
    frame.setZoomLevel(Math.max(minimumLevel, Math.min(maximumLevel, Math.log(next / 100) / Math.log(1.2))));
    sync();
    if (!host.document.body) return;
    if (!readout) {
      readout = host.document.createElement("div");
      readout.className = "desktop-zoom-readout";
      readout.setAttribute("role", "status");
      readout.setAttribute("aria-atomic", "true");
      host.document.body.append(readout);
    }
    readout.textContent = `${Math.round(frame.getZoomFactor() * 100)}%`;
    host.clearTimeout(dismissTimer);
    dismissTimer = host.setTimeout(() => {
      readout?.remove();
      readout = undefined;
    }, 1200);
  });

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
    unsubscribe();
    host.clearTimeout(dismissTimer);
    readout?.remove();
    host.removeEventListener("DOMContentLoaded", sync);
    host.removeEventListener("resize", sync);
    host.removeEventListener("pagehide", sync);
  };
}
