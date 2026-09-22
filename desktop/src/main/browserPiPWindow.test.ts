import type { Rectangle } from "electron";
import { describe, expect, it, vi } from "vitest";
import type { ActivitySession } from "../shared/protocol";
import type { BrowserInteractionHint } from "./browserHostWindows";
import {
  BrowserPiPSurface,
  browserPiPOverlayHTML,
  pipContainRect,
  pipHostname,
  type BrowserPiPOverlayHandle,
  type BrowserPiPTabHost,
  type BrowserPiPWindowHandle,
} from "./browserPiPWindow";
import type { ObservationPiPEventSink } from "./cuaActivityWindows";

const settle = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

describe("browser PiP pure helpers", () => {
  it("extracts the host for the surface chrome", () => {
    expect(pipHostname("https://docs.example.com/path?q=1")).toBe("docs.example.com");
    expect(pipHostname("not a url")).toBe("not a url");
    expect(pipHostname("")).toBe("about:blank");
  });

  it("fits the viewport into the window with letterboxing and a zoom scale", () => {
    const rect = pipContainRect(1000, 500, 260, 170);
    expect(rect.scale).toBeCloseTo(0.26);
    expect(rect.width).toBeCloseTo(260);
    expect(rect.height).toBeCloseTo(130);
    expect(rect.x).toBeCloseTo(0);
    expect(rect.y).toBeCloseTo(20);
    expect(pipContainRect(0, 500, 260, 170).scale).toBe(0);
    const larger = pipContainRect(100, 80, 400, 400);
    expect(larger.scale).toBe(1);
    expect(larger.width).toBe(100);
    expect(larger.height).toBe(80);
  });
});

class FakePipWindow {
  bounds: Rectangle;
  destroyed = false;
  visible = false;
  readonly added: unknown[] = [];
  readonly removed: unknown[] = [];
  private readonly listeners = new Map<string, Array<(...args: unknown[]) => void>>();

  constructor(bounds: Rectangle) {
    this.bounds = bounds;
  }

  readonly contentView = {
    addChildView: (view: unknown): void => {
      this.added.push(view);
    },
    removeChildView: (view: unknown): void => {
      this.removed.push(view);
    },
  };

  setAlwaysOnTop(): void {}
  setVisibleOnAllWorkspaces(): void {}
  showInactive(): void {
    if (this.destroyed) throw new Error("Object has been destroyed");
    this.visible = true;
  }
  hide(): void {
    if (this.destroyed) throw new Error("Object has been destroyed");
    this.visible = false;
  }
  isDestroyed(): boolean {
    return this.destroyed;
  }
  isVisible(): boolean {
    return this.visible;
  }
  getBounds(): Rectangle {
    return this.bounds;
  }
  setBounds(bounds: Rectangle): void {
    this.bounds = bounds;
  }
  on(event: string, listener: (...args: unknown[]) => void): void {
    const list = this.listeners.get(event) ?? [];
    list.push(listener);
    this.listeners.set(event, list);
  }
  emit(event: string, ...args: unknown[]): void {
    for (const listener of this.listeners.get(event) ?? []) listener(...args);
  }
  destroy(): void {
    this.destroyed = true;
    this.emit("closed");
  }

  asHandle(): BrowserPiPWindowHandle {
    return this as unknown as BrowserPiPWindowHandle;
  }
}

class FakeOverlay {
  readonly executed: string[] = [];
  readonly boundsSet: Rectangle[] = [];
  loadedURL = "";
  destroyed = false;
  private navigateListener: ((event: unknown, url: string) => void) | undefined;

  readonly webContents = {
    loadURL: async (url: string) => {
      this.loadedURL = url;
    },
    executeJavaScript: async (code: string) => {
      this.executed.push(code);
      return undefined;
    },
    setWindowOpenHandler: () => undefined,
    on: (_event: string, listener: (event: unknown, url: string) => void) => {
      this.navigateListener = listener;
    },
    isDestroyed: () => this.destroyed,
  };

  setBounds(bounds: Rectangle): void {
    this.boundsSet.push(bounds);
  }

  navigate(url: string): void {
    this.navigateListener?.({ preventDefault: vi.fn() }, url);
  }

  asHandle(): BrowserPiPOverlayHandle {
    return this as unknown as BrowserPiPOverlayHandle;
  }
}

