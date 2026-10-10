import type { HandlerDetails, LoadURLOptions, Rectangle, Session, WebContents, WebContentsViewConstructorOptions, WindowOpenHandlerResponse } from "electron";
import type { ActivitySession, BrowserListedTab, BrowserSurfaceSnapshot, BrowserTabStatus, BrowserTurnEndedResult, JsonValue, ServerEvent } from "../shared/protocol";
import {
  agentCursorCommandScript,
  clearAgentCursorScript,
  CURSOR_ARRIVE_CAP_MS,
  type AgentCursorFeedback,
} from "./agentCursor";
import { writeBufferFileAtomicSync, writeTextFileAtomicSync } from "./atomicFile";
import type { WindowRegistry } from "./windowRegistry";

// One partition for every embedded browser tab. A login performed while the
// agent drives a tab is still there when the user drives that same tab.
// Permission decisions stay per webContents so a deny for these tabs does
// not blanket every other contents on the partition.
export const BROWSER_PARTITION = "persist:wuu-browser";

// Conservative overflow gate. The core stdin scanner enforces a 4MB line
// limit; a DOM snapshot on a large page is 5-50MB before trimming. We project
// that snapshot to interactable nodes plus one slice of readable text, then
// still gate the serialized result at 1MB and spill to the core-designated
// dest_path so a pathological page can never wedge the protocol.
export const MAX_INLINE_RESULT_BYTES = 1024 * 1024;

// Sensitive capabilities refused for tabs this coordinator owns. Contents it
// does not own are left to the default permission path.
export const BROWSER_AGENT_DENIED_PERMISSIONS = new Set<string>([
  "media", // camera + microphone
  "geolocation",
  "notifications",
  "midi",
  "midiSysex",
  "pointerLock",
  "openExternal",
  "hid",
  "serial",
  "usb",
  "display-capture",
  "idle-detection",
  "window-management",
  "keyboardLock",
  "speaker-selection",
  "clipboard-read", // the OS clipboard frequently holds the user's secrets
  "clipboard-sanitized-write",
]);

// ---------------------------------------------------------------------------
// Injected handle surface. Real Electron types are structurally compatible;
// tests supply vi.fn-backed fakes so the coordinator never touches a real
// WebContentsView / BrowserWindow / debugger / capturer.
// ---------------------------------------------------------------------------

export interface BrowserDebuggerHandle {
  attach(protocolVersion?: string): void;
  detach(): void;
  isAttached(): boolean;
  sendCommand(method: string, params?: Record<string, unknown>): Promise<Record<string, unknown>>;
}

export interface BrowserNativeImageHandle {
  toPNG(): Buffer;
  getSize(): { width: number; height: number };
}

export interface BrowserWebContentsHandle {
  readonly id: number;
  readonly debugger: BrowserDebuggerHandle;
  setBackgroundThrottling(allowed: boolean): void;
  setWindowOpenHandler(handler: (details: Partial<HandlerDetails>) => WindowOpenHandlerResponse): void;
  canGoBack(): boolean;
  canGoForward(): boolean;
  goBack(): void;
  goForward(): void;
  reload(): void;
  stop(): void;
  isLoading(): boolean;
  executeJavaScript(code: string): Promise<unknown>;
  // User-origin CSS outranks page styles. The preview card uses it to stop
  // scrollbar paint without giving the page a way to draw the bar back.
  insertCSS(css: string, options?: { cssOrigin?: "user" | "author" }): Promise<string>;
  removeInsertedCSS(key: string): Promise<void>;
  // UI zoom scales rendering only. CDP input coordinates and the layout
  // viewport are CSS-px based and unaffected, so the PiP can shrink the view
  // for display without disturbing the agent's coordinate space.
  setZoomFactor(factor: number): void;
  on(event: string, listener: (...args: unknown[]) => void): void;
  loadURL(url: string, options?: LoadURLOptions): Promise<unknown>;
  getURL(): string;
  getTitle(): string;
  capturePage(rect?: Rectangle, opts?: { stayHidden?: boolean }): Promise<BrowserNativeImageHandle>;
  close(): void;
  isDestroyed(): boolean;
}

export interface BrowserViewHandle {
  readonly webContents: BrowserWebContentsHandle;
  getBounds(): Rectangle;
  setBounds(bounds: Rectangle): void;
  setVisible(visible: boolean): void;
}

// A parent for a WebContentsView: either the hidden host window or a real
// application window during visibility takeover. Only the contentView surface
// (reparent) plus a liveness check are needed here.
export interface BrowserParentWindowHandle {
  readonly contentView: {
    addChildView(view: BrowserViewHandle): void;
    removeChildView(view: BrowserViewHandle): void;
  };
  isDestroyed(): boolean;
}

export interface BrowserHostWindowHandle extends BrowserParentWindowHandle {
  destroy(): void;
}

export interface BrowserHostDeps {
  createHostWindow(): BrowserHostWindowHandle;
  createView(options?: WebContentsViewConstructorOptions): BrowserViewHandle;
  writePng(destPath: string, data: Buffer): void;
  writeJson(destPath: string, data: string): void;
  now?(): number;
}

// The single-shot reply channel back to the core. index.ts wires this to
// appServerClientPool.respondToServerRequest / rejectServerRequest.
export interface BrowserReplyPort {
  respond(id: string, result: unknown): void;
  reject(id: string, message: string): void;
}

// Page identity shown on the preview surface chrome.
export interface BrowserTabSurfaceMeta {
  url: string;
  title: string;
}

// Page CSS coordinates. Move listeners complete when the preview pointer
// arrives; feedback is emitted only after the corresponding input succeeds.
export type BrowserInteractionHint = AgentCursorFeedback
  | { kind: "move"; x: number; y: number }
  | { kind: "clear" };

type BrowserTurn = { turnID: string; executionID: string; ended: boolean };
type BrowserRequest = { cancelled: boolean; workdir: string; threadID: string; turn?: BrowserTurn };

type TabEntry = {
  view: BrowserViewHandle;
  // The view getter clears before a native self-close emits destroyed. Keep
  // identity and liveness independent from that disposable view property.
  contents: BrowserWebContentsHandle;
  contentsID: number;
  workdir: string;
  tabID: string;
  threadID?: string;
  openerTabID?: string;
  creationTurn?: BrowserTurn;
  controlledTurn?: BrowserTurn;
  status: BrowserTabStatus;
  inputGeneration: number;
  pageGeneration: number;
  agentInputAllowed: boolean;
  debuggerAttached: boolean;
  // node_id -> backendNodeId, rebuilt on every observe. node_id is a small
  // incrementing int handed to the model; backendNodeId is the CDP identity we
  // resolve boxes/focus against.
  nodeMap: Map<number, number>;
  // The contentView this tab is currently parented under. Tracked so a reparent
  // can explicitly detach from the previous parent (belt-and-suspenders around
  // Electron's implicit re-parenting).
  currentParent: BrowserParentWindowHandle["contentView"] | undefined;
  // Whether a renderer full-window overlay is currently suppressing this tab's
  // visibility. setVisibility must respect it so an agent promotion cannot paint
  // over a modal; cleared back to visible when the overlay goes away.
  suppressed: boolean;
  // A tab is presented while the workspace panel or a picture-in-picture
  // window owns it. Hidden tabs stay fully active only during an operation.
  presented: boolean;
  // Painted in the workspace panel. Picture-in-picture sets `presented`
  // without setting this, so the mirror can stay up while the panel is closed.
  inPanel: boolean;
  // An explicit hide ignores in-flight rectangles until the panel asks again
  // with force. Otherwise a late bounds report puts the page back on screen.
  blockPresent: boolean;
  agentInputDepth: number;
  ignoreUserInputUntil: number;
  loading: boolean;
  loadError?: string;
  activeOperations: number;
  // Bumps cancel an in-flight scrollbar stylesheet install. The key is the
  // sheet currently hiding scrollbar paint while this tab is on the preview card.
  spectatorScrollbarEpoch: number;
  spectatorScrollbarKey?: string;
};

type BrowserRendererSink = {
  surface: (snapshot: BrowserSurfaceSnapshot) => void;
  userInput: (payload: { workdir: string; tabID: string; threadID?: string; inputGeneration?: number }) => void;
  adopted: (payload: { workdir: string; openerTabID: string; tabID: string; url: string }) => void;
  presented: () => void;
};

type BoundsReport = {
  window: BrowserParentWindowHandle;
  rect: Rectangle;
};

type RawInteractableNode = {
  backendNodeId: number;
  role: string;
  name: string;
  value: string;
  bounds: [number, number, number, number];
};

/**
 * Owns every agent-driven browser tab: a hidden host BrowserWindow plus one
 * WebContentsView per (workdir, tab_id). Requests arrive from the core over the
 * reverse-RPC channel (browser/*), are translated to CDP, and replied to via
 * the injected reply port.
 *
 * Deliberately has NO stop-before-replace lock / retry backoff (unlike the CUA
 * observation coordinator): those exist because replayd groups screen-capture
 * clients by executable and two overlapping helpers fight over one exclusive
 * capture stream. A WebContentsView owns no exclusive OS resource and has no
 * child-process crash surface, so there is nothing to serialize.
 */
export class BrowserHostCoordinator {
  private readonly tabs = new Map<string, TabEntry>();
  private readonly lastBounds = new Map<string, BoundsReport>();
  // webContents ids of every live agent view — the session permission handler
  // consults this to sort agent traffic (deny/controlled) from the user's own
  // <webview> traffic (untouched).
  private readonly agentWebContentsIds = new Set<number>();
  // Workdirs whose core is known-down (crash or teardown). A reply issued here
  // would hit AppServerClient.respond → ensureStarted and RESPAWN a dead core to
  // answer a request nobody awaits, so we swallow it. Cleared when that workdir
  // sends its next request (proof the core is back).
  private readonly downWorkdirs = new Set<string>();
  private hostWindow: BrowserHostWindowHandle | undefined;
  private readonly threadControls = new Map<string, { controller: string; allowed: boolean; updatedAt: string; grantAt?: string; localTakeover?: number }>();
  private readonly pendingRequests = new Map<string, BrowserRequest>();
  private readonly threadTurns = new Map<string, BrowserTurn>();
  // Preview-surface listeners. Interaction hints flow main-side only (never
  // through the core protocol): they mirror actions this coordinator already
  // dispatched, so no model-visible data crosses a new channel.
  private readonly interactionListeners = new Set<
    (workdir: string, tabID: string, hint: BrowserInteractionHint) => Promise<unknown> | void
  >();
  private readonly tabClosedListeners = new Set<(workdir: string, tabID: string) => void>();
  // Navigation and reparent notifications let the observation surface (PiP)
  // keep its chrome label fresh and notice when a visibility takeover adopts
  // the tab it is presenting.
  private readonly navigateListeners = new Set<(workdir: string, tabID: string, url: string) => void>();
  private rendererSink: BrowserRendererSink | undefined;
  private popupSerial = 0;
  private localTakeoverSerial = 0;
  private readonly tabReparentedListeners = new Set<
    (workdir: string, tabID: string, parent: BrowserParentWindowHandle["contentView"] | undefined) => void
  >();

  constructor(
    private readonly registry: WindowRegistry,
    private readonly reply: BrowserReplyPort,
    private readonly deps: BrowserHostDeps,
    // Synthetic invalidation fired when a workdir's core is torn down: the
    // renderer clears that workdir's activitySessions to drop ghost UI. Kept
    // separate from server-exit because the disposing/eviction path suppresses
    // server-exit entirely (appServerClients.ts finalizeChild).
    private readonly broadcastInvalidation: (workdir: string) => void,
  ) {}

  // -------------------------------------------------------------------------
  // Request entry point (index.ts emitServerEvent interception).
  // -------------------------------------------------------------------------
  async handleServerRequest(event: Extract<ServerEvent, { kind: "server-request" }>): Promise<void> {
    const id = event.message.id;
    const method = event.message.method;
    const params = asRecord(event.message.params);
    const workdir = typeof params.workdir === "string" && params.workdir ? params.workdir : event.workdir;
    // Receiving a request is proof this workdir's core is alive again.
    this.downWorkdirs.delete(workdir);
    const threadID = typeof params.thread_id === "string" ? params.thread_id.trim() : "";
    const requestID = typeof params.request_id === "string" ? params.request_id : "";
    const requestKey = requestID ? JSON.stringify([workdir, threadID, requestID]) : undefined;
    const request: BrowserRequest = { cancelled: false, workdir, threadID, turn: this.threadTurns.get(tabKey(workdir, threadID)) };
    if (requestKey) this.pendingRequests.set(requestKey, request);
    try {
      if (!threadID) throw new Error("browser request requires thread_id");
      const result = await this.dispatch(method, workdir, threadID, params, request);
      if (method !== "browser/turn_ended") this.assertRequestCurrent(request);
      this.respondSafe(id, workdir, result);
    } catch (error) {
      this.rejectSafe(id, workdir, error instanceof Error ? error.message : String(error));
    } finally {
      if (requestKey) this.pendingRequests.delete(requestKey);
    }
  }

