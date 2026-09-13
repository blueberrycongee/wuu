import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { useMascotMorph, type MascotMorph } from "./useMascotMorph";

let cleanup = () => {};
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

function mount() {
  vi.useFakeTimers();
  vi.stubGlobal("IntersectionObserver", class { observe() {} disconnect() {} });
  const reduced = new EventTarget() as MediaQueryList;
  Object.defineProperty(reduced, "matches", { value: false, writable: true });
  vi.stubGlobal("matchMedia", () => reduced);
  const host = document.createElement("div");
  host.innerHTML = '<svg><g class="mo-root"><g class="mo-bob"><g><path d="M11 50 H89" /></g><g class="mo-eyes" /></g></g></svg>';
  document.body.append(host);
  const svg = host.querySelector("svg")!;
  const body = svg.querySelector("path")!;
  Object.assign(body, {
    getTotalLength: () => 245,
    getPointAtLength: (d: number) => ({ x: 50 + 39 * Math.cos(d / 39), y: 50 + 39 * Math.sin(d / 39) }),
  });
  const root = createRoot(document.createElement("div"));
  function Motion({ mode, paused }: { mode: MascotMorph; paused: boolean }) { useMascotMorph(svg, mode, paused, 0, "round"); return null; }
  const render = (mode: MascotMorph, paused = false) => act(() => root.render(<Motion mode={mode} paused={paused} />));
  render("dots");
  cleanup = () => { act(() => root.unmount()); host.remove(); };
  const advance = (ms: number) => act(() => vi.advanceTimersByTime(ms));
  return { svg, body, render, advance, reduced };
}

it("retargets rapid morphs without replacing the identity and restores its authored contour", () => {
  const { svg, body, render, advance } = mount();
  const art = svg.querySelector("[data-mascot-morph-art]");
  for (const mode of ["pencil", "whirl", "wave", "idle"] as const) { render(mode); advance(80); }
  advance(1500);
  expect(svg.querySelector("[data-mascot-morph-art]")).toBe(art);
  expect(svg.querySelector(".mo-bob path")).toBe(body);
  expect(svg.querySelector("[data-morph-core]")?.getAttribute("d")).toBe(body.getAttribute("d"));
  expect(Number(svg.querySelector<SVGGElement>(".mo-root")!.style.opacity)).toBeGreaterThan(.99);
  expect(svg.innerHTML).not.toMatch(/NaN|Infinity/);
});

it("parks paused and reduced-motion scenes and removes its artwork on unmount", () => {
  const { svg, render, advance, reduced } = mount();
  render("wave", true); advance(1600);
  const paused = svg.innerHTML;
  advance(500); expect(svg.innerHTML).toBe(paused);
  Object.defineProperty(reduced, "matches", { value: true });
  act(() => reduced.dispatchEvent(new Event("change")));
  render("pencil"); advance(50);
  const still = svg.innerHTML;
  advance(1000); expect(svg.innerHTML).toBe(still);
  cleanup();
  expect(svg.querySelector("[data-mascot-morph-art]")).toBeNull();
  expect(svg.querySelector<SVGGElement>(".mo-root")!.style.opacity).toBe("");
  cleanup = () => {};
});
