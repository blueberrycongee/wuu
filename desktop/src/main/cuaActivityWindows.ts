import { screen, type Rectangle } from "electron";
import type { ActivitySession, ServerEvent } from "../shared/protocol";
import {
  browserPiPScreenRect,
  PIP_ANCHOR_MARGIN,
  PIP_CARD_SIZE,
  type BrowserPiPScreenLayout,
  type PipRect,
} from "./browserPiPPlacement";
import type { CUANativePiPEvent } from "./cuaFrameStreams";
import type { ActivityPreviewAppearance } from "../shared/activityPreview";
import { activityTimestamp } from "../shared/activityTimestamp";
import type { WindowRegistry } from "./windowRegistry";
import { activityPreviewIsLive } from "./activityPiPContent";

export type ActivityControlAction = "takeover" | "release" | "stop";

export function activityControlMethod(action: ActivityControlAction): "activity/takeover" | "activity/release" | "activity/stop" {
  switch (action) {
    case "takeover": return "activity/takeover";
    case "release": return "activity/release";
    case "stop": return "activity/stop";
  }
}

export function frameStreamRetryDelay(attempt: number): number {
  return Math.min(16_000, 1_000 * 2 ** Math.max(1, attempt));
}

export function activityVisibleForThread(activityThreadID: string, activeThreadID?: string): boolean {
  return Boolean(activeThreadID && activityThreadID === activeThreadID);
}

// Fallback before the conversation column is measured. The live card then
// moves to a corner of that column; this only covers the first frame.
export function browserPiPInitialBounds(mainBounds: Rectangle | undefined, workArea: Rectangle): Rectangle {
  const width = PIP_CARD_SIZE.width;
  const height = PIP_CARD_SIZE.height;
  const inset = PIP_ANCHOR_MARGIN;
  if (!mainBounds) {
    return {
      x: workArea.x + workArea.width - width - inset,
      y: workArea.y + workArea.height - height - inset,
      width,
      height,
    };
  }
  const x = Math.min(
    workArea.x + workArea.width - width - inset,
    Math.max(workArea.x + inset, mainBounds.x + mainBounds.width - width - inset),
  );
  const y = Math.min(
    workArea.y + workArea.height - height - inset,
    Math.max(workArea.y + inset, mainBounds.y + mainBounds.height - height - inset),
  );
  return { x, y, width, height };
}

// Native preview needs a resolved process, regardless of which extension owns it.
export function isObservableActivity(activity: ActivitySession): boolean {
  if (activity.kind === "browser") return true;
  return activity.kind === "cua" && (activity.process_id ?? 0) > 0;
}

// While the user watches the real page full-size (browser visibility takeover
// in the workspace panel), mirroring the same page into the PiP is redundant —
// hide the surface but keep the observation so agent control restores it.
// CUA surfaces stay up: there the user controls the target app itself, not a
// Wuu panel showing the same pixels.
export function pipVisibleForActivity(activity: ActivitySession, inPanel = false): boolean {
  // Ordinary browser actions report background control even after an explicit
  // visibility promotion. That execution state must not hide the watch-only
  // preview; only presenting the same page in the panel replaces it.
  if (activity.kind === "browser") return !inPanel;
  return true;
}

type ActivitySnapshot = (threadID: string) => Promise<ActivitySession[]>;

// The core owns one observation host. Provider adapters only attach content;
// task control, visibility, theme and interaction feedback stay in this contract.
export type ObservationPiPHandle = {
  start(): void;
  setVisible(visible: boolean): void;
  animateInteraction(interaction: NonNullable<ActivitySession["interaction"]>): void;
  stop(onStopped?: () => void): void;
  updateActivity?(activity: ActivitySession): void;
  setLive?(live: boolean): void;
  setTurnCompleted?(completed: boolean): void;
  setAppearance?(dark: boolean, preferences?: ActivityPreviewAppearance): void;
  setControlPending?(pending: boolean): void;
  setControlFailed?(): void;
  suspendContent?(message: string): void;
  setHostLayout?(layout: BrowserPiPScreenLayout | null): void;
  setHostParent?(parent: BrowserPiPHostWindow | null): void;
  // Content changes never replace the shared host window.
  retarget?(activity: ActivitySession, sink: ObservationPiPEventSink): void;
};

