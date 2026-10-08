import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as ComposerFocus from "./ComposerFocus";
import {
  rightPanelMotionMs,
  COMPACT_NAVIGATION_WINDOW_WIDTH,
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MIN_WIDTH,
  sidebarMotionMs,
  WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH,
  useAppLayoutState
} from "./AppLayoutState";
import {
  LAYOUT_MOTION_CLASS,
  WINDOW_RESIZING_CLASS,
} from "./WindowResizeState";

interface Harness {
  compactNavigation: ReturnType<typeof useAppLayoutState>["compactNavigation"];
  effectiveSidebarWidth: ReturnType<typeof useAppLayoutState>["effectiveSidebarWidth"];
  sidebarWidth: ReturnType<typeof useAppLayoutState>["sidebarWidth"];
  sidebarCollapsed: ReturnType<typeof useAppLayoutState>["sidebarCollapsed"];
  workspaceRightPanelWidth: ReturnType<
    typeof useAppLayoutState
  >["workspaceRightPanelWidth"];
  clampedWorkspaceRightPanelWidth: ReturnType<
    typeof useAppLayoutState
  >["clampedWorkspaceRightPanelWidth"];
  handleRightPanelSeparatorKey: ReturnType<
    typeof useAppLayoutState
  >["handleRightPanelSeparatorKey"];
  rightPanelOpen: boolean;
  rightPanelAnimating: ReturnType<typeof useAppLayoutState>["rightPanelAnimating"];
  toggleSidebar: ReturnType<typeof useAppLayoutState>["toggleSidebar"];
  startSidebarResize: ReturnType<typeof useAppLayoutState>["startSidebarResize"];
  startRightPanelResize: ReturnType<typeof useAppLayoutState>["startRightPanelResize"];
  setRightPanelOpenWithMotion: ReturnType<
    typeof useAppLayoutState
  >["setRightPanelOpenWithMotion"];
  animateRightPanelLayout: ReturnType<
    typeof useAppLayoutState
  >["animateRightPanelLayout"];
  workspaceRightPanelAutoGlobalized?: boolean;
  workspaceRightPanelDockableWithoutSidebar?: boolean;
}

let container: HTMLDivElement;
let root: Root | null = null;
let latest: Harness | null = null;
const originalInnerWidth = window.innerWidth;
const narrowWindowWidth = 820;
const roomyWindowWidth = 1020;

function makePointerDownEvent(clientX: number): React.PointerEvent<HTMLDivElement> {
  // The hook only reads `button`, `clientX`, and `preventDefault`, so a plain
  // object shaped like a PointerEvent is enough for the reducer path.
  return {
    button: 0,
    clientX,
    preventDefault: vi.fn()
  } as unknown as React.PointerEvent<HTMLDivElement>;
}

function renderHookHarness(): void {
  function Harness(): null {
    const hook = useAppLayoutState({
      onCloseWorkspaceMenu: () => {}
    });
    const responsiveHook = hook as typeof hook & {
      workspaceRightPanelAutoGlobalized?: boolean;
      workspaceRightPanelDockableWithoutSidebar?: boolean;
    };
    latest = {
      compactNavigation: hook.compactNavigation,
      effectiveSidebarWidth: hook.effectiveSidebarWidth,
      sidebarWidth: hook.sidebarWidth,
      sidebarCollapsed: hook.sidebarCollapsed,
      workspaceRightPanelWidth: hook.workspaceRightPanelWidth,
      clampedWorkspaceRightPanelWidth: hook.clampedWorkspaceRightPanelWidth,
      handleRightPanelSeparatorKey: hook.handleRightPanelSeparatorKey,
      rightPanelOpen: hook.rightPanelOpen,
      rightPanelAnimating: hook.rightPanelAnimating,
      toggleSidebar: hook.toggleSidebar,
      startSidebarResize: hook.startSidebarResize,
      startRightPanelResize: hook.startRightPanelResize,
      setRightPanelOpenWithMotion: hook.setRightPanelOpenWithMotion,
      animateRightPanelLayout: hook.animateRightPanelLayout,
      workspaceRightPanelAutoGlobalized:
        responsiveHook.workspaceRightPanelAutoGlobalized,
      workspaceRightPanelDockableWithoutSidebar:
        responsiveHook.workspaceRightPanelDockableWithoutSidebar,
    };
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(<Harness />);
  });
}

