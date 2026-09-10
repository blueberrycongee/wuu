/**
 * Collapsed-sidebar hover drawer close-on-window-exit regression test.
 *
 * User report: with the sidebar collapsed, hovering the left edge opens the
 * drawer overlay — but moving the mouse straight out of the app window (or
 * switching focus to another app) left the drawer stranded open, because the
 * sidebar's pointerleave never fired. The drawer must close when the pointer
 * leaves the window or the app loses focus.
 */
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { translateCurrent } from "./i18n";
import type {
  InitializeResult,
  ServerEvent,
  Thread,
  WuuDesktopApi,
} from "../shared/protocol";

vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn(),
    dispose: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })),
  })),
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: vi.fn().mockImplementation(() => ({ fit: vi.fn() })),
}));

vi.mock("./WorkspaceMonacoEditor", () => ({
  WorkspaceMonacoEditor: () => (
    <div className="workspace-monaco-editor" data-testid="mock-monaco-editor" />
  ),
}));

import { App, SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS } from "./App";
import { SIDEBAR_MOTION_MS } from "./AppLayoutState";
import { WINDOW_RESIZING_CLASS } from "./WindowResizeState";

let container: HTMLDivElement;
let root: Root | null = null;
let serverEventHandlers: Array<(event: ServerEvent) => void> = [];
let elementFromPointTarget: Element | null = null;
const originalInnerWidth = window.innerWidth;

const workspace = "/tmp/wuu-drawer-test";

function threadFixture(
  id: string,
  title: string,
  updatedAt: string,
): Thread {
  return {
    id,
    preview: title,
    title,
    model_provider: "fake",
    model: "fake-model",
    cwd: workspace,
    workspace_kind: "scratch",
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: updatedAt,
    turns: [],
  };
}

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "fake",
    model: "fake-model",
    workspace_root: workspace,
    permissions: { mode: "standard" },
    providers: [
      { name: "fake", type: "openai-compatible", model: "fake-model", api_key_configured: true },
    ],
    advanced_settings: {
      max_steps: 64,
      max_context_tokens: 0,
      temperature: 0,
      disable_auto_compact: false,
    },
  };
}

