import { BrowserWindow, WebContentsView, screen, systemPreferences, type Rectangle } from "electron";
import type { ActivitySession } from "../shared/protocol";
import type { ActivityPreviewAppearance } from "../shared/activityPreview";
import { appShellWebPreferences } from "./appShellGuards";
import { browserPiPOverlayHTML } from "./activityPiPChrome";
import { activityPreviewIsLive, pipContainRect } from "./activityPiPContent";
import {
  browserPiPAnchors, browserPiPCardSize, browserPiPClampOrigin, browserPiPDragCommand,
  browserPiPFitCard, browserPiPNearestAnchor, browserPiPOrigin, browserPiPResizeCommand,
  browserPiPResizeRect, browserPiPSizeForAspect, PIP_ANCHOR_MARGIN, PIP_MIN_SIZE,
  type BrowserPiPScreenLayout, type PipAlignment, type PipPoint, type PipRect, type PipResizeEdge,
} from "./browserPiPPlacement";
import type { BrowserInteractionHint, BrowserParentWindowHandle, BrowserViewHandle } from "./browserHostWindows";
import type { BrowserPiPHostWindow, ObservationPiPEventSink, ObservationPiPHandle } from "./cuaActivityWindows";
import type { ActivityPiPContent, ActivityPiPContentFactory } from "./activityPiPContent";
import type { CUANativePiPEvent } from "./cuaFrameStreams";
type Interaction = NonNullable<ActivitySession["interaction"]>;

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

export type ActivityPiPSurfaceDeps = {
  activity: ActivitySession;
  bounds: Rectangle;
  sink: ObservationPiPEventSink;
  createContent: ActivityPiPContentFactory;
  isPackaged: boolean;
  // Injectable for tests; production uses real Electron views/windows.
  createWindow?: (bounds: Rectangle) => BrowserPiPWindowHandle;
  createOverlay?: () => BrowserPiPOverlayHandle;
  cursorPosition?: () => PipPoint;
  reducedMotion?: () => boolean;
  parent?: () => BrowserPiPHostWindow | null | undefined;
};

