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
import type { PiPStackLead } from "./activityPiPWindow";

export function frameStreamRetryDelay(attempt: number): number {
  return Math.min(16_000, 1_000 * 2 ** Math.max(1, attempt));
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

const VISIBLE_CARD_LIMIT = 5;

// The core owns one observation card per target. Provider adapters only attach
// content; visibility, theme and interaction feedback stay in this contract.
export type ObservationPiPHandle = {
  start(): void;
  setVisible(visible: boolean, immediately?: boolean): void;
  animateInteraction(interaction: NonNullable<ActivitySession["interaction"]>): void;
  stop(onStopped?: () => void): void;
  updateActivity?(activity: ActivitySession, restoreTarget?: boolean): void;
  setLive?(live: boolean): void;
  setTurnCompleted?(completed: boolean): void;
  setAppearance?(dark: boolean, preferences?: ActivityPreviewAppearance): void;
  suspendContent?(message: string): void;
  setHostLayout?(layout: BrowserPiPScreenLayout | null): void;
  setHostParent?(parent: BrowserPiPHostWindow | null): void;
  // Stack placement: index 0 leads at its corner; others rest behind the lead.
  setStackPosition?(index: number, lead?: PiPStackLead): void;
  stackLead?(): PiPStackLead | undefined;
  raise?(): void;
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
  canRestoreTarget?(): boolean;
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
  // "preparing" = content starting, no live frame yet.
  // "live" = first real frame presented (native `ready`).
  phase: "preparing" | "live" | "failed";
};

/**
 * Owns the observation cards of every thread with a live, observable task
 * (CUA windows, embedded-browser tabs). Targets retain independent cards while
 * task authority remains with their activity. Only the active conversation
 * presents its stack; other tasks continue while their cards are hidden.
 */
export class ObservationCoordinator {
  private readonly observations = new Map<string, ActivitySession>();
  private readonly goneObservations = new Map<string, { key: string; executionID?: string }>();
  private readonly dismissedCards = new Set<string>();
  private readonly completedCards = new Set<string>();
  // An activity can visit many targets. Fence its event stream independently
  // from the cards so stale snapshots cannot revive earlier executions.
  private readonly sources = new Map<string, { activity: ActivitySession; retiredExecutions: Set<string> }>();
  // Threads whose turn ended without success; their card returns with the next turn.
  private readonly endedThreads = new Set<string>();
  private readonly lastInteractionRevisions = new Map<string, number>();
  private readonly retryAttempts = new Map<string, number>();
  private readonly retryTimers = new Map<string, NodeJS.Timeout>();
  private readonly entries = new Map<string, PiPEntry>();
  // Most recently started or promoted first.
  private stackOrder: string[] = [];
  private stackSignature = "";
  private stackLeadEntry: PiPEntry | undefined;
  // Where the stack last rested, kept after its cards leave.
  private lastStackLead: PiPStackLead | undefined;
  private readonly pendingStops = new Set<Promise<void>>();
  private closed = false;
  private userBounds: Rectangle | undefined;
  private browserHost: {
    id: number;
    client: { host: PipRect; obstacles: PipRect[] } | null;
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
  ) {}

  handleServerEvent(event: ServerEvent): void {
    const activity = observationActivityFromServerEvent(event);
    if (activity) {
      this.update(activity);
      return;
    }
    if (event.kind === "notification") {
      const { method, params } = event.message;
      const turn = params as { thread_id?: string; turn?: { status: string }; awaiting_auto_continuation?: boolean; truncated?: boolean } | undefined;
      const threadID = turn?.thread_id;
      const entries = [...this.entries.values()].filter((entry) => entry.threadID === threadID && entry.activity.workdir === event.workdir);
      if (threadID) {
        if (method === "turn/started") {
          for (const entry of entries) entry.pip.setTurnCompleted?.(false);
          if (this.endedThreads.delete(threadID)) this.syncThread(threadID);
        } else if (method === "turn/error") {
          for (const entry of entries) entry.pip.setTurnCompleted?.(false);
        } else if (method === "turn/completed" && entries.length) {
          // Stopping a browser activity is not evidence that its task succeeded.
          const succeeded = turn?.turn?.status === "completed" && !turn.awaiting_auto_continuation && !turn.truncated;
          const visible = this.visibleStack();
          for (const entry of entries) {
            if (!succeeded || visible.includes(entry)) entry.pip.setTurnCompleted?.(succeeded);
            else this.completedCards.add(entry.key);
          }
          if ((succeeded && this.activeThreadID !== threadID)
            || turn?.turn?.status === "interrupted" || turn?.turn?.status === "failed") {
            // A task that finishes offscreen must not return as a running preview.
            this.endedThreads.add(threadID);
            this.syncThread(threadID);
          }
        }
      }
    }
    this.scheduleReconcile();
  }

  setAppearance(dark: boolean, preferences?: ActivityPreviewAppearance): void {
    this.darkAppearance = dark;
    if (preferences) this.appearance = preferences;
    for (const entry of this.entries.values()) entry.pip.setAppearance?.(dark, this.appearance);
  }

  setAutoShow(enabled: boolean): void {
    this.autoShow = enabled;
    this.syncAll();
  }

  showForThread(threadID: string): void {
    this.endedThreads.delete(threadID);
    this.explicitlyShown.add(threadID);
    for (const [key, activity] of this.observations) {
      if (activity.thread_id !== threadID) continue;
      this.dismissedCards.delete(key);
      this.completedCards.delete(key);
      // Only retry the current target of each activity; older gone targets
      // cannot be recovered merely by reopening the conversation previews.
      if ([...this.sources.values()].some((source) => observationKey(source.activity) === key)) this.goneObservations.delete(key);
      this.resetRetryBudget(key);
      this.entries.get(key)?.pip.setTurnCompleted?.(false);
    }
    this.syncThread(threadID);
  }

  setBrowserInPanel(check: (activity: ActivitySession) => boolean): void {
    this.browserInPanel = check;
  }

  setBrowserExpandHandler(handler: (activity: ActivitySession) => void): void {
    this.onBrowserExpand = handler;
  }

  refreshBrowserPresentation(): void {
    this.restack();
  }

  private wantsCard(threadID: string): boolean {
    return !this.endedThreads.has(threadID)
      && (this.autoShow || this.explicitlyShown.has(threadID));
  }

  private pipVisibility(activity: ActivitySession): boolean {
    const key = observationKey(activity);
    return activity.thread_id === this.activeThreadID && this.wantsCard(activity.thread_id)
      && !this.dismissedCards.has(key) && !this.completedCards.has(key) && !this.observationIsGone(activity)
      && pipVisibleForActivity(activity, this.browserInPanel?.(activity) ?? false);
  }

  // Session selection changes presentation only, never task execution.
  setActiveThread(threadID?: string): void {
    if (this.closed) return;
    this.activeThreadID = threadID?.trim() || undefined;
    this.refreshBrowserPresentation();
    if (this.activeThreadID) {
      this.syncThread(this.activeThreadID);
    }
    this.restack();
    this.scheduleReconcile(0);
  }

  update(activity: ActivitySession): void {
    if (this.closed) return;
    const sourceKey = JSON.stringify([activity.workdir, activity.thread_id, activity.id]);
    const source = this.sources.get(sourceKey);
    if (source && ((activity.execution_id && source.retiredExecutions.has(activity.execution_id))
      || !acceptsActivityUpdate(source.activity, activity))) return;
    // Only a new authority transition on the current target may restore it.
    // A snapshot can win the race with its notification; initial/repeated
    // snapshots, helper retries and historical cards only sync state.
    const restoreTarget = source !== undefined
      && source.activity.execution_id === activity.execution_id
      && ((source.activity.controller === "agent" && activity.controller !== "agent")
        || (source.activity.state !== "stopped" && activity.state === "stopped"));
    const executionChanged = source && source.activity.execution_id !== activity.execution_id;
    const retiredExecutions = source?.retiredExecutions ?? new Set<string>();
    if (executionChanged && source.activity.execution_id) retiredExecutions.add(source.activity.execution_id);
    this.sources.set(sourceKey, { activity, retiredExecutions });

    const key = observationKey(activity);
    // Authority belongs to the activity, even when its last event names only
    // one of the targets it visited. Keep each card's capture target intact.
    for (const [otherKey, previous] of this.observations) {
      if (otherKey === key || previous.workdir !== activity.workdir
        || previous.thread_id !== activity.thread_id || previous.id !== activity.id
        || (!executionChanged && previous.execution_id !== activity.execution_id)) continue;
      if (executionChanged || activity.controller !== "agent" || !activityPreviewIsLive(activity)) {
        const stopped: ActivitySession = {
          ...previous, state: executionChanged ? "stopped" : activity.state,
          controller: executionChanged ? "none" : activity.controller,
          interaction: undefined, updated_at: activity.updated_at,
        };
        this.observations.set(otherKey, stopped);
        const entry = this.entries.get(otherKey);
        if (entry) {
          entry.activity = stopped;
          entry.pip.setLive?.(activityPreviewIsLive(stopped));
          entry.pip.updateActivity?.(stopped);
        }
      }
    }
    if (!isObservableActivity(activity) || !activity.target?.trim()) return;
    const current = this.observations.get(key);
    if (current && !acceptsActivityUpdate(current, activity)) return;
    if (current?.execution_id !== activity.execution_id || current?.id !== activity.id) {
      this.resetRetryBudget(key);
      this.lastInteractionRevisions.delete(key);
      this.completedCards.delete(key);
      this.endedThreads.delete(activity.thread_id);
      this.entries.get(key)?.pip.setTurnCompleted?.(false);
    }
    this.observations.set(key, activity);
    if (!this.observationIsGone(activity)) this.goneObservations.delete(key);
    this.syncObservation(key, restoreTarget);
    this.restack();
  }

  // Core teardown for a workdir: drop its observations and tear down cards
  // showing one of its tabs, so no ghost preview can outlive the core that
  // owned the page.
  dropWorkdir(workdir: string): void {
    for (const [key, activity] of this.observations) {
      if (activity.workdir !== workdir) continue;
      this.observations.delete(key);
      this.goneObservations.delete(key);
      this.dismissedCards.delete(key);
      this.completedCards.delete(key);
      this.lastInteractionRevisions.delete(key);
      this.resetRetryBudget(key);
    }
    for (const [key, source] of this.sources) {
      if (source.activity.workdir === workdir) this.sources.delete(key);
    }
    for (const entry of [...this.entries.values()]) {
      if (entry.activity.workdir === workdir) this.stopEntry(entry);
    }
  }

  shutdown(): Promise<void> {
    this.closed = true;
    this.activeThreadID = undefined;
    this.observations.clear();
    this.sources.clear();
    this.goneObservations.clear();
    this.browserHost?.detach();
    this.browserHost = undefined;
    if (this.reconcileTimer) clearTimeout(this.reconcileTimer);
    for (const timer of this.retryTimers.values()) clearTimeout(timer);
    this.retryTimers.clear();
    for (const entry of [...this.entries.values()]) this.stopEntry(entry);
    return Promise.all([...this.pendingStops]).then(() => undefined);
  }

  private syncAll(): void {
    for (const key of this.observations.keys()) this.syncObservation(key);
    this.restack();
  }

  private syncThread(threadID: string): void {
    for (const [key, activity] of this.observations) {
      if (activity.thread_id === threadID) this.syncObservation(key);
    }
    this.restack();
  }

  private syncObservation(key: string, restoreTarget = false): void {
    if (this.closed) return;
    const entry = this.entries.get(key);
    const activity = this.observations.get(key);
    if (!activity) return;
    // Hidden cards must still receive authority changes and stop capture.
    if (entry) {
      entry.activity = activity;
      entry.pip.setLive?.(activityPreviewIsLive(activity));
      entry.pip.updateActivity?.(activity, restoreTarget);
    }
    if (!this.wantsCard(activity.thread_id) || this.dismissedCards.has(key)
      || this.observationIsGone(activity)) return;
    if (entry) {
      if (entry.phase === "failed") {
        if (this.retryTimers.has(key) || (this.retryAttempts.get(key) ?? 0) >= 4) return;
        entry.phase = "preparing";
        entry.pip.retarget?.(activity, this.eventSink(entry));
        if (this.entries.get(key) !== entry) return;
      }
      this.animateInteractionIfNew(activity, entry.pip);
      return;
    }
    if ((activity.state === "stopped" && !this.explicitlyShown.has(activity.thread_id))
      || this.retryTimers.has(key) || (this.retryAttempts.get(key) ?? 0) >= 4) return;
    this.startEntry(activity, key, restoreTarget);
  }

  private startEntry(activity: ActivitySession, key: string, restoreTarget: boolean): void {
    if (!this.pipFactory) return;
    // The sink closes over the entry, so it exists before its handle.
    const entry = { key, threadID: activity.thread_id, activity, phase: "preparing" } as PiPEntry;
    const pip = this.pipFactory(activity, key, this.eventSink(entry), () => this.initialBounds());
    if (!pip) return;
    entry.pip = pip;
    this.entries.set(key, entry);
    this.stackOrder = [key, ...this.stackOrder.filter((id) => id !== key)];
    pip.start();
    if (this.darkAppearance !== undefined) pip.setAppearance?.(this.darkAppearance, this.appearance);
    this.pushBrowserHost(pip);
    pip.setLive?.(activityPreviewIsLive(activity));
    pip.updateActivity?.(activity, restoreTarget);
    this.animateInteractionIfNew(activity, pip);
  }

  private stopEntry(entry: PiPEntry): void {
    if (this.entries.get(entry.key) !== entry) return;
    this.entries.delete(entry.key);
    this.stackOrder = this.stackOrder.filter((id) => id !== entry.key);
    entry.pip.setVisible(false, true);
    const stopped = new Promise<void>((resolve) => entry.pip.stop(resolve));
    this.pendingStops.add(stopped);
    void stopped.then(() => this.pendingStops.delete(stopped));
    this.restack();
  }

  private promote(key: string): void {
    if (!this.stackOrder.includes(key)) return;
    this.stackOrder = [key, ...this.stackOrder.filter((id) => id !== key)];
  }

  // Lays out the visible cards in stack order. Cards are raised back to front
  // only when the order changes.
  private restack(): void {
    if (this.closed) return;
    const visible = this.visibleStack();
    this.layoutStack(visible);
    // Hide overflow before exposing a replacement so the stack never exceeds
    // its visible limit, even while the ordinary exit animation is running.
    for (const entry of this.entries.values()) {
      if (!visible.includes(entry)) entry.pip.setVisible(false, true);
    }
    for (const entry of visible) entry.pip.setVisible(true);
    const signature = visible.map((entry) => entry.key).join("\n");
    if (signature === this.stackSignature) return;
    this.stackSignature = signature;
    this.raiseStack(visible);
  }

  private raiseStack(visible = this.visibleStack()): void {
    for (const entry of [...visible].reverse()) entry.pip.raise?.();
    visible.find((entry) => entry.pip.stackLead?.()?.dragging)?.pip.raise?.();
  }

  private layoutStack(visible = this.visibleStack()): void {
    const lead = visible[0];
    if (!lead) return;
    const draggingIndex = visible.findIndex((entry) => entry.pip.stackLead?.()?.dragging);
    if (draggingIndex >= 0) {
      const motionLead = { ...visible[draggingIndex]!.pip.stackLead!()!, index: draggingIndex };
      visible.forEach((entry, index) => entry.pip.setStackPosition?.(index, motionLead));
      this.stackLeadEntry = lead;
      return;
    }
    // A new lead takes over the resting place of the card it replaces, or of
    // the last stack when none remains.
    const replaced = this.stackLeadEntry;
    const previous = replaced === lead ? undefined
      : replaced && this.entries.get(replaced.key) === replaced ? replaced.pip.stackLead?.() : this.lastStackLead;
    this.stackLeadEntry = lead;
    lead.pip.setStackPosition?.(0, previous);
    const frame = lead.pip.stackLead?.();
    this.lastStackLead = frame;
    visible.slice(1).forEach((entry, index) => entry.pip.setStackPosition?.(index + 1, frame));
  }

  private visibleStack(): PiPEntry[] {
    return this.stackOrder
      .map((id) => this.entries.get(id))
      .filter((entry): entry is PiPEntry => Boolean(entry && this.pipVisibility(entry.activity)))
      .slice(0, VISIBLE_CARD_LIMIT);
  }

  private eventSink(entry: PiPEntry): ObservationPiPEventSink {
    const key = (): string | undefined => this.entries.get(entry.key) === entry ? entry.key : undefined;
    const current = entry.key;
    return {
      canRestoreTarget: () => {
        const sourceKey = JSON.stringify([entry.activity.workdir, entry.threadID, entry.activity.id]);
        const source = this.sources.get(sourceKey)?.activity;
        return key() === current && source !== undefined && observationKey(source) === current
          && source.execution_id === entry.activity.execution_id
          && ![...this.sources].some(([id, other]) => id !== sourceKey && other.activity.kind === "cua"
            && other.activity.process_id === entry.activity.process_id && activityPreviewIsLive(other.activity));
      },
      onEvent: (event) => { if (key() === current) this.handlePiPEvent(entry, event); },
      onFailure: (message) => { if (key() === current) this.handlePiPFailure(entry, message); },
      onGone: () => { if (key() === current) this.handlePiPGone(entry); },
    };
  }

  private handlePiPEvent(entry: PiPEntry, event: CUANativePiPEvent): void {
    switch (event.event) {
      case "ready":
        entry.phase = "live";
        this.retryAttempts.delete(entry.key);
        this.restack();
        return;
      case "retry":
        if (!activityPreviewIsLive(entry.activity)) return;
        this.resetRetryBudget(entry.key);
        entry.phase = "failed";
        this.syncObservation(entry.key);
        this.restack();
        return;
      case "user_close":
        this.dismissedCards.add(entry.key);
        this.restack();
        return;
      case "drag_start":
        this.layoutStack();
        this.raiseStack();
        return;
      case "promote":
        this.promote(entry.key);
        this.restack();
        return;
      case "shown":
        this.raiseStack();
        return;
      case "completion_expired":
        // A completed card that has left returns with the next turn, like an
        // ended one, so it no longer holds a place in the stack.
        this.completedCards.add(entry.key);
        this.restack();
        return;
      case "expand":
        if (entry.activity.kind === "browser") this.onBrowserExpand?.(entry.activity);
        return;
      case "gone":
        this.handlePiPGone(entry);
        return;
      case "user_input":
        return;
      case "geometry": {
        const visible = this.visibleStack();
        const moving = visible.find((item) => item.pip.stackLead?.()?.dragging) ?? visible[0];
        if (moving !== entry) return;
        if ([event.x, event.y, event.width, event.height].every((value) => typeof value === "number")) {
          this.userBounds = { x: event.x!, y: event.y!, width: event.width!, height: event.height! };
        }
        this.layoutStack();
        return;
      }
      case "capture_status":
        return;
    }
  }

  private handlePiPFailure(entry: PiPEntry, message: string): void {
    if (entry.pip.suspendContent) {
      entry.phase = "failed";
      entry.pip.suspendContent(message);
      this.restack();
    } else this.stopEntry(entry);
    const key = entry.key;
    if (this.dismissedCards.has(key)) return;
    const attempts = (this.retryAttempts.get(key) ?? 0) + 1;
    this.retryAttempts.set(key, attempts);
    if (attempts >= 4) return;
    const timer = setTimeout(() => {
      if (this.retryTimers.get(key) !== timer) return;
      this.retryTimers.delete(key);
      this.syncObservation(key);
      this.restack();
    }, frameStreamRetryDelay(attempts));
    timer.unref?.();
    this.retryTimers.set(key, timer);
  }

  private resetRetryBudget(key: string): void {
    this.retryAttempts.delete(key);
    const timer = this.retryTimers.get(key);
    if (timer) clearTimeout(timer);
    this.retryTimers.delete(key);
  }

  // The capture target disappeared (tab closed, view crashed, workdir torn
  // down). Retain its metadata as the ordering high-water mark, but fence this
  // exact source until a different target/execution or an explicit user retry.
  // A registry snapshot cannot establish that a disappeared target is back.
  private handlePiPGone(entry: PiPEntry): void {
    const observed = this.observations.get(entry.key);
    if (observed && observationKey(observed) === entry.key) {
      this.goneObservations.set(entry.key, { key: entry.key, executionID: observed.execution_id });
    }
    this.resetRetryBudget(entry.key);
    this.stopEntry(entry);
  }

  private observationIsGone(activity: ActivitySession): boolean {
    const gone = this.goneObservations.get(observationKey(activity));
    return gone?.key === observationKey(activity) && gone.executionID === activity.execution_id;
  }

  private animateInteractionIfNew(activity: ActivitySession, pip: ObservationPiPHandle): void {
    const interaction = activity.interaction;
    if (!interaction) return;
    const key = observationKey(activity);
    const previous = this.lastInteractionRevisions.get(key);
    if (previous !== undefined && interaction.revision <= previous) return;
    this.lastInteractionRevisions.set(key, interaction.revision);
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
      this.browserHost &&
      this.browserHost.id !== webContentsId &&
      !this.browserHost.window.isDestroyed() &&
      !hostWindow.isFocused()
    ) {
      return;
    }
    // An unavailable column still belongs to its window. Retain that owner so
    // background layout updates cannot restore previews over its dialog.
    if (!this.browserHost || this.browserHost.id !== webContentsId || this.browserHost.window !== hostWindow) {
      this.browserHost?.detach();
      const reproject = (): void => {
        const source = this.browserHost;
        if (!source || source.window.isDestroyed()) return;
        const content = source.window.getContentBounds();
        // Dragging the top/left edge can emit move and resize together.
        if (content.width !== source.contentSize.width || content.height !== source.contentSize.height) return;
        this.browserScreenLayout = this.projectBrowserHost();
        this.pushBrowserHostToAll();
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
    this.pushBrowserHostToAll();
  }

  private projectBrowserHost(): BrowserPiPScreenLayout | null {
    const source = this.browserHost;
    if (!source?.client || source.window.isDestroyed()) return null;
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

  // The lead places itself at the column corner; trailing cards follow it.
  private pushBrowserHostToAll(): void {
    const stack = this.visibleStack();
    for (const entry of this.entries.values()) {
      if (entry !== stack[0]) this.pushBrowserHost(entry.pip);
    }
    if (stack[0]) this.pushBrowserHost(stack[0].pip);
    this.layoutStack(stack);
  }

  private pushBrowserHost(pip: ObservationPiPHandle | undefined): void {
    if (!pip?.setHostLayout || this.browserScreenLayout === undefined) return;
    // Reparenting may reveal a card, so apply dialog suppression first.
    if (this.browserScreenLayout === null) pip.setHostLayout(null);
    pip.setHostParent?.(this.browserHost && !this.browserHost.window.isDestroyed() ? this.browserHost.window : null);
    if (this.browserScreenLayout !== null) pip.setHostLayout(this.browserScreenLayout);
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
  // A tab/window keeps its presentation across activity and execution changes.
  // JSON encoding also keeps delimiters inside targets or workdirs unambiguous.
  return JSON.stringify([activity.workdir, activity.thread_id, activity.kind, activity.target?.trim(),
    ...(activity.kind === "cua" ? [activity.process_id ?? 0, activity.window_id ?? 0] : [])]);
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
