import { EventEmitter } from "node:events";
import { WebContentsView } from "electron";
import type { BrowserWindow, IpcMainEvent, IpcMainInvokeEvent } from "electron";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ChromeOverlayRequest } from "../shared/chromeOverlay";
import { ChromeOverlayHost } from "./chromeOverlayHost";

vi.mock("electron", async () => {
  const { EventEmitter } = await import("node:events");
  class Contents extends EventEmitter {
    focused = false;
    destroyed = false;
    mainFrame = { url: "" };
    send = vi.fn();
    setWindowOpenHandler = vi.fn();
    setBackgroundThrottling = vi.fn();
    isDestroyed = () => this.destroyed;
    close = vi.fn(() => { this.destroyed = true; this.emit("destroyed"); });
    isFocused = () => this.focused;
    zoom = 1;
    getZoomFactor = () => this.zoom;
    setZoomFactor = (zoom: number) => { this.zoom = zoom; };
    focus = vi.fn(() => { this.focused = true; this.emit("focus"); });
    loadURL = async (url: string) => { this.mainFrame.url = url; };
  }
  class WebContentsView {
    webContents = new Contents();
    visible = true;
    children = [];
    bounds = { x: 0, y: 0, width: 0, height: 0 };
    getVisible = () => this.visible;
    setVisible = (visible: boolean) => { this.visible = visible; };
    getBounds = () => this.bounds;
    setBounds = (bounds: typeof this.bounds) => { this.bounds = bounds; };
    setBackgroundColor = vi.fn();
  }
  return {
    WebContentsView, BrowserWindow: class {}, Menu: {}, net: {}, screen: {},
    session: { fromPartition: () => ({
      setPermissionRequestHandler: vi.fn(), setPermissionCheckHandler: vi.fn(),
      setDevicePermissionHandler: vi.fn(), on: vi.fn(), protocol: { handle: vi.fn() },
    }) },
  };
});

type AccountView = {
  webContents: EventEmitter & { focused: boolean; mainFrame: { url: string }; send: ReturnType<typeof vi.fn>; focus: ReturnType<typeof vi.fn>; isDestroyed(): boolean };
  getVisible(): boolean;
};

function accountMenu() {
  const owner = Object.assign(new EventEmitter(), {
    isVisible: () => true, isMinimized: () => false, isDestroyed: () => false,
    isFocused: () => true, getContentSize: () => [1280, 850],
    webContents: Object.assign(new EventEmitter(), {
      isDestroyed: () => false, isFocused: () => false, getZoomFactor: (): number => 1, focus: vi.fn(),
    }),
    contentView: {
      children: [] as AccountView[],
      addChildView(view: AccountView) { this.children = this.children.filter(child => child !== view); this.children.push(view); },
      removeChildView(view: AccountView) { this.children = this.children.filter(child => child !== view); },
    },
  });
  const emit = vi.fn();
  const host = new ChromeOverlayHost(emit, "/renderer", "/preload");
  const request: ChromeOverlayRequest = {
    version: 1, token: "account-opening", generation: 1, focus: true,
    bounds: { x: 12, y: 400, width: 260, height: 300 },
    appearance: { theme: "light", locale: "en-US", fontFamily: "sans-serif", variables: {} },
    model: { kind: "account", panel: { label: "Account", rows: [{ action: "account", label: "Settings" }] } },
  };
  const window = owner as unknown as BrowserWindow;
  host.show(window, request);
  const view = owner.contentView.children[0];
  const event = { sender: view.webContents, senderFrame: view.webContents.mainFrame } as unknown as IpcMainEvent & IpcMainInvokeEvent;
  const paint = () => { host.prepared(event, request.token, request.generation); host.painted(event, request.token, request.generation, host.ready(event)?.bounds ?? { width: 0, height: 0 }); };
  return { host, owner, window, view, event, request, paint, emit };
}

afterEach(() => { vi.useRealTimers(); });

