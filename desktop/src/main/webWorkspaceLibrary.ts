import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { WEB_WORKSPACE_RIBBON_ID, WEB_WORKSPACE_TAB_LIMIT, WEB_WORKSPACE_ZOOM_MIN, WEB_WORKSPACE_ZOOM_MAX } from "../shared/webWorkspace";
import {
  persistentWebWorkspaceURL,
  type WebWorkspaceBookmark,
  type WebWorkspaceHistoryEntry,
  type WebWorkspaceLibraryState,
  type WebWorkspaceSavedSession,
} from "../shared/webWorkspaceLibrary";
import { writeTextFileAtomicSync } from "./atomicFile";

const MAX_HISTORY = 1000;
const HISTORY_AGE = 90 * 24 * 60 * 60 * 1000;
const MAX_BOOKMARKS = 1000;
const MAX_SESSIONS = 100;

type Store = {
  version: 1;
  history: WebWorkspaceHistoryEntry[];
  bookmarks: WebWorkspaceBookmark[];
  sessions: Record<string, WebWorkspaceSavedSession>;
  ribbon?: WebWorkspaceSavedSession;
};

function titleText(value: unknown, fallback: string): string {
  return typeof value === "string" && value.trim() ? value.trim().slice(0, 512) : fallback;
}

function validId(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && value.length <= 256;
}

function validTime(value: unknown): value is number {
  return typeof value === "number" && value >= 0 && !Number.isNaN(new Date(value).getTime());
}

function restorableURL(input: string): string | undefined {
  if (input === "about:blank") return input;
  const safe = persistentWebWorkspaceURL(input);
  if (safe) return safe;
  // Authentication callbacks must never be replayed on restart. Keep only
  // the origin, without a callback path, parameters, or page title.
  try {
    const url = new URL(input);
    if (/^https?:$/.test(url.protocol) && !url.username && !url.password) return `${url.origin}/`;
  } catch { /* Invalid addresses are not restorable. */ }
  return undefined;
}

function savedSession(value: unknown, ribbon = false): WebWorkspaceSavedSession | undefined {
  if (!value || typeof value !== "object" || !("tabs" in value) || !Array.isArray(value.tabs)) return undefined;
  const tabs: WebWorkspaceSavedSession["tabs"] = [];
  // A migration may contain every tab from every old website. Keep those
  // existing pages even when their total exceeds the new-tab resource limit.
  for (const tab of ribbon ? value.tabs : value.tabs.slice(0, WEB_WORKSPACE_TAB_LIMIT)) {
    if (!tab || !validId(tab.id) || typeof tab.url !== "string" || tabs.some((entry) => entry.id === tab.id)) continue;
    const url = restorableURL(tab.url);
    if (!url) continue;
    tabs.push({ id: tab.id, url, title: url === tab.url ? titleText(tab.title, url) : new URL(url).hostname,
      ...(tab.pinned === true ? { pinned: true } : {}),
      ...(tab.muted === true ? { muted: true } : {}),
      ...(typeof tab.zoomFactor === "number" && Number.isFinite(tab.zoomFactor)
        && tab.zoomFactor >= WEB_WORKSPACE_ZOOM_MIN && tab.zoomFactor <= WEB_WORKSPACE_ZOOM_MAX
        ? { zoomFactor: tab.zoomFactor } : {}) });
  }
  if (!tabs.length && !ribbon) return undefined;
  const requestedActive = "activeTabId" in value ? value.activeTabId : undefined;
  const homeURL = "homeURL" in value && typeof value.homeURL === "string" ? restorableURL(value.homeURL) : undefined;
  const migratedTabGroups: Record<string, string[]> = Object.create(null);
  if (ribbon && "migratedTabGroups" in value && value.migratedTabGroups && typeof value.migratedTabGroups === "object") {
    for (const [id, tabIds] of Object.entries(value.migratedTabGroups)) {
      if (validId(id) && Array.isArray(tabIds) && tabIds.every(validId)) migratedTabGroups[id] = [...tabIds];
    }
  }
  return { tabs, activeTabId: tabs.some((tab) => tab.id === requestedActive) ? requestedActive as string : tabs[0]?.id ?? "", ...(homeURL ? { homeURL } : {}),
    ...(Object.keys(migratedTabGroups).length ? { migratedTabGroups } : {}) };
}

