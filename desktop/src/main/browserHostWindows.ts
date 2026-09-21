import type { Rectangle, Session } from "electron";
import type { BrowserSurfaceSnapshot, JsonValue, ServerEvent } from "../shared/protocol";
import { agentCursorCommandScript, clearAgentCursorScript } from "./agentCursor";
import { writeBufferFileAtomicSync, writeTextFileAtomicSync } from "./atomicFile";
import type { WindowRegistry } from "./windowRegistry";

// One partition for every embedded browser tab. A login performed while the
// agent drives a tab is still there when the user drives that same tab.
// Permission decisions stay per webContents so a deny for these tabs does
// not blanket every other contents on the partition.
export const BROWSER_PARTITION = "persist:wuu-browser";

// Conservative overflow gate. The core stdin scanner enforces a 4MB line
// limit; a DOM snapshot on a large page is 5-50MB before trimming. We trim to
// interactable nodes desktop-side, then still gate the serialized result at
// 1MB and spill to the core-designated dest_path so a pathological page can
// never wedge the whole protocol by overrunning the line limit.
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
  setWindowOpenHandler(handler: (details: { url?: string }) => { action: "deny" } | { action: "allow" }): void;
  canGoBack(): boolean;
  canGoForward(): boolean;
  goBack(): void;
  goForward(): void;
  reload(): void;
  stop(): void;
  isLoading(): boolean;
  executeJavaScript(code: string): Promise<unknown>;
  // UI zoom scales rendering only. CDP input coordinates and the layout
  // viewport are CSS-px based and unaffected, so the PiP can shrink the view
  // for display without disturbing the agent's coordinate space.
  setZoomFactor(factor: number): void;
  on(event: string, listener: (...args: unknown[]) => void): void;
  loadURL(url: string): Promise<unknown>;
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
  createView(): BrowserViewHandle;
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

// Live pointer hint for the preview surface. Coordinates are page CSS pixels,
// the same space click/scroll dispatch in. Emitted only after the input event
// was actually dispatched — a hint never precedes the action it visualizes.
export type BrowserInteractionHint = {
  kind: "click" | "scroll" | "type";
  x: number;
  y: number;
  direction?: string;
};

type TabEntry = {
  view: BrowserViewHandle;
  workdir: string;
  tabID: string;
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
  userInterrupted: boolean;
  loading: boolean;
  loadError?: string;
  activeOperations: number;
};

