import type { WebWorkspaceLibraryApi } from "./webWorkspaceLibrary";
import type { WebWorkspaceDownloadCommand, WebWorkspaceDownloadState } from "./webWorkspaceDownloads";

export const WEB_WORKSPACE_TAB_LIMIT = 64;
/** One native tab collection, presented directly in the navigation ribbon. */
export const WEB_WORKSPACE_RIBBON_ID = "ribbon-browser";
export const WEB_WORKSPACE_ZOOM_MIN = 0.25;
export const WEB_WORKSPACE_ZOOM_MAX = 3;

/** CSS pixels: a local rectangle, or edge insets for a window-filling surface. */
export type WebWorkspaceBounds = { x: number; y: number; borderRadius?: number; occluded?: true } & (
  | { width: number; height: number }
  | { right: number; bottom: number }
);
export type WebWorkspaceFindResult = { requestId: number; activeMatchOrdinal: number; matches: number; finalUpdate: boolean };
export type WebWorkspaceFindOptions = { forward?: boolean; findNext?: boolean };

export type WebWorkspaceTabState = {
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
export type WebWorkspaceState = Omit<WebWorkspaceTabState, "zoomFactor"> & {
  zoomFactor?: number;
  tabs?: WebWorkspaceTabState[];
  activeTabId?: string;
  canReopenTab?: boolean;
};

export type WebWorkspaceDestination = { id: string; url: string; tabs?: boolean };
export type WebWorkspaceLegacyDestination = { id: string; url: string; title?: string };
export type WebWorkspaceInitializationState = WebWorkspaceState & { migratedTabGroups?: Record<string, string[]> };
export type WebWorkspaceCommand = "back" | "forward" | "reload" | "reloadIgnoringCache" | "stop" | "navigate" | "focus" | "takeOver";

export type WebWorkspaceMenuAction = "find" | "zoomOut" | "resetZoom" | "zoomIn" | "reopenTab" | "openExternal" | "resetStoragePermission";
export type WebWorkspaceMenuRequest = {
  /** Anchor in shell CSS pixels. */
  x: number;
  y: number;
  labels: Record<Exclude<WebWorkspaceMenuAction, "resetStoragePermission">, string> & { resetStoragePermission?: string };
};

export type WebWorkspaceTooltip = {
  text: string;
  x: number;
  y: number;
  width: number;
  height: number;
  style: { fontFamily: string; fontSize: string; fontWeight: string; fontStyle: string; color: string; backgroundColor: string; border: string; borderRadius: string; padding: string; lineHeight: string };
};

export type WebWorkspacePopoverBounds = { x: number; y: number; width: number; height: number; focus: boolean };

export type WebWorkspaceApi = {
  reportPopover: (token: string, bounds: WebWorkspacePopoverBounds | null) => Promise<void>;
  onPopoverDismiss: (handler: (token: string) => void) => () => void;
  /** Restore the ribbon and migrate legacy website sessions once, without losing their child tabs. */
  initializeTabs: (destinations: WebWorkspaceLegacyDestination[]) => Promise<WebWorkspaceInitializationState>;
  /** The preload and native host understand window-relative edge geometry. */
  supportsWindowInsets?: true;
  /** Reopening retains navigation. Extension pages retain their independent lifetime. */
  open: (destination: WebWorkspaceDestination) => Promise<WebWorkspaceState>;
  /** One lossless, device-resolution backdrop, including an occluded active tab; null when stale, unavailable, or over budget. */
  capture: (id: string) => Promise<string | null>;
  showTooltip: (token: string, hint: WebWorkspaceTooltip | null) => Promise<void>;
  /** Native popup keeps the live page composited; null means dismissed. */
  showOptions: (id: string, request: WebWorkspaceMenuRequest) => Promise<WebWorkspaceMenuAction | null>;
  /** CSS-pixel geometry; occluded retains capture geometry, while null hides the destination. */
  reportBounds: (id: string, bounds: WebWorkspaceBounds | null) => Promise<void>;
  setShortcutTarget: (id: string, active: boolean) => Promise<void>;
  command: (id: string, command: WebWorkspaceCommand, url?: string) => Promise<WebWorkspaceState>;
  createTab: (id: string, url?: string) => Promise<WebWorkspaceState>;
  selectTab: (id: string, tabId: string) => Promise<WebWorkspaceState>;
  closeTab: (id: string, tabId: string) => Promise<WebWorkspaceState>;
  reopenTab: (id: string) => Promise<WebWorkspaceState>;
  setTabPinned: (id: string, tabId: string, pinned: boolean) => Promise<WebWorkspaceState>;
  setTabMuted: (id: string, tabId: string, muted: boolean) => Promise<WebWorkspaceState>;
  reorderTabs: (id: string, tabIds: string[]) => Promise<WebWorkspaceState>;
  find: (id: string, text: string, options?: WebWorkspaceFindOptions) => Promise<WebWorkspaceState>;
  stopFind: (id: string) => Promise<WebWorkspaceState>;
  setZoom: (id: string, factor: number) => Promise<WebWorkspaceState>;
  /** Remove every tab and transient permission decision belonging to a destination. */
  close: (id: string) => Promise<void>;
  onState: (handler: (state: WebWorkspaceState) => void) => () => void;
  onReveal: (handler: (id: string) => void) => () => void;
  /** A new-tab target focuses its central field after the matching state renders. */
  onFocusAddress: (handler: (id: string, newTabId?: string) => void) => () => void;
  onFocusFind: (handler: (id: string) => void) => () => void;
  listDownloads: () => Promise<WebWorkspaceDownloadState[]>;
  downloadCommand: (id: string, command: WebWorkspaceDownloadCommand) => Promise<WebWorkspaceDownloadState[]>;
  onDownloads: (handler: (downloads: WebWorkspaceDownloadState[]) => void) => () => void;
  library: WebWorkspaceLibraryApi;
};
