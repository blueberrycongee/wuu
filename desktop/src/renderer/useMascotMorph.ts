import { useEffect, useId, useRef } from "react";
import type { WuuMascotActivity } from "./wuu-mascot-spec";

export type MascotMorph = "dots" | "orbit" | "radar" | "progress" | "gather" | "wave" | "send" | "receive" | "dock" | "ball" | "whirl" | "pencil" | "bang" | "standby" | "idle" | "liquid" | "responding" | "queued" | "waiting" | "failed" | "interrupted" | "scan" | "terminal";

export const ACTIVITY_MORPHS: Record<WuuMascotActivity, MascotMorph> = {
  idle: "idle", compose: "idle", thinking: "dots", compact: "gather",
  search: "radar", edit: "pencil", command: "terminal", read: "scan", tool: "orbit",
  sending: "send", responding: "wave", queued: "progress", waiting: "waiting",
  failed: "bang", interrupted: "standby",
};
const STATIC_MORPHS = new Set<MascotMorph>(["idle", "queued", "waiting", "failed", "interrupted"]);

type Particle = { x: number; y: number; r: number; alpha: number };
type Ring = { x: number; y: number; radius: number; start: number; sweep: number; width: number; alpha: number };
type Bar = { x: number; y: number; width: number; height: number; angle: number; alpha: number };
type Core = { x: number; y: number; sx: number; sy: number; angle: number; alpha: number; face: number; pencil: number; fluid: number };
type Scene = { core: Core; particles: Particle[]; rings: Ring[]; bars: Bar[]; ink: Bar[] };
type Point = { x: number; y: number };
const TAU = Math.PI * 2;
const clamp = (n: number) => Math.max(0, Math.min(1, n));
const smooth = (n: number) => { const t = clamp(n); return t * t * (3 - 2 * t); };
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
const cycle = (time: number, seconds: number, offset = 0) => (time / seconds + offset) % 1;
const bell = (phase: number) => Math.sin(clamp(phase) * Math.PI);

function scene(): Scene {
  return {
    core: { x: 50, y: 50, sx: 1, sy: 1, angle: 0, alpha: 1, face: 1, pencil: 0, fluid: 0 },
    particles: Array.from({ length: 8 }, () => ({ x: 50, y: 50, r: 0, alpha: 0 })),
    rings: Array.from({ length: 5 }, () => ({ x: 50, y: 50, radius: 15, start: -90, sweep: 0, width: 2, alpha: 0 })),
    bars: Array.from({ length: 5 }, () => ({ x: 50, y: 50, width: 0, height: 0, angle: 0, alpha: 0 })),
    ink: Array.from({ length: 3 }, () => ({ x: 50, y: 50, width: 0, height: 0, angle: 0, alpha: 0 })),
  };
}

