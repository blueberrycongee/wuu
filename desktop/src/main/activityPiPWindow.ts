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

const COMPLETION_DISMISS_DELAY_MS = 30_000;
// A card grows out of its resting corner on a soft spring while it fades in
// and comes into focus; leaving reverses the motion slightly faster.
const APPEAR_SCALE = 0.72;
const APPEAR_SPRING = { stiffness: 185, damping: 18 };
const APPEAR_BLUR = 18;
const FADE_IN_MS = 200;
const FADE_OUT_MS = 220;
const BLUR_IN_MS = 280;
const BLUR_OUT_MS = 240;
// The first frame waits briefly for a content snapshot; without one the card
// only fades, because live content cannot scale with the window.
const SNAPSHOT_WAIT_MS = 120;

// Snapping to a corner after a drag, and a new lead taking its place.
const SNAP_SPRING = { stiffness: 260, damping: 32 };
const LEAD_SPRING = { stiffness: 320, damping: 42 };
// Trailing cards: softer at rest, stiffer while the lead is being dragged.
const FOLLOW_SPRING = { stiffness: 150, damping: 30 };
const FOLLOW_DRAG_SPRING = { stiffness: 260, damping: 32 };
const STACK_OFFSET = 22;
// Cards deeper than this share the last offset and sit fully behind.
const STACK_DEPTH = 2;

export type PiPStackLead = { frame: Rectangle; alignment: PipAlignment; dragging: boolean };

type PiPTransition = {
  direction: "in" | "out";
  // Resting frame of the card; the animated frame scales about its corner.
  target: Rectangle;
  scale: number;
  velocity: number;
  opacity: number;
  fromOpacity: number;
  scales: boolean;
  startedAt: number | undefined;
  previousAt: number;
  timer: ReturnType<typeof setTimeout> | undefined;
  done: Array<() => void>;
};

