import { BrowserWindow, WebContentsView, screen, type Rectangle } from "electron";
import appIcon from "../../../assets/app-icon-source.svg?raw";
import { iconSVG } from "../shared/iconArtwork";
import type { ActivitySession } from "../shared/protocol";
import { cursorRuntimeSource } from "./agentCursor";
import { appShellWebPreferences } from "./appShellGuards";
import {
  browserPiPAnchors,
  browserPiPCardSize,
  browserPiPClampOrigin,
  browserPiPDragCommand,
  browserPiPFitCard,
  browserPiPNearestAnchor,
  browserPiPOrigin,
  browserPiPResizeCommand,
  browserPiPResizeRect,
  browserPiPSizeForAspect,
  browserPiPSnapEase,
  browserPiPSnapPoint,
  PIP_ANCHOR_MARGIN,
  PIP_MIN_SIZE,
  PIP_SNAP_MS,
  type BrowserPiPScreenLayout,
  type PipAlignment,
  type PipPoint,
  type PipRect,
  type PipResizeEdge,
} from "./browserPiPPlacement";
import type {
  BrowserHostCoordinator,
  BrowserInteractionHint,
  BrowserParentWindowHandle,
  BrowserViewHandle,
} from "./browserHostWindows";
import {
  type BrowserPiPHostWindow,
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

// Fit a page viewport into the card the way page zoom does. The layout size
// stays `contentW`×`contentH`; `scale` is the zoom factor (never above 1) and
// the returned rect is the zoomed DIP size, centered when the card is larger.
export function pipContainRect(
  contentW: number,
  contentH: number,
  boxW: number,
  boxH: number,
): { x: number; y: number; width: number; height: number; scale: number } {
  if (contentW <= 0 || contentH <= 0 || boxW <= 0 || boxH <= 0) {
    return { x: 0, y: 0, width: 0, height: 0, scale: 0 };
  }
  const scale = Math.min(1, boxW / contentW, boxH / contentH);
  const width = contentW * scale;
  const height = contentH * scale;
  return { x: (boxW - width) / 2, y: (boxH - height) / 2, width, height, scale };
}

// ---------------------------------------------------------------------------
// The browser PiP surface: an Electron panel window presenting the REAL tab
// view, reparented off the hidden host. The page keeps its layout viewport
// and is zoomed down so that whole viewport fits in the card. A
// transparent overlay view above it draws the chrome strip, the interaction
// effects, and the frosted placeholder, and swallows pointer input so the
// preview can never steal focus or become an input target. The card is
// watch-only: move and resize it, or expand it into the panel to take the
// page over. Scrollbar paint is suppressed on the page while this card owns
// the view.
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
    event: "close" | "closed" | "moved" | "resize" | "ready-to-show",
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
  cursorPosition?: () => PipPoint;
  parent?: () => BrowserPiPHostWindow | null | undefined;
};

