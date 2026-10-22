import { BrowserWindow, Menu, clipboard, dialog, shell, webContents, type DisplayMediaRequestHandlerHandlerRequest, type Streams, type DownloadItem, type WebContents, type WebContentsView } from "electron";
import { chooseBrowserDisplaySource } from "./browserDisplayCapture";
import { getPersistentStoragePermission, setPersistentStoragePermission } from "./desktopSettings";
import { randomUUID } from "node:crypto";
import { join } from "node:path";
import type { NavigationEntry } from "electron";
import { WEB_WORKSPACE_ZOOM_MIN, WEB_WORKSPACE_ZOOM_MAX, WEB_WORKSPACE_TAB_LIMIT, WEB_WORKSPACE_RIBBON_ID } from "../shared/webWorkspace";
import type { WebWorkspaceMenuAction, WebWorkspaceMenuRequest, WebWorkspaceBounds, WebWorkspaceCommand, WebWorkspaceDestination, WebWorkspaceLegacyDestination, WebWorkspaceInitializationState, WebWorkspaceFindOptions, WebWorkspaceFindResult, WebWorkspaceState, WebWorkspaceTabState } from "../shared/webWorkspace";
import { BROWSER_PARTITION, BROWSER_WORKSPACE_TAB_PREFIX, type BrowserWorkspaceSource, type BrowserWorkspaceAttachment, type BrowserViewHandle, type BrowserParentWindowHandle, type BrowserPermissionRequestDetails, type BrowserSessionPermissionDelegate } from "./browserHostWindows";
import { createBrowserView } from "./browserView";
import { captureVisiblePage, safePageURL, type PageContextSnapshot } from "../shared/pageContext";
import type { WebWorkspaceLibrary } from "./webWorkspaceLibrary";
import type { WebWorkspaceDownloads } from "./webWorkspaceDownloads";

// Website pages keep their native ownership. Explicit, turn-scoped automation
// borrows a live view; restored tabs create native views only when selected.
type WorkspaceTab = { id: string; reopenedFrom?: string; url: string; title: string; zoomFactor: number; pinned?: boolean; muted?: boolean; favicon?: string; startPage?: boolean; page?: WorkspacePage;
  discarded?: { entries: NavigationEntry[]; index: number; scroll?: { x: number; y: number } } };
type WorkspaceGroup = {
  id: string;
  owner: BrowserWindow;
  tabbed: boolean;
  homeURL: string;
  tabs: WorkspaceTab[];
  activeTabId: string;
  bounds: WebWorkspaceBounds | null;
  lastBounds?: WebWorkspaceBounds;
  closedTabs: { id: string; index: number; url: string; title: string; zoomFactor: number; pinned?: boolean; muted?: boolean; discarded?: WorkspaceTab["discarded"] }[];
  migratedTabGroups?: Record<string, string[]>;
};
type AutomationRestriction = { active: boolean };
type WorkspacePage = {
  id: string;
  group: WorkspaceGroup;
  tab: WorkspaceTab;
  owner: BrowserWindow;
  view: WebContentsView;
  contents: WebContents;
  bounds: WebWorkspaceBounds | null;
  borderRadius: number;
  htmlFullscreen?: boolean;
  requestedURL: string;
  navigationGeneration: number;
  error?: string;
  findResult?: WebWorkspaceFindResult;
  findRequestId?: number;
  pendingClose?: { promise: Promise<void>; resolve: () => void; navigation?: WorkspaceTab["discarded"] };
  popups: Set<BrowserWindow>;
  automation?: { restriction: AutomationRestriction; reclaim(): void; layout(): void; agentInput(): boolean; external: boolean };
  inheritedAutomation?: readonly AutomationRestriction[];
  inactiveSince?: number;
  discardProtected?: boolean;
  discarding?: boolean;
};
type PermissionOwner = { page: WorkspacePage; grants: Set<string>; navigationGeneration: number };

// Files, individual devices, and external apps always ask again. Camera and
// microphone grants are separate and scoped to this tab and both page origins.
// Notifications are unsupported: Electron may check them without a contents
// owner, and this shared profile cannot safely grant unattributed access.
const TAB_PERMISSIONS = new Set(["geolocation", "geolocation-approximate", "fullscreen", "screen-wake-lock"]);

function permissionGrantKeys(permission: string, mediaTypes: readonly string[] = []): string[] {
  if (permission === "media") {
    // An unspecified device must never borrow a microphone or camera grant.
    return mediaTypes.length && mediaTypes.every(type => type === "audio" || type === "video")
      ? [...new Set(mediaTypes)].map(type => `media:${type}`) : [];
  }
  return TAB_PERMISSIONS.has(permission) ? [permission] : [];
}
const ZOOM_LEVELS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3];
// Preserve Retina/5K page pixels without allowing an unusually large display
// or high-entropy page to create an unbounded encode or renderer IPC payload.
const MAX_BACKDROP_PIXELS = 16 * 1024 * 1024;
const MAX_BACKDROP_BYTES = 12 * 1024 * 1024;
const IDLE_TAB_TIMEOUT = 30 * 60 * 1000;

function websiteURL(value: string): string {
  const url = new URL(value);
  if (url.protocol !== "https:" && url.protocol !== "http:") throw new Error("Website destinations support HTTP and HTTPS addresses only.");
  if (url.username || url.password) throw new Error("Website addresses cannot include credentials.");
  return url.href;
}
function requestOrigin(value: string): string | undefined {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? url.origin : undefined;
  } catch { return undefined; }
}

function contextDownloadReferrer(params: Electron.ContextMenuParams, target: string): string {
  const source = new URL(params.frameURL || params.pageURL);
  const destination = new URL(target);
  if (!/^https?:$/.test(source.protocol) || !/^https?:$/.test(destination.protocol)) return "";
  source.username = "";
  source.password = "";
  source.hash = "";
  const downgrade = source.protocol === "https:" && destination.protocol === "http:";
  const origin = `${source.origin}/`;
  // downloadURL cannot propagate the frame's policy across redirects. Omit
  // same-origin-only referrers and trim paths for origin/strict policies so a
  // redirect cannot disclose more than the original document permits.
  // Electron 44 emits a string here; remove String() when its ContextMenuParams type is corrected.
  switch (String(params.referrerPolicy)) {
    case "no-referrer":
    case "same-origin": return "";
    case "unsafe-url": return source.href;
    case "no-referrer-when-downgrade": return downgrade ? "" : source.href;
    case "origin":
    case "origin-when-cross-origin": return origin;
    default: return downgrade ? "" : origin;
  }
}

export class WebWorkspaceHost implements BrowserSessionPermissionDelegate {
  private shortcutTargets = new Map<number, string>();
  private groups = new Map<string, WorkspaceGroup>();
  private pages = new Map<string, WorkspacePage>();
  private contentsOwners = new Map<number, PermissionOwner>();
  private observedOwners = new Set<number>();
  private externalPrompts = new Set<number>();
  private permissionPrompts = new Set<number>();
  private discardTimer: ReturnType<typeof setInterval>;
  private discarding = false;
  private detachedWindows = new Map<BrowserWindow, WorkspacePage>();

  constructor(
    private readonly onState: (owner: BrowserWindow, state: WebWorkspaceState) => void,
    private readonly onFocusAddress: (owner: BrowserWindow, id: string, newTabId?: string) => void,
    private readonly options: {
      onViewAttached?: (owner: BrowserWindow) => void;
      onFullscreen?: (owner: BrowserWindow, fullscreen: boolean) => void;
      onReveal?: (owner: BrowserWindow, id: string) => void;
      onFocusFind?: (owner: BrowserWindow, id: string) => void;
      onLibraryChange?: (owner: BrowserWindow) => void;
      library?: WebWorkspaceLibrary;
      downloads?: WebWorkspaceDownloads;
    } = {},
  ) {
    this.discardTimer = setInterval(() => {
      void this.discardInactiveTabs().catch(error => console.warn("[web-workspace] Idle tab cleanup failed", error));
    }, 60_000);
    this.discardTimer.unref();
  }

  protectEditedPage(contents: WebContents): void {
    const page = this.contentsOwners.get(contents.id)?.page;
    if (page) page.discardProtected = true;
  }