  handleServerEvent(event: ServerEvent): void {
    if (event.kind !== "notification") return;
    const params = asRecord(event.message.params);
    if (event.message.method === "browser/turn_started") {
      const workdir = typeof params.workdir === "string" ? params.workdir : event.workdir;
      const threadID = typeof params.thread_id === "string" ? params.thread_id : "";
      const turnID = typeof params.turn_id === "string" ? params.turn_id : "";
      const executionID = typeof params.execution_id === "string" ? params.execution_id : "";
      if (!threadID || !turnID || !executionID) return;
      const key = tabKey(workdir, threadID);
      const previous = this.threadTurns.get(key);
      if (previous?.turnID === turnID && previous.executionID === executionID) return;
      this.threadTurns.set(key, { turnID, executionID, ended: false });
      for (const request of this.pendingRequests.values()) {
        if (request.workdir === workdir && request.threadID === threadID) request.cancelled = true;
      }
      for (const entry of this.tabs.values()) {
        if (entry.workdir !== workdir || entry.threadID !== threadID) continue;
        entry.inputGeneration += 1;
        entry.controlledTurn = undefined;
        entry.agentInputAllowed = this.threadControls.get(key)?.allowed !== false;
        this.clearCursor(entry);
      }
      return;
    }
    if (event.message.method !== "browser/request_cancelled") return;
    const request = this.pendingRequests.get(JSON.stringify([params.workdir, params.thread_id, params.request_id]));
    if (request) request.cancelled = true;
  }

  // Called from index.ts on a server-exit event so a reply that lands after the
  // core died is dropped instead of respawning it.
  markServerExit(workdir: string): void {
    this.downWorkdirs.add(workdir);
  }

  // Pool client-teardown sink. NOT server-exit driven: the disposing flag
  // suppresses server-exit on the eviction path, so this hook is the authority
  // for tearing down a workdir's views. Only touches the given workdir's tabs —
  // the pool mints up to 3 cores, each with its own tab_ids, so a global sweep
  // would kill a sibling core's live tab.
  onClientTorndown(workdir: string): void {
    this.downWorkdirs.add(workdir);
    for (const [key, entry] of [...this.tabs]) {
      if (entry.workdir === workdir) {
        this.destroyEntry(entry);
        this.tabs.delete(key);
        this.lastBounds.delete(key);
      }
    }
    for (const key of this.threadControls.keys()) {
      if (key.startsWith(tabKey(workdir, ""))) this.threadControls.delete(key);
    }
    for (const key of this.threadTurns.keys()) {
      if (key.startsWith(tabKey(workdir, ""))) this.threadTurns.delete(key);
    }
    this.broadcastInvalidation(workdir);
  }

  // Pool eviction guard: a workdir with a live agent tab (especially mid user
  // takeover) must count as busy, or an idle-evict would yank the page out from
  // under the user with no warning.
  hasAgentTabs(workdir: string): boolean {
    for (const entry of this.tabs.values()) {
      if (entry.workdir === workdir) return true;
    }
    return false;
  }

  ownsWebContents(webContentsID: number): boolean {
    return this.agentWebContentsIds.has(webContentsID);
  }

  // -------------------------------------------------------------------------
  // Preview-surface accessors (browser PiP). All read-only against the tab;
  // returning undefined signals "tab gone" so the surface can tear down
  // without retrying.
  // -------------------------------------------------------------------------
  addInteractionListener(
    listener: (workdir: string, tabID: string, hint: BrowserInteractionHint) => Promise<unknown> | void,
  ): () => void {
    this.interactionListeners.add(listener);
    return () => this.interactionListeners.delete(listener);
  }

  addTabClosedListener(listener: (workdir: string, tabID: string) => void): () => void {
    this.tabClosedListeners.add(listener);
    return () => this.tabClosedListeners.delete(listener);
  }

  addNavigateListener(listener: (workdir: string, tabID: string, url: string) => void): () => void {
    this.navigateListeners.add(listener);
    return () => this.navigateListeners.delete(listener);
  }

  addTabReparentedListener(
    listener: (workdir: string, tabID: string, parent: BrowserParentWindowHandle["contentView"] | undefined) => void,
  ): () => void {
    this.tabReparentedListeners.add(listener);
    return () => this.tabReparentedListeners.delete(listener);
  }