function setInnerWidth(value: number): void {
  window.innerWidth = value;
}

it("can use an explicit embedded viewport without reacting to the outer window", () => {
  setInnerWidth(900);
  window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
  let view!: ReturnType<typeof useAppLayoutState>;
  function ExtensionHarness({ width }: { width?: number }) {
    view = useAppLayoutState({ viewportWidth: width, onCloseWorkspaceMenu: () => {} });
    return null;
  }
  root = createRoot(container);
  act(() => root!.render(<ExtensionHarness />));
  const before = { sidebarWidth: view.sidebarWidth, collapsed: view.sidebarCollapsed, compact: view.compactNavigation };
  act(() => root!.render(<ExtensionHarness width={900} />));
  act(() => { setInnerWidth(1380); window.dispatchEvent(new Event("resize")); });
  expect({ sidebarWidth: view.sidebarWidth, collapsed: view.sidebarCollapsed, compact: view.compactNavigation }).toEqual(before);
  act(() => { setInnerWidth(900); root!.render(<ExtensionHarness />); });
  act(() => window.dispatchEvent(new Event("resize")));
  expect({ sidebarWidth: view.sidebarWidth, collapsed: view.sidebarCollapsed, compact: view.compactNavigation }).toEqual(before);
});

beforeEach(() => {
  setInnerWidth(1280);
  container = document.createElement("div");
  document.body.appendChild(container);
  // The hook reads sidebar collapse / width from localStorage on mount.
  window.localStorage.clear();
  // Wipe any leftover class from a previous test (defensive: the production
  // code path only adds it during a drag, but other tests in the same file
  // could leave it behind).
  document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
  document.documentElement.classList.remove(LAYOUT_MOTION_CLASS);
  latest = null;
});

afterEach(() => {
  vi.restoreAllMocks();
  setInnerWidth(originalInnerWidth);
  document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
  document.documentElement.classList.remove(LAYOUT_MOTION_CLASS);
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  vi.useRealTimers();
});

it("keeps the full content width when opening navigation on a phone", () => {
  vi.spyOn(ComposerFocus, "isTouchWebShell").mockReturnValue(true);
  vi.spyOn(window.screen, "width", "get").mockReturnValue(430);
  vi.spyOn(window.screen, "height", "get").mockReturnValue(932);
  setInnerWidth(430);
  renderHookHarness();
  expect(latest!.sidebarCollapsed).toBe(false);
  expect(latest!.effectiveSidebarWidth).toBe(0);
  act(() => latest!.toggleSidebar());
  expect(latest!.effectiveSidebarWidth).toBe(0);
  act(() => latest!.toggleSidebar());
  expect(latest!.sidebarCollapsed).toBe(false);
  expect(latest!.effectiveSidebarWidth).toBe(0);
});

it.each([
  { touch: true, shortSide: 430, focused: true },
  { touch: true, shortSide: 820, focused: false },
  { touch: false, shortSide: 430, focused: false },
])("keeps phone tools in one surface after rotation ($touch, $shortSide)", ({ touch, shortSide, focused }) => {
  vi.spyOn(ComposerFocus, "isTouchWebShell").mockReturnValue(touch);
  vi.spyOn(window.screen, "width", "get").mockReturnValue(932);
  vi.spyOn(window.screen, "height", "get").mockReturnValue(shortSide);
  setInnerWidth(932);
  renderHookHarness();
  expect(latest?.workspaceRightPanelAutoGlobalized).toBe(focused);
  expect(latest?.workspaceRightPanelDockableWithoutSidebar).toBe(!focused);
});