/** Navigation metadata only. The browser's cookies and credentials stay in its profile. */
export class WebWorkspaceLibrary {
  private store: Store = { version: 1, history: [], bookmarks: [], sessions: Object.create(null) };
  private loaded = false;
  private readError?: Error;
  private writeError?: Error;
  private dirty = false;
  private timer?: ReturnType<typeof setTimeout>;

  constructor(private readonly path: string) {}

  getState(): WebWorkspaceLibraryState {
    this.load();
    this.pruneHistory();
    return {
      history: this.store.history.map((entry) => ({ ...entry })),
      bookmarks: this.store.bookmarks.map((entry) => ({ ...entry })),
      ...((this.readError || this.writeError) ? { error: "Browser history and bookmarks could not be saved. Check available disk space and restart Wuu." } : {}),
    };
  }

  recordVisit({ url: input, title }: { url: string; title: string }): void {
    this.load();
    const url = persistentWebWorkspaceURL(input);
    if (!url) return;
    const previous = this.store.history.find((entry) => entry.url === url);
    this.store.history = [
      { id: previous?.id ?? randomUUID(), url, title: titleText(title, url), visitedAt: Date.now() },
      ...this.store.history.filter((entry) => entry.url !== url),
    ].slice(0, MAX_HISTORY);
    this.pruneHistory();
    this.schedule();
  }

  updateTitle(input: string, title: string): void {
    this.load();
    const url = persistentWebWorkspaceURL(input);
    const entry = this.store.history.find((candidate) => candidate.url === url);
    if (!entry || !title.trim() || entry.title === title) return;
    entry.title = titleText(title, entry.url);
    this.schedule();
  }

  setBookmark(input: string, title: string, bookmarked: boolean): WebWorkspaceLibraryState {
    this.load();
    if (this.readError) throw this.readError;
    const url = persistentWebWorkspaceURL(input);
    if (!url) throw new Error("Authentication links and non-web addresses cannot be bookmarked.");
    const previous = this.store.bookmarks.find((entry) => entry.url === url);
    if (bookmarked && !previous && this.store.bookmarks.length >= MAX_BOOKMARKS) {
      throw new Error("The bookmark limit has been reached. Remove a bookmark before adding another.");
    }
    const next = this.store.bookmarks.filter((entry) => entry.url !== url);
    if (bookmarked) next.unshift({ id: previous?.id ?? randomUUID(), url, title: titleText(title, url), createdAt: previous?.createdAt ?? Date.now() });
    const original = this.store.bookmarks;
    this.store.bookmarks = next;
    this.dirty = true;
    try { this.flush(); } catch (error) { this.store.bookmarks = original; throw error; }
    return this.getState();
  }

  removeHistory(id: string): WebWorkspaceLibraryState {
    this.load();
    if (this.readError) throw this.readError;
    if (!validId(id)) throw new Error("Invalid browser history entry.");
    const original = this.store.history;
    this.store.history = original.filter((entry) => entry.id !== id);
    if (this.store.history.length === original.length) return this.getState();
    this.dirty = true;
    try { this.flush(); } catch (error) { this.store.history = original; throw error; }
    return this.getState();
  }

  clearHistory(): WebWorkspaceLibraryState {
    this.load();
    if (this.readError) throw this.readError;
    const original = this.store.history;
    this.store.history = [];
    this.dirty = true;
    try { this.flush(); } catch (error) { this.store.history = original; throw error; }
    return this.getState();
  }

  getSession(id: string): WebWorkspaceSavedSession | undefined {
    this.load();
    const session = id === WEB_WORKSPACE_RIBBON_ID ? this.store.ribbon : this.store.sessions[id];
    return session ? { ...session, tabs: session.tabs.map((tab) => ({ ...tab })) } : undefined;
  }