// One numeric scene lets rapid selections retarget from the currently painted
// shape. Nothing remounts or snaps back to a neutral icon between two morphs.
export function targetScene(mode: MascotMorph, time: number): Scene {
  const s = scene(), c = s.core;
  const shrink = (scale: number) => { c.sx = c.sy = scale; c.face = 0; };
  const dot = (i: number, x: number, y: number, r: number, alpha = 1) => { s.particles[i] = { x, y, r, alpha }; };
  const ring = (i: number, radius: number, sweep: number, start = -90, width = 2.4, alpha = 1) => {
    s.rings[i] = { x: 50, y: 50, radius, sweep, start, width, alpha };
  };
  const bar = (i: number, x: number, y: number, width: number, height: number, angle = 0, alpha = 1) => {
    s.bars[i] = { x, y, width, height, angle, alpha };
  };
  switch (mode) {
    case "dots": {
      const bounce = (i: number) => Math.max(0, Math.sin(time * TAU / 1.5 - i * 1.05));
      shrink(.20 + bounce(1) * .035); c.y = 50 - bounce(1) * 5;
      dot(0, 23, 50 - bounce(0) * 5, 7.4 + bounce(0), .5 + bounce(0) * .5);
      dot(1, 77, 50 - bounce(2) * 5, 7.4 + bounce(2), .5 + bounce(2) * .5);
      break;
    }
    case "orbit":
      shrink(.32);
      for (let i = 0; i < 5; i++) {
        const a = time * 1.25 + i * TAU / 5;
        dot(i, 50 + Math.cos(a) * 32, 50 + Math.sin(a) * 17, 4.5 + Math.sin(a), .55 + .35 * Math.sin(a));
      }
      break;
    case "radar":
      shrink(.30);
      for (let i = 0; i < 3; i++) {
        const p = cycle(time, 2.2, i / 3);
        ring(i, 12 + p * 30, 359.9, -90, 4.2, .95 * (1 - smooth((p - .65) / .35)));
      }
      break;
    case "progress": {
      shrink(.30); const p = .22 + .12 * Math.sin(time * 1.8);
      ring(0, 31, 359.9, -90, 3, .16); ring(1, 31, Math.max(.1, p * 359.9), time * 100 - 90, 3.5);
      c.sx = c.sy = .30 + .035 * bell(clamp((p - .9) * 10));
      break;
    }
    case "gather": {
      const p = cycle(time, 3.8), collected = smooth(p / .78);
      shrink(.10 + .64 * collected); c.face = smooth((p - .72) / .15);
      for (let i = 0; i < 6; i++) {
        const q = smooth((p - i * .05) / .46), a = i * TAU / 6 + q * 1.7, r = 43 * (1 - q);
        dot(i, 50 + Math.cos(a) * r, 50 + Math.sin(a) * r, 4 + q * 2, Math.min(1, p * 12) * (1 - smooth((q - .7) / .3)));
      }
      break;
    }
    case "wave":
      shrink(.17); c.sy *= 1 + .55 * Math.sin(time * 4) ** 2;
      for (let i = 0; i < 4; i++) {
        const x = [18, 34, 66, 82][i], height = 10 + 23 * (.5 + .5 * Math.sin(time * 4.4 - i * .8)) * (.65 + .35 * Math.sin(time * 1.8 + i));
        bar(i, x, 50, 8, height);
      }
      break;
    case "send": {
      const p = cycle(time, 2.1), travel = smooth((p - .2) / .55), squeeze = bell(p / .25);
      shrink(.54); c.sx -= .06 * squeeze; c.sy += .04 * squeeze; c.x -= squeeze * 2;
      for (let i = 0; i < 3; i++) {
        const q = clamp(travel - i * .085);
        dot(i, 58 + q * 29, 44 - q * 25, 5.5 - i * 1.3, bell(travel) * (1 - i * .25));
      }
      break;
    }
    case "receive": {
      const p = cycle(time, 2.3), travel = smooth(p / .64), caught = bell((p - .64) / .3);
      shrink(.54 + caught * .07); c.sy -= caught * .06;
      for (let i = 0; i < 3; i++) {
        const q = clamp(travel - i * .065);
        dot(i, 15 + q * 35, 19 + q * 31, 5.5 - i * 1.2, (1 - smooth((travel - .82) / .18)) * Math.min(1, p * 12) * (1 - i * .25));
      }
      break;
    }
    case "dock": {
      const p = cycle(time, 2.8), rise = smooth(p / .65);
      shrink(.28); c.y = 72 - rise * 38; c.alpha = 1 - smooth((p - .65) / .17);
      bar(0, 50, 23, 42, 4); bar(1, 29, 29, 4, 16); bar(2, 71, 29, 4, 16);
      dot(0, 50, c.y + 17, 2.5, c.alpha * rise * .5); dot(1, 50, c.y + 24, 1.8, c.alpha * rise * .25);
      break;
    }
    case "ball": {
      const p = cycle(time, 1.4), bounce = 4 * p * (1 - p), squash = Math.exp(-p * 28) + Math.exp(-(1 - p) * 28);
      shrink(.61); c.sx += squash * .09; c.sy -= squash * .10; c.y = 60 - bounce * 29;
      bar(0, 50, 87, 24 - bounce * 10, 2, 0, .13 + squash * .06);
      break;
    }
    case "whirl":
    case "liquid":
      shrink(mode === "liquid" ? .59 : .43); c.fluid = 1;
      c.x += Math.sin(time * 2.1) * 1.6; c.sy *= 1 + Math.sin(time * 3.1) * .1;
      for (let i = 0; i < 4; i++) {
        const a = time * (i % 2 ? -1.05 : 1.35) + i * TAU / 4;
        const r = (mode === "liquid" ? 30 : 25) + Math.sin(time * 2.3 + i) * 7;
        dot(i, 50 + Math.cos(a) * r, 50 + Math.sin(a) * r * .7, [9, 7.5, 4.5, 3.7][i]);
      }
      break;
    case "pencil": {
      const p = cycle(time, 2.7);
      shrink(.75); c.pencil = 1; c.angle = 28 + Math.sin(time * 5) * 3;
      c.x = 42 + p * 16; c.y = 43 + Math.sin(time * 8) * 2;
      // The cut and nib move with the body, using the identity's eye colour.
      s.ink[0] = { x: 50, y: 28, width: 16, height: 3, angle: 0, alpha: 1 };
      s.ink[1] = { x: 50, y: 70, width: 12, height: 2.4, angle: 0, alpha: 1 };
      bar(0, 48, 84, 12 + p * 22, 2, 0, .35);
      break;
    }
    case "bang": {
      shrink(.17); c.y = 77;
      const kick = Math.exp(-cycle(time, 2.7) * 18);
      bar(0, 50 + Math.sin(time * 25) * kick, 38, 13, 40 + kick * 3);
      break;
    }
    case "standby":
      shrink(.02); c.alpha = 0;
      ring(0, 26, 284, -52, 5, .7 + Math.sin(time * 1.6) * .15);
      bar(0, 50, 30, 5, 29, 0, .7 + Math.sin(time * 1.6) * .15);
      break;
    case "scan": {
      const phase = cycle(time, 3.6) * 3, row = Math.floor(phase);
      const widths = [58, 46, 34];
      shrink(.075);
      c.x = 21 + widths[row] * smooth(phase - row);
      c.y = 38 + row * 20;
      for (let i = 0; i < 3; i++) {
        bar(i, 21 + widths[i] / 2, 27 + i * 20, widths[i], 7, 0, i === row ? 1 : .5);
      }
      break;
    }
    case "terminal":
      shrink(.13); c.x = 70; c.y = 67; c.alpha = .45 + .55 * Math.sin(time * 3) ** 2;
      bar(0, 36, 39, 24, 5, 40); bar(1, 36, 54, 24, 5, -40);
      break;
    case "failed": c.y = 54; c.sx = 1.035; c.sy = .92; break;
    case "interrupted": c.sy = .96; c.y = 52; break;
    case "queued": c.sx = c.sy = .94; break;
    case "waiting": c.angle = -4; break;
    case "responding": c.y = 50 - Math.sin(time * 2.5) * .7; break;
    default: break;
  }
  return s;
}

