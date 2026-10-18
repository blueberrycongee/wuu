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

// The core owns one observation card per thread. Provider adapters only attach
// content; visibility, theme and interaction feedback stay in this contract.
export type ObservationPiPHandle = {
  start(): void;
  setVisible(visible: boolean): void;
  animateInteraction(interaction: NonNullable<ActivitySession["interaction"]>): void;
  stop(onStopped?: () => void): void;
  updateActivity?(activity: ActivitySession): void;
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
  retiredExecutions: Set<string>;
};

/**
 * Owns the observation cards of every thread with a live, observable task
 * (CUA windows, embedded-browser tabs). Each thread has at most one card;
 * activity events retarget it, but an individual action ending never owns its
 * lifetime. Only the active conversation presents its card; other tasks keep
 * their observations and continue running while their cards are hidden.
 */
export class ObservationCoordinator {
  private readonly observations = new Map<string, ActivitySession>();
  private readonly goneObservations = new Map<string, { key: string; executionID?: string }>();
  private readonly dismissedThreads = new Set<string>();
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
      const entry = threadID ? this.entries.get(threadID) : undefined;
      if (threadID && (!entry || event.workdir === entry.activity.workdir)) {
        if (method === "turn/started") {
          entry?.pip.setTurnCompleted?.(false);
          if (this.endedThreads.delete(threadID)) this.syncThread(threadID);
        } else if (method === "turn/error") {
          entry?.pip.setTurnCompleted?.(false);
        } else if (method === "turn/completed" && entry) {
          // Stopping a browser activity is not evidence that its task succeeded.
          const succeeded = turn?.turn?.status === "completed" && !turn.awaiting_auto_continuation && !turn.truncated;
          if (!succeeded || this.pipVisibility(entry.activity)) entry.pip.setTurnCompleted?.(succeeded);
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
    this.dismissedThreads.delete(threadID);
    this.endedThreads.delete(threadID);
    this.goneObservations.delete(threadID);
    this.explicitlyShown.add(threadID);
    this.resetRetryBudget(threadID);
    this.entries.get(threadID)?.pip.setTurnCompleted?.(false);
    this.promote(threadID);
    this.syncThread(threadID);
  }

  setBrowserInPanel(check: (activity: ActivitySession) => boolean): void {
    this.browserInPanel = check;
  }

  setBrowserExpandHandler(handler: (activity: ActivitySession) => void): void {
    this.onBrowserExpand = handler;
  }

  refreshBrowserPresentation(): void {
    for (const entry of this.entries.values()) entry.pip.setVisible(this.pipVisibility(entry.activity));
    this.restack();
  }

  private wantsCard(threadID: string): boolean {
    return !this.dismissedThreads.has(threadID) && !this.endedThreads.has(threadID)
      && (this.autoShow || this.explicitlyShown.has(threadID));
  }

  private pipVisibility(activity: ActivitySession): boolean {
    return activity.thread_id === this.activeThreadID && this.wantsCard(activity.thread_id)
      && pipVisibleForActivity(activity, this.browserInPanel?.(activity) ?? false);
  }

  // Session selection changes presentation only, never task execution.
  setActiveThread(threadID?: string): void {
    if (this.closed) return;
    this.activeThreadID = threadID?.trim() || undefined;
    this.refreshBrowserPresentation();
    if (this.activeThreadID) {
      this.promote(this.activeThreadID);
      this.syncThread(this.activeThreadID);
    }
    this.restack();
    this.scheduleReconcile(0);
  }

  update(activity: ActivitySession): void {
    if (this.closed) return;
    const retained = this.entries.get(activity.thread_id);
    if (retained && activity.execution_id && retained.retiredExecutions.has(activity.execution_id)) return;
    if (retained && retained.activity.id === activity.id && acceptsActivityUpdate(retained.activity, activity)) {
      // A hidden card still owns capture resources. Deliver task termination
      // and takeover without showing or retargeting it. An unresolved or new
      // target cannot resume old pixels.
      if (retained.activity.execution_id !== activity.execution_id) {
        if (retained.activity.execution_id) retained.retiredExecutions.add(retained.activity.execution_id);
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
    if (current?.execution_id !== activity.execution_id) this.endedThreads.delete(activity.thread_id);
    this.observations.set(activity.thread_id, activity);
    if (!this.observationIsGone(activity)) this.goneObservations.delete(activity.thread_id);
    this.syncThread(activity.thread_id);
  }

  // Core teardown for a workdir: drop its observations and tear down cards
  // showing one of its tabs, so no ghost preview can outlive the core that
  // owned the page.
  dropWorkdir(workdir: string): void {
    for (const [threadID, activity] of [...this.observations]) {
      if (activity.workdir === workdir) {
        this.observations.delete(threadID);
        this.goneObservations.delete(threadID);
      }
    }
    for (const entry of [...this.entries.values()]) {
      if (entry.activity.workdir === workdir) this.stopEntry(entry);
    }
  }

  shutdown(): Promise<void> {
    this.closed = true;
    this.activeThreadID = undefined;
    this.observations.clear();
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
    for (const threadID of new Set([...this.observations.keys(), ...this.entries.keys()])) this.syncThread(threadID, false);
    this.restack();
  }

  private syncThread(threadID: string, restack = true): void {
    if (this.closed) return;
    const entry = this.entries.get(threadID);
    const activity = this.observations.get(threadID);
    if (!this.wantsCard(threadID) || !activity || this.observationIsGone(activity)) {
      entry?.pip.setVisible(false);
      if (restack) this.restack();
      return;
    }
    const key = observationKey(activity);
    if (entry?.key === key) {
      if (entry.phase === "failed") {
        if (this.retryTimers.has(threadID) || (this.retryAttempts.get(threadID) ?? 0) >= 4) return;
        entry.phase = "preparing";
        entry.pip.retarget?.(activity, this.eventSink(entry));
        if (this.entries.get(threadID) !== entry) return;
      }
      if (entry.activity.execution_id !== activity.execution_id) {
        entry.pip.setTurnCompleted?.(false);
      }
      entry.activity = activity;
      entry.pip.setVisible(this.pipVisibility(activity));
      entry.pip.setLive?.(activityPreviewIsLive(activity));
      entry.pip.updateActivity?.(activity);
      this.animateInteractionIfNew(activity, entry.pip);
      if (restack) this.restack();
      return;
    }
    if ((activity.state === "stopped" && !this.explicitlyShown.has(threadID)) || this.retryTimers.has(threadID)
      || (this.retryAttempts.get(threadID) ?? 0) >= 4) return;
    if (entry?.pip.retarget) {
      if (entry.activity.execution_id && entry.activity.execution_id !== activity.execution_id) {
        entry.retiredExecutions.add(entry.activity.execution_id);
      }
      entry.key = key;
      entry.activity = activity;
      entry.phase = "preparing";
      entry.pip.retarget(activity, this.eventSink(entry));
      if (this.entries.get(threadID) !== entry) return;
      entry.pip.setVisible(this.pipVisibility(activity));
      entry.pip.setLive?.(activityPreviewIsLive(activity));
      this.animateInteractionIfNew(activity, entry.pip);
      if (restack) this.restack();
      return;
    }
    if (entry) this.stopEntry(entry);
    this.startEntry(activity, key);
    if (restack) this.restack();
  }

  private startEntry(activity: ActivitySession, key: string): void {
    if (!this.pipFactory) return;
    // The sink closes over the entry, so it exists before its handle.
    const entry = { key, threadID: activity.thread_id, activity, phase: "preparing", retiredExecutions: new Set<string>() } as PiPEntry;
    const pip = this.pipFactory(activity, key, this.eventSink(entry), () => this.initialBounds());
    if (!pip) return;
    entry.pip = pip;
    this.entries.set(activity.thread_id, entry);
    // A new card leads unless the conversation in view already has one.
    const order = this.stackOrder.filter((id) => id !== activity.thread_id);
    const behind = order[0] !== undefined && order[0] === this.activeThreadID && activity.thread_id !== this.activeThreadID;
    this.stackOrder = behind ? [order[0], activity.thread_id, ...order.slice(1)] : [activity.thread_id, ...order];
    pip.start();
    if (this.darkAppearance !== undefined) pip.setAppearance?.(this.darkAppearance, this.appearance);
    this.pushBrowserHost(pip);
    pip.setVisible(this.pipVisibility(activity));
    pip.setLive?.(activityPreviewIsLive(activity));
    pip.updateActivity?.(activity);
    this.animateInteractionIfNew(activity, pip);
  }

  private stopEntry(entry: PiPEntry): void {
    if (this.entries.get(entry.threadID) !== entry) return;
    this.entries.delete(entry.threadID);
    this.stackOrder = this.stackOrder.filter((id) => id !== entry.threadID);
    const stopped = new Promise<void>((resolve) => entry.pip.stop(resolve));
    this.pendingStops.add(stopped);
    void stopped.then(() => this.pendingStops.delete(stopped));
    this.restack();
  }

  private promote(threadID: string): void {
    if (!this.stackOrder.includes(threadID)) return;
    this.stackOrder = [threadID, ...this.stackOrder.filter((id) => id !== threadID)];
  }

  // Lays out the visible cards in stack order. Cards are raised back to front
  // only when the order changes.
  private restack(): void {
    if (this.closed) return;
    const visible = this.visibleStack();
    this.layoutStack(visible);
    const signature = visible.map((entry) => entry.key).join("\n");
    if (signature === this.stackSignature) return;
    this.stackSignature = signature;
    this.raiseStack(visible);
  }

  private raiseStack(visible = this.visibleStack()): void {
    for (const entry of [...visible].reverse()) entry.pip.raise?.();
  }

  private layoutStack(visible = this.visibleStack()): void {
    const lead = visible[0];
    if (!lead) return;
    // A new lead takes over the resting place of the card it replaces, or of
    // the last stack when none remains.
    const replaced = this.stackLeadEntry;
    const previous = replaced === lead ? undefined
      : replaced && this.entries.get(replaced.threadID) === replaced ? replaced.pip.stackLead?.() : this.lastStackLead;
    this.stackLeadEntry = lead;
    lead.pip.setStackPosition?.(0, previous);
    const frame = lead.pip.stackLead?.();
    this.lastStackLead = frame;
    visible.slice(1).forEach((entry, index) => entry.pip.setStackPosition?.(index + 1, frame));
  }

  private visibleStack(): PiPEntry[] {
    return this.stackOrder
      .map((id) => this.entries.get(id))
      .filter((entry): entry is PiPEntry => Boolean(entry && this.pipVisibility(entry.activity)));
  }

  private eventSink(entry: PiPEntry): ObservationPiPEventSink {
    const key = (): string | undefined => this.entries.get(entry.threadID) === entry ? entry.key : undefined;
    const current = entry.key;
    return {
      onEvent: (event) => { if (key() === current) this.handlePiPEvent(entry, event); },
      onFailure: (message) => { if (key() === current) this.handlePiPFailure(entry, message); },
      onGone: () => { if (key() === current) this.handlePiPGone(entry); },
    };
  }

  private handlePiPEvent(entry: PiPEntry, event: CUANativePiPEvent): void {
    switch (event.event) {
      case "ready":
        entry.phase = "live";
        this.retryAttempts.delete(entry.threadID);
        this.restack();
        return;
      case "retry":
        if (!activityPreviewIsLive(entry.activity)) return;
        this.resetRetryBudget(entry.threadID);
        entry.phase = "failed";
        this.syncThread(entry.threadID);
        return;
      case "user_close":
        this.dismissedThreads.add(entry.threadID);
        this.explicitlyShown.delete(entry.threadID);
        entry.pip.setVisible(false);
        this.restack();
        return;
      case "promote":
        this.promote(entry.threadID);
        this.restack();
        return;
      case "shown":
        this.raiseStack();
        return;
      case "completion_expired":
        // A completed card that has left returns with the next turn, like an
        // ended one, so it no longer holds a place in the stack.
        this.endedThreads.add(entry.threadID);
        this.syncThread(entry.threadID);
        return;
      case "expand":
        if (entry.activity.kind === "browser") this.onBrowserExpand?.(entry.activity);
        return;
      case "gone":
        this.handlePiPGone(entry);
        return;
      case "user_input":
        return;
      case "geometry":
        if (this.visibleStack()[0] !== entry) return;
        if ([event.x, event.y, event.width, event.height].every((value) => typeof value === "number")) {
          this.userBounds = { x: event.x!, y: event.y!, width: event.width!, height: event.height! };
        }
        this.layoutStack();
        return;
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
    const threadID = entry.threadID;
    if (this.dismissedThreads.has(threadID)) return;
    const attempts = (this.retryAttempts.get(threadID) ?? 0) + 1;
    this.retryAttempts.set(threadID, attempts);
    if (attempts >= 4) return;
    const timer = setTimeout(() => {
      if (this.retryTimers.get(threadID) !== timer) return;
      this.retryTimers.delete(threadID);
      this.syncThread(threadID);
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
  private handlePiPGone(entry: PiPEntry): void {
    const observed = this.observations.get(entry.threadID);
    if (observed && observationKey(observed) === entry.key) {
      this.goneObservations.set(entry.threadID, { key: entry.key, executionID: observed.execution_id });
    }
    this.resetRetryBudget(entry.threadID);
    this.stopEntry(entry);
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