  /** Release only idle ordinary tabs, preserving Chromium navigation state.
   * Eligibility follows Chromium's DiscardEligibilityPolicy: active/pinned,
   * audible/captured, edited, busy and externally controlled pages are protected.
   * Electron does not expose Chromium's complete live-state graph; iframe,
   * session-storage and editor pages stay resident rather than guessing safety.
   * https://github.com/chromium/chromium/blob/main/chrome/browser/performance_manager/policies/discard_eligibility_policy.cc
   */
  async discardInactiveTabs(now = performance.now()): Promise<number> {
    if (this.discarding) return 0;
    this.discarding = true;
    let discarded = 0;
    try {
      for (const page of this.pages.values()) {
        const eligible = (): boolean => !page.contents.isDestroyed() && this.pages.get(page.id) === page
          && page.group.tabbed && page.group.activeTabId !== page.tab.id && !page.tab.pinned
          && page.inactiveSince !== undefined && now - page.inactiveSince >= IDLE_TAB_TIMEOUT
          && !page.bounds && !page.discardProtected && !page.pendingClose && !page.error
          && !page.automation && !page.inheritedAutomation?.some(restriction => restriction.active) && !page.popups.size
          && !page.contents.isLoading() && !page.contents.isCurrentlyAudible() && !page.contents.isBeingCaptured()
          && !page.contents.isDevToolsOpened() && !page.contents.debugger.isAttached()
          && !this.permissionPrompts.has(page.contents.id) && !this.externalPrompts.has(page.contents.id)
          && page.contents.mainFrame.framesInSubtree.length === 1
          && /^https?:/.test(page.contents.getURL())
          && !this.options.downloads?.list(page.owner).some(download => download.websiteId === page.group.id && !download.done);
        if (!eligible()) continue;
        const generation = page.navigationGeneration, inactiveSince = page.inactiveSince;
        let timeout: ReturnType<typeof setTimeout> | undefined;
        try {
          const safe = await Promise.race([
            page.contents.executeJavaScriptInIsolatedWorld(999, [{ code: `(() => document.contentType === 'text/html'
              && sessionStorage.length === 0 && !document.pictureInPictureElement
              && !document.querySelector('[contenteditable]:not([contenteditable="false"])')
              && ![...document.querySelectorAll('input,textarea,select')].some(el =>
                el instanceof HTMLSelectElement ? [...el.options].some(option => option.selected !== option.defaultSelected)
                : el instanceof HTMLInputElement && ['checkbox','radio'].includes(el.type) ? el.checked !== el.defaultChecked
                : el.value !== el.defaultValue)
              && ![...document.querySelectorAll('video,audio')].some(media => !media.paused && !media.ended)
              && ({ x: scrollX, y: scrollY }))()` }]),
            new Promise<boolean>(resolve => { timeout = setTimeout(() => resolve(false), 1000); }),
          ]);
          if (!safe || !eligible() || page.navigationGeneration !== generation || page.inactiveSince !== inactiveSince) continue;
          const entries = page.contents.navigationHistory.getAllEntries(), index = page.contents.navigationHistory.getActiveIndex();
          if (!entries[index]?.pageState) continue;
          page.tab.url = page.contents.getURL();
          page.tab.title = page.contents.getTitle();
          // Respect a site's beforeunload veto without opening a background
          // confirmation dialog. The tab remains resident if it refuses.
          page.discarding = true;
          const closed = await new Promise<boolean>(resolve => {
            let settled = false;
            const finish = (value: boolean): void => {
              if (settled) return;
              settled = true;
              page.contents.removeListener("destroyed", destroyed);
              page.contents.removeListener("will-prevent-unload", prevented);
              resolve(value);
            };
            const destroyed = (): void => finish(true);
            // Do not resume callers inside Chromium's veto callback. A caller
            // may navigate or close again before the native callback returns.
            // Keep observing destruction until that callback has completed.
            const prevented = (): void => { setImmediate(() => finish(false)); };
            page.contents.once("destroyed", destroyed);
            page.contents.once("will-prevent-unload", prevented);
            page.contents.close({ waitForBeforeUnload: true });
          });
          page.discarding = false;
          // Beforeunload is asynchronous. The owner, group or tab may have
          // been removed while Chromium was deciding whether to close.
          if (this.groups.get(page.group.id) !== page.group || page.owner.isDestroyed()
            || this.pages.get(page.id) !== page || page.tab.page !== page
            || !page.group.tabs.includes(page.tab)) continue;
          if (!closed) {
            page.discardProtected = true;
            page.pendingClose?.resolve();
            page.pendingClose = undefined;
            continue;
          }
          page.tab.discarded = { entries, index, scroll: safe };
          if (page.pendingClose) {
            // An explicit close during idle cleanup still closes the tab and
            // keeps its navigation state available for Reopen Closed Tab.
            this.removeTab(page.group, page.group.tabs.indexOf(page.tab));
          } else {
            this.destroyPage(page);
            // Selecting a tab cannot cancel a close already sent to Chromium.
            // Restore the selected page immediately instead of leaving a
            // discarded active tab with no native surface until another click.
            if (page.group.activeTabId === page.tab.id) this.ensurePage(page.group, page.tab);
            this.emit(page.group);
          }
          discarded++;
        } catch (error) {
          // Navigation/preflight failures are expected; a transaction failure
          // after issuing close needs a diagnostic without exposing page data.
          if (page.discarding) console.warn("[web-workspace] Idle discard transaction failed", error instanceof Error ? error.name : "UnknownError");
        }
        finally { page.discarding = false; if (timeout) clearTimeout(timeout); }
      }
    } finally { this.discarding = false; }
    return discarded;
  }

  /** Only live website pages from the nominated application window are discoverable. */
  browserSource(owner: () => BrowserWindow | null): BrowserWorkspaceSource {
    return {
      list: () => [...this.pages.values()]
        .filter(page => page.owner === owner() && !page.contents.isDestroyed() && !page.automation && !page.pendingClose && !page.discarding)
        .map(page => ({ tab_id: BROWSER_WORKSPACE_TAB_PREFIX + page.tab.id, url: page.contents.getURL(), title: page.contents.getTitle(),
          workspace_id: page.group.id, active: page.group.activeTabId === page.tab.id, source: page.group.tabbed ? "website" : "plugin" })),
      attach: (tabID, reclaim, layout, agentInput): BrowserWorkspaceAttachment => {
        const page = [...this.pages.values()].find(page => BROWSER_WORKSPACE_TAB_PREFIX + page.tab.id === tabID && page.owner === owner());
        if (!page || page.contents.isDestroyed() || page.pendingClose || page.discarding) throw new Error("Workspace tab is no longer available");
        if (page.automation) throw new Error("Workspace tab is already attached to another task");
        if (page.contents.debugger.isAttached()) throw new Error("Workspace tab already has a debugger attached");
        const automation = { reclaim, layout, agentInput, external: false, restriction: { active: true } };
        page.automation = automation;
        const visible = () => Boolean(page.bounds && !page.owner.isDestroyed() && page.owner.isVisible() && !page.owner.isMinimized());
        this.emit(page.group);
        return {
          view: page.view as unknown as BrowserViewHandle,
          parent: page.owner as unknown as BrowserParentWindowHandle,
          visible,
          setExternalPresentation: external => {
            automation.external = external;
            // An observation window owns its own outline. Never carry the
            // workspace canvas's clipping into the borrowed page's PiP surface.
            if (external && page.borderRadius !== 0) {
              page.view.setBorderRadius(0);
              page.borderRadius = 0;
            }
          },
          restore: () => this.layout(page),
          reveal: () => {
            if (this.groups.get(page.group.id) !== page.group || page.contents.isDestroyed()) return;
            this.activate(page.group, page.tab);
            this.emit(page.group);
            this.options.onReveal?.(page.owner, page.group.id);
          },
          release: () => {
            if (page.automation !== automation) return;
            automation.restriction.active = false;
            page.automation = undefined;
            if (!page.contents.isDestroyed()) {
              page.contents.setBackgroundThrottling(true);
              // Only visible layout reapplies tab zoom; releasing a hidden
              // same-origin sibling must not change the user's active page.
              this.layout(page);
            }
            this.emit(page.group);
          },
        };
      },
    };
  }

  initializeTabs(owner: BrowserWindow, destinations: WebWorkspaceLegacyDestination[]): WebWorkspaceInitializationState {
    const existing = this.groups.get(WEB_WORKSPACE_RIBBON_ID);
    if (existing) return { ...this.state(this.group(owner, existing.id)), migratedTabGroups: existing.migratedTabGroups };
    if (!Array.isArray(destinations)) throw new Error("Website destinations must be a list.");
    this.observeOwner(owner);
    const restored = this.options.library?.getSession(WEB_WORKSPACE_RIBBON_ID);
    const tabs: WorkspaceTab[] = restored ? restored.tabs.map(tab => ({ ...tab, zoomFactor: tab.zoomFactor ?? 1 })) : [];
    const migratedTabGroups: Record<string, string[]> = restored?.migratedTabGroups ?? Object.create(null);
    if (!restored) {
      const ids = new Set<string>();
      for (const destination of destinations) {
        if (!destination || typeof destination.id !== "string" || !/^website:[a-z\d_-]+$/i.test(destination.id)) throw new Error("Invalid legacy website destination.");
        if (ids.has(destination.id)) continue;
        const url = websiteURL(destination.url);
        const session = this.options.library?.getSession(destination.id);
        const previousTabs = session?.tabs.length ? session.tabs : [{ id: destination.id, url, title: destination.title ?? "" }];
        migratedTabGroups[destination.id] = [];
        for (const previous of previousTabs) {
          let id = !session || previous.id === session.activeTabId ? destination.id : `website:${previous.id}`;
          if (ids.has(id)) id = `website:${randomUUID()}`;
          ids.add(id);
          migratedTabGroups[destination.id].push(id);
          tabs.push({ ...previous, id, zoomFactor: previous.zoomFactor ?? 1 });
        }
      }
    }
    const group: WorkspaceGroup = {
      id: WEB_WORKSPACE_RIBBON_ID, owner, tabbed: true, homeURL: "about:blank", tabs,
      activeTabId: restored?.activeTabId ?? destinations[0]?.id ?? "", bounds: null, closedTabs: [],
      migratedTabGroups,
    };
    this.groups.set(group.id, group);
    // Keep legacy sessions intact. The new collection is saved separately, so
    // a failed migration write cannot consume or evict the old destinations.
    this.emit(group);
    return { ...this.state(group), migratedTabGroups };
  }

