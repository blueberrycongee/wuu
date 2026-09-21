import { BrowserWindow, WebContentsView, type Rectangle } from "electron";
import type { ActivitySession } from "../shared/protocol";
import { appShellWebPreferences } from "./appShellGuards";
import {
  browserPiPAnchors,
  browserPiPCardSize,
  browserPiPClampOrigin,
  browserPiPDragCommand,
  browserPiPNearestAnchor,
  browserPiPOrigin,
  browserPiPSnapEase,
  browserPiPSnapPoint,
  PIP_SNAP_MS,
  type BrowserPiPScreenLayout,
  type PipAlignment,
  type PipPoint,
} from "./browserPiPPlacement";
import type {
  BrowserHostCoordinator,
  BrowserInteractionHint,
  BrowserParentWindowHandle,
  BrowserViewHandle,
} from "./browserHostWindows";
import {
  type ObservationPiPEventSink,
  type ObservationPiPFactory,
  type ObservationPiPHandle,
} from "./cuaActivityWindows";
import { CUANativePiP, resolveCUAFrameHelper } from "./cuaFrameStreams";

// The protocol carries interaction inline on ActivitySession; reuse that shape.
type Interaction = NonNullable<ActivitySession["interaction"]>;

// The host surface the PiP needs from the BrowserHostCoordinator: mount and
// geometry for the real tab view, chrome metadata, and lifecycle listeners.
export type BrowserPiPTabHost = Pick<
  BrowserHostCoordinator,
  | "tabBounds"
  | "tabSurfaceMeta"
  | "mountTabOnWindow"
  | "unmountTabIfOwner"
  | "relayoutMountedTab"
  | "addInteractionListener"
  | "addTabClosedListener"
  | "addNavigateListener"
  | "addTabReparentedListener"
>;

// ---------------------------------------------------------------------------
// Pure helpers (unit-tested).
// ---------------------------------------------------------------------------

