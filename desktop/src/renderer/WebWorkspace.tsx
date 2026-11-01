import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { ApplicationWebWorkspaceV1 as WebWorkspaceApi, WebWorkspaceBoundsV1 as WebWorkspaceBounds, WebWorkspaceCommandV1 as WebWorkspaceCommand, WebWorkspaceStateV1 as WebWorkspaceState } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { useWebWorkspaceControls } from "./WebWorkspaceControlsContext";
import { ArrowLeft, ArrowRight, ChevronDown, ChevronUp, Ellipsis, Globe, Hand, RotateCw, Search, X } from "./WuuIcons";
import { useI18n } from "./i18n";
import { normalizeWebsiteURL } from "./NavigationRibbonPreferences";
import { rawErrorMessage } from "./UserFacingErrors";
import { showActionErrorToast } from "./Toast";
import { WebWorkspaceDownloadsMenu } from "./WebWorkspaceDownloadsMenu";
import { BrowserLibrary } from "./BrowserLibrary";
import { WebWorkspaceAddress } from "./WebWorkspaceAddress";
import { WebWorkspaceStartPage } from "./WebWorkspaceStartPage";
import { menuOpeningKey } from "./ComposerFloatingMenu";
import "./styles/web-workspace.css";

export interface WebWorkspaceSite {
  id: string;
  url: string;
  title?: string;
}