  // Current DIP bounds of the tab's view — i.e. its layout viewport while the
  // zoom factor is 1. undefined when the tab is gone.
  tabBounds(workdir: string, tabID: string): Rectangle | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return undefined;
    return entry.view.getBounds();
  }

  // Mount the tab onto an observation window (PiP): reparent, then shrink the
  // rendering into `rect` via UI zoom so the layout viewport — and with it
  // every CDP coordinate the agent uses — stays exactly what it was. Returns
  // the pre-mount bounds for restoration on unmount; undefined when the tab
  // is gone.
  mountTabOnWindow(
    workdir: string,
    tabID: string,
    parent: BrowserParentWindowHandle,
    rect: Rectangle,
    zoom: number,
  ): Rectangle | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return undefined;
    const previous = entry.view.getBounds();
    this.reparent(entry, parent);
    const wasPanel = entry.inPanel;
    entry.inPanel = false;
    entry.presented = true;
    entry.contents.setZoomFactor(zoom);
    entry.view.setBounds(rect);
    this.applyEntryActivity(entry);
    // The card is watch-only. Scrollbar paint reads as a control, and the
    // card cannot scroll or take the page over. The sheet comes off when the
    // view leaves the card.
    this.applySpectatorScrollbars(entry);
    if (wasPanel) this.rendererSink?.presented();
    return previous;
  }

  // Re-fit an already-mounted tab after the observation window resized.
  // Ownership-checked like unmountTabIfOwner: a takeover-adopted tab keeps
  // the panel's geometry.
  relayoutMountedTab(
    workdir: string,
    tabID: string,
    owner: BrowserParentWindowHandle["contentView"],
    rect: Rectangle,
    zoom: number,
  ): void {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return;
    if (entry.currentParent !== owner) return;
    entry.contents.setZoomFactor(zoom);
    entry.view.setBounds(rect);
  }

  // Park the tab back on the hidden host — but only when the observation
  // window still owns it. A visibility takeover may have adopted the view in
  // between, and yanking it back would tear the page out of the user's panel.
  unmountTabIfOwner(
    workdir: string,
    tabID: string,
    owner: BrowserParentWindowHandle["contentView"],
    restore: Rectangle,
  ): void {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return;
    if (entry.currentParent !== owner) return;
    this.reparent(entry, this.ensureHostWindow());
    const wasPanel = entry.inPanel;
    entry.inPanel = false;
    entry.presented = false;
    // The card lays the page out at its own size. Handing the view back
    // must not leave that zoom on the hidden host.
    entry.contents.setZoomFactor(1);
    entry.view.setBounds(restore);
    this.applyEntryActivity(entry);
    if (wasPanel) this.rendererSink?.presented();
  }

  tabSurfaceMeta(workdir: string, tabID: string): BrowserTabSurfaceMeta | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return undefined;
    return { url: entry.contents.getURL(), title: entry.contents.getTitle() };
  }

  private async emitInteraction(entry: TabEntry, hint: BrowserInteractionHint): Promise<void> {
    const tasks = [...this.interactionListeners].map(async (listener) => {
      await listener(entry.workdir, entry.tabID, hint);
    });
    if (entry.inPanel && hint.kind !== "move" && hint.kind !== "clear" && !entry.contents.isDestroyed()) {
      tasks.push(entry.contents.executeJavaScript(`window.__wuuAgentCursor?.feedback(${JSON.stringify(hint)})`).then(() => undefined));
    }
    // A preview failure must not fail browser input.
    await Promise.allSettled(tasks);
  }

  private emitTabClosed(entry: TabEntry): void {
    for (const listener of this.tabClosedListeners) {
      try {
        listener(entry.workdir, entry.tabID);
      } catch {
        // Listener teardown races are harmless here.
      }
    }
  }

  private emitNavigate(workdir: string, tabID: string, url: string): void {
    for (const listener of this.navigateListeners) {
      try {
        listener(workdir, tabID, url);
      } catch {
        // A broken preview listener must never fail navigation.
      }
    }
  }

  private emitTabReparented(entry: TabEntry): void {
    for (const listener of this.tabReparentedListeners) {
      try {
        listener(entry.workdir, entry.tabID, entry.currentParent);
      } catch {
        // Listener teardown races are harmless here.
      }
    }
  }

  // -------------------------------------------------------------------------
  // Renderer-reported geometry (visibility takeover positioning).
  // -------------------------------------------------------------------------
  reportBounds(
    workdir: string,
    tabID: string,
    window: BrowserParentWindowHandle,
    cssRect: Rectangle | null,
    zoomFactor: number,
    force = false,
  ): void {
    const target = this.windowForBounds(window);
    if (!target) return;
    const key = tabKey(workdir, tabID);
    // A missing or empty rectangle means the panel is not showing this tab.
    // Drop the cached rect so a later show cannot reuse a stale conversation-
    // covering position. Do not clear blockPresent here: the hide that set it
    // must keep ignoring in-flight rectangles until the panel asks with force.
    if (!cssRect || cssRect.width <= 0 || cssRect.height <= 0) {
      this.lastBounds.delete(key);
      const entry = this.tabs.get(key);
      if (entry && entry.currentParent === target.contentView) {
        this.parkHidden(entry);
      }
      return;
    }
    // Native views use DIP, not the reporting renderer's zoomed CSS pixels.
    const rect = {
      x: Math.round(cssRect.x * zoomFactor),
      y: Math.round(cssRect.y * zoomFactor),
      width: Math.round(cssRect.width * zoomFactor),
      height: Math.round(cssRect.height * zoomFactor),
    };
    this.lastBounds.set(key, { window: target, rect });
    const entry = this.tabs.get(key);
    if (!entry || entry.contents.isDestroyed()) return;
    if (entry.blockPresent && !force) return;
    entry.blockPresent = false;
    this.presentInPanel(entry, target, rect);
  }

  setRendererSink(sink: BrowserRendererSink): void {
    this.rendererSink = sink;
  }

  isInPanel(workdir: string, tabID: string): boolean {
    return this.tabs.get(tabKey(workdir, tabID))?.inPanel === true;
  }

  updateActivity(activity: ActivitySession, eventMethod?: string): void {
    if (activity.kind !== "browser") return;
    const key = tabKey(activity.workdir, activity.thread_id);
    const previous = this.threadControls.get(key);
    // The core emits UTC RFC3339Nano. Preserve all digits: Date.parse would
    // collapse distinct revoke/grant events in the same millisecond.
    const updatedAt = activityTimestamp(activity.updated_at);
    if (previous && updatedAt < previous.updatedAt) return;
    const controlled = activity.controller === "agent" && activity.state !== "stopped";
    // A late metadata snapshot cannot renew revoked authority. Only an
    // explicit control grant can do so, and old pending guards stay invalid.
    const localTakeover = previous?.localTakeover;
    const grantAt = controlled && eventMethod === "activity/control_changed" ? updatedAt : previous?.grantAt;
    const allowed = controlled && localTakeover === undefined && (!previous || previous.allowed || eventMethod === "activity/control_changed");
    this.threadControls.set(key, { controller: activity.controller, allowed, updatedAt, grantAt, localTakeover });
    if (activity.controller === "user" && eventMethod === "activity/control_changed") {
      // An explicit takeover hands the task's live browsing context to the user,
      // including popups that have not yet appeared in the core's tab store.
      for (const entry of this.tabs.values()) {
        if (entry.workdir === activity.workdir && entry.threadID === activity.thread_id) {
          entry.status = "persistent";
          entry.controlledTurn = undefined;
        }
      }
    }
    this.applyThreadAuthority(activity.workdir, activity.thread_id, allowed);
  }

  acknowledgeLocalTakeover(activity: ActivitySession, inputGeneration: number): void {
    if (activity.kind !== "browser") return;
    const key = tabKey(activity.workdir, activity.thread_id);
    const control = this.threadControls.get(key);
    if (!control || control.localTakeover !== inputGeneration) return;
    // Only the response to this click's takeover request clears its latch.
    // A newer release may already have arrived in the same protocol batch.
    const allowed = control.controller === "agent" && control.grantAt !== undefined &&
      control.grantAt > activityTimestamp(activity.updated_at);
    this.threadControls.set(key, { ...control, allowed, localTakeover: undefined });
    this.applyThreadAuthority(activity.workdir, activity.thread_id, allowed);
  }

  private applyThreadAuthority(workdir: string, threadID: string, allowed: boolean): void {
    allowed = allowed && this.threadTurns.get(tabKey(workdir, threadID))?.ended !== true;
    for (const entry of this.tabs.values()) {
      if (entry.workdir !== workdir || entry.threadID !== threadID) continue;
      if (entry.agentInputAllowed !== allowed || !allowed) entry.inputGeneration += 1;
      entry.agentInputAllowed = allowed;
      if (!allowed) this.clearCursor(entry);
    }
  }

  surface(workdir: string, tabID: string): BrowserSurfaceSnapshot | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return undefined;
    return this.snapshotOf(entry);
  }

  async runCommand(
    workdir: string,
    tabID: string,
    command: string,
    url?: string,
    threadID?: string,
  ): Promise<BrowserSurfaceSnapshot | undefined> {
    if (!tabID) return undefined;
    if (command === "navigate") {
      const target = url?.trim() ?? "";
      if (!target) return this.surface(workdir, tabID);
      let entry = this.tabs.get(tabKey(workdir, tabID));
      if (!entry) {
        await this.openTab(workdir, { thread_id: threadID ?? "", tab_id: tabID, initial_url: target }, undefined, "persistent");
        entry = this.tabs.get(tabKey(workdir, tabID));
      } else if (!entry.contents.isDestroyed()) {
        entry.status = "persistent";
        entry.controlledTurn = undefined;
        const current = entry;
        await this.withActiveEntry(current, () => current.contents.loadURL(target));
      }
      if (!entry || entry.contents.isDestroyed()) return undefined;
      this.presentIfCached(entry);
      return this.snapshotOf(entry);
    }
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.contents.isDestroyed()) return undefined;
    entry.status = "persistent";
    entry.controlledTurn = undefined;
    const contents = entry.contents;
    switch (command) {
      case "back":
        if (contents.canGoBack()) contents.goBack();
        break;
      case "forward":
        if (contents.canGoForward()) contents.goForward();
        break;
      case "reload":
        contents.reload();
        break;
      case "stop":
        contents.stop();
        break;
      default:
        return undefined;
    }
    return this.snapshotOf(entry);
  }

  setOverlaySuppressed(workdir: string, tabID: string, suppressed: boolean): void {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry) return;
    // Persist the flag so a concurrent set_visibility promotion respects it
    // instead of painting the agent view back over the modal.
    entry.suppressed = suppressed;
    if (suppressed) this.clearCursor(entry);
    this.applyEntryActivity(entry);
  }

  // -------------------------------------------------------------------------
  // Teardown (before-quit).
  // -------------------------------------------------------------------------
  destroyAll(): void {
    for (const entry of this.tabs.values()) this.destroyEntry(entry);
    this.tabs.clear();
    this.lastBounds.clear();
    this.threadControls.clear();
    this.threadTurns.clear();
    for (const request of this.pendingRequests.values()) request.cancelled = true;
    if (this.hostWindow && !this.hostWindow.isDestroyed()) {
      this.hostWindow.destroy();
    }
    this.hostWindow = undefined;
  }

  // -------------------------------------------------------------------------
  // Method dispatch.
  // -------------------------------------------------------------------------
  private async dispatch(method: string, workdir: string, threadID: string, params: Record<string, JsonValue>, request: BrowserRequest): Promise<unknown> {
    if (method === "browser/turn_ended" || method === "browser/finalize") {
      return this.finalizeTabs(workdir, threadID, params, method === "browser/turn_ended", request);
    }
    this.assertRequestCurrent(request);
    const turn = request.turn;
    if (turn ? turn.ended || params.turn_id !== turn.turnID || params.execution_id !== turn.executionID
      : Boolean(params.turn_id || params.execution_id)) {
      throw new Error("Browser request belongs to an inactive execution");
    }
    const tabID = typeof params.tab_id === "string" ? params.tab_id : "";
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (entry && entry.threadID !== threadID) throw new Error("browser tab belongs to another thread");
    if (method === "browser/open_tab" && this.threadControls.get(tabKey(workdir, threadID))?.allowed === false) {
      throw new Error("Browser control revoked");
    }
    switch (method) {
      case "browser/open_tab":
        return this.openTab(workdir, params, request.turn, "temporary", request);
      case "browser/close_tab":
        return this.closeTab(workdir, params);
      case "browser/list_tabs":
        return this.listTabs(workdir, threadID);
      case "browser/set_visibility":
        return this.setVisibility(workdir, params);
      case "browser/screenshot":
        return this.screenshot(workdir, params, request);
      case "browser/cdp":
        return this.cdpWithGate(workdir, params, request);
      default:
        throw new Error(`unsupported browser method: ${method}`);
    }
  }

  private async openTab(workdir: string, params: Record<string, JsonValue>, turn?: BrowserTurn, status: BrowserTabStatus = "temporary", request?: BrowserRequest): Promise<{ ok: true; tab_id: string }> {
    const tabID = String(params.tab_id ?? "");
    if (!tabID) throw new Error("open_tab requires tab_id");
    const key = tabKey(workdir, tabID);
    let entry = this.tabs.get(key);
    if (!entry) {
      entry = this.registerTab(workdir, tabID, this.deps.createView(),
        typeof params.thread_id === "string" && params.thread_id ? params.thread_id : undefined, { turn, status });
    }
    if (turn) entry.controlledTurn = turn;
    const inputGeneration = entry.inputGeneration;
    const initialURL = typeof params.initial_url === "string" ? params.initial_url : "";
    if (initialURL) {
      await this.withActiveEntry(entry, () => entry.contents.loadURL(initialURL));
    }
    if (request) {
      this.assertRequestCurrent(request);
      if (!entry.agentInputAllowed || entry.inputGeneration !== inputGeneration) throw new Error("Browser control revoked during navigation");
    }
    if (this.tabs.get(key) !== entry || entry.contents.isDestroyed()) throw new Error("Browser tab closed during navigation");
    this.presentIfCached(entry);
    return { ok: true, tab_id: tabID };
  }

  private registerTab(workdir: string, tabID: string, view: BrowserViewHandle, threadID?: string, ownership: { opener?: TabEntry; turn?: BrowserTurn; status?: BrowserTabStatus } = {}): TabEntry {
    // Set a usable layout before the first navigation or script-written popup.
    view.setBounds({ x: 0, y: 0, width: 1280, height: 800 });
    const parent = this.ensureHostWindow().contentView;
    parent.addChildView(view);
    if (!view.webContents.debugger.isAttached()) view.webContents.debugger.attach("1.3");
    const entry: TabEntry = {
      view, contents: view.webContents, contentsID: view.webContents.id, workdir, tabID, threadID, openerTabID: ownership.opener?.tabID,
      creationTurn: ownership.turn, controlledTurn: ownership.turn, status: ownership.status ?? "temporary",
      inputGeneration: 0, pageGeneration: 0,
      agentInputAllowed: this.threadControls.get(tabKey(workdir, threadID ?? ""))?.allowed !== false,
      debuggerAttached: true, nodeMap: new Map(), currentParent: parent,
      suppressed: false, presented: false, inPanel: false, blockPresent: false,
      agentInputDepth: 0, ignoreUserInputUntil: 0, loading: false,
      activeOperations: 0, spectatorScrollbarEpoch: 0,
    };
    this.tabs.set(tabKey(workdir, tabID), entry);
    this.agentWebContentsIds.add(entry.contentsID);
    this.applyEntryActivity(entry);
    this.wireEntry(entry);
    return entry;
  }

  private closeTab(workdir: string, params: Record<string, JsonValue>): { ok: true } {
    const tabID = String(params.tab_id ?? "");
    const key = tabKey(workdir, tabID);
    const entry = this.tabs.get(key);
    if (!entry) return { ok: true }; // idempotent — close of an unknown tab is a no-op
    this.destroyEntry(entry);
    this.tabs.delete(key);
    this.lastBounds.delete(key);
    return { ok: true };
  }

  private listTabs(workdir: string, threadID: string): {
    tab_ids: string[];
    tabs: BrowserListedTab[];
  } {
    const ids: string[] = [];
    const tabs: BrowserListedTab[] = [];
    for (const entry of this.tabs.values()) {
      if (entry.workdir !== workdir || entry.threadID !== threadID) continue;
      ids.push(entry.tabID);
      tabs.push({
        tab_id: entry.tabID,
        status: entry.status,
        url: entry.contents.getURL(),
        title: entry.contents.getTitle(),
      });
    }
    return { tab_ids: ids, tabs };
  }

  private setVisibility(workdir: string, params: Record<string, JsonValue>): { ok: true } {
    const tabID = String(params.tab_id ?? "");
    const entry = this.requireTab(workdir, tabID);
    const visible = params.visible === true;
    if (visible) {
      // Take the view off any mirror immediately so that mirror cannot keep
      // a shrunk zoom, but do not paint it until a panel rectangle arrives.
      // A view with no rectangle would sit at the window origin.
      entry.blockPresent = false;
      const cached = this.lastBounds.get(tabKey(workdir, tabID));
      if (cached && !cached.window.isDestroyed()) {
        this.presentInPanel(entry, cached.window, cached.rect);
      } else {
        const main = this.registry.mainWindow();
        if (main && !main.isDestroyed()) {
          this.reparent(entry, main as unknown as BrowserParentWindowHandle);
        }
        entry.inPanel = false;
        entry.presented = false;
        this.applyEntryActivity(entry);
      }
    } else {
      entry.blockPresent = true;
      this.lastBounds.delete(tabKey(workdir, tabID));
      this.parkHidden(entry);
    }
    return { ok: true };
  }

  private async screenshot(workdir: string, params: Record<string, JsonValue>, request: BrowserRequest): Promise<{ width: number; height: number; path: string }> {
    const entry = this.requireTab(workdir, String(params.tab_id ?? ""));
    const destPath = String(params.dest_path ?? "");
    if (!destPath) throw new Error("screenshot requires dest_path");
    const assertCurrent = this.operationGuard(entry, request);
    assertCurrent();
    this.attachDebugger(entry, assertCurrent);
    const result = await this.withActiveEntry(entry, () => this.captureToFile(entry, destPath, assertCurrent));
    assertCurrent();
    return result;
  }

  // browser/cdp with the >1MB overflow gate applied to the semantic result.
  private async cdpWithGate(
    workdir: string,
    params: Record<string, JsonValue>,
    request: BrowserRequest,
  ): Promise<{ result?: JsonValue; path?: string; size?: number }> {
    const entry = this.requireTab(workdir, String(params.tab_id ?? ""));
    const semanticMethod = String(params.method ?? "");
    const semanticParams = asRecord(params.params);
    const assertCurrent = this.operationGuard(entry, request, semanticMethod === "navigate");
    assertCurrent();
    this.attachDebugger(entry, assertCurrent);
    const output = await this.withActiveEntry(entry, () =>
      this.runSemantic(entry, semanticMethod, semanticParams, assertCurrent),
    );
    assertCurrent();
    const json = JSON.stringify(output ?? null);
    const size = Buffer.byteLength(json, "utf8");
    if (size > MAX_INLINE_RESULT_BYTES) {
      const destPath = typeof semanticParams.dest_path === "string" ? semanticParams.dest_path : "";
      if (destPath) {
        // observe already wrote its PNG to dest_path, so spill the JSON to a
        // sibling to avoid clobbering the screenshot. The core reads whatever
        // path we return, so a derived name is transparent to it.
        const spill = semanticMethod === "observe" ? `${destPath}.json` : destPath;
        this.deps.writeJson(spill, json);
        return { path: spill, size };
      }
      // No dest_path to spill to: inline anyway (still under the 4MB core line
      // limit; the 1MB gate is the conservative early cut).
    }
    return { result: output };
  }

  private async runSemantic(
    entry: TabEntry,
    method: string,
    params: Record<string, JsonValue>,
    assertCurrent: () => void,
  ): Promise<JsonValue> {
    switch (method) {
      case "navigate":
        return this.navigate(entry, params);
      case "observe":
        return this.observe(entry, params, assertCurrent);
      case "click":
        return this.click(entry, params, assertCurrent);
      case "type":
        return this.typeText(entry, params, assertCurrent);
      case "scroll":
        return this.scroll(entry, params, assertCurrent);
      case "key":
        return this.key(entry, params, assertCurrent);
      case "wait":
        return this.wait(params);
      default:
        throw new Error(`unsupported browser action: ${method}`);
    }
  }

  private async navigate(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    const url = String(params.url ?? "");
    if (!url) throw new Error("navigate requires url");
    // loadURL natively resolves on did-finish-load (and rejects on
    // did-fail-load), which is far more robust than racing Page.loadEventFired
    // over the debugger on a hidden view. The wire contract is about the
    // {url,title} result, not the exact CDP verb used to get there.
    await entry.contents.loadURL(url);
    return { url: entry.contents.getURL(), title: entry.contents.getTitle() };
  }

  private async observe(entry: TabEntry, params: Record<string, JsonValue>, assertCurrent: () => void): Promise<JsonValue> {
    const snapshot = await entry.contents.debugger.sendCommand("DOMSnapshot.captureSnapshot", {
      computedStyles: [],
    });
    assertCurrent();
    const raw = interactableNodesFromSnapshot(snapshot);
    const page = pageReadableContent(readableBlocksFromSnapshot(snapshot), contentOffsetParam(params));
    // Rebuild the node map from scratch: node_ids are only valid until the next
    // observe. Content node ids come from this same map.
    entry.nodeMap = new Map();
    const nodeIDByBackend = new Map<number, number>();
    const nodes: JsonValue[] = raw.map((node, index) => {
      const nodeID = index + 1;
      entry.nodeMap.set(nodeID, node.backendNodeId);
      nodeIDByBackend.set(node.backendNodeId, nodeID);
      return {
        node_id: nodeID,
        role: node.role,
        name: node.name,
        value: node.value,
        bounds: node.bounds,
      };
    });
    const content = page.blocks.map((block) => readableBlockJSON(block, nodeIDByBackend));
    const result: Record<string, JsonValue> = {
      url: entry.contents.getURL(),
      title: entry.contents.getTitle(),
      nodes,
      content,
      content_offset: page.offset,
      content_total: page.total,
    };
    if (page.nextOffset !== undefined) result.content_next_offset = page.nextOffset;
    const destPath = typeof params.dest_path === "string" ? params.dest_path : "";
    if (params.screenshot === true && destPath) {
      const shot = await this.captureToFile(entry, destPath, assertCurrent);
      result.screenshot_path = shot.path;
    }
    return result;
  }

  private async click(entry: TabEntry, params: Record<string, JsonValue>, assertCurrent: () => void): Promise<JsonValue> {
    const point = await this.resolvePoint(entry, params);
    await this.glideCursor(entry, point[0], point[1], assertCurrent);
    await this.withAgentInput(entry, async () => {
      const dbg = entry.contents.debugger;
      assertCurrent();
      await dbg.sendCommand("Input.dispatchMouseEvent", {
        type: "mousePressed",
        x: point[0],
        y: point[1],
        button: "left",
        buttons: 1,
        clickCount: 1,
      });
      assertCurrent();
      await dbg.sendCommand("Input.dispatchMouseEvent", {
        type: "mouseReleased",
        x: point[0],
        y: point[1],
        button: "left",
        buttons: 1,
        clickCount: 1,
      });
    });
    void this.emitInteraction(entry, { kind: "click", x: point[0], y: point[1] });
    return { ok: true };
  }

  private async typeText(entry: TabEntry, params: Record<string, JsonValue>, assertCurrent: () => void): Promise<JsonValue> {
    const dbg = entry.contents.debugger;
    let point: [number, number] | undefined;
    if (typeof params.node_id === "number") {
      const backendNodeId = this.requireBackendNode(entry, params.node_id);
      assertCurrent();
      await dbg.sendCommand("DOM.focus", { backendNodeId });
      point = await this.pointForNode(entry, params.node_id).catch(() => undefined);
    }
    const text = String(params.text ?? "");
    if (point) await this.glideCursor(entry, point[0], point[1], assertCurrent);
    await this.withAgentInput(entry, () => {
      assertCurrent();
      return dbg.sendCommand("Input.insertText", { text });
    });
    if (point) void this.emitInteraction(entry, { kind: "type", x: point[0], y: point[1] });
    return { ok: true };
  }

  private async scroll(entry: TabEntry, params: Record<string, JsonValue>, assertCurrent: () => void): Promise<JsonValue> {
    let x = 0;
    let y = 0;
    if (typeof params.node_id === "number") {
      const point = await this.pointForNode(entry, params.node_id);
      [x, y] = point;
    }
    if (typeof params.x === "number" && typeof params.y === "number") {
      x = params.x;
      y = params.y;
    }
    const wheel = wheelDeltas(params.dx, params.dy);
    const aimed = typeof params.node_id === "number" || (typeof params.x === "number" && typeof params.y === "number");
    if (aimed) await this.glideCursor(entry, x, y, assertCurrent);
    await this.withAgentInput(entry, () => {
      assertCurrent();
      return entry.contents.debugger.sendCommand("Input.dispatchMouseEvent", {
        type: "mouseWheel",
        x,
        y,
        deltaX: wheel.dx,
        deltaY: wheel.dy,
      });
    });
    void this.emitInteraction(entry, { kind: "scroll", x, y, direction: scrollDirection(wheel.dx, wheel.dy) });
    return { ok: true };
  }

  private async key(entry: TabEntry, params: Record<string, JsonValue>, assertCurrent: () => void): Promise<JsonValue> {
    const chord = keyChord(params.keys);
    if (!chord) throw new Error("key requires keys");
    const dbg = entry.contents.debugger;
    await this.withAgentInput(entry, async () => {
      for (const stroke of chord) {
        const event = keyDispatch(stroke);
        assertCurrent();
        await dbg.sendCommand("Input.dispatchKeyEvent", { type: "keyDown", ...event });
        if (event.text) {
          assertCurrent();
          await dbg.sendCommand("Input.dispatchKeyEvent", { type: "char", text: event.text, key: event.key });
        }
        assertCurrent();
        await dbg.sendCommand("Input.dispatchKeyEvent", { type: "keyUp", ...event });
      }
    });
    return { ok: true };
  }

  private async wait(params: Record<string, JsonValue>): Promise<JsonValue> {
    // Go splits long waits into <=10s polling slices, so a single slice is
    // bounded here too — never a 30s call that would collide with the
    // per-call reverse-RPC timeout.
    const requested = typeof params.timeout_ms === "number" ? params.timeout_ms : 0;
    const timeout = Math.max(0, Math.min(10_000, requested));
    if (timeout > 0) {
      await new Promise<void>((resolve) => {
        const timer = setTimeout(resolve, timeout);
        timer.unref?.();
      });
    }
    return { ok: true, changed: false };
  }

  private wireEntry(entry: TabEntry): void {
    const contents = entry.contents;
    const publish = (): void => this.publishSurface(entry);
    const onNavigate = (...args: unknown[]): void => {
      entry.pageGeneration += 1;
      entry.nodeMap.clear();
      this.clearCursor(entry);
      const url = typeof args[1] === "string" ? args[1] : contents.getURL();
      this.emitNavigate(entry.workdir, entry.tabID, url);
      publish();
    };
    contents.on("did-start-navigation", (...args: unknown[]) => {
      if (args[3] === false) return;
      entry.pageGeneration += 1;
      entry.nodeMap.clear();
    });
    contents.on("did-navigate", onNavigate);
    contents.on("did-navigate-in-page", onNavigate);
    contents.on("did-start-loading", () => {
      entry.loading = true;
      entry.loadError = undefined;
      publish();
    });
    contents.on("did-stop-loading", () => {
      entry.loading = false;
      publish();
    });
    contents.on("page-title-updated", publish);
    contents.on("did-fail-load", (...args: unknown[]) => {
      const errorCode = typeof args[1] === "number" ? args[1] : 0;
      const isMainFrame = args[4] !== false;
      if (!isMainFrame || errorCode === -3) return;
      entry.loading = false;
      entry.loadError = typeof args[2] === "string" && args[2].length > 0 ? args[2] : "load failed";
      publish();
    });
    // Moving onto the page, or scrolling to watch it, is not using it.
    // Opening the panel under the pointer synthesizes an enter event.
    // A press or a context menu is the user taking the page.
    contents.on("before-mouse-event", (...args: unknown[]) => {
      const mouse = args[1] as { type?: string } | undefined;
      if (mouse?.type !== "mouseDown" && mouse?.type !== "contextMenu") return;
      this.noteUserInput(entry);
    });
    contents.on("before-input-event", (...args: unknown[]) => {
      const input = args[1] as { type?: string } | undefined;
      if (input?.type === "keyDown") this.noteUserInput(entry);
    });
    contents.on("destroyed", () => this.destroyEntry(entry, true));
    contents.setWindowOpenHandler((details) => {
      if (this.tabs.get(tabKey(entry.workdir, entry.tabID)) !== entry || contents.isDestroyed() || this.downWorkdirs.has(entry.workdir)) {
        return { action: "deny" };
      }
      const activeTurn = entry.threadID ? this.threadTurns.get(tabKey(entry.workdir, entry.threadID)) : undefined;
      const originTurn = entry.controlledTurn ?? (entry.status === "temporary" ? entry.creationTurn : undefined);
      const popupTurn = originTurn && !originTurn.ended ? originTurn : undefined;
      const inputGeneration = entry.inputGeneration;
      if ((popupTurn && popupTurn !== activeTurn) || (!popupTurn && originTurn && entry.status === "temporary")) return { action: "deny" };
      const validOpener = (): boolean => this.tabs.get(tabKey(entry.workdir, entry.tabID)) === entry &&
        !contents.isDestroyed() && !this.downWorkdirs.has(entry.workdir) &&
        (!popupTurn || (!popupTurn.ended && entry.agentInputAllowed && entry.inputGeneration === inputGeneration &&
          this.threadTurns.get(tabKey(entry.workdir, entry.threadID ?? "")) === popupTurn));
      if (!validOpener()) return { action: "deny" };
      const target = details.url || "about:blank";
      let protocol: string;
      try { protocol = new URL(target).protocol; } catch { return { action: "deny" }; }
      // Keep page-created content in the browser; never launch OS URL handlers.
      if (!["http:", "https:", "blob:"].includes(protocol) && target !== "about:blank") return { action: "deny" };
      return {
        action: "allow",
        outlivesOpener: true,
        overrideBrowserWindowOptions: { show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } },
        createWindow: (options) => {
          const view = this.deps.createView(options);
          if (!validOpener()) {
            view.webContents.close();
            return view.webContents as WebContents;
          }
          const tabID = `popup-${entry.tabID}-${this.popupSerial++}`;
          const child = this.registerTab(entry.workdir, tabID, view, entry.threadID, { opener: entry, turn: popupTurn, status: popupTurn ? "temporary" : entry.status });
          child.agentInputAllowed = entry.agentInputAllowed;
          if (!child.agentInputAllowed) this.releaseDebugger(child);
          // The supplied child retains Chromium's opener, WindowProxy, and POST
          // navigation. Calling loadURL here would destroy that original context.
          const childOptions = options as WebContentsViewConstructorOptions;
          if (!childOptions.webContents) {
            const post = details.postBody;
            const headers = post ? `Content-Type: ${post.contentType}${post.boundary ? `; boundary=${post.boundary}` : ""}` : undefined;
            void this.withActiveEntry(child, () => view.webContents.loadURL(target, {
              httpReferrer: details.referrer, postData: post?.data, extraHeaders: headers,
            })).catch(() => undefined); // did-fail-load publishes the navigation failure.
          }
          this.rendererSink?.adopted({ workdir: entry.workdir, openerTabID: entry.tabID, tabID, url: target });
          return view.webContents as WebContents;
        },
      };
    });
  }

  private presentInPanel(entry: TabEntry, window: BrowserParentWindowHandle, rect: Rectangle): void {
    this.reparent(entry, window);
    entry.presented = true;
    entry.inPanel = true;
    entry.contents.setZoomFactor(1);
    entry.view.setBounds(rect);
    this.applyEntryActivity(entry);
    this.rendererSink?.presented();
  }

  private presentIfCached(entry: TabEntry): void {
    if (entry.blockPresent || entry.contents.isDestroyed()) return;
    const cached = this.lastBounds.get(tabKey(entry.workdir, entry.tabID));
    if (!cached) return;
    this.presentInPanel(entry, cached.window, cached.rect);
  }

  private parkHidden(entry: TabEntry): void {
    const wasPanel = entry.inPanel;
    this.clearCursor(entry);
    this.reparent(entry, this.ensureHostWindow());
    entry.inPanel = false;
    entry.presented = false;
    this.applyEntryActivity(entry);
    if (wasPanel) this.rendererSink?.presented();
  }

  private operationGuard(entry: TabEntry, request: BrowserRequest, navigates = false): () => void {
    const inputGeneration = entry.inputGeneration;
    const pageGeneration = entry.pageGeneration;
    return () => {
      this.assertRequestCurrent(request);
      if (request.cancelled || !entry.agentInputAllowed || entry.inputGeneration !== inputGeneration ||
        (!navigates && entry.pageGeneration !== pageGeneration) || entry.contents.isDestroyed() ||
        this.tabs.get(tabKey(entry.workdir, entry.tabID)) !== entry) {
        throw new Error("Browser action interrupted: control revoked, request cancelled, or page changed");
      }
      entry.controlledTurn = request.turn;
    };
  }

  private async withAgentInput<T>(entry: TabEntry, run: () => Promise<T>): Promise<T> {
    entry.agentInputDepth += 1;
    entry.ignoreUserInputUntil = Date.now() + 200;
    try {
      return await run();
    } finally {
      entry.agentInputDepth = Math.max(0, entry.agentInputDepth - 1);
      entry.ignoreUserInputUntil = Date.now() + 200;
    }
  }

  private noteUserInput(entry: TabEntry): void {
    if (!entry.inPanel || entry.suppressed) return;
    if (entry.agentInputDepth > 0) return;
    if (Date.now() < entry.ignoreUserInputUntil) return;
    entry.ignoreUserInputUntil = Date.now() + 300;
    entry.status = "persistent";
    entry.controlledTurn = undefined;
    const key = tabKey(entry.workdir, entry.threadID ?? "");
    const control = this.threadControls.get(key);
    // Manual browsing before the first browser activity has no agent lease to
    // revoke. Do not create a tombstone that blocks the task's first action.
    const inputGeneration = control ? ++this.localTakeoverSerial : undefined;
    if (control) this.threadControls.set(key, {
      ...control, allowed: false, localTakeover: inputGeneration,
    });
    for (const owned of this.tabs.values()) {
      if (owned.workdir !== entry.workdir || owned.threadID !== entry.threadID) continue;
      owned.inputGeneration += 1;
      if (control) owned.agentInputAllowed = false;
      this.clearCursor(owned);
    }
    this.rendererSink?.userInput({ workdir: entry.workdir, tabID: entry.tabID, threadID: entry.threadID, inputGeneration });
  }

  // Move the pointer to the action, then let the caller send the input.
  // Hidden pages skip the travel so background work is not delayed. A real
  // user click during the travel cancels the action.
  private async glideCursor(entry: TabEntry, x: number, y: number, assertCurrent: () => void): Promise<void> {
    assertCurrent();
    if (!entry.presented || entry.suppressed || entry.contents.isDestroyed()) return;
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const arrival = entry.inPanel
        ? entry.contents.executeJavaScript(agentCursorCommandScript(x, y))
        : this.emitInteraction(entry, { kind: "move", x, y });
      // Navigation or a stalled renderer can abandon its JS promise. Bound
      // the wait in main as well as in the animation runtime.
      await Promise.race([
        arrival,
        new Promise<void>((resolve) => { timer = setTimeout(resolve, CURSOR_ARRIVE_CAP_MS + 100); }),
      ]);
    } catch {
      // Painting can fail during navigation; input reports its own errors.
    } finally {
      clearTimeout(timer);
    }
    assertCurrent();
  }

  private clearCursor(entry: TabEntry, contentsDestroyed = false): void {
    void this.emitInteraction(entry, { kind: "clear" });
    if (contentsDestroyed || entry.contents.isDestroyed()) return;
    void entry.contents.executeJavaScript(clearAgentCursorScript()).catch(() => undefined);
  }

  private publishSurface(entry: TabEntry): void {
    if (entry.contents.isDestroyed()) return;
    this.rendererSink?.surface(this.snapshotOf(entry));
  }

  private snapshotOf(entry: TabEntry): BrowserSurfaceSnapshot {
    const contents = entry.contents;
    return {
      workdir: entry.workdir,
      tabID: entry.tabID,
      url: contents.getURL(),
      title: contents.getTitle(),
      canGoBack: contents.canGoBack(),
      canGoForward: contents.canGoForward(),
      loading: entry.loading || contents.isLoading(),
      ...(entry.loadError ? { error: entry.loadError } : {}),
    };
  }

  // -------------------------------------------------------------------------
  // Helpers.
  // -------------------------------------------------------------------------
  private applyEntryActivity(entry: TabEntry): void {
    if (this.tabs.get(tabKey(entry.workdir, entry.tabID)) !== entry || entry.contents.isDestroyed()) return;
    const active = entry.presented || entry.activeOperations > 0;
    entry.contents.setBackgroundThrottling(!active);
    entry.view.setVisible(active && !entry.suppressed);
  }

  private async withActiveEntry<T>(entry: TabEntry, operation: () => Promise<T>): Promise<T> {
    entry.activeOperations += 1;
    this.applyEntryActivity(entry);
    try {
      return await operation();
    } finally {
      entry.activeOperations = Math.max(0, entry.activeOperations - 1);
      this.applyEntryActivity(entry);
    }
  }

  private async captureToFile(
    entry: TabEntry,
    destPath: string,
    assertCurrent: () => void,
  ): Promise<{ width: number; height: number; path: string }> {
    assertCurrent();
    // A loaded hidden document may not have submitted its first compositor
    // frame. Wait for rendering while withActiveEntry keeps it unthrottled;
    // never show or focus the native window just to make a preview.
    let frameTimer: ReturnType<typeof setTimeout> | undefined;
    try {
      await Promise.race([
        entry.contents.executeJavaScript("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))"),
        new Promise<never>((_, reject) => {
          frameTimer = setTimeout(() => reject(new Error("Timed out waiting for browser capture frame")), 5000);
        }),
      ]);
    } finally {
      clearTimeout(frameTimer);
    }
    assertCurrent();
    const image = await entry.contents.capturePage(undefined, { stayHidden: true });
    assertCurrent();
    const size = image.getSize();
    const png = image.toPNG();
    // A hidden view can report a successful capture before it has a frame.
    // Writing that result would replace a real preview with an empty file.
    if (size.width <= 0 || size.height <= 0 || png.length === 0) {
      throw new Error("screenshot captured an empty frame");
    }
    this.deps.writePng(destPath, png);
    return { width: size.width, height: size.height, path: destPath };
  }

  private async resolvePoint(entry: TabEntry, params: Record<string, JsonValue>): Promise<[number, number]> {
    if (typeof params.node_id === "number") {
      return this.pointForNode(entry, params.node_id);
    }
    if (typeof params.x === "number" && typeof params.y === "number") {
      return [params.x, params.y];
    }
    throw new Error("action requires node_id or x,y");
  }

  private async pointForNode(entry: TabEntry, nodeID: number): Promise<[number, number]> {
    const backendNodeId = this.requireBackendNode(entry, nodeID);
    const box = await entry.contents.debugger.sendCommand("DOM.getBoxModel", { backendNodeId });
    const center = boxModelCenter(box);
    if (!center) throw new Error(`node_id ${nodeID} has no layout box; observe again`);
    return center;
  }

  private requireBackendNode(entry: TabEntry, nodeID: number): number {
    const backendNodeId = entry.nodeMap.get(nodeID);
    if (backendNodeId === undefined) {
      throw new Error(`node_id ${nodeID} not found; observe before referencing nodes`);
    }
    return backendNodeId;
  }

  private requireTab(workdir: string, tabID: string): TabEntry {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    // Exact sentinel the Go tool matches on to rebuild the tab from its store.
    if (!entry) throw new Error("tab_not_found");
    return entry;
  }

  private ensureHostWindow(): BrowserHostWindowHandle {
    if (!this.hostWindow || this.hostWindow.isDestroyed()) {
      this.hostWindow = this.deps.createHostWindow();
    }
    return this.hostWindow;
  }

  private windowForBounds(window: BrowserParentWindowHandle): BrowserParentWindowHandle | undefined {
    if (!window.isDestroyed()) return window;
    const main = this.registry.mainWindow();
    if (main && !main.isDestroyed()) return main as unknown as BrowserParentWindowHandle;
    return undefined;
  }

  private reparent(entry: TabEntry, target: BrowserParentWindowHandle): void {
    const changed = entry.currentParent !== target.contentView;
    if (changed) this.clearCursor(entry);
    if (entry.currentParent && changed) {
      try {
        entry.currentParent.removeChildView(entry.view);
      } catch {
        // Previous parent may already be gone; addChildView below re-homes it.
      }
    }
    target.contentView.addChildView(entry.view);
    entry.currentParent = target.contentView;
    if (changed) {
      // A parent change is an ownership handoff: the new owner decides the
      // display scale. Normalizing here means a visibility takeover never
      // inherits the PiP's shrink-to-fit zoom or the card's hidden scrollbars.
      entry.contents.setZoomFactor(1);
      this.clearSpectatorScrollbars(entry);
      this.emitTabReparented(entry);
    }
  }

  private applySpectatorScrollbars(entry: TabEntry): void {
    const epoch = ++entry.spectatorScrollbarEpoch;
    void this.installSpectatorScrollbars(entry, epoch);
  }

  private async installSpectatorScrollbars(entry: TabEntry, epoch: number): Promise<void> {
    const contents = entry.contents;
    if (contents.isDestroyed() || entry.spectatorScrollbarEpoch !== epoch) return;
    let overlay = true;
    try {
      overlay = await contents.executeJavaScript(SPECTATOR_SCROLLBAR_PROBE) !== false;
    } catch {
      overlay = true;
    }
    if (contents.isDestroyed() || entry.spectatorScrollbarEpoch !== epoch) return;
    if (!entry.presented || entry.inPanel) return;
    let key = "";
    try {
      key = await contents.insertCSS(spectatorScrollbarCSS(overlay), { cssOrigin: "user" });
    } catch {
      return;
    }
    if (
      contents.isDestroyed()
      || entry.spectatorScrollbarEpoch !== epoch
      || !entry.presented
      || entry.inPanel
    ) {
      if (!contents.isDestroyed() && key) void contents.removeInsertedCSS(key).catch(() => undefined);
      return;
    }
    const previous = entry.spectatorScrollbarKey;
    entry.spectatorScrollbarKey = key;
    if (previous && previous !== key) void contents.removeInsertedCSS(previous).catch(() => undefined);
  }

  private clearSpectatorScrollbars(entry: TabEntry): void {
    entry.spectatorScrollbarEpoch += 1;
    const key = entry.spectatorScrollbarKey;
    entry.spectatorScrollbarKey = undefined;
    const contents = entry.contents;
    if (!key || contents.isDestroyed()) return;
    void contents.removeInsertedCSS(key).catch(() => undefined);
  }

  private assertRequestCurrent(request: BrowserRequest): void {
    if (request.cancelled || request.turn?.ended ||
      this.threadTurns.get(tabKey(request.workdir, request.threadID)) !== request.turn) {
      throw new Error("Browser request belongs to an inactive execution");
    }
  }

  private attachDebugger(entry: TabEntry, assertCurrent: () => void): void {
    assertCurrent();
    if (!entry.contents.debugger.isAttached()) entry.contents.debugger.attach("1.3");
    entry.debuggerAttached = true;
    assertCurrent();
  }

  private releaseDebugger(entry: TabEntry, contentsDestroyed = false): void {
    entry.inputGeneration += 1;
    entry.agentInputAllowed = false;
    entry.controlledTurn = undefined;
    entry.nodeMap.clear();
    this.clearCursor(entry, contentsDestroyed);
    try {
      if (!contentsDestroyed && !entry.contents.isDestroyed() && entry.contents.debugger.isAttached()) entry.contents.debugger.detach();
    } catch {
      // A closing or crashed contents may have already detached its debugger.
    }
    entry.debuggerAttached = false;
  }

  private finalizeTabs(workdir: string, threadID: string, params: Record<string, JsonValue>, ended: boolean, request: BrowserRequest): BrowserTurnEndedResult {
    const turn = this.threadTurns.get(tabKey(workdir, threadID));
    if (!turn || turn.ended || params.turn_id !== turn.turnID || params.execution_id !== turn.executionID) {
      return { closed: [], kept: [], stale: true };
    }
    this.assertRequestCurrent(request);
    const retained = new Map<string, BrowserTabStatus>();
    for (const raw of Array.isArray(params.keep) ? params.keep : []) {
      const keep = asRecord(raw);
      if (typeof keep.tab_id !== "string" || !["persistent", "handoff", "deliverable"].includes(String(keep.status))) {
        throw new Error("Invalid browser keep classification");
      }
      const entry = this.tabs.get(tabKey(workdir, keep.tab_id));
      // A durable tab may not be materialized after a host restart.
      if (!entry) continue;
      if (entry.threadID !== threadID) throw new Error("browser tab belongs to another thread");
      retained.set(entry.tabID, keep.status as BrowserTabStatus);
    }
    const entries = [...this.tabs.values()].filter(entry => entry.workdir === workdir && entry.threadID === threadID);
    for (const entry of entries) {
      if (entry.status !== "temporary" || (ended && params.preserve_all === true)) {
        if (entry.status === "persistent" || !retained.has(entry.tabID)) {
          retained.set(entry.tabID, entry.status === "temporary" ? "persistent" : entry.status);
        }
      }
    }
    // A retained popup still uses its opener's live WindowProxy and message
    // context. Protect that ancestry before closing any temporary siblings.
    for (const id of retained.keys()) {
      let entry = this.tabs.get(tabKey(workdir, id));
      const seen = new Set<string>();
      while (entry?.openerTabID && !seen.has(entry.openerTabID)) {
        seen.add(entry.openerTabID);
        const opener = this.tabs.get(tabKey(workdir, entry.openerTabID));
        if (!opener || opener.threadID !== threadID) break;
        if (!retained.has(opener.tabID)) retained.set(opener.tabID, "persistent");
        entry = opener;
      }
    }
    if (ended) {
      turn.ended = true;
      for (const pending of this.pendingRequests.values()) {
        if (pending !== request && pending.workdir === workdir && pending.threadID === threadID) pending.cancelled = true;
      }
    }
    const closed: string[] = [];
    const kept: string[] = [];
    for (const entry of entries) {
      const status = retained.get(entry.tabID);
      if (!status) {
        closed.push(entry.tabID);
        this.destroyEntry(entry);
      } else {
        entry.status = status;
        kept.push(entry.tabID);
        // Keep the current view, zoom and PiP geometry intact for the user.
        if (ended) this.releaseDebugger(entry);
      }
    }
    return { closed, kept, tabs: this.listTabs(workdir, threadID).tabs };
  }

  private destroyEntry(entry: TabEntry, contentsDestroyed = false): void {
    const key = tabKey(entry.workdir, entry.tabID);
    if (this.tabs.get(key) !== entry) return;
    this.tabs.delete(key);
    this.lastBounds.delete(key);
    entry.inputGeneration += 1;
    entry.pageGeneration += 1;
    entry.agentInputAllowed = false;
    this.agentWebContentsIds.delete(entry.contentsID);
    this.emitTabClosed(entry);
    this.releaseDebugger(entry, contentsDestroyed);
    if (entry.currentParent) {
      try {
        entry.currentParent.removeChildView(entry.view);
      } catch {
        // Parent may already be destroyed.
      }
      entry.currentParent = undefined;
    }
    try {
      if (!contentsDestroyed && !entry.contents.isDestroyed()) {
        entry.contents.close();
      }
    } catch {
      // Already closing.
    }
  }

  private respondSafe(id: string, workdir: string, result: unknown): void {
    if (this.downWorkdirs.has(workdir)) return; // core gone — a reply would respawn it
    try {
      this.reply.respond(id, result);
    } catch {
      // The route was dropped (client disposed/evicted mid-flight). Nothing to answer.
    }
  }

  private rejectSafe(id: string, workdir: string, message: string): void {
    if (this.downWorkdirs.has(workdir)) return;
    try {
      this.reply.reject(id, message);
    } catch {
      // Route already gone.
    }
  }
}

