import { screen, type Rectangle } from "electron";
import type { ActivitySession, ServerEvent } from "../shared/protocol";
import {
  browserPiPScreenRect,
  PIP_ANCHOR_MARGIN,
  PIP_CARD_SIZE,
  PIP_WINDOW_MIN_SIZE,
  type BrowserPiPScreenLayout,
  type PipRect,
} from "./browserPiPPlacement";
import type { CUANativePiPEvent } from "./cuaFrameStreams";
import type { ActivityPreviewAction, ActivityPreviewPlacement, ActivityPreviewState, ActivityPreviewAppearance } from "../shared/activityPreview";
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
  setPlacement?(placement: ActivityPreviewPlacement, petAvailable: boolean): void;
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
 * lifetime. Home cards follow the selected conversation; desktop and Pet cards
 * retain their own task while other conversations or apps are foregrounded.
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
  private readonly stackLeadEntries = new Map<ActivityPreviewPlacement, PiPEntry>();
  // Where the stack last rested, kept after its cards leave.
  private readonly lastStackLeads = new Map<ActivityPreviewPlacement, PiPStackLead>();
  private readonly pendingStops = new Set<Promise<void>>();
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
  private readonly placements = new Map<string, ActivityPreviewPlacement>();
  private readonly presentationSignatures = new Map<string, string>();
  private presentationListener: ((state: ActivityPreviewState) => void) | undefined;
  private petHost: (() => { window: BrowserPiPHostWindow; anchor: { x: number; y: number }; sprite: { width: number; height: number }; ready: boolean; occupiedBounds: Rectangle } | undefined) | undefined;
  private readonly pendingPetThreads = new Set<string>();
  private requestingPetHost = false;
  private requestPetHost: (() => void) | undefined;
  private petPreviewActive: ((active: boolean) => void) | undefined;
  private onReturnHome: ((activity: ActivitySession) => void) | undefined;

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
          if ((succeeded && !this.pipVisibility(entry.activity))
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

  setPresentationListener(listener: (state: ActivityPreviewState) => void): void {
    this.presentationListener = listener;
  }

  setPetHost(provider: NonNullable<ObservationCoordinator["petHost"]>, active: (active: boolean) => void, request?: () => void): void {
    this.requestPetHost = request;
    this.petHost = provider;
    this.petPreviewActive = active;
  }

  setReturnHomeHandler(handler: (activity: ActivitySession) => void): void { this.onReturnHome = handler; }

  presentationForThread(threadID: string): ActivityPreviewState | null {
    const activity = this.observations.get(threadID);
    if (!activity) return null;
    return { threadID, workdir: activity.workdir, visible: this.wantsCard(threadID) && !this.observationIsGone(activity),
      placement: this.placements.get(threadID) ?? "home", petAvailable: Boolean(this.requestPetHost || this.petHost?.()?.ready) };
  }

  setPresentation(threadID: string, action: ActivityPreviewAction): ActivityPreviewState | null {
    if (this.closed || !this.observations.has(threadID)) return null;
    if (action !== "show") this.pendingPetThreads.delete(threadID);
    if (action === "hide") {
      this.dismissedThreads.add(threadID);
      this.explicitlyShown.delete(threadID);
      this.syncThread(threadID);
    } else {
      if (action === "pet" && !this.petHost?.()?.ready) {
        if (!this.requestPetHost) return this.presentationForThread(threadID);
        // Keep the current owner until the requested desktop host can receive
        // the existing content. A second action cancels this pending intent.
        this.pendingPetThreads.add(threadID);
        this.requestingPetHost = true;
        try { this.showForThread(threadID); this.requestPetHost(); }
        finally { this.requestingPetHost = false; this.refreshPetHost(); }
        return this.presentationForThread(threadID);
      }
      if (action !== "show") this.placements.set(threadID, action);
      this.pushBrowserHost(this.entries.get(threadID));
      this.showForThread(threadID);
    }
    this.publishPresentation(threadID);
    return this.presentationForThread(threadID);
  }

  refreshPetHost(): void {
    if (this.closed || this.requestingPetHost) return;
    const host = this.petHost?.();
    if (host?.ready) {
      for (const threadID of this.pendingPetThreads) {
        if (this.wantsCard(threadID) && this.observations.has(threadID)) this.placements.set(threadID, "pet");
      }
      this.pendingPetThreads.clear();
    } else if (!host) this.pendingPetThreads.clear();
    if (!host) {
      for (const [threadID, placement] of this.placements) {
        if (placement === "pet") this.placements.set(threadID, "home");
      }
    }
    for (const entry of this.entries.values()) {
      this.pushBrowserHost(entry);
      entry.pip.setVisible(this.pipVisibility(entry.activity));
      this.publishPresentation(entry.threadID);
    }
    for (const threadID of this.observations.keys()) this.publishPresentation(threadID);
    this.restack();
  }

  private publishPresentation(threadID: string): void {
    const state = this.presentationForThread(threadID);
    if (!state) return;
    const signature = JSON.stringify(state);
    if (this.presentationSignatures.get(threadID) === signature) return;
    this.presentationSignatures.set(threadID, signature);
    this.presentationListener?.(state);
  }

  setBrowserInPanel(check: (activity: ActivitySession) => boolean): void {
    this.browserInPanel = check;
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
    const placement = this.placements.get(activity.thread_id) ?? "home";
    return (placement !== "home" || activity.thread_id === this.activeThreadID) && this.wantsCard(activity.thread_id)
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
        const state = this.presentationForThread(threadID);
        this.observations.delete(threadID);
        this.goneObservations.delete(threadID);
        this.placements.delete(threadID);
        this.pendingPetThreads.delete(threadID);
        this.presentationSignatures.delete(threadID);
        if (state) this.presentationListener?.({ ...state, visible: false });
      }
    }
    for (const entry of [...this.entries.values()]) {
      if (entry.activity.workdir === workdir) this.stopEntry(entry);
    }
  }

  shutdown(): Promise<void> {
    this.closed = true;
    this.pendingPetThreads.clear();
    this.petPreviewActive?.(false);
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
    this.publishPresentation(threadID);
    const entry = this.entries.get(threadID);
    const activity = this.observations.get(threadID);
    if (!this.wantsCard(threadID) || !activity || this.observationIsGone(activity)) {
      this.pendingPetThreads.delete(threadID);
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
    this.pushBrowserHost(entry);
    pip.setVisible(this.pipVisibility(activity));
    pip.setLive?.(activityPreviewIsLive(activity));
    pip.updateActivity?.(activity);
    this.animateInteractionIfNew(activity, pip);
  }

  private stopEntry(entry: PiPEntry): void {
    if (this.entries.get(entry.threadID) !== entry) return;
    this.entries.delete(entry.threadID);
    this.pendingPetThreads.delete(entry.threadID);
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
    // Browser-panel takeover temporarily hides pixels, not the user's desktop
    // ownership. Explicit hide, completion and target loss release that intent.
    this.petPreviewActive?.(this.pendingPetThreads.size > 0 || [...this.entries.values()].some(entry =>
      this.placements.get(entry.threadID) === "pet" && this.wantsCard(entry.threadID) && !this.observationIsGone(entry.activity)));
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
    for (const placement of ["home", "floating", "pet"] as const) {
      const group = visible.filter(entry => (this.placements.get(entry.threadID) ?? "home") === placement);
      const lead = group[0];
      if (!lead) continue;
      const replaced = this.stackLeadEntries.get(placement);
      // Never inherit geometry from a card that moved to a different host.
      const previous = replaced === lead ? undefined
        : replaced && (this.placements.get(replaced.threadID) ?? "home") === placement
          && this.entries.get(replaced.threadID) === replaced ? replaced.pip.stackLead?.() : this.lastStackLeads.get(placement);
      this.stackLeadEntries.set(placement, lead);
      lead.pip.setStackPosition?.(0, previous);
      const frame = lead.pip.stackLead?.();
      if (frame) this.lastStackLeads.set(placement, frame);
      group.slice(1).forEach((entry, index) => entry.pip.setStackPosition?.(index + 1, frame));
    }
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
        this.setPresentation(entry.threadID, "hide");
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
      case "return_home":
        this.setPresentation(entry.threadID, "home");
        this.onReturnHome?.(entry.activity);
        return;
      case "send_to_pet":
        this.setPresentation(entry.threadID, "pet");
        return;
      case "gone":
        this.handlePiPGone(entry);
        return;
      case "user_input":
        return;
      case "geometry":
        if (this.visibleStack().find(candidate => (this.placements.get(candidate.threadID) ?? "home")
          === (this.placements.get(entry.threadID) ?? "home")) !== entry) return;
        if ([event.x, event.y, event.width, event.height].every((value) => typeof value === "number")) {
          this.userBounds = { x: event.x!, y: event.y!, width: event.width!, height: event.height! };
        }
        if (this.placements.get(entry.threadID) === "pet") this.pushBrowserHostToAll();
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
    this.publishPresentation(entry.threadID);
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
      this.pushBrowserHostToAll();
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

  // The lead places itself at the column corner; trailing cards follow it.
  private pushBrowserHostToAll(): void {
    const stack = this.visibleStack();
    for (const entry of this.entries.values()) {
      if (entry !== stack[0]) this.pushBrowserHost(entry);
    }
    if (stack[0]) this.pushBrowserHost(stack[0]);
    this.layoutStack(stack);
  }

  private pushBrowserHost(entry: PiPEntry | undefined): void {
    if (!entry?.pip.setHostLayout) return;
    const pip = entry.pip;
    const placement = this.placements.get(entry.threadID) ?? "home";
    const pet = this.petHost?.();
    pip.setPlacement?.(placement, Boolean(this.requestPetHost || pet?.ready));
    if (placement === "pet" && pet) {
      pip.setHostParent?.(pet.window);
      // A loading replacement retains ownership, but has no usable pixels yet.
      if (!pet.ready) { pip.setHostLayout?.(null); return; }
      const frame = screen.getDisplayNearestPoint(pet.anchor).workArea;
      const lead = this.visibleStack().find(candidate => this.placements.get(candidate.threadID) === "pet");
      const wanted = lead?.pip.stackLead?.()?.frame ?? pip.stackLead?.()?.frame ?? PIP_CARD_SIZE;
      const occupied = pet.occupiedBounds;
      const gap = 8;
      const right = frame.x + frame.width, bottom = frame.y + frame.height;
      // Reserve the whole Pet panel, including its dock and expanded cards.
      // Prefer above/below, then the sides; shrink into available screen space
      // rather than carrying an oversized frame onto a smaller display.
      const regions = [
        { side: "above", x: frame.x, y: frame.y, width: frame.width, height: Math.max(0, Math.min(bottom, occupied.y - gap) - frame.y) },
        { side: "below", x: frame.x, y: Math.max(frame.y, occupied.y + occupied.height + gap), width: frame.width, height: Math.max(0, bottom - Math.max(frame.y, occupied.y + occupied.height + gap)) },
        { side: "left", x: frame.x, y: frame.y, width: Math.max(0, Math.min(right, occupied.x - gap) - frame.x), height: frame.height },
        { side: "right", x: Math.max(frame.x, occupied.x + occupied.width + gap), y: frame.y, width: Math.max(0, right - Math.max(frame.x, occupied.x + occupied.width + gap)), height: frame.height },
      ].filter(region => region.width >= PIP_WINDOW_MIN_SIZE.width && region.height >= PIP_WINDOW_MIN_SIZE.height);
      const region = regions.sort((a, b) =>
        Math.min(wanted.width, b.width) * Math.min(wanted.height, b.height)
        - Math.min(wanted.width, a.width) * Math.min(wanted.height, a.height))[0];
      if (!region) { pip.setHostLayout?.(null); return; }
      const size = { width: Math.min(wanted.width, region.width), height: Math.min(wanted.height, region.height) };
      const x = region.side === "left" ? region.x + region.width - size.width
        : region.side === "right" ? region.x
          : Math.max(region.x, Math.min(pet.anchor.x + pet.sprite.width / 2 - size.width, region.x + region.width - size.width));
      const y = region.side === "above" ? region.y + region.height - size.height
        : region.side === "below" ? region.y
          : Math.max(region.y, Math.min(pet.anchor.y - pet.sprite.height / 2 - size.height / 2, region.y + region.height - size.height));
      pip.setHostLayout?.({ host: { x: x - PIP_ANCHOR_MARGIN, y: y - PIP_ANCHOR_MARGIN,
        width: size.width + PIP_ANCHOR_MARGIN * 2, height: size.height + PIP_ANCHOR_MARGIN * 2 },
        obstacles: [], visibleFrame: frame, stackBounds: region });
      return;
    }
    if (placement === "floating") {
      const bounds = pip.stackLead?.()?.frame ?? this.initialBounds();
      const frame = screen.getDisplayMatching(bounds).workArea;
      pip.setHostParent?.(null);
      pip.setHostLayout?.({ host: frame, obstacles: [], visibleFrame: frame });
      return;
    }
    const parent = this.browserHost?.window ?? this.registry.mainWindow();
    pip.setHostParent?.(parent && !parent.isDestroyed() ? parent as BrowserPiPHostWindow : null);
    if (this.browserScreenLayout !== undefined) pip.setHostLayout?.(this.browserScreenLayout);

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
