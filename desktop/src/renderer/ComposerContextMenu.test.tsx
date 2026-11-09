/**
 * Tests for ComposerContextMenu — the composer textarea's right-click
 * edit menu. Placement geometry (flip-then-clamp) is covered through
 * the exported placeContextMenu pure function; component tests cover
 * re-anchoring when a second right-click moves the menu while it is
 * already open, and the dismiss-on-scroll/resize wiring. jsdom does no
 * layout, so the menu's size is stubbed via offsetWidth/offsetHeight
 * prototype getters and the viewport via window.innerWidth/innerHeight.
 */
import { act, createElement, type RefObject } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ComposerContextMenu, placeContextMenu } from "./ComposerContextMenu";

const MENU_WIDTH = 120;
const MENU_HEIGHT = 160;
const VIEWPORT_WIDTH = 1000;
const VIEWPORT_HEIGHT = 800;
const MARGIN = 8;

describe("placeContextMenu", () => {
  const place = (x: number, y: number) =>
    placeContextMenu(x, y, MENU_WIDTH, MENU_HEIGHT, VIEWPORT_WIDTH, VIEWPORT_HEIGHT);

  it("grows down-right from the cursor when there is room", () => {
    expect(place(100, 100)).toEqual({ left: 100, top: 100, origin: "top-left" });
  });

  it("flips above the cursor when the bottom edge is close", () => {
    expect(place(100, 700)).toEqual({
      left: 100,
      top: 700 - MENU_HEIGHT,
      origin: "bottom-left"
    });
  });

  it("flips left of the cursor when the right edge is close", () => {
    expect(place(950, 100)).toEqual({
      left: 950 - MENU_WIDTH,
      top: 100,
      origin: "top-right"
    });
  });

  it("flips both axes in the bottom-right corner", () => {
    expect(place(950, 700)).toEqual({
      left: 950 - MENU_WIDTH,
      top: 700 - MENU_HEIGHT,
      origin: "bottom-right"
    });
  });

  it("keeps the preferred side and clamps when neither side fits", () => {
    const layout = placeContextMenu(50, 50, MENU_WIDTH, MENU_HEIGHT, 100, 100);
    expect(layout).toEqual({ left: MARGIN, top: MARGIN, origin: "top-left" });
  });
});

let mountedRoots: Root[] = [];
let mountedNodes: HTMLElement[] = [];
let restoreStubs: Array<() => void> = [];

afterEach(() => {
  for (const root of mountedRoots) {
    act(() => {
      root.unmount();
    });
  }
  for (const node of mountedNodes) {
    node.remove();
  }
  mountedRoots = [];
  mountedNodes = [];
  for (const restore of restoreStubs) {
    restore();
  }
  restoreStubs = [];
});

function stubProperty(target: object, key: string, value: unknown): void {
  const original = Object.getOwnPropertyDescriptor(target, key);
  Object.defineProperty(target, key, { configurable: true, get: () => value });
  restoreStubs.push(() => {
    if (original) {
      Object.defineProperty(target, key, original);
    } else {
      delete (target as Record<string, unknown>)[key];
    }
  });
}

function stubMenuGeometry(): void {
  stubProperty(HTMLElement.prototype, "offsetWidth", MENU_WIDTH);
  stubProperty(HTMLElement.prototype, "offsetHeight", MENU_HEIGHT);
  stubProperty(window, "innerWidth", VIEWPORT_WIDTH);
  stubProperty(window, "innerHeight", VIEWPORT_HEIGHT);
}

function mountMenu(props: { x: number; y: number; onClose?: () => void }): {
  render: (next: { x: number; y: number }) => void;
  menu: () => HTMLElement;
} {
  const textarea = document.createElement("textarea");
  document.body.appendChild(textarea);
  mountedNodes.push(textarea);
  const textareaRef: RefObject<HTMLTextAreaElement | null> = { current: textarea };
  const container = document.createElement("div");
  document.body.appendChild(container);
  mountedNodes.push(container);
  const root = createRoot(container);
  mountedRoots.push(root);
  const render = (next: { x: number; y: number }) => {
    act(() => {
      root.render(
        createElement(ComposerContextMenu, {
          textareaRef,
          x: next.x,
          y: next.y,
          hasSelection: true,
          onClose: props.onClose ?? (() => {}),
          onValueChange: () => {}
        })
      );
    });
  };
  render(props);
  return {
    render,
    menu: () => {
      const element = document.body.querySelector<HTMLElement>(
        ".composer-textarea-context-menu"
      );
      if (!element) {
        throw new Error("menu not mounted");
      }
      return element;
    }
  };
}

/** Lets the menu's deferred (setTimeout 0) dismiss listeners attach. */
async function flushListenerAttach(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

describe("ComposerContextMenu", () => {
  it("positions at the cursor before first paint", () => {
    stubMenuGeometry();
    const { menu } = mountMenu({ x: 200, y: 150 });
    expect(menu().style.left).toBe("200px");
    expect(menu().style.top).toBe("150px");
    expect(menu().style.visibility).toBe("");
    expect(menu().dataset.origin).toBe("top-left");
  });

  it("re-anchors to fresh coordinates when reopened while already open", () => {
    stubMenuGeometry();
    const { render, menu } = mountMenu({ x: 100, y: 100 });
    // Second right-click at an unclamped position: the menu must follow
    // even though no viewport edge forces a reposition (regression test
    // for the placement only updating when clamping changed it).
    render({ x: 300, y: 200 });
    expect(menu().style.left).toBe("300px");
    expect(menu().style.top).toBe("200px");
  });

  it("opens upward when the cursor is near the bottom of the viewport", () => {
    stubMenuGeometry();
    const { menu } = mountMenu({ x: 100, y: VIEWPORT_HEIGHT - 40 });
    expect(menu().style.top).toBe(`${VIEWPORT_HEIGHT - 40 - MENU_HEIGHT}px`);
    expect(menu().dataset.origin).toBe("bottom-left");
  });

  it("closes on scroll", async () => {
    stubMenuGeometry();
    const onClose = vi.fn();
    mountMenu({ x: 100, y: 100, onClose });
    await flushListenerAttach();
    act(() => {
      document.dispatchEvent(new Event("scroll"));
    });
    expect(onClose).toHaveBeenCalled();
  });

  it("closes on window resize", async () => {
    stubMenuGeometry();
    const onClose = vi.fn();
    mountMenu({ x: 100, y: 100, onClose });
    await flushListenerAttach();
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });
    expect(onClose).toHaveBeenCalled();
  });
});