export interface BrowserPiPHostWindow {
  isDestroyed(): boolean;
  isVisible(): boolean;
  isMinimized(): boolean;
  getContentBounds(): Rectangle;
  readonly webContents: { getZoomFactor(): number };
  on(event: "move" | "resize" | "show" | "hide" | "minimize" | "restore" | "focus", listener: () => void): void;
  removeListener(event: "move" | "resize" | "show" | "hide" | "minimize" | "restore" | "focus", listener: () => void): void;
  isFocused(): boolean;
}

// Surface→coordinator reporting. onEvent carries ready/user_close/geometry
// style events; onFailure is a retryable stream failure; onGone means the
// capture target disappeared for good (tab closed, view crashed) and must not
// be retried.
export type ObservationPiPEventSink = {
  onEvent(event: CUANativePiPEvent): void;
  onFailure(message: string): void;
  onGone(): void;
};

export type ObservationPiPFactory = (
  activity: ActivitySession,
  key: string,
  sink: ObservationPiPEventSink,
  bounds: () => Rectangle,
) => ObservationPiPHandle | undefined;

type PiPEntry = {
  key: string;
  threadID: string;
  activity: ActivitySession;
  pip: ObservationPiPHandle;
  // "preparing" = frosted placeholder shown, no live frame yet.
  // "live" = first real frame presented (native `ready`).
  phase: "preparing" | "live" | "failed";
  controlPending?: boolean;
  retiredExecutions: Set<string>;
};

/**
 * Owns the single user-visible observation surface across activity kinds
 * (CUA windows, embedded-browser tabs). Activity events update the target,
 * but an individual action ending never owns the surface lifetime — the
 * surface follows the latest observable target of the active thread, one at
 * a time, no matter which backend produces it.
 */
export class ObservationCoordinator {
  private readonly observations = new Map<string, ActivitySession>();
  private readonly goneObservations = new Map<string, { key: string; executionID?: string }>();
  private readonly dismissedThreads = new Set<string>();
  private readonly lastInteractionRevisions = new Map<string, number>();
  private readonly retryAttempts = new Map<string, number>();
  private readonly retryTimers = new Map<string, NodeJS.Timeout>();
  private current: PiPEntry | undefined;
  private replacement: { activity: ActivitySession; key: string } | undefined;
  private replacementInFlight = false;
  private pendingStop: Promise<void> = Promise.resolve();
  private closed = false;
  private userBounds: Rectangle | undefined;
  private browserHost: {
    id: number;
    client: { host: PipRect; obstacles: PipRect[] };
    contentSize: { width: number; height: number };
    window: BrowserPiPHostWindow;
    detach: () => void;
  } | undefined;
  private browserScreenLayout: BrowserPiPScreenLayout | null | undefined;
  private reconcileTimer: NodeJS.Timeout | undefined;
  private reconcileInFlight = false;
  private activeThreadID: string | undefined;
  private darkAppearance: boolean | undefined;
  private appearance: ActivityPreviewAppearance | undefined;
  private autoShow = true;
  private readonly explicitlyShown = new Set<string>();
  private browserInPanel: ((activity: ActivitySession) => boolean) | undefined;
  private onBrowserExpand: ((activity: ActivitySession) => void) | undefined;

  constructor(
    private readonly registry: WindowRegistry,
    private readonly snapshot?: ActivitySnapshot,
    private readonly pipFactory?: ObservationPiPFactory,
    private readonly control?: (activity: ActivitySession, action: ActivityControlAction) => Promise<ActivitySession>,
  ) {}