describe("native overlay opening recovery", () => {
  it.each(["did-fail-load", "render-process-gone"])("retires %s and ignores late failures from the retired renderer", failure => {
    const { host, owner, window, view, request, emit } = accountMenu();
    view.webContents.emit(failure, {}, -2, "failed", "wuu-chrome://ui/chrome-overlay.html?surface=account", true);
    expect(view.webContents.isDestroyed()).toBe(true);
    expect(emit).toHaveBeenCalledTimes(1);
    const reopened = { ...request, token: "user-reopened" };
    host.show(window, reopened);
    const fresh = owner.contentView.children[0];
    view.webContents.emit("render-process-gone", {});
    view.webContents.emit("did-fail-load", {}, -2, "late failure", "", true);
    const event = { sender: fresh.webContents, senderFrame: fresh.webContents.mainFrame } as unknown as IpcMainEvent & IpcMainInvokeEvent;
    expect(host.ready(event)).toMatchObject(reopened);
    host.prepared(event, reopened.token, reopened.generation);
    host.painted(event, reopened.token, reopened.generation, host.ready(event)?.bounds ?? { width: 0, height: 0 });
    expect(fresh.getVisible()).toBe(true);
    expect(emit).toHaveBeenCalledTimes(1);
  });

  it.each(["no ready", "no paint"])("retires an opening with %s, then accepts a fresh user opening", async phase => {
    vi.useFakeTimers();
    const { host, owner, window, view, event, request, emit } = accountMenu();
    if (phase === "no paint") host.ready(event);
    await vi.advanceTimersByTimeAsync(9_000);
    expect(view.getVisible()).toBe(false);
    expect(emit).not.toHaveBeenCalled();
    // Streaming state and geometry updates cannot keep a broken opening alive.
    const updated = { ...request, generation: 2 };
    host.show(window, updated);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(emit).toHaveBeenCalledWith(owner, { token: request.token, generation: 2, command: { type: "dismiss", reason: "failed" } });
    expect(view.webContents.isDestroyed()).toBe(true);
    expect(owner.contentView.children).not.toContain(view);

    const reopened = { ...request, token: "user-reopened" };
    host.show(window, reopened);
    const fresh = owner.contentView.children[0];
    expect(fresh).not.toBe(view);
    const freshEvent = { sender: fresh.webContents, senderFrame: fresh.webContents.mainFrame } as unknown as IpcMainEvent & IpcMainInvokeEvent;
    expect(host.ready(freshEvent)).toMatchObject(reopened);
    host.painted(event, request.token, 2, request.bounds);
    expect(fresh.getVisible()).toBe(false);
    host.prepared(freshEvent, reopened.token, reopened.generation);
    host.painted(freshEvent, reopened.token, reopened.generation, host.ready(freshEvent)?.bounds ?? { width: 0, height: 0 });
    expect(fresh.getVisible()).toBe(true);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(fresh.getVisible()).toBe(true);
    expect(emit).toHaveBeenCalledTimes(1);
  });

  it.each(["hide", "owner blur"])("does not turn an intentional %s into a late presentation failure", async reason => {
    vi.useFakeTimers();
    const { host, owner, window, view, request, emit } = accountMenu();
    if (reason === "hide") host.hide(window, request.token, request.generation);
    else owner.emit("blur");
    emit.mockClear();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(emit).not.toHaveBeenCalled();
    expect(view.webContents.isDestroyed()).toBe(false);
    expect(view.getVisible()).toBe(false);
  });
});

