import type { BrowserWindow, Rectangle } from "electron";

// Some X11 desktops ignore roundedCorners and draw a rectangular client frame.
// Clip the entire host, including child views and appearance snapshots, rather
// than rounding only the video. The omitted corners also pass input through.
export function installLinuxPiPWindowShape(win: BrowserWindow): void {
  let previousSize = "";
  const update = (): void => {
    if (win.isDestroyed()) return;
    const { width, height } = win.getBounds();
    const size = `${width}:${height}`;
    if (size === previousSize) return;
    previousSize = size;
    const radius = Math.min(12, Math.floor(width / 2), Math.floor(height / 2));
    const rects: Rectangle[] = [];
    for (let y = 0; y < radius; y++) {
      const inset = Math.ceil(radius - Math.sqrt(radius ** 2 - (radius - y - 0.5) ** 2));
      rects.push({ x: inset, y, width: width - 2 * inset, height: 1 });
      rects.push({ x: inset, y: height - y - 1, width: width - 2 * inset, height: 1 });
    }
    if (height > 2 * radius) rects.push({ x: 0, y: radius, width, height: height - 2 * radius });
    win.setShape(rects);
  };
  // This listener also follows appearance animation sizes, which the shared
  // host deliberately excludes from saved user geometry.
  win.on("resize", update);
  update();
}