  handleServerEvent(event: ServerEvent): void {
    const activity = observationActivityFromServerEvent(event);
    if (activity) {
      this.update(activity);
      return;
    }
    const current = this.current;
    if (event.kind === "notification" && current
      && event.workdir === current.activity.workdir) {
      const { method, params } = event.message;
      const turn = params as { thread_id?: string; turn?: { status: string }; awaiting_auto_continuation?: boolean; truncated?: boolean } | undefined;
      if (turn?.thread_id === current.threadID) {
        if (method === "turn/started" || method === "turn/error") {
          current.pip.setTurnCompleted?.(false);
        } else if (method === "turn/completed" && this.pipVisibility(current.activity)
          && !this.dismissedThreads.has(current.threadID)) {
          // Stopping a browser activity is not evidence that its task succeeded.
          current.pip.setTurnCompleted?.(turn.turn?.status === "completed"
            && !turn.awaiting_auto_continuation && !turn.truncated);
        }
      }
    }
    this.scheduleReconcile();
  }

  setAppearance(dark: boolean, preferences?: ActivityPreviewAppearance): void {
    this.darkAppearance = dark;
    if (preferences) this.appearance = preferences;
    this.current?.pip.setAppearance?.(dark, this.appearance);
  }

  setAutoShow(enabled: boolean): void {
    this.autoShow = enabled;
    this.syncActiveObservation();
  }

  showForThread(threadID: string): void {
    this.dismissedThreads.delete(threadID);
    this.goneObservations.delete(threadID);
    this.explicitlyShown.add(threadID);
    this.resetRetryBudget(threadID);
    if (threadID === this.activeThreadID) this.syncActiveObservation();
  }

  setBrowserInPanel(check: (activity: ActivitySession) => boolean): void {
    this.browserInPanel = check;
  }

  setBrowserExpandHandler(handler: (activity: ActivitySession) => void): void {
    this.onBrowserExpand = handler;
  }

  refreshBrowserPresentation(): void {
    if (!this.current) return;
    this.current.pip.setVisible(this.pipVisibility(this.current.activity));
  }

  private pipVisibility(activity: ActivitySession): boolean {
    return activityVisibleForThread(activity.thread_id, this.activeThreadID)
      && !this.dismissedThreads.has(activity.thread_id)
      && (this.autoShow || this.explicitlyShown.has(activity.thread_id))
      && pipVisibleForActivity(activity, this.browserInPanel?.(activity) ?? false);
  }

  setActiveThread(threadID?: string): void {
    if (this.closed) return;
    this.activeThreadID = threadID?.trim() || undefined;
    if (this.current?.threadID !== this.activeThreadID) this.current?.pip.setVisible(false);
    if (this.replacement?.activity.thread_id !== this.activeThreadID) this.replacement = undefined;
    this.syncActiveObservation();
    this.scheduleReconcile(0);
  }

  update(activity: ActivitySession): void {
    if (this.closed) return;
    const retained = this.current;
    if (retained?.threadID === activity.thread_id && activity.execution_id
      && retained.retiredExecutions.has(activity.execution_id)) return;
    if (retained?.threadID === activity.thread_id && retained.activity.id === activity.id
      && acceptsActivityUpdate(retained.activity, activity)) {
      // A hidden or inactive host still owns capture resources. Deliver task
      // termination/takeover so native staging is restored, without showing or
      // retargeting the card. An unresolved/new target cannot resume old pixels.
      if (retained.activity.execution_id !== activity.execution_id) {
        if (retained.activity.execution_id) retained.retiredExecutions.add(retained.activity.execution_id);
        retained.controlPending = false;
        retained.pip.setControlPending?.(false);
        retained.pip.setTurnCompleted?.(false);
      }
      const sameTarget = retained.key === observationKey(activity);
      const live = sameTarget && isObservableActivity(activity) && activityPreviewIsLive(activity);
      if (!live) retained.pip.setLive?.(false);
      retained.pip.updateActivity?.(activity);
      if (live) retained.pip.setLive?.(true);
      retained.activity = activity;
    }
    if (!isObservableActivity(activity)) return;
    if (!activity.target?.trim()) return;
    const current = this.observations.get(activity.thread_id);
    if (current && !acceptsActivityUpdate(current, activity)) return;
    const targetChanged = current && observationKey(current) !== observationKey(activity);
    if (targetChanged) {
      // Task-wide stop/takeover also updates inactive providers. Those terminal
      // notifications must not move the preview back to an earlier target.
      if (current.execution_id === activity.execution_id && activity.controller !== "agent") return;
    }
    if (targetChanged || (current && activity.execution_id && current.execution_id !== activity.execution_id)) {
      this.resetRetryBudget(activity.thread_id);
    }
    this.observations.set(activity.thread_id, activity);
    if (!this.observationIsGone(activity)) this.goneObservations.delete(activity.thread_id);
    if (activity.thread_id === this.activeThreadID) this.syncActiveObservation();
  }

