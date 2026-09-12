import type { BrowserWindow, Rectangle } from "electron";
import type { SessionInspectorExpansionResult } from "../shared/protocol";

/** Owns only the extra right-edge width, never a saved window position. */
export function createSessionInspectorExpansion(
  workAreaForBounds: (bounds: Rectangle) => Rectangle,
) {
  const states = new WeakMap<BrowserWindow, { width: number; closing: boolean }>();
  const attached = new WeakSet<BrowserWindow>();

  function unexpandedBounds(window: BrowserWindow): Rectangle {
    const bounds = window.getBounds();
    const width = states.get(window)?.width ?? 0;
    if (!width) return bounds;
    const [minimumWidth] = window.getMinimumSize();
    return { ...bounds, width: Math.max(minimumWidth, 1, bounds.width - width) };
  }

  function close(window: BrowserWindow): SessionInspectorExpansionResult {
    const state = states.get(window);
    if (!state || window.isDestroyed()) {
      states.delete(window);
      return { expanded: false, panelWidth: 0 };
    }
    // Closing a panel must not unmaximize a window or leave fullscreen. Remove
    // the extension once normal bounds become active again instead.
    state.closing = true;
    if (window.isMaximized() || window.isFullScreen()) {
      return { expanded: false, panelWidth: 0 };
    }
    const bounds = unexpandedBounds(window);
    states.delete(window);
    window.setBounds(bounds);
    return { expanded: false, panelWidth: 0 };
  }

  function set(window: BrowserWindow, payload: unknown): SessionInspectorExpansionResult {
    if (!payload || typeof payload !== "object" || !("open" in payload) || typeof payload.open !== "boolean") {
      throw new Error("Invalid session inspector expansion request");
    }
    if (!payload.open) return close(window);
    if (window.isDestroyed() || window.isMaximized() || window.isFullScreen() || !window.isResizable()) {
      return { expanded: false, panelWidth: 0, reason: "unsupported-window" };
    }
    const zoom = window.webContents.getZoomFactor();
    const existing = states.get(window);
    if (existing) {
      existing.closing = false;
      return { expanded: true, panelWidth: existing.width / zoom };
    }
    const requested = "width" in payload && payload.width !== undefined ? payload.width : 480;
    if (typeof requested !== "number" || !Number.isFinite(requested) || requested <= 0) {
      throw new Error("Invalid session inspector width");
    }
    const width = Math.ceil(requested * zoom);
    const bounds = window.getBounds();
    const workArea = workAreaForBounds(bounds);
    const [maximumWidth] = window.getMaximumSize();
    if (bounds.x + bounds.width + width > workArea.x + workArea.width) {
      return { expanded: false, panelWidth: 0, reason: "insufficient-space" };
    }
    if (maximumWidth > 0 && bounds.width + width > maximumWidth) {
      return { expanded: false, panelWidth: 0, reason: "unsupported-window" };
    }
    states.set(window, { width, closing: false });
    window.setBounds({ ...bounds, width: bounds.width + width });
    const actual = window.getBounds();
    if (actual.x !== bounds.x || actual.y !== bounds.y || actual.height !== bounds.height || actual.width !== bounds.width + width) {
      // The OS refused exact bounds. Never silently compress the original UI.
      states.delete(window);
      window.setBounds(bounds);
      return { expanded: false, panelWidth: 0, reason: "unsupported-window" };
    }
    if (!attached.has(window)) {
      attached.add(window);
      window.webContents.on("did-start-navigation", (_event, _url, isInPlace, isMainFrame) => {
        if (isMainFrame && !isInPlace) close(window);
      });
      window.webContents.on("render-process-gone", () => close(window));
      window.on("closed", () => states.delete(window));
      const finishClose = () => {
        if (states.get(window)?.closing) close(window);
      };
      window.on("unmaximize", finishClose);
      window.on("leave-full-screen", finishClose);
    }
    return { expanded: true, panelWidth: width / zoom };
  }

  return { set, unexpandedBounds };
}