export function pipHostname(url: string): string {
  const match = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/([^/?#]+)/.exec(url);
  return match?.[1] ?? (url.trim() || "about:blank");
}

// Contain-fit geometry: shrink the tab's layout viewport into the PiP content
// box without changing it. scale doubles as the UI zoom factor — view DIP
// size = viewport × scale, so CSS layout stays at viewport size and every
// CDP coordinate the agent uses keeps its meaning.
export function pipContainRect(
  contentW: number,
  contentH: number,
  boxW: number,
  boxH: number,
): { x: number; y: number; width: number; height: number; scale: number } {
  if (contentW <= 0 || contentH <= 0 || boxW <= 0 || boxH <= 0) {
    return { x: 0, y: 0, width: 0, height: 0, scale: 0 };
  }
  const scale = Math.min(boxW / contentW, boxH / contentH);
  const width = contentW * scale;
  const height = contentH * scale;
  return { x: (boxW - width) / 2, y: (boxH - height) / 2, width, height, scale };
}

// A tab whose real bounds were never established (hidden host, zero-size
// view) still gets a deterministic viewport for contain math.
const PIP_FALLBACK_VIEWPORT = { width: 1280, height: 800 };

// ---------------------------------------------------------------------------
// The browser PiP surface: an Electron panel window presenting the REAL tab
// view, reparented off the hidden host and zoom-fitted into the window —
// the same surface the agent acts on, never a re-encoded frame stream. A
// transparent overlay view above it draws the chrome strip, the interaction
// effects, and the frosted placeholder, and swallows pointer input so the
// preview can never steal focus or become an input target.
// ---------------------------------------------------------------------------

export type BrowserPiPWindowHandle = BrowserParentWindowHandle & {
  setAlwaysOnTop(flag: boolean, level?: string): void;
  setVisibleOnAllWorkspaces(visible: boolean, options?: { visibleOnFullScreen?: boolean }): void;
  showInactive(): void;
  hide(): void;
  isVisible(): boolean;
  getBounds(): Rectangle;
  setBounds(bounds: Rectangle): void;
  setParentWindow?(parent: unknown): void;
  on(
    event: "close" | "closed" | "moved" | "resized" | "ready-to-show",
    listener: (...args: unknown[]) => void,
  ): void;
  destroy(): void;
};

// The overlay is a transparent WebContentsView stacked above the content
// view. It is the only page the PiP loads; updates arrive through the
// window.wuuPip* hooks, user actions leave through wuu-pip:// navigations.
export interface BrowserPiPOverlayHandle {
  readonly webContents: {
    loadURL(url: string): Promise<unknown>;
    executeJavaScript(code: string, userGesture?: boolean): Promise<unknown>;
    setWindowOpenHandler(handler: () => { action: "deny" }): void;
    on(event: "will-navigate", listener: (event: unknown, url: string) => void): void;
    isDestroyed(): boolean;
  };
  setBounds(bounds: Rectangle): void;
}

type BrowserPiPSurfaceDeps = {
  activity: ActivitySession;
  bounds: Rectangle;
  sink: ObservationPiPEventSink;
  host: BrowserPiPTabHost;
  workdir: string;
  tabID: string;
  isPackaged: boolean;
  // Injectable for tests; production uses real Electron views/windows.
  createWindow?: (bounds: Rectangle) => BrowserPiPWindowHandle;
  createOverlay?: () => BrowserPiPOverlayHandle;
  parent?: () => { isDestroyed(): boolean } | null | undefined;
};

export class BrowserPiPSurface implements ObservationPiPHandle {
  private win: BrowserPiPWindowHandle | undefined;
  private overlay: BrowserPiPOverlayHandle | undefined;
  private mounted = false;
  private announced = false;
  private restoreBounds: Rectangle | undefined;
  private viewport: { width: number; height: number } = PIP_FALLBACK_VIEWPORT;
  private visible = false;
  private stopped = false;
  private activity: ActivitySession;
  private lastInteractionRevision = 0;
  private readonly unsubs: Array<() => void> = [];
  // Resting corner inside the conversation column. A release may change it;
  // later column resizes keep the card on that same corner.
  private alignment: PipAlignment = "bottom-right";
  // undefined: the column has not been measured yet. null: measured, and the
  // conversation column is not on screen.
  private screenLayout: BrowserPiPScreenLayout | null | undefined = undefined;
  private hostParent: { isDestroyed(): boolean } | null = null;
  private dragging = false;
  private grab: PipPoint = { x: 0, y: 0 };
  private snapTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly deps: BrowserPiPSurfaceDeps) {
    this.activity = deps.activity;
  }

  start(): void {
    if (this.stopped || this.win) return;
    const win = this.deps.createWindow
      ? this.deps.createWindow(this.deps.bounds)
      : this.createElectronWindow(this.deps.bounds);
    this.win = win;
    const overlay = this.deps.createOverlay ? this.deps.createOverlay() : this.createElectronOverlay();
    this.overlay = overlay;
    const bounds = win.getBounds();
    win.contentView.addChildView(overlay as unknown as BrowserViewHandle);
    overlay.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
    overlay.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    overlay.webContents.on("will-navigate", (event: unknown, rawURL: string) => {
      if (typeof rawURL === "string" && rawURL.startsWith("wuu-pip://")) {
        (event as { preventDefault?: () => void }).preventDefault?.();
        const drag = browserPiPDragCommand(rawURL);
        if (drag) {
          this.handleDrag(drag);
          this.execute("window.wuuPipDragAck?.()");
          return;
        }
        if (rawURL === "wuu-pip://close") this.deps.sink.onEvent({ event: "user_close" });
        if (rawURL === "wuu-pip://expand") this.deps.sink.onEvent({ event: "expand" });
      }
    });
    const initialLabel = pipHostname(
      this.deps.host.tabSurfaceMeta(this.deps.workdir, this.deps.tabID)?.url ?? "",
    );
    void overlay.webContents
      .loadURL(
        `data:text/html;charset=utf-8,${encodeURIComponent(browserPiPOverlayHTML(initialLabel))}`,
      )
      .then(() => {
        if (this.overlay !== overlay || this.win !== win || win.isDestroyed()) return;
        // The hooks only exist after the page loads; replay the state that
        // mount/state pushes before this moment silently dropped.
        this.syncOverlayMode();
        this.pushActivityState();
        this.pushHostLabel();
      })
      .catch(() => undefined);
    const reportGeometry = (): void => {
      if (win.isDestroyed() || this.win !== win) return;
      const b = win.getBounds();
      this.deps.sink.onEvent({ event: "geometry", x: b.x, y: b.y, width: b.width, height: b.height });
    };
    win.on("moved", reportGeometry);
    win.on("resized", () => {
      reportGeometry();
      this.refit();
    });
    win.on("ready-to-show", () => {
      if (win.isDestroyed() || this.win !== win) return;
      if (this.visible) win.showInactive();
    });
    // Reparent the agent tab out before the window dies: a child
    // WebContentsView would be destroyed with the window and kill the tab.
    win.on("close", () => this.unmount());
    win.on("closed", () => {
      if (this.win !== win) return;
      this.win = undefined;
      this.overlay = undefined;
      this.mounted = false;
      this.visible = false;
    });
    this.unsubs.push(
      this.deps.host.addTabClosedListener((workdir, tabID) => {
        if (this.matches(workdir, tabID)) this.deps.sink.onGone();
      }),
      this.deps.host.addInteractionListener((workdir, tabID, hint) => {
        if (this.matches(workdir, tabID)) this.forwardInteraction(hint);
      }),
      this.deps.host.addNavigateListener((workdir, tabID, url) => {
        if (this.matches(workdir, tabID)) this.pushHostLabel(url);
      }),
      this.deps.host.addTabReparentedListener((workdir, tabID, parent) => {
        if (!this.matches(workdir, tabID) || !this.mounted) return;
        if (parent !== this.win?.contentView) {
          // A visibility takeover adopted the tab. Do not fight for it — the
          // placeholder covers the empty PiP until the coordinator hides us.
          this.mounted = false;
          this.restoreBounds = undefined;
          this.syncOverlayMode();
        }
      }),
    );
  }

  setVisible(visible: boolean): void {
    this.visible = visible;
    this.applyVisibility();
  }

  private applyVisibility(): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    // Unknown layout (no report yet) still shows, so a card can appear before
    // the first measurement. An explicit empty layout means the column is gone.
    const columnGone = this.screenLayout === null;
    if (this.visible && !columnGone) {
      this.mount();
      if (this.win !== win || win.isDestroyed()) return;
      if (!win.isVisible()) win.showInactive();
    } else if (!this.visible) {
      this.unmount();
      if (this.win !== win || win.isDestroyed()) return;
      win.hide();
    } else {
      win.hide();
    }
  }

  // A live view has no frame production to freeze: a stopped activity simply
  // leaves its (static) page on screen — the same observation semantics as
  // the CUA PiP's frozen last frame, at zero capture cost.
  setLive(): void {}

  // Screen geometry of the conversation column. A null host means that column
  // is not on screen, so the card hides until it returns. Column changes while
  // the card is resting retarget the committed corner immediately; a drag in
  // progress keeps following the pointer and snaps on release.
  setHostLayout(layout: BrowserPiPScreenLayout | null): void {
    if (!layout) {
      this.screenLayout = null;
      this.applyVisibility();
      return;
    }
    this.screenLayout = layout;
    if (!this.dragging) this.placeCommitted();
    this.applyVisibility();
  }

  setHostParent(parent: { isDestroyed(): boolean } | null): void {
    const win = this.win;
    if (!win || win.isDestroyed() || !win.setParentWindow || this.hostParent === parent) return;
    this.hostParent = parent;
    if (!parent || parent.isDestroyed()) return;
    win.setParentWindow(parent);
  }

  updateActivity(activity: ActivitySession): void {
    this.activity = activity;
    this.pushActivityState();
  }

  animateInteraction(interaction: Interaction): void {
    if (interaction.revision <= this.lastInteractionRevision) return;
    this.lastInteractionRevision = interaction.revision;
    this.forwardInteraction({
      kind: interaction.kind as BrowserInteractionHint["kind"],
      x: interaction.x,
      y: interaction.y,
      direction: interaction.direction,
    });
  }

  stop(onStopped?: () => void): void {
    if (this.stopped) {
      onStopped?.();
      return;
    }
    this.stopped = true;
    this.cancelSnap();
    for (const unsub of this.unsubs.splice(0)) unsub();
    this.unmount();
    const win = this.win;
    this.win = undefined;
    this.overlay = undefined;
    if (win && !win.isDestroyed()) {
      try {
        win.destroy();
      } catch {
        // Already closing.
      }
    }
    onStopped?.();
  }

  // -------------------------------------------------------------------------
  // Mount lifecycle.
  // -------------------------------------------------------------------------
  private mount(): void {
    const win = this.win;
    if (!win || win.isDestroyed() || this.mounted) return;
    const { workdir, tabID, host } = this.deps;
    const bounds = host.tabBounds(workdir, tabID);
    if (!bounds) {
      this.reportTabGoneUnlessStarting();
      return;
    }
    this.viewport = {
      width: bounds.width > 0 ? bounds.width : PIP_FALLBACK_VIEWPORT.width,
      height: bounds.height > 0 ? bounds.height : PIP_FALLBACK_VIEWPORT.height,
    };
    const fit = this.containRect();
    const restore = host.mountTabOnWindow(
      workdir,
      tabID,
      win,
      { x: fit.x, y: fit.y, width: fit.width, height: fit.height },
      fit.scale,
    );
    if (!restore) {
      this.reportTabGoneUnlessStarting();
      return;
    }
    this.restoreBounds = restore;
    this.mounted = true;
    this.syncOverlayMode();
    this.pushHostLabel();
    if (!this.announced) {
      this.announced = true;
      this.deps.sink.onEvent({ event: "ready" });
    }
  }

  private reportTabGoneUnlessStarting(): void {
    // Activity creation precedes the first browser/open_tab request. Keep the
    // placeholder alive until the post-action update retries the mount; only a
    // missing tab after startup means that the observed target is truly gone.
    if (this.activity.state !== "starting") this.deps.sink.onGone();
  }

  private unmount(): void {
    if (!this.mounted) return;
    this.mounted = false;
    const win = this.win;
    const restore = this.restoreBounds;
    this.restoreBounds = undefined;
    if (win && !win.isDestroyed() && restore) {
      this.deps.host.unmountTabIfOwner(this.deps.workdir, this.deps.tabID, win.contentView, restore);
    }
    this.syncOverlayMode();
  }

  private refit(): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const b = win.getBounds();
    this.overlay?.setBounds({ x: 0, y: 0, width: b.width, height: b.height });
    if (!this.mounted) return;
    const fit = this.containRect();
    this.deps.host.relayoutMountedTab(
      this.deps.workdir,
      this.deps.tabID,
      win.contentView,
      { x: fit.x, y: fit.y, width: fit.width, height: fit.height },
      fit.scale,
    );
  }

  private containRect(): { x: number; y: number; width: number; height: number; scale: number } {
    const b = this.win?.getBounds() ?? this.deps.bounds;
    return pipContainRect(this.viewport.width, this.viewport.height, b.width, b.height);
  }

  private placeCommitted(): void {
    const layout = this.screenLayout;
    const win = this.win;
    if (!layout || !win || win.isDestroyed()) return;
    this.cancelSnap();
    const card = browserPiPCardSize(layout.host);
    const anchor = browserPiPAnchors(layout.host, layout.obstacles, card)
      .find((item) => item.alignment === this.alignment);
    if (!anchor) return;
    const origin = browserPiPOrigin(anchor, card);
    this.applyBounds({ x: origin.x, y: origin.y, width: card.width, height: card.height });
  }

  private handleDrag(command: { phase: "start" | "move" | "end"; x: number; y: number; vx: number; vy: number }): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const bounds = win.getBounds();
    if (command.phase === "start") {
      this.cancelSnap();
      this.dragging = true;
      this.grab = { x: command.x - bounds.x, y: command.y - bounds.y };
      return;
    }
    if (!this.dragging) return;
    const dragged = this.screenLayout
      ? browserPiPClampOrigin(
        { x: command.x - this.grab.x, y: command.y - this.grab.y },
        bounds,
        this.screenLayout.visibleFrame,
      )
      : { x: command.x - this.grab.x, y: command.y - this.grab.y };
    if (command.phase === "move") {
      this.applyBounds({ x: dragged.x, y: dragged.y, width: bounds.width, height: bounds.height });
      return;
    }
    this.dragging = false;
    this.applyBounds({ x: dragged.x, y: dragged.y, width: bounds.width, height: bounds.height });
    const layout = this.screenLayout;
    if (!layout) return;
    const settled = win.getBounds();
    const card = { width: settled.width, height: settled.height };
    const origin = { x: settled.x, y: settled.y };
    const nearest = browserPiPNearestAnchor(
      browserPiPAnchors(layout.host, layout.obstacles, card),
      origin,
      card,
      { x: command.vx, y: command.vy },
    );
    if (!nearest) return;
    this.alignment = nearest.alignment;
    this.animateTo(browserPiPOrigin(nearest, card), card, { x: command.vx, y: command.vy });
  }

  private animateTo(origin: PipPoint, card: { width: number; height: number }, velocity: PipPoint): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    this.cancelSnap();
    const start = win.getBounds();
    const from = { x: start.x, y: start.y };
    const startedAt = Date.now();
    const frame = (): void => {
      this.snapTimer = undefined;
      const current = this.win;
      if (!current || current.isDestroyed() || this.dragging) return;
      const t = browserPiPSnapEase((Date.now() - startedAt) / PIP_SNAP_MS);
      const point = browserPiPSnapPoint(from, origin, velocity, t);
      this.applyBounds({ x: point.x, y: point.y, width: card.width, height: card.height });
      if (t < 1) this.scheduleSnap(frame);
    };
    frame();
  }

  private scheduleSnap(frame: () => void): void {
    const timer = setTimeout(frame, 16);
    timer.unref?.();
    this.snapTimer = timer;
  }

  private cancelSnap(): void {
    if (this.snapTimer === undefined) return;
    clearTimeout(this.snapTimer);
    this.snapTimer = undefined;
  }

  private applyBounds(bounds: Rectangle): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const next = {
      x: Math.round(bounds.x),
      y: Math.round(bounds.y),
      width: Math.max(1, Math.round(bounds.width)),
      height: Math.max(1, Math.round(bounds.height)),
    };
    const current = win.getBounds();
    if (current.x === next.x && current.y === next.y && current.width === next.width && current.height === next.height) return;
    win.setBounds(next);
    if (current.width !== next.width || current.height !== next.height) this.refit();
  }

  private matches(workdir: string, tabID: string): boolean {
    return workdir === this.deps.workdir && tabID === this.deps.tabID;
  }

  // -------------------------------------------------------------------------
  // Overlay pushes.
  // -------------------------------------------------------------------------
  private syncOverlayMode(): void {
    this.execute(`window.wuuPipMount?.(${JSON.stringify({ mounted: this.mounted })})`);
  }

  private pushActivityState(): void {
    this.execute(
      `window.wuuPipState?.(${JSON.stringify({
        state: this.activity.state,
        controller: this.activity.controller,
      })})`,
    );
  }

  private pushHostLabel(url?: string): void {
    const meta = this.deps.host.tabSurfaceMeta(this.deps.workdir, this.deps.tabID);
    const label = pipHostname(url ?? meta?.url ?? "");
    this.execute(`window.wuuPipHost?.(${JSON.stringify({ label })})`);
  }

  private forwardInteraction(hint: BrowserInteractionHint): void {
    if (!this.mounted) return;
    const fit = this.containRect();
    this.execute(
      `window.wuuPipInteract?.(${JSON.stringify({
        kind: hint.kind,
        x: fit.x + hint.x * fit.scale,
        y: fit.y + hint.y * fit.scale,
        direction: hint.direction ?? "",
      })})`,
    );
  }

  private execute(code: string): void {
    const overlay = this.overlay;
    const win = this.win;
    if (!overlay || !win || win.isDestroyed()) return;
    void overlay.webContents.executeJavaScript(code, true).catch(() => undefined);
  }

  private createElectronWindow(bounds: Rectangle): BrowserPiPWindowHandle {
    const win = new BrowserWindow({
      width: bounds.width,
      height: bounds.height,
      x: bounds.x,
      y: bounds.y,
      frame: false,
      transparent: false,
      backgroundColor: "#f4f4f5",
      hasShadow: true,
      roundedCorners: true,
      skipTaskbar: true,
      resizable: false,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      acceptFirstMouse: true,
      show: false,
      type: "panel",
      minWidth: 120,
      minHeight: 120,
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        ...appShellWebPreferences(this.deps.isPackaged),
      },
    }) as unknown as BrowserPiPWindowHandle;
    const parent = this.deps.parent?.();
    if (parent && !parent.isDestroyed()) win.setParentWindow?.(parent);
    return win;
  }

  private createElectronOverlay(): BrowserPiPOverlayHandle {
    const view = new WebContentsView({
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        ...appShellWebPreferences(this.deps.isPackaged),
      },
    });
    view.setBackgroundColor("#00000000");
    return view as unknown as BrowserPiPOverlayHandle;
  }
}