// ---------------------------------------------------------------------------
// Pure helpers (unit-tested directly).
// ---------------------------------------------------------------------------

// The preview card is watch-only: it can be moved and resized, and it cannot
// scroll or take over the page. Scrollbar paint would read as a control.
// Overlay scrollbars do not occupy layout, so they are removed. Classic
// scrollbars do occupy a gutter; that gutter stays and is left unpainted so
// the agent's coordinates do not move. The panel paints scrollbars again.
export function spectatorScrollbarCSS(overlayScrollbars: boolean): string {
  if (overlayScrollbars) {
    return [
      "html, body, * { scrollbar-width: none !important; }",
      "::-webkit-scrollbar { width: 0 !important; height: 0 !important; background: transparent !important; }",
    ].join("\n");
  }
  return [
    "html, body, * { scrollbar-color: transparent transparent !important; }",
    "::-webkit-scrollbar-thumb, ::-webkit-scrollbar-track, ::-webkit-scrollbar-track-piece, ::-webkit-scrollbar-corner, ::-webkit-scrollbar-button {",
    "  background: transparent !important;",
    "  background-color: transparent !important;",
    "  border-color: transparent !important;",
    "  box-shadow: none !important;",
    "}",
  ].join("\n");
}

// True when the OS draws overlay scrollbars. A forced overflow probe reads the
// system mode even when the current page does not scroll yet.
export const SPECTATOR_SCROLLBAR_PROBE = `(function(){
  var parent=document.documentElement||document.body;
  if(!parent) return true;
  var probe=document.createElement("div");
  probe.style.cssText="position:absolute;left:-9999px;top:0;width:120px;height:80px;overflow:scroll;visibility:hidden";
  var inner=document.createElement("div");
  inner.style.height="200px";
  probe.appendChild(inner);
  parent.appendChild(probe);
  var overlay=probe.offsetWidth-probe.clientWidth<2;
  probe.remove();
  return overlay;
})()`;