export class BrowserPiPSurface implements ObservationPiPHandle {
  private win: BrowserPiPWindowHandle | undefined;
  private overlay: BrowserPiPOverlayHandle | undefined;
  private mounted = false;
  private announced = false;
  private restoreBounds: Rectangle | undefined;
  private visible = false;
  private stopped = false;
  private completed = false;
  private dark = false;
  private hoverTimer: ReturnType<typeof setInterval> | undefined;
  private hoverPoint: PipPoint | null | undefined;
  private activity: ActivitySession;
  private lastInteractionRevision = 0;
  private readonly unsubs: Array<() => void> = [];
  // Resting corner inside the conversation column. A release may change it;
  // later column resizes keep the card on that same corner.
  private alignment: PipAlignment = "bottom-right";
  // undefined: the column has not been measured yet. null: measured, and the
  // conversation column is not on screen.
  private screenLayout: BrowserPiPScreenLayout | null | undefined = undefined;
  private hostParent: BrowserPiPHostWindow | null = null;
  private detachHostParent: (() => void) | undefined;
  private dragging = false;
  private resizing = false;
  private grab: PipPoint = { x: 0, y: 0 };
  private userSize: { width: number; height: number } | undefined;
  // The page's layout viewport, captured before the card zooms it down.
  // Later view bounds are the zoomed size, so they must not replace this.
  private layoutViewport: { width: number; height: number } | undefined;
  private resizeGesture: { edge: PipResizeEdge; start: PipRect; pointer: PipPoint } | undefined;
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
        const resize = browserPiPResizeCommand(rawURL);
        if (resize) {
          this.handleResize(resize);
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
        this.setAppearance(this.dark);
        this.pushCompletion();
        this.hoverPoint = undefined;
        this.syncHover();
      })
      .catch(() => undefined);
    const reportGeometry = (): void => {
      if (win.isDestroyed() || this.win !== win) return;
      const b = win.getBounds();
      this.deps.sink.onEvent({ event: "geometry", x: b.x, y: b.y, width: b.width, height: b.height });
    };
    win.on("moved", reportGeometry);
    win.on("resize", () => {
      reportGeometry();
      this.refit();
    });
    win.on("ready-to-show", () => {
      if (win.isDestroyed() || this.win !== win) return;
      this.applyVisibility();
    });
    // Reparent the agent tab out before the window dies: a child
    // WebContentsView would be destroyed with the window and kill the tab.
    win.on("close", () => this.unmount());
    win.on("closed", () => {
      if (this.win !== win) return;
      this.stopHoverTracking();
      this.detachHostParent?.();
      this.detachHostParent = undefined;
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
        if (this.matches(workdir, tabID)) return this.forwardInteraction(hint);
      }),
      this.deps.host.addNavigateListener((workdir, tabID, url) => {
        if (!this.matches(workdir, tabID)) return;
        this.pushHostLabel(url);
        // Chromium can restore an origin's zoom during navigation.
        this.refit();
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
    this.setHostParent(this.deps.parent?.() ?? null);
  }

  setVisible(visible: boolean): void {
    this.visible = visible;
    if (!visible) this.setTurnCompleted(false);
    this.applyVisibility();
  }

  private applyVisibility(): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    // Unknown layout (no report yet) still shows, so a card can appear before
    // the first measurement. An explicit empty layout means the column is gone.
    const columnGone = this.screenLayout === null;
    const parent = this.hostParent;
    // Showing a native child window can reveal its hidden parent on macOS.
    // Restore the preview from host visibility events, never by showing Wuu.
    const parentHidden = parent && (parent.isDestroyed() || !parent.isVisible() || parent.isMinimized());
    if (this.visible && !columnGone && !parentHidden) {
      this.mount();
      if (this.win !== win || win.isDestroyed()) return;
      if (!win.isVisible()) win.showInactive();
      if (!this.hoverTimer) {
        this.syncHover();
        // Native pointer delivery can pause while another app is active.
        // Track only the visible card, without activating it or capturing frames.
        this.hoverTimer = setInterval(() => this.syncHover(), 80);
        this.hoverTimer.unref?.();
      }
    } else if (!this.visible) {
      this.stopHoverTracking();
      this.unmount();
      if (this.win !== win || win.isDestroyed()) return;
      win.hide();
    } else {
      this.stopHoverTracking();
      win.hide();
    }
  }

  private syncHover(): void {
    const win = this.win;
    if (!win || win.isDestroyed() || !win.isVisible()) return;
    const cursor = this.deps.cursorPosition?.() ?? screen.getCursorScreenPoint();
    const bounds = win.getBounds();
    const x = cursor.x - bounds.x, y = cursor.y - bounds.y;
    const point = x >= 0 && y >= 0 && x < bounds.width && y < bounds.height ? { x, y } : null;
    if (this.hoverPoint !== undefined && this.hoverPoint?.x === point?.x && this.hoverPoint?.y === point?.y) return;
    this.hoverPoint = point;
    this.execute(`window.wuuPipHover?.(${JSON.stringify(point)})`);
  }

  private stopHoverTracking(): void {
    if (this.hoverTimer) clearInterval(this.hoverTimer);
    this.hoverTimer = undefined;
    this.hoverPoint = undefined;
    this.execute("window.wuuPipHover?.(null)");
  }

  // A live view has no frame production to freeze: a stopped activity simply
  // leaves its (static) page on screen — the same observation semantics as
  // the CUA PiP's frozen last frame, at zero capture cost.
  setLive(): void {}

  setAppearance(dark: boolean): void {
    this.dark = dark;
    this.execute(`window.wuuPipAppearance?.(${JSON.stringify({ dark })})`);
  }

  setTurnCompleted(completed: boolean): void {
    if (this.completed === completed) return;
    this.completed = completed;
    this.pushCompletion();
  }

  private pushCompletion(): void {
    this.execute(`window.wuuPipCompleted?.(${this.completed})`);
  }

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
    if (!this.dragging && !this.resizing) this.placeCommitted();
    this.applyVisibility();
  }

  setHostParent(parent: BrowserPiPHostWindow | null): void {
    const win = this.win;
    if (!win || win.isDestroyed() || this.hostParent === parent) return;
    this.detachHostParent?.();
    this.detachHostParent = undefined;
    this.hostParent = parent;
    if (win.isVisible()) win.hide();
    win.setParentWindow?.(parent && !parent.isDestroyed() ? parent : null);
    if (parent && !parent.isDestroyed()) {
      const events = ["show", "hide", "minimize", "restore"] as const;
      const syncVisibility = (): void => this.applyVisibility();
      for (const event of events) parent.on(event, syncVisibility);
      this.detachHostParent = () => {
        for (const event of events) parent.removeListener(event, syncVisibility);
      };
    }
    this.applyVisibility();
  }

  updateActivity(activity: ActivitySession): void {
    this.activity = activity;
    if (activity.state === "stopped" || activity.state === "error" || activity.controller === "user") {
      this.setTurnCompleted(false);
    }
    this.pushActivityState();
  }

  retarget(activity: ActivitySession, sink: ObservationPiPEventSink): void {
    this.setTurnCompleted(false);
    this.unmount();
    const bounds = this.win?.getBounds();
    // Page aspect ratios must not reset the user's card size or resting corner.
    if (bounds) this.userSize = { width: bounds.width, height: bounds.height };
    this.deps.workdir = activity.workdir;
    this.deps.tabID = activity.target!.trim();
    this.deps.sink = sink;
    this.layoutViewport = undefined;
    this.announced = false;
    this.lastInteractionRevision = 0;
    this.updateActivity(activity);
    this.pushHostLabel();
    // The coordinator applies visibility after rebinding, so a tab already in
    // the workspace panel is never pulled back into the card in between.
  }

  animateInteraction(interaction: Interaction): void {
    if (interaction.revision <= this.lastInteractionRevision) return;
    this.lastInteractionRevision = interaction.revision;
    this.forwardInteraction({
      kind: interaction.kind as "click" | "type" | "scroll",
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
    this.stopHoverTracking();
    this.detachHostParent?.();
    this.detachHostParent = undefined;
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
    if (bounds.width > 0 && bounds.height > 0) {
      this.layoutViewport = { width: bounds.width, height: bounds.height };
      if (!this.userSize && this.screenLayout && !this.dragging && !this.resizing) {
        this.placeCommitted();
      }
    }
    const fit = this.contentRect();
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
    // The page view is added after the overlay, which would put it on top and
    // let clicks reach the page. The overlay has to stay above it: the card
    // is moved and resized from that layer, and the page is not a click target.
    this.raiseOverlay();
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
    const fit = this.contentRect();
    this.execute(`window.wuuPipViewport?.(${JSON.stringify(fit)})`);
    this.deps.host.relayoutMountedTab(
      this.deps.workdir,
      this.deps.tabID,
      win.contentView,
      { x: fit.x, y: fit.y, width: fit.width, height: fit.height },
      fit.scale,
    );
  }

  private contentRect(): { x: number; y: number; width: number; height: number; scale: number } {
    const b = this.win?.getBounds() ?? this.deps.bounds;
    const viewport = this.layoutViewport;
    if (!viewport) return { x: 0, y: 0, width: Math.max(0, b.width), height: Math.max(0, b.height), scale: 1 };
    return pipContainRect(viewport.width, viewport.height, b.width, b.height);
  }

  private placeCommitted(): void {
    const layout = this.screenLayout;
    const win = this.win;
    if (!layout || !win || win.isDestroyed()) return;
    this.cancelSnap();
    const aspect = this.layoutViewport && this.layoutViewport.height > 0
      ? this.layoutViewport.width / this.layoutViewport.height
      : 4 / 3;
    const card = this.userSize
      ? browserPiPFitCard(layout.host, this.userSize)
      : browserPiPCardSize(layout.host, browserPiPSizeForAspect(aspect));
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
      this.resizing = false;
      this.resizeGesture = undefined;
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

  private handleResize(command: { phase: "start" | "move" | "end"; edge: PipResizeEdge; x: number; y: number }): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    if (command.phase === "start") {
      this.cancelSnap();
      this.dragging = false;
      this.resizing = true;
      const bounds = win.getBounds();
      this.resizeGesture = {
        edge: command.edge,
        start: { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height },
        pointer: { x: command.x, y: command.y },
      };
      return;
    }
    const gesture = this.resizeGesture;
    if (!this.resizing || !gesture) return;
    const frame = this.resizeFrame();
    const next = frame
      ? browserPiPResizeRect(
        gesture.start,
        gesture.edge,
        { x: command.x - gesture.pointer.x, y: command.y - gesture.pointer.y },
        { min: PIP_MIN_SIZE, frame },
      )
      : gesture.start;
    this.applyBounds(next);
    if (command.phase === "move") return;
    this.resizing = false;
    this.resizeGesture = undefined;
    const settled = win.getBounds();
    this.userSize = { width: settled.width, height: settled.height };
    const layout = this.screenLayout;
    if (!layout) return;
    const nearest = browserPiPNearestAnchor(
      browserPiPAnchors(layout.host, layout.obstacles, this.userSize),
      { x: settled.x, y: settled.y },
      this.userSize,
      { x: 0, y: 0 },
    );
    if (nearest) this.alignment = nearest.alignment;
  }

  // The card may occupy the column inside the same margin the corners use.
  private resizeFrame(): PipRect | undefined {
    const layout = this.screenLayout;
    if (!layout) return undefined;
    const innerW = Math.max(1, layout.host.width - PIP_ANCHOR_MARGIN * 2);
    const innerH = Math.max(1, layout.host.height - PIP_ANCHOR_MARGIN * 2);
    return {
      x: layout.host.x + PIP_ANCHOR_MARGIN,
      y: layout.host.y + PIP_ANCHOR_MARGIN,
      width: innerW,
      height: innerH,
    };
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
      if (!current || current.isDestroyed() || this.dragging || this.resizing) return;
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

  private raiseOverlay(): void {
    const win = this.win;
    const overlay = this.overlay;
    if (!win || win.isDestroyed() || !overlay) return;
    const view = overlay as unknown as BrowserViewHandle;
    // Adding an existing child raises it without detaching its native view.
    // Detaching during a resize interrupts the overlay's pointer capture.
    win.contentView.addChildView(view);
    const b = win.getBounds();
    overlay.setBounds({ x: 0, y: 0, width: b.width, height: b.height });
  }

  private matches(workdir: string, tabID: string): boolean {
    return workdir === this.deps.workdir && tabID === this.deps.tabID;
  }

  // -------------------------------------------------------------------------
  // Overlay pushes.
  // -------------------------------------------------------------------------
  private syncOverlayMode(): void {
    this.execute(`window.wuuPipViewport?.(${JSON.stringify(this.contentRect())})`);
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

  private forwardInteraction(hint: BrowserInteractionHint): Promise<unknown> | undefined {
    if (hint.kind !== "clear" && (!this.mounted || !this.win?.isVisible())) return;
    if (hint.kind !== "clear") this.setTurnCompleted(false);
    return this.execute(`window.wuuPipInteract?.(${JSON.stringify(hint)})`);
  }

  private execute(code: string): Promise<unknown> | undefined {
    const overlay = this.overlay;
    const win = this.win;
    if (!overlay || !win || win.isDestroyed()) return;
    return overlay.webContents.executeJavaScript(code, true).catch(() => undefined);
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
      minWidth: 80,
      minHeight: 64,
      webPreferences: {
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        ...appShellWebPreferences(this.deps.isPackaged),
      },
    }) as unknown as BrowserPiPWindowHandle;
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
  parent?: () => BrowserPiPHostWindow | null | undefined;
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
  const label = JSON.stringify(initialLabel).replace(/</g, "\\u003c");
  const icon = `data:image/svg+xml,${encodeURIComponent(appIcon)}`;
  return `<!doctype html>
<html><head><meta charset="utf-8" />
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{color-scheme:light;--surface:#f4f4f5;--muted:rgba(28,28,30,.45);--completion-scrim:rgba(255,255,255,.30)}
:root.dark{color-scheme:dark;--surface:#242426;--muted:rgba(255,255,255,.55);--completion-scrim:rgba(0,0,0,.24)}
::-webkit-scrollbar{width:0;height:0;display:none}
html,body{width:100%;height:100%;overflow:hidden;scrollbar-width:none;
  background:rgba(0,0,0,0.004);
  font-family:-apple-system,BlinkMacSystemFont,"Helvetica Neue",sans-serif;
  color:#fff;user-select:none}
#root{position:relative;width:100%;height:100%;cursor:grab;touch-action:none;
  background:rgba(0,0,0,0.004)}
#root.dragging{cursor:grabbing}
#ph{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;
  background:var(--surface);color:var(--muted);transition:opacity .2s ease}
#ph.gone{opacity:0;pointer-events:none}
#completion{position:absolute;inset:0;display:grid;place-items:center;pointer-events:none;
  background:var(--completion-scrim);opacity:0;transition:opacity .2s ease}
#completion[data-completed="true"]{opacity:1}
#completion-mark{position:relative;width:64px;height:64px}
#completion-icon{display:block;width:100%;height:100%;border-radius:15px;
  box-shadow:0 3px 12px rgba(0,0,0,.20)}
#completion-check{position:absolute;inset:0;display:grid;place-items:center;border-radius:50%;
  background:#4cc38a;color:#082a1b;box-shadow:0 3px 10px rgba(0,0,0,.22);
  opacity:0;transform:translate(27px,27px) scale(.46)}
#completion-check svg{width:34px;height:34px}
#completion[data-completed="true"] #completion-icon{animation:completion-icon .28s ease-out both}
#completion[data-completed="true"] #completion-check{animation:completion-check 1.5s ease both}
@keyframes completion-icon{from{opacity:0;transform:scale(.88)}to{opacity:1;transform:scale(1)}}
@keyframes completion-check{
  0%,18%{opacity:0;transform:scale(.5)}
  36%{opacity:1;transform:scale(1.08)}
  46%,66%{opacity:1;transform:scale(1)}
  90%{opacity:1;transform:translate(29px,29px) scale(.43)}
  100%{opacity:1;transform:translate(27px,27px) scale(.46)}}
@media (prefers-reduced-motion:reduce){
  #completion{transition:none}
  #completion[data-completed="true"] #completion-icon{animation:none}
  #completion[data-completed="true"] #completion-check{animation:none;opacity:1}
}
@media (max-height:140px){#completion-mark{width:48px;height:48px;transform:translateY(8px)}}
#hover-shade{position:absolute;inset:0;pointer-events:none;opacity:0;
  background:linear-gradient(rgba(0,0,0,.26),rgba(0,0,0,.04) 60%,transparent);
  transition:opacity .16s ease}
#actions{position:absolute;top:8px;left:8px;z-index:6;display:flex;gap:6px;opacity:0;
  transition:opacity .16s ease}
#root:is(:focus-within,.hovered,.dragging,.resizing) :is(#actions,#hover-shade){opacity:1}
#expand,#close{width:30px;height:30px;border:none;border-radius:50%;padding:0;
  display:grid;place-items:center;cursor:pointer;color:#fff;
  background:rgba(28,28,30,.48);backdrop-filter:blur(10px);transition:background .12s ease}
#expand:is(:hover,.hovered),#close:is(:hover,.hovered){background:rgba(28,28,30,.78)}
#expand:active,#close:active{background:rgba(28,28,30,.92)}
#expand:focus-visible,#close:focus-visible{outline:2px solid #fff;outline-offset:2px}
@media(prefers-reduced-motion:reduce){#actions,#hover-shade,#expand,#close{transition:none}}
[data-resize]{position:absolute;z-index:4;touch-action:none;background:rgba(0,0,0,0.004)}
[data-resize="n"],[data-resize="s"]{left:18px;right:18px;height:12px;cursor:ns-resize}
[data-resize="n"]{top:0}[data-resize="s"]{bottom:0}
[data-resize="e"],[data-resize="w"]{top:18px;bottom:18px;width:12px;cursor:ew-resize}
[data-resize="e"]{right:0}[data-resize="w"]{left:0}
[data-resize="nw"],[data-resize="ne"],[data-resize="sw"],[data-resize="se"]{width:18px;height:18px}
[data-resize="nw"]{top:0;left:0;cursor:nwse-resize}
[data-resize="se"]{bottom:0;right:0;cursor:nwse-resize}
[data-resize="ne"]{top:0;right:0;cursor:nesw-resize}
[data-resize="sw"]{bottom:0;left:0;cursor:nesw-resize}
</style></head>
<body>
<div id="root">
  <div id="ph">${iconSVG("Globe", 30)}</div>
  <div id="completion" role="status" aria-label="Browser task completed" aria-hidden="true" data-completed="false">
    <div id="completion-mark">
      <img id="completion-icon" src="${icon}" alt="" draggable="false" />
      <div id="completion-check">${iconSVG("Check", 34)}</div>
    </div>
  </div>
  <div id="hover-shade"></div>
  <div id="actions">
    <button id="close" title="Close" aria-label="Close">
      ${iconSVG("X", 14)}
    </button>
    <button id="expand" title="Open in the side panel" aria-label="Open in the side panel">
      ${iconSVG("ExternalLink", 14)}
    </button>
  </div>
  <div data-resize="nw"></div><div data-resize="n"></div><div data-resize="ne"></div>
  <div data-resize="w"></div><div data-resize="e"></div>
  <div data-resize="sw"></div><div data-resize="s"></div><div data-resize="se"></div>
</div>
<script>
(function(){
  var ph=document.getElementById("ph");
  var cursor=(${cursorRuntimeSource})();
  var root=document.getElementById("root");
  var completion=document.getElementById("completion");
  var label=${label};
  var dragAck=true,dragQueued=null,ackTimer=0,grabbing=false,resizing=false,resizeEdge="",last=null,velocity={x:0,y:0};
  document.getElementById("close").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://close";
  });
  document.getElementById("expand").addEventListener("click",function(e){
    e.stopPropagation();
    window.location.href="wuu-pip://expand";
  });
  function postDrag(phase,x,y,vx,vy){
    postUrl("wuu-pip://drag?phase="+phase+"&x="+x+"&y="+y+"&vx="+vx+"&vy="+vy);
  }
  function postResize(phase,edge,x,y){
    postUrl("wuu-pip://resize?phase="+phase+"&edge="+edge+"&x="+x+"&y="+y);
  }
  function postUrl(url){
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
  function postDragUrl(url){postUrl(url);}
  root.addEventListener("pointerdown",function(e){
    if(e.button!==0||(e.target&&e.target.closest&&(e.target.closest("button")||e.target.closest("[data-resize]"))))return;
    grabbing=true;
    root.classList.add("dragging");
    root.setPointerCapture(e.pointerId);
    last={x:e.screenX,y:e.screenY,t:performance.now()};
    velocity={x:0,y:0};
    postDrag("start",e.screenX,e.screenY,0,0);
  });
  Array.prototype.forEach.call(root.querySelectorAll("[data-resize]"),function(el){
    el.addEventListener("pointerdown",function(e){
      if(e.button!==0)return;
      e.stopPropagation();
      resizing=true;
      grabbing=false;
      resizeEdge=el.getAttribute("data-resize")||"";
      root.classList.add("resizing");
      el.setPointerCapture(e.pointerId);
      postResize("start",resizeEdge,e.screenX,e.screenY);
    });
  });
  root.addEventListener("pointermove",function(e){
    if(resizing){postResize("move",resizeEdge,e.screenX,e.screenY);return;}
    if(!grabbing||!last)return;
    var now=performance.now(),dt=Math.max(1,now-last.t);
    velocity={x:(e.screenX-last.x)/dt*1000,y:(e.screenY-last.y)/dt*1000};
    last={x:e.screenX,y:e.screenY,t:now};
    postDrag("move",e.screenX,e.screenY,velocity.x,velocity.y);
  });
  function endDrag(e){
    if(resizing){
      resizing=false;
      root.classList.remove("resizing");
      postResize("end",resizeEdge,e.screenX,e.screenY);
      return;
    }
    if(!grabbing)return;
    grabbing=false;
    root.classList.remove("dragging");
    postDrag("end",e.screenX,e.screenY,velocity.x,velocity.y);
  }
  root.addEventListener("pointerup",endDrag);
  root.addEventListener("pointercancel",endDrag);
  window.wuuPipMount=function(m){
    ph.classList.toggle("gone",!!(m&&m.mounted));
    if(!m||!m.mounted)cursor.hide();
  };
  window.wuuPipHost=function(h){
    label=(h&&h.label)||label;
  };
  window.wuuPipViewport=function(viewport){cursor.setViewport(viewport);};
  window.wuuPipState=function(state){
    if(state.controller!=="agent"||state.state==="stopped")cursor.hide();
  };
  window.wuuPipHover=function(point){
    root.classList.toggle("hovered",!!point);
    ["close","expand"].forEach(function(id){
      var button=document.getElementById(id),r=button.getBoundingClientRect();
      button.classList.toggle("hovered",!!point&&point.x>=r.left&&point.x<r.right&&point.y>=r.top&&point.y<r.bottom);
    });
  };
  window.wuuPipAppearance=function(a){document.documentElement.classList.toggle("dark",a.dark);};
  window.wuuPipCompleted=function(done){
    if(completion.dataset.completed===String(done))return;
    completion.dataset.completed=String(done);
    completion.setAttribute("aria-hidden",String(!done));
    if(done)cursor.hide();
  };
  window.wuuPipInteract=function(it){
    if(it.kind==="clear"){cursor.hide();return;}
    if(it.kind==="move")return cursor.moveTo(it.x,it.y);
    cursor.feedback(it);
  };
})();
</script>
</body></html>`;
}