// Factory covering both observation backends: CUA keeps its native helper,
// browser activities get the Electron surface. One coordinator, one surface,
// two presentation paths.
export function createObservationPiPFactory(deps: {
  browserHost: BrowserHostCoordinator;
  isPackaged: boolean;
  parent?: () => { isDestroyed(): boolean } | null | undefined;
}): ObservationPiPFactory {
  return (activity, _key, sink, bounds) => {
    if (activity.kind === "browser") {
      const tabID = activity.target?.trim();
      if (!tabID) return undefined;
      return new BrowserPiPSurface({
        activity,
        bounds: bounds(),
        sink,
        host: deps.browserHost,
        workdir: activity.workdir,
        tabID,
        isPackaged: deps.isPackaged,
        parent: deps.parent,
      });
    }
    const helper = resolveCUAFrameHelper();
    const target = activity.target?.trim();
    if (!helper || !target) return undefined;
    return new CUANativePiP(
      helper,
      activity.thread_id,
      target,
      activity.process_id,
      activity.window_id,
      bounds(),
      sink.onEvent,
      sink.onFailure,
    );
  };
}

// ---------------------------------------------------------------------------
// Overlay page. Sandboxed data: URL like the pet window; all updates arrive
// through window.wuuPip* hooks via executeJavaScript, user actions leave
// through wuu-pip:// navigations. The page draws no content of its own while
// mounted — the pixels below it are the real tab. Without a mounted view it
// shows the frosted placeholder, matching the CUA PiP's "never paint
// failure" rule.
// ---------------------------------------------------------------------------
export function browserPiPOverlayHTML(initialLabel: string): string {
  const label = JSON.stringify(initialLabel);
  return `<!doctype html>
<html><head><meta charset="utf-8" />
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{width:100%;height:100%;overflow:hidden;background:transparent;
  font-family:-apple-system,BlinkMacSystemFont,"Helvetica Neue",sans-serif;
  color:#fff;user-select:none}
#root{position:relative;width:100%;height:100%;cursor:grab;touch-action:none}
#root.dragging{cursor:grabbing}
#ph{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;
  background:#f4f4f5;color:rgba(28,28,30,.45);transition:opacity .2s ease}
#ph.gone{opacity:0;pointer-events:none}
#actions{position:absolute;top:8px;right:8px;display:flex;gap:6px;opacity:0;
  transition:opacity .12s ease}
#root:hover #actions,#root:focus-within #actions,#root.dragging #actions{opacity:1}
#expand,#close{width:26px;height:26px;border:none;border-radius:13px;padding:0;
  display:grid;place-items:center;cursor:pointer;color:#fff;
  background:rgba(28,28,30,.55);backdrop-filter:blur(10px)}
#expand:hover,#close:hover{background:rgba(28,28,30,.72)}
#close:hover{background:rgba(215,0,21,.82)}
#ptr{position:absolute;width:14px;height:14px;margin:-7px 0 0 -7px;border-radius:50%;
  background:rgba(255,255,255,.95);box-shadow:0 0 0 2px rgba(0,0,0,.45);
  opacity:0;pointer-events:none;transition:transform .14s ease-out,opacity .2s ease}
#ptr.on{opacity:1}
#ring{position:absolute;width:26px;height:26px;margin:-13px 0 0 -13px;border-radius:50%;
  border:2px solid rgba(255,255,255,.9);opacity:0;pointer-events:none}
#caret{position:absolute;width:2px;height:14px;margin:-7px 0 0 -1px;background:#fff;
  box-shadow:0 0 0 1px rgba(0,0,0,.4);opacity:0;pointer-events:none}
#scroll{position:absolute;font-size:14px;color:#fff;text-shadow:0 1px 2px rgba(0,0,0,.6);
  opacity:0;pointer-events:none;transform:translate(-50%,-50%)}
</style></head>
<body>
<div id="root">
  <div id="ph"><svg width="30" height="30" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
    <circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.6 2.6 3.9 5.7 3.9 9s-1.3 6.4-3.9 9c-2.6-2.6-3.9-5.7-3.9-9S9.4 5.6 12 3z"/>
  </svg></div>
  <div id="actions">
    <button id="expand" title="Open in the side panel" aria-label="Open in the side panel">
      <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2h4v4"/><path d="M12 2 7.5 6.5"/><path d="M6 3H3.5A1.5 1.5 0 0 0 2 4.5v6A1.5 1.5 0 0 0 3.5 12h6A1.5 1.5 0 0 0 11 10.5V8"/></svg>
    </button>
    <button id="close" title="Close" aria-label="Close">
      <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M2 2l8 8M10 2 2 10"/></svg>
    </button>
  </div>
  <div id="ring"></div><div id="ptr"></div><div id="caret"></div><div id="scroll"></div>
</div>
<script>
(function(){
  var ph=document.getElementById("ph");
  var ptr=document.getElementById("ptr");
  var ring=document.getElementById("ring");
  var caret=document.getElementById("caret");
  var scrollEl=document.getElementById("scroll");
  var root=document.getElementById("root");
  var hideTimer=0;
  var label=${label};
  var dragAck=true,dragQueued=null,ackTimer=0,grabbing=false,last=null,velocity={x:0,y:0};
  document.getElementById("close").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://close";
  });
  document.getElementById("expand").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://expand";
  });
  function place(el,x,y){el.style.transform="translate("+x+"px,"+y+"px)";}
  function postDrag(phase,x,y,vx,vy){
    var url="wuu-pip://drag?phase="+phase+"&x="+x+"&y="+y+"&vx="+vx+"&vy="+vy;
    if(!dragAck){dragQueued=url;return;}
    dragAck=false;
    clearTimeout(ackTimer);
    ackTimer=setTimeout(function(){window.wuuPipDragAck&&window.wuuPipDragAck();},80);
    window.location.href=url;
  }
  window.wuuPipDragAck=function(){
    dragAck=true;
    clearTimeout(ackTimer);
    if(!dragQueued)return;
    var url=dragQueued;dragQueued=null;
    postDragUrl(url);
  };
  function postDragUrl(url){
    if(!dragAck){dragQueued=url;return;}
    dragAck=false;
    clearTimeout(ackTimer);
    ackTimer=setTimeout(function(){window.wuuPipDragAck&&window.wuuPipDragAck();},80);
    window.location.href=url;
  }
  root.addEventListener("pointerdown",function(e){
    if(e.button!==0||(e.target&&e.target.closest&&e.target.closest("button")))return;
    grabbing=true;
    root.classList.add("dragging");
    root.setPointerCapture(e.pointerId);
    last={x:e.screenX,y:e.screenY,t:performance.now()};
    velocity={x:0,y:0};
    postDrag("start",e.screenX,e.screenY,0,0);
  });
  root.addEventListener("pointermove",function(e){
    if(!grabbing||!last)return;
    var now=performance.now(),dt=Math.max(1,now-last.t);
    velocity={x:(e.screenX-last.x)/dt*1000,y:(e.screenY-last.y)/dt*1000};
    last={x:e.screenX,y:e.screenY,t:now};
    postDrag("move",e.screenX,e.screenY,velocity.x,velocity.y);
  });
  function endDrag(e){
    if(!grabbing)return;
    grabbing=false;
    root.classList.remove("dragging");
    postDrag("end",e.screenX,e.screenY,velocity.x,velocity.y);
  }
  root.addEventListener("pointerup",endDrag);
  root.addEventListener("pointercancel",endDrag);
  window.wuuPipMount=function(m){
    ph.classList.toggle("gone",!!(m&&m.mounted));
  };
  window.wuuPipHost=function(h){
    label=(h&&h.label)||label;
  };
  window.wuuPipState=function(){};
  window.wuuPipInteract=function(it){
    var x=it.x||0,y=it.y||0;
    ptr.style.transition="transform .14s ease-out,opacity .2s ease";
    place(ptr,x,y);
    ptr.style.opacity="1";
    clearTimeout(hideTimer);
    hideTimer=setTimeout(function(){ptr.style.opacity="0";},1200);
    if(it.kind==="click"){
      place(ring,x,y);
      ring.style.opacity="0";
      ring.animate([{opacity:.9,transform:"translate("+x+"px,"+y+"px) scale(.4)"},
        {opacity:0,transform:"translate("+x+"px,"+y+"px) scale(1.4)"}],
        {duration:380,easing:"ease-out"});
    }else if(it.kind==="type"){
      place(caret,x,y);
      caret.animate([{opacity:1},{opacity:1}],{duration:600});
      caret.animate([{opacity:1},{opacity:0}],{duration:600,delay:600});
    }else if(it.kind==="scroll"){
      var ch={up:"↑",down:"↓",left:"←",right:"→"}[it.direction]||"↕";
      scrollEl.textContent=ch;
      scrollEl.animate([{opacity:.95,transform:"translate(-50%,-50%) translate("+x+"px,"+y+"px)"},
        {opacity:0,transform:"translate(-50%,-50%) translate("+x+"px,"+(y+(it.direction==="up"?10:-10))+"px)"}],
        {duration:520,easing:"ease-out"});
    }
  };
})();
</script>
</body></html>`;
}