function activityTimestamp(value: string): string {
  return value.replace(/(?:\.(\d+))?Z$/, (_match, fraction: string | undefined) => `.${(fraction ?? "").padEnd(9, "0")}Z`);
}

// Composite (workdir, tab_id) key. The pool mints up to 3 cores, each of which
// generates its own tab_ids independently, so a bare tab_id can collide across
// cores; the workdir disambiguates. NUL separator can't appear in either part.
export function tabKey(workdir: string, tabID: string): string {
  return `${workdir} ${tabID}`;
}

// Center of a CDP DOM.getBoxModel content quad. quad is
// [x1,y1,x2,y2,x3,y3,x4,y4]; the center is the mean of the four corners.
export function boxModelCenter(box: unknown): [number, number] | undefined {
  const model = isRecord(box) ? box.model : undefined;
  const content = isRecord(model) ? model.content : undefined;
  if (!Array.isArray(content) || content.length < 8) return undefined;
  const xs = [content[0], content[2], content[4], content[6]];
  const ys = [content[1], content[3], content[5], content[7]];
  if (![...xs, ...ys].every((n) => typeof n === "number")) return undefined;
  const x = (xs as number[]).reduce((a, b) => a + b, 0) / 4;
  const y = (ys as number[]).reduce((a, b) => a + b, 0) / 4;
  return [Math.round(x), Math.round(y)];
}