  open(owner: BrowserWindow, destination: WebWorkspaceDestination): WebWorkspaceState {
    if (typeof destination?.id !== "string" || !destination.id.trim() || destination.id.length > 256) throw new Error("A website destination ID is required.");
    if (destination.id === WEB_WORKSPACE_RIBBON_ID) {
      const group = this.group(owner, destination.id);
      const tab = this.activeTab(group);
      if (tab) this.ensurePage(group, tab);
      return this.state(group);
    }
    const url = websiteURL(destination.url);
    const existing = this.groups.get(destination.id);
    if (existing) {
      if (existing.owner !== owner) throw new Error("This website belongs to another window.");
      if (existing.homeURL !== url) {
        existing.homeURL = url;
        this.navigate(this.ensurePage(existing, this.activeTab(existing)!), url);
        this.emit(existing);
      }
      return this.state(existing);
    }
    this.observeOwner(owner);
    const tabbed = destination.tabs === true;
    const restored = tabbed ? this.options.library?.getSession(destination.id) : undefined;
    const tabs: WorkspaceTab[] = restored?.tabs.length
      ? restored.tabs.map(tab => ({ ...tab, zoomFactor: tab.zoomFactor ?? 1 }))
      : [{ id: randomUUID(), url, title: "", zoomFactor: 1 }];
    const activeTabId = tabs.some(tab => tab.id === restored?.activeTabId) ? restored!.activeTabId : tabs[0].id;
    if (restored?.homeURL && restored.homeURL !== url) {
      const active = tabs.find(tab => tab.id === activeTabId)!;
      active.url = url;
      active.title = "";
    }
    const group: WorkspaceGroup = {
      id: destination.id, owner, tabbed, homeURL: url, tabs, activeTabId,
      bounds: null, closedTabs: [],
    };
    this.groups.set(group.id, group);
    this.ensurePage(group, this.activeTab(group)!);
    this.emit(group);
    return this.state(group);
  }

  showOptions(owner: BrowserWindow, id: string, request: WebWorkspaceMenuRequest): Promise<WebWorkspaceMenuAction | null> {
    const group = this.group(owner, id), tab = this.activeTab(group);
    if (!tab || !owner.isVisible() || owner.isMinimized()) return Promise.resolve(null);
    if (![request.x, request.y].every(Number.isFinite)) throw new Error("Menu coordinates must be finite numbers.");
    const state = this.tabState(tab), labels = request.labels;
    const blank = state.url === "about:blank" && state.startPage !== false;
    const origin = requestOrigin(state.url);
    return new Promise((resolve, reject) => {
      let selected: WebWorkspaceMenuAction | null = null;
      const item = (action: WebWorkspaceMenuAction, enabled = true) => ({
        label: action === "resetZoom" ? `${labels[action]} (${Math.round(tab.zoomFactor * 100)}%)` : labels[action] ?? "Reset saved storage permission",
        enabled, click: () => { selected = action; },
      });
      const menu = Menu.buildFromTemplate([
        item("find", !blank),
        { type: "separator" },
        item("zoomOut", tab.zoomFactor > WEB_WORKSPACE_ZOOM_MIN),
        item("resetZoom"),
        item("zoomIn", tab.zoomFactor < WEB_WORKSPACE_ZOOM_MAX),
        { type: "separator" },
        ...(group.tabbed ? [item("reopenTab", group.closedTabs.length > 0 && group.tabs.length < WEB_WORKSPACE_TAB_LIMIT)] : []),
        item("openExternal", !blank),
        item("resetStoragePermission", Boolean(origin && getPersistentStoragePermission(origin) !== undefined)),
      ]);
      const zoom = owner.webContents.getZoomFactor();
      // Menu coordinates are relative to the owner's content view, in DIPs.
      menu.popup({ window: owner, x: Math.round(request.x * zoom), y: Math.round(request.y * zoom),
        callback: () => {
          if (this.groups.get(id) !== group || this.activeTab(group) !== tab || requestOrigin(tab.url) !== origin) { resolve(null); return; }
          try {
            if (selected === "resetStoragePermission" && origin) setPersistentStoragePermission(origin, undefined);
            resolve(selected);
          } catch (error) { reject(error); }
        } });
    });
  }

  /** Capture only the exact visible tab nominated by an explicit capsule send. */
  async pageContext(owner: BrowserWindow, id: string, tabId: string): Promise<PageContextSnapshot> {
    if (typeof id !== "string" || typeof tabId !== "string") throw new Error("Invalid page context target.");
    const group = this.group(owner, id), page = this.activeTab(group)?.page;
    if (!page || page.tab.id !== tabId || !page.bounds || page.automation?.external || page.contents.isDestroyed()) {
      throw new Error("The source page is no longer visible. Return to it and send again.");
    }
    const generation = page.navigationGeneration;
    const url = page.contents.getURL();
    const visible = await page.contents.executeJavaScriptInIsolatedWorld(998, [{ code: `(${captureVisiblePage.toString()})()` }]) as { text: string; selection: string };
    if (this.groups.get(id) !== group || this.activeTab(group)?.page !== page || !page.bounds || page.contents.isDestroyed()
      || page.navigationGeneration !== generation || page.contents.getURL() !== url) {
      throw new Error("The page changed while reading its context. Send again on the intended page.");
    }
    return { kind: "website", pageId: BROWSER_WORKSPACE_TAB_PREFIX + tabId, title: page.contents.getTitle().slice(0, 200),
      url: safePageURL(url), text: visible.text.slice(0, 6000), selection: visible.selection.slice(0, 2000), capturedAt: new Date().toISOString() };
  }

  /** One bounded, in-memory backdrop for chrome overlays; no capture subscription. */
  async capture(owner: BrowserWindow, id: string): Promise<string | null> {
    const group = this.group(owner, id);
    const page = this.activeTab(group)?.page;
    if (!page || (!page.bounds && !group.lastBounds) || page.automation?.external || page.contents.isDestroyed() || !owner.isVisible() || owner.isMinimized()) return null;
    // An expanded tab rail can select a different page while the native layer
    // remains occluded. Size that page for this one capture without showing it
    // or changing the visibility used by automation and ownership handoff.
    if (!page.bounds) this.layout(page, group.lastBounds);
    const generation = page.navigationGeneration;
    const image = await page.contents.capturePage(undefined, { stayHidden: true });
    if (this.groups.get(id) !== group || this.activeTab(group)?.page !== page || page.contents.isDestroyed() || page.navigationGeneration !== generation || image.isEmpty()) return null;
    // capturePage already returns device pixels (including Retina) in a 1x
    // bitmap. Keep them intact: a thumbnail would be enlarged to fill the
    // native viewport. PNG also preserves fine strokes lost by JPEG.
    const size = image.getSize();
    if (size.width * size.height > MAX_BACKDROP_PIXELS) return null;
    const png = image.toPNG();
    return png.length > 0 && png.length <= MAX_BACKDROP_BYTES ? `data:image/png;base64,${png.toString("base64")}` : null;
  }

  reportBounds(owner: BrowserWindow, id: string, bounds: WebWorkspaceBounds | null): void {
    if (bounds === null && !this.groups.has(id)) return;
    const group = this.group(owner, id);
    if (bounds !== null) {
      const dimensions = "right" in bounds ? [bounds.right, bounds.bottom] : [bounds.width, bounds.height];
      if (![bounds.x, bounds.y, bounds.borderRadius ?? 0, ...dimensions].every(Number.isFinite)) throw new Error("Website bounds must be finite numbers.");
    }
    group.lastBounds = bounds ?? undefined;
    group.bounds = bounds?.occluded ? null : bounds;
    this.layoutGroup(group);
  }

  setShortcutTarget(owner: BrowserWindow, id: string, active: boolean): void {
    if (active) {
      this.group(owner, id);
      this.shortcutTargets.set(owner.webContents.id, id);
    } else if (this.shortcutTargets.get(owner.webContents.id) === id) this.shortcutTargets.delete(owner.webContents.id);
  }

  /** Route keys consumed before DOM dispatch only while browser chrome has focus. */
  handleShellInput(contents: WebContents, event: Electron.Event, input: Electron.Input): boolean {
    const id = this.shortcutTargets.get(contents.id);
    const group = id ? this.groups.get(id) : undefined;
    if (!group || !group.tabs.length || group.owner.webContents !== contents || !group.owner.isVisible() || group.owner.isMinimized()) return false;
    const key = input.key.toLowerCase();
    const modifier = process.platform === "darwin" ? input.meta : input.control;
    if (input.alt) return false;
    const reload = key === "f5" || (modifier && key === "r");
    const zoom = modifier && ["+", "=", "-", "0"].includes(key);
    const closeTab = modifier && key === "w" && !input.shift && group.tabbed;
    if (!reload && !zoom && !closeTab) return false;
    // In particular, consume Cmd/Ctrl+W before the native window-menu Close
    // accelerator; waiting for the renderer's keydown can close the shell.
    event.preventDefault();
    if (input.type === "keyDown") {
      if (closeTab) void this.closeTab(group.owner, group.id, group.activeTabId).catch(error => console.warn("[web-workspace] Could not close tab", error));
      else if (reload) this.command(group.owner, group.id, input.shift ? "reloadIgnoringCache" : "reload");
      else if (key === "0") this.setZoom(group.owner, group.id, 1);
      else this.stepZoom(this.ensurePage(group, this.activeTab(group)!), key === "-" ? -1 : 1);
    }
    return true;
  }

