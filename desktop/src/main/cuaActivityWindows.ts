import { screen, type Rectangle } from "electron";
import type { ActivitySession, ServerEvent } from "../shared/protocol";
import { CUANativePiP, resolveCUAFrameHelper, type CUANativePiPEvent } from "./cuaFrameStreams";
import type { WindowRegistry } from "./windowRegistry";

export type ActivityControlAction = "takeover" | "release" | "stop";

const PIP_WIDTH = 260;
const PIP_HEIGHT = 170;
const PIP_INSET = 12;

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

export function nativePiPInitialBounds(mainBounds: Rectangle | undefined, workArea: Rectangle): Rectangle {
  if (!mainBounds) {
    return {
      x: workArea.x + workArea.width - PIP_WIDTH - 24,
      y: workArea.y + 24,
      width: PIP_WIDTH,
      height: PIP_HEIGHT,
    };
  }
  const x = Math.min(
    workArea.x + workArea.width - PIP_WIDTH - PIP_INSET,
    Math.max(workArea.x + PIP_INSET, mainBounds.x + mainBounds.width - PIP_WIDTH - PIP_INSET),
  );
  const y = Math.min(
    workArea.y + workArea.height - PIP_HEIGHT - PIP_INSET,
    Math.max(workArea.y + PIP_INSET, mainBounds.y + PIP_INSET),
  );
  return { x, y, width: PIP_WIDTH, height: PIP_HEIGHT };
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
  // The panel is already showing this page, so the floating mirror would
  // duplicate it. Foreground and user control also mean the page is meant
  // to be watched in the panel rather than in the mirror.
  if (inPanel) return false;
  if (activity.kind !== "browser") return true;
  return activity.state !== "foreground_controlled" && activity.state !== "user_controlled";
}

type ActivitySnapshot = (threadID: string) => Promise<ActivitySession[]>;