function blend<T extends object>(current: T, target: T, amount: number): void {
  for (const key of Object.keys(current) as (keyof T)[]) {
    current[key] = lerp(current[key] as number, target[key] as number, amount) as T[keyof T];
  }
}
function arc(r: Ring): string {
  const a = r.start * Math.PI / 180, b = (r.start + Math.min(359.9, r.sweep)) * Math.PI / 180;
  return `M${r.x + r.radius * Math.cos(a)} ${r.y + r.radius * Math.sin(a)} A${r.radius} ${r.radius} 0 ${r.sweep > 180 ? 1 : 0} 1 ${r.x + r.radius * Math.cos(b)} ${r.y + r.radius * Math.sin(b)}`;
}
function polygonPoints(vertices: Point[], count: number): Point[] {
  const distances = vertices.map((v, i) => Math.hypot(v.x - vertices[(i + 1) % vertices.length].x, v.y - vertices[(i + 1) % vertices.length].y));
  const total = distances.reduce((a, b) => a + b, 0);
  return Array.from({ length: count }, (_, i) => {
    let length = total * i / count, edge = 0;
    while (length > distances[edge] && edge < vertices.length - 1) length -= distances[edge++];
    const a = vertices[edge], b = vertices[(edge + 1) % vertices.length], t = length / distances[edge];
    return { x: lerp(a.x, b.x, t), y: lerp(a.y, b.y, t) };
  });
}