// Dominant-axis direction for a scroll hint; page-space dy>0 scrolls down.
export function scrollDirection(dx: number, dy: number): string {
  if (Math.abs(dy) >= Math.abs(dx)) return dy >= 0 ? "down" : "up";
  return dx >= 0 ? "right" : "left";
}

// One observe stays small enough for the model: a slice of blocks, not the
// whole article. A single paragraph or table is split before paging so the
// next content_offset always lands on a block boundary.
const READABLE_SEGMENT_CHARS = 6000;
const READABLE_SEGMENT_BLOCKS = 20;
const READABLE_PARAGRAPH_CHARS = 1600;
const READABLE_TABLE_ROWS = 12;

const SKIP_CONTENT_TAGS = new Set([
  "script",
  "style",
  "noscript",
  "svg",
  "template",
  "canvas",
  "iframe",
  "head",
  "nav",
  "footer",
]);

const INLINE_CONTENT_TAGS = new Set([
  "a",
  "span",
  "strong",
  "em",
  "b",
  "i",
  "code",
  "small",
  "mark",
  "abbr",
  "time",
  "label",
  "sup",
  "sub",
  "u",
  "s",
  "br",
  "img",
  "wbr",
  "font",
  "cite",
  "q",
  "kbd",
  "samp",
  "var",
  "bdi",
  "bdo",
]);

const PARAGRAPH_TAGS = new Set(["p", "blockquote", "figcaption", "caption", "dt", "dd", "address"]);

export interface ReadablePiece {
  text: string;
  backendNodeId?: number;
}

export interface ReadableBlock {
  kind: "heading" | "paragraph" | "list" | "table";
  level?: number;
  text?: string;
  backendNodeId?: number;
  ordered?: boolean;
  links?: ReadablePiece[];
  items?: ReadablePiece[];
  header?: ReadablePiece[];
  rows?: ReadablePiece[][];
}

interface ContentNode {
  tag: string;
  parent: number;
  children: number[];
  attrs: Record<string, string>;
  backend?: number;
  zeroArea: boolean;
  pieces: string[];
}

// Project one DOM snapshot into readable blocks. Interactable nodes are
// produced from the same snapshot, so a block's backendNodeId addresses one
// of those controls. Navigation and footer chrome stay out of the reading
// text; their controls remain in the interactable list.
export function readableBlocksFromSnapshot(snapshot: unknown): ReadableBlock[] {
  if (!isRecord(snapshot)) return [];
  const strings = Array.isArray(snapshot.strings) ? snapshot.strings : [];
  const str = (index: unknown): string =>
    typeof index === "number" && index >= 0 && index < strings.length && typeof strings[index] === "string"
      ? (strings[index] as string)
      : "";
  const documents = Array.isArray(snapshot.documents) ? snapshot.documents : [];
  const blocks: ReadableBlock[] = [];
  for (const document of documents) {
    if (!isRecord(document)) continue;
    const nodes = buildContentNodes(document, str);
    for (let i = 0; i < nodes.length; i++) {
      const parent = nodes[i].parent;
      if (parent < 0 || parent >= nodes.length || parent === i) collectContent(nodes, i, blocks);
    }
  }
  return splitReadableBlocks(blocks);
}

export function pageReadableContent(
  blocks: ReadableBlock[],
  offset: number,
): { blocks: ReadableBlock[]; offset: number; total: number; nextOffset?: number } {
  const total = blocks.length;
  const start = Number.isFinite(offset) && offset > 0 ? Math.min(Math.floor(offset), total) : 0;
  const page: ReadableBlock[] = [];
  let chars = 0;
  let index = start;
  while (index < total && page.length < READABLE_SEGMENT_BLOCKS) {
    const block = blocks[index];
    const weight = readableBlockChars(block);
    if (page.length > 0 && chars + weight > READABLE_SEGMENT_CHARS) break;
    page.push(block);
    chars += weight;
    index += 1;
  }
  return {
    blocks: page,
    offset: start,
    total,
    ...(index < total ? { nextOffset: index } : {}),
  };
}

function contentOffsetParam(params: Record<string, JsonValue>): number {
  const raw = params.content_offset;
  if (typeof raw !== "number" || !Number.isFinite(raw) || raw <= 0) return 0;
  return Math.floor(raw);
}

function readableBlockJSON(block: ReadableBlock, nodeIDByBackend: Map<number, number>): JsonValue {
  const out: { [key: string]: JsonValue } = { kind: block.kind };
  if (block.level) out.level = block.level;
  if (block.text) out.text = block.text;
  const nodeID = nodeIDFor(block.backendNodeId, nodeIDByBackend);
  if (nodeID) out.node_id = nodeID;
  if (block.ordered) out.ordered = true;
  const links = piecesJSON(block.links, nodeIDByBackend);
  if (links.length > 0) out.links = links;
  const items = piecesJSON(block.items, nodeIDByBackend);
  if (items.length > 0) out.items = items;
  const header = piecesJSON(block.header, nodeIDByBackend);
  if (header.length > 0) out.header = header;
  if (block.rows && block.rows.length > 0) {
    out.rows = block.rows.map((row) => piecesJSON(row, nodeIDByBackend));
  }
  return out;
}

function piecesJSON(pieces: ReadablePiece[] | undefined, nodeIDByBackend: Map<number, number>): JsonValue[] {
  if (!pieces || pieces.length === 0) return [];
  return pieces.map((piece) => {
    const item: { [key: string]: JsonValue } = {};
    if (piece.text) item.text = piece.text;
    const nodeID = nodeIDFor(piece.backendNodeId, nodeIDByBackend);
    if (nodeID) item.node_id = nodeID;
    return item;
  });
}

function nodeIDFor(backend: number | undefined, nodeIDByBackend: Map<number, number>): number | undefined {
  if (backend === undefined) return undefined;
  return nodeIDByBackend.get(backend);
}

