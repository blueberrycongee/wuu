import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PullToNewSession } from "./PullToNewSession";

let root: Root;
let host: HTMLElement;
let viewport: HTMLElement;
let geometry: { scrollHeight: number; clientHeight: number };
let coarse: boolean;
let onNewSession: ReturnType<typeof vi.fn>;
let contentRef: { current: HTMLDivElement };

function finishAnimation() {
  act(() => { vi.runAllTimers(); });
}

function touch(type: string, y: number, options: {
  x?: number; count?: number; target?: HTMLElement; cancelable?: boolean;
} = {}) {
  const point = { identifier: 1, clientX: options.x ?? 100, clientY: y };
  const event = new Event(type, { bubbles: true, cancelable: options.cancelable ?? true });
  Object.defineProperties(event, {
    touches: { value: type === "touchend" || type === "touchcancel" ? [] :
      Array.from({ length: options.count ?? 1 }, (_, index) => ({ ...point, identifier: index + 1 })) },
    changedTouches: { value: [point] },
  });
  act(() => { (options.target ?? viewport).dispatchEvent(event); });
  return event;
}

function render(key = "session-1") {
  act(() => root.render(<PullToNewSession key={key} containerRef={containerRef}
    contentRef={contentRef} bottomAnchor={null} onNewSession={onNewSession} />));
}
let containerRef: { current: HTMLElement };

beforeEach(() => {
  coarse = true;
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", vi.fn((query: string) => ({ matches: query === "(pointer: coarse)" && coarse })));
  document.documentElement.dataset.hostKind = "web";
  host = document.createElement("div");
  viewport = document.createElement("div");
  contentRef = { current: document.createElement("div") };
  viewport.append(contentRef.current);
  viewport.getBoundingClientRect = () => ({ left: 0, top: 50, width: 390, height: 600, bottom: 650, right: 390, x: 0, y: 50, toJSON() {} });
  document.body.append(host, viewport);
  geometry = { scrollHeight: 1000, clientHeight: 600 };
  for (const key of ["scrollHeight", "clientHeight"] as const) {
    Object.defineProperty(viewport, key, { get: () => geometry[key] });
  }
  viewport.scrollTop = 400;
  containerRef = { current: viewport };
  root = createRoot(host);
  onNewSession = vi.fn();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  viewport.remove();
  delete document.documentElement.dataset.hostKind;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("mobile pull to new session", () => {
  it("shows progress, opens only on release, and ignores duplicate releases", () => {
    render();
    touch("touchstart", 400);
    expect(touch("touchmove", 360).defaultPrevented).toBe(true);
    const progress = document.querySelector('[role="status"]')?.getAttribute("aria-label");
    expect(progress).toBeTruthy();
    touch("touchmove", 300);
    expect(document.querySelector('[role="status"]')?.getAttribute("aria-label")).not.toBe(progress);
    expect(onNewSession).not.toHaveBeenCalled();
    touch("touchend", 300);
    touch("touchend", 300);
    expect(onNewSession).not.toHaveBeenCalled();
    finishAnimation();
    expect(onNewSession).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it("requires a fresh gesture after scrolling from history to bottom", () => {
    render();
    viewport.scrollTop = 100;
    touch("touchstart", 400);
    viewport.scrollTop = 400;
    expect(touch("touchmove", 280).defaultPrevented).toBe(false);
    touch("touchend", 280);
    expect(onNewSession).not.toHaveBeenCalled();
    touch("touchstart", 400);
    touch("touchmove", 300);
    touch("touchend", 300);
    finishAnimation();
    expect(onNewSession).toHaveBeenCalledOnce();
  });

  it.each(["short", "retreat", "cancel", "multitouch", "horizontal", "resize", "selection", "native", "stream", "switch"])("cancels on %s", (reason) => {
    render();
    touch("touchstart", 400);
    touch("touchmove", 300);
    let endY = 300;
    switch (reason) {
      case "short":
      case "retreat": endY = 370; touch("touchmove", endY); break;
      case "cancel": touch("touchcancel", 300); break;
      case "multitouch": touch("touchmove", 300, { count: 2 }); break;
      case "horizontal": touch("touchmove", 300, { x: 250 }); break;
      case "resize": act(() => window.dispatchEvent(new Event("resize"))); break;
      case "selection": act(() => document.dispatchEvent(new Event("selectionchange"))); break;
      case "native": touch("touchmove", 280, { cancelable: false }); break;
      case "stream": geometry.scrollHeight += 200; break;
      case "switch": render("session-2"); break;
    }
    touch("touchend", endY);
    finishAnimation();
    expect(onNewSession).not.toHaveBeenCalled();
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it.each(["button", "code", "nested"])("leaves %s interactions native", (kind) => {
    const target = document.createElement(kind === "button" ? "button" : "div");
    if (kind === "code") target.style.overflowX = "auto";
    if (kind === "nested") target.setAttribute("data-wuu-nested-scroll", "true");
    viewport.append(target);
    render();
    touch("touchstart", 400, { target });
    expect(touch("touchmove", 280, { target }).defaultPrevented).toBe(false);
    touch("touchend", 280, { target });
    expect(onNewSession).not.toHaveBeenCalled();
  });

  it.each(["desktop", "fine-pointer"])("does not intercept %s", (kind) => {
    if (kind === "desktop") document.documentElement.dataset.hostKind = "desktop";
    else coarse = false;
    render();
    touch("touchstart", 400);
    expect(touch("touchmove", 280).defaultPrevented).toBe(false);
    touch("touchend", 280);
    expect(onNewSession).not.toHaveBeenCalled();
  });

  it("ignores wheel and downward touch but allows a short populated conversation", () => {
    geometry.scrollHeight = 300;
    viewport.scrollTop = 0;
    render();
    act(() => viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: 200 })));
    touch("touchstart", 200);
    expect(touch("touchmove", 320).defaultPrevented).toBe(false);
    touch("touchend", 320);
    expect(onNewSession).not.toHaveBeenCalled();
    touch("touchstart", 400);
    touch("touchmove", 300);
    touch("touchend", 300);
    finishAnimation();
    expect(onNewSession).toHaveBeenCalledOnce();
  });

  it("cancels a pending opening when the session changes during the transition", () => {
    render();
    touch("touchstart", 400);
    touch("touchmove", 300);
    touch("touchend", 300);
    render("session-2");
    finishAnimation();
    expect(onNewSession).not.toHaveBeenCalled();
    touch("touchstart", 400);
    touch("touchmove", 300);
    touch("touchend", 300);
    finishAnimation();
    expect(onNewSession).toHaveBeenCalledOnce();
  });

  it("allows another attempt after an action that leaves the current session open", () => {
    render();
    for (let attempt = 0; attempt < 2; attempt++) {
      touch("touchstart", 400);
      touch("touchmove", 300);
      touch("touchend", 300);
      finishAnimation();
    }
    expect(onNewSession).toHaveBeenCalledTimes(2);
  });
});
