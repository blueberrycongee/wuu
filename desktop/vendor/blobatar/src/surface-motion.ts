import { bakePose, idle, type Pose } from "./expression";
import type { Layout } from "./styles/blob";
import { surfaceEye } from "./surface";
import { superellipse } from "./shape";

/**
 * CSS owns clocks and interpolation; geometry owns the resulting contours.
 * Reads are batched before writes so several visible mascots do not repeatedly
 * invalidate style between each other's computed-value reads.
 */
const readers = new Set<() => (() => void) | undefined>();
let frame: number | undefined;
function schedule() {
  if (frame !== undefined || readers.size === 0) return;
  frame = requestAnimationFrame(() => {
    frame = undefined;
    const writes = [...readers].map((read) => read());
    for (const write of writes) write?.();
    schedule();
  });
}
function subscribe(read: () => (() => void) | undefined) {
  readers.add(read);
  schedule();
  return () => {
    readers.delete(read);
    if (readers.size === 0 && frame !== undefined) {
      cancelAnimationFrame(frame);
      frame = undefined;
    }
  };
}

/** Installs contour updates without replacing nodes, including accessory portals. */
export function animateSurface(root: SVGGElement, layout: Layout) {
  const eyes = root.querySelector<SVGGElement>(".mo-eyes")!;
  const paths = [...eyes.querySelectorAll<SVGPathElement>(".mo-eye > path")];
  const doc = root.ownerDocument;
  const win = doc.defaultView!;
  const reduced = win.matchMedia?.("(prefers-reduced-motion: reduce)");
  let signature = "";
  let visible = true;
  let unsubscribe: (() => void) | undefined;
  const read = () => {
    const style = win.getComputedStyle(eyes);
    const value = (key: string, fallback = 0) => {
      const v = parseFloat(style.getPropertyValue(`--mo-${key}`) || root.style.getPropertyValue(`--mo-${key}`));
      return Number.isFinite(v) ? v : fallback;
    };
    const p = { ...idle.p };
    for (const key of ["esx", "esy", "esx2", "esy2", "tilt", "tilt2", "lock", "edx", "edy"] as (keyof Pose)[]) {
      p[key] = value(key, p[key]);
    }
    const amp = reduced?.matches ? 0 : value("amp", root.classList.contains("mo-always") ? 1 : 0);
    const blink = 1 - amp * (1 - value("lid", 1));
    p.esy *= blink;
    p.esy2 *= blink;
    const view = {
      yaw: value("yaw") + amp * value("scan-x") * value("look-x", 1.4) + (reduced?.matches ? 0 : value("pointer-yaw")),
      pitch: value("pitch") - amp * value("scan-y") * value("look-y", 1.1) + (reduced?.matches ? 0 : value("pointer-pitch")),
      strength: 1,
    };
    const next = JSON.stringify([p, view]);
    if (next === signature) return;
    signature = next;
    const posed = bakePose(layout, p).l;
    const data = posed.eyes.map((eye) => surfaceEye(eye, layout.body, view).path);
    return () => paths.forEach((path, i) => path.setAttribute("d", data[i]!));
  };
  const flush = () => read()?.();
  const refresh = () => {
    unsubscribe?.();
    unsubscribe = undefined;
    flush();
    if (visible && !doc.hidden && !reduced?.matches) unsubscribe = subscribe(read);
  };
  // Offscreen rows keep their current expression but spend no frame budget.
  const observer = win.IntersectionObserver ? new win.IntersectionObserver(([entry]) => {
    visible = entry!.isIntersecting;
    refresh();
  }) : undefined;
  observer?.observe(root.ownerSVGElement!);
  reduced?.addEventListener("change", refresh);
  doc.addEventListener("visibilitychange", refresh);
  refresh();
  return {
    flush,
    dispose() {
      // Turning surface mode off can keep byte-identical authored markup.
      // Restore its chart before the flat CSS renderer takes ownership again.
      paths.forEach((path, i) => path.setAttribute("d", superellipse(layout.eyes[i]!)));
      unsubscribe?.();
      observer?.disconnect();
      reduced?.removeEventListener("change", refresh);
      doc.removeEventListener("visibilitychange", refresh);
    },
  };
}