export class ActivityPiPSurface implements ObservationPiPHandle {
  private win: BrowserPiPWindowHandle | undefined;
  private overlay: BrowserPiPOverlayHandle | undefined;
  private mounted = false;
  private visible = false;
  private stopped = false;
  private completed = false;
  private dark = false;
  private hoverTimer: ReturnType<typeof setInterval> | undefined;
  private hoverPoint: PipPoint | null | undefined;
  private activity: ActivitySession;
  private lastInteractionRevision = 0;
  private pendingInteraction: Interaction | undefined;
  private content: ActivityPiPContent | undefined;
  private contentGeneration = 0;
  private contentStopping = false;
  private awaitingVisibility = false;
  private pendingRetarget = false;
  private appearance: ActivityPreviewAppearance | undefined;
  private controlPending = false;
  private captureState: Pick<CUANativePiPEvent, "status" | "message"> = { status: "healthy" };
  private stopCallbacks: Array<() => void> = [];
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
  // Choose the default card shape once. A panel visit changes the page
  // viewport, but must not reshape the card when it returns.
  private initialAspect: number | undefined;
  // The page's layout viewport, captured before the card zooms it down.
  // Later view bounds are the zoomed size, so they must not replace this.
  private layoutViewport: { width: number; height: number } | undefined;
  private resizeGesture: { edge: PipResizeEdge; start: PipRect; pointer: PipPoint } | undefined;
  private snapTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly deps: ActivityPiPSurfaceDeps) {
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
        if (rawURL.startsWith("wuu-pip://control?")) {
          const action = new URL(rawURL).searchParams.get("action");
          if (!this.controlPending && (action === "takeover" || action === "release" || action === "stop")) {
            this.deps.sink.onEvent({ event: "control", action, execution_id: new URL(rawURL).searchParams.get("execution_id") ?? "" });
          }
        }
        if (rawURL === "wuu-pip://close") this.deps.sink.onEvent({ event: "user_close" });
        if (rawURL === "wuu-pip://expand") this.deps.sink.onEvent({ event: "expand" });
      }
    });
    const initialLabel = this.activity.target ?? "";
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
        this.setCaptureStatus(this.captureState);
        this.content?.refresh?.();
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
    // Closing hides the host; only application teardown destroys it. The
    // content adapter parks browser tabs before that explicit teardown.
    win.on("close", (event) => {
      (event as { preventDefault?: () => void }).preventDefault?.();
      this.setVisible(false);
      this.deps.sink.onEvent({ event: "user_close" });
    });
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
    this.startContent();
    this.setHostParent(this.deps.parent?.() ?? null);
  }

  setVisible(visible: boolean): void {
    this.awaitingVisibility = false;
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
      // On macOS, showing a native child orders its parent forward even with
      // showInactive(). Defer a new presentation until the user returns to its
      // host; an already-visible card stays put, including while it has focus.
      if (!win.isVisible() && parent && !parent.isFocused()) return;
      this.content?.setVisible(true);
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
      this.content?.setVisible(false);
      if (this.win !== win || win.isDestroyed()) return;
      win.hide();
    } else {
      this.stopHoverTracking();
      this.content?.setVisible(false);
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

  // Native adapters stop producing frames; a browser adapter keeps its real
  // page mounted after task control has been revoked by the core.
  setLive(live: boolean): void { this.content?.setLive?.(live); }

  setAppearance(dark: boolean, preferences?: ActivityPreviewAppearance): void {
    this.dark = dark;
    if (preferences) this.appearance = preferences;
    this.execute(`window.wuuPipAppearance?.(${JSON.stringify({ dark, ...this.appearance })})`);
  }

  setControlPending(pending: boolean): void {
    this.controlPending = pending;
    this.pushActivityState();
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
      const events = ["show", "hide", "minimize", "restore", "focus"] as const;
      const syncVisibility = (): void => this.applyVisibility();
      for (const event of events) parent.on(event, syncVisibility);
      this.detachHostParent = () => {
        for (const event of events) parent.removeListener(event, syncVisibility);
      };
    }
    this.applyVisibility();
  }

  updateActivity(activity: ActivitySession): void {
    if (activity.execution_id !== this.activity.execution_id) {
      this.lastInteractionRevision = 0;
      this.pendingInteraction = undefined;
      this.forwardInteraction({ kind: "clear" });
    }
    if (activity.controller !== "agent") this.pendingInteraction = undefined;
    this.activity = activity;
    if (activity.state === "stopped" || activity.state === "error" || activity.controller === "user") {
      this.setTurnCompleted(false);
    }
    this.pushActivityState();
    this.content?.updateActivity?.(activity);
  }

  retarget(activity: ActivitySession, sink: ObservationPiPEventSink): void {
    this.setTurnCompleted(false);
    this.cancelSnap();
    const bounds = this.win?.getBounds();
    // Provider switches preserve the user's exact card size and resting corner.
    if (bounds) this.userSize = { width: bounds.width, height: bounds.height };
    this.deps.sink = sink;
    this.activity = activity;
    this.lastInteractionRevision = 0;
    this.pendingInteraction = undefined;
    this.controlPending = false;
    this.setCaptureStatus({ status: "healthy" });
    this.pendingRetarget = true;
    this.awaitingVisibility = true;
    this.stopContent();
    this.pushActivityState();
  }

  suspendContent(message: string): void {
    this.pendingRetarget = false;
    this.stopContent();
    this.setCaptureStatus({ status: "error", message });
  }

  animateInteraction(interaction: Interaction): void {
    if (interaction.revision <= this.lastInteractionRevision) return;
    if (this.activity.controller !== "agent") return;
    if (this.activity.kind === "cua" && (!this.mounted || !this.layoutViewport)) {
      this.pendingInteraction = interaction;
      return;
    }
    this.lastInteractionRevision = interaction.revision;
    if (this.activity.kind === "cua") {
      const viewport = this.layoutViewport!;
      const x = (interaction.to_x ?? interaction.x) * viewport.width;
      const y = (interaction.to_y ?? interaction.y) * viewport.height;
      const generation = this.contentGeneration;
      const executionID = this.activity.execution_id;
      void this.forwardInteraction({ kind: "move", x, y })?.then(() => {
        if (this.contentGeneration !== generation || this.lastInteractionRevision !== interaction.revision
          || this.activity.execution_id !== executionID || this.activity.controller !== "agent") return;
        if (interaction.kind === "click" || interaction.kind === "type" || interaction.kind === "scroll") {
          this.forwardInteraction({ kind: interaction.kind, x, y, direction: interaction.direction });
        }
      });
      return;
    }
    if (interaction.kind === "click" || interaction.kind === "type" || interaction.kind === "scroll") {
      this.forwardInteraction({ kind: interaction.kind, x: interaction.x, y: interaction.y, direction: interaction.direction });
    }
  }

  stop(onStopped?: () => void): void {
    if (onStopped) this.stopCallbacks.push(onStopped);
    if (this.stopped) {
      if (!this.contentStopping) this.finishStop();
      return;
    }
    this.stopped = true;
    this.pendingRetarget = false;
    this.stopHoverTracking();
    this.detachHostParent?.();
    this.detachHostParent = undefined;
    this.cancelSnap();
    this.stopContent();
  }

  private finishStop(): void {
    const win = this.win;
    this.win = undefined;
    this.overlay = undefined;
    if (win && !win.isDestroyed()) win.destroy();
    for (const callback of this.stopCallbacks.splice(0)) callback();
  }

  private startContent(): void {
    const win = this.win;
    if (!win || win.isDestroyed() || this.stopped) return;
    this.pendingRetarget = false;
    const generation = ++this.contentGeneration;
    const current = (): boolean => generation === this.contentGeneration && !this.stopped;
    const content = this.deps.createContent(this.activity, {
      window: win,
      setViewport: (viewport) => {
        if (!current()) return;
        this.layoutViewport = viewport;
        this.initialAspect ??= viewport.width / viewport.height;
        if (!this.userSize && this.screenLayout && !this.dragging && !this.resizing) this.placeCommitted();
        this.refit();
      },
      setMounted: (mounted) => {
        if (!current()) return;
        if (this.mounted === mounted) return;
        this.mounted = mounted;
        this.syncOverlayMode();
        if (mounted) {
          this.raiseOverlay();
          const interaction = this.pendingInteraction;
          this.pendingInteraction = undefined;
          if (interaction) this.animateInteraction(interaction);
        }
      },
      setLabel: (label) => { if (current()) this.execute(`window.wuuPipHost?.(${JSON.stringify({ label })})`); },
      onInteraction: (hint) => current() ? this.forwardInteraction(hint) : undefined,
      onEvent: (event) => {
        if (!current()) return;
        if (event.event === "capture_status") this.setCaptureStatus(event);
        this.deps.sink.onEvent(event);
      },
      onFailure: (message) => { if (current()) this.deps.sink.onFailure(message); },
      onGone: () => { if (current()) this.deps.sink.onGone(); },
    });
    this.content = content;
    this.mounted = false;
    this.layoutViewport = undefined;
    this.setCaptureStatus({ status: "healthy" });
    this.syncOverlayMode();
    if (!content) {
      this.setCaptureStatus({ status: "error", message: "Preview is unavailable" });
      return;
    }
    content.start();
    content.setLive?.(activityPreviewIsLive(this.activity));
    this.refit();
    this.raiseOverlay();
    if (!this.awaitingVisibility) this.applyVisibility();
  }

  private stopContent(): void {
    ++this.contentGeneration;
    if (this.contentStopping) return;
    const content = this.content;
    this.content = undefined;
    this.mounted = false;
    this.syncOverlayMode();
    this.contentStopping = true;
    const stopped = (): void => {
      this.contentStopping = false;
      if (this.stopped) this.finishStop();
      else if (this.pendingRetarget) this.startContent();
    };
    if (content) content.stop(stopped);
    else stopped();
  }

  private refit(): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const bounds = win.getBounds();
    this.overlay?.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
    this.content?.layout({ x: 0, y: 0, width: bounds.width, height: bounds.height });
    this.execute(`window.wuuPipViewport?.(${JSON.stringify(this.contentRect())})`);
  }

  private contentRect(): { x: number; y: number; width: number; height: number; scale: number } {
    const bounds = this.win?.getBounds() ?? this.deps.bounds;
    const viewport = this.layoutViewport;
    if (!viewport) return { x: 0, y: 0, width: bounds.width, height: bounds.height, scale: 1 };
    return pipContainRect(viewport.width, viewport.height, bounds.width, bounds.height);
  }

  private placeCommitted(): void {
    const layout = this.screenLayout;
    const win = this.win;
    if (!layout || !win || win.isDestroyed()) return;
    this.cancelSnap();
    const card = this.userSize
      ? browserPiPFitCard(layout.host, this.userSize)
      : browserPiPCardSize(layout.host, browserPiPSizeForAspect(this.initialAspect ?? 4 / 3));
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
    if (this.appearance?.reducedMotion ?? (this.deps.reducedMotion ?? (() => systemPreferences.getAnimationSettings().prefersReducedMotion))()) {
      this.applyBounds({ ...origin, ...card });
      return;
    }
    const start = win.getBounds();
    const point = { x: start.x, y: start.y };
    // Match the reference PiP's damped release, without carrying full flick
    // speed into the spring. Keep fractional positions across native rounding.
    const release = Math.hypot(velocity.x, velocity.y) >= 120 ? 0.25 : 0;
    const speed = { x: velocity.x * release, y: velocity.y * release };
    let previous = Date.now();
    const frame = (): void => {
      this.snapTimer = undefined;
      const current = this.win;
      if (!current || current.isDestroyed() || this.dragging || this.resizing) return;
      const now = Date.now();
      const dt = Math.min(1 / 30, Math.max(1 / 120, (now - previous) / 1000));
      previous = now;
      speed.x += (260 * (origin.x - point.x) - 32 * speed.x) * dt;
      speed.y += (260 * (origin.y - point.y) - 32 * speed.y) * dt;
      point.x += speed.x * dt;
      point.y += speed.y * dt;
      const settled = Math.hypot(origin.x - point.x, origin.y - point.y) <= 0.5
        && Math.hypot(speed.x, speed.y) <= 2;
      const next = settled ? origin : point;
      const confined = this.screenLayout
        ? browserPiPClampOrigin(next, card, this.screenLayout.visibleFrame) : next;
      this.applyBounds({ ...confined, width: card.width, height: card.height });
      if (!settled) this.scheduleSnap(frame);
    };
    this.scheduleSnap(frame);
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

  // -------------------------------------------------------------------------
  // Overlay pushes.
  // -------------------------------------------------------------------------
  private syncOverlayMode(): void {
    this.execute(`window.wuuPipViewport?.(${JSON.stringify(this.contentRect())})`);
    this.execute(`window.wuuPipMount?.(${JSON.stringify({ mounted: this.mounted })})`);
  }

  private setCaptureStatus(status: Pick<CUANativePiPEvent, "status" | "message">): void {
    this.captureState = status;
    this.execute(`window.wuuPipCapture?.(${JSON.stringify(status)})`);
  }

  private pushActivityState(): void {
    this.execute(
      `window.wuuPipState?.(${JSON.stringify({
        kind: this.activity.kind,
        execution_id: this.activity.execution_id,
        state: this.activity.state,
        controller: this.activity.controller,
        error: this.activity.error,
        controlPending: this.controlPending,
      })})`,
    );
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
      title: "Wuu Live Activity",
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