type BrowserRendererSink = {
  surface: (snapshot: BrowserSurfaceSnapshot) => void;
  userInput: (payload: { workdir: string; tabID: string }) => void;
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
  // Preview-surface listeners. Interaction hints flow main-side only (never
  // through the core protocol): they mirror actions this coordinator already
  // dispatched, so no model-visible data crosses a new channel.
  private readonly interactionListeners = new Set<
    (workdir: string, tabID: string, hint: BrowserInteractionHint) => void
  >();
  private readonly tabClosedListeners = new Set<(workdir: string, tabID: string) => void>();
  // Navigation and reparent notifications let the observation surface (PiP)
  // keep its chrome label fresh and notice when a visibility takeover adopts
  // the tab it is presenting.
  private readonly navigateListeners = new Set<(workdir: string, tabID: string, url: string) => void>();
  private rendererSink: BrowserRendererSink | undefined;
  private popupSerial = 0;
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
    try {
      const result = await this.dispatch(method, workdir, params);
      this.respondSafe(id, workdir, result);
    } catch (error) {
      this.rejectSafe(id, workdir, error instanceof Error ? error.message : String(error));
    }
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
    listener: (workdir: string, tabID: string, hint: BrowserInteractionHint) => void,
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
    if (!entry || entry.view.webContents.isDestroyed()) return undefined;
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
    if (!entry || entry.view.webContents.isDestroyed()) return undefined;
    const previous = entry.view.getBounds();
    this.reparent(entry, parent);
    const wasPanel = entry.inPanel;
    entry.inPanel = false;
    entry.presented = true;
    entry.view.webContents.setZoomFactor(zoom);
    entry.view.setBounds(rect);
    this.applyEntryActivity(entry);
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
    if (!entry || entry.view.webContents.isDestroyed()) return;
    if (entry.currentParent !== owner) return;
    entry.view.webContents.setZoomFactor(zoom);
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
    if (!entry || entry.view.webContents.isDestroyed()) return;
    if (entry.currentParent !== owner) return;
    this.reparent(entry, this.ensureHostWindow());
    const wasPanel = entry.inPanel;
    entry.inPanel = false;
    entry.presented = false;
    entry.view.setBounds(restore);
    this.applyEntryActivity(entry);
    if (wasPanel) this.rendererSink?.presented();
  }

  tabSurfaceMeta(workdir: string, tabID: string): BrowserTabSurfaceMeta | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.view.webContents.isDestroyed()) return undefined;
    return { url: entry.view.webContents.getURL(), title: entry.view.webContents.getTitle() };
  }

  private emitInteraction(entry: TabEntry, hint: BrowserInteractionHint): void {
    for (const listener of this.interactionListeners) {
      try {
        listener(entry.workdir, entry.tabID, hint);
      } catch {
        // A broken preview listener must never fail the action it mirrors.
      }
    }
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
    if (!entry || entry.view.webContents.isDestroyed()) return;
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

  surface(workdir: string, tabID: string): BrowserSurfaceSnapshot | undefined {
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.view.webContents.isDestroyed()) return undefined;
    return this.snapshotOf(entry);
  }

  async runCommand(
    workdir: string,
    tabID: string,
    command: string,
    url?: string,
  ): Promise<BrowserSurfaceSnapshot | undefined> {
    if (!tabID) return undefined;
    if (command === "navigate") {
      const target = url?.trim() ?? "";
      if (!target) return this.surface(workdir, tabID);
      let entry = this.tabs.get(tabKey(workdir, tabID));
      if (!entry) {
        await this.openTab(workdir, { tab_id: tabID, initial_url: target });
        entry = this.tabs.get(tabKey(workdir, tabID));
      } else if (!entry.view.webContents.isDestroyed()) {
        const current = entry;
        await this.withActiveEntry(current, () => current.view.webContents.loadURL(target));
      }
      if (!entry || entry.view.webContents.isDestroyed()) return undefined;
      this.presentIfCached(entry);
      return this.snapshotOf(entry);
    }
    const entry = this.tabs.get(tabKey(workdir, tabID));
    if (!entry || entry.view.webContents.isDestroyed()) return undefined;
    const contents = entry.view.webContents;
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
    this.applyEntryActivity(entry);
  }

  // -------------------------------------------------------------------------
  // Teardown (before-quit).
  // -------------------------------------------------------------------------
  destroyAll(): void {
    for (const entry of this.tabs.values()) this.destroyEntry(entry);
    this.tabs.clear();
    this.lastBounds.clear();
    if (this.hostWindow && !this.hostWindow.isDestroyed()) {
      this.hostWindow.destroy();
    }
    this.hostWindow = undefined;
  }

  // -------------------------------------------------------------------------
  // Method dispatch.
  // -------------------------------------------------------------------------
  private async dispatch(method: string, workdir: string, params: Record<string, JsonValue>): Promise<unknown> {
    switch (method) {
      case "browser/open_tab":
        return this.openTab(workdir, params);
      case "browser/close_tab":
        return this.closeTab(workdir, params);
      case "browser/list_tabs":
        return this.listTabs(workdir);
      case "browser/set_visibility":
        return this.setVisibility(workdir, params);
      case "browser/screenshot":
        return this.screenshot(workdir, params);
      case "browser/cdp":
        return this.cdpWithGate(workdir, params);
      default:
        throw new Error(`unsupported browser method: ${method}`);
    }
  }

  private async openTab(workdir: string, params: Record<string, JsonValue>): Promise<{ ok: true; tab_id: string }> {
    const tabID = String(params.tab_id ?? "");
    if (!tabID) throw new Error("open_tab requires tab_id");
    const key = tabKey(workdir, tabID);
    let entry = this.tabs.get(key);
    if (!entry) {
      const view = this.deps.createView();
      this.ensureHostWindow().contentView.addChildView(view);
      if (!view.webContents.debugger.isAttached()) {
        view.webContents.debugger.attach("1.3");
      }
      entry = {
        view,
        workdir,
        tabID,
        debuggerAttached: true,
        nodeMap: new Map(),
        currentParent: this.ensureHostWindow().contentView,
        suppressed: false,
        presented: false,
        inPanel: false,
        blockPresent: false,
        agentInputDepth: 0,
        ignoreUserInputUntil: 0,
        userInterrupted: false,
        loading: false,
        activeOperations: 0,
      };
      this.applyEntryActivity(entry);
      this.tabs.set(key, entry);
      this.agentWebContentsIds.add(view.webContents.id);
      this.wireEntry(entry);
    }
    const initialURL = typeof params.initial_url === "string" ? params.initial_url : "";
    if (initialURL) {
      await this.withActiveEntry(entry, () => entry.view.webContents.loadURL(initialURL));
    }
    this.presentIfCached(entry);
    return { ok: true, tab_id: tabID };
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

  private listTabs(workdir: string): { tab_ids: string[] } {
    const ids: string[] = [];
    for (const entry of this.tabs.values()) {
      if (entry.workdir === workdir) ids.push(entry.tabID);
    }
    return { tab_ids: ids };
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

  private async screenshot(workdir: string, params: Record<string, JsonValue>): Promise<{ width: number; height: number; path: string }> {
    const entry = this.requireTab(workdir, String(params.tab_id ?? ""));
    const destPath = String(params.dest_path ?? "");
    if (!destPath) throw new Error("screenshot requires dest_path");
    return this.withActiveEntry(entry, () => this.captureToFile(entry, destPath));
  }

  // browser/cdp with the >1MB overflow gate applied to the semantic result.
  private async cdpWithGate(
    workdir: string,
    params: Record<string, JsonValue>,
  ): Promise<{ result?: JsonValue; path?: string; size?: number }> {
    const entry = this.requireTab(workdir, String(params.tab_id ?? ""));
    const semanticMethod = String(params.method ?? "");
    const semanticParams = asRecord(params.params);
    const output = await this.withActiveEntry(entry, () =>
      this.runSemantic(entry, semanticMethod, semanticParams),
    );
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
  ): Promise<JsonValue> {
    switch (method) {
      case "navigate":
        return this.navigate(entry, params);
      case "observe":
        return this.observe(entry, params);
      case "click":
        return this.click(entry, params);
      case "type":
        return this.typeText(entry, params);
      case "scroll":
        return this.scroll(entry, params);
      case "key":
        return this.key(entry, params);
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
    await entry.view.webContents.loadURL(url);
    return { url: entry.view.webContents.getURL(), title: entry.view.webContents.getTitle() };
  }

  private async observe(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    const snapshot = await entry.view.webContents.debugger.sendCommand("DOMSnapshot.captureSnapshot", {
      computedStyles: [],
    });
    const raw = interactableNodesFromSnapshot(snapshot);
    // Rebuild the node map from scratch: node_ids are only valid until the next
    // observe.
    entry.nodeMap = new Map();
    const nodes: JsonValue[] = raw.map((node, index) => {
      const nodeID = index + 1;
      entry.nodeMap.set(nodeID, node.backendNodeId);
      return {
        node_id: nodeID,
        role: node.role,
        name: node.name,
        value: node.value,
        bounds: node.bounds,
      };
    });
    const result: Record<string, JsonValue> = {
      url: entry.view.webContents.getURL(),
      title: entry.view.webContents.getTitle(),
      nodes,
    };
    const destPath = typeof params.dest_path === "string" ? params.dest_path : "";
    if (params.screenshot === true && destPath) {
      const shot = await this.captureToFile(entry, destPath);
      result.screenshot_path = shot.path;
    }
    return result;
  }

  private async click(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    const point = await this.resolvePoint(entry, params);
    if (!(await this.glideCursor(entry, point[0], point[1], true))) return { ok: true };
    await this.withAgentInput(entry, async () => {
      const dbg = entry.view.webContents.debugger;
      await dbg.sendCommand("Input.dispatchMouseEvent", {
        type: "mousePressed",
        x: point[0],
        y: point[1],
        button: "left",
        buttons: 1,
        clickCount: 1,
      });
      await dbg.sendCommand("Input.dispatchMouseEvent", {
        type: "mouseReleased",
        x: point[0],
        y: point[1],
        button: "left",
        buttons: 1,
        clickCount: 1,
      });
    });
    this.emitInteraction(entry, { kind: "click", x: point[0], y: point[1] });
    return { ok: true };
  }

  private async typeText(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    const dbg = entry.view.webContents.debugger;
    let point: [number, number] | undefined;
    if (typeof params.node_id === "number") {
      const backendNodeId = this.requireBackendNode(entry, params.node_id);
      await dbg.sendCommand("DOM.focus", { backendNodeId });
      point = await this.pointForNode(entry, params.node_id).catch(() => undefined);
    }
    const text = String(params.text ?? "");
    if (point && !(await this.glideCursor(entry, point[0], point[1], false))) return { ok: true };
    await this.withAgentInput(entry, () => dbg.sendCommand("Input.insertText", { text }));
    if (point) this.emitInteraction(entry, { kind: "type", x: point[0], y: point[1] });
    return { ok: true };
  }

  private async scroll(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    let x = 0;
    let y = 0;
    if (typeof params.node_id === "number") {
      const point = await this.pointForNode(entry, params.node_id);
      [x, y] = point;
    }
    const dx = typeof params.dx === "number" ? params.dx : 0;
    const dy = typeof params.dy === "number" ? params.dy : 0;
    const aimed = typeof params.node_id === "number" || (typeof params.x === "number" && typeof params.y === "number");
    if (aimed && !(await this.glideCursor(entry, x, y, false))) return { ok: true };
    await this.withAgentInput(entry, () =>
      entry.view.webContents.debugger.sendCommand("Input.dispatchMouseEvent", {
        type: "mouseWheel",
        x,
        y,
        deltaX: dx,
        deltaY: dy,
      }),
    );
    this.emitInteraction(entry, { kind: "scroll", x, y, direction: scrollDirection(dx, dy) });
    return { ok: true };
  }

  private async key(entry: TabEntry, params: Record<string, JsonValue>): Promise<JsonValue> {
    const keys = String(params.keys ?? "");
    if (!keys) throw new Error("key requires keys");
    const dbg = entry.view.webContents.debugger;
    await this.withAgentInput(entry, async () => {
      await dbg.sendCommand("Input.dispatchKeyEvent", { type: "rawKeyDown", key: keys });
      await dbg.sendCommand("Input.dispatchKeyEvent", { type: "keyUp", key: keys });
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
    const contents = entry.view.webContents;
    const publish = (): void => this.publishSurface(entry);
    const onNavigate = (...args: unknown[]): void => {
      const url = typeof args[1] === "string" ? args[1] : contents.getURL();
      this.emitNavigate(entry.workdir, entry.tabID, url);
      publish();
    };
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
    // A page-requested window stays inside this tab set. The native window is
    // refused so the new page keeps the same session and panel.
    contents.setWindowOpenHandler((details) => {
      this.adoptPopup(entry, typeof details?.url === "string" ? details.url : "");
      return { action: "deny" };
    });
  }

  private adoptPopup(opener: TabEntry, url: string): void {
    const target = url.trim();
    if (!target || target === "about:blank") return;
    const tabID = `popup-${opener.tabID}-${this.popupSerial++}`;
    void this.openTab(opener.workdir, { tab_id: tabID, initial_url: target })
      .then(() => {
        this.rendererSink?.adopted({
          workdir: opener.workdir,
          openerTabID: opener.tabID,
          tabID,
          url: target,
        });
      })
      .catch(() => {
        // The opener stays where it is when the new tab cannot be created.
      });
  }

  private presentInPanel(entry: TabEntry, window: BrowserParentWindowHandle, rect: Rectangle): void {
    this.reparent(entry, window);
    entry.presented = true;
    entry.inPanel = true;
    entry.view.webContents.setZoomFactor(1);
    entry.view.setBounds(rect);
    this.applyEntryActivity(entry);
    this.rendererSink?.presented();
  }

  private presentIfCached(entry: TabEntry): void {
    if (entry.blockPresent || entry.view.webContents.isDestroyed()) return;
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
    entry.userInterrupted = true;
    this.clearCursor(entry);
    this.rendererSink?.userInput({ workdir: entry.workdir, tabID: entry.tabID });
  }

  // Move the pointer to the action, then let the caller send the input.
  // Hidden pages skip the travel so background work is not delayed. A real
  // user click during the travel cancels the action.
  private async glideCursor(entry: TabEntry, x: number, y: number, press: boolean): Promise<boolean> {
    if (!entry.inPanel || entry.view.webContents.isDestroyed()) return true;
    if (!Number.isFinite(x) || !Number.isFinite(y)) return true;
    entry.userInterrupted = false;
    try {
      await entry.view.webContents.executeJavaScript(agentCursorCommandScript(x, y, press));
    } catch {
      // The document can be mid-navigation. The action still proceeds.
    }
    return !entry.userInterrupted;
  }

  private clearCursor(entry: TabEntry): void {
    if (entry.view.webContents.isDestroyed()) return;
    void entry.view.webContents.executeJavaScript(clearAgentCursorScript()).catch(() => undefined);
  }

  private publishSurface(entry: TabEntry): void {
    if (entry.view.webContents.isDestroyed()) return;
    this.rendererSink?.surface(this.snapshotOf(entry));
  }

  private snapshotOf(entry: TabEntry): BrowserSurfaceSnapshot {
    const contents = entry.view.webContents;
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
    if (entry.view.webContents.isDestroyed()) return;
    const active = entry.presented || entry.activeOperations > 0;
    entry.view.webContents.setBackgroundThrottling(!active);
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
  ): Promise<{ width: number; height: number; path: string }> {
    // capturePage with stayHidden temporarily bumps the capturer count so a
    // hidden host actually produces a real frame — Page.captureScreenshot over
    // CDP on a non-visible view returns a blank/stale image.
    const image = await entry.view.webContents.capturePage(undefined, { stayHidden: true });
    const size = image.getSize();
    this.deps.writePng(destPath, image.toPNG());
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
    const box = await entry.view.webContents.debugger.sendCommand("DOM.getBoxModel", { backendNodeId });
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
      // inherits the PiP's shrink-to-fit zoom.
      entry.view.webContents.setZoomFactor(1);
      this.emitTabReparented(entry);
    }
  }

  private destroyEntry(entry: TabEntry): void {
    this.agentWebContentsIds.delete(entry.view.webContents.id);
    this.emitTabClosed(entry);
    if (entry.debuggerAttached) {
      try {
        if (entry.view.webContents.debugger.isAttached()) {
          entry.view.webContents.debugger.detach();
        }
      } catch {
        // Debugger auto-detaches on crash/navigation; ignore.
      }
      entry.debuggerAttached = false;
    }
    if (entry.currentParent) {
      try {
        entry.currentParent.removeChildView(entry.view);
      } catch {
        // Parent may already be destroyed.
      }
      entry.currentParent = undefined;
    }
    try {
      if (!entry.view.webContents.isDestroyed()) {
        entry.view.webContents.close();
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
        name: nameFor(tag, attrs, inputValueByNode.get(idx) ?? ""),
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
  createView: () => BrowserViewHandle,
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

function nameFor(tag: string, attrs: Record<string, string>, inputValue: string): string {
  return (
    attrs["aria-label"] ||
    attrs.placeholder ||
    attrs.alt ||
    attrs.title ||
    attrs.name ||
    (tag === "input" && attrs.type === "submit" ? inputValue : "") ||
    ""
  );
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
