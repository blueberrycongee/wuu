import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as ComposerFocus from "./ComposerFocus";
import {
  RIGHT_PANEL_MOTION_MS,
  SIDEBAR_AUTO_COLLAPSE_WINDOW_WIDTH,
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_MOTION_MS,
  WORKSPACE_RIGHT_PANEL_DEFAULT_WIDTH,
  clampSidebarWidthForWindow,
  useAppLayoutState
} from "./AppLayoutState";
import {
  LAYOUT_MOTION_CLASS,
  WINDOW_RESIZING_CLASS,
} from "./WindowResizeState";

interface Harness {
  effectiveSidebarWidth: ReturnType<typeof useAppLayoutState>["effectiveSidebarWidth"];
  sidebarWidth: ReturnType<typeof useAppLayoutState>["sidebarWidth"];
  sidebarCollapsed: ReturnType<typeof useAppLayoutState>["sidebarCollapsed"];
  workspaceRightPanelWidth: ReturnType<
    typeof useAppLayoutState
  >["workspaceRightPanelWidth"];
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
const narrowWindowWidth = SIDEBAR_AUTO_COLLAPSE_WINDOW_WIDTH - 1;
const roomyWindowWidth = SIDEBAR_AUTO_COLLAPSE_WINDOW_WIDTH + 120;

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
      onCloseProjectMenu: () => {}
    });
    const responsiveHook = hook as typeof hook & {
      workspaceRightPanelAutoGlobalized?: boolean;
      workspaceRightPanelDockableWithoutSidebar?: boolean;
    };
    latest = {
      effectiveSidebarWidth: hook.effectiveSidebarWidth,
      sidebarWidth: hook.sidebarWidth,
      sidebarCollapsed: hook.sidebarCollapsed,
      workspaceRightPanelWidth: hook.workspaceRightPanelWidth,
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
  expect(latest!.effectiveSidebarWidth).toBe(0);
  act(() => latest!.toggleSidebar());
  expect(latest!.sidebarCollapsed).toBe(false);
  expect(latest!.effectiveSidebarWidth).toBe(0);
  act(() => latest!.toggleSidebar());
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
      vi.advanceTimersByTime(RIGHT_PANEL_MOTION_MS);
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
      vi.advanceTimersByTime(RIGHT_PANEL_MOTION_MS);
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
      vi.advanceTimersByTime(SIDEBAR_MOTION_MS);
    });
    expect(document.documentElement.classList.contains(LAYOUT_MOTION_CLASS)).toBe(false);
  });

  it("paces sidebar layout motion as a drawer transition", () => {
    expect(SIDEBAR_MOTION_MS).toBeGreaterThanOrEqual(200);
    expect(SIDEBAR_MOTION_MS).toBeLessThanOrEqual(280);
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
    expect(latest!.sidebarCollapsed).toBe(true);
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
});

describe("useAppLayoutState initial widths", () => {
  it("scales both ends of the sidebar resize range below the 1280px baseline", () => {
    expect(clampSidebarWidthForWindow(SIDEBAR_MIN_WIDTH, 1000)).toBe(SIDEBAR_MIN_WIDTH);
    expect(clampSidebarWidthForWindow(SIDEBAR_MAX_WIDTH, 1000)).toBe(406);
    expect(clampSidebarWidthForWindow(SIDEBAR_DEFAULT_WIDTH, 640)).toBe(SIDEBAR_MIN_WIDTH);
  });

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

  it("scales the remembered sidebar width with a narrow window without losing the baseline width", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    expect(latest!.sidebarWidth).toBe(390);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");

    act(() => {
      setInnerWidth(1400);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarWidth).toBe(500);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");
  });

  it("does not replace a wider remembered width when the scaled resizer is clicked without moving", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(400));
    });
    act(() => {
      window.dispatchEvent(new Event("pointerup", { bubbles: true }));
    });

    expect(latest!.sidebarWidth).toBe(390);
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("500");
  });

  it("stores a narrow-window drag in baseline coordinates", () => {
    setInnerWidth(1000);
    window.localStorage.setItem("wuu.desktop.sidebarWidth", "500");
    renderHookHarness();

    act(() => {
      latest!.startSidebarResize(makePointerDownEvent(390));
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
    expect(window.localStorage.getItem("wuu.desktop.sidebarWidth")).toBe("384");

    act(() => {
      setInnerWidth(1400);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarWidth).toBe(384);
  });

  it("starts with the sidebar collapsed when the window is too narrow", () => {
    setInnerWidth(narrowWindowWidth);

    renderHookHarness();

    expect(latest!.sidebarCollapsed).toBe(true);
    expect(latest!.sidebarWidth).toBe(SIDEBAR_MIN_WIDTH);

    act(() => {
      setInnerWidth(roomyWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarCollapsed).toBe(false);
  });

  it("auto-collapses an open sidebar when the window becomes too narrow", () => {
    setInnerWidth(roomyWindowWidth);
    renderHookHarness();
    expect(latest!.sidebarCollapsed).toBe(false);

    act(() => {
      setInnerWidth(narrowWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarCollapsed).toBe(true);
    expect(latest!.sidebarWidth).toBe(SIDEBAR_MIN_WIDTH);

    act(() => {
      setInnerWidth(roomyWindowWidth);
      window.dispatchEvent(new Event("resize"));
    });

    expect(latest!.sidebarCollapsed).toBe(false);
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