class FakeHost {
  bounds: Rectangle | undefined = { x: 0, y: 0, width: 1000, height: 500 };
  meta = { url: "https://example.com/page", title: "Example" };
  readonly mounts: Array<{ rect: Rectangle; zoom: number }> = [];
  readonly unmounts: Array<{ restore: Rectangle }> = [];
  readonly relayouts: Array<{ rect: Rectangle; zoom: number }> = [];
  private readonly listeners = {
    closed: new Set<(workdir: string, tabID: string) => void>(),
    interaction: new Set<(workdir: string, tabID: string, hint: BrowserInteractionHint) => void>(),
    navigate: new Set<(workdir: string, tabID: string, url: string) => void>(),
    reparented: new Set<(workdir: string, tabID: string, parent: unknown) => void>(),
  };

  tabBounds(): Rectangle | undefined {
    return this.bounds;
  }
  tabSurfaceMeta(): { url: string; title: string } | undefined {
    return this.meta;
  }
  mountTabOnWindow(_w: string, _t: string, _p: unknown, rect: Rectangle, zoom: number): Rectangle | undefined {
    if (!this.bounds) return undefined;
    this.mounts.push({ rect, zoom });
    return this.bounds;
  }
  unmountTabIfOwner(_w: string, _t: string, _o: unknown, restore: Rectangle): void {
    this.unmounts.push({ restore });
  }
  relayoutMountedTab(_w: string, _t: string, _o: unknown, rect: Rectangle, zoom: number): void {
    this.relayouts.push({ rect, zoom });
  }
  addTabClosedListener(listener: (workdir: string, tabID: string) => void): () => void {
    this.listeners.closed.add(listener);
    return () => this.listeners.closed.delete(listener);
  }
  addInteractionListener(
    listener: (workdir: string, tabID: string, hint: BrowserInteractionHint) => void,
  ): () => void {
    this.listeners.interaction.add(listener);
    return () => this.listeners.interaction.delete(listener);
  }
  addNavigateListener(listener: (workdir: string, tabID: string, url: string) => void): () => void {
    this.listeners.navigate.add(listener);
    return () => this.listeners.navigate.delete(listener);
  }
  addTabReparentedListener(
    listener: (workdir: string, tabID: string, parent: unknown) => void,
  ): () => void {
    this.listeners.reparented.add(listener);
    return () => this.listeners.reparented.delete(listener);
  }

  emitClosed(): void {
    for (const listener of this.listeners.closed) listener("/repo", "t1");
  }
  emitInteraction(hint: BrowserInteractionHint): void {
    for (const listener of this.listeners.interaction) listener("/repo", "t1", hint);
  }
  emitNavigate(url: string): void {
    for (const listener of this.listeners.navigate) listener("/repo", "t1", url);
  }
  emitReparented(parent: unknown): void {
    for (const listener of this.listeners.reparented) listener("/repo", "t1", parent);
  }

  asHost(): BrowserPiPTabHost {
    return this as unknown as BrowserPiPTabHost;
  }
}

function makeActivity(): ActivitySession {
  return {
    id: "a1",
    thread_id: "th1",
    kind: "browser",
    plugin_id: "browser",
    target: "t1",
    workdir: "/repo",
    state: "active",
    controller: "agent",
    created_at: "2026-07-17T00:00:00Z",
    updated_at: "2026-07-17T00:00:00Z",
  } as ActivitySession;
}

function makeSink(): ObservationPiPEventSink & {
  events: Array<{ event: string }>;
  gone: number;
  failures: string[];
} {
  const sink = {
    events: [] as Array<{ event: string }>,
    gone: 0,
    failures: [] as string[],
    onEvent(event: { event: string }): void {
      this.events.push(event);
    },
    onFailure(message: string): void {
      this.failures.push(message);
    },
    onGone(): void {
      this.gone += 1;
    },
  };
  return sink as unknown as ObservationPiPEventSink & {
    events: Array<{ event: string }>;
    gone: number;
    failures: string[];
  };
}

function makeSurface(opts?: { host?: FakeHost; bounds?: Rectangle }): {
  surface: BrowserPiPSurface;
  win: FakePipWindow;
  overlay: FakeOverlay;
  host: FakeHost;
  sink: ReturnType<typeof makeSink>;
} {
  const host = opts?.host ?? new FakeHost();
  const sink = makeSink();
  const bounds = opts?.bounds ?? { x: 100, y: 100, width: 260, height: 170 };
  const win = new FakePipWindow(bounds);
  const overlay = new FakeOverlay();
  const surface = new BrowserPiPSurface({
    activity: makeActivity(),
    bounds,
    sink,
    host: host.asHost(),
    workdir: "/repo",
    tabID: "t1",
    isPackaged: false,
    createWindow: () => win.asHandle(),
    createOverlay: () => overlay.asHandle(),
  });
  return { surface, win, overlay, host, sink };
}