  // Core teardown for a workdir: drop its observations and tear down a
  // current surface showing one of its tabs, so no ghost preview can outlive
  // the core that owned the page.
  dropWorkdir(workdir: string): void {
    for (const [threadID, activity] of [...this.observations]) {
      if (activity.workdir === workdir) {
        this.observations.delete(threadID);
        this.goneObservations.delete(threadID);
      }
    }
    if (this.replacement?.activity.workdir === workdir) this.replacement = undefined;
    if (this.current?.activity.workdir === workdir) this.stopCurrent();
  }

  shutdown(): Promise<void> {
    this.closed = true;
    this.activeThreadID = undefined;
    this.replacement = undefined;
    this.observations.clear();
    this.goneObservations.clear();
    this.browserHost?.detach();
    this.browserHost = undefined;
    if (this.reconcileTimer) clearTimeout(this.reconcileTimer);
    for (const timer of this.retryTimers.values()) clearTimeout(timer);
    this.retryTimers.clear();
    this.stopCurrent();
    return this.pendingStop;
  }

  private syncActiveObservation(): void {
    const threadID = this.activeThreadID;
    if (!threadID || this.dismissedThreads.has(threadID) || (!this.autoShow && !this.explicitlyShown.has(threadID))) {
      this.current?.pip.setVisible(false);
      return;
    }
    const activity = this.observations.get(threadID);
    if (!activity || this.observationIsGone(activity)) {
      this.current?.pip.setVisible(false);
      return;
    }
    const key = observationKey(activity);
    if (this.current?.key === key) {
      if (this.current.phase === "failed") {
        if (this.retryTimers.has(threadID) || (this.retryAttempts.get(threadID) ?? 0) >= 4) return;
        this.current.phase = "preparing";
        const entry = this.current;
        entry.pip.retarget?.(activity, this.eventSink(key));
        if (this.current !== entry) return;
      }
      if (this.current.activity.execution_id !== activity.execution_id) {
        this.current.controlPending = false;
        this.current.pip.setControlPending?.(false);
        this.current.pip.setTurnCompleted?.(false);
      }
      this.current.activity = activity;
      this.current.pip.setVisible(this.pipVisibility(activity));
      this.current.pip.setLive?.(activityPreviewIsLive(activity));
      this.current.pip.updateActivity?.(activity);
      this.animateInteractionIfNew(activity, this.current.pip);
      return;
    }
    if ((activity.state === "stopped" && !this.explicitlyShown.has(threadID)) || this.retryTimers.has(threadID) || (this.retryAttempts.get(threadID) ?? 0) >= 4) return;
    if (this.replacement?.key === key) {
      this.replacement.activity = activity;
      return;
    }
    const current = this.current;
    if (current?.pip.retarget) {
      if (current.threadID !== activity.thread_id) current.retiredExecutions.clear();
      else if (current.activity.execution_id && current.activity.execution_id !== activity.execution_id) {
        current.retiredExecutions.add(current.activity.execution_id);
      }
      current.key = key;
      current.threadID = activity.thread_id;
      current.controlPending = false;
      current.activity = activity;
      current.phase = "preparing";
      current.pip.retarget(activity, this.eventSink(key));
      if (this.current !== current) return;
      current.pip.setVisible(this.pipVisibility(activity));
      current.pip.setLive?.(activityPreviewIsLive(activity));
      this.animateInteractionIfNew(activity, current.pip);
      return;
    }
    if (this.current || this.replacementInFlight) {
      this.replaceCurrent(activity, key);
      return;
    }
    this.startImmediate(activity, key);
  }

