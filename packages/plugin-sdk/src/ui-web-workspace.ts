type Immutable<T> = T extends object ? { readonly [K in keyof T]: Immutable<T[K]> } : T;

/** CSS pixels: a local rectangle, or edge insets for a window-filling surface. */
type WebWorkspaceBounds = { x: number; y: number; borderRadius?: number; occluded?: true } & (
  | { width: number; height: number }
  | { right: number; bottom: number }
);
type WebWorkspaceFindResult = { requestId: number; activeMatchOrdinal: number; matches: number; finalUpdate: boolean };
type WebWorkspaceFindOptions = { forward?: boolean; findNext?: boolean };

type WebWorkspaceTabState = {
  discarded?: true;
  id: string;
  /** Former identity lets the ribbon reclaim its mixed navigation slot. Not persisted. */
  reopenedFrom?: string;
  url: string;
  title: string;
  loading: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  zoomFactor: number;
  pinned?: boolean;
  audible?: boolean;
  muted?: boolean;
  favicon?: string;
  /** False for a live about:blank popup, which may contain script-written content. */
  startPage?: boolean;
  agentControlled?: boolean;
  findResult?: WebWorkspaceFindResult;
  error?: string;
};

/** Navigation fields describe the active tab; id always names the destination. */
type WebWorkspaceState = Omit<WebWorkspaceTabState, "zoomFactor"> & {
  zoomFactor?: number;
  tabs?: WebWorkspaceTabState[];
  activeTabId?: string;
  canReopenTab?: boolean;
};

type WebWorkspaceDestination = { id: string; url: string; tabs?: boolean };
type WebWorkspaceLegacyDestination = { id: string; url: string; title?: string };
type WebWorkspaceInitializationState = WebWorkspaceState & { migratedTabGroups?: Record<string, string[]> };
type WebWorkspaceCommand = "back" | "forward" | "reload" | "reloadIgnoringCache" | "stop" | "navigate" | "focus" | "takeOver";

type WebWorkspaceMenuAction = "find" | "zoomOut" | "resetZoom" | "zoomIn" | "reopenTab" | "openExternal";
type WebWorkspaceMenuRequest = {
  /** Anchor in shell CSS pixels. */
  x: number;
  y: number;
  labels: Record<WebWorkspaceMenuAction, string>;
};

type WebWorkspaceTooltip = {
  text: string;
  x: number;
  y: number;
  width: number;
  height: number;
  style: { fontFamily: string; fontSize: string; fontWeight: string; fontStyle: string; color: string; backgroundColor: string; border: string; borderRadius: string; padding: string; lineHeight: string };
};

type WebWorkspacePopoverBounds = { x: number; y: number; width: number; height: number; focus: boolean };

type WebWorkspaceHistoryEntry = {
  id: string;
  url: string;
  title: string;
  visitedAt: number;
};

type WebWorkspaceBookmark = {
  id: string;
  url: string;
  title: string;
  createdAt: number;
};

type WebWorkspaceLibraryState = {
  history: WebWorkspaceHistoryEntry[];
  bookmarks: WebWorkspaceBookmark[];
  error?: string;
};

type WebWorkspaceDownloadState = {
  id: string;
  websiteId: string;
  filename: string;
  savePath: string;
  receivedBytes: number;
  totalBytes: number;
  state: "progressing" | "completed" | "cancelled" | "interrupted";
  paused: boolean;
  canResume: boolean;
  done: boolean;
  startedAt: number;
  /** The record remains usable in memory but cannot be saved in download history. */
  persistenceError?: "read" | "write";
};

type WebWorkspaceDownloadCommand = "pause" | "resume" | "cancel" | "open" | "reveal";