// The observation-surface contract shared by the native CUA helper and the
// Electron browser preview: lifecycle + visibility + pointer animation. The
// optional hooks let a surface reflect fresh activity state on its chrome
// (updateActivity) and freeze frame production while the activity is stopped
// (setLive) — both no-ops on surfaces that do not implement them.
export type ObservationPiPHandle = Pick<CUANativePiP, "start" | "setVisible" | "animateInteraction" | "stop"> & {
  updateActivity?(activity: ActivitySession): void;
  setLive?(live: boolean): void;
  setAppearance?(dark: boolean): void;
};

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
  phase: "preparing" | "live";
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
  private readonly dismissedAt = new Map<string, string>();
  private readonly lastInteractionRevisions = new Map<string, number>();
  private readonly retryAttempts = new Map<string, number>();
  private readonly retryTimers = new Map<string, NodeJS.Timeout>();
  private current: PiPEntry | undefined;
  private replacement: { activity: ActivitySession; key: string } | undefined;
  private replacementInFlight = false;
  private pendingStop: Promise<void> = Promise.resolve();
  private closed = false;
  private userBounds: Rectangle | undefined;
  private reconcileTimer: NodeJS.Timeout | undefined;
  private reconcileInFlight = false;
  private activeThreadID: string | undefined;
  private darkAppearance: boolean | undefined;
  private browserInPanel: ((activity: ActivitySession) => boolean) | undefined;

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
    this.scheduleReconcile();
  }

  setAppearance(dark: boolean): void {
    this.darkAppearance = dark;
    this.current?.pip.setAppearance?.(dark);
  }

  setBrowserInPanel(check: (activity: ActivitySession) => boolean): void {
    this.browserInPanel = check;
  }

  refreshBrowserPresentation(): void {
    if (!this.current) return;
    this.current.pip.setVisible(this.pipVisibility(this.current.activity));
  }

  private pipVisibility(activity: ActivitySession): boolean {
    return pipVisibleForActivity(activity, this.browserInPanel?.(activity) ?? false);
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
    if (!isObservableActivity(activity)) return;
    if (!activity.target?.trim()) return;
    const current = this.observations.get(activity.thread_id);
    if (current && (current.updated_at > activity.updated_at || (current.id === activity.id && current.state === "stopped" && activity.state !== "stopped"))) return;
    if (current && observationKey(current) !== observationKey(activity)) {
      this.retryAttempts.delete(activity.thread_id);
      const timer = this.retryTimers.get(activity.thread_id);
      if (timer) clearTimeout(timer);
      this.retryTimers.delete(activity.thread_id);
    }
    this.observations.set(activity.thread_id, activity);
    const dismissed = this.dismissedAt.get(activity.thread_id);
    if (dismissed && dismissed < activity.updated_at && activity.state !== "stopped") this.dismissedAt.delete(activity.thread_id);
    if (activity.thread_id === this.activeThreadID) this.syncActiveObservation();
  }

  // Core teardown for a workdir: drop its observations and tear down a
  // current surface showing one of its tabs, so no ghost preview can outlive
  // the core that owned the page.
  dropWorkdir(workdir: string): void {
    for (const [threadID, activity] of [...this.observations]) {
      if (activity.workdir === workdir) this.observations.delete(threadID);
    }
    if (this.replacement?.activity.workdir === workdir) this.replacement = undefined;
    if (this.current?.activity.workdir === workdir) this.stopCurrent();
  }

  shutdown(): Promise<void> {
    this.closed = true;
    this.activeThreadID = undefined;
    this.replacement = undefined;
    this.observations.clear();
    if (this.reconcileTimer) clearTimeout(this.reconcileTimer);
    for (const timer of this.retryTimers.values()) clearTimeout(timer);
    this.retryTimers.clear();
    this.stopCurrent();
    return this.pendingStop;
  }

  private syncActiveObservation(): void {
    const threadID = this.activeThreadID;
    if (!threadID || this.dismissedAt.has(threadID)) {
      this.current?.pip.setVisible(false);
      return;
    }
    const activity = this.observations.get(threadID);
    if (!activity) {
      this.current?.pip.setVisible(false);
      return;
    }
    const key = observationKey(activity);
    if (this.current?.key === key) {
      this.current.activity = activity;
      this.current.pip.setVisible(this.pipVisibility(activity));
      this.current.pip.setLive?.(activity.state !== "stopped");
      this.current.pip.updateActivity?.(activity);
      this.animateInteractionIfNew(activity, this.current.pip);
      return;
    }
    if (activity.state === "stopped" || this.retryTimers.has(threadID) || (this.retryAttempts.get(threadID) ?? 0) >= 4) return;
    if (this.replacement?.key === key) {
      this.replacement.activity = activity;
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
    this.current = { key, threadID: activity.thread_id, activity, pip, phase: "preparing" };
    pip.start();
    if (this.darkAppearance !== undefined) pip.setAppearance?.(this.darkAppearance);
    pip.setVisible(this.pipVisibility(activity));
    pip.setLive?.(activity.state !== "stopped");
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
    if (replacement.activity.thread_id !== this.activeThreadID || this.dismissedAt.has(replacement.activity.thread_id)) return;
    this.startImmediate(replacement.activity, replacement.key);
  }

  private spawnPiP(activity: ActivitySession, key: string): ObservationPiPHandle | undefined {
    const sink: ObservationPiPEventSink = {
      onEvent: (event) => this.handlePiPEvent(key, event),
      onFailure: (message) => this.handlePiPFailure(key, message),
      onGone: () => this.handlePiPGone(key),
    };
    if (this.pipFactory) return this.pipFactory(activity, key, sink, () => this.initialBounds());
    // No factory: the historical default is the native CUA helper only.
    if (activity.kind !== "cua") return undefined;
    const helper = resolveCUAFrameHelper();
    const target = activity.target?.trim();
    if (!helper || !target) return undefined;
    return new CUANativePiP(
      helper,
      activity.thread_id,
      target,
      activity.process_id,
      activity.window_id,
      this.initialBounds(),
      sink.onEvent,
      sink.onFailure,
    );
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
        this.dismissedAt.set(entry.threadID, new Date().toISOString());
        if (this.current?.key === key) this.stopCurrent();
        return;
      case "control":
        if (this.control && (event.action === "takeover" || event.action === "release" || event.action === "stop")) {
          void this.control(entry.activity, event.action)
            .then((activity) => this.update(activity))
            .catch((error: unknown) => {
              if (this.current === entry) entry.pip.updateActivity?.({ ...entry.activity, error: error instanceof Error ? error.message : String(error) });
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

  private handlePiPFailure(key: string, _message: string): void {
    const entry = this.current?.key === key ? this.current : undefined;
    if (!entry) return;
    if (this.current?.key === key) this.stopCurrent();
    if (entry.threadID !== this.activeThreadID || this.dismissedAt.has(entry.threadID)) return;
    const attempts = (this.retryAttempts.get(entry.threadID) ?? 0) + 1;
    this.retryAttempts.set(entry.threadID, attempts);
    if (attempts >= 4) return;
    const timer = setTimeout(() => {
      this.retryTimers.delete(entry.threadID);
      this.syncActiveObservation();
    }, frameStreamRetryDelay(attempts));
    timer.unref?.();
    this.retryTimers.set(entry.threadID, timer);
  }

  // The capture target disappeared (tab closed, view crashed, workdir torn
  // down). Unlike a transient stream failure this is never retried: the
  // surface comes down and the observation is forgotten so a later reconcile
  // cannot resurrect a dead target.
  private handlePiPGone(key: string): void {
    const entry = this.current?.key === key ? this.current : undefined;
    if (!entry) return;
    const observed = this.observations.get(entry.threadID);
    if (observed && observationKey(observed) === key) {
      this.observations.delete(entry.threadID);
    }
    if (this.current?.key === key) this.stopCurrent();
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
    return nativePiPInitialBounds(mainBounds, workArea);
  }
}

export function observationKey(activity: ActivitySession): string {
  // Replacement is serialized by stopCurrent; resolved identity changes must
  // rebind capture instead of silently following the old process/window.
  return [activity.thread_id, activity.id, activity.target?.trim(),
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