export type BrowserPiPWindowHandle = BrowserParentWindowHandle & {
  setAlwaysOnTop(flag: boolean, level?: string): void;
  setVisibleOnAllWorkspaces(visible: boolean, options?: { visibleOnFullScreen?: boolean }): void;
  showInactive(): void;
  hide(): void;
  isVisible(): boolean;
  getBounds(): Rectangle;
  setBounds(bounds: Rectangle): void;
  setOpacity?(opacity: number): void;
  setIgnoreMouseEvents?(ignore: boolean): void;
  moveTop?(): void;
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
  private completionDismissed = false;
  private completionTimer: ReturnType<typeof setTimeout> | undefined;
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
  private transition: PiPTransition | undefined;
  private motion: {
    origin: PipPoint;
    card: { width: number; height: number };
    spring: { stiffness: number; damping: number };
    point: PipPoint;
    speed: PipPoint;
    previous: number;
  } | undefined;
  // 0 leads the stack; -1 until the coordinator first places the card.
  private stackIndex = -1;
  // Shown transparent and click-through while content renders its first
  // pixels; Chromium decodes nothing for a window that was never shown.
  private preparing = false;
  private overlayReady = false;
  private placeholderTimer: ReturnType<typeof setTimeout> | undefined;
  private showPlaceholder = false;

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
        if (rawURL === "wuu-pip://close") this.deps.sink.onEvent({ event: "user_close" });
        if (rawURL === "wuu-pip://expand") this.deps.sink.onEvent({ event: "expand" });
        if (rawURL === "wuu-pip://retry") this.deps.sink.onEvent({ event: "retry" });
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
        this.overlayReady = true;
        this.syncOverlayMode();
        this.pushActivityState();
        this.setCaptureStatus(this.captureState);
        this.content?.refresh?.();
        this.setAppearance(this.dark);
        this.pushCompletion();
        this.hoverPoint = undefined;
        this.syncHover();
        this.applyVisibility();
      })
      .catch(() => undefined);
    const reportGeometry = (): void => {
      // Appearance frames are not user geometry.
      if (win.isDestroyed() || this.win !== win || this.transition) return;
      const b = win.getBounds();
      this.deps.sink.onEvent({ event: "geometry", x: b.x, y: b.y, width: b.width, height: b.height });
    };
    win.on("moved", reportGeometry);
    win.on("resize", () => {
      if (this.transition) return;
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
      this.setTurnCompleted(false);
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
    this.applyVisibility();
  }

  private applyVisibility(): void {
    const win = this.win;
    if (!win || win.isDestroyed() || this.stopped || this.awaitingVisibility) return;
    // Unknown layout (no report yet) still shows, so a card can appear before
    // the first measurement. An explicit empty layout means the column is gone.
    const columnGone = this.screenLayout === null;
    const parent = this.hostParent;
    // Showing a native child window can reveal its hidden parent on macOS.
    // Restore the preview from host visibility events, never by showing Wuu.
    const parentHidden = parent && (parent.isDestroyed() || !parent.isVisible() || parent.isMinimized());
    const shown = win.isVisible() && !this.preparing && this.transition?.direction !== "out";
    if (this.visible && !this.completionDismissed && !columnGone && !parentHidden) {
      // The nonactivating panel can start while CUA owns the foreground.
      // Host visibility, not keyboard focus, determines whether it may appear.
      this.content?.setVisible(true);
      if (this.win !== win || win.isDestroyed()) return;
      if (!shown) {
        // Give fast captures time to paint, then expose loading or failure.
        if (!this.mounted && (!this.overlayReady || !this.showPlaceholder)) {
          this.prepare();
          return;
        }
        this.present();
      }
      if (!this.hoverTimer) {
        this.syncHover();
        // Native pointer delivery can pause while another app is active.
        // Track only the visible card, without activating it or capturing frames.
        this.hoverTimer = setInterval(() => this.syncHover(), 80);
        this.hoverTimer.unref?.();
      }
    } else {
      clearTimeout(this.placeholderTimer);
      this.placeholderTimer = undefined;
      this.stopHoverTracking();
      // An unavailable column (including a foreground dialog) or hidden host
      // takes its children away immediately, without fading over the new UI.
      if (columnGone || parentHidden || !win.isVisible() || this.preparing) {
        this.endPreparing();
        this.settleTransition();
        this.content?.setVisible(false);
        if (this.win === win && !win.isDestroyed()) win.hide();
        return;
      }
      this.dismiss(() => this.content?.setVisible(false));
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

  setTurnCompleted(completed: boolean): void {
    if (this.stopped || this.completed === completed) return;
    this.completed = completed;
    if (this.completionTimer) clearTimeout(this.completionTimer);
    this.completionTimer = undefined;
    const wasDismissed = this.completionDismissed;
    this.completionDismissed = false;
    this.pushCompletion();
    if (completed) {
      this.completionTimer = setTimeout(() => {
        this.completionTimer = undefined;
        // Keep dismissal separate from requested visibility so layout and
        // activity refreshes cannot reopen a completed preview.
        this.completionDismissed = true;
        this.applyVisibility();
        this.deps.sink.onEvent({ event: "completion_expired" });
      }, COMPLETION_DISMISS_DELAY_MS);
      this.completionTimer.unref?.();
    } else if (wasDismissed && !this.awaitingVisibility) {
      this.applyVisibility();
    }
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
    const previous = this.activity;
    this.activity = activity;
    // Task cleanup and repeated snapshots must not erase a successful turn
    // completion or restart its dismissal deadline.
    if (activity.execution_id !== previous.execution_id
      || activity.state === "error" || activity.controller === "user") {
      this.setTurnCompleted(false);
    }
    this.pushActivityState();
    this.content?.updateActivity?.(activity);
  }

  retarget(activity: ActivitySession, sink: ObservationPiPEventSink): void {
    this.awaitingVisibility = true;
    clearTimeout(this.placeholderTimer);
    this.placeholderTimer = undefined;
    this.setTurnCompleted(false);
    this.cancelSnap();
    const bounds = this.win?.getBounds();
    // Provider switches preserve the user's exact card size and resting corner.
    if (bounds) this.userSize = { width: bounds.width, height: bounds.height };
    this.deps.sink = sink;
    this.activity = activity;
    this.lastInteractionRevision = 0;
    this.pendingInteraction = undefined;
    this.setCaptureStatus({ status: "healthy" });
    this.pendingRetarget = true;
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
    clearTimeout(this.placeholderTimer);
    this.placeholderTimer = undefined;
    if (this.completionTimer) clearTimeout(this.completionTimer);
    this.completionTimer = undefined;
    this.pendingRetarget = false;
    this.stopHoverTracking();
    this.detachHostParent?.();
    this.detachHostParent = undefined;
    this.cancelSnap();
    const win = this.win;
    if (win && !win.isDestroyed() && win.isVisible()) this.dismiss(() => this.stopContent());
    else this.stopContent();
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
          clearTimeout(this.placeholderTimer);
          this.placeholderTimer = undefined;
          this.raiseOverlay();
          this.applyVisibility();
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
      this.applyVisibility();
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
    // A trailing card rests relative to its lead, never at a corner of its own.
    if (!layout || !win || win.isDestroyed() || this.stackIndex > 0) return;
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
      if (this.stackIndex > 0) this.deps.sink.onEvent({ event: "promote" });
      this.settleTransition();
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
      this.settleTransition();
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

  // -------------------------------------------------------------------------
  // Appearance. The window frame scales about the resting corner and fades;
  // a snapshot of the content in the overlay carries the blur and the scaled
  // pixels, so live content never re-lays out during the motion.
  // -------------------------------------------------------------------------
  private reducedMotion(): boolean {
    return this.appearance?.reducedMotion
      ?? (this.deps.reducedMotion ?? (() => systemPreferences.getAnimationSettings().prefersReducedMotion))();
  }

  private prepare(): void {
    const win = this.win;
    if (!win || win.isDestroyed() || this.stopped) return;
    if (!this.mounted && !this.showPlaceholder && !this.placeholderTimer) {
      this.placeholderTimer = setTimeout(() => {
        this.placeholderTimer = undefined;
        this.showPlaceholder = true;
        this.applyVisibility();
      }, 1_500);
    }
    if (win.isVisible()) return;
    this.preparing = true;
    win.setOpacity?.(0);
    win.setIgnoreMouseEvents?.(true);
    win.showInactive();
    // Showing orders the window in front; the stack restores its own order.
    this.deps.sink.onEvent({ event: "shown" });
  }

  private endPreparing(): void {
    if (!this.preparing) return;
    this.preparing = false;
    this.win?.setIgnoreMouseEvents?.(false);
  }

  // Shows a hidden card, or turns a leaving one around from where it is.
  private present(): void {
    const win = this.win;
    // Mounting content re-enters visibility; an appearance already under way
    // owns the resting frame.
    if (!win || win.isDestroyed() || this.stopped || this.transition?.direction === "in") return;
    this.endPreparing();
    const leaving = this.transition?.direction === "out" ? this.transition : undefined;
    const reduced = this.reducedMotion();
    const transition = this.beginTransition("in", leaving?.target ?? win.getBounds(), {
      scale: leaving?.scale ?? (reduced ? 1 : APPEAR_SCALE),
      velocity: leaving?.velocity ?? 0,
      opacity: leaving?.opacity ?? 0,
      scales: !reduced && Boolean(leaving?.scales),
    });
    this.applyTransitionFrame(transition);
    if (!win.isVisible()) {
      win.showInactive();
      this.deps.sink.onEvent({ event: "shown" });
    }
    if (leaving || reduced) this.runTransition(transition);
    else this.withSnapshot(transition, () => this.runTransition(transition));
  }

  private dismiss(done: () => void): void {
    const win = this.win;
    if (!win || win.isDestroyed() || !win.isVisible()) {
      done();
      return;
    }
    if (this.transition?.direction === "out") {
      this.transition.done.push(done);
      return;
    }
    const appearing = this.transition;
    const reduced = this.reducedMotion();
    const transition = this.beginTransition("out", appearing?.target ?? win.getBounds(), {
      scale: appearing?.scale ?? 1,
      velocity: appearing?.velocity ?? 0,
      opacity: appearing?.opacity ?? 1,
      scales: !reduced && Boolean(appearing?.scales),
    });
    transition.done.push(done);
    if (reduced || transition.scales) this.runTransition(transition);
    else this.withSnapshot(transition, () => this.runTransition(transition));
  }

  private beginTransition(
    direction: PiPTransition["direction"],
    target: Rectangle,
    from: { scale: number; velocity: number; opacity: number; scales: boolean },
  ): PiPTransition {
    const previous = this.transition;
    if (previous?.timer) clearTimeout(previous.timer);
    const transition: PiPTransition = {
      direction, target,
      scale: from.scale, velocity: from.velocity, opacity: from.opacity, fromOpacity: from.opacity,
      scales: from.scales, startedAt: undefined, previousAt: Date.now(), timer: undefined, done: [],
    };
    this.transition = transition;
    return transition;
  }

  // Waits briefly for the content snapshot that carries scale and blur.
  private withSnapshot(transition: PiPTransition, run: () => void): void {
    let settled = false;
    const proceed = (src: string | undefined): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (this.transition !== transition) return;
      if (src) {
        transition.scales = true;
        const rect = this.contentFraction(transition.target);
        this.execute(`window.wuuPipAppear?.(${JSON.stringify({ src, rect, blur: transition.direction === "in" ? APPEAR_BLUR : 0 })})`);
      } else {
        transition.scale = 1;
        transition.velocity = 0;
        this.applyTransitionFrame(transition);
      }
      run();
    };
    const timer = setTimeout(() => proceed(undefined), SNAPSHOT_WAIT_MS);
    timer.unref?.();
    const snapshot = this.content?.snapshot?.();
    if (!snapshot) proceed(undefined);
    else void snapshot.then(proceed, () => proceed(undefined));
  }

  private runTransition(transition: PiPTransition): void {
    if (this.transition !== transition) return;
    transition.startedAt = Date.now();
    transition.previousAt = transition.startedAt;
    if (transition.scales) {
      const entering = transition.direction === "in";
      this.execute(`window.wuuPipAppear?.(${JSON.stringify({
        blur: entering ? 0 : APPEAR_BLUR,
        duration: entering ? BLUR_IN_MS : BLUR_OUT_MS,
        easing: entering ? "ease-out" : "ease-in",
      })})`);
    }
    this.scheduleTransitionFrame(transition);
  }

  private scheduleTransitionFrame(transition: PiPTransition): void {
    transition.timer = setTimeout(() => this.transitionFrame(transition), 16);
    transition.timer.unref?.();
  }

  private transitionFrame(transition: PiPTransition): void {
    transition.timer = undefined;
    if (this.transition !== transition) return;
    const win = this.win;
    if (!win || win.isDestroyed()) {
      this.finishTransition(transition);
      return;
    }
    const now = Date.now();
    const dt = Math.min(1 / 30, Math.max(1 / 120, (now - transition.previousAt) / 1000));
    transition.previousAt = now;
    const elapsed = now - (transition.startedAt ?? now);
    const entering = transition.direction === "in";
    if (transition.scales) {
      const goal = entering ? 1 : APPEAR_SCALE;
      transition.velocity += (APPEAR_SPRING.stiffness * (goal - transition.scale) - APPEAR_SPRING.damping * transition.velocity) * dt;
      transition.scale += transition.velocity * dt;
    }
    const fade = entering ? FADE_IN_MS : FADE_OUT_MS;
    const progress = Math.min(1, elapsed / fade);
    // Ease-out while arriving and ease-in while leaving.
    transition.opacity = entering
      ? transition.fromOpacity + (1 - transition.fromOpacity) * (1 - (1 - progress) ** 2)
      : transition.fromOpacity * (1 - progress ** 2);
    this.applyTransitionFrame(transition);
    const duration = Math.max(fade, transition.scales ? (entering ? BLUR_IN_MS : BLUR_OUT_MS) : 0);
    const settled = !entering || !transition.scales
      || (Math.abs(1 - transition.scale) < 0.002 && Math.abs(transition.velocity) < 0.02);
    if (elapsed >= duration && settled) this.finishTransition(transition);
    else this.scheduleTransitionFrame(transition);
  }

  private applyTransitionFrame(transition: PiPTransition): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    const { target, scale } = transition;
    const width = Math.max(1, Math.round(target.width * scale));
    const height = Math.max(1, Math.round(target.height * scale));
    const right = this.alignment.endsWith("right");
    const bottom = this.alignment.startsWith("bottom");
    win.setBounds({
      x: Math.round(right ? target.x + target.width - width : target.x),
      y: Math.round(bottom ? target.y + target.height - height : target.y),
      width,
      height,
    });
    this.overlay?.setBounds({ x: 0, y: 0, width, height });
    win.setOpacity?.(transition.opacity);
  }

  // Completes the current transition at once, at its final frame.
  private settleTransition(): void {
    if (this.transition) this.finishTransition(this.transition);
  }

  private finishTransition(transition: PiPTransition): void {
    if (this.transition !== transition) return;
    if (transition.timer) clearTimeout(transition.timer);
    this.transition = undefined;
    const win = this.win;
    if (win && !win.isDestroyed()) {
      if (transition.direction === "out") win.hide();
      win.setBounds(transition.target);
      win.setOpacity?.(1);
      this.refit();
    }
    this.execute("window.wuuPipAppear?.(null)");
    for (const done of transition.done.splice(0)) done();
  }

  // The content rectangle as fractions of a card frame, so the snapshot scales
  // with the window.
  private contentFraction(frame: Rectangle): { x: number; y: number; width: number; height: number } {
    const viewport = this.layoutViewport;
    if (!viewport || frame.width <= 0 || frame.height <= 0) return { x: 0, y: 0, width: 1, height: 1 };
    const rect = pipContainRect(viewport.width, viewport.height, frame.width, frame.height);
    return { x: rect.x / frame.width, y: rect.y / frame.height, width: rect.width / frame.width, height: rect.height / frame.height };
  }

  // A running spring takes a new target without losing its momentum, so a
  // trailing card follows a moving leader smoothly.
  private animateTo(
    origin: PipPoint,
    card: { width: number; height: number },
    velocity: PipPoint,
    spring: { stiffness: number; damping: number } = SNAP_SPRING,
  ): void {
    const win = this.win;
    if (!win || win.isDestroyed()) return;
    if (this.reducedMotion()) {
      this.cancelSnap();
      this.applyBounds({ ...origin, ...card });
      return;
    }
    if (this.motion && this.snapTimer !== undefined) {
      Object.assign(this.motion, { origin, card, spring });
      return;
    }
    this.cancelSnap();
    const start = win.getBounds();
    // Match the reference PiP's damped release, without carrying full flick
    // speed into the spring. Keep fractional positions across native rounding.
    const release = Math.hypot(velocity.x, velocity.y) >= 120 ? 0.25 : 0;
    const motion = {
      origin, card, spring,
      point: { x: start.x, y: start.y },
      speed: { x: velocity.x * release, y: velocity.y * release },
      previous: Date.now(),
    };
    this.motion = motion;
    const frame = (): void => {
      this.snapTimer = undefined;
      const current = this.win;
      if (this.motion !== motion || !current || current.isDestroyed() || this.dragging || this.resizing) return;
      const now = Date.now();
      const dt = Math.min(1 / 30, Math.max(1 / 120, (now - motion.previous) / 1000));
      motion.previous = now;
      const { point, speed } = motion;
      speed.x += (motion.spring.stiffness * (motion.origin.x - point.x) - motion.spring.damping * speed.x) * dt;
      speed.y += (motion.spring.stiffness * (motion.origin.y - point.y) - motion.spring.damping * speed.y) * dt;
      point.x += speed.x * dt;
      point.y += speed.y * dt;
      const settled = Math.hypot(motion.origin.x - point.x, motion.origin.y - point.y) <= 0.5
        && Math.hypot(speed.x, speed.y) <= 2;
      const next = settled ? motion.origin : point;
      const confined = this.screenLayout
        ? browserPiPClampOrigin(next, motion.card, this.screenLayout.visibleFrame) : next;
      this.applyBounds({ ...confined, width: motion.card.width, height: motion.card.height });
      if (settled) this.motion = undefined;
      else this.scheduleSnap(frame);
    };
    this.scheduleSnap(frame);
  }

  // -------------------------------------------------------------------------
  // Stack. Several live tasks share one corner: the lead card rests at the
  // committed corner and the others rest behind it, each offset toward the
  // column so its edge stays visible.
  // -------------------------------------------------------------------------
  // A new lead takes over the corner of the card it replaces: the committed
  // corner of the column, or without a measured column that card's frame.
  setStackPosition(index: number, lead?: PiPStackLead): void {
    const wasLead = this.stackIndex === 0;
    this.stackIndex = index;
    const win = this.win;
    if (index === 0) {
      if (wasLead || !win || win.isDestroyed()) return;
      if (lead) this.alignment = lead.alignment;
      if (this.screenLayout || !lead) {
        this.placeCommitted();
        return;
      }
      const card = this.restingFrame();
      const origin = {
        x: lead.alignment.endsWith("right") ? lead.frame.x + lead.frame.width - card.width : lead.frame.x,
        y: lead.alignment.startsWith("bottom") ? lead.frame.y + lead.frame.height - card.height : lead.frame.y,
      };
      if (!win.isVisible() || this.transition) this.applyBounds({ ...origin, width: card.width, height: card.height });
      else this.animateTo(origin, { width: card.width, height: card.height }, { x: 0, y: 0 }, LEAD_SPRING);
      return;
    }
    if (!lead || !win || win.isDestroyed()) return;
    this.alignment = lead.alignment;
    const resting = this.restingFrame();
    const card = { width: resting.width, height: resting.height };
    const offset = STACK_OFFSET * Math.min(index, STACK_DEPTH);
    const right = lead.alignment.endsWith("right");
    const bottom = lead.alignment.startsWith("bottom");
    const origin = {
      x: right ? lead.frame.x + lead.frame.width - card.width : lead.frame.x,
      y: bottom ? lead.frame.y + lead.frame.height - card.height - offset : lead.frame.y + offset,
    };
    if (!win.isVisible() || this.transition) {
      this.applyBounds({ ...origin, ...card });
      return;
    }
    // Cards farther back trail on softer springs.
    const base = lead.dragging ? FOLLOW_DRAG_SPRING : FOLLOW_SPRING;
    this.animateTo(origin, card, { x: 0, y: 0 }, {
      stiffness: base.stiffness / (1 + 0.18 * index),
      damping: base.damping / (1 + 0.08 * index),
    });
  }

  stackLead(): PiPStackLead | undefined {
    const win = this.win;
    if (!win || win.isDestroyed()) return undefined;
    return { frame: this.restingFrame(), alignment: this.alignment, dragging: this.dragging };
  }

  // Where the card is headed: a running spring's target, an appearance's
  // resting frame, or its current frame. A drag follows the pointer.
  private restingFrame(): Rectangle {
    const bounds = this.win!.getBounds();
    if (this.motion && !this.dragging) return { ...this.motion.origin, ...this.motion.card };
    return this.transition?.target ?? bounds;
  }

  raise(): void {
    const win = this.win;
    if (win && !win.isDestroyed() && win.isVisible()) win.moveTop?.();
  }

  private scheduleSnap(frame: () => void): void {
    const timer = setTimeout(frame, 16);
    timer.unref?.();
    this.snapTimer = timer;
  }

  private cancelSnap(): void {
    this.motion = undefined;
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
    // A layout change during an appearance moves its resting frame; the next
    // animation frame scales about the new corner.
    if (this.transition) {
      this.transition.target = next;
      return;
    }
    const current = win.getBounds();
    if (current.x === next.x && current.y === next.y && current.width === next.width && current.height === next.height) return;
    win.setBounds(next);
    if (current.width !== next.width || current.height !== next.height) this.refit();
    // Trailing cards follow every frame of their lead.
    if (this.stackIndex === 0) this.deps.sink.onEvent({ event: "geometry", ...next });
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
    if (status.status === "error" || status.status === "blank" || status.status === "suspended") {
      this.showPlaceholder = true;
      clearTimeout(this.placeholderTimer);
      this.placeholderTimer = undefined;
      this.applyVisibility();
    }
  }

  private pushActivityState(): void {
    this.execute(
      `window.wuuPipState?.(${JSON.stringify({
        kind: this.activity.kind,
        state: this.activity.state,
        controller: this.activity.controller,
      })})`,
    );
  }

  private forwardInteraction(hint: BrowserInteractionHint): Promise<unknown> | undefined {
    if (hint.kind !== "clear") this.setTurnCompleted(false);
    if (hint.kind !== "clear" && (!this.mounted || !this.win?.isVisible())) return;
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