function buildContentNodes(document: Record<string, unknown>, str: (index: unknown) => string): ContentNode[] {
  const raw = isRecord(document.nodes) ? document.nodes : {};
  const layout = isRecord(document.layout) ? document.layout : {};
  const nodeName = numberArray(raw.nodeName);
  const parentIndex = numberArray(raw.parentIndex);
  const backendNodeId = numberArray(raw.backendNodeId);
  const attributes = Array.isArray(raw.attributes) ? raw.attributes : [];
  const nodes: ContentNode[] = nodeName.map((name, index) => ({
    tag: str(name).toLowerCase(),
    parent: typeof parentIndex[index] === "number" ? parentIndex[index] : -1,
    children: [],
    attrs: parseSnapshotAttributes(attributes[index], str),
    backend: typeof backendNodeId[index] === "number" ? backendNodeId[index] : undefined,
    zeroArea: false,
    pieces: [],
  }));
  for (let i = 0; i < nodes.length; i++) {
    const parent = nodes[i].parent;
    if (parent >= 0 && parent < nodes.length && parent !== i) nodes[parent].children.push(i);
  }
  const nodeIndex = numberArray(layout.nodeIndex);
  const text = Array.isArray(layout.text) ? layout.text : [];
  const bounds = Array.isArray(layout.bounds) ? layout.bounds : [];
  const positive = new Map<number, boolean>();
  for (let i = 0; i < nodeIndex.length; i++) {
    const idx = nodeIndex[i];
    if (typeof idx !== "number" || idx < 0 || idx >= nodes.length) continue;
    const rect = bounds[i];
    let positiveBox = false;
    if (Array.isArray(rect) && rect.length >= 4 && typeof rect[2] === "number" && typeof rect[3] === "number") {
      if (rect[2] > 0 && rect[3] > 0) {
        positiveBox = true;
        positive.set(idx, true);
      } else if (!positive.has(idx)) {
        positive.set(idx, false);
      }
    }
    const piece = str(text[i]).replace(/\s+/g, " ").trim();
    if (piece && positiveBox && nodes[idx].pieces[nodes[idx].pieces.length - 1] !== piece) {
      nodes[idx].pieces.push(piece);
    }
  }
  for (const [idx, ok] of positive) {
    if (!ok) nodes[idx].zeroArea = true;
  }
  return nodes;
}

function collectContent(nodes: ContentNode[], index: number, blocks: ReadableBlock[]): void {
  const node = nodes[index];
  if (!node || node.tag === "#text" || contentSkipped(node)) return;
  if (isHeadingNode(node)) {
    emitTextBlock(nodes, index, blocks, "heading", headingLevel(node));
    return;
  }
  if (node.tag === "table" || node.attrs.role?.toLowerCase() === "table") {
    emitTable(nodes, index, blocks);
    return;
  }
  if (node.tag === "ul" || node.tag === "ol" || node.attrs.role?.toLowerCase() === "list") {
    emitList(nodes, index, blocks);
    return;
  }
  if (node.tag === "pre" || PARAGRAPH_TAGS.has(node.tag) || node.attrs.role?.toLowerCase() === "paragraph") {
    emitTextBlock(nodes, index, blocks, "paragraph");
    return;
  }
  let run: number[] = [];
  const flush = (): void => {
    if (run.length === 0) return;
    emitInlineRun(nodes, run, blocks, node);
    run = [];
  };
  for (const child of node.children) {
    const current = nodes[child];
    if (current.tag === "#text" || isInlineContent(current)) run.push(child);
    else {
      flush();
      collectContent(nodes, child, blocks);
    }
  }
  flush();
}

function emitTextBlock(
  nodes: ContentNode[],
  index: number,
  blocks: ReadableBlock[],
  kind: "heading" | "paragraph",
  level?: number,
): void {
  const text = descendantContentText(nodes, index);
  if (!text) return;
  const links = contentLinks(nodes, index);
  const block: ReadableBlock = { kind, text };
  if (level) block.level = level;
  assignLinks(block, text, links);
  blocks.push(block);
}

function emitInlineRun(nodes: ContentNode[], run: number[], blocks: ReadableBlock[], parent: ContentNode): void {
  const parts: string[] = [];
  const links: ReadablePiece[] = [];
  for (const index of run) {
    const text = descendantContentText(nodes, index);
    if (text) parts.push(text);
    const node = nodes[index];
    if (isInteractable(node.tag, node.attrs) && typeof node.backend === "number" && text) {
      links.push({ text, backendNodeId: node.backend });
      continue;
    }
    for (const link of contentLinks(nodes, index)) links.push(link);
  }
  const text = parts.join(" ").replace(/\s+/g, " ").trim();
  if (!text) return;
  const block: ReadableBlock = { kind: "paragraph", text };
  // The run's text nodes are not themselves controls. When the whole run is
  // the label of the parent link or button, that parent is the click target.
  if (isInteractable(parent.tag, parent.attrs) && typeof parent.backend === "number") {
    links.unshift({ text, backendNodeId: parent.backend });
  }
  assignLinks(block, text, links);
  blocks.push(block);
}

function assignLinks(block: ReadableBlock, text: string, links: ReadablePiece[]): void {
  const unique = dedupeLinks(links.filter((link) => link.text.length > 0 && link.backendNodeId !== undefined));
  if (unique.length === 1 && unique[0].text === text) {
    block.backendNodeId = unique[0].backendNodeId;
    return;
  }
  if (unique.length > 0) block.links = unique;
}

function emitList(nodes: ContentNode[], index: number, blocks: ReadableBlock[]): void {
  const items: ReadablePiece[] = [];
  const ordered = nodes[index].tag === "ol";
  const extras: number[] = [];
  for (const child of nodes[index].children) {
    const current = nodes[child];
    if (contentSkipped(current)) continue;
    const item = current.tag === "li" || current.attrs.role?.toLowerCase() === "listitem";
    if (!item) {
      extras.push(child);
      continue;
    }
    const text = descendantContentText(nodes, child);
    if (!text) continue;
    const links = contentLinks(nodes, child);
    const piece: ReadablePiece = { text };
    if (links.length === 1) piece.backendNodeId = links[0].backendNodeId;
    items.push(piece);
  }
  if (items.length === 0) {
    for (const child of nodes[index].children) collectContent(nodes, child, blocks);
    return;
  }
  blocks.push({ kind: "list", ordered, items });
  for (const extra of extras) collectContent(nodes, extra, blocks);
}

function emitTable(nodes: ContentNode[], index: number, blocks: ReadableBlock[]): void {
  const rows: ReadablePiece[][] = [];
  let header: ReadablePiece[] | undefined;
  const walk = (current: number): void => {
    const node = nodes[current];
    if (current !== index && (node.tag === "table" || node.attrs.role?.toLowerCase() === "table")) return;
    const row = current !== index && (node.tag === "tr" || node.attrs.role?.toLowerCase() === "row");
    if (row) {
      const cells = rowCells(nodes, current);
      if (cells.length === 0) return;
      if (!header && cells.every((cell) => cell.header)) header = cells.map(stripHeaderFlag);
      else rows.push(cells.map(stripHeaderFlag));
      return;
    }
    for (const child of node.children) walk(child);
  };
  walk(index);
  if (!header && rows.length === 0) return;
  blocks.push({ kind: "table", ...(header ? { header } : {}), rows });
}

function rowCells(
  nodes: ContentNode[],
  index: number,
): Array<ReadablePiece & { header: boolean }> {
  const cells: Array<ReadablePiece & { header: boolean }> = [];
  for (const child of nodes[index].children) {
    const node = nodes[child];
    if (contentSkipped(node)) continue;
    const role = node.attrs.role?.toLowerCase();
    const cell =
      node.tag === "td" ||
      node.tag === "th" ||
      role === "cell" ||
      role === "gridcell" ||
      role === "columnheader" ||
      role === "rowheader";
    if (!cell) continue;
    const text = descendantContentText(nodes, child);
    const links = contentLinks(nodes, child);
    cells.push({
      text,
      ...(links.length === 1 ? { backendNodeId: links[0].backendNodeId } : {}),
      header: node.tag === "th" || role === "columnheader" || role === "rowheader",
    });
  }
  return cells;
}

function stripHeaderFlag(cell: ReadablePiece & { header: boolean }): ReadablePiece {
  return { text: cell.text, ...(cell.backendNodeId !== undefined ? { backendNodeId: cell.backendNodeId } : {}) };
}

function contentLinks(nodes: ContentNode[], index: number): ReadablePiece[] {
  const links: ReadablePiece[] = [];
  const visit = (current: number): void => {
    const node = nodes[current];
    if (current !== index && contentSkipped(node)) return;
    if (isInteractable(node.tag, node.attrs) && typeof node.backend === "number") {
      const text = descendantContentText(nodes, current);
      if (text) links.push({ text, backendNodeId: node.backend });
      return;
    }
    for (const child of node.children) {
      if (nodes[child].tag !== "#text") visit(child);
    }
  };
  visit(index);
  return links;
}

function descendantContentText(nodes: ContentNode[], index: number): string {
  const parts: string[] = [];
  const visit = (current: number): void => {
    const node = nodes[current];
    if (!node || (current !== index && contentSkipped(node))) return;
    parts.push(...node.pieces);
    for (const child of node.children) visit(child);
  };
  visit(index);
  return parts.join(" ").replace(/\s+/g, " ").trim();
}

function contentSkipped(node: ContentNode): boolean {
  if (!node || node.tag === "#text") return node?.zeroArea === true;
  if (SKIP_CONTENT_TAGS.has(node.tag)) return true;
  const role = node.attrs.role?.toLowerCase();
  if (role === "navigation" || role === "contentinfo") return true;
  if (node.attrs["aria-hidden"] === "true" || node.attrs["aria-hidden"] === "") return true;
  if (node.attrs.hidden !== undefined) return true;
  return node.zeroArea;
}

function isInlineContent(node: ContentNode): boolean {
  if (contentSkipped(node)) return false;
  if (INLINE_CONTENT_TAGS.has(node.tag)) return true;
  const role = node.attrs.role?.toLowerCase();
  return role === "link" || role === "button" || role === "presentation" || role === "none";
}

function isHeadingNode(node: ContentNode): boolean {
  return /^h[1-6]$/.test(node.tag) || node.attrs.role?.toLowerCase() === "heading";
}

function headingLevel(node: ContentNode): number {
  const tag = /^h([1-6])$/.exec(node.tag);
  if (tag) return Number(tag[1]);
  const aria = Number(node.attrs["aria-level"]);
  if (Number.isFinite(aria) && aria >= 1 && aria <= 6) return aria;
  return 2;
}

