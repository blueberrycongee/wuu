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
import { sidebarMotionMs } from "./AppLayoutState";
import {
  createWindowResizeSettleScheduler,
  WINDOW_RESIZE_SETTLE_DELAY_MS,
  WINDOW_RESIZING_CLASS,
} from "./WindowResizeState";
import { PhoneNavigationContext } from "./PhoneNavigationContext";
import { HOVER_REVEAL_OPEN_DELAY_MS } from "./HoverReveal";

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
  const workspaceState = (): {
    projects: never[];
    active_context: { kind: "no_project"; cwd: string };
  } => ({
    projects: [],
    active_context: { kind: "no_project", cwd: workspace },
  });
  const api = {
    listProjects: vi
      .fn()
      .mockImplementation(() => Promise.resolve(workspaceState())),
    selectNoProject: vi
      .fn()
      .mockImplementation(() => Promise.resolve(workspaceState())),
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

async function renderCollapsedApp(withPhoneNavigation = false): Promise<void> {
  window.localStorage.setItem("wuu.desktop.sidebarCollapsed", "true");
  await act(async () => {
    root = createRoot(container);
    root.render(withPhoneNavigation
      ? <PhoneNavigationContext.Provider value={{ openDevices: () => {} }}><App /></PhoneNavigationContext.Provider>
      : <App />);
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

async function moveBetweenSidebarTargets(from: Element, to: Element): Promise<void> {
  elementFromPointTarget = to;
  await act(async () => {
    from.dispatchEvent(new MouseEvent("pointerout", {
      bubbles: true, relatedTarget: to, clientX: 320, clientY: 80,
    }));
    to.dispatchEvent(new MouseEvent("pointerover", {
      bubbles: true, relatedTarget: from, clientX: 320, clientY: 80,
    }));
    vi.advanceTimersByTime(0);
  });
}

async function hoverDrawerSession(reveal = true): Promise<HTMLElement> {
  installWuuApi([threadFixture("hover-thread", "Hover session", "2026-01-01T00:00:00Z")]);
  await renderCollapsedApp();
  await openDrawerViaHoverZone();
  const row = container.querySelector<HTMLElement>(".thread-row")!;
  expect(row).not.toBeNull();
  await moveBetweenSidebarTargets(document.body, row);
  if (reveal) {
    await act(async () => { vi.advanceTimersByTime(HOVER_REVEAL_OPEN_DELAY_MS); });
    expect(document.querySelector(".sidebar-hover-card")).not.toBeNull();
  }
  return row;
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
    expect(Boolean(container.querySelector(`aside button[aria-label="${translateCurrent("sidebar.switchWorkspace")}"]`))).toBe(host === "web" && coarse && width < 700);
    expect(container.querySelector('.composer-bar .compact-conversation-actions')).toBeNull();
    await act(async () => {
      window.innerWidth = 820;
      window.dispatchEvent(new Event("resize"));
    });
    expect(container.querySelector('[data-wuu-component="conversation-titlebar"]')).not.toBeNull();
    expect(container.querySelector('.composer-bar .compact-conversation-actions')).toBeNull();
  });

  it.each(["web", "desktop"])("%s keeps the sidebar toggle appropriate while the drawer is open", async (host) => {
    document.documentElement.dataset.hostKind = host;
    window.innerWidth = 390;
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(pointer: coarse)", media: query, onchange: null,
      addEventListener: vi.fn(), removeEventListener: vi.fn(),
      addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
    }));
    await renderCollapsedApp(true);
    const toggle = () => container.querySelector<HTMLButtonElement>('[data-wuu-component="sidebar-toggle"]');
    expect(toggle()).not.toBeNull();
    await act(async () => { toggle()!.click(); });
    await act(async () => { vi.advanceTimersByTime(400); });
    expect(appShell()?.dataset.wuuSidebarMode).toBe("drawer");
    expect(toggle()).not.toBeNull();
    expect(toggle()!.getAttribute("aria-pressed")).toBe("true");
    await act(async () => {
      toggle()!.click();
    });
    await act(async () => { vi.advanceTimersByTime(400); });
    expect(appShell()?.dataset.wuuSidebarMode).toBe("collapsed");
    expect(toggle()).not.toBeNull();
  });

  it("opens the collapsed sidebar when the titlebar toggle is hovered", async () => {
    await renderCollapsedApp();
    expect(appShell()?.dataset.wuuSidebarMode).toBe("collapsed");
    await openDrawerViaSidebarToggle();
    expect(appShell()?.dataset.wuuSidebarMode).toBe("drawer");
  });

  it("keeps the drawer open when the titlebar toggle is covered by the sliding rail", async () => {
    window.innerWidth = 820;
    await renderCollapsedApp();
    const toggle = container.querySelector<HTMLElement>(".sidebar-toggle-button");
    const sidebar = container.querySelector<HTMLElement>(".sidebar");
    expect(toggle).not.toBeNull();
    expect(sidebar).not.toBeNull();
    vi.spyOn(toggle!, "getBoundingClientRect").mockReturnValue(
      new DOMRect(96, 9, 30, 30),
    );
    vi.spyOn(sidebar!, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 296, 820),
    );

    elementFromPointTarget = toggle;
    await act(async () => {
      toggle?.dispatchEvent(
        new MouseEvent("pointerover", {
          bubbles: true,
          clientX: 110,
          clientY: 24,
          relatedTarget: null,
        }),
      );
    });
    await act(async () => {
      vi.advanceTimersByTime(SIDEBAR_DRAWER_HOVER_OPEN_DELAY_MS);
    });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);

    elementFromPointTarget = sidebar;
    await act(async () => {
      window.dispatchEvent(
        new MouseEvent("pointermove", {
          bubbles: true,
          clientX: 110,
          clientY: 24,
        }),
      );
      toggle?.dispatchEvent(
        new MouseEvent("pointerout", {
          bubbles: true,
          clientX: 110,
          clientY: 24,
          relatedTarget: sidebar,
        }),
      );
      vi.advanceTimersByTime(1);
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(false);
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
      vi.advanceTimersByTime(sidebarMotionMs());
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

  it("applies deferred window-resize layout when the freeze lifts, not after an extra settle delay", async () => {
    await renderCollapsedApp();
    const callback = vi.fn();
    const scheduler = createWindowResizeSettleScheduler(callback);

    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(true);
    scheduler.schedule();
    expect(callback).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(140);
    });
    expect(document.documentElement.classList.contains(WINDOW_RESIZING_CLASS)).toBe(false);
    expect(callback).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(WINDOW_RESIZE_SETTLE_DELAY_MS + 1);
    });
    expect(callback).toHaveBeenCalledTimes(1);
    scheduler.cancel();
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
      vi.advanceTimersByTime(sidebarMotionMs());
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

  it("retains the drawer across the session card gap and retires both after leaving", async () => {
    const row = await hoverDrawerSession();
    const card = document.querySelector<HTMLElement>(".sidebar-hover-card")!;
    await moveBetweenSidebarTargets(row, document.body);
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    await moveBetweenSidebarTargets(document.body, card);
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(document.querySelector(".sidebar-hover-card")).toBe(card);

    await moveBetweenSidebarTargets(card, document.body);
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(document.querySelector(".sidebar-hover-card")).toBeNull();
    await act(async () => { vi.advanceTimersByTime(0); });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
  });

  it("keeps the drawer when returning from a card to another area of the sidebar", async () => {
    const row = await hoverDrawerSession();
    const card = document.querySelector<HTMLElement>(".sidebar-hover-card")!;
    await moveBetweenSidebarTargets(row, card);
    const sidebar = container.querySelector<HTMLElement>(".sidebar")!;
    await moveBetweenSidebarTargets(card, sidebar);
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(document.querySelector(".sidebar-hover-card")).toBeNull();
    await act(async () => { vi.advanceTimersByTime(0); });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
  });

  it("retains a focused card editor with its drawer until an outside press", async () => {
    const row = await hoverDrawerSession();
    const card = document.querySelector<HTMLElement>(".sidebar-hover-card")!;
    await moveBetweenSidebarTargets(row, card);
    await act(async () => {
      card.querySelector<HTMLButtonElement>(".sidebar-hover-card-title-editable")!.click();
    });
    const editor = card.querySelector("textarea");
    expect(editor).not.toBeNull();
    expect(document.activeElement).toBe(editor);
    await moveBetweenSidebarTargets(card, document.body);
    await act(async () => { vi.advanceTimersByTime(500); });
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(true);
    expect(document.activeElement).toBe(editor);
    await act(async () => {
      document.body.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true, clientX: 320, clientY: 80 }));
    });
    await act(async () => { vi.advanceTimersByTime(0); });
    expect(document.querySelector(".sidebar-hover-card")).toBeNull();
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
  });

  it.each([false, true])("dismisses pending or open cards when the drawer is explicitly closed (revealed=%s)", async (revealed) => {
    await hoverDrawerSession(revealed);
    await act(async () => { window.dispatchEvent(new Event("wuu:workbench-back")); });
    expect(document.querySelector(".sidebar-hover-card")).toBeNull();
    await act(async () => { vi.advanceTimersByTime(1000); });
    expect(document.querySelector(".sidebar-hover-card")).toBeNull();
    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
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
      vi.advanceTimersByTime(sidebarMotionMs());
    });

    expect(appShell()?.classList.contains("sidebar-drawer-open")).toBe(false);
    expect(appShell()?.classList.contains("sidebar-drawer-closing")).toBe(
      false,
    );
    expect(sidebarContainsFocus()).toBe(false);
  });
});