  private startImmediate(activity: ActivitySession, key: string): void {
    const pip = this.spawnPiP(activity, key);
    if (!pip) return;
    this.current = { key, threadID: activity.thread_id, activity, pip, phase: "preparing", retiredExecutions: new Set() };
    pip.start();
    if (this.darkAppearance !== undefined) pip.setAppearance?.(this.darkAppearance, this.appearance);
    this.pushBrowserHost(pip);
    pip.setVisible(this.pipVisibility(activity));
    pip.setLive?.(activityPreviewIsLive(activity));
    pip.updateActivity?.(activity);
    this.animateInteractionIfNew(activity, pip);
  }

  private replaceCurrent(activity: ActivitySession, key: string): void {
    this.replacement = { activity, key };
    if (this.replacementInFlight) return;
    if (!this.current) {
      this.startReplacement();
      return;
    }
    this.stopCurrent();
  }

  private stopCurrent(): void {
    if (this.replacementInFlight) return;
    const outgoing = this.current;
    if (!outgoing) return;
    // replayd groups capture clients by executable path. Two overlapping PiP
    // helpers therefore interrupt one another even when they stream different
    // apps. Every stop path shares this lock so a user close or stream failure
    // cannot let a replacement start before the old process has closed.
    this.replacementInFlight = true;
    this.current = undefined;
    this.pendingStop = new Promise<void>((resolve) => {
      outgoing.pip.stop(() => {
        this.replacementInFlight = false;
        this.startReplacement();
        resolve();
      });
    });
  }

  private startReplacement(): void {
    const replacement = this.replacement;
    this.replacement = undefined;
    if (!replacement) return;
    if (replacement.activity.thread_id !== this.activeThreadID || this.dismissedThreads.has(replacement.activity.thread_id)) return;
    this.startImmediate(replacement.activity, replacement.key);
  }

  private spawnPiP(activity: ActivitySession, key: string): ObservationPiPHandle | undefined {
    const sink = this.eventSink(key);
    if (this.pipFactory) return this.pipFactory(activity, key, sink, () => this.initialBounds());
    return undefined;
  }

  private eventSink(key: string): ObservationPiPEventSink {
    return {
      onEvent: (event) => this.handlePiPEvent(key, event),
      onFailure: (message) => this.handlePiPFailure(key, message),
      onGone: () => this.handlePiPGone(key),
    };
  }

  private handlePiPEvent(key: string, event: CUANativePiPEvent): void {
    const entry = this.current?.key === key ? this.current : undefined;
    if (!entry) return;
    switch (event.event) {
      case "ready":
        // A placeholder-first current reached its first live frame; the
        // surface itself already cross-faded from the frosted placeholder.
        entry.phase = "live";
        this.retryAttempts.delete(entry.threadID);
        return;
      case "user_close":
        this.dismissedThreads.add(entry.threadID);
        this.explicitlyShown.delete(entry.threadID);
        entry.pip.setVisible(false);
        return;
      case "expand":
        if (entry.activity.kind === "browser") this.onBrowserExpand?.(entry.activity);
        return;
      case "control":
        if (event.execution_id !== undefined && event.execution_id !== (entry.activity.execution_id ?? "")) return;
        if (this.control && !entry.controlPending && (event.action === "takeover" || event.action === "release" || event.action === "stop")) {
          const controlActivity = entry.activity;
          const executionID = controlActivity.execution_id;
          entry.controlPending = true;
          entry.pip.setControlPending?.(true);
          void this.control(controlActivity, event.action)
            .then((activity) => this.update(activity))
            .catch((error: unknown) => {
              console.warn("Activity preview control failed", error);
              if (this.current === entry && entry.key === key && entry.activity.execution_id === executionID
                && entry.activity.state !== "stopped" && entry.activity.controller === controlActivity.controller) entry.pip.setControlFailed?.();
            })
            .finally(() => {
              if (this.current !== entry || entry.key !== key || entry.activity.execution_id !== executionID) return;
              entry.controlPending = false;
              entry.pip.setControlPending?.(false);
            });
        }
        return;
      case "gone":
        this.handlePiPGone(key);
        return;
      case "user_input":
        return;
      case "geometry":
        if ([event.x, event.y, event.width, event.height].every((value) => typeof value === "number")) {
          this.userBounds = { x: event.x!, y: event.y!, width: event.width!, height: event.height! };
        }
        return;
      case "capture_status":
        return;
    }
  }