describe("independent panel preferences", () => {
  it("restores right-panel visibility without changing left navigation", () => {
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelOpen", "true");
    window.localStorage.setItem("wuu.desktop.sidebarCollapsed", "true");
    renderHookHarness();
    expect(latest!.rightPanelOpen).toBe(true);
    expect(latest!.sidebarCollapsed).toBe(true);
    act(() => latest!.setRightPanelOpenWithMotion(false));
    expect(window.localStorage.getItem("wuu.desktop.workspaceRightPanelOpen")).toBe("false");
    expect(latest!.sidebarCollapsed).toBe(true);
    act(() => latest!.toggleSidebar());
    expect(latest!.rightPanelOpen).toBe(false);
  });

  it("keeps the preferred right width when a clamped separator is clicked without dragging", () => {
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelWidth", "800");
    renderHookHarness();
    act(() => latest!.setRightPanelOpenWithMotion(true));
    expect(latest!.clampedWorkspaceRightPanelWidth).toBeLessThan(800);
    act(() => latest!.startRightPanelResize(makePointerDownEvent(800)));
    act(() => window.dispatchEvent(new Event("pointerup")));
    expect(latest!.workspaceRightPanelWidth).toBe(800);
    expect(window.localStorage.getItem("wuu.desktop.workspaceRightPanelWidth")).toBe("800");
  });

  it("clamps the right panel against an embedded viewport rather than the outer window", () => {
    setInnerWidth(1800);
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelWidth", "800");
    let layout!: ReturnType<typeof useAppLayoutState>;
    function Embedded() {
      layout = useAppLayoutState({ viewportWidth: 1100, onCloseWorkspaceMenu: () => {} });
      return null;
    }
    root = createRoot(container);
    act(() => root!.render(<Embedded />));
    expect(layout.clampedWorkspaceRightPanelWidth).toBe(1100 - SIDEBAR_DEFAULT_WIDTH - 352);
    expect(layout.workspaceRightPanelWidth).toBe(800);
  });
});

it("lets a direct resize interrupt a panel toggle without animated drag lag", () => {
  renderHookHarness();
  act(() => latest!.setRightPanelOpenWithMotion(true));
  expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(true);
  act(() => latest!.startRightPanelResize(makePointerDownEvent(800)));
  expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(false);
  expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(true);
  act(() => window.dispatchEvent(new Event("pointerup")));
  act(() => latest!.toggleSidebar());
  expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(true);
  act(() => latest!.startSidebarResize(makePointerDownEvent(0)));
  expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(false);
});