describe("BrowserPiPSurface", () => {
  it("mounts the real tab zoom-fitted on show and restores it on hide", async () => {
    const { surface, win, overlay, host, sink } = makeSurface();
    surface.start();
    surface.setVisible(true);
    await settle();

    // 1000×500 page zoomed into the 260×170 card: scale 0.26, letterboxed.
    expect(host.mounts).toHaveLength(1);
    expect(host.mounts[0].zoom).toBeCloseTo(0.26);
    expect(host.mounts[0].rect.width).toBeCloseTo(260);
    expect(host.mounts[0].rect.height).toBeCloseTo(130);
    expect(host.mounts[0].rect.y).toBeCloseTo(20);
    expect(win.visible).toBe(true);
    expect(sink.events.some((e) => e.event === "ready")).toBe(true);
    expect(overlay.executed.some((code) => code.includes('"mounted":true'))).toBe(true);

    surface.setVisible(false);
    expect(host.unmounts).toEqual([{ restore: { x: 0, y: 0, width: 1000, height: 500 } }]);
    expect(win.visible).toBe(false);
    surface.stop();
  });

  it("reports a missing tab as gone instead of mounting", () => {
    const host = new FakeHost();
    host.bounds = undefined;
    const { surface, sink } = makeSurface({ host });
    surface.start();
    surface.setVisible(true);
    expect(host.mounts).toHaveLength(0);
    expect(sink.gone).toBe(1);
    surface.stop();
  });

  it("waits for a starting browser tab to become mountable", () => {
    const host = new FakeHost();
    host.bounds = undefined;
    const { surface, win, sink } = makeSurface({ host });
    surface.updateActivity({ ...makeActivity(), state: "starting" });
    surface.start();
    surface.setVisible(true);

    expect(sink.gone).toBe(0);
    expect(win.visible).toBe(true);
    expect(host.mounts).toHaveLength(0);

    host.bounds = { x: 0, y: 0, width: 1000, height: 500 };
    surface.updateActivity({ ...makeActivity(), state: "background_controlled" });
    surface.setVisible(true);

    expect(host.mounts).toHaveLength(1);
    expect(sink.gone).toBe(0);
    surface.stop();
  });

  it("does not reuse a window synchronously destroyed by a gone callback", () => {
    const host = new FakeHost();
    host.bounds = undefined;
    const { surface, win, sink } = makeSurface({ host });
    const countGone = sink.onGone.bind(sink);
    sink.onGone = () => {
      countGone();
      surface.stop();
    };
    surface.start();

    expect(() => surface.setVisible(true)).not.toThrow();
    expect(sink.gone).toBe(1);
    expect(win.destroyed).toBe(true);
  });

  it("shows the placeholder again when a takeover adopts the mounted tab", async () => {
    const { surface, overlay, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    await settle();
    overlay.executed.length = 0;

    host.emitReparented({ foreign: true });
    expect(overlay.executed.some((code) => code.includes('"mounted":false'))).toBe(true);

    // Hiding afterwards must not yank the adopted tab back.
    surface.setVisible(false);
    expect(host.unmounts).toHaveLength(0);
    surface.stop();
  });

  it("refits the overlay and the mounted tab when the window resizes", async () => {
    const { surface, win, overlay, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    await settle();

    win.bounds = { ...win.bounds, width: 520, height: 340 };
    const detached = win.removed.length;
    win.emit("resized");
    // The input view must remain attached throughout a pointer gesture.
    expect(win.removed).toHaveLength(detached);
    expect(overlay.boundsSet.at(-1)).toEqual({ x: 0, y: 0, width: 520, height: 340 });
    expect(host.relayouts).toHaveLength(1);
    expect(host.relayouts[0].zoom).toBeCloseTo(0.52);
    expect(host.relayouts[0].rect.width).toBeCloseTo(520);
    expect(host.relayouts[0].rect.height).toBeCloseTo(260);
    surface.stop();
  });

  it("maps interaction hints from page CSS pixels into window coordinates", async () => {
    const { surface, overlay, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    await settle();
    overlay.executed.length = 0;

    host.emitInteraction({ kind: "click", x: 500, y: 250 });
    const push = overlay.executed.find((code) => code.includes("wuuPipInteract"));
    expect(push).toBeDefined();
    // scale 0.26, offset (0, 20) → (130, 85).
    expect(push).toContain('"x":130');
    expect(push).toContain('"y":85');
    surface.stop();
  });

  it("reports close navigation as user_close and geometry on move", async () => {
    const { surface, win, overlay, sink } = makeSurface();
    surface.start();
    await settle();

    overlay.navigate("wuu-pip://close");
    expect(sink.events.some((e) => e.event === "user_close")).toBe(true);

    win.emit("moved");
    const geometry = sink.events.find((e) => e.event === "geometry");
    expect(geometry).toMatchObject({ x: 100, y: 100, width: 260, height: 170 });
    surface.stop();
  });

  it("parks the tab back before an external window close destroys the view", () => {
    const { surface, win, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    win.emit("close");
    expect(host.unmounts).toEqual([{ restore: { x: 0, y: 0, width: 1000, height: 500 } }]);
    surface.stop();
  });

  it("reports a closed tab as gone", () => {
    const { surface, host, sink } = makeSurface();
    surface.start();
    host.emitClosed();
    expect(sink.gone).toBe(1);
    surface.stop();
  });

  it("updates the chrome label when the tab navigates", async () => {
    const { surface, overlay, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    await settle();
    overlay.executed.length = 0;

    host.meta = { url: "https://next.test/path", title: "Next" };
    host.emitNavigate("https://next.test/path");
    expect(host.relayouts.at(-1)?.zoom).toBeCloseTo(0.26);
    expect(
      overlay.executed.some((code) => code.includes("wuuPipHost") && code.includes("next.test")),
    ).toBe(true);
    surface.stop();
  });

  it("never leaks third-party product names into the overlay page", () => {
    const html = browserPiPOverlayHTML("example.com");
    expect(html).not.toMatch(/chatgpt|openai|claude|anthropic/i);
    expect(html).not.toContain("-webkit-app-region:drag");
    expect(html).toContain('data-resize="se"');
  });

  it("grows the card from a corner and lays the page out at the new size", () => {
    const { surface, win, overlay, host } = makeSurface();
    surface.start();
    surface.setVisible(true);
    surface.setHostLayout({
      host: { x: 0, y: 0, width: 800, height: 600 },
      obstacles: [],
      visibleFrame: { x: -2000, y: -2000, width: 6000, height: 6000 },
    });
    host.relayouts.length = 0;
    overlay.navigate("wuu-pip://resize?phase=start&edge=nw&x=0&y=0");
    overlay.navigate("wuu-pip://resize?phase=end&edge=nw&x=-40&y=-30");
    expect(win.bounds).toMatchObject({ x: 416, y: 386, width: 360, height: 190 });
    expect(host.relayouts.at(-1)?.zoom).toBeCloseTo(0.36);
    expect(host.relayouts.at(-1)?.rect.width).toBeCloseTo(360);
    expect(host.relayouts.at(-1)?.rect.height).toBeCloseTo(180);
    expect(win.added.at(-1)).toBe(overlay);
    surface.setHostLayout({
      host: { x: 0, y: 0, width: 800, height: 600 },
      obstacles: [],
      visibleFrame: { x: -2000, y: -2000, width: 6000, height: 6000 },
    });
    expect(win.bounds).toMatchObject({ width: 360, height: 190 });
    surface.stop();
  });

  it("keeps the page aspect in a narrow column and recaptures the viewport after panel use", () => {
    const { surface, win, host } = makeSurface();
    surface.start();
    surface.setHostLayout({
      host: { x: 0, y: 0, width: 300, height: 600 },
      obstacles: [],
      visibleFrame: { x: 0, y: 0, width: 1200, height: 800 },
    });
    surface.setVisible(true);
    expect(win.bounds.width / win.bounds.height).toBeCloseTo(2);
    expect(host.mounts.at(-1)?.rect.width).toBe(win.bounds.width);
    expect(host.mounts.at(-1)?.rect.height).toBe(win.bounds.height);

    surface.setVisible(false);
    host.bounds = { x: 0, y: 0, width: 600, height: 800 };
    surface.setVisible(true);
    expect(win.bounds.width / win.bounds.height).toBeCloseTo(0.75);
    expect(host.mounts.at(-1)?.zoom).toBeCloseTo(win.bounds.height / 800);
    surface.stop();
  });

  it("snaps a drag to the nearest corner of the conversation column", () => {
    vi.useFakeTimers();
    try {
      const { surface, win, overlay } = makeSurface();
      surface.start();
      surface.setHostLayout({
        host: { x: 0, y: 0, width: 800, height: 600 },
        obstacles: [],
        visibleFrame: { x: -2000, y: -2000, width: 6000, height: 6000 },
      });
      expect(win.bounds).toMatchObject({ x: 456, y: 336, width: 320, height: 240 });

      overlay.navigate("wuu-pip://drag?phase=start&x=476&y=356&vx=0&vy=0");
      overlay.navigate("wuu-pip://drag?phase=end&x=44&y=44&vx=0&vy=0");
      vi.advanceTimersByTime(300);
      expect(win.bounds).toMatchObject({ x: 24, y: 24, width: 320, height: 240 });
      surface.stop();
    } finally {
      vi.useRealTimers();
    }
  });
});
