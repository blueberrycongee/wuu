import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { TruncatedText } from "./TruncatedText";

// jsdom has no layout engine and no ResizeObserver. The mock captures the
// observer callback so tests can simulate a layout change: define fake
// scroll/client metrics, then fire the callback to trigger a re-measure.
class MockResizeObserver {
  static instances: MockResizeObserver[] = [];
  private callback: ResizeObserverCallback;

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    MockResizeObserver.instances.push(this);
  }

  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}

  fire(): void {
    this.callback([], this as unknown as ResizeObserver);
  }
}

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  MockResizeObserver.instances = [];
  vi.stubGlobal("ResizeObserver", MockResizeObserver);
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  document
    .querySelectorAll(".tooltip-layer")
    .forEach((layer) => layer.remove());
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function renderTruncated(text = "完整的长文本内容"): HTMLElement {
  act(() => {
    root = createRoot(container);
    root!.render(<TruncatedText text={text} className="subject" />);
  });
  const element = container.querySelector(".subject");
  if (!(element instanceof HTMLElement)) {
    throw new Error("truncated text element not rendered");
  }
  return element;
}

function hover(element: Element, ms = 500): void {
  const wrapper = element.closest("[data-wuu-tooltip-trigger]");
  if (!wrapper) {
    throw new Error("tooltip trigger wrapper not found");
  }
  act(() => {
    wrapper.dispatchEvent(new Event("pointerover", { bubbles: true }));
    vi.advanceTimersByTime(ms);
  });
}

function remeasure(element: HTMLElement, metrics: Partial<{
  scrollWidth: number;
  clientWidth: number;
  scrollHeight: number;
  clientHeight: number;
}>): void {
  for (const [key, value] of Object.entries(metrics)) {
    Object.defineProperty(element, key, { value, configurable: true });
  }
  act(() => {
    MockResizeObserver.instances.forEach((instance) => instance.fire());
  });
}

function tooltipLayer(): HTMLElement | null {
  return document.querySelector(".tooltip-layer");
}

describe("TruncatedText", () => {
  it("renders the text itself and stays quiet while nothing is truncated", () => {
    const element = renderTruncated();
    // jsdom reports zero layout: scrollWidth == clientWidth, so the
    // tooltip never arms.
    hover(element);
    expect(tooltipLayer()).toBeNull();
    expect(element.textContent).toBe("完整的长文本内容");
  });

  it("reveals the full text on hover once CSS truncation hides part of it", () => {
    const element = renderTruncated();
    remeasure(element, { scrollWidth: 320, clientWidth: 120 });
    hover(element);
    expect(tooltipLayer()?.textContent).toBe("完整的长文本内容");
  });

  it("respects multi-line clamp via scrollHeight", () => {
    const element = renderTruncated();
    remeasure(element, { scrollHeight: 96, clientHeight: 40 });
    hover(element);
    expect(tooltipLayer()?.textContent).toBe("完整的长文本内容");
  });

  it("disarms again when the box grows wide enough", () => {
    const element = renderTruncated();
    remeasure(element, { scrollWidth: 320, clientWidth: 120 });
    hover(element);
    expect(tooltipLayer()).not.toBeNull();
    act(() => {
      element
        .closest("[data-wuu-tooltip-trigger]")!
        .dispatchEvent(new Event("pointerout", { bubbles: true }));
    });
    expect(tooltipLayer()).toBeNull();

    remeasure(element, { scrollWidth: 320, clientWidth: 640 });
    hover(element);
    expect(tooltipLayer()).toBeNull();
  });
});