export type WebWorkspaceBoundsV1 = Immutable<WebWorkspaceBounds>;
export type WebWorkspaceFindResultV1 = Immutable<WebWorkspaceFindResult>;
export type WebWorkspaceFindOptionsV1 = Immutable<WebWorkspaceFindOptions>;
export type WebWorkspaceTabStateV1 = Immutable<WebWorkspaceTabState>;
export type WebWorkspaceStateV1 = Immutable<WebWorkspaceState>;
export type WebWorkspaceDestinationV1 = Immutable<WebWorkspaceDestination>;
export type WebWorkspaceLegacyDestinationV1 = Immutable<WebWorkspaceLegacyDestination>;
export type WebWorkspaceInitializationStateV1 = Immutable<WebWorkspaceInitializationState>;
export type WebWorkspaceCommandV1 = Immutable<WebWorkspaceCommand>;
export type WebWorkspaceMenuActionV1 = Immutable<WebWorkspaceMenuAction>;
export type WebWorkspaceMenuRequestV1 = Immutable<WebWorkspaceMenuRequest>;
export type WebWorkspaceTooltipV1 = Immutable<WebWorkspaceTooltip>;
export type WebWorkspacePopoverBoundsV1 = Immutable<WebWorkspacePopoverBounds>;
export type WebWorkspaceHistoryEntryV1 = Immutable<WebWorkspaceHistoryEntry>;
export type WebWorkspaceBookmarkV1 = Immutable<WebWorkspaceBookmark>;
export type WebWorkspaceLibraryStateV1 = Immutable<WebWorkspaceLibraryState>;
export type WebWorkspaceDownloadStateV1 = Immutable<WebWorkspaceDownloadState>;
export type WebWorkspaceDownloadCommandV1 = Immutable<WebWorkspaceDownloadCommand>;

export type WebWorkspaceLibraryApiV1 = {
  getState: () => Promise<WebWorkspaceLibraryStateV1>;
  setBookmark: (url: string, title: string, bookmarked: boolean) => Promise<WebWorkspaceLibraryStateV1>;
  removeHistory: (id: string) => Promise<WebWorkspaceLibraryStateV1>;
  clearHistory: () => Promise<WebWorkspaceLibraryStateV1>;
  onState: (handler: (state: WebWorkspaceLibraryStateV1) => void) => () => void;
};


export interface ApplicationWebWorkspaceV1 {
  reportPopover: (token: string, bounds: WebWorkspacePopoverBoundsV1 | null) => Promise<void>;
  onPopoverDismiss: (handler: (token: string) => void) => () => void;
  /** Restore the ribbon and migrate legacy website sessions once, without losing their child tabs. */
  initializeTabs: (destinations: readonly WebWorkspaceLegacyDestinationV1[]) => Promise<WebWorkspaceInitializationStateV1>;
  /** The preload and native host understand window-relative edge geometry. */
  readonly capabilities: { readonly available: boolean; readonly windowInsets: boolean; readonly nativeOverlays: boolean };
  readonly platform: string;
  readonly limits: { readonly tabs: number; readonly zoomMin: number; readonly zoomMax: number };
  /** Child owners close their pages, release presentation and unsubscribe on disposal. */
  fork(): ApplicationWebWorkspaceV1;
  dispose(): Promise<void>;
  openExternal(url: string): Promise<void>;
  dismissOptions(id: string): Promise<void>;
  /** Reopening retains navigation. Extension pages retain their independent lifetime. */
  open: (destination: WebWorkspaceDestinationV1) => Promise<WebWorkspaceStateV1>;
  /** One lossless, device-resolution backdrop, including an occluded active tab; null when stale, unavailable, or over budget. */
  capture: (id: string) => Promise<string | null>;
  showTooltip: (token: string, hint: WebWorkspaceTooltipV1 | null) => Promise<void>;
  /** Native popup keeps the live page composited; null means dismissed. */
  showOptions: (id: string, request: WebWorkspaceMenuRequestV1) => Promise<WebWorkspaceMenuActionV1 | null>;
  /** CSS-pixel geometry; occluded retains capture geometry, while null hides the destination. */
  reportBounds: (id: string, bounds: WebWorkspaceBoundsV1 | null) => Promise<void>;
  setShortcutTarget: (id: string, active: boolean) => Promise<void>;
  command: (id: string, command: WebWorkspaceCommandV1, url?: string) => Promise<WebWorkspaceStateV1>;
  createTab: (id: string, url?: string) => Promise<WebWorkspaceStateV1>;
  selectTab: (id: string, tabId: string) => Promise<WebWorkspaceStateV1>;
  closeTab: (id: string, tabId: string) => Promise<WebWorkspaceStateV1>;
  reopenTab: (id: string) => Promise<WebWorkspaceStateV1>;
  setTabPinned: (id: string, tabId: string, pinned: boolean) => Promise<WebWorkspaceStateV1>;
  setTabMuted: (id: string, tabId: string, muted: boolean) => Promise<WebWorkspaceStateV1>;
  reorderTabs: (id: string, tabIds: readonly string[]) => Promise<WebWorkspaceStateV1>;
  find: (id: string, text: string, options?: WebWorkspaceFindOptionsV1) => Promise<WebWorkspaceStateV1>;
  stopFind: (id: string) => Promise<WebWorkspaceStateV1>;
  setZoom: (id: string, factor: number) => Promise<WebWorkspaceStateV1>;
  /** Release this owner's destination. Owned pages close; restored user collections retain their tabs. */
  close: (id: string) => Promise<void>;
  onState: (handler: (state: WebWorkspaceStateV1) => void) => () => void;
  onReveal: (handler: (id: string) => void) => () => void;
  /** A new-tab target focuses its central field after the matching state renders. */
  onFocusAddress: (handler: (id: string, newTabId?: string) => void) => () => void;
  onFocusFind: (handler: (id: string) => void) => () => void;
  listDownloads: () => Promise<readonly WebWorkspaceDownloadStateV1[]>;
  downloadCommand: (id: string, command: WebWorkspaceDownloadCommandV1) => Promise<readonly WebWorkspaceDownloadStateV1[]>;
  onDownloads: (handler: (downloads: readonly WebWorkspaceDownloadStateV1[]) => void) => () => void;
  library: WebWorkspaceLibraryApiV1;
};

