import { act, createElement, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS,
  useSidebarDrawerState,
  type SidebarDrawerStateController,
} from "./SidebarDrawerState";

let root: Root | undefined;
let container: HTMLDivElement;
let elementFromPointTarget: Element | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
  Object.defineProperty(document, "elementFromPoint", {
    configurable: true,
    value: vi.fn(() => elementFromPointTarget),
  });
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = undefined;
  container.remove();
  elementFromPointTarget = null;
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function renderSidebarDrawerState({
  closeOnWindowResize = false,
}: {
  closeOnWindowResize?: boolean;
} = {}): Promise<{
  get: () => SidebarDrawerStateController;
  setCollapsed: (collapsed: boolean) => Promise<void>;
  sidebar: HTMLElement;
  hoverZone: HTMLElement;
}> {
  let latest: SidebarDrawerStateController | undefined;
  let sidebar: HTMLElement | null = null;
  let hoverZone: HTMLElement | null = null;

  function Probe({ collapsed = true }: { collapsed?: boolean }) {
    const appShellRef = useRef<HTMLDivElement>(null);
    const drawer = useSidebarDrawerState({
      appShellRef,
      sidebarCollapsed: collapsed,
      resizingSidebar: false,
      motionMs: () => 120,
      closeOnWindowResize,
    });
    latest = drawer;
    return (
      <div ref={appShellRef}>
        <aside
          ref={(node) => {
            sidebar = node;
          }}
          className="sidebar"
        />
        <div
          ref={(node) => {
            hoverZone = node;
            drawer.sidebarHoverZoneRef.current = node;
          }}
          className="sidebar-hover-zone"
        />
      </div>
    );
  }

  await act(async () => {
    root = createRoot(container);
    root.render(createElement(Probe));
    await Promise.resolve();
  });

  if (!latest || !sidebar || !hoverZone) {
    throw new Error("sidebar drawer state was not rendered");
  }

  return {
    setCollapsed: async (collapsed) => { await act(async () => root!.render(createElement(Probe, { collapsed }))); },
    get: () => {
      if (!latest) {
        throw new Error("sidebar drawer state was not rendered");
      }
      return latest;
    },
    sidebar,
    hoverZone,
  };
}

describe("useSidebarDrawerState", () => {
  it("finishes docking when the pointer enters the newly pinned rail", async () => {
    const hook = await renderSidebarDrawerState();
    await act(async () => hook.get().openSidebarDrawerNow());
    await hook.setCollapsed(false);
    expect(hook.get().sidebarDrawerPhase).toBe("docking");
    await act(async () => hook.get().openSidebarDrawer());
    await act(async () => vi.advanceTimersByTime(120));
    expect(hook.get().sidebarDrawerPhase).toBe("closed");
  });

  it("only closes the focused-workspace drawer after the pointer is outside it", async () => {
    const hook = await renderSidebarDrawerState();

    await act(async () => {
      hook.get().openSidebarDrawerNow();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");

    // The panel swap can synthesize a pointerleave while the cursor is still
    // over the revealed sidebar. Re-checking on the next task keeps it open.
    elementFromPointTarget = hook.sidebar;
    await act(async () => {
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", { clientX: 80, clientY: 80 }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("open");

    // A true leave (including the pointer leaving the window) closes it.
    elementFromPointTarget = document.body;
    await act(async () => {
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", { clientX: 320, clientY: 80 }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("closing");
  });

  it("keeps the drawer open when the titlebar toggle is covered by the rail but still under the pointer", async () => {
    const hook = await renderSidebarDrawerState();
    const toggle = document.createElement("button");
    toggle.className = "sidebar-toggle-button";
    hook.hoverZone.parentElement?.appendChild(toggle);
    vi.spyOn(toggle, "getBoundingClientRect").mockReturnValue(
      new DOMRect(90, 8, 30, 30),
    );
    vi.spyOn(hook.sidebar, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 296, 820),
    );
    vi.spyOn(hook.hoverZone, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 14, 820),
    );

    await act(async () => {
      hook.get().openSidebarDrawerNow();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");

    elementFromPointTarget = hook.sidebar;
    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("pointermove", {
          bubbles: true,
          clientX: 104,
          clientY: 24,
        }),
      );
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", {
          clientX: 104,
          clientY: 24,
          relatedTarget: hook.sidebar,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("open");
  });

  it("uses the leave event's relatedTarget to decide close before re-checking coordinates", async () => {
    const hook = await renderSidebarDrawerState();

    await act(async () => {
      hook.get().openSidebarDrawerNow();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");

    // relatedTarget points at the hover zone: even if clientX/Y look outside,
    // the pointer is really moving onto a hover trigger, so keep the drawer.
    elementFromPointTarget = document.body;
    await act(async () => {
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", {
          clientX: 320,
          clientY: 80,
          relatedTarget: hook.hoverZone,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("open");

    // relatedTarget points at the sidebar itself: keep the drawer open.
    await act(async () => {
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", {
          clientX: 320,
          clientY: 80,
          relatedTarget: hook.sidebar,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("open");

    // relatedTarget points at a non-hover element: close immediately, even if
    // the coordinate re-check would have also returned false.
    await act(async () => {
      hook.get().scheduleSidebarDrawerCloseFromPointerLeave(
        new MouseEvent("pointerout", {
          clientX: 320,
          clientY: 80,
          relatedTarget: document.body,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("closing");
  });

  it("opens after the edge hover intent delay while the pointer remains hovered", async () => {
    const hook = await renderSidebarDrawerState();
    elementFromPointTarget = hook.hoverZone;

    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("pointermove", {
          bubbles: true,
          clientX: 8,
          clientY: 24,
        }),
      );
      hook.get().scheduleSidebarDrawerOpen();
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });

    expect(hook.get().sidebarDrawerPhase).toBe("open");
  });

  it("finishes the close animation after the configured motion duration", async () => {
    const hook = await renderSidebarDrawerState();
    elementFromPointTarget = hook.sidebar;

    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("pointermove", {
          bubbles: true,
          clientX: 8,
          clientY: 24,
        }),
      );
      hook.get().openSidebarDrawer();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");

    await act(async () => {
      hook.get().closeSidebarDrawer();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("closing");

    await act(async () => {
      vi.advanceTimersByTime(120);
    });
    expect(hook.get().sidebarDrawerPhase).toBe("closed");
  });

  it("closes a hover drawer when the native window is resized", async () => {
    const hook = await renderSidebarDrawerState({ closeOnWindowResize: true });

    await act(async () => {
      hook.get().openSidebarDrawerNow();
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");

    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });

    expect(hook.get().sidebarDrawerPhase).toBe("closed");
  });
});

it("keeps a touch drawer open through compatibility mouse leave events", async () => {
  document.documentElement.dataset.hostKind = "web";
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true })));
  try {
    const hook = await renderSidebarDrawerState();
    await act(async () => { hook.get().openSidebarDrawerNow(); });
    await act(async () => {
      window.dispatchEvent(new MouseEvent("mouseout", { relatedTarget: null }));
      window.dispatchEvent(new MouseEvent("mousemove", { clientX: 999, clientY: 999 }));
      vi.advanceTimersByTime(1000);
    });
    expect(hook.get().sidebarDrawerPhase).toBe("open");
    await act(async () => { hook.get().closeSidebarDrawer(); });
    expect(hook.get().sidebarDrawerPhase).toBe("closing");
  } finally {
    delete document.documentElement.dataset.hostKind;
    vi.unstubAllGlobals();
  }
});