  private handlePiPFailure(key: string, message: string): void {
    const entry = this.current?.key === key ? this.current : undefined;
    if (!entry) return;
    if (entry.pip.suspendContent) {
      entry.phase = "failed";
      entry.pip.suspendContent(message);
    } else this.stopCurrent();
    const threadID = entry.threadID;
    if (threadID !== this.activeThreadID || this.dismissedThreads.has(threadID)) return;
    const attempts = (this.retryAttempts.get(threadID) ?? 0) + 1;
    this.retryAttempts.set(threadID, attempts);
    if (attempts >= 4) return;
    const timer = setTimeout(() => {
      if (this.retryTimers.get(threadID) !== timer) return;
      this.retryTimers.delete(threadID);
      if (this.activeThreadID === threadID) this.syncActiveObservation();
    }, frameStreamRetryDelay(attempts));
    timer.unref?.();
    this.retryTimers.set(threadID, timer);
  }

  private resetRetryBudget(threadID: string): void {
    this.retryAttempts.delete(threadID);
    const timer = this.retryTimers.get(threadID);
    if (timer) clearTimeout(timer);
    this.retryTimers.delete(threadID);
  }

  // The capture target disappeared (tab closed, view crashed, workdir torn
  // down). Retain its metadata as the ordering high-water mark, but fence this
  // exact source until a different target/execution or an explicit user retry.
  // A registry snapshot cannot establish that a disappeared target is back.
  private handlePiPGone(key: string): void {
    const entry = this.current?.key === key ? this.current : undefined;
    if (!entry) return;
    const observed = this.observations.get(entry.threadID);
    if (observed && observationKey(observed) === key) {
      this.goneObservations.set(entry.threadID, { key, executionID: observed.execution_id });
    }
    this.resetRetryBudget(entry.threadID);
    if (this.current?.key === key) this.stopCurrent();
  }

  private observationIsGone(activity: ActivitySession): boolean {
    const gone = this.goneObservations.get(activity.thread_id);
    return gone?.key === observationKey(activity) && gone.executionID === activity.execution_id;
  }

  private animateInteractionIfNew(activity: ActivitySession, pip: ObservationPiPHandle): void {
    const interaction = activity.interaction;
    if (!interaction) return;
    const previous = this.lastInteractionRevisions.get(activity.thread_id);
    if (previous !== undefined && interaction.revision <= previous) return;
    this.lastInteractionRevisions.set(activity.thread_id, interaction.revision);
    pip.animateInteraction(interaction);
  }

  private scheduleReconcile(delay = 150): void {
    if (!this.snapshot || !this.activeThreadID || this.reconcileInFlight || this.reconcileTimer) return;
    this.reconcileTimer = setTimeout(() => {
      this.reconcileTimer = undefined;
      const threadID = this.activeThreadID;
      if (!threadID) return;
      this.reconcileInFlight = true;
      void this.snapshot?.(threadID)
        .then((activities) => {
          if (this.activeThreadID !== threadID) return;
          for (const activity of activities) this.update(activity);
        })
        .catch(() => undefined)
        .finally(() => { this.reconcileInFlight = false; });
    }, delay);
    this.reconcileTimer.unref?.();
  }

