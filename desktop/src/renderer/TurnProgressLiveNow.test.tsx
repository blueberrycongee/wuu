import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useLiveNow } from "./TurnProgress";

let root: Root | undefined;

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  document.body.innerHTML = "";
  vi.useRealTimers();
});

describe("useLiveNow", () => {
  it("does not enqueue an extra state update when the live timer mounts", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-07T00:00:00Z"));
    const rendered: number[] = [];

    function Probe(): null {
      rendered.push(useLiveNow(true));
      return null;
    }

    const container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => root?.render(createElement(Probe)));

    expect(rendered).toEqual([Date.now()]);
    act(() => vi.advanceTimersByTime(1_000));
    expect(rendered).toEqual([Date.now() - 1_000, Date.now()]);
  });

  it("uses the current time when an inactive timer becomes active", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-07T00:00:00Z"));
    const rendered: number[] = [];

    function Probe({ active }: { active: boolean }): null {
      rendered.push(useLiveNow(active));
      return null;
    }

    const container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    act(() => root?.render(createElement(Probe, { active: false })));

    vi.advanceTimersByTime(23_000);
    act(() => root?.render(createElement(Probe, { active: true })));

    expect(rendered.at(-1)).toBe(Date.now());
  });
});