/** Own the source fork before admitting work, so disposal retains pending acquisitions. */
export function scopeApplicationWebWorkspace(source: () => ApplicationWebWorkspaceV1, signal: AbortSignal, assertActive: () => void): ApplicationWebWorkspaceV1 {
  let owned: ApplicationWebWorkspaceV1 | undefined;
  let disposal: Promise<void> | undefined;
  let disposed = false;
  const check = () => { if(disposed) throw new DOMException("Web workspace scope disposed", "AbortError"); assertActive(); signal.throwIfAborted(); };
  const get = () => { if(!owned) {check();owned=source().fork();} return owned; };
  const dispose = () => { disposed=true; return disposal ??= (owned?.dispose() ?? Promise.resolve()).catch(error=>{disposal=undefined;throw error;}); };
  signal.addEventListener("abort", () => { void dispose().catch(error => console.error("Web workspace cleanup failed", error)); }, {once:true});
  const bind = <T extends object>(getTarget: () => T): T => new Proxy({} as T, {
    get(_target,key) {
      if (key === "dispose") return dispose;
      if (key === "fork") return () => { check(); return scopeApplicationWebWorkspace(get,signal,assertActive); };
      if (key === "library") { check(); return library; }
      const cleanup = (args: unknown[]) => key === "close" || key === "dismissOptions"
        || ((key === "reportBounds" || key === "reportPopover" || key === "showTooltip") && args[1] === null)
        || (key === "setShortcutTarget" && args[1] === false);
      // Reading capabilities does not create a native page or choose a fallback.
      const value = Reflect.get(getTarget(),key);
      if (typeof value !== "function") { check(); return value; }
      if (String(key).startsWith("on")) return (listener: (...args: unknown[]) => void) => {
        check(); return value.call(getTarget(),(...args:unknown[])=>{if(!signal.aborted && !disposed) {check(); listener(...args);} });
      };
      return async (...args:unknown[]) => {
        const releasing = cleanup(args);
        if (!releasing) check();
        const result = await value.apply(getTarget(),args);
        if (!releasing) check();
        return result;
      };
    },
  });
  const library = bind(() => get().library);
  return bind(get);
}
