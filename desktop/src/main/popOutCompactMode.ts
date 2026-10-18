import type { BrowserWindow, Rectangle } from "electron";

/** Window-local presentation only: no thread ownership or renderer replacement. */
export class PopOutCompactMode {
  private readonly expanded = new WeakMap<BrowserWindow, {
    bounds: Rectangle;
    maximized: boolean;
  }>();

  isCompact(win: BrowserWindow): boolean {
    return this.expanded.has(win);
  }

  set(win: BrowserWindow, compact: boolean, getWorkArea: (bounds: Rectangle) => Rectangle): boolean {
    if (compact === this.isCompact(win)) return compact;
    // macOS fullscreen transitions are asynchronous. Do not race them with a
    // geometry change; the existing native control lets the user exit first.
    if (win.isFullScreen()) throw new Error("Exit full screen before changing window size.");
    const saved = this.expanded.get(win);
    let target: Rectangle;
    if (compact) {
      const bounds = win.getNormalBounds();
      this.expanded.set(win, { bounds, maximized: win.isMaximized() });
      if (win.isMaximized()) win.unmaximize();
      target = { ...bounds, width: 420, height: 480 };
    } else {
      target = saved!.bounds;
      if (win.isMaximized()) win.unmaximize();
    }
    const workArea = getWorkArea(target);
    const width = Math.min(target.width, workArea.width);
    const height = Math.min(target.height, workArea.height);
    win.setBounds({
      width,
      height,
      x: Math.max(workArea.x, Math.min(target.x, workArea.x + workArea.width - width)),
      y: Math.max(workArea.y, Math.min(target.y, workArea.y + workArea.height - height)),
    });
    if (!compact) {
      this.expanded.delete(win);
      if (saved!.maximized) win.maximize();
    }
    return compact;
  }
}