describe("native account menu focus lifecycle", () => {
  it.each(["loading", "ready", "reopening"])("keeps a %s hidden opening through initialization blur until paint", async (phase) => {
    const { host, window, view, event, request, paint, emit } = accountMenu();
    if (phase !== "loading") expect(host.ready(event)).toMatchObject(request);
    if (phase === "reopening") {
      paint();
      host.hide(window, request.token, request.generation);
      host.show(window, request);
      view.webContents.focused = false;
    }
    expect(view.getVisible()).toBe(false);
    view.webContents.emit("blur");
    await Promise.resolve();
    expect(emit).not.toHaveBeenCalled();
    expect(host.ready(event)).toMatchObject(request);
    paint();
    expect(view.getVisible()).toBe(true);
    expect(view.webContents.focus).toHaveBeenCalled();
    expect(view.webContents.focused).toBe(true);
  });

  it("dismisses a presented menu on outside focus without stealing focus back", async () => {
    const { host, owner, view, event, request, paint, emit } = accountMenu();
    host.ready(event);
    paint();
    view.webContents.focused = false;
    view.webContents.emit("blur");
    // Native-tree mutation remains deferred outside Chromium's focus callback.
    expect(view.getVisible()).toBe(true);
    await Promise.resolve();
    expect(view.getVisible()).toBe(false);
    expect(emit).toHaveBeenCalledWith(owner, { token: request.token, generation: request.generation, command: { type: "dismiss", reason: "outside" } });
    expect(owner.webContents.focus).not.toHaveBeenCalled();
  });

  it("keeps focus recovered before deferred dismissal", async () => {
    const { host, view, event, paint, emit } = accountMenu();
    host.ready(event);
    paint();
    view.webContents.focused = false;
    view.webContents.emit("blur");
    view.webContents.focus();
    await Promise.resolve();
    expect(view.getVisible()).toBe(true);
    expect(emit).not.toHaveBeenCalled();
  });

  it.each(["blur", "hide", "minimize"])("still cancels an unpainted opening when its owner receives %s", (reason) => {
    const { host, owner, view, event, request, paint, emit } = accountMenu();
    owner.emit(reason);
    expect(host.ready(event)).toBeNull();
    paint();
    expect(view.getVisible()).toBe(false);
    expect(emit).toHaveBeenCalledWith(owner, { token: request.token, generation: request.generation, command: { type: "dismiss", reason: "owner" } });
  });
});


describe("native overlay viewport handoff", () => {
  it("reports the clipped viewport in CSS pixels at native zoom", () => {
    const { host, owner, window, event, request } = accountMenu();
    owner.getContentSize = () => [300, 200]; owner.webContents.getZoomFactor = () => 1.5;
    host.show(window, { ...request, bounds: { x: 10, y: 20, width: 320, height: 91 } });
    expect(host.ready(event)?.bounds).toEqual({ x: 10, y: 20, width: 190, height: 137 / 1.5 });
  });

  it("requests a new paint when native zoom changes after ready", () => {
    const { host, owner, view, event, request, paint } = accountMenu();
    host.ready(event); host.prepared(event, request.token, 1);
    owner.webContents.getZoomFactor = () => 1.5;
    paint();
    expect(view.webContents.focus).not.toHaveBeenCalled();
    expect(view.webContents.send).toHaveBeenCalledWith("wuu:chrome-overlay-state", expect.objectContaining({ bounds: expect.objectContaining({ width: 260 }) }));
    paint();
    expect(view.getVisible()).toBe(true);
  });

  it("refreshes a capsule viewport after resize during opening and while visible", () => {
    const { host, owner, window, request } = accountMenu();
    const capsule: ChromeOverlayRequest = { ...request, token: "capsule", bounds: { x: 10, y: 10, width: 300, height: 80 }, model: {
      kind: "capsule", draft: "", expanded: false, menuOpen: false, busy: false,
      sourceTitle: "Page", label: "Ask", placeholder: "Ask", sendLabel: "Send", openLabel: "Open", collapseLabel: "Close",
    } };
    host.show(window, capsule);
    const view = owner.contentView.children[1];
    const event = { sender: view.webContents, senderFrame: view.webContents.mainFrame } as unknown as IpcMainEvent & IpcMainInvokeEvent;
    host.ready(event); host.prepared(event, capsule.token, 1);
    owner.getContentSize = () => [200, 200]; owner.emit("resize");
    host.painted(event, capsule.token, 1, { width: 300, height: 80 });
    expect(view.webContents.focus).not.toHaveBeenCalled();
    expect(view.webContents.send).toHaveBeenLastCalledWith("wuu:chrome-overlay-state", expect.objectContaining({ bounds: expect.objectContaining({ width: 190 }) }));
    host.painted(event, capsule.token, 1, host.ready(event)?.bounds ?? { width: 0, height: 0 });
    expect(view.getVisible()).toBe(true);
    owner.getContentSize = () => [180, 200]; owner.emit("resize");
    expect(view.getVisible()).toBe(true);
    expect(view.webContents.send).toHaveBeenLastCalledWith("wuu:chrome-overlay-state", expect.objectContaining({ bounds: expect.objectContaining({ width: 170 }) }));
  });
});


