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
export function animateSurface(root: SVGGElement, layout: Layout, paused = false) {
  const eyes = root.querySelector<SVGGElement>(".mo-eyes")!;
  const paths = [...eyes.querySelectorAll<SVGPathElement>(".mo-eye > path")];
  const doc = root.ownerDocument;
  const win = doc.defaultView!;
  const svg = root.ownerSVGElement!;
  const reduced = win.matchMedia?.("(prefers-reduced-motion: reduce)");
  let signature = "";
  let visible = true;
  let active = false;
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
    const amp = paused || reduced?.matches ? 0 : value("amp", root.classList.contains("mo-always") ? 1 : 0);
    const blink = 1 - amp * (1 - value("lid", 1));
    p.esy *= blink;
    p.esy2 *= blink;
    const view = {
      yaw: value("yaw") + amp * value("scan-x") * value("look-x", 1.4) + (reduced?.matches ? 0 : value("pointer-yaw")),
      pitch: value("pitch") - amp * value("scan-y") * value("look-y", 1.1) + (reduced?.matches ? 0 : value("pointer-pitch")),
      strength: 1,
    };
    // Consumers can briefly carry the face around the sphere without changing its resting camera.
    const spin = reduced?.matches ? 0 : value("spin");
    const next = JSON.stringify([p, view, spin]);
    if (next === signature) return;
    signature = next;
    const posed = bakePose(layout, p).l;
    const data = posed.eyes.map((eye) => surfaceEye(eye, layout.face, view, spin).path);
    return () => paths.forEach((path, i) => path.setAttribute("d", data[i]!));
  };
  const flush = () => read()?.();

  /**
   * The face variables this reader polls only move while a transition on a
   * `--mo-*` channel or a keyframe animation that sets one is running. Polling
   * unconditionally meant a getComputedStyle read on every display frame for
   * every mounted mascot — including all the ones whose ambient loops are
   * paused and whose signature never changes — so the reader now subscribes
   * only while a driving animation or transition is in flight. Transform and
   * ornament loops (activity props, hover lifts, compaction squeezes) never
   * reach the eye contours and do not keep the reader awake.
   */
  const drivesFaceVars = (animation: Animation): boolean => {
    // A pending transition still reports "running"; paused and finished
    // effects are what must not keep the reader awake.
    if (animation.playState !== "running") return false;
    if ("transitionProperty" in animation) {
      return String((animation as CSSTransition).transitionProperty).startsWith("--mo-");
    }
    const effect = animation.effect as KeyframeEffect | null;
    const target = effect?.target;
    // Keyframes on the eye elements are what bake the blink and glance
    // variables; check the target first so engines without custom-property
    // keyframes in getKeyframes() still see them.
    if (target instanceof win.Element && target.closest(".mo-eyes, .mo-eye")) return true;
    try {
      const frames = effect?.getKeyframes?.();
      return frames ? frames.some(f => Object.keys(f).some(k => k.startsWith("--mo-"))) : true;
    } catch {
      return true;
    }
  };
  const motionDrivingFaceVars = (): boolean => {
    if (typeof svg.getAnimations !== "function") return true;
    const animations = svg.getAnimations({ subtree: true });
    for (const animation of svg.getAnimations()) animations.push(animation);
    // Engines without the `subtree` option only return the svg's own effects
    // above; the blink/scan loops live on the eyes group, so ask it directly.
    for (const animation of eyes.getAnimations()) animations.push(animation);
    return animations.some(drivesFaceVars);
  };
  const evaluate = () => {
    if (active && motionDrivingFaceVars()) {
      if (!unsubscribe) unsubscribe = subscribe(read);
    } else if (unsubscribe) {
      unsubscribe();
      unsubscribe = undefined;
      // Settle on the exact pose the last transition reached rather than the
      // value the previous frame happened to poll.
      flush();
    }
  };
  const refresh = () => {
    active = visible && !doc.hidden && !reduced?.matches && !paused;
    root.toggleAttribute("data-mo-paused", !active);
    // flush() forces style resolution first, so the playStates evaluate()
    // scans already reflect the data-mo-paused toggle above.
    flush();
    evaluate();
  };
  // Offscreen rows keep their current expression but spend no frame budget.
  const observer = win.IntersectionObserver ? new win.IntersectionObserver(([entry]) => {
    visible = entry!.isIntersecting;
    refresh();
  }) : undefined;
  observer?.observe(svg);
  reduced?.addEventListener("change", refresh);
  doc.addEventListener("visibilitychange", refresh);
  // Transition and animation starts/stops re-evaluate the subscription
  // immediately; the interval catches drivers that fire no DOM event —
  // element.animate() Web Animations, animation-play-state toggles from
  // ancestor attribute selectors — and settles discrete --mo-* writes no
  // transition reported. It only runs while the reader is parked.
  const MOTION_EVENTS = [
    "transitionrun", "transitionstart", "transitionend", "transitioncancel",
    "animationstart", "animationend", "animationcancel", "animationiteration",
  ];
  for (const type of MOTION_EVENTS) svg.addEventListener(type, evaluate);
  const watchdog = win.setInterval(() => {
    if (!active || unsubscribe) return;
    evaluate();
    if (!unsubscribe) flush();
  }, 400);
  refresh();
  return {
    flush,
    setPaused(next: boolean) {
      if (paused === next) return;
      paused = next;
      refresh();
    },
    dispose() {
      // Turning surface mode off can keep byte-identical authored markup.
      // Restore its chart before the flat CSS renderer takes ownership again.
      paths.forEach((path, i) => path.setAttribute("d", superellipse(layout.eyes[i]!)));
      root.removeAttribute("data-mo-paused");
      unsubscribe?.();
      win.clearInterval(watchdog);
      for (const type of MOTION_EVENTS) svg.removeEventListener(type, evaluate);
      observer?.disconnect();
      reduced?.removeEventListener("change", refresh);
      doc.removeEventListener("visibilitychange", refresh);
    },
  };
}