  command(owner: BrowserWindow, id: string, command: WebWorkspaceCommand, url?: string): WebWorkspaceState {
    const group = this.group(owner, id);
    const tab = this.activeTab(group);
    if (!tab) throw new Error("Open a browser tab first.");
    const page = this.ensurePage(group, tab);
    const contents = page.contents;
    if (command !== "focus") page.automation?.reclaim();
    switch (command) {
      case "navigate": this.navigate(page, websiteURL(url ?? "")); break;
      case "back": if (contents.navigationHistory.canGoBack()) contents.navigationHistory.goBack(); break;
      case "forward": if (contents.navigationHistory.canGoForward()) contents.navigationHistory.goForward(); break;
      case "reload":
      case "reloadIgnoringCache": {
        // A failed navigation can leave getURL() on the preceding document.
        // Retry the attempted destination, not that stale committed page.
        if (page.error && page.requestedURL !== contents.getURL()) this.navigate(page, page.requestedURL);
        else {
          page.error = undefined;
          if (command === "reloadIgnoringCache") contents.reloadIgnoringCache(); else contents.reload();
        }
        break;
      }
      case "stop": contents.stop(); break;
      case "takeOver": break;
      case "focus": if (page.bounds) contents.focus(); break;
      default: throw new Error("Unsupported website command.");
    }
    this.emit(group);
    return this.state(group);
  }