/** Animate inside the original SVG so identity, accessories and caller sizing stay intact. */
export function useMascotMorph(host: SVGSVGElement | null, mode: MascotMorph, paused: boolean, replay: number, identity: string) {
  const props = useRef({ mode, paused, replay });
  props.current = { mode, paused, replay };
  const wake = useRef<(() => void) | null>(null);
  const id = `mascot-morph-${useId().replace(/:/g, "")}`;
  useEffect(() => {
    if (!host) return;
    const original = host.querySelector<SVGGElement>(".mo-root");
    const source = original?.querySelector<SVGPathElement>(".mo-bob > g:not(.mo-eyes) > path");
    if (!original || !source || typeof source.getTotalLength !== "function") return;
    const saved = { transform: original.style.transform, origin: original.style.transformOrigin, opacity: original.style.opacity };
    const overlay = document.createElementNS("http://www.w3.org/2000/svg", "g");
    overlay.setAttribute("data-mascot-morph-art", "");
    overlay.innerHTML = `<defs><filter id="${id}" x="-40%" y="-40%" width="180%" height="180%" color-interpolation-filters="sRGB"><feGaussianBlur in="SourceGraphic" stdDeviation="1.7"/><feColorMatrix type="matrix" values="1 0 0 0 0 0 1 0 0 0 0 0 1 0 0 0 0 0 20 -9" result="shape"/><feFlood flood-color="var(--mo-head)"/><feComposite in2="shape" operator="in"/></filter></defs><g data-morph-fluid fill="var(--mo-head)"><g data-morph-core-group><path data-morph-core/><rect data-morph-ink fill="var(--mo-eye)"/><rect data-morph-ink fill="var(--mo-eye)"/><rect data-morph-ink fill="var(--mo-eye)"/></g><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/><circle data-morph-particle/></g><g fill="none" stroke="var(--mo-head)" stroke-linecap="round"><path data-morph-ring/><path data-morph-ring/><path data-morph-ring/><path data-morph-ring/><path data-morph-ring/></g><g fill="var(--mo-head)"><rect data-morph-bar/><rect data-morph-bar/><rect data-morph-bar/><rect data-morph-bar/><rect data-morph-bar/></g>`;
    original.before(overlay);
    const core = overlay.querySelector<SVGPathElement>("[data-morph-core]")!;
    const coreGroup = overlay.querySelector<SVGGElement>("[data-morph-core-group]")!;
    const fluidGroup = overlay.querySelector<SVGGElement>("[data-morph-fluid]")!;
    const originalPath = source.getAttribute("d")!;
    const length = source.getTotalLength();
    const points = Array.from({ length: 64 }, (_, i) => source.getPointAtLength(length * i / 64));
    const pencil = polygonPoints([{ x: 59, y: 15 }, { x: 59, y: 68 }, { x: 50, y: 87 }, { x: 41, y: 68 }, { x: 41, y: 15 }], 64);
    const particles = [...host.querySelectorAll<SVGCircleElement>("[data-morph-particle]")];
    const rings = [...host.querySelectorAll<SVGPathElement>("[data-morph-ring]")];
    const bars = [...host.querySelectorAll<SVGRectElement>("[data-morph-bar]")];
    const ink = [...host.querySelectorAll<SVGRectElement>("[data-morph-ink]")];
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    const current = scene();
    let frame = 0, previous = performance.now(), time = 0, lastMode = props.current.mode, lastReplay = props.current.replay;
    let inView = true, settledFor = 0;
    const setBar = (node: SVGRectElement, b: Bar) => {
      node.setAttribute("x", `${b.x - b.width / 2}`); node.setAttribute("y", `${b.y - b.height / 2}`);
      node.setAttribute("width", `${Math.max(0, b.width)}`); node.setAttribute("height", `${Math.max(0, b.height)}`);
      node.setAttribute("rx", `${Math.min(b.width, b.height) / 2}`); node.setAttribute("opacity", `${b.alpha}`);
      node.setAttribute("transform", `rotate(${b.angle} ${b.x} ${b.y})`);
    };
    const draw = (now: number) => {
      frame = 0;
      const { mode: nextMode, paused: stopped, replay: nextReplay } = props.current;
      // A frame's timestamp can precede the clock sampled while resuming it.
      const dt = Math.max(0, Math.min(40, now - previous)); previous = now;
      const changed = nextMode !== lastMode || nextReplay !== lastReplay;
      if (changed) { settledFor = 0; time = 0; lastMode = nextMode; lastReplay = nextReplay; }
      if (!stopped && !reduced.matches) time += dt / 1000;
      settledFor += dt;
      const target = targetScene(nextMode, reduced.matches ? 1.1 : time);
      const speed = reduced.matches ? 1 : 1 - Math.exp(-dt / 95);
      blend(current.core, target.core, speed);
      current.particles.forEach((p, i) => blend(p, target.particles[i], speed));
      current.rings.forEach((p, i) => blend(p, target.rings[i], speed));
      current.bars.forEach((p, i) => blend(p, target.bars[i], speed));
      current.ink.forEach((p, i) => blend(p, target.ink[i], speed));
      const c = current.core;
      const transform = `translate(${c.x} ${c.y}) rotate(${c.angle}) scale(${c.sx} ${c.sy}) translate(-50 -50)`;
      coreGroup.setAttribute("transform", transform);
      coreGroup.setAttribute("opacity", `${c.alpha * (1 - c.face)}`);
      original.style.transformOrigin = "0 0";
      original.style.transform = `translate(${c.x}px, ${c.y}px) rotate(${c.angle}deg) scale(${c.sx}, ${c.sy}) translate(-50px, -50px)`;
      original.style.opacity = `${c.face * c.alpha}`;
      if (c.fluid > .001 || c.pencil > .001) {
        const coords = points.map((p, i) => {
          const a = Math.atan2(p.y - 50, p.x - 50), wobble = 1 + .10 * Math.sin(time * 3.1 + a * 2);
          const x = lerp(p.x, 50 + Math.cos(a) * 39 * wobble, c.fluid), y = lerp(p.y, 50 + Math.sin(a) * 39 / wobble, c.fluid);
          return `${lerp(x, pencil[i].x, c.pencil)},${lerp(y, pencil[i].y, c.pencil)}`;
        });
        core.setAttribute("d", `M${coords.join(" L")}Z`);
      } else core.setAttribute("d", originalPath);
      fluidGroup.setAttribute("filter", c.fluid > .02 ? `url(#${id})` : "none");
      particles.forEach((node, i) => { const p = current.particles[i]; node.setAttribute("cx", `${p.x}`); node.setAttribute("cy", `${p.y}`); node.setAttribute("r", `${p.r}`); node.setAttribute("opacity", `${p.alpha}`); });
      rings.forEach((node, i) => { const r = current.rings[i]; node.setAttribute("d", arc(r)); node.setAttribute("stroke-width", `${r.width}`); node.setAttribute("opacity", `${r.alpha}`); });
      bars.forEach((node, i) => setBar(node, current.bars[i]));
      ink.forEach((node, i) => setBar(node, current.ink[i]));
      if (!document.hidden && inView && paintable() && !reduced.matches && (settledFor < 1200 || (!stopped && !STATIC_MORPHS.has(nextMode)))) frame = requestAnimationFrame(draw);
    };
    const paintable = () => !host.closest('[data-renderer-hidden], .cached-conversation-pane[data-active="false"]');
    const resume = () => { if (!frame && !document.hidden && inView && paintable()) { previous = performance.now(); frame = requestAnimationFrame(draw); } };
    wake.current = () => { settledFor = 0; resume(); };
    reduced.addEventListener("change", resume);
    const observer = new IntersectionObserver(entries => { inView = entries[0].isIntersecting; if (!inView) { cancelAnimationFrame(frame); frame = 0; } else resume(); });
    observer.observe(host);
    const visibility = new MutationObserver(resume);
    for (let node: Element | null = host.parentElement; node; node = node.parentElement) visibility.observe(node, { attributes: true, attributeFilter: ["data-active", "data-renderer-hidden"] });
    document.addEventListener("visibilitychange", resume);
    resume();
    return () => { wake.current = null; cancelAnimationFrame(frame); observer.disconnect(); visibility.disconnect(); document.removeEventListener("visibilitychange", resume); reduced.removeEventListener("change", resume); overlay.remove(); original.style.transform = saved.transform; original.style.transformOrigin = saved.origin; original.style.opacity = saved.opacity; };
  }, [host, identity, id]);
  useEffect(() => { wake.current?.(); }, [mode, paused, replay]);
}
