import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { UsageOverviewResponse, WuuDesktopApi } from "../shared/protocol";
import { EmptyHomeOverview } from "./EmptyHomeOverview";

let root: Root | undefined;
let container: HTMLDivElement | undefined;
const animateDescriptor = Object.getOwnPropertyDescriptor(Element.prototype, "animate");
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(Element.prototype, "getAnimations");
const hitTestDescriptor = Object.getOwnPropertyDescriptor(document, "elementFromPoint");

// JSDOM has no Web Animations: the card mounts with no entrance running
// unless a test supplies one.
beforeEach(() => {
  Object.defineProperty(Element.prototype, "getAnimations", { configurable: true, value: () => [] });
});

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  container?.remove();
  container = undefined;
  vi.restoreAllMocks();
  vi.useRealTimers();
  if (animateDescriptor) Object.defineProperty(Element.prototype, "animate", animateDescriptor);
  else Reflect.deleteProperty(Element.prototype, "animate");
  if (getAnimationsDescriptor) Object.defineProperty(Element.prototype, "getAnimations", getAnimationsDescriptor);
  else Reflect.deleteProperty(Element.prototype, "getAnimations");
  if (hitTestDescriptor) Object.defineProperty(document, "elementFromPoint", hitTestDescriptor);
  else Reflect.deleteProperty(document, "elementFromPoint");
  delete (window as unknown as { wuu?: unknown }).wuu;
});

function installWuuStub(overrides: Partial<WuuDesktopApi>): void {
  (window as unknown as { wuu: WuuDesktopApi }).wuu = {
    ...overrides,
  } as WuuDesktopApi;
}

async function renderOverview(): Promise<HTMLDivElement> {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root?.render(<EmptyHomeOverview />);
    await Promise.resolve();
    await Promise.resolve();
  });
  return container;
}

function storeWithoutUsage(): UsageOverviewResponse {
  return {
    total_sessions: 0,
    metrics: {
      prompt_tokens: 0,
      context_tokens: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
      date_range: ["", ""],
      active_days: 0,
    },
    days: [],
  };
}

describe("EmptyHomeOverview", () => {
  it("starts idle play after the entrance and resumes it after input interrupts", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "performance"] });
    installWuuStub({ getUsageOverview: vi.fn().mockResolvedValue(storeWithoutUsage()) });
    // The card's entrance runs until the test finishes it.
    let finishEntrance!: () => void;
    const entrance = { finished: new Promise<void>((resolve) => (finishEntrance = resolve)) };
    Object.defineProperty(Element.prototype, "getAnimations", { configurable: true, value: () => [entrance] });
    const view = await renderOverview();
    view.className = "empty-home";
    const mascot = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    mascot.classList.add("empty-home-mascot");
    mascot.innerHTML = '<g class="mo-eyes"></g>';
    view.prepend(mascot);

    // JSDOM has no layout or Web Animations. Keep animations pending so
    // input, rather than their natural completion, ends the first play.
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 10, 10));
    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: () => view.querySelector(".empty-home-heatmap-week")?.lastElementChild,
    });
    const animate = vi.fn(() => ({
      finished: new Promise(() => undefined),
      cancel: vi.fn(),
      pause: vi.fn(),
    }));
    Object.defineProperty(Element.prototype, "animate", { configurable: true, value: animate });

    // Scenes measure cell positions, so the idle wait starts only once the
    // entrance has settled the cells.
    act(() => vi.advanceTimersByTime(20_000));
    expect(animate).not.toHaveBeenCalled();
    await act(async () => finishEntrance());
    act(() => vi.advanceTimersByTime(19_000));
    expect(animate).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1_000));
    expect(animate).toHaveBeenCalled();
    act(() => window.dispatchEvent(new Event("pointermove")));
    const callsAfterInput = animate.mock.calls.length;
    act(() => vi.advanceTimersByTime(19_000));
    expect(animate).toHaveBeenCalledTimes(callsAfterInput);
    act(() => vi.advanceTimersByTime(1_000));
    expect(animate.mock.calls.length).toBeGreaterThan(callsAfterInput);
  });

  it("shows nothing instead of zero usage when the host cannot answer", async () => {
    installWuuStub({
      getUsageOverview: vi.fn().mockRejectedValue(new Error("unknown method usage/overview")),
    });

    const view = await renderOverview();

    expect(view.querySelector(".empty-home-overview")).toBeNull();
  });

  it("shows zero totals and an empty heatmap for a store without usage", async () => {
    const getUsageOverview = vi.fn().mockResolvedValue(storeWithoutUsage());
    installWuuStub({ getUsageOverview });

    const view = await renderOverview();

    // Days must line up with the calendar the renderer draws.
    expect(getUsageOverview).toHaveBeenCalledWith({
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    });
    const values = [...view.querySelectorAll(".empty-home-stats dd")].map((value) => value.textContent);
    expect(values).toEqual(["0", "0", "0"]);
    const cells = [...view.querySelectorAll(".empty-home-heatmap-week i")];
    expect(cells.length).toBeGreaterThan(0);
    expect(cells.every((cell) => cell.getAttribute("data-level") === "0")).toBe(true);
  });
});