function dedupeLinks(links: ReadablePiece[]): ReadablePiece[] {
  const seen = new Set<string>();
  const out: ReadablePiece[] = [];
  for (const link of links) {
    const key = `${link.backendNodeId ?? ""}:${link.text}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(link);
  }
  return out;
}

function splitReadableBlocks(blocks: ReadableBlock[]): ReadableBlock[] {
  const out: ReadableBlock[] = [];
  for (const block of blocks) {
    if ((block.kind === "paragraph" || block.kind === "heading") && (block.text?.length ?? 0) > READABLE_PARAGRAPH_CHARS) {
      const parts = splitReadableText(block.text ?? "", READABLE_PARAGRAPH_CHARS);
      parts.forEach((text, index) => {
        const next: ReadableBlock = { ...block, text };
        if (index > 0) {
          delete next.links;
          delete next.backendNodeId;
        }
        out.push(next);
      });
      continue;
    }
    if (block.kind === "table" && (block.rows?.length ?? 0) > READABLE_TABLE_ROWS) {
      const rows = block.rows ?? [];
      for (let i = 0; i < rows.length; i += READABLE_TABLE_ROWS) {
        out.push({ kind: "table", ...(block.header ? { header: block.header } : {}), rows: rows.slice(i, i + READABLE_TABLE_ROWS) });
      }
      continue;
    }
    out.push(block);
  }
  return out;
}

function splitReadableText(text: string, limit: number): string[] {
  const parts: string[] = [];
  let rest = text;
  while (rest.length > limit) {
    let cut = rest.lastIndexOf(" ", limit);
    if (cut < Math.floor(limit / 2)) cut = limit;
    const piece = rest.slice(0, cut).trim();
    if (piece) parts.push(piece);
    rest = rest.slice(cut).trim();
  }
  if (rest) parts.push(rest);
  return parts;
}

function readableBlockChars(block: ReadableBlock): number {
  if (block.kind === "list") return (block.items ?? []).reduce((sum, item) => sum + item.text.length, 0);
  if (block.kind === "table") {
    const header = (block.header ?? []).reduce((sum, cell) => sum + cell.text.length, 0);
    const rows = (block.rows ?? []).reduce(
      (sum, row) => sum + row.reduce((rowSum, cell) => rowSum + cell.text.length, 0),
      0,
    );
    return header + rows;
  }
  return block.text?.length ?? 0;
}

const INTERACTABLE_TAGS = new Set([
  "a",
  "button",
  "input",
  "textarea",
  "select",
  "summary",
  "option",
]);

const INTERACTABLE_ROLES = new Set([
  "button",
  "link",
  "textbox",
  "checkbox",
  "radio",
  "combobox",
  "listbox",
  "menuitem",
  "menuitemcheckbox",
  "menuitemradio",
  "option",
  "switch",
  "tab",
  "searchbox",
  "slider",
  "spinbutton",
]);

// Trim a raw DOMSnapshot.captureSnapshot response to interactable, visible
// nodes. Exported and pure so the (large, flattened, string-table) parse is
// unit-tested without a live page. The desktop-side trim is what keeps the
// observe result under the core's line limit.
export function interactableNodesFromSnapshot(snapshot: unknown): RawInteractableNode[] {
  if (!isRecord(snapshot)) return [];
  const strings = Array.isArray(snapshot.strings) ? (snapshot.strings as unknown[]) : [];
  const str = (index: unknown): string =>
    typeof index === "number" && index >= 0 && index < strings.length && typeof strings[index] === "string"
      ? (strings[index] as string)
      : "";
  const documents = Array.isArray(snapshot.documents) ? snapshot.documents : [];
  const out: RawInteractableNode[] = [];
  for (const document of documents) {
    if (!isRecord(document)) continue;
    const nodes = isRecord(document.nodes) ? document.nodes : {};
    const layout = isRecord(document.layout) ? document.layout : {};
    const nodeIndex = numberArray(layout.nodeIndex);
    const bounds = Array.isArray(layout.bounds) ? layout.bounds : [];
    const nodeName = numberArray(nodes.nodeName);
    const backendNodeId = numberArray(nodes.backendNodeId);
    const attributes = Array.isArray(nodes.attributes) ? nodes.attributes : [];
    const textByNode = snapshotTextByNode(document, str);
    const inputValueByNode = new Map<number, string>();
    const iv = nodes.inputValue;
    if (isRecord(iv)) {
      const ivIndex = numberArray(iv.index);
      const ivValue = numberArray(iv.value);
      for (let k = 0; k < ivIndex.length; k++) {
        inputValueByNode.set(ivIndex[k], str(ivValue[k]));
      }
    }
    for (let i = 0; i < nodeIndex.length; i++) {
      const idx = nodeIndex[i];
      const rect = bounds[i];
      if (!Array.isArray(rect) || rect.length < 4) continue;
      const [x, y, w, h] = rect as number[];
      if (typeof w !== "number" || typeof h !== "number" || w <= 0 || h <= 0) continue;
      const tag = str(nodeName[idx]).toLowerCase();
      const attrs = parseSnapshotAttributes(attributes[idx], str);
      if (!isInteractable(tag, attrs)) continue;
      const backend = backendNodeId[idx];
      if (typeof backend !== "number") continue;
      out.push({
        backendNodeId: backend,
        role: roleFor(tag, attrs),
        name: nameFor(tag, attrs, inputValueByNode.get(idx) ?? "", textByNode.get(idx) ?? ""),
        value: valueFor(attrs, inputValueByNode.get(idx) ?? ""),
        bounds: [Math.round(x), Math.round(y), Math.round(w), Math.round(h)],
      });
    }
  }
  return out;
}

// Permission verdict for the persist:wuu-browser session. A non-agent
// webContents (the user's own <webview>) is always granted — the point of
// sorting by ownership is zero regression for the user surface. Agent tabs are
// denied the sensitive capability set.
export function browserPermissionDecision(isAgentOwned: boolean, permission: string): boolean {
  if (!isAgentOwned) return true;
  return !BROWSER_AGENT_DENIED_PERMISSIONS.has(permission);
}

// Wire the persist:wuu-browser session handlers. Called once from
// app.whenReady with the real partition session. Contents this coordinator
// does not own keep the default permission and download path.
export function installBrowserSessionHandlers(session: Session, coordinator: BrowserHostCoordinator): void {
  session.setPermissionRequestHandler((webContents, permission, callback) => {
    const owned = webContents ? coordinator.ownsWebContents(webContents.id) : false;
    callback(browserPermissionDecision(owned, permission));
  });
  session.setPermissionCheckHandler((webContents, permission) => {
    const owned = webContents ? coordinator.ownsWebContents(webContents.id) : false;
    return browserPermissionDecision(owned, permission);
  });
  session.on("will-download", (event, _item, webContents) => {
    // Owned tabs do not write silent downloads to disk.
    if (webContents && coordinator.ownsWebContents(webContents.id)) {
      event.preventDefault();
    }
  });
}

// Configure only the embedded browser partition. The app-server and model
// provider clients use their own transports, so this must not be replaced with
// a process-wide proxy setting. Keep the proxy opt-in: machines without Clash
// should retain Electron's normal direct connection.
export async function configureBrowserProxy(
  session: Pick<Session, "setProxy">,
  rawProxy = process.env.WUU_BROWSER_PROXY,
): Promise<boolean> {
  const proxy = rawProxy?.trim();
  if (!proxy) return false;

  await session.setProxy({ proxyRules: proxy });
  return true;
}

export function defaultBrowserHostDeps(
  createHostWindow: () => BrowserHostWindowHandle,
  createView: (options?: WebContentsViewConstructorOptions) => BrowserViewHandle,
): BrowserHostDeps {
  return {
    createHostWindow,
    createView,
    writePng: (destPath, data) => writeBufferFileAtomicSync(destPath, data),
    writeJson: (destPath, data) => writeTextFileAtomicSync(destPath, data),
  };
}

function isInteractable(tag: string, attrs: Record<string, string>): boolean {
  if (INTERACTABLE_TAGS.has(tag)) return true;
  const role = attrs.role?.toLowerCase();
  if (role && INTERACTABLE_ROLES.has(role)) return true;
  if (attrs.onclick !== undefined) return true;
  if (attrs.tabindex !== undefined && attrs.tabindex !== "-1") return true;
  if (attrs.contenteditable === "" || attrs.contenteditable === "true") return true;
  return false;
}

function roleFor(tag: string, attrs: Record<string, string>): string {
  const explicit = attrs.role?.toLowerCase();
  if (explicit) return explicit;
  switch (tag) {
    case "a":
      return "link";
    case "button":
    case "summary":
      return "button";
    case "textarea":
      return "textbox";
    case "select":
      return "combobox";
    case "option":
      return "option";
    case "input": {
      const type = (attrs.type ?? "text").toLowerCase();
      if (type === "button" || type === "submit" || type === "reset" || type === "image") return "button";
      if (type === "checkbox") return "checkbox";
      if (type === "radio") return "radio";
      if (type === "search") return "searchbox";
      return "textbox";
    }
    default:
      return tag || "generic";
  }
}

function snapshotTextByNode(document: Record<string, unknown>, str: (index: unknown) => string): Map<number, string> {
  const nodes = isRecord(document.nodes) ? document.nodes : {};
  const layout = isRecord(document.layout) ? document.layout : {};
  const nodeIndex = numberArray(layout.nodeIndex);
  const parentIndex = numberArray(nodes.parentIndex);
  const text = Array.isArray(layout.text) ? layout.text : [];
  // CDP indexes text by layout entry, not DOM node. Each entry contains the
  // complete rendered text, so wrapped text boxes must not be stitched again.
  // Text lives on the text node, while the clickable element is its ancestor.
  const textByNode = new Map<number, string>();
  for (let i = 0; i < nodeIndex.length; i++) {
    const piece = str(text[i]).replace(/\s+/g, " ").trim();
    if (!piece) continue;
    const seen = new Set<number>();
    let current = nodeIndex[i];
    while (current >= 0 && !seen.has(current)) {
      seen.add(current);
      const currentText = textByNode.get(current);
      if (!currentText) textByNode.set(current, piece);
      else textByNode.set(current, `${currentText} ${piece}`);
      const parent = parentIndex[current];
      if (typeof parent !== "number" || parent === current) break;
      current = parent;
    }
  }
  return textByNode;
}

function nameFor(tag: string, attrs: Record<string, string>, inputValue: string, text: string): string {
  return (
    attrs["aria-label"] ||
    attrs.placeholder ||
    attrs.alt ||
    attrs.title ||
    attrs.name ||
    (tag === "input" && attrs.type === "submit" ? inputValue : "") ||
    text.trim() ||
    ""
  );
}

// A bare scroll means "down one viewport", not a zero-length wheel event.
export function wheelDeltas(dx: unknown, dy: unknown): { dx: number; dy: number } {
  const x = typeof dx === "number" ? dx : 0;
  const y = typeof dy === "number" ? dy : 0;
  if (x === 0 && y === 0) return { dx: 0, dy: 600 };
  return { dx: x, dy: y };
}

const NAMED_KEYS: Record<string, { code: string; keyCode: number; text?: string }> = {
  Enter: { code: "Enter", keyCode: 13, text: "\r" },
  Tab: { code: "Tab", keyCode: 9 },
  Escape: { code: "Escape", keyCode: 27 },
  Backspace: { code: "Backspace", keyCode: 8 },
  Delete: { code: "Delete", keyCode: 46 },
  ArrowUp: { code: "ArrowUp", keyCode: 38 },
  ArrowDown: { code: "ArrowDown", keyCode: 40 },
  ArrowLeft: { code: "ArrowLeft", keyCode: 37 },
  ArrowRight: { code: "ArrowRight", keyCode: 39 },
  Home: { code: "Home", keyCode: 36 },
  End: { code: "End", keyCode: 35 },
  PageUp: { code: "PageUp", keyCode: 33 },
  PageDown: { code: "PageDown", keyCode: 34 },
  " ": { code: "Space", keyCode: 32, text: " " },
};

// Accept one key or a list. Stringifying an array would send its characters
// instead of the named key.
export function keyChord(raw: unknown): string[] | undefined {
  const values = Array.isArray(raw) ? raw : [raw];
  const keys = values.filter((value): value is string => typeof value === "string" && value.length > 0);
  return keys.length > 0 ? keys : undefined;
}

export function keyDispatch(key: string): {
  key: string;
  code: string;
  windowsVirtualKeyCode: number;
  nativeVirtualKeyCode: number;
  text?: string;
} {
  const named = NAMED_KEYS[key];
  if (named) {
    return {
      key,
      code: named.code,
      windowsVirtualKeyCode: named.keyCode,
      nativeVirtualKeyCode: named.keyCode,
      ...(named.text ? { text: named.text } : {}),
    };
  }
  if (key.length === 1) {
    const upper = key.toUpperCase();
    const letter = upper >= "A" && upper <= "Z" ? upper.charCodeAt(0) : key.charCodeAt(0);
    return {
      key,
      code: upper >= "A" && upper <= "Z" ? `Key${upper}` : "",
      windowsVirtualKeyCode: letter,
      nativeVirtualKeyCode: letter,
      text: key,
    };
  }
  return { key, code: "", windowsVirtualKeyCode: 0, nativeVirtualKeyCode: 0 };
}

export function valueFor(attrs: Record<string, string>, inputValue: string): string {
  // Never surface secret field contents to the model (design section 6 hard
  // boundary). A pattern-based redactor downstream cannot catch an arbitrary
  // password, so the value is dropped at the source: password/hidden inputs and
  // the credential / one-time-code / payment autofill hints.
  const type = (attrs.type || "").toLowerCase();
  const autocomplete = (attrs.autocomplete || "").toLowerCase();
  if (
    type === "password" ||
    type === "hidden" ||
    autocomplete.includes("current-password") ||
    autocomplete.includes("new-password") ||
    autocomplete.includes("one-time-code") ||
    autocomplete.includes("cc-")
  ) {
    return "";
  }
  return inputValue || attrs.value || "";
}

function parseSnapshotAttributes(
  raw: unknown,
  str: (index: unknown) => string,
): Record<string, string> {
  const attrs: Record<string, string> = {};
  if (!Array.isArray(raw)) return attrs;
  for (let i = 0; i + 1 < raw.length; i += 2) {
    const name = str(raw[i]).toLowerCase();
    if (name) attrs[name] = str(raw[i + 1]);
  }
  return attrs;
}

// CDP flattened arrays are index-aligned (nodeName[idx], backendNodeId[idx]),
// so positions must be preserved — filtering out a stray non-number would shift
// every later index. Callers guard element types at the point of use.
function numberArray(value: unknown): number[] {
  return Array.isArray(value) ? (value as number[]) : [];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asRecord(value: unknown): Record<string, JsonValue> {
  return isRecord(value) ? (value as Record<string, JsonValue>) : {};
}