  saveSession(id: string, session: WebWorkspaceSavedSession): void {
    this.load();
    if (!validId(id)) return;
    const ribbon = id === WEB_WORKSPACE_RIBBON_ID;
    const clean = savedSession(session, ribbon);
    if (ribbon) {
      if (!clean || JSON.stringify(this.store.ribbon) === JSON.stringify(clean)) return;
      this.store.ribbon = clean;
      this.schedule();
      return;
    }
    if (!clean || JSON.stringify(this.store.sessions[id]) === JSON.stringify(clean)) return;
    delete this.store.sessions[id];
    this.store.sessions[id] = clean;
    const ids = Object.keys(this.store.sessions);
    for (const oldest of ids.slice(0, Math.max(0, ids.length - MAX_SESSIONS))) delete this.store.sessions[oldest];
    this.schedule();
  }

  removeSession(id: string): void {
    this.load();
    if (id === WEB_WORKSPACE_RIBBON_ID) {
      this.store.ribbon = { tabs: [], activeTabId: "" };
      this.schedule();
      return;
    }
    if (!Object.hasOwn(this.store.sessions, id)) return;
    delete this.store.sessions[id];
    this.schedule();
  }

  /** Flush once on shell shutdown, before destroying tabs. */
  flush(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
    if (!this.dirty) return;
    // Preserve the original file if it could not be decoded or read.
    if (this.readError) throw this.readError;
    try {
      writeTextFileAtomicSync(this.path, JSON.stringify(this.store));
      this.dirty = false;
      this.writeError = undefined;
    } catch (error) {
      this.writeError = error instanceof Error ? error : new Error(String(error));
      throw error;
    }
  }

  private schedule(): void {
    this.dirty = true;
    if (this.timer) return;
    this.timer = setTimeout(() => {
      try { this.flush(); } catch (error) { console.warn("[web-workspace] Could not save browser library:", error); }
    }, 500);
    this.timer.unref();
  }

  private pruneHistory(): void {
    const cutoff = Date.now() - HISTORY_AGE;
    const history = this.store.history.filter((entry) => entry.visitedAt >= cutoff).slice(0, MAX_HISTORY);
    if (history.length === this.store.history.length) return;
    this.store.history = history;
    this.schedule();
  }

  private load(): void {
    if (this.loaded) return;
    this.loaded = true;
    try {
      const data: unknown = JSON.parse(readFileSync(this.path, "utf8"));
      if (!data || typeof data !== "object" || !("version" in data) || data.version !== 1) throw new Error("Unsupported browser library file.");
      if ("history" in data && Array.isArray(data.history)) {
        const seen = new Set<string>();
        for (const entry of data.history.slice(0, MAX_HISTORY)) {
          const url = entry && persistentWebWorkspaceURL(entry.url);
          if (!url || !validId(entry.id) || !validTime(entry.visitedAt) || seen.has(url)) continue;
          seen.add(url);
          this.store.history.push({ id: entry.id, url, title: titleText(entry.title, url), visitedAt: entry.visitedAt });
        }
      }
      if ("bookmarks" in data && Array.isArray(data.bookmarks)) {
        const seen = new Set<string>();
        for (const entry of data.bookmarks.slice(0, MAX_BOOKMARKS)) {
          const url = entry && persistentWebWorkspaceURL(entry.url);
          if (!url || !validId(entry.id) || !validTime(entry.createdAt) || seen.has(url)) continue;
          seen.add(url);
          this.store.bookmarks.push({ id: entry.id, url, title: titleText(entry.title, url), createdAt: entry.createdAt });
        }
      }
      if ("sessions" in data && data.sessions && typeof data.sessions === "object") {
        for (const [id, value] of Object.entries(data.sessions).slice(-MAX_SESSIONS)) {
          const session = savedSession(value);
          if (validId(id) && session) this.store.sessions[id] = session;
        }
      }
      if ("ribbon" in data) this.store.ribbon = savedSession(data.ribbon, true);
      this.pruneHistory();
    } catch (error) {
      if (!(error && typeof error === "object" && "code" in error && error.code === "ENOENT")) {
        this.readError = error instanceof Error ? error : new Error(String(error));
        console.warn("[web-workspace] Could not read browser library:", error);
      }
    }
  }
}