/** Shared browser surface for user destinations and extension-provided pages. */
export function WebWorkspacePanel({
  site,
  active = true,
  tabs = false,
  resizeWithWindow = false,
}: {
  site?: WebWorkspaceSite;
  active?: boolean;
  tabs?: boolean;
  resizeWithWindow?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const controls = useWebWorkspaceControls();
  const api = controls.capabilities.available ? controls : undefined;
  const panelRef = useRef<HTMLDivElement>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const pendingHides = useRef(new Map<string, number>());
  const inputRef = useRef<HTMLInputElement>(null);
  const startInputRef = useRef<HTMLInputElement>(null);
  const [focusNewTab, setFocusNewTab] = useState<string>();
  const findInputRef = useRef<HTMLInputElement>(null);
  const composingFind = useRef(false);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const [page, setPage] = useState<WebWorkspaceState>();
  const [failure, setFailure] = useState<{ id: string; message: string }>();
  const [address, setAddress] = useState("");
  const [addressError, setAddressError] = useState("");
  const [findOpen, setFindOpen] = useState(false);
  const [findText, setFindText] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);
  const addressEdited = useRef(false);
  const lastAddressPage = useRef("");
  const instanceID = useId();
  const addressErrorID = `${instanceID}-address-error`;
  const currentSiteID = useRef(site?.id);
  currentSiteID.current = site?.id;
  const current = page?.id === site?.id ? page : undefined;
  const error = failure && failure.id === site?.id ? failure.message : current?.error;
  const currentURL = current?.url || site?.url || "";
  const blank = currentURL === "about:blank" && current?.startPage !== false;
  const zoomFactor = current?.zoomFactor ?? 1;
  const pageTabs = current?.tabs ?? [];
  const hasCurrent = Boolean(current);
  const snapshotKey = `${site?.id ?? ""}/${current?.activeTabId ?? ""}/${currentURL}`;
  const snapshotKeyRef = useRef(snapshotKey);
  snapshotKeyRef.current = snapshotKey;
  const captureInFlight = useRef<string | undefined>(undefined);
  const [backdrop, setBackdrop] = useState<{ key: string; image: string }>();
  const [occluded, setOccluded] = useState(false);
  const captureBackdrop = useCallback(async (): Promise<void> => {
    const id = site?.id;
    if (!api?.capture || !id || !active || !hasCurrent || error || blank) return;
    const key = snapshotKeyRef.current;
    // Loading and an opening overlay can request the same expensive native readback.
    if (captureInFlight.current === key) return;
    captureInFlight.current = key;
    try {
      const image = await api.capture(id);
      if (image && snapshotKeyRef.current === key) setBackdrop({ key, image });
    } catch { /* A preview is optional; it never blocks page interaction. */ }
    finally { if (captureInFlight.current === key) captureInFlight.current = undefined; }
  }, [api, site?.id, active, hasCurrent, error, blank]);

  useEffect(() => {
    setBackdrop(undefined);
  }, [snapshotKey]);

  useEffect(() => {
    if (!current?.loading) void captureBackdrop();
  }, [captureBackdrop, current?.loading, snapshotKey]);

  const focusAddress = useCallback(() => {
    setFocusNewTab(undefined);
    setMenuOpen(false);
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const update = useCallback(async (operation: (browser: WebWorkspaceApi, id: string) => Promise<WebWorkspaceState>): Promise<WebWorkspaceState | undefined> => {
    if (!api || !site) return;
    const { id } = site;
    setFailure(undefined);
    try {
      const next = await operation(api, id);
      if (currentSiteID.current === id) setPage(next);
      return next;
    } catch (cause) {
      if (currentSiteID.current === id) {
        if (hasCurrent) showActionErrorToast(rawErrorMessage(cause));
        else setFailure({ id, message: rawErrorMessage(cause) });
      }
    }
  }, [api, site?.id, hasCurrent]);

  useEffect(() => {
    if (!api) return;
    return api.onState((next) => {
      if (next.id !== currentSiteID.current) return;
      setPage(next);
    });
  }, [api]);

  useEffect(() => {
    if (!api || !active) return;
    const visible = (id: string): boolean => id === currentSiteID.current && Boolean(hostRef.current && visibleElementRect(hostRef.current));
    const offAddress = api.onFocusAddress((id, newTabId) => {
      if (!visible(id)) return;
      if (newTabId) { setMenuOpen(false); setFocusNewTab(newTabId); }
      else focusAddress();
    });
    const offFind = api.onFocusFind((id) => {
      if (!visible(id)) return;
      setMenuOpen(false);
      setFindOpen(true);
      findInputRef.current?.focus();
      findInputRef.current?.select();
    });
    return () => { offAddress(); offFind(); };
  }, [api, active, focusAddress]);

  useEffect(() => {
    if (!api || !site || !active) return;
    let disposed = false;
    const { id, url } = site;
    setFailure(undefined);
    void api.open({ id, url, tabs }).then((next) => {
      if (!disposed) setPage(next);
    }).catch((cause: unknown) => {
      if (!disposed) setFailure({ id, message: rawErrorMessage(cause) });
    });
    return () => { disposed = true; };
  }, [api, site?.id, site?.url, active, tabs]);

  useEffect(() => {
    const addressPage = `${site?.id}/${current?.activeTabId ?? ""}`;
    const changedPage = lastAddressPage.current !== addressPage;
    lastAddressPage.current = addressPage;
    // Redirects must not replace a new address while the user is typing it.
    if (!changedPage && addressEdited.current && (document.activeElement === inputRef.current || document.activeElement === startInputRef.current)) return;
    addressEdited.current = false;
    setAddress(blank ? "" : currentURL);
    setAddressError("");
  }, [currentURL, site?.id, current?.activeTabId, blank]);

  useEffect(() => {
    setMenuOpen(false);
    setFindOpen(false);
    composingFind.current = false;
    setFindText("");
  }, [site?.id, current?.activeTabId, active]);

  useEffect(() => { setFocusNewTab(undefined); }, [site?.id]);

  useLayoutEffect(() => {
    if (findOpen) { findInputRef.current?.focus(); findInputRef.current?.select(); }
  }, [findOpen]);

  useLayoutEffect(() => {
    // The ribbon can create/select a blank tab before this panel mounts.
    if (tabs && active && blank && current?.activeTabId) startInputRef.current?.focus();
  }, [tabs, active, blank, current?.activeTabId]);

  const run = useCallback(async (action: WebWorkspaceCommand, url?: string): Promise<void> => {
    await update(async (browser, id) => {
      // A failed initial creation remains retryable through the address bar.
      if (!current || action === "reload" || action === "reloadIgnoringCache") await browser.open({ id, url: site!.url, tabs });
      return browser.command(id, action, url);
    });
  }, [update, current, site?.url, tabs]);

  const addTab = useCallback(async (): Promise<void> => {
    const id = site?.id;
    const next = await update((browser, destination) => browser.createTab(destination));
    if (next && currentSiteID.current === id) {
      setFocusNewTab(next.activeTabId);
      setMenuOpen(false);
    }
  }, [update, site?.id]);

  useLayoutEffect(() => {
    if (!focusNewTab) return;
    if (!active) { setFocusNewTab(undefined); return; }
    // Native shortcut state and focus requests arrive separately. Keep the
    // request until its tab has rendered instead of focusing stale chrome.
    if (focusNewTab !== current?.activeTabId) {
      // Once the new tab exists, another selected tab is a newer user choice.
      if (pageTabs.some(tab => tab.id === focusNewTab)) setFocusNewTab(undefined);
      return;
    }
    setFocusNewTab(undefined);
    if (blank) startInputRef.current?.focus();
  }, [active, blank, current?.activeTabId, focusNewTab, pageTabs]);

  const closeFind = useCallback(() => {
    setFindOpen(false);
    composingFind.current = false;
    setFindText("");
    void update(async (browser, id) => { await browser.stopFind(id); return browser.command(id, "focus"); });
  }, [update]);

  const find = useCallback((text: string, forward = true, findNext = false) => {
    void update((browser, id) => text ? browser.find(id, text, { forward, findNext }) : browser.stopFind(id));
  }, [update]);

  const openFind = useCallback(() => {
    setMenuOpen(false);
    setFindOpen(true);
    findInputRef.current?.focus();
    findInputRef.current?.select();
  }, []);

  const changeZoom = useCallback((factor: number) => {
    void update((browser, id) => browser.setZoom(id, Math.max(controls.limits.zoomMin, Math.min(controls.limits.zoomMax, Math.round(factor * 100) / 100))));
  }, [controls, update]);

  useLayoutEffect(() => {
    // Tabs share this host. Replacing its observer on tab selection would hide
    // the native page that the main process has just selected, then show it again.
    if (site?.id) {
      cancelAnimationFrame(pendingHides.current.get(site.id) ?? 0);
      pendingHides.current.delete(site.id);
    }
    const host = hostRef.current;
    const id = site?.id;
    if (!api || !id || !host || !current) return;
    const hideAfterPaint = (): void => {
      cancelAnimationFrame(pendingHides.current.get(id) ?? 0);
      // Layout effects run before the shell paints its next destination. Keep
      // the live page above it until that paint, rather than exposing old canvas.
      pendingHides.current.set(id, requestAnimationFrame(() => {
        pendingHides.current.set(id, requestAnimationFrame(() => {
          pendingHides.current.delete(id);
          void api.reportBounds(id, null).catch(() => undefined);
        }));
      }));
    };
    if (!active || error || blank) {
      hideAfterPaint();
      return;
    }
    let disposed = false;
    let wasVisible = false;
    const report = (bounds: WebWorkspaceBounds | null): void => {
      void api.reportBounds(id, bounds).catch((cause: unknown) => {
        if (!disposed && currentSiteID.current === id) setFailure({ id, message: rawErrorMessage(cause) });
      });
    };
    // A development hot update can run against the preceding native process.
    // Keep rectangle reports until its matching preload has been restarted.
    const stop = observeWebWorkspaceBounds(host, resizeWithWindow && api.capabilities.windowInsets, (bounds) => {
      if (disposed) return;
      setOccluded(bounds === null || bounds.occluded === true);
      if (bounds && !bounds.occluded) { wasVisible = true; report(bounds); return; }
      // Keep the hidden viewport current, including a first selected tab under
      // an already expanded rail. Native capture does not make that tab visible.
      report(bounds);
      // Keep a still image under renderer menus instead of replacing the whole
      // site with empty canvas. Capture once per occlusion, never per frame.
      if (wasVisible) void captureBackdrop();
      wasVisible = false;
    });
    return () => {
      disposed = true;
      stop();
      hideAfterPaint();
    };
  }, [api, site?.id, active, resizeWithWindow, Boolean(current), error, blank, captureBackdrop]);

  useEffect(() => {
    if (!api || !site || !active) return;
    const id = site.id;
    let last: boolean | undefined;
    let frame = 0;
    let candidates: HTMLElement[] = [];
    let candidatesDirty = true;
    const report = (): void => {
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      if (candidatesDirty) {
        candidates = [...document.querySelectorAll<HTMLElement>(SHORTCUT_TARGETS)];
        candidatesDirty = false;
      }
      const panel = panelRef.current;
      const selected = Boolean(panel && focusedWebWorkspacePanel(candidates) === panel);
      if (last === selected) return;
      last = selected;
      void api.setShortcutTarget(id, selected).catch(() => undefined);
    };
    // Streaming text and hover styles do not change which page owns shortcuts.
    const schedule = (): void => { if (!frame) frame = requestAnimationFrame(report); };
    const mutation = new MutationObserver((records) => {
      if (panelRef.current && mutationAffectsSurface(records, panelRef.current, SHORTCUT_TARGETS, candidates)) {
        candidatesDirty ||= records.some((record) => record.type === "childList" || record.attributeName !== "style");
        // Ownership must settle before a following native Cmd+W/Cmd+R.
        report();
      }
    });
    mutation.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["hidden", "inert", "class", "style", "open", "aria-modal"] });
    const onFocus = (): void => {
      // A newly mounted focus target can arrive before its mutation notification.
      candidatesDirty = true;
      report();
    };
    document.addEventListener("focusin", onFocus);
    window.addEventListener("resize", schedule);
    report();
    return () => {
      mutation.disconnect();
      if (frame) cancelAnimationFrame(frame);
      document.removeEventListener("focusin", onFocus);
      window.removeEventListener("resize", schedule);
      void api.setShortcutTarget(id, false).catch(() => undefined);
    };
  }, [api, site?.id, active]);

  useEffect(() => {
    if (!active || !site) return;
    const onKeyDown = (event: KeyboardEvent): void => {
      const key = event.key.toLowerCase();
      const modifier = controls.platform === "darwin" ? event.metaKey : event.ctrlKey;
      const cycleTabs = key === "tab" && event.ctrlKey;
      if ((!modifier && !cycleTabs) || event.altKey || event.defaultPrevented) return;
      // Retained plugin pages may be inert behind another page. Only the visible
      // focused browser surface owns browser shortcuts.
      if (!hostRef.current || !visibleElementRect(hostRef.current)) return;
      const target = focusedWebWorkspacePanel();
      if (target !== panelRef.current) return;
      let action: (() => void) | undefined;
      if (key === "l") action = focusAddress;
      else if (key === "f") action = openFind;
      else if (key === "r") action = () => { void run(event.shiftKey ? "reloadIgnoringCache" : "reload"); };
      else if (key === "0") action = () => changeZoom(1);
      else if (key === "=" || key === "+") action = () => changeZoom(zoomFactor + 0.1);
      else if (key === "-") action = () => changeZoom(zoomFactor - 0.1);
      else if (tabs && key === "t") action = () => { if (pageTabs.length >= controls.limits.tabs) return; if (event.shiftKey) void update((browser, id) => browser.reopenTab(id)); else void addTab(); };
      else if (tabs && key === "w" && current?.activeTabId) action = () => { void update((browser, id) => browser.closeTab(id, current.activeTabId!)); };
      else if (tabs && pageTabs.length && (cycleTabs || /^[1-9]$/.test(key))) {
        const position = pageTabs.findIndex((tab) => tab.id === current?.activeTabId);
        const index = key === "tab" ? (position + (event.shiftKey ? -1 : 1) + pageTabs.length) % pageTabs.length : key === "9" ? pageTabs.length - 1 : Number(key) - 1;
        if (pageTabs[index]) action = () => { void update((browser, id) => browser.selectTab(id, pageTabs[index].id)); };
      }
      if (action) { event.preventDefault(); event.stopImmediatePropagation(); action(); }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [controls, active, site?.id, tabs, current?.activeTabId, pageTabs, focusAddress, openFind, run, update, addTab, changeZoom, zoomFactor]);

  const changeAddress = (value: string): void => {
    addressEdited.current = true;
    setAddress(value);
    setAddressError("");
  };

  const restoreAddress = (): void => {
    addressEdited.current = false;
    setAddress(blank ? "" : currentURL);
    setAddressError("");
    void run("focus");
  };

  const openAddress = (input: string): boolean => {
    const value = input.trim();
    const hasScheme = /^[a-z][a-z\d+.-]*:/i.test(value) && !/^(?:localhost|[^\s/:]+\.[^\s/:]+|\[[\da-f:]+\]):\d+(?:[/#?]|$)/i.test(value);
    const isAddress = hasScheme || /^(?:localhost|\[[\da-f:]+\])(?:[:/\?#]|$)/i.test(value) || /^[^\s]+[./][^\s]*$/.test(value);
    const url = !value || /[\u0000-\u001f\u007f]/.test(value) ? null : isAddress ? normalizeWebsiteURL(value) : `https://www.google.com/search?q=${encodeURIComponent(value)}`;
    if (!url) { setAddressError(t("webWorkspace.invalidURL")); return false; }
    addressEdited.current = false;
    setAddress(url);
    setAddressError("");
    void run("navigate", url);
    return true;
  };

  const openExternal = (): void => {
    const url = normalizeWebsiteURL(currentURL);
    setMenuOpen(false);
    if (url) void controls.openExternal(url).catch((cause: unknown) => showActionErrorToast(rawErrorMessage(cause)));
  };

  const openOptions = async (): Promise<void> => {
    if (!api || !site || menuOpen || !menuButtonRef.current) return;
    const key = snapshotKeyRef.current;
    const rect = menuButtonRef.current.getBoundingClientRect();
    setMenuOpen(true);
    try {
      const action = await api.showOptions(site.id, { x: rect.left, y: rect.bottom, labels: {
        find: t("webWorkspace.find"), zoomOut: t("webWorkspace.zoomOut"), resetZoom: t("webWorkspace.resetZoom"),
        zoomIn: t("webWorkspace.zoomIn"), reopenTab: t("webWorkspace.reopenTab"), openExternal: t("webWorkspace.openExternal"),
      } });
      if (snapshotKeyRef.current !== key) return;
      if (action === "find") openFind();
      else if (action === "zoomOut") changeZoom(zoomFactor - 0.1);
      else if (action === "resetZoom") changeZoom(1);
      else if (action === "zoomIn") changeZoom(zoomFactor + 0.1);
      else if (action === "reopenTab") void update((browser, id) => browser.reopenTab(id));
      else if (action === "openExternal") openExternal();
    } catch (cause) { showActionErrorToast(rawErrorMessage(cause)); }
    finally { setMenuOpen(false); }
  };

  useEffect(() => {
    window.dispatchEvent(new Event("wuu:page-context-source-changed"));
    return () => { queueMicrotask(() => window.dispatchEvent(new Event("wuu:page-context-source-changed"))); };
  }, [active, blank, error, site?.id, current?.activeTabId, current?.title]);

  return (
    <div ref={panelRef} className="web-workspace-panel" data-wuu-component="web-workspace" data-website-id={site?.id}
      data-page-context-active={active && !blank && !error ? "true" : undefined}
      data-page-context-tab-id={current?.activeTabId} data-page-context-title={current?.title}>
      <div className="workspace-browser-toolbar web-workspace-toolbar" role="toolbar" aria-label={site?.title || t("webWorkspace.address")}>
        <div className="workspace-browser-nav-cluster">
          <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.back")} title={t("webWorkspace.back")} disabled={!api || !current?.canGoBack} onClick={() => void run("back")}><ArrowLeft className="icon" aria-hidden="true" /></button>
          <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.forward")} title={t("webWorkspace.forward")} disabled={!api || !current?.canGoForward} onClick={() => void run("forward")}><ArrowRight className="icon" aria-hidden="true" /></button>
          <button type="button" className="icon-button workspace-browser-nav" aria-label={t(current?.loading ? "webWorkspace.stop" : "webWorkspace.reload")} title={t(current?.loading ? "webWorkspace.stop" : "webWorkspace.reload")} disabled={!api || !site || blank} onClick={() => void run(current?.loading ? "stop" : "reload")}>{current?.loading ? <X className="icon" aria-hidden="true" /> : <RotateCw className="icon" aria-hidden="true" />}</button>
        </div>
        <WebWorkspaceAddress api={api} destinationId={site?.id} pageKey={`${site?.id}/${current?.activeTabId}`} active={active}
          inputRef={inputRef} value={address} disabled={!site || !api} invalid={Boolean(addressError)} errorID={addressErrorID}
          onChange={changeAddress} onSubmit={openAddress} onEscape={restoreAddress}>
          {zoomFactor !== 1 ? <button type="button" className="web-workspace-zoom-badge" title={t("webWorkspace.resetZoom")} aria-label={t("webWorkspace.resetZoom")} onClick={() => changeZoom(1)}>{Math.round(zoomFactor * 100)}%</button> : null}
        </WebWorkspaceAddress>
        <div className="web-workspace-actions">
          {current?.agentControlled ? <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.takeOver")} title={t("webWorkspace.takeOver")} onClick={() => void run("takeOver")}><Hand className="icon" aria-hidden="true" /></button> : null}
          <BrowserLibrary api={api} destinationId={site?.id} currentURL={currentURL} currentTitle={current?.title || site?.title || ""} onOpen={(url) => void run("navigate", url)} active={active} />
          <WebWorkspaceDownloadsMenu api={api} active={active} destinationId={site?.id} />
          <button ref={menuButtonRef} type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.options")} title={t("webWorkspace.options")} aria-expanded={menuOpen} aria-haspopup="menu" disabled={!api || !site} onClick={() => void openOptions()} onKeyDown={(event) => { if (menuOpeningKey(event)) void openOptions(); }}><Ellipsis className="icon" aria-hidden="true" /></button>
        </div>
        {addressError ? <p className="web-workspace-address-error" id={addressErrorID} role="alert">{addressError}</p> : null}
      </div>
      {findOpen ? <div className="web-workspace-findbar" role="search" aria-label={t("webWorkspace.find")}>
        <Search className="icon" aria-hidden="true" />
        <input ref={findInputRef} value={findText} type="text" aria-label={t("webWorkspace.find")} placeholder={t("webWorkspace.find")}
          onCompositionStart={() => { composingFind.current = true; }} onCompositionEnd={() => { composingFind.current = false; }}
          onBlur={() => { composingFind.current = false; }} onChange={(event) => { setFindText(event.target.value); find(event.target.value); }} onKeyDown={(event) => {
          if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229 || composingFind.current) return;
          if (event.key === "Escape") { event.preventDefault(); closeFind(); }
          if (event.key === "Enter") { event.preventDefault(); find(findText, !event.shiftKey, true); }
        }} />
        <span className="web-workspace-find-count" role="status" aria-live="polite">{findText ? t("webWorkspace.findMatches", { current: current?.findResult?.activeMatchOrdinal ?? 0, total: current?.findResult?.matches ?? 0 }) : ""}</span>
        <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.findPrevious")} title={t("webWorkspace.findPrevious")} disabled={!findText} onClick={() => find(findText, false, true)}><ChevronUp className="icon" aria-hidden="true" /></button>
        <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.findNext")} title={t("webWorkspace.findNext")} disabled={!findText} onClick={() => find(findText, true, true)}><ChevronDown className="icon" aria-hidden="true" /></button>
        <button type="button" className="icon-button workspace-browser-nav" aria-label={t("webWorkspace.closeFind")} onClick={closeFind}><X className="icon" aria-hidden="true" /></button>
      </div> : null}
      <div className="web-workspace-content" id={`${instanceID}-page`}>
        {occluded && backdrop?.key === snapshotKey ? <img className="web-workspace-backdrop" src={backdrop.image} alt="" aria-hidden="true" /> : null}
        <div ref={hostRef} className="web-workspace-host" data-wuu-native-surface={active && current && !error && !blank ? "" : undefined} />
        {!api || error ? (
          <div className="web-workspace-message" role={error ? "alert" : "status"}>
            <Globe className="icon" aria-hidden="true" />
            <p>{t(!api ? "webWorkspace.unavailable" : "webWorkspace.loadFailed")}</p>
            {error ? <p className="web-workspace-error-detail">{error}</p> : null}
            <div className="web-workspace-message-actions">
              {api ? <button type="button" className="settings-button" onClick={() => void run("reload")}>{t("webWorkspace.retry")}</button> : null}
              <button type="button" className="settings-button settings-button-ghost" disabled={blank} onClick={openExternal}>{t("webWorkspace.openExternal")}</button>
            </div>
          </div>
        ) : blank ? <WebWorkspaceStartPage key={`${site?.id}/${current?.activeTabId}`} active={active} searchField={
          <WebWorkspaceAddress className="web-workspace-start-address" api={api} destinationId={site?.id} pageKey={`${site?.id}/${current?.activeTabId}`} active={active}
            inputRef={startInputRef} value={address} disabled={!site || !api} invalid={Boolean(addressError)} errorID={addressErrorID}
            onChange={changeAddress} onSubmit={openAddress} onEscape={restoreAddress} leading={<Search className="icon" aria-hidden="true" />}>
            <button type="submit" className="icon-button workspace-browser-nav web-workspace-start-submit" disabled={!address.trim()} aria-label={t("webWorkspace.addressSearch")} title={t("webWorkspace.addressSearch")}><ArrowRight className="icon" aria-hidden="true" /></button>
          </WebWorkspaceAddress>
        } />
          : !current ? <div className="web-workspace-message" role="status"><p>{t("webWorkspace.loading")}</p></div> : null}
      </div>
    </div>
  );
}

type HostRect = { x: number; y: number; width: number; height: number };
const MODALS = '[aria-modal="true"], dialog[open]';
const FLOATING_LAYERS = '[role="dialog"], [role="menu"], [data-wuu-layer="popover"], .plugin-workbench-view-overlay';
const NOTICES = '[data-wuu-layer="notice"]';
const OCCLUDERS = `${MODALS}, ${FLOATING_LAYERS}, ${NOTICES}`;
const SHORTCUT_TARGETS = `${MODALS}, .web-workspace-panel`;

function focusedWebWorkspacePanel(candidates = [...document.querySelectorAll<HTMLElement>(SHORTCUT_TARGETS)]): Element | undefined {
  const focused = document.activeElement;
  const panels = candidates.filter((element) => element.matches(".web-workspace-panel"));
  const owner = focused?.closest<HTMLElement>("[data-web-workspace-owner]")?.dataset.webWorkspaceOwner;
  const direct = focused?.closest(".web-workspace-panel");
  const target = panels.find((panel) => (panel === direct || panel.dataset.websiteId === owner) && visibleElementRect(panel))
    ?? (focused === document.body || focused === document.documentElement || !focused ? panels.find(visibleElementRect)
      : panels.find((panel) => panel.closest(".web-workspace-stage") && visibleElementRect(panel)));
  // An auxiliary extension browser never owns shortcuts while the user is
  // typing in a conversation or another workbench surface.
  if (!target || candidates.some((layer) => layer.matches(MODALS) && !layer.contains(target) && visibleElementRect(layer))) return;
  return target;
}

function visibleElementRect(element: Element): DOMRect | null {
  if (!(element instanceof HTMLElement) || element.closest('[hidden], [inert]')) return null;
  if (typeof element.checkVisibility === "function" && !element.checkVisibility({ checkVisibilityCSS: true })) return null;
  const style = getComputedStyle(element);
  if (style.display === "none" || style.visibility === "hidden") return null;
  const rect = element.getBoundingClientRect();
  return rect.width > 0 && rect.height > 0 ? rect : null;
}

function mutationAffectsSurface(records: MutationRecord[], host: Element | null, selector: string, tracked: Element[]): boolean {
  return records.some((record) => {
    if (record.type === "attributes") {
      const target = record.target;
      return target instanceof Element && (target.contains(host) || target.matches(selector)
        || tracked.some((element) => target.contains(element)));
    }
    return tracked.some((element) => element.contains(record.target))
      || [...record.addedNodes, ...record.removedNodes].some((node) => node instanceof Element
      && (node.contains(host) || node.matches(selector) || Boolean(node.querySelector(selector))));
  });
}

// Native views sit above every renderer layer. Hide the surface for modal UI
// and overlapping menus, then restore the same page when the overlay closes.
function observeWebWorkspaceBounds(host: HTMLElement, resizeWithWindow: boolean, report: (bounds: WebWorkspaceBounds | null) => void): () => void {
  let last = "";
  let frame = 0;
  let overlays: Element[] = [];
  let overlaysDirty = true;
  const movingAnimations = new Set<Animation>();
  const trackMotion = (element: Element): void => {
    if (!element.contains(host) && !element.matches(OCCLUDERS) && !element.querySelector(OCCLUDERS)) return;
    for (const animation of element.getAnimations()) {
      // Scroll-linked effects can remain "running" while stationary. Scroll
      // events already schedule their geometry; only timed effects need a loop.
      if (animation.timeline === document.timeline && (animation.playState === "running" || animation.pending)) movingAnimations.add(animation);
    }
  };
  const send = (): void => {
    if (frame) cancelAnimationFrame(frame);
    frame = 0;
    const rect = visibleElementRect(host);
    // Window occlusion is owned by the native parent, not this layout contract.
    // A hidden document must retain the selected page's restore geometry.
    let bounds: HostRect | null = rect ? {
      x: Math.round(rect.left), y: Math.round(rect.top), width: Math.round(rect.width), height: Math.round(rect.height),
    } : null;
    let occluded = false;
    if (bounds) {
      if (overlaysDirty) {
        // Resizing changes geometry, not membership. Reuse the candidate list
        // until an actual overlay/ancestor mutation invalidates it.
        overlays = [...document.querySelectorAll(OCCLUDERS)];
        overlaysDirty = false;
      }
      for (const overlay of overlays) {
        if (overlay.contains(host)) continue;
        const obstacle = visibleElementRect(overlay);
        if (!obstacle) continue;
        const overlaps = obstacle.left < bounds.x + bounds.width && obstacle.right > bounds.x
          && obstacle.top < bounds.y + bounds.height && obstacle.bottom > bounds.y;
        if (overlay.matches(NOTICES) && overlaps) {
          // A top notice only reserves its shallow strip. Keep the rest of the
          // page usable instead of blanking it for the toast's entire lifetime.
          const bottom = bounds.y + bounds.height;
          bounds.y = Math.min(bottom, Math.ceil(obstacle.bottom + 4));
          bounds.height = bottom - bounds.y;
          if (bounds.height > 0) continue;
        }
        if (overlay.matches(MODALS) || overlaps) {
          occluded = true;
          break;
        }
      }
    }
    // The native window can grow before this renderer gets another frame.
    // Insets let its view follow that resize immediately, without replaying a
    // stale viewport size. Local extension surfaces still report their rectangle.
    const geometry: WebWorkspaceBounds | null = bounds && resizeWithWindow ? {
      x: bounds.x, y: bounds.y,
      right: window.innerWidth - bounds.x - bounds.width,
      bottom: window.innerHeight - bounds.y - bounds.height,
    } : bounds;
    const presented = geometry ? { ...geometry, borderRadius: Number.parseFloat(getComputedStyle(host).borderBottomLeftRadius), ...(occluded ? { occluded: true as const } : {}) } : null;
    // Page zoom changes the native scale even when CSS insets stay identical.
    const key = JSON.stringify([presented, window.devicePixelRatio]);
    if (key !== last) { last = key; report(presented); }
    for (const animation of movingAnimations) {
      const target = animation.effect instanceof KeyframeEffect ? animation.effect.target : null;
      if (!target?.isConnected || animation.timeline !== document.timeline
        || (animation.playState !== "running" && !animation.pending)) movingAnimations.delete(animation);
    }
    if (movingAnimations.size) frame = requestAnimationFrame(send);
  };
  const schedule = (): void => { if (!frame) frame = requestAnimationFrame(send); };
  const onMotionStart = (event: Event): void => {
    if (!(event.target instanceof Element)) return;
    trackMotion(event.target);
    if (movingAnimations.size) schedule();
  };
  const onMotionEnd = (): void => {
    // Each effect has its own lifetime. An earlier opacity animation ending
    // must not stop tracking a transform still moving the same surface.
    if (!movingAnimations.size) return;
    schedule();
  };
  const resized = typeof ResizeObserver === "function" ? new ResizeObserver(schedule) : undefined;
  resized?.observe(host);
  const mutation = new MutationObserver((records) => {
    if (mutationAffectsSurface(records, null, OCCLUDERS, overlays)) {
      overlaysDirty ||= records.some((record) => record.type === "childList" || record.attributeName !== "style");
      // Hide native content before newly opened renderer overlays can paint.
      send();
    } else if (mutationAffectsSurface(records, host, OCCLUDERS, overlays)) {
      schedule();
    }
  });
  mutation.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class", "style", "hidden", "inert", "open", "role", "aria-modal", "data-wuu-layer", "data-settings-open", "data-web-workspace-open"] });
  window.addEventListener("resize", schedule);
  window.addEventListener("scroll", schedule, true);
  document.addEventListener("visibilitychange", send);
  document.addEventListener("animationstart", onMotionStart);
  document.addEventListener("animationend", onMotionEnd);
  document.addEventListener("animationcancel", onMotionEnd);
  document.addEventListener("transitionrun", onMotionStart);
  document.addEventListener("transitionend", onMotionEnd);
  document.addEventListener("transitioncancel", onMotionEnd);
  // A surface may mount while its parent is already moving. Transforms change
  // position without triggering ResizeObserver, so follow the active effects.
  for (const animation of document.getAnimations()) {
    const target = animation.effect instanceof KeyframeEffect ? animation.effect.target : null;
    if (target) trackMotion(target);
  }
  send();
  return () => {
    if (frame) cancelAnimationFrame(frame);
    resized?.disconnect();
    mutation.disconnect();
    window.removeEventListener("resize", schedule);
    window.removeEventListener("scroll", schedule, true);
    document.removeEventListener("visibilitychange", send);
    document.removeEventListener("animationstart", onMotionStart);
    document.removeEventListener("animationend", onMotionEnd);
    document.removeEventListener("animationcancel", onMotionEnd);
    document.removeEventListener("transitionrun", onMotionStart);
    document.removeEventListener("transitionend", onMotionEnd);
    document.removeEventListener("transitioncancel", onMotionEnd);
    report(null);
  };
}
