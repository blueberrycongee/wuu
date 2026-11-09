import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { useMascotCoalescence } from "./useMascotCoalescence";

let cleanup = () => {};
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function mount() {
  vi.useFakeTimers();
  vi.spyOn(Math, "random").mockReturnValue(0);
  const reduced = new EventTarget() as MediaQueryList;
  Object.defineProperty(reduced, "matches", { value: false, writable: true });
  vi.stubGlobal("matchMedia", () => reduced);
  const host = document.createElement("div");
  host.innerHTML = '<svg><g class="mo-bob"><g><path d="M11 50 H89" /></g><g class="mo-eyes" /></g></svg>';
  document.body.append(host);
  const svg = host.querySelector("svg")!;
  const body = svg.querySelector("path")!;
  // jsdom has no SVG geometry; the browser supplies these contour measurements.
  Object.assign(body, {
    getBBox: () => ({ x: 11, y: 11, width: 78, height: 78 }),
    getTotalLength: () => 245,
    getPointAtLength: (distance: number) => ({ x: 50 + 39 * Math.cos(distance / 39), y: 50 + 39 * Math.sin(distance / 39) }),
  });
  const root = createRoot(document.createElement("div"));
  function Gesture({ mode }: { mode: "idle" | "off" }) { useMascotCoalescence(svg, mode, "round"); return null; }
  const render = (mode: "idle" | "off") => act(() => root.render(<Gesture mode={mode} />));
  render("idle");
  cleanup = () => { act(() => root.unmount()); host.remove(); };
  return { svg, body, reduced, render };
}

it("returns to the original body after interaction and cancels when work starts", () => {
  const { svg, body, render } = mount();
  act(() => vi.advanceTimersByTime(20_500));
  expect(svg.querySelector("[data-wuu-liquid]")).not.toBeNull();
  expect(body.style.visibility).toBe("hidden");
  act(() => { document.dispatchEvent(new KeyboardEvent("keydown")); vi.advanceTimersByTime(200); });
  expect(svg.querySelector("[data-wuu-liquid]")).toBeNull();
  expect(body.style.visibility).toBe("");
  expect(svg.querySelector("path")).toBe(body);
  act(() => vi.advanceTimersByTime(20_500));
  expect(svg.querySelector("[data-wuu-liquid]")).not.toBeNull();
  render("off");
  act(() => vi.advanceTimersByTime(200));
  expect(svg.querySelector("[data-wuu-liquid]")).toBeNull();
  expect(body.style.visibility).toBe("");
  act(() => vi.advanceTimersByTime(50_000));
  expect(svg.querySelector("[data-wuu-liquid]")).toBeNull();
});

it("removes active artwork and stops scheduling when reduced motion is enabled", () => {
  const { svg, body, reduced } = mount();
  act(() => vi.advanceTimersByTime(20_500));
  expect(svg.querySelector("[data-wuu-liquid]")).not.toBeNull();
  act(() => {
    Object.defineProperty(reduced, "matches", { value: true });
    reduced.dispatchEvent(new Event("change"));
    vi.advanceTimersByTime(50_000);
  });
  expect(svg.querySelector("[data-wuu-liquid]")).toBeNull();
  expect(svg.querySelector("filter")).toBeNull();
  expect(body.style.visibility).toBe("");
});