describe("useAppLayoutState window-resizing class", () => {
  it("adds window-resizing to <html> while a sidebar drag is active", () => {
    renderHookHarness();
    expect(latest).not.toBeNull();

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(100));
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(true);

    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
  });

  it("adds window-resizing to <html> while a right-panel drag is active", () => {
    renderHookHarness();
    expect(latest).not.toBeNull();

    // The right-panel drag is a no-op while the panel is closed, so open it
    // first via the hook's own setter.
    act(() => {
      latest!.setRightPanelOpenWithMotion(true);
    });

    act(() => {
      latest!.startRightPanelResize(makePointerDownEvent(400));
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(true);

    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
  });

  it("clears right-panel animation after the motion window", () => {
    vi.useFakeTimers();
    renderHookHarness();
    expect(latest).not.toBeNull();
    expect(latest!.rightPanelAnimating).toBe(false);

    act(() => {
      latest!.setRightPanelOpenWithMotion(true);
    });
    expect(latest!.rightPanelAnimating).toBe(true);

    act(() => {
      vi.advanceTimersByTime(rightPanelMotionMs());
    });
    expect(latest!.rightPanelAnimating).toBe(false);
  });

  it("animates right-panel layout mode changes without reopening the panel", () => {
    vi.useFakeTimers();
    renderHookHarness();

    act(() => {
      latest!.animateRightPanelLayout();
    });
    expect(latest!.rightPanelAnimating).toBe(true);

    act(() => {
      vi.advanceTimersByTime(rightPanelMotionMs());
    });
    expect(latest!.rightPanelAnimating).toBe(false);
  });

  it("marks structural panel motion as transient layout work", () => {
    vi.useFakeTimers();
    renderHookHarness();

    act(() => {
      latest!.toggleSidebar();
    });
    expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(true);

    act(() => {
      vi.advanceTimersByTime(sidebarMotionMs());
    });
    expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(false);
  });

  it("does not add the class for non-primary-button pointerdowns on the sidebar", () => {
    renderHookHarness();
    expect(latest).not.toBeNull();

    act(() => {
      latest!.startSidebarResize({
        button: 2,
        clientX: 100,
        preventDefault: vi.fn()
      } as unknown as React.PointerEvent<HTMLDivElement>);
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
  });
});

describe("useAppLayoutState responsive workspace presentation", () => {
  it("enters compact focus and returns to docking after the window has room", () => {
    renderHookHarness();
    expect(latest!.workspaceRightPanelAutoGlobalized).toBe(false);
    expect(latest!.workspaceRightPanelDockableWithoutSidebar).toBe(true);

    act(() => {
      setInnerWidth(674);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.sidebarCollapsed).toBe(false);
    expect(latest!.effectiveSidebarWidth).toBe(0);
    expect(latest!.workspaceRightPanelAutoGlobalized).toBe(true);
    expect(latest!.workspaceRightPanelDockableWithoutSidebar).toBe(false);

    act(() => {
      setInnerWidth(1000);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.sidebarCollapsed).toBe(false);
    expect(latest!.workspaceRightPanelAutoGlobalized).toBe(false);
    expect(latest!.workspaceRightPanelDockableWithoutSidebar).toBe(true);
  });

  it("keeps the panel docked when an open sidebar is the only space pressure", () => {
    // A medium window (>= 760) where conversation + panel fit without the
    // sidebar, but sidebar + conversation + panel do not. With the sidebar
    // open, opening the panel must NOT auto-globalize it — all three dock and
    // the conversation column absorbs the squeeze (the narrow-band fix). The
    // sidebar starts open at the 1280px default (see beforeEach); resizing to
    // 1000px (>= 900) does not auto-collapse it, so it stays docked. The old
    // behavior (passing effectiveSidebarWidth) would have globalized here,
    // since 1000 - ~254 sidebar < 760.
    renderHookHarness();
    act(() => {
      setInnerWidth(1000);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.sidebarCollapsed).toBe(false);
    expect(latest!.workspaceRightPanelAutoGlobalized).toBe(false);
    expect(latest!.workspaceRightPanelDockableWithoutSidebar).toBe(true);
  });

  it("keeps the remembered right-panel width through a narrow window", () => {
    setInnerWidth(1600);
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelWidth", "700");
    renderHookHarness();
    expect(latest!.clampedWorkspaceRightPanelWidth).toBe(700);

    act(() => {
      setInnerWidth(1100);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.clampedWorkspaceRightPanelWidth).toBeLessThan(700);
    expect(window.localStorage.getItem("wuu.desktop.workspaceRightPanelWidth")).toBe("700");

    act(() => {
      setInnerWidth(1600);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.clampedWorkspaceRightPanelWidth).toBe(700);
  });

  it("steps the right panel from its displayed width in a narrow window", () => {
    setInnerWidth(1600);
    window.localStorage.setItem("wuu.desktop.workspaceRightPanelWidth", "700");
    renderHookHarness();
    act(() => {
      setInnerWidth(1100);
      window.dispatchEvent(new Event("resize"));
    });
    const displayed = latest!.clampedWorkspaceRightPanelWidth;

    act(() => {
      latest!.handleRightPanelSeparatorKey({
        key: "ArrowRight",
        preventDefault: vi.fn(),
      } as unknown as React.KeyboardEvent<HTMLDivElement>);
    });
    expect(latest!.clampedWorkspaceRightPanelWidth).toBeLessThan(displayed);
  });
});

describe("useAppLayoutState initial widths", () => {
  // localStorage.getItem returns null for a missing key, and Number(null) is
  // 0 — a naive Number() conversion clamps a fresh profile to the minimum
  // width, parking the sidebar exactly on the collapse threshold.
  it("falls back to the defaults when nothing is stored", () => {
    renderHookHarness();
    expect(latest!.sidebarWidth).toBe(SIDEBAR_DEFAULT_WIDTH);
    expect(latest!.workspaceRightPanelWidth).toBe(WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH);
  });

  it("falls back to the default when the stored width is not numeric", () => {
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "garbage");
    renderHookHarness();
    expect(latest!.sidebarWidth).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("keeps a stored in-range width", () => {
    setInnerWidth(1400);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "420");
    renderHookHarness();
    expect(latest!.sidebarWidth).toBe(420);
  });

  it("keeps the user-selected sidebar width when the window is resized", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    expect(latest!.sidebarWidth).toBe(500);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");

    act(() => {
      setInnerWidth(1400);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarWidth).toBe(500);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");
  });

  it("does not replace the remembered width when the resizer is clicked without moving", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(400));
    });
    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });

    expect(latest!.sidebarWidth).toBe(500);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");
  });

  it("persists the displayed drag width and keeps it when the window grows", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(500));
    });
    act(() => {
      window.dispatchEvent(
        Object.assign(new Event("pointermove"), {
          clientX: 300,
        })
      );
    });
    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });

    expect(latest!.sidebarWidth).toBe(300);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("300");

    act(() => {
      setInnerWidth(1400);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarWidth).toBe(300);
  });

  it("keeps an open sidebar open in a narrow window", () => {
    setInnerWidth(narrowWindowWidth);
    renderHookHarness();
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      setInnerWidth(COMPACT_NAVIGATION_WINDOW_WIDTH - 1);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.compactNavigation).toBe(true);
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      setInnerWidth(roomyWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });
    expect(latest!.compactNavigation).toBe(false);
    expect(latest!.sidebarCollapsed).toBe(false);
    expect(latest!.effectiveSidebarWidth).toBe(SIDEBAR_DEFAULT_WIDTH);
    expect(window.localStorage.getItem("wuu.desktop.sidebarCollapsed")).toBe("false");
  });

  it("keeps a manually collapsed sidebar closed when the window becomes roomy", () => {
    setInnerWidth(roomyWindowWidth);
    renderHookHarness();

    act(() => {
      latest!.toggleSidebar();
    });
    expect(latest!.sidebarCollapsed).toBe(true);

    act(() => {
      setInnerWidth(narrowWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });
    act(() => {
      setInnerWidth(roomyWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarCollapsed).toBe(true);
    expect(window.localStorage.getItem("wuu.desktop.sidebarCollapsed")).toBe("true");
  });

  it("holds at the minimum width before the collapse intent threshold", () => {
    const startingWidth = SIDEBAR_MIN_WIDTH + 20;
    window.localStorage.setItem("wuu.desktop.sidebarWidth", String(startingWidth));
    renderHookHarness();
    expect(latest!.sidebarWidth).toBe(startingWidth);
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(startingWidth));
    });
    act(() => {
      window.dispatchEvent(
        Object.assign(new Event("pointermove"), {
          clientX: SIDEBAR_MIN_WIDTH - 12,
        })
      );
    });
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });
    expect(latest!.sidebarCollapsed).toBe(false);
    expect(latest!.sidebarWidth).toBe(SIDEBAR_MIN_WIDTH);
  });

  it("collapses mid-drag once the pointer crosses the collapse intent threshold", () => {
    const startingWidth = SIDEBAR_MIN_WIDTH + 20;
    window.localStorage.setItem("wuu.desktop.sidebarWidth", String(startingWidth));
    renderHookHarness();
    expect(latest!.sidebarWidth).toBe(startingWidth);
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(startingWidth));
    });
    act(() => {
      window.dispatchEvent(
        Object.assign(new Event("pointermove"), {
          clientX: SIDEBAR_MIN_WIDTH - 40,
        })
      );
    });
    expect(latest!.sidebarCollapsed).toBe(true);

    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });
    expect(latest!.sidebarCollapsed).toBe(true);
    expect(latest!.sidebarWidth).toBe(startingWidth);
  });
});