  createTab(owner: BrowserWindow, id: string, url?: string, options: { background?: boolean } = {}): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    if (group.tabs.length >= WEB_WORKSPACE_TAB_LIMIT) throw new Error(`Close a tab before opening another. The browser limit is ${WEB_WORKSPACE_TAB_LIMIT} tabs.`);
    const tab: WorkspaceTab = { id: group.id === WEB_WORKSPACE_RIBBON_ID ? `website:${randomUUID()}` : randomUUID(), url: url === undefined ? "about:blank" : websiteURL(url), title: "", zoomFactor: 1 };
    group.tabs.push(tab);
    if (options.background) this.ensurePage(group, tab);
    else this.activate(group, tab);
    this.emit(group);
    if (!options.background && group.id === WEB_WORKSPACE_RIBBON_ID) this.options.onReveal?.(owner, group.id);
    return this.state(group);
  }

  selectTab(owner: BrowserWindow, id: string, tabId: string): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    const tab = group.tabs.find(candidate => candidate.id === tabId);
    if (!tab) throw new Error("The website tab is no longer open.");
    this.activate(group, tab);
    this.emit(group);
    return this.state(group);
  }

  async closeTab(owner: BrowserWindow, id: string, tabId: string): Promise<WebWorkspaceState> {
    const group = this.tabGroup(owner, id);
    const index = group.tabs.findIndex(tab => tab.id === tabId);
    if (index < 0) throw new Error("The website tab is no longer open.");
    const page = group.tabs[index].page;
    page?.automation?.reclaim();
    if (!page || page.contents.isDestroyed()) this.removeTab(group, index);
    else {
      if (!page.pendingClose) {
        let resolve!: () => void;
        const promise = new Promise<void>(done => { resolve = done; });
        const entries = page.contents.navigationHistory.getAllEntries();
        const index = page.contents.navigationHistory.getActiveIndex();
        page.pendingClose = { promise, resolve,
          ...(entries[index] ? { navigation: { entries, index } } : {}),
        };
        // Keep the tab and its reopen entry unchanged until Chromium accepts
        // the close. A site's beforeunload can still let the user stay.
        if (!page.discarding) page.contents.close({ waitForBeforeUnload: true });
      }
      await page.pendingClose?.promise;
    }
    if (this.groups.get(id) !== group) throw new Error("The website is no longer open.");
    return this.state(group);
  }

  reopenTab(owner: BrowserWindow, id: string): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    if (group.tabs.length >= WEB_WORKSPACE_TAB_LIMIT) throw new Error(`Close a tab before restoring another. The browser limit is ${WEB_WORKSPACE_TAB_LIMIT} tabs.`);
    const closed = group.closedTabs.pop();
    if (closed) {
      const { id: previousId, index, ...state } = closed;
      const tab: WorkspaceTab = { ...state, reopenedFrom: previousId, id: group.id === WEB_WORKSPACE_RIBBON_ID ? `website:${randomUUID()}` : randomUUID() };
      group.tabs.splice(index, 0, tab);
      this.activate(group, tab);
      this.emit(group);
      if (group.id === WEB_WORKSPACE_RIBBON_ID) this.options.onReveal?.(owner, group.id);
      if (tab.page?.view.getVisible()) tab.page.contents.focus();
    }
    return this.state(group);
  }

  setTabPinned(owner: BrowserWindow, id: string, tabId: string, pinned: boolean): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    if (typeof pinned !== "boolean") throw new Error("A tab's pinned state must be a boolean.");
    const tab = group.tabs.find(candidate => candidate.id === tabId);
    if (!tab) throw new Error("The website tab is no longer open.");
    tab.pinned = pinned;
    this.emit(group);
    return this.state(group);
  }

  setTabMuted(owner: BrowserWindow, id: string, tabId: string, muted: boolean): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    if (typeof muted !== "boolean") throw new Error("A tab's muted state must be a boolean.");
    const tab = group.tabs.find(candidate => candidate.id === tabId);
    if (!tab) throw new Error("The website tab is no longer open.");
    tab.muted = muted;
    if (tab.page && !tab.page.contents.isDestroyed()) tab.page.contents.setAudioMuted(muted);
    this.emit(group);
    return this.state(group);
  }

  reorderTabs(owner: BrowserWindow, id: string, tabIds: string[]): WebWorkspaceState {
    const group = this.tabGroup(owner, id);
    if (!Array.isArray(tabIds) || tabIds.some(tabId => typeof tabId !== "string") || new Set(tabIds).size !== tabIds.length) throw new Error("Invalid browser tab order.");
    const positions = new Map(tabIds.map((tabId, index) => [tabId, index]));
    group.tabs.sort((a, b) => (positions.get(a.id) ?? tabIds.length) - (positions.get(b.id) ?? tabIds.length));
    this.emit(group);
    return this.state(group);
  }

  find(owner: BrowserWindow, id: string, text: string, options: WebWorkspaceFindOptions = {}): WebWorkspaceState {
    const group = this.group(owner, id);
    if (typeof text !== "string" || text.length > 10_000) throw new Error("The search text is too long.");
    if (!text) return this.stopFind(owner, id);
    const tab = this.activeTab(group);
    if (!tab) throw new Error("Open a browser tab first.");
    const page = this.ensurePage(group, tab);
    // Electron calls Chromium new_session "findNext": true starts a search,
    // while our public flag means advance an existing search result.
    const requestId = page.contents.findInPage(text, { forward: options.forward !== false, findNext: options.findNext !== true });
    page.findRequestId = requestId;
    page.findResult = { requestId, activeMatchOrdinal: 0, matches: 0, finalUpdate: false };
    this.emit(group);
    return this.state(group);
  }

  stopFind(owner: BrowserWindow, id: string): WebWorkspaceState {
    const group = this.group(owner, id);
    this.clearFind(this.activeTab(group)?.page);
    this.emit(group);
    return this.state(group);
  }

  setZoom(owner: BrowserWindow, id: string, factor: number): WebWorkspaceState {
    const group = this.group(owner, id);
    if (!Number.isFinite(factor) || factor < WEB_WORKSPACE_ZOOM_MIN || factor > WEB_WORKSPACE_ZOOM_MAX) throw new Error("Website zoom must be between 25% and 300%.");
    const tab = this.activeTab(group);
    if (!tab) throw new Error("Open a browser tab first.");
    tab.page?.automation?.reclaim();
    tab.zoomFactor = factor;
    this.ensurePage(group, tab).contents.setZoomFactor(factor);
    this.emit(group);
    return this.state(group);
  }

  close(owner: BrowserWindow, id: string): void {
    const group = this.groups.get(id);
    // Explicit removal also forgets destinations not opened since startup.
    if (!group) { this.options.library?.removeSession(id); return; }
    if (group.owner !== owner) throw new Error("This website belongs to another window.");
    if (group.tabbed) this.options.library?.removeSession(id);
    this.destroyGroup(group);
  }

  destroyAll(): void {
    clearInterval(this.discardTimer);
    for (const group of [...this.groups.values()]) this.destroyGroup(group);
    this.closeDetachedWindows();
    this.flushLibrary();
    this.options.downloads?.destroyAll();
  }

  private automationRestricted(page: WorkspacePage): boolean {
    return Boolean(page.automation || page.inheritedAutomation?.some(lease => lease.active));
  }

  handleDownload(contents: WebContents, item: DownloadItem): void {
    const owned = this.contentsOwners.get(contents.id);
    if (owned && !this.automationRestricted(owned.page) && this.options.downloads) this.options.downloads.start(owned.page.owner, owned.page.group.id, item);
    else item.cancel();
  }
  ownsWebContents(id: number): boolean {
    return this.contentsOwners.has(id);
  }

  requestDisplayMedia(request: DisplayMediaRequestHandlerHandlerRequest, callback: (streams: Streams | null) => void): void {
    const frame = request.frame;
    const contents = frame && !frame.isDestroyed() ? webContents.fromFrame(frame) : undefined;
    const owned = contents && this.contentsOwners.get(contents.id);
    const origin = requestOrigin(request.securityOrigin);
    // This picker is scoped to foreground top-level documents. Embedded frames
    // need their own origin/lifetime handling before joining this flow.
    if (!frame || !contents || !owned || !origin || !request.videoRequested || !request.userGesture
      || frame !== contents.mainFrame || this.permissionPrompts.has(contents.id)) {
      callback(null);
      return;
    }
    const parent = BrowserWindow.fromWebContents(contents) ?? owned.page.owner;
    const navigationGeneration = owned.navigationGeneration;
    const current = () => !contents.isDestroyed() && !frame.isDestroyed() && !frame.detached
      && frame === contents.mainFrame && this.contentsOwners.get(contents.id) === owned
      && owned.navigationGeneration === navigationGeneration && !this.automationRestricted(owned.page)
      && requestOrigin(frame.url) === origin && requestOrigin(contents.getURL()) === origin
      && !parent.isDestroyed() && parent.isVisible() && !parent.isMinimized()
      && (contents !== owned.page.contents || Boolean(owned.page.bounds && owned.page.view.getVisible()
        && owned.page.group.activeTabId === owned.page.tab.id));
    if (!current()) { callback(null); return; }
    this.permissionPrompts.add(contents.id);
    void chooseBrowserDisplaySource(parent, origin, current).then(source => {
      const allowed = source && current();
      if (allowed) owned.page.discardProtected = true;
      this.permissionPrompts.delete(contents.id);
      // No system-audio grant: platform support and consent differ from video.
      callback(allowed ? { video: source } : null);
    }, () => {
      this.permissionPrompts.delete(contents.id);
      callback(null);
    });
  }

  checkPermission(contents: WebContents, permission: string, requestingOrigin: string, mediaType?: string): boolean {
    const origin = requestOrigin(requestingOrigin);
    const topOrigin = requestOrigin(contents.getURL());
    const owned = this.contentsOwners.get(contents.id);
    if (permission === "persistent-storage") {
      return Boolean(origin && origin === topOrigin && owned && !this.automationRestricted(owned.page)
        && getPersistentStoragePermission(origin) === true);
    }
    const keys = permissionGrantKeys(permission, mediaType ? [mediaType] : []);
    return Boolean(origin && topOrigin && owned && !this.automationRestricted(owned.page) && keys.length
      && keys.every(key => owned.grants.has(`${topOrigin}\n${origin}\n${key}`)));
  }

  requestPermission(
    contents: WebContents,
    permission: string,
    callback: (allowed: boolean) => void,
    details: BrowserPermissionRequestDetails,
  ): void {
    const owned = this.contentsOwners.get(contents.id);
    const origin = requestOrigin(details.requestingUrl);
    if (!owned || this.automationRestricted(owned.page) || !origin || permission === "notifications" || contents.isDestroyed() || this.permissionPrompts.has(contents.id)) {
      callback(false);
      return;
    }
    const mediaTypes = "mediaTypes" in details ? details.mediaTypes : undefined;
    const grantKeys = permissionGrantKeys(permission, mediaTypes);
    const pageOrigin = requestOrigin(contents.getURL());
    const persistentStorage = permission === "persistent-storage";
    if (persistentStorage) {
      // Embedded origins cannot acquire durable privileges under another site.
      if (origin !== pageOrigin) { callback(false); return; }
      const decision = getPersistentStoragePermission(origin);
      if (decision !== undefined) { callback(decision); return; }
    }
    if (pageOrigin && grantKeys.length && grantKeys.every(key => owned.grants.has(`${pageOrigin}\n${origin}\n${key}`))) {
      callback(true);
      return;
    }
    const parent = BrowserWindow.fromWebContents(contents) ?? owned.page.owner;
    if (parent.isDestroyed() || !parent.isVisible() || parent.isMinimized()
      || (contents === owned.page.contents && !owned.page.bounds)) { callback(false); return; }
    const navigationGeneration = owned.navigationGeneration;
    this.permissionPrompts.add(contents.id);
    void dialog.showMessageBox(parent, {
      type: "question",
      title: "Website permission",
      message: persistentStorage ? `${origin} wants to keep website data on this device.` : `${origin} wants to use ${permission}.`,
      detail: [
        ...(mediaTypes?.length ? [`Requested media: ${mediaTypes.join(", ")}.`] : []),
        ...("filePath" in details ? [`File: ${details.filePath} (${details.fileAccessType}).`] : []),
        ...("externalURL" in details ? [`Application link: ${details.externalURL}`] : []),
        ...(pageOrigin && pageOrigin !== origin ? [`Embedded in ${pageOrigin}.`] : []),
        persistentStorage
          ? "This protects the site's local data from automatic cleanup. Your Allow or Deny choice will be remembered. Reset it from this website's menu."
          : grantKeys.length ? "Allow for this website tab until it is closed?" : "Allow this request?",
      ].join("\n"),
      buttons: ["Deny", "Allow"], defaultId: 0, cancelId: 0, noLink: true,
    }).then(({ response }) => {
      const current = !this.automationRestricted(owned.page) && !contents.isDestroyed()
        && this.contentsOwners.get(contents.id) === owned && owned.navigationGeneration === navigationGeneration
        && requestOrigin(contents.getURL()) === pageOrigin;
      const allowed = response === 1 && current;
      if (persistentStorage && current) {
        try { setPersistentStoragePermission(origin, allowed); }
        catch (error) {
          console.warn("[web-workspace] Could not save storage permission", error);
          this.permissionPrompts.delete(contents.id);
          callback(false);
          return;
        }
      }
      if (allowed && !persistentStorage) owned.page.discardProtected = true;
      if (allowed) for (const key of grantKeys) owned.grants.add(`${pageOrigin}\n${origin}\n${key}`);
      if (allowed && permission === "fullscreen" && !parent.isDestroyed() && parent.isVisible() && !parent.isMinimized()
        && (contents !== owned.page.contents || (owned.page.bounds && owned.page.view.getVisible()
          && owned.page.group.activeTabId === owned.page.tab.id))) {
        // Closing a native permission dialog need not reactivate its owner
        // (notably GTK). Restore the accepting user's input target before
        // Chromium starts fullscreen; focusing a guest alone cannot do that.
        const focused = BrowserWindow.getFocusedWindow();
        if (!focused || focused === parent) {
          parent.focus();
          contents.focus();
        }
      }
      // Chromium can synchronously continue into display-source selection.
      this.permissionPrompts.delete(contents.id);
      callback(allowed);
    }, () => {
      this.permissionPrompts.delete(contents.id);
      callback(false);
    });
  }

  private group(owner: BrowserWindow, id: string): WorkspaceGroup {
    const group = this.groups.get(id);
    if (!group || group.owner !== owner) throw new Error("The website is no longer open.");
    return group;
  }

  private tabGroup(owner: BrowserWindow, id: string): WorkspaceGroup {
    const group = this.group(owner, id);
    if (!group.tabbed) throw new Error("This website does not support tab groups.");
    return group;
  }

  private activeTab(group: WorkspaceGroup): WorkspaceTab | undefined {
    return group.tabs.find(tab => tab.id === group.activeTabId);
  }

  private tabState(tab: WorkspaceTab): WebWorkspaceTabState {
    const page = tab.page;
    const contents = page && !page.contents.isDestroyed() ? page.contents : undefined;
    return {
      ...(tab.reopenedFrom ? { reopenedFrom: tab.reopenedFrom } : {}),
      id: tab.id, url: page?.error || contents?.isLoading() ? page!.requestedURL : contents?.getURL() || tab.url,
      title: contents?.getTitle() || tab.title, loading: contents?.isLoading() ?? false,
      canGoBack: contents?.navigationHistory.canGoBack() ?? Boolean(tab.discarded && tab.discarded.index > 0),
      canGoForward: contents?.navigationHistory.canGoForward() ?? Boolean(tab.discarded && tab.discarded.index < tab.discarded.entries.length - 1),
      ...(tab.discarded ? { discarded: true } : {}),
      zoomFactor: tab.zoomFactor,
      ...(tab.pinned ? { pinned: true } : {}),
      ...(tab.muted ? { muted: true } : {}),
      ...(contents?.isCurrentlyAudible() ? { audible: true } : {}),
      ...(tab.favicon ? { favicon: tab.favicon } : {}),
      startPage: tab.startPage !== false,
      ...(page?.automation ? { agentControlled: true } : {}),
      ...(page?.findResult ? { findResult: page.findResult } : {}),
      ...(page?.error ? { error: page.error } : {}),
    };
  }

  private state(group: WorkspaceGroup): WebWorkspaceState {
    const active = this.activeTab(group);
    return {
      ...(active ? this.tabState(active) : { url: "about:blank", title: "", loading: false, canGoBack: false, canGoForward: false }), id: group.id, activeTabId: group.activeTabId,
      ...(group.tabbed ? { tabs: group.tabs.map(tab => this.tabState(tab)), canReopenTab: group.closedTabs.length > 0 } : {}),
    };
  }

  private emit(group: WorkspaceGroup): void {
    if (this.groups.get(group.id) !== group || group.owner.isDestroyed()) return;
    if (group.tabbed) this.options.library?.saveSession(group.id, {
      homeURL: group.homeURL,
      tabs: group.tabs.map(tab => { const state = this.tabState(tab); return { id: state.id, url: state.url, title: state.title, zoomFactor: tab.zoomFactor, ...(tab.pinned ? { pinned: true } : {}), ...(tab.muted ? { muted: true } : {}) }; }),
      activeTabId: group.activeTabId,
      ...(group.migratedTabGroups ? { migratedTabGroups: group.migratedTabGroups } : {}),
    });
    this.onState(group.owner, this.state(group));
  }

  private ensurePage(group: WorkspaceGroup, tab: WorkspaceTab): WorkspacePage {
    if (tab.page && !tab.page.contents.isDestroyed()) return tab.page;
    const history = tab.discarded;
    tab.discarded = undefined;
    const page = this.attachPage(group, tab, createBrowserView({ webPreferences: { preload: join(__dirname, "../preload/browserPage.cjs") } }));
    if (history) void page.contents.navigationHistory.restore({ entries: history.entries, index: history.index }).then(async () => {
      if (this.pages.get(page.id) !== page || page.contents.isDestroyed() || page.navigationGeneration !== 1) return;
      // Chromium serializes pageState periodically; its saved scroll can lag
      // behind the final viewport. Apply the live offset captured at discard.
      if (history.scroll) await page.contents.executeJavaScriptInIsolatedWorld(999, [{ code: `scrollTo(${history.scroll.x}, ${history.scroll.y})` }]);
    }).catch((error: Error) => {
      if (this.pages.get(page.id) !== page || page.navigationGeneration > 1) return;
      page.error = error.message; this.emit(group);
    });
    else this.navigate(page, tab.url);
    return page;
  }

  private attachPage(group: WorkspaceGroup, tab: WorkspaceTab, view: WebContentsView, inheritedAutomation?: readonly AutomationRestriction[]): WorkspacePage {
    const page: WorkspacePage = {
      id: randomUUID(), group, tab, owner: group.owner, view, contents: view.webContents,
      bounds: null, borderRadius: 0, requestedURL: tab.url, navigationGeneration: 0, popups: new Set(), inheritedAutomation,
      inactiveSince: group.activeTabId === tab.id ? undefined : performance.now(),
    };
    tab.page = page;
    page.contents.setAudioMuted(tab.muted ?? false);
    this.pages.set(page.id, page);
    view.setVisible(false);
    // Chromium can focus a newly attached hidden view before bounds arrive.
    // Apply the visibility contract even when group layout has no bounds change.
    page.contents.on("focus", () => this.layout(page));
    group.owner.contentView.addChildView(view);
    this.options.onViewAttached?.(group.owner);
    this.secureContents(page, page.contents);
    page.contents.on("before-input-event", (event, input) => this.handleInput(page, event, input));
    page.contents.on("enter-html-full-screen", () => {
      page.htmlFullscreen = true;
      this.options.onFullscreen?.(group.owner, true);
      // HTML fullscreen belongs to this native page, not the shell's ribbon
      // viewport. Restore its focus after the permission dialog so Chromium
      // receives the user's Escape key and handles fullscreen exit itself.
      group.owner.contentView.addChildView(view);
      this.layout(page);
      if (view.getVisible()) page.contents.focus();
    });
    page.contents.on("leave-html-full-screen", () => {
      page.htmlFullscreen = false;
      this.options.onFullscreen?.(group.owner, false);
      this.layout(page);
      if (!page.contents.isDestroyed() && page.bounds && view.getVisible()) page.contents.focus();
    });
    page.contents.on("did-start-loading", () => { page.error = undefined; this.emit(group); });
    page.contents.on("did-stop-loading", () => this.emit(group));
    page.contents.on("audio-state-changed", () => this.emit(group));
    page.contents.on("did-start-navigation", details => {
      if (!details.isMainFrame) return;
      page.navigationGeneration++;
      page.requestedURL = details.url;
      if (!details.isSameDocument) this.clearFind(page);
    });
    const committed = (_event: Electron.Event, url: string) => {
      page.error = undefined;
      page.requestedURL = url;
      tab.url = url;
      tab.title = page.contents.getTitle();
      // Chromium's origin zoom is shared; reapply this tab's chosen factor when
      // it navigates or becomes active, keeping the shell's zoom independent.
      if (page.bounds) page.contents.setZoomFactor(tab.zoomFactor);
      if (group.tabbed) {
        this.options.library?.recordVisit({ url, title: tab.title });
        this.options.onLibraryChange?.(group.owner);
      }
      this.emit(group);
    };
    page.contents.on("did-navigate", committed);
    page.contents.on("did-navigate-in-page", (event, url, isMainFrame) => { if (isMainFrame) committed(event, url); });
    page.contents.on("page-title-updated", () => {
      if (page.inactiveSince !== undefined && !page.contents.isLoading()) page.discardProtected = true;
      tab.title = page.contents.getTitle();
      if (group.tabbed) {
        this.options.library?.updateTitle(tab.url, tab.title);
        this.options.onLibraryChange?.(group.owner);
      }
      this.emit(group);
    });
    page.contents.on("page-favicon-updated", (_event, favicons) => {
      if (page.inactiveSince !== undefined && !page.contents.isLoading()) page.discardProtected = true;
      tab.favicon = favicons.find(value => /^https?:\/\//i.test(value) || /^data:image\//i.test(value));
      this.emit(group);
    });
    page.contents.on("did-fail-load", (_event, code, description, url, isMainFrame) => {
      if (!isMainFrame || code === -3 || (url && url !== page.requestedURL)) return;
      page.error = description || "The website could not be loaded.";
      this.emit(group);
    });
    page.contents.on("render-process-gone", () => {
      page.error = "The website stopped responding. Reload to try again.";
      this.emit(group);
    });
    page.contents.on("found-in-page", (_event, result) => {
      if (page.findRequestId !== result.requestId) return;
      page.findResult = { requestId: result.requestId, activeMatchOrdinal: result.activeMatchOrdinal, matches: result.matches, finalUpdate: result.finalUpdate };
      this.emit(group);
    });
    page.contents.on("zoom-changed", (_event, direction) => this.stepZoom(page, direction === "in" ? 1 : -1));
    page.contents.once("destroyed", () => {
      if (page.discarding) return;
      if (this.pages.get(page.id) !== page || this.groups.get(group.id) !== group) return;
      const index = group.tabs.indexOf(tab);
      if (group.tabbed && index >= 0) this.removeTab(group, index);
      else {
        this.destroyPage(page);
        page.error = "The website closed this page. Reopen it to continue.";
        if (!group.owner.isDestroyed()) this.onState(group.owner, { id: group.id, url: tab.url, title: tab.title, loading: false, canGoBack: false, canGoForward: false, error: page.error });
        this.groups.delete(group.id);
      }
    });
    this.layoutGroup(group);
    return page;
  }

  private activate(group: WorkspaceGroup, tab: WorkspaceTab): void {
    if (group.activeTabId !== tab.id) {
      const previous = this.activeTab(group)?.page;
      this.clearFind(previous);
      if (previous) previous.inactiveSince = performance.now();
    }
    group.activeTabId = tab.id;
    const page = this.ensurePage(group, tab);
    page.inactiveSince = undefined;
    page.contents.setZoomFactor(tab.zoomFactor);
    this.layoutGroup(group);
  }

  private removeTab(group: WorkspaceGroup, index: number): void {
    const tab = group.tabs[index];
    const state = this.tabState(tab);
    group.closedTabs.push({ id: tab.id, index, url: state.url, title: state.title, zoomFactor: tab.zoomFactor, pinned: tab.pinned, muted: tab.muted,
      discarded: tab.discarded ?? tab.page?.pendingClose?.navigation });
    if (group.closedTabs.length > 20) group.closedTabs.shift();
    group.tabs.splice(index, 1);
    if (tab.page) this.destroyPage(tab.page);
    if (!group.tabs.length && group.id !== WEB_WORKSPACE_RIBBON_ID) group.tabs.push({ id: randomUUID(), url: "about:blank", title: "", zoomFactor: 1 });
    if (group.activeTabId === tab.id) {
      group.activeTabId = group.tabs[Math.min(index, group.tabs.length - 1)]?.id ?? "";
      const active = this.activeTab(group);
      if (active) this.activate(group, active);
    }
    this.emit(group);
  }

  private clearFind(page?: WorkspacePage): void {
    if (!page || page.contents.isDestroyed()) return;
    page.contents.stopFindInPage("clearSelection");
    page.findRequestId = undefined;
    page.findResult = undefined;
  }

  private navigate(page: WorkspacePage, url: string): void {
    page.automation?.reclaim();
    page.error = undefined;
    page.requestedURL = url;
    const generation = ++page.navigationGeneration;
    void page.contents.loadURL(url).catch((error: Error & { code?: string }) => {
      if (error.code === "ERR_ABORTED" || this.pages.get(page.id) !== page || page.navigationGeneration > generation + 1) return;
      page.error = error.message;
      this.emit(page.group);
    });
  }

  private destroyPage(page: WorkspacePage): void {
    page.automation?.reclaim();
    this.pages.delete(page.id);
    page.tab.page = undefined;
    for (const popup of page.popups) if (!popup.isDestroyed()) popup.destroy();
    if (!page.owner.isDestroyed()) page.owner.contentView.removeChildView(page.view);
    if (!page.contents.isDestroyed()) page.contents.close();
    page.pendingClose?.resolve();
    page.pendingClose = undefined;
  }

  private flushLibrary(): void {
    try { this.options.library?.flush(); } catch (error) { console.warn("[web-workspace] Could not save browser session", error); }
  }

  private destroyGroup(group: WorkspaceGroup): void {
    this.groups.delete(group.id);
    for (const [ownerID, id] of this.shortcutTargets) if (id === group.id) this.shortcutTargets.delete(ownerID);
    for (const tab of group.tabs) if (tab.page) this.destroyPage(tab.page);
  }

  private closeDetachedWindows(owner?: BrowserWindow): void {
    for (const [window, page] of this.detachedWindows) {
      if (owner && page.owner !== owner) continue;
      this.detachedWindows.delete(window);
      if (!window.isDestroyed()) window.destroy();
    }
  }

  private layoutGroup(group: WorkspaceGroup): void {
    for (const tab of group.tabs) {
      if (!tab.page) continue;
      const bounds = tab.id === group.activeTabId ? group.bounds : null;
      // Inactive pages have already been hidden. Resizing their owner must not
      // wake them or disturb a view borrowed by an observation window.
      if (!bounds && !tab.page.bounds) continue;
      tab.page.bounds = bounds;
      tab.page.automation?.layout();
      this.layout(tab.page);
    }
  }

  private layout(page: WorkspacePage, captureBounds?: WebWorkspaceBounds): void {
    const { owner, view } = page;
    if (owner.isDestroyed() || page.contents.isDestroyed() || page.automation?.external) return;
    const reportedBounds = page.bounds ?? captureBounds;
    // The native parent already hides its children. Keep the selected view's
    // presentation intact so showing/restoring the window needs no extra frame.
    if (!reportedBounds) {
      if (page.contents.isFocused()) owner.webContents.focus();
      if (view.getVisible()) view.setVisible(false);
      return;
    }
    // Renderer bounds and native resize events can report the same geometry.
    // Chromium converts zoom through logarithmic levels, so compare with a
    // tolerance instead of reapplying it for floating-point round-off.
    if (Math.abs(page.contents.getZoomFactor() - page.tab.zoomFactor) > 1e-6) page.contents.setZoomFactor(page.tab.zoomFactor);
    const [width, height] = owner.getContentSize();
    const htmlFullscreen = page.htmlFullscreen && page.group.activeTabId === page.tab.id;
    const bounds = htmlFullscreen ? { x: 0, y: 0, width, height } : reportedBounds;
    const zoom = htmlFullscreen ? 1 : owner.webContents.getZoomFactor();
    const x = Math.max(0, Math.min(width, Math.round(bounds.x * zoom)));
    const y = Math.max(0, Math.min(height, Math.round(bounds.y * zoom)));
    // Window-filling pages retain their chrome insets through native live
    // resizing, including while the shell renderer is busy with its own layout.
    const rightEdge = "right" in bounds ? width - Math.round(bounds.right * zoom) : Math.round((bounds.x + bounds.width) * zoom);
    const bottomEdge = "bottom" in bounds ? height - Math.round(bounds.bottom * zoom) : Math.round((bounds.y + bounds.height) * zoom);
    const right = Math.max(x, Math.min(width, rightEdge));
    const bottom = Math.max(y, Math.min(height, bottomEdge));
    const current = view.getBounds();
    if (current.x !== x || current.y !== y || current.width !== right - x || current.height !== bottom - y) {
      view.setBounds({ x, y, width: right - x, height: bottom - y });
    }
    // View radii use native DIPs, like bounds. Shell zoom converts CSS pixels;
    // Electron applies display scaling itself, so multiplying DPR again would
    // over-round Retina corners. HTML fullscreen has no workspace canvas.
    const borderRadius = htmlFullscreen ? 0 : Math.max(0, Math.round((reportedBounds.borderRadius ?? 0) * zoom));
    if (page.borderRadius !== borderRadius) {
      view.setBorderRadius(borderRadius);
      page.borderRadius = borderRadius;
    }
    const visible = Boolean(page.bounds) && right > x && bottom > y;
    if (view.getVisible() !== visible) {
      view.setVisible(visible);
      if (visible && htmlFullscreen) page.contents.focus();
    }
  }

  private observeOwner(owner: BrowserWindow): void {
    const id = owner.webContents.id;
    if (this.observedOwners.has(id)) return;
    this.observedOwners.add(id);
    const layout = () => { for (const group of this.groups.values()) if (group.owner === owner) this.layoutGroup(group); };
    const closePages = () => {
      // Renderer recovery releases all native resources but keeps the safe
      // session metadata. Destinations restore lazily with the same profile.
      for (const group of [...this.groups.values()]) if (group.owner === owner) this.destroyGroup(group);
      this.closeDetachedWindows(owner);
      this.flushLibrary();
    };
    // Moving and resizing together can emit resize before the native content
    // size getter is updated. Finish that bounds operation before reading it;
    // this still runs in the same turn, without waiting for a renderer frame.
    owner.on("resize", () => queueMicrotask(layout));
    owner.on("show", layout);
    owner.on("hide", layout);
    owner.on("minimize", layout);
    owner.on("restore", layout);
    const focusFullscreenPage = () => {
      if (!owner.isFocused()) return;
      for (const page of this.pages.values()) {
        if (page.owner === owner && page.htmlFullscreen && page.group.activeTabId === page.tab.id
          && !page.contents.isDestroyed() && page.view.getVisible()) page.contents.focus();
      }
    };
    // Native activation can finish after the HTML fullscreen event. Follow
    // that event once rather than retrying focus or activating a background app.
    owner.on("focus", focusFullscreenPage);
    owner.on("enter-full-screen", () => {
      layout();
      focusFullscreenPage();
    });
    owner.on("leave-full-screen", layout);
    owner.webContents.on("did-start-navigation", details => { if (details.isMainFrame && !details.isSameDocument) closePages(); });
    owner.webContents.on("render-process-gone", closePages);
    owner.once("closed", () => { closePages(); this.observedOwners.delete(id); });
  }

  private handleInput(page: WorkspacePage, event: Electron.Event, input: Electron.Input): void {
    if (page.automation?.agentInput()) return;
    if (input.type !== "keyDown" || input.alt || !page.bounds) return;
    const modifier = process.platform === "darwin" ? input.meta : input.control;
    const key = input.key.toLowerCase();
    const group = page.group;
    if (key === "tab" && input.control && group.tabbed) {
      event.preventDefault();
      const index = group.tabs.findIndex(tab => tab.id === group.activeTabId);
      this.selectTab(page.owner, group.id, group.tabs[(index + (input.shift ? -1 : 1) + group.tabs.length) % group.tabs.length].id);
      this.activeTab(group)?.page?.contents.focus();
      return;
    }
    if (key === "f5") {
      event.preventDefault();
      this.command(page.owner, group.id, input.shift ? "reloadIgnoringCache" : "reload");
      return;
    }
    if (!modifier) return;
    if (group.tabbed && !input.shift && /^[1-9]$/.test(key)) {
      event.preventDefault();
      const tab = group.tabs[key === "9" ? group.tabs.length - 1 : Number(key) - 1];
      if (tab) { this.selectTab(page.owner, group.id, tab.id); tab.page?.contents.focus(); }
      return;
    }
    if (key === "l" || key === "f") {
      event.preventDefault();
      page.owner.webContents.focus();
      if (key === "l") this.onFocusAddress(page.owner, group.id); else this.options.onFocusFind?.(page.owner, group.id);
    } else if (key === "r") {
      event.preventDefault();
      this.command(page.owner, group.id, input.shift ? "reloadIgnoringCache" : "reload");
    } else if (key === "t" && group.tabbed) {
      event.preventDefault();
      if (group.tabs.length >= WEB_WORKSPACE_TAB_LIMIT) return;
      if (input.shift) this.reopenTab(page.owner, group.id);
      else {
        const next = this.createTab(page.owner, group.id);
        page.owner.webContents.focus();
        this.onFocusAddress(page.owner, group.id, next.activeTabId);
      }
    } else if (key === "w" && !input.shift && group.tabbed) {
      event.preventDefault();
      void this.closeTab(page.owner, group.id, group.activeTabId).then(() => {
        // Closing may finish after a newer address, overlay or window focus.
        // Restore keyboard navigation only if destruction left focus unowned.
        const next = this.activeTab(group)?.page;
        if (!webContents.getFocusedWebContents() && !page.owner.isDestroyed() && page.owner.isFocused() && next?.view.getVisible()) next.contents.focus();
      }).catch(error => console.warn("[web-workspace] Could not close tab", error));
    } else if (key === "+" || key === "=" || key === "-" || key === "0") {
      event.preventDefault();
      if (key === "0") this.setZoom(page.owner, group.id, 1); else this.stepZoom(page, key === "-" ? -1 : 1);
    }
  }

  private stepZoom(page: WorkspacePage, direction: number): void {
    const current = page.tab.zoomFactor;
    const next = direction > 0 ? ZOOM_LEVELS.find(level => level > current + 0.001) : [...ZOOM_LEVELS].reverse().find(level => level < current - 0.001);
    if (next) this.setZoom(page.owner, page.group.id, next);
  }

  private contextMenu(page: WorkspacePage, contents: WebContents, params: Electron.ContextMenuParams): void {
    const available = () => !contents.isDestroyed() && !this.automationRestricted(page);
    if (!available()) return;
    const downloadable = (url: string) => /^(https?:|blob:|data:)/i.test(url);
    const download = (url: string) => {
      // Native downloads retain the browser session and reach the shared download
      // manager without requiring the website to read cross-origin image bytes.
      if (!available()) return;
      const referrer = contextDownloadReferrer(params, url);
      contents.downloadURL(url, referrer ? { headers: { Referer: referrer } } : undefined);
    };
    const template: Electron.MenuItemConstructorOptions[] = [];
    let link: string | undefined;
    try { if (params.linkURL) link = websiteURL(params.linkURL); } catch { /* Internal and executable URLs are not browser destinations. */ }
    if (link || downloadable(params.linkURL)) {
      const url = link ?? params.linkURL;
      if (link && page.group.tabbed) template.push({ label: "Open Link in New Tab", enabled: page.group.tabs.length < WEB_WORKSPACE_TAB_LIMIT, click: () => { if (available()) this.createTab(page.owner, page.group.id, url, { background: true }); } });
      template.push(
        { label: "Save Link As…", click: () => download(url) },
        { label: "Copy Link Address", click: () => { if (available()) clipboard.writeText(url); } },
        { type: "separator" },
      );
    }
    if (params.mediaType === "image" && downloadable(params.srcURL)) {
      let imageURL: string | undefined;
      try { imageURL = websiteURL(params.srcURL); } catch { /* Blob and data images can be saved without becoming persistent tabs. */ }
      if (imageURL && page.group.tabbed) {
        const url = imageURL;
        template.push({ label: "Open Image in New Tab", enabled: page.group.tabs.length < WEB_WORKSPACE_TAB_LIMIT, click: () => { if (available()) this.createTab(page.owner, page.group.id, url, { background: true }); } });
      }
      template.push(
        { label: "Save Image As…", enabled: params.hasImageContents, click: () => download(params.srcURL) },
        { label: "Copy Image", enabled: params.hasImageContents, click: () => { if (available()) contents.copyImageAt(params.x, params.y); } },
        { label: "Copy Image Address", click: () => { if (available()) clipboard.writeText(params.srcURL); } },
        { type: "separator" },
      );
    }
    if (params.isEditable) template.push({ role: "undo" }, { role: "redo" }, { type: "separator" }, { role: "cut" }, { role: "copy" }, { role: "paste" }, { role: "selectAll" });
    else if (params.selectionText) template.push({ role: "copy" }, { role: "selectAll" });
    else template.push(
      { label: "Back", enabled: contents.navigationHistory.canGoBack(), click: () => { if (available()) contents.navigationHistory.goBack(); } },
      { label: "Forward", enabled: contents.navigationHistory.canGoForward(), click: () => { if (available()) contents.navigationHistory.goForward(); } },
      { label: "Reload", click: () => {
        if (!available()) return;
        if (contents === page.contents) this.command(page.owner, page.group.id, "reload");
        else contents.reload();
      } },
    );
    Menu.buildFromTemplate(template).popup({ window: BrowserWindow.fromWebContents(contents) ?? page.owner });
  }

  private secureContents(page: WorkspacePage, contents: WebContents, detached = false): void {
    const contentsID = contents.id;
    const owned: PermissionOwner = { page, grants: new Set(), navigationGeneration: 0 };
    this.contentsOwners.set(contentsID, owned);
    contents.on("context-menu", (_event, params) => this.contextMenu(page, contents, params));
    contents.once("destroyed", () => this.contentsOwners.delete(contentsID));
    contents.on("did-start-navigation", details => { if (details.isMainFrame && !details.isSameDocument) owned.navigationGeneration++; });
    contents.on("will-attach-webview", event => event.preventDefault());
    contents.on("will-prevent-unload", event => {
      if (page.discarding) return;
      const parent = BrowserWindow.fromWebContents(contents) ?? page.owner;
      // The native dialog identifies the origin, rather than trusting site
      // wording. Stay is the safe default for unsaved forms and editors.
      const response = parent.isDestroyed() || !parent.isVisible() || parent.isMinimized() ? 0 : dialog.showMessageBoxSync(parent, {
        type: "question", title: "Leave this website?",
        message: `${requestOrigin(contents.getURL()) ?? "This website"} may have unsaved changes.`,
        detail: "Leaving or closing this page may discard changes you have made.",
        buttons: ["Stay", "Leave"], defaultId: 0, cancelId: 0, noLink: true,
      });
      if (response === 1) event.preventDefault();
      else if (contents === page.contents) {
        page.requestedURL = contents.getURL() || page.tab.url;
        page.error = undefined;
        page.navigationGeneration++;
        page.pendingClose?.resolve();
        page.pendingClose = undefined;
        this.emit(page.group);
      }
    });
    const guardNavigation = (event: Electron.Event, url: string) => {
      try { websiteURL(url); } catch { event.preventDefault(); void this.openExternal(page, contents, url); }
    };
    contents.on("will-navigate", guardNavigation);
    contents.on("will-redirect", guardNavigation);
    contents.setWindowOpenHandler(details => {
      const parent = BrowserWindow.fromWebContents(contents) ?? page.owner;
      const restricted = this.automationRestricted(page);
      if (parent.isDestroyed() || (!restricted && (!parent.isVisible() || parent.isMinimized() || (contents === page.contents && !page.bounds)))) return { action: "deny" };
      const target = details.url || "about:blank";
      if (target !== "about:blank") {
        try { websiteURL(target); } catch { void this.openExternal(page, contents, target); return { action: "deny" }; }
      }
      const ribbon = page.group.id === WEB_WORKSPACE_RIBBON_ID;
      const requestedPopup = details.features.split(",").some(feature => /^(popup|width|height|left|top)\s*(=|$)/i.test(feature.trim()));
      // A user's separate window survives its source tab. Agent-created and
      // feature/OAuth popups retain the existing opener-bound lifecycle.
      const separateWindow = !restricted && page.group.tabbed && details.disposition === "new-window" && !requestedPopup;
      const popup = (target === "about:blank" && !ribbon) || !page.group.tabbed || (!ribbon && page.group.tabs.length >= WEB_WORKSPACE_TAB_LIMIT) || contents !== page.contents || details.disposition === "new-window"
        || requestedPopup;
      if (!popup && page.group.tabs.length >= WEB_WORKSPACE_TAB_LIMIT) return { action: "deny" };
      const response: Electron.WindowOpenHandlerResponse = {
        action: "allow", outlivesOpener: !popup || separateWindow,
        overrideBrowserWindowOptions: {
          parent: page.owner, width: 1000, height: 750, autoHideMenuBar: true,
          ...(restricted ? { show: false } : {}),
          frame: true, alwaysOnTop: false, fullscreen: false,
          webPreferences: {
            partition: BROWSER_PARTITION, preload: join(__dirname, "../preload/browserPage.cjs"), contextIsolation: true,
            nodeIntegration: false, nodeIntegrationInSubFrames: false,
            nodeIntegrationInWorker: false, sandbox: true, webviewTag: false,
            webSecurity: true, allowRunningInsecureContent: false,
          },
        },
      };
      if (separateWindow) response.createWindow = options => {
        const window = new BrowserWindow(options);
        this.attachPopup(page, contents, window, true);
        return window.webContents;
      };
      else if (!popup) response.createWindow = options => {
        const view = createBrowserView(options);
        if (this.pages.get(page.id) !== page || this.groups.get(page.group.id) !== page.group) {
          view.webContents.close();
          return view.webContents;
        }
        const tab: WorkspaceTab = { id: ribbon ? `website:${randomUUID()}` : randomUUID(), url: target, title: "", zoomFactor: 1, startPage: false };
        page.group.tabs.push(tab);
        // A child keeps user ownership, but cannot escape its opener's live
        // agent restrictions. Tokens expire independently; a later attachment
        // of this child retains its own restriction until that lease releases.
        const restrictions = [...(page.inheritedAutomation ?? []), ...(page.automation ? [page.automation.restriction] : [])].filter(lease => lease.active);
        const child = this.attachPage(page.group, tab, view, restrictions);
        // Electron cannot reconstruct a live WindowProxy connection on restore.
        page.discardProtected = true;
        child.discardProtected = true;
        // Keep Chromium's WindowProxy/opener, original POST, and referrer. A
        // supplied guest is already navigating; never load it a second time.
        if (!restricted && details.disposition !== "background-tab") this.activate(page.group, tab);
        this.emit(page.group);
        if (ribbon && !restricted && details.disposition !== "background-tab") this.options.onReveal?.(page.owner, page.group.id);
        return view.webContents;
      };
      return response;
    });
    contents.on("did-create-window", popup => this.attachPopup(page, contents, popup, detached));
  }

  private attachPopup(page: WorkspacePage, opener: WebContents, popup: BrowserWindow, detached: boolean): void {
    page.discardProtected = true;
    if (detached) this.detachedWindows.set(popup, page);
    else page.popups.add(popup);
    this.secureContents(page, popup.webContents, detached);
    const openerOrigin = requestOrigin(opener.getURL());
    const showOrigin = () => popup.setTitle(`Website: ${requestOrigin(popup.webContents.getURL()) ?? openerOrigin ?? "New window"}`);
    popup.webContents.on("did-navigate", showOrigin);
    popup.webContents.on("page-title-updated", event => { event.preventDefault(); showOrigin(); });
    showOrigin();
    popup.once("closed", () => { page.popups.delete(popup); this.detachedWindows.delete(popup); });
  }
  private async openExternal(page: WorkspacePage, contents: WebContents, value: string): Promise<void> {
    if (this.automationRestricted(page)) return;
    // Never hand local files, executable URLs, or browser-internal schemes to
    // the OS. Custom application links still require an explicit user choice.
    let url: URL;
    try { url = new URL(value); } catch { return; }
    if (["http:", "https:", "file:", "javascript:", "data:", "blob:", "about:", "chrome:", "devtools:", "wuu-file:", "wuu-artifact:", "wuu-plugin:"].includes(url.protocol)
      || this.externalPrompts.has(contents.id) || page.owner.isDestroyed()) return;
    const origin = requestOrigin(contents.getURL());
    const parent = BrowserWindow.fromWebContents(contents) ?? page.owner;
    if (!parent.isVisible() || parent.isMinimized() || (contents === page.contents && !page.bounds)) return;
    this.externalPrompts.add(contents.id);
    try {
      const { response } = await dialog.showMessageBox(parent, {
        type: "question", title: "Open another application?",
        message: `This website wants to open a ${url.protocol.slice(0, -1)} link.`,
        detail: value, buttons: ["Cancel", "Open"], defaultId: 0, cancelId: 0, noLink: true,
      });
      if (response === 1 && !this.automationRestricted(page) && !contents.isDestroyed() && this.contentsOwners.get(contents.id)?.page === page
        && requestOrigin(contents.getURL()) === origin) await shell.openExternal(value);
    } catch (error) {
      console.warn("[web-workspace] Could not open external link", error);
    } finally {
      this.externalPrompts.delete(contents.id);
    }
  }
}