describe("native overlay preparation handshake", () => {
  it("makes only the current prepared model visible without admitting commands or taking owner focus", () => {
    const { host, owner, view, event, request, emit } = accountMenu();
    host.ready(event);
    host.prepared(event, "stale", 1);
    host.prepared(event, request.token, 0);
    expect(view.getVisible()).toBe(false);
    host.prepared(event, request.token, 1);
    expect(view.getVisible()).toBe(true);
    expect(view.webContents.focus).not.toHaveBeenCalled();
    view.webContents.focused = true; view.webContents.emit("focus");
    expect(owner.webContents.focus).toHaveBeenCalled();
    host.command(event, { token: request.token, generation: 1, command: { type: "account-action", action: "account" } });
    expect(emit).not.toHaveBeenCalled();
    host.painted(event, request.token, 1, host.ready(event)?.bounds ?? { width: 0, height: 0 });
    expect(view.webContents.focus).toHaveBeenCalled();
    host.command(event, { token: request.token, generation: 1, command: { type: "account-action", action: "account" } });
    expect(emit).toHaveBeenCalledTimes(1);
  });

  it.each(["hide", "escape", "owner blur"])("cancels preparation on %s and fences late acknowledgements", reason => {
    const { host, owner, window, view, event, request } = accountMenu();
    host.ready(event); host.prepared(event, request.token, 1);
    if (reason === "hide") host.hide(window, request.token, 1);
    else if (reason === "escape") owner.webContents.emit("before-input-event", { preventDefault: vi.fn() }, { type: "keyDown", key: "Escape" });
    else owner.emit("blur");
    expect(view.getVisible()).toBe(false);
    host.prepared(event, request.token, 1); host.painted(event, request.token, 1, host.ready(event)?.bounds ?? { width: 0, height: 0 });
    expect(view.getVisible()).toBe(false);
    expect(view.webContents.focus).not.toHaveBeenCalled();
  });

  it("does not restart the total opening budget when preparation becomes native-visible", async () => {
    vi.useFakeTimers();
    const { host, view, event, request, emit } = accountMenu();
    host.ready(event);
    await vi.advanceTimersByTimeAsync(9_000);
    host.prepared(event, request.token, 1);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(view.webContents.isDestroyed()).toBe(true);
    expect(emit).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ command: { type: "dismiss", reason: "failed" } }));
  });
});


it("cancels a preparing menu when an outside native page takes focus", () => {
  const { host, owner, window, view, event, request } = accountMenu();
  const page = new WebContentsView();
  owner.contentView.addChildView(page as unknown as AccountView);
  host.prepare(window);
  host.ready(event); host.prepared(event, request.token, 1);
  page.webContents.emit("focus");
  expect(view.getVisible()).toBe(false);
  host.painted(event, request.token, 1, request.bounds);
  expect(view.getVisible()).toBe(false);
});

it("does not inherit pending focus from a replaced opening", () => {
  const { host, window, view, event, request } = accountMenu();
  const replacement = { ...request, token: "replacement", focus: false };
  host.show(window, replacement);
  host.ready(event); host.prepared(event, replacement.token, 1);
  host.painted(event, replacement.token, 1, replacement.bounds);
  expect(view.webContents.focus).not.toHaveBeenCalled();
});

it("cancels on an outside owner pointer event before a delayed layout acknowledgement can focus", () => {
  const { host, owner, window, view, event, request } = accountMenu();
  host.prepare(window);
  host.ready(event); host.prepared(event, request.token, 1);
  owner.webContents.emit("before-mouse-event", {}, { type: "mouseDown" });
  host.painted(event, request.token, 1, request.bounds);
  expect(view.getVisible()).toBe(false);
  expect(view.webContents.focus).not.toHaveBeenCalled();
});

it("does not present an opening clipped to an empty viewport", () => {
  const { host, owner, window, view, event, request } = accountMenu();
  owner.getContentSize = () => [100, 100];
  const clipped = { ...request, bounds: { x: 100, y: 100, width: 100, height: 100 } };
  host.show(window, clipped); host.ready(event); host.prepared(event, request.token, 1);
  host.painted(event, request.token, 1, { width: 0, height: 0 });
  expect(view.webContents.focus).not.toHaveBeenCalled();
});
