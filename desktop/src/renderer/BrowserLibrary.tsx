import { Tooltip } from "./Tooltip";
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import type { ApplicationWebWorkspaceV1 as WebWorkspaceApi } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { persistentWebWorkspaceURL } from "../shared/webWorkspaceLibrary";
import type { WebWorkspaceLibraryStateV1 as WebWorkspaceLibraryState } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { Bookmark, BookOpen, Clock3, Pencil, Search, X } from "./WuuIcons";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import { rawErrorMessage } from "./UserFacingErrors";
import { showActionErrorToast } from "./Toast";
import { NativeBrowserPopover } from "./NativeBrowserPopover";
import "./styles/browser-library.css";

export function BrowserLibrary({ api, currentURL, currentTitle, onOpen, active = true, destinationId, presentation = "toolbar" }: {
  api?: WebWorkspaceApi;
  currentURL: string;
  currentTitle: string;
  onOpen: (url: string) => void;
  active?: boolean;
  destinationId?: string;
  presentation?: "toolbar" | "menu";
}): JSX.Element {
  const { t, formatDate } = useI18n();
  const library = api?.library;
  const [state, setState] = useState<WebWorkspaceLibraryState>();
  const [open, setOpen] = useState(false);
  const [section, setSection] = useState<"history" | "bookmarks">("history");
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(100);
  const [pending, setPending] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [editing, setEditing] = useState<{ url: string; title: string }>();
  const [error, setError] = useState("");
  const [position, setPosition] = useState<CSSProperties>({ visibility: "hidden" });
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const cancelClear = useRef<HTMLButtonElement>(null);
  const bookmarkTitle = useRef<HTMLInputElement>(null);
  const updates = useRef(0);
  const panelId = useId();
  const headingId = useId();
  const safeURL = persistentWebWorkspaceURL(currentURL);
  const bookmarked = Boolean(safeURL && state?.bookmarks.some((entry) => entry.url === safeURL));

  const refresh = useCallback(() => {
    if (!library) return;
    const version = updates.current;
    void library.getState().then((next) => {
      if (version === updates.current) { updates.current++; setState(next); setError(""); }
    }).catch((cause: unknown) => { if (version === updates.current) setError(rawErrorMessage(cause)); });
  }, [library]);

  useEffect(() => {
    if (!library) return;
    const unsubscribe = library.onState((next) => { updates.current++; setState(next); });
    refresh();
    return () => { updates.current++; unsubscribe(); };
  }, [library, refresh]);

  useEffect(() => { if (!active) setOpen(false); }, [active]);
  useEffect(() => { setLimit(100); }, [query, section]);
  useEffect(() => {
    if (open && confirmClear) cancelClear.current?.focus();
  }, [open, confirmClear]);

  useEffect(() => { if (editing) bookmarkTitle.current?.focus(); }, [editing?.url]);

  const close = useCallback((restoreFocus = true) => {
    setOpen(false);
    setConfirmClear(false);
    setEditing(undefined);
    if (restoreFocus) trigger.current?.focus();
  }, []);

  useLayoutEffect(() => {
    if (!open) return;
    const place = (): void => {
      const anchor = trigger.current?.getBoundingClientRect();
      const content = panel.current;
      if (!anchor || !content) return;
      const margin = 12;
      const top = Math.min(anchor.bottom + 8, Math.max(margin, window.innerHeight - 200));
      const left = Math.max(margin, Math.min(anchor.right - content.offsetWidth, window.innerWidth - content.offsetWidth - margin));
      setPosition({ left, top, maxHeight: Math.max(0, window.innerHeight - top - margin) });
    };
    place();
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(place) : undefined;
    if (panel.current) observer?.observe(panel.current);
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    const frame = requestAnimationFrame(() => search.current?.focus());
    return () => {
      observer?.disconnect();
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    refresh();
    const outside = (event: PointerEvent): void => {
      const target = event.target as Node;
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) close(false);
    };
    const focusOutside = (event: FocusEvent): void => {
      const target = event.target as Node;
      if (!panel.current?.contains(target) && !trigger.current?.contains(target)) close(false);
    };
    const escape = (event: KeyboardEvent): void => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      if (editing) { setEditing(undefined); search.current?.focus(); }
      else if (confirmClear) { setConfirmClear(false); search.current?.focus(); } else close();
    };
    document.addEventListener("pointerdown", outside, true);
    document.addEventListener("focusin", focusOutside, true);
    document.addEventListener("keydown", escape, true);
    return () => {
      document.removeEventListener("pointerdown", outside, true);
      document.removeEventListener("focusin", focusOutside, true);
      document.removeEventListener("keydown", escape, true);
    };
  }, [open, refresh, close, confirmClear, editing?.url]);

  const changeBookmark = async (url: string, title: string, value: boolean): Promise<boolean> => {
    if (!library || pending) return false;
    setPending(true);
    setError("");
    const version = updates.current;
    try {
      const next = await library.setBookmark(url, title, value);
      if (version === updates.current) { updates.current++; setState(next); }
      return true;
    } catch (cause) {
      const message = rawErrorMessage(cause);
      setError(message);
      if (!open) showActionErrorToast(message);
      return false;
    } finally { setPending(false); }
  };

  const removeHistory = async (id: string): Promise<void> => {
    if (!library || pending) return;
    setPending(true);
    setError("");
    const version = updates.current;
    try {
      const next = await library.removeHistory(id);
      if (version === updates.current) { updates.current++; setState(next); }
      search.current?.focus();
    } catch (cause) { setError(rawErrorMessage(cause)); }
    finally { setPending(false); }
  };

  const clearHistory = async (): Promise<void> => {
    if (!library || pending) return;
    setPending(true);
    setError("");
    const version = updates.current;
    try {
      const next = await library.clearHistory();
      if (version === updates.current) { updates.current++; setState(next); }
      setConfirmClear(false);
    } catch (cause) { setError(rawErrorMessage(cause)); }
    finally { setPending(false); }
  };

  const entries = state?.[section] ?? [];
  const needle = query.trim().toLowerCase();
  const matching = needle ? entries.filter((entry) => `${entry.title}\n${entry.url}`.toLowerCase().includes(needle)) : entries;
  const bookmarkLabel = t(bookmarked ? "browserLibrary.removeBookmark" : "browserLibrary.addBookmark");
  return <>
    <Tooltip asChild content={safeURL ? bookmarkLabel : t("browserLibrary.privacy")} disabled={presentation === "menu" && Boolean(safeURL)}><button type="button" className={presentation === "menu" ? "web-workspace-menu-item browser-library-bookmark" : "icon-button workspace-browser-nav browser-library-bookmark"} data-browser-bookmark-current
      aria-label={bookmarkLabel} aria-pressed={bookmarked}
      disabled={!library || !safeURL || pending} onClick={() => void changeBookmark(currentURL, currentTitle, !bookmarked)}>
      <Bookmark className="icon" aria-hidden="true" />
      {presentation === "menu" ? <span>{bookmarkLabel}</span> : null}
    </button></Tooltip>
    <Tooltip asChild content={t("browserLibrary.title")} disabled={presentation === "menu"}><button ref={trigger} type="button" className={presentation === "menu" ? "web-workspace-menu-item" : "icon-button workspace-browser-nav"} data-browser-library-trigger
      aria-label={t("browserLibrary.title")} aria-haspopup="dialog" aria-expanded={open}
      aria-controls={open ? panelId : undefined} disabled={!library} onClick={() => { if (open) close(); else { setOpen(true); setConfirmClear(false); } }}>
      <BookOpen className="icon" aria-hidden="true" />
      {presentation === "menu" ? <span>{t("browserLibrary.title")}</span> : null}
    </button></Tooltip>
    {open ? <NativeBrowserPopover controls={api} onDismiss={() => close(false)}><UILayerPortal layer="popover">
      <div ref={panel} id={panelId} role="dialog" aria-labelledby={headingId} className="browser-library-panel"
        data-browser-library data-wuu-layer="popover" data-wuu-component="popover" data-web-workspace-owner={destinationId} style={position}
        onPointerDown={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key !== "Escape") return;
          event.preventDefault(); event.stopPropagation();
          if (editing) { setEditing(undefined); search.current?.focus(); }
          else if (confirmClear) { setConfirmClear(false); search.current?.focus(); } else close();
        }}>
        <div className="browser-library-heading">
          <span id={headingId}>{t("browserLibrary.title")}</span>
          <button type="button" className="icon-button" aria-label={t("browserLibrary.close")} onClick={() => close()}><X className="icon" /></button>
        </div>
        <div className="browser-library-sections" role="group" aria-label={t("browserLibrary.title")}>
          <button type="button" data-library-section="history" aria-pressed={section === "history"} onClick={() => { setSection("history"); setConfirmClear(false); setEditing(undefined); }}><Clock3 className="icon" />{t("browserLibrary.history")}</button>
          <button type="button" data-library-section="bookmarks" aria-pressed={section === "bookmarks"} onClick={() => { setSection("bookmarks"); setConfirmClear(false); setEditing(undefined); }}><Bookmark className="icon" />{t("browserLibrary.bookmarks")}</button>
        </div>
        <label className="browser-library-search"><Search className="icon" /><input ref={search} type="search" value={query}
          aria-label={t("browserLibrary.search")} placeholder={t("browserLibrary.search")} onChange={(event) => setQuery(event.target.value)} /></label>
        {(error || state?.error) ? <p className="browser-library-error" role="alert">{error || state?.error}</p> : null}
        {confirmClear ? <div className="browser-library-confirm" role="alert">
          <p>{t("browserLibrary.confirmClear")}</p>
          <div><button ref={cancelClear} type="button" className="settings-button settings-button-ghost" disabled={pending} onClick={() => { setConfirmClear(false); search.current?.focus(); }}>{t("browserLibrary.cancel")}</button>
            <button type="button" className="settings-button" data-library-confirm-clear disabled={pending} onClick={() => void clearHistory()}>{t("browserLibrary.clearHistory")}</button></div>
        </div> : <div className="browser-library-results">
          {!state && !error ? <p className="browser-library-empty" role="status">{t("browserLibrary.loading")}</p>
            : matching.length ? <ul>{matching.slice(0, limit).map((entry) => <li key={entry.id} data-library-entry>
              {editing?.url === entry.url && section === "bookmarks" ? <form className="browser-library-edit" onSubmit={(event) => {
                event.preventDefault();
                void changeBookmark(entry.url, editing.title, true).then((saved) => { if (saved) setEditing(undefined); });
              }}>
                <label>{t("browserLibrary.bookmarkTitle")}<input ref={bookmarkTitle} data-library-bookmark-title value={editing.title}
                  disabled={pending} maxLength={512} onChange={(event) => setEditing({ url: entry.url, title: event.target.value })} /></label>
                <div><button type="button" className="settings-button settings-button-ghost" disabled={pending} onClick={() => setEditing(undefined)}>{t("browserLibrary.cancel")}</button>
                  <button type="submit" className="settings-button" data-library-save-bookmark disabled={pending}>{t("browserLibrary.save")}</button></div>
              </form> : <>
              <button type="button" className="browser-library-open" data-library-url={entry.url}
                onClick={() => { close(false); onOpen(entry.url); }}>
                <span className="browser-library-entry-title">{entry.title || entry.url}</span>
                <span className="browser-library-entry-url">{entry.url}</span>
                {"visitedAt" in entry ? <time dateTime={new Date(entry.visitedAt).toISOString()}>{formatDate(entry.visitedAt, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}</time> : null}
              </button>
              {section === "bookmarks" ? <button type="button" className="icon-button" data-library-edit-bookmark disabled={pending}
                aria-label={`${t("browserLibrary.renameBookmark")}: ${entry.title || entry.url}`}
                onClick={() => { setError(""); setEditing({ url: entry.url, title: entry.title }); }}><Pencil className="icon" /></button> : null}
              {section === "bookmarks" ? <button type="button" className="icon-button browser-library-remove" disabled={pending}
                aria-label={`${t("browserLibrary.removeBookmark")}: ${entry.title || entry.url}`}
                onClick={() => void changeBookmark(entry.url, entry.title, false)}><X className="icon" /></button>
                : <button type="button" className="icon-button browser-library-remove" data-library-remove-history={entry.id} disabled={pending}
                  aria-label={`${t("browserLibrary.removeHistory")}: ${entry.title || entry.url}`}
                  onClick={() => void removeHistory(entry.id)}><X className="icon" /></button>}
              </>}
            </li>)}</ul>
              : <p className="browser-library-empty">{t(needle ? "browserLibrary.noResults" : section === "history" ? "browserLibrary.emptyHistory" : "browserLibrary.emptyBookmarks")}</p>}
          {matching.length > limit ? <button type="button" className="settings-button settings-button-ghost browser-library-more" onClick={() => setLimit((value) => value + 100)}>{t("browserLibrary.showMore")}</button> : null}
        </div>}
        <div className="browser-library-footer">
          <p>{t("browserLibrary.privacy")}</p>
          {section === "history" && Boolean(state?.history.length) && !confirmClear ? <button type="button" className="settings-button settings-button-ghost" data-library-clear-history onClick={() => setConfirmClear(true)}>{t("browserLibrary.clearHistory")}</button> : null}
        </div>
      </div>
    </UILayerPortal></NativeBrowserPopover> : null}
  </>;
}