function installWindowStubs(): void {
  class MockResizeObserver {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
  (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver =
    MockResizeObserver as typeof ResizeObserver;
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
  Object.defineProperty(document, "elementFromPoint", {
    configurable: true,
    value: vi.fn(() => elementFromPointTarget),
  });
}

function installWuuApi(threads: Thread[] = []): void {
  const projectState = (): {
    projects: never[];
    active_context: { kind: "no_project"; cwd: string };
  } => ({
    projects: [],
    active_context: { kind: "no_project", cwd: workspace },
  });
  const api = {
    listProjects: vi
      .fn()
      .mockImplementation(() => Promise.resolve(projectState())),
    selectNoProject: vi
      .fn()
      .mockImplementation(() => Promise.resolve(projectState())),
    initialize: vi.fn().mockResolvedValue(initialized()),
    listThreads: vi.fn().mockResolvedValue({ threads }),
    listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    resumeThread: vi.fn().mockImplementation((threadID?: string) => {
      const thread = threads.find((item) => item.id === threadID) ?? threads[0];
      return Promise.resolve({ thread });
    }),
    startThread: vi.fn().mockResolvedValue({
      thread: threadFixture("draft-thread", "New conversation", "2026-01-01T00:00:00Z"),
    }),
    deleteThread: vi.fn().mockResolvedValue(undefined),
    getActiveGoalSummary: vi.fn().mockResolvedValue(null),
    gitStatus: vi.fn().mockResolvedValue({
      is_repo: false,
      dirty_count: 0,
      files: [],
    }),
    onServerEvent: vi.fn((handler: (event: ServerEvent) => void) => {
      serverEventHandlers.push(handler);
      return () => {
        serverEventHandlers = serverEventHandlers.filter(
          (item) => item !== handler,
        );
      };
    }),
    onWindowResizeState: vi.fn(() => () => {}),
    onTerminalEvent: vi.fn(() => () => {}),
    respondToServerRequest: vi.fn().mockResolvedValue(undefined),
    rejectServerRequest: vi.fn().mockResolvedValue(undefined),
  } as unknown as WuuDesktopApi;
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: api,
  });
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function appShell(): HTMLElement | null {
  return container.querySelector<HTMLElement>(".app-shell");
}

function touch(target: Element, type: string, x: number, y: number, count = 1, time?: number): TouchEvent {
  const points = Array.from({ length: count }, (_, identifier) => ({ identifier, clientX: x, clientY: y }) as Touch);
  const event = new TouchEvent(type, {
    bubbles: true, cancelable: true,
    touches: type === "touchend" || type === "touchcancel" ? [] : points,
    changedTouches: points,
  });
  if (time !== undefined) Object.defineProperty(event, "timeStamp", { value: time });
  target.dispatchEvent(event);
  return event;
}

async function renderCollapsedApp(): Promise<void> {
  window.localStorage.setItem("wuu.desktop.sidebarCollapsed", "true");
  await act(async () => {
    root = createRoot(container);
    root.render(<App />);
  });
  await flushAsync();
  expect(appShell()?.classList.contains("sidebar-collapsed")).toBe(true);
}

async function clickSidebarSession(
  label: string,
  options: { pointerTarget?: Element | null } = {},
): Promise<HTMLButtonElement> {
  const sessionButton = container.querySelector<HTMLButtonElement>(
    `aside button[aria-label^="${label}"]`,
  );
  expect(sessionButton).not.toBeNull();
  if (!sessionButton) {
    throw new Error(`Missing sidebar session button: ${label}`);
  }
  elementFromPointTarget =
    "pointerTarget" in options ? (options.pointerTarget ?? null) : sessionButton;
  await act(async () => {
    sessionButton.dispatchEvent(
      new MouseEvent("mousedown", {
        bubbles: true,
        clientX: 32,
        clientY: 48,
      }),
    );
    sessionButton.dispatchEvent(
      new MouseEvent("click", {
        bubbles: true,
        clientX: 32,
        clientY: 48,
      }),
    );
    await Promise.resolve();
    await Promise.resolve();
  });
  return sessionButton;
}

async function movePointerOver(target: Element | null): Promise<void> {
  elementFromPointTarget = target;
  await act(async () => {
    window.dispatchEvent(
      new MouseEvent("pointermove", {
        bubbles: true,
        clientX: 320,
        clientY: 80,
      }),
    );
    await Promise.resolve();
  });
}

async function openDrawerViaHoverZone(): Promise<void> {
  const zone = container.querySelector<HTMLElement>(".sidebar-hover-zone");
  expect(zone).not.toBeNull();
  await act(async () => {
    // React synthesizes onPointerEnter from a delegated pointerover whose
    // relatedTarget lies outside the element.
    zone?.dispatchEvent(
      new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
    );
  });
  expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
  await act(async () => {
    vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
  });
  expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
}

async function openDrawerViaSidebarToggle(): Promise<void> {
  const toggle = container.querySelector<HTMLElement>(".sidebar-toggle-button");
  expect(toggle).not.toBeNull();
  elementFromPointTarget = toggle;
  await act(async () => {
    toggle?.dispatchEvent(
      new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
    );
  });
  expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
  await act(async () => {
    vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
  });
  expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
}

function sidebarContainsFocus(): boolean {
  const sidebar = container.querySelector<HTMLElement>(".sidebar");
  const active = document.activeElement;
  return Boolean(
    sidebar && active instanceof HTMLElement && sidebar.contains(active),
  );
}

describe("collapsed sidebar hover drawer", () => {
  beforeEach(() => {
    window.innerWidth = 1280;
    delete document.documentElement.dataset.hostKind;
    vi.useFakeTimers();
    installWindowStubs();
    serverEventHandlers = [];
    elementFromPointTarget = null;
    container = document.createElement("div");
    document.body.appendChild(container);
    window.localStorage.clear();
    installWuuApi();
  });

  afterEach(() => {
    window.innerWidth = originalInnerWidth;
    delete document.documentElement.dataset.hostKind;
    act(() => {
      root?.unmount();
    });
    root = null;
    container.remove();
    document.documentElement.classList.remove(WINDOW_RESIZING_CLASS);
    Reflect.deleteProperty(globalThis, "ResizeObserver");
    delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  });

  it("uses a 240ms edge hover intent delay", () => {
    expect(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS).toBe(240);
  });

  it.each([
    ["web", true, 390, true],
    ["web", true, 820, false],
    ["web", false, 390, false],
    ["desktop", true, 390, false],
  ] as const)("uses swipe-only phone navigation and restores the titlebar on wider layouts: %s touch=%s width=%s", async (host, coarse, width, inComposer) => {
    document.documentElement.dataset.hostKind = host;
    window.innerWidth = width;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: coarse && query === "(pointer: coarse)", media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();
    expect(Boolean(container.querySelector('[data-wuu-component="conversation-titlebar"]'))).toBe(!inComposer);
    expect(Boolean(container.querySelector(`aside button[aria-label="${translateCurrent("sidebar.switchProject")}"]`))).toBe(host === "web" && coarse && width < 700);
    expect(container.querySelector('.composer-bar .compact-conversation-actions')).toBeNull();
    await act(async () => {
      window.innerWidth = 820;
      window.dispatchEvent(new Event("resize"));
    });
    expect(container.querySelector('[data-wuu-component="conversation-titlebar"]')).not.toBeNull();
    expect(container.querySelector('.composer-bar .compact-conversation-actions')).toBeNull();
  });

  it("stops measuring turn positions when compact navigation hides the turn rail", async () => {
    document.documentElement.dataset.hostKind = "web";
    const thread = threadFixture("rail-thread", "Rail history", "2026-01-01T00:00:00Z");
    thread.turns = [1, 2].map((index) => ({
      id: `turn-${index}`, items_view: "full", status: "completed",
      items: [{ id: `user-${index}`, type: "user_message", status: "completed", text: `Question ${index}` }],
    }));
    installWuuApi([thread]);
    await renderCollapsedApp();
    await openDrawerViaHoverZone();
    await clickSidebarSession("Rail history");
    await act(async () => { vi.advanceTimersByTime(400); });

    const scroll = container.querySelector<HTMLElement>(".scroll-region")!;
    Object.defineProperties(scroll, {
      scrollHeight: { configurable: true, value: 2000 },
      clientHeight: { configurable: true, value: 600 },
    });
    const turn = container.querySelector<HTMLElement>('.turn[data-turn-id="turn-1"]')!;
    expect(turn).not.toBeNull();
    const measure = vi.spyOn(turn, "getBoundingClientRect")
      .mockReturnValue(new DOMRect(0, 0, 500, 300));
    const scrollHistory = async () => {
      measure.mockClear();
      await act(async () => {
        scroll.scrollTop = 200;
        scroll.dispatchEvent(new Event("scroll"));
        vi.advanceTimersByTime(100);
      });
    };

    await scrollHistory();
    expect(measure).toHaveBeenCalled();
    for (const width of [390, 1280]) {
      await act(async () => {
        window.innerWidth = width;
        window.dispatchEvent(new Event("resize"));
      });
      await act(async () => { vi.advanceTimersByTime(400); });
      await scrollHistory();
      if (width === 390) {
        expect(container.querySelector(".conversation-turn-rail")).toBeNull();
        expect(measure).not.toHaveBeenCalled();
      } else {
        expect(container.querySelector(".conversation-turn-rail")).not.toBeNull();
        expect(measure).toHaveBeenCalled();
      }
    }
  });

  it.each([
    ["phone web left edge", "web", true, 390, true, 16],
    ["phone web middle", "web", true, 390, true, 150],
    ["phone web right side", "web", true, 390, true, 280],
    ["desktop app with touch", "desktop", true, 390, false, 150],
    ["mouse web", "web", false, 390, false, 150],
    ["wide touch web", "web", true, 1280, false, 150],
  ] as const)("limits message swipe to phone web: %s", async (_name, host, coarse, width, opens, x) => {
    document.documentElement.dataset.hostKind = host;
    window.innerWidth = width;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: coarse && query === "(pointer: coarse)",
      media: query, onchange: null, addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();
    const shell = appShell()!;
    const flow = shell.querySelector(".scroll-region")!;
    const sidebar = shell.querySelector<HTMLElement>(".sidebar")!;
    Object.defineProperty(sidebar, "offsetWidth", { configurable: true, value: 300 });
    vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue(new DOMRect(-300, 0, 300, 820));
    await act(async () => {
      touch(flow, "touchstart", x, 100, 1, 100);
      const move = touch(flow, "touchmove", x + 79, 105, 1, 160);
      expect(move.defaultPrevented).toBe(opens);
      touch(flow, "touchend", x + 79, 105, 1, 170);
    });
    await act(async () => { vi.advanceTimersByTime(400); });
    expect(shell.dataset.wuuSidebarMode).toBe(opens ? "drawer" : "collapsed");
    if (opens) {
      const backdrop = container.querySelector<HTMLButtonElement>(".compact-session-switcher-backdrop");
      expect(backdrop).not.toBeNull();
      await act(async () => { backdrop!.click(); });
      await act(async () => { vi.advanceTimersByTime(300); });
      expect(shell.dataset.wuuSidebarMode).toBe("collapsed");
    }
  });

  it.each([
    ["slow tiny drag", 100, 25, 400, 450, false],
    ["slow left-side swipe", 16, 80, 400, 650, true],
    ["slow middle swipe", 150, 80, 400, 650, true],
    ["slow right-side swipe", 280, 70, 400, 650, true],
    ["light middle-right swipe", 250, 34, 400, 650, true],
    ["light right-side swipe", 300, 32, 400, 650, true],
    ["light near-edge swipe", 350, 26, 400, 650, true],
    ["slow near-edge swipe", 340, 38, 400, 650, true],
    ["near-edge accidental movement", 340, 20, 400, 650, false],
    ["slow drag past halfway", 100, 180, 400, 450, true],
    ["short flick", 100, 40, 150, 160, true],
    ["light flick", 280, 20, 150, 160, true],
    ["short flick followed by a hold", 100, 20, 150, 500, false],
    ["tap jitter", 280, 6, 110, 120, false],
    ["deliberate swipe followed by a hold", 100, 80, 150, 500, true],
    ["right-side swipe pulled back before release", 280, 15, 700, 950, false, 70],
  ] as [string, number, number, number, number, boolean, number?][])("settles a %s using distance and recent speed", async (_name, x, distance, moveTime, endTime, opens, peak = distance) => {
    document.documentElement.dataset.hostKind = "web";
    window.innerWidth = 390;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(pointer: coarse)", media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();
    const shell = appShell()!;
    const sidebar = shell.querySelector<HTMLElement>(".sidebar")!;
    Object.defineProperty(sidebar, "offsetWidth", { configurable: true, value: 300 });
    vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue(new DOMRect(-300, 0, 300, 820));
    const flow = shell.querySelector(".scroll-region")!;
    await act(async () => {
      touch(flow, "touchstart", x, 100, 1, 100);
      if (peak !== distance) touch(flow, "touchmove", x + peak, 100, 1, 200);
      touch(flow, "touchmove", x + distance, 100, 1, moveTime);
      touch(flow, "touchend", x + distance, 100, 1, endTime);
    });
    await act(async () => { vi.advanceTimersByTime(400); });
    expect(shell.dataset.wuuSidebarMode).toBe(opens ? "drawer" : "collapsed");
  });

  it.each([
    ["short closing drag", 80, 700, 750, true],
    ["closing past halfway", 180, 700, 750, false],
    ["closing flick", 80, 450, 460, false],
    ["closing flick then hold", 80, 450, 850, true],
  ] as const)("settles a %s from the open drawer", async (_name, distance, moveTime, endTime, staysOpen) => {
    document.documentElement.dataset.hostKind = "web";
    window.innerWidth = 390;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(pointer: coarse)", media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();
    const shell = appShell()!;
    const sidebar = shell.querySelector<HTMLElement>(".sidebar")!;
    Object.defineProperty(sidebar, "offsetWidth", { configurable: true, value: 300 });
    vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue(new DOMRect(-300, 0, 300, 820));
    const flow = shell.querySelector(".scroll-region")!;
    await act(async () => {
      touch(flow, "touchstart", 100, 100, 1, 100);
      touch(flow, "touchmove", 280, 100, 1, 200);
      touch(flow, "touchend", 280, 100, 1, 210);
    });
    await act(async () => { vi.advanceTimersByTime(400); });
    expect(shell.dataset.wuuSidebarMode).toBe("drawer");
    vi.mocked(sidebar.getBoundingClientRect).mockReturnValue(new DOMRect(0, 0, 300, 820));
    await act(async () => {
      touch(sidebar, "touchstart", 250, 100, 1, 400);
      touch(sidebar, "touchmove", 250 - distance, 100, 1, moveTime);
      touch(sidebar, "touchend", 250 - distance, 100, 1, endTime);
    });
    await act(async () => { vi.advanceTimersByTime(600); });
    expect(shell.dataset.wuuSidebarMode).toBe(staysOpen ? "drawer" : "collapsed");
  });

  it.each(["vertical", "left", "outside messages", "short", "cancel", "multitouch", "input", "scroller"])(
    "does not steal %s gestures", async (kind) => {
      document.documentElement.dataset.hostKind = "web";
      window.innerWidth = 390;
      vi.mocked(window.matchMedia).mockImplementation((query) => ({
        matches: query === "(pointer: coarse)", media: query, onchange: null,
        addEventListener: vi.fn(), removeEventListener: vi.fn(),
        addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
      }));
      await renderCollapsedApp();
      const shell = appShell()!;
      const sidebar = shell.querySelector<HTMLElement>(".sidebar")!;
      Object.defineProperty(sidebar, "offsetWidth", { configurable: true, value: 300 });
      vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue(new DOMRect(-300, 0, 300, 820));
      const target = document.createElement(kind === "input" ? "textarea" : "div");
      (kind === "outside messages" ? shell : shell.querySelector(".scroll-region")!).appendChild(target);
      if (kind === "scroller") {
        target.style.overflowX = "auto";
        Object.defineProperties(target, { scrollWidth: { value: 500 }, clientWidth: { value: 200 } });
      }
      await act(async () => {
        touch(target, "touchstart", 150, 100);
        if (kind === "vertical") touch(target, "touchmove", 152, 125);
        if (kind === "left") touch(target, "touchmove", 130, 100);
        if (kind === "cancel") touch(target, "touchcancel", 150, 100);
        if (kind === "multitouch") touch(target, "touchstart", 150, 100, 2);
        const move = touch(target, "touchmove", kind === "short" ? 156 : 240, 105);
        if (kind !== "short") expect(move.defaultPrevented).toBe(false);
        touch(target, "touchend", kind === "short" ? 156 : 240, 105);
      });
      await act(async () => { vi.advanceTimersByTime(400); });
      expect(shell.dataset.wuuSidebarMode).toBe("collapsed");
      target.remove();
    },
  );

  it("opens the collapsed sidebar when the titlebar toggle is hovered", async () => {
    await renderCollapsedApp();
    expect(appShell()?.dataset.wuuSidebarMode).toBe("collapsed");
    await openDrawerViaSidebarToggle();
    expect(appShell()?.dataset.wuuSidebarMode).toBe("drawer");
  });

  it("pins the collapsed sidebar open when its toggle is clicked after hover preview", async () => {
    await renderCollapsedApp();
    await openDrawerViaSidebarToggle();

    const toggle = container.querySelector<HTMLButtonElement>(
      ".sidebar-toggle-button",
    );
    await act(async () => {
      toggle?.click();
    });

    expect(appShell()?.classList.contains("sidebar-collapsed")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-docking")).toBe(true);
    expect(appShell()?.dataset.wuuSidebarMode).toBe("docked");

    await act(async () => {
      vi.advanceTimersByTime(SIDEBAR_MOTION_MS);
    });
    expect(appShell()?.classList.contains("sidebar-drawer-docking")).toBe(false);
  });

  it("closes the drawer when the pointer leaves the window", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    // Leaving the window entirely: mouseout with no relatedTarget.
    elementFromPointTarget = document.body;
    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("mouseout", { relatedTarget: null }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      true,
    );
  });

  it("does not open the drawer when the pointer only sweeps across the edge", async () => {
    await renderCollapsedApp();
    const zone = container.querySelector<HTMLElement>(".sidebar-hover-zone");
    expect(zone).not.toBeNull();

    await act(async () => {
      zone?.dispatchEvent(
        new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
      );
      zone?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          relatedTarget: document.body,
        }),
      );
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it("does not open when hover intent expires after the pointer has already left", async () => {
    await renderCollapsedApp();
    const zone = container.querySelector<HTMLElement>(".sidebar-hover-zone");
    expect(zone).not.toBeNull();

    await act(async () => {
      elementFromPointTarget = zone;
      zone?.dispatchEvent(
        new MouseEvent("pointerover", {
          bubbles: true,
          clientX: 4,
          clientY: 80,
          relatedTarget: null,
        }),
      );
    });
    await movePointerOver(document.body);
    await act(async () => {
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it("opens the drawer while the window is resizing", async () => {
    await renderCollapsedApp();
    const zone = container.querySelector<HTMLElement>(".sidebar-hover-zone");
    expect(zone).not.toBeNull();

    await act(async () => {
      document.documentElement.classList.add(WINDOW_RESIZING_CLASS);
      zone?.dispatchEvent(
        new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
      );
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it("keeps an open drawer visible when window resize starts", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it.each([
    ["web", false],
    ["desktop", true],
  ] as const)("handles %s viewport height changes without confusing keyboard and native resize", async (host, suppressMotion) => {
    document.documentElement.dataset.hostKind = host;
    window.innerWidth = 390;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(pointer: coarse)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);

    const originalHeight = window.innerHeight;
    try {
      for (const height of [420, originalHeight]) {
        await act(async () => {
          window.innerHeight = height;
          window.dispatchEvent(new Event("resize"));
        });
        expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(suppressMotion);
        await act(async () => { vi.advanceTimersByTime(200); });
        expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
      }
    } finally {
      window.innerHeight = originalHeight;
    }
  });

  it("still marks the touch web shell as window-resizing when the viewport width changes", async () => {
    document.documentElement.dataset.hostKind = "web";
    window.innerWidth = 390;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(pointer: coarse)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp();

    window.innerWidth = 640;
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });

    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(true);
    // Subsequent keyboard/chrome changes must not prolong a completed width resize.
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
      vi.advanceTimersByTime(200);
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
  });

  it("finishes closing when a pending edge hover is cancelled mid-close", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();
    const sidebar = container.querySelector<HTMLElement>(".sidebar");
    const zone = container.querySelector<HTMLElement>(".sidebar-hover-zone");
    expect(sidebar).not.toBeNull();
    expect(zone).not.toBeNull();

    await act(async () => {
      elementFromPointTarget = document.body;
      sidebar?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          relatedTarget: document.body,
        }),
      );
      vi.advanceTimersByTime(1);
    });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(true);

    await act(async () => {
      zone?.dispatchEvent(
        new MouseEvent("pointerover", { bubbles: true, relatedTarget: null }),
      );
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS - 1);
      zone?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          relatedTarget: document.body,
        }),
      );
      vi.advanceTimersByTime(SIDEBAR_MOTION_MS);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it("keeps the drawer open when mouseout stays inside the window", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    // Moving between elements inside the window: relatedTarget is set.
    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("mouseout", { relatedTarget: document.body }),
      );
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
  });

  it("closes the drawer when pointer movement shows it is no longer hovered", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    await movePointerOver(document.body);

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      true,
    );
  });

  it("does not reopen from a stale sidebar pointerenter while the pointer is outside", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();
    const sidebar = container.querySelector<HTMLElement>(".sidebar");
    expect(sidebar).not.toBeNull();

    await movePointerOver(document.body);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      true,
    );

    await act(async () => {
      sidebar?.dispatchEvent(
        new MouseEvent("pointerover", {
          bubbles: true,
          clientX: 320,
          clientY: 80,
          relatedTarget: document.body,
        }),
      );
      await Promise.resolve();
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      true,
    );
  });

  it("closes the drawer when the window loses focus", async () => {
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    await act(async () => {
      window.dispatchEvent(new Event("blur"));
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      true,
    );
  });

  it("keeps the drawer open after selecting a sidebar session while still hovering it", async () => {
    installWuuApi([
      threadFixture(
        "thread-active",
        "Already open session",
        "2026-01-02T00:00:00Z",
      ),
      threadFixture(
        "thread-target",
        "Session from hover drawer",
        "2026-01-01T00:00:00Z",
      ),
    ]);
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    await clickSidebarSession("Session from hover drawer");

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
  });

  it("returns to conversation when compact focus navigation selects a session", async () => {
    installWuuApi([
      threadFixture(
        "thread-active",
        "Already open session",
        "2026-01-02T00:00:00Z",
      ),
      threadFixture(
        "thread-target",
        "Session from focused workspace",
        "2026-01-01T00:00:00Z",
      ),
    ]);
    window.innerWidth = 674;
    await renderCollapsedApp();

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>('.compact-conversation-actions [aria-haspopup="menu"]')!
        .click();
    });
    await act(async () => {
      const openPanel = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')]
        .find(button => button.textContent === "打开右侧栏");
      expect(openPanel).toBeTruthy();
      openPanel!.click();
      await Promise.resolve();
    });
    expect(appShell()?.classList.contains("right-panel-globalized")).toBe(true);

    await act(async () => {
      container
        .querySelector<HTMLButtonElement>(
          '.globalized-sidebar-toggle[aria-label="展开左侧栏"]',
        )
        ?.click();
    });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);

    await clickSidebarSession("Session from focused workspace");

    expect(appShell()?.classList.contains("right-panel-open")).toBe(false);
    expect(appShell()?.classList.contains("right-panel-globalized")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(container.querySelector(".conversation-pane")?.hasAttribute("inert")).toBe(false);
  });

  it("clears mouse-click focus when a session switch drawer closes after pointer exit", async () => {
    installWuuApi([
      threadFixture(
        "thread-active",
        "Already open session",
        "2026-01-02T00:00:00Z",
      ),
      threadFixture(
        "thread-target",
        "Session from hover drawer",
        "2026-01-01T00:00:00Z",
      ),
    ]);
    await renderCollapsedApp();
    await openDrawerViaHoverZone();

    const selectedButton = await clickSidebarSession(
      "Session from hover drawer",
    );
    selectedButton.focus();
    expect(sidebarContainsFocus()).toBe(true);

    await movePointerOver(document.body);
    await act(async () => {
      vi.advanceTimersByTime(SIDEBAR_MOTION_MS);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
    expect(sidebarContainsFocus()).toBe(false);
  });
});
