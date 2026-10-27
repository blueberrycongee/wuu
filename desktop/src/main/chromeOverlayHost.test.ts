import { EventEmitter } from "node:events";
import type { BrowserWindow, IpcMainEvent, IpcMainInvokeEvent } from "electron";
import { describe, expect, it, vi } from "vitest";
import type { ChromeOverlayRequest } from "../shared/chromeOverlay";
import { ChromeOverlayHost } from "./chromeOverlayHost";

vi.mock("electron", async () => {
  const { EventEmitter } = await import("node:events");
  class Contents extends EventEmitter {
    focused = false;
    mainFrame = { url: "" };
    send = vi.fn();
    setWindowOpenHandler = vi.fn();
    setBackgroundThrottling = vi.fn();
    isDestroyed = () => false;
    isFocused = () => this.focused;
    getZoomFactor = () => 1;
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
  webContents: EventEmitter & { focused: boolean; mainFrame: { url: string }; focus: ReturnType<typeof vi.fn> };
  getVisible(): boolean;
};

function accountMenu() {
  const owner = Object.assign(new EventEmitter(), {
    isVisible: () => true, isMinimized: () => false, isDestroyed: () => false,
    isFocused: () => true, getContentSize: () => [1280, 850],
    webContents: Object.assign(new EventEmitter(), {
      isDestroyed: () => false, isFocused: () => false, getZoomFactor: () => 1, focus: vi.fn(),
    }),
    contentView: { children: [] as AccountView[], addChildView(view: AccountView) { this.children.push(view); } },
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
  const paint = () => host.painted(event, request.token, request.generation);
  return { host, owner, window, view, event, request, paint, emit };
}

describe("native account menu focus lifecycle", () => {
  it.each(["loading", "ready", "reopening"])("keeps a %s hidden opening through initialization blur until paint", async (phase) => {
    const { host, window, view, event, request, paint, emit } = accountMenu();
    if (phase !== "loading") expect(host.ready(event)).toEqual(request);
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
    expect(host.ready(event)).toEqual(request);
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
