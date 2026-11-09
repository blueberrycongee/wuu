import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SidebarCollapseBody } from "./SidebarSection";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  document.documentElement.style.removeProperty("--motion-slow");
  vi.restoreAllMocks();
  vi.useRealTimers();
});

function Content(): JSX.Element {
  const [count, setCount] = useState(0);
  return <button onClick={() => setCount(count + 1)}>{count}</button>;
}

function render(expanded: boolean): void {
  act(() => root.render(<SidebarCollapseBody expanded={expanded}><Content /></SidebarCollapseBody>));
}

function finishTransition(element: Element, propertyName = "height"): void {
  const event = new Event("transitionend", { bubbles: true });
  Object.defineProperty(event, "propertyName", { value: propertyName });
  act(() => element.dispatchEvent(event));
}

describe("SidebarCollapseBody", () => {
  it("mounts rows on demand and releases them only when its own height finishes closing", () => {
    render(false);
    expect(container.querySelector("button")).toBeNull();
    render(true);
    const button = container.querySelector("button")!;
    render(false);
    const body = container.firstElementChild!;
    expect(body.getAttribute("aria-hidden")).toBe("true");
    expect(body.hasAttribute("inert")).toBe(true);
    finishTransition(button);
    finishTransition(body, "opacity");
    expect(container.querySelector("button")).toBe(button);
    finishTransition(body);
    expect(container.querySelector("button")).toBeNull();
  });

  it("preserves row state when closing is reversed and ignores stale completion", () => {
    render(true);
    const button = container.querySelector("button")!;
    act(() => button.click());
    render(false);
    render(true);
    finishTransition(container.firstElementChild!);
    act(() => vi.runAllTimers());
    expect(container.querySelector("button")).toBe(button);
    expect(button.textContent).toBe("1");
    expect(container.firstElementChild!.hasAttribute("inert")).toBe(false);
  });

  it("uses the current motion duration for cleanup when no transition event arrives", () => {
    render(true);
    // A theme may load after the module, so retention cannot be a module constant.
    document.documentElement.style.setProperty("--motion-slow", "1s");
    render(false);
    act(() => vi.advanceTimersByTime(500));
    expect(container.querySelector("button")).not.toBeNull();
    act(() => vi.runAllTimers());
    expect(container.querySelector("button")).toBeNull();
  });

  it("releases closed rows immediately when motion is reduced", () => {
    render(true);
    vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true } as MediaQueryList);
    render(false);
    expect(container.querySelector("button")).toBeNull();
  });
});