  // Client rectangles are viewport coordinates from the conversation window.
  // Reproject moves immediately. Resizes need fresh renderer measurements;
  // projecting the previous column size first makes the card jump backwards.
  setBrowserPiPHostLayout(
    webContentsId: number,
    hostWindow: BrowserPiPHostWindow,
    client: { host: PipRect; obstacles: PipRect[] } | null,
  ): void {
    if (
      client &&
      this.browserHost &&
      this.browserHost.id !== webContentsId &&
      !this.browserHost.window.isDestroyed() &&
      !hostWindow.isFocused()
    ) {
      return;
    }
    if (!client) {
      if (this.browserHost?.id !== webContentsId) return;
      this.browserHost.detach();
      this.browserHost = undefined;
      this.browserScreenLayout = null;
      this.current?.pip.setHostLayout?.(null);
      return;
    }
    if (!this.browserHost || this.browserHost.id !== webContentsId || this.browserHost.window !== hostWindow) {
      this.browserHost?.detach();
      const reproject = (): void => {
        const source = this.browserHost;
        if (!source || source.window.isDestroyed()) return;
        const content = source.window.getContentBounds();
        // Dragging the top/left edge can emit move and resize together.
        if (content.width !== source.contentSize.width || content.height !== source.contentSize.height) return;
        this.browserScreenLayout = this.projectBrowserHost();
        this.pushBrowserHost(this.current?.pip);
      };
      hostWindow.on("move", reproject);
      this.browserHost = {
        id: webContentsId,
        client,
        contentSize: hostWindow.getContentBounds(),
        window: hostWindow,
        detach: () => {
          try {
            hostWindow.removeListener("move", reproject);
          } catch {
            // The window can already be destroyed.
          }
        },
      };
    } else {
      this.browserHost.client = client;
      this.browserHost.contentSize = hostWindow.getContentBounds();
    }
    this.browserScreenLayout = this.projectBrowserHost();
    this.pushBrowserHost(this.current?.pip);
  }

  private projectBrowserHost(): BrowserPiPScreenLayout | null {
    const source = this.browserHost;
    if (!source || source.window.isDestroyed()) return null;
    const content = source.window.getContentBounds();
    const zoom = source.window.webContents.getZoomFactor();
    let visibleFrame: Rectangle;
    try {
      visibleFrame = screen.getDisplayMatching(content).workArea;
    } catch {
      visibleFrame = { x: content.x, y: content.y, width: content.width, height: content.height };
    }
    return {
      host: browserPiPScreenRect(source.client.host, content, zoom),
      obstacles: source.client.obstacles.map((obstacle) => browserPiPScreenRect(obstacle, content, zoom)),
      visibleFrame,
    };
  }

  private pushBrowserHost(pip: ObservationPiPHandle | undefined): void {
    if (!pip?.setHostLayout || this.browserScreenLayout === undefined) return;
    pip.setHostParent?.(this.browserHost && !this.browserHost.window.isDestroyed() ? this.browserHost.window : null);
    pip.setHostLayout(this.browserScreenLayout);
  }

  private initialBounds(): Rectangle {
    if (this.userBounds) return this.userBounds;
    const mainWindow = this.registry.mainWindow();
    const mainBounds = mainWindow && !mainWindow.isDestroyed() && mainWindow.isVisible()
      ? mainWindow.getContentBounds()
      : undefined;
    const cursor = screen.getCursorScreenPoint();
    const workArea = mainBounds
      ? screen.getDisplayMatching(mainBounds).workArea
      : screen.getDisplayNearestPoint(cursor).workArea;
    return browserPiPInitialBounds(mainBounds, workArea);
  }
}

export function observationKey(activity: ActivitySession): string {
  // Execution identity fences controls separately; resolved target changes
  // retarget content without replacing the user's observation window.
  return [activity.thread_id, activity.id, activity.kind, activity.target?.trim(),
    ...(activity.kind === "cua" ? [activity.process_id ?? 0, activity.window_id ?? 0] : [])].join(":");
}

export function observationActivityFromServerEvent(event: ServerEvent): ActivitySession | undefined {
  if (event.kind !== "notification") return undefined;
  const method = event.message.method;
  if (method !== "activity/started" && method !== "activity/updated" && method !== "activity/control_changed" && method !== "activity/stopped") return undefined;
  const value = event.message.params;
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const activity = value as Partial<ActivitySession>;
  if ((activity.kind !== "cua" && activity.kind !== "browser") || typeof activity.id !== "string" || typeof activity.thread_id !== "string") return undefined;
  return value as ActivitySession;
}

function acceptsActivityUpdate(previous: ActivitySession, next: ActivitySession): boolean {
  return activityTimestamp(previous.updated_at) <= activityTimestamp(next.updated_at)
    && !(previous.id === next.id && previous.execution_id === next.execution_id
      && previous.state === "stopped" && next.state !== "stopped");
}
