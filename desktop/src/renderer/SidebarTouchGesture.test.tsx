import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSidebarTouchGesture } from "./SidebarTouchGesture";
import { PullToNewSession } from "./PullToNewSession";

let root: Root;
let host: HTMLDivElement;
let target: HTMLElement;
let open: ReturnType<typeof vi.fn>;
let close: ReturnType<typeof vi.fn>;
let newSession: ReturnType<typeof vi.fn>;

function Harness({ opened, withPull = false }: { opened: boolean; withPull?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  useSidebarTouchGesture(ref, true, opened ? "open" : "closed", open, close);
  return <div ref={ref}><div className="sidebar" /><div ref={viewport} className="scroll-region"><div ref={content} /></div>
    <button className="compact-session-switcher-backdrop" />
    {withPull && <PullToNewSession containerRef={viewport} contentRef={content} bottomAnchor={null} onNewSession={newSession} />}
  </div>;
}

function render(opened = false, withPull = false) {
  act(() => root.render(<Harness opened={opened} withPull={withPull} />));
  const sidebar = host.querySelector<HTMLElement>(".sidebar")!;
  vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue({ width: 280 } as DOMRect);
  target = opened ? sidebar : host.querySelector<HTMLElement>(".scroll-region")!;
}

function touch(type: string, dx: number, dy: number, cancelable = true) {
  const point = { identifier: 1, clientX: 180 + dx, clientY: 300 + dy };
  const event = new Event(type, { bubbles: true, cancelable });
  Object.defineProperties(event, {
    touches: { value: type === "touchend" ? [] : [point] },
    changedTouches: { value: [point] },
  });
  act(() => target.dispatchEvent(event));
  return event;
}

function finish(dx: number, dy: number) {
  touch("touchend", dx, dy);
  act(() => vi.runAllTimers());
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", vi.fn((query: string) => ({ matches: query === "(pointer: coarse)" })));
  document.documentElement.dataset.hostKind = "web";
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  open = vi.fn();
  close = vi.fn();
  newSession = vi.fn();
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  delete document.documentElement.dataset.hostKind;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("sidebar thumb gestures", () => {
  it.each([false, true])("opens from an upward thumb arc at the bottom with sparse events=%s", (sparse) => {
    render(false, true);
    Object.defineProperties(target, {
      scrollHeight: { value: 800 }, clientHeight: { value: 600 }, scrollTop: { value: 200 },
    });
    touch("touchstart", 0, 0);
    if (!sparse) {
      expect(touch("touchmove", 3, -4).defaultPrevented).toBe(false);
      expect(touch("touchmove", 9, -12).defaultPrevented).toBe(true);
    }
    expect(touch("touchmove", 24, -32).defaultPrevented).toBe(true);
    touch("touchmove", 72, -90);
    finish(72, -90);
    expect(open).toHaveBeenCalledOnce();
    expect(newSession).not.toHaveBeenCalled();
  });

  it("leaves a vertical pull at the bottom to new-session navigation", () => {
    render(false, true);
    Object.defineProperties(target, {
      scrollHeight: { value: 800 }, clientHeight: { value: 600 }, scrollTop: { value: 200 },
    });
    touch("touchstart", 0, 0);
    touch("touchmove", 2, -20);
    touch("touchmove", 5, -100);
    finish(5, -100);
    expect(open).not.toHaveBeenCalled();
    expect(newSession).toHaveBeenCalledOnce();
  });

  it.each([false, true])("accepts diagonal drags when opened=%s", (opened) => {
    render(opened);
    const direction = opened ? -1 : 1;
    touch("touchstart", 0, 0);
    expect(touch("touchmove", direction * 12, 10).defaultPrevented).toBe(true);
    touch("touchmove", direction * 180, 130);
    finish(direction * 180, 130);
    expect(opened ? close : open).toHaveBeenCalledOnce();
  });

  it("allows an ambiguous thumb arc to become a horizontal drag", () => {
    render();
    touch("touchstart", 0, 0);
    expect(touch("touchmove", 9, -12).defaultPrevented).toBe(true);
    expect(touch("touchmove", 18, -15).defaultPrevented).toBe(true);
    touch("touchmove", 90, -40);
    finish(90, -40);
    expect(open).toHaveBeenCalledOnce();
  });

  it.each(["vertical", "native", "opposite"])("never reclaims a %s gesture", (kind) => {
    render();
    touch("touchstart", 0, 0);
    if (kind === "vertical") touch("touchmove", 4, 14);
    if (kind === "opposite") touch("touchmove", -14, 2);
    if (kind === "native") {
      touch("touchmove", 9, 12);
      touch("touchmove", 18, 15, false);
    }
    expect(touch("touchmove", 90, 30).defaultPrevented).toBe(false);
    finish(90, 30);
    expect(open).not.toHaveBeenCalled();
  });

  it.each([-1, 1])("accepts a sparse diagonal first move with vertical direction %s", (direction) => {
    render();
    touch("touchstart", 0, 0);
    expect(touch("touchmove", 24, direction * 32).defaultPrevented).toBe(true);
    touch("touchmove", 36, direction * 45);
    finish(36, direction * 45);
    expect(open).toHaveBeenCalledOnce();
  });

  it.each([false, true])("catches and reverses a settling drawer when opened=%s", (opened) => {
    render(opened);
    const direction = opened ? -1 : 1;
    touch("touchstart", 0, 0);
    touch("touchmove", direction * 180, 0);
    touch("touchend", direction * 180, 0);
    expect(opened ? close : open).not.toHaveBeenCalled();

    const sidebar = host.querySelector<HTMLElement>(".sidebar")!;
    // The compositor is halfway through the release animation.
    vi.mocked(sidebar.getBoundingClientRect).mockReturnValue(new DOMRect(-140, 0, 280, 800));
    target = host.querySelector<HTMLElement>(".compact-session-switcher-backdrop")!;
    touch("touchstart", 0, 0);
    expect(sidebar.style.transform).toBe("translate3d(-140px, 0, 0)");
    touch("touchmove", -direction * 120, 0);
    act(() => vi.advanceTimersToNextFrame());
    expect(sidebar.style.transform).toBe(`translate3d(${opened ? -20 : -260}px, 0, 0)`);
    finish(-direction * 120, 0);
    expect(open).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(sidebar.style.transform).toBe("");
  });

  it.each(["touchend", "touchcancel", "vertical"])("resumes a caught animation after %s without a horizontal drag", (ending) => {
    render();
    touch("touchstart", 0, 0);
    touch("touchmove", 180, 0);
    touch("touchend", 180, 0);
    const sidebar = host.querySelector<HTMLElement>(".sidebar")!;
    vi.mocked(sidebar.getBoundingClientRect).mockReturnValue(new DOMRect(-100, 0, 280, 800));
    touch("touchstart", 0, 0);
    if (ending === "vertical") expect(touch("touchmove", 0, 30).defaultPrevented).toBe(false);
    else touch(ending, 0, 0);
    act(() => vi.runAllTimers());
    expect(open).toHaveBeenCalledOnce();
  });

  it("tracks the latest finger position and discards queued frames on resize", () => {
    render();
    const sidebar = host.querySelector<HTMLElement>(".sidebar")!;
    touch("touchstart", 0, 0);
    touch("touchmove", 30, 0);
    touch("touchmove", 90, 0);
    act(() => vi.advanceTimersToNextFrame());
    expect(sidebar.style.transform).toBe("translate3d(-190px, 0, 0)");
    touch("touchmove", 120, 0);
    act(() => window.dispatchEvent(new Event("resize")));
    act(() => vi.runAllTimers());
    expect(sidebar.style.transform).toBe("");
    expect(host.querySelector<HTMLElement>(".compact-session-switcher-backdrop")!.style.opacity).toBe("");
    expect(open).not.toHaveBeenCalled();
  });
});
