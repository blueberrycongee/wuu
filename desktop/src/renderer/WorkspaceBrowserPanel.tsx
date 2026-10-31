import {
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  RotateCw,
  X
} from "./WuuIcons";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent
} from "react";
import type { ApplicationBrowserV1, BrowserActivityV1 as ActivitySession, BrowserDockTargetV1 as BrowserDockTarget,
  BrowserSurfaceV1 as BrowserSurfaceSnapshot, BrowserSurfaceHandleV1, BrowserNavigationRequestV1 as WorkspaceBrowserNavigationRequest,
} from "../../../packages/plugin-sdk/src/ui-browser";
import type { ApplicationPreviewContextV1 as RuntimeContext } from "../../../packages/plugin-sdk/src/ui-preview";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useBrowserControls } from "./BrowserControlsContext";
import { useI18n } from "./i18n";
import { showActionErrorToast, toastErrorMessage } from "./Toast";
import { normalizeWebsiteURL } from "./NavigationRibbonPreferences";
import { browserTabIDForActivity, displayedBrowserTabID, observeBrowserPanelBounds } from "./BrowserVisibility";
import { workspaceBrowserOpenTarget } from "./WorkspaceBrowserOpen";



const HOME_PAGE_URL = "wuu://new-tab";
const SEARCH_FALLBACK_URL = "https://www.google.com/search?igu=1&q=";

type BrowserStatus = "idle" | "loading" | "error";

function looksLikeUrl(input: string): boolean {
  const value = input.trim();
  if (value.length === 0) {
    return false;
  }
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(value)) {
    return true;
  }
  // loopback host with optional port
  if (/^localhost(:\d+)?([/#?].*)?$/i.test(value)) {
    return true;
  }
  // IPv4 loopback / unspecified address with optional port
  if (/^(?:127\.0\.0\.1|0\.0\.0\.0)(?::\d+)?(?:[/#?].*)?$/.test(value)) {
    return true;
  }
  // bare host with optional port and path/query
  if (/^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+(:\d+)?([/#?].*)?$/.test(value)) {
    return true;
  }
  return false;
}

function resolveNavigationInput(input: string): string {
  const value = input.trim();
  if (value.length === 0) {
    return HOME_PAGE_URL;
  }
  if (value === HOME_PAGE_URL) {
    return HOME_PAGE_URL;
  }
  if (looksLikeUrl(value)) {
    return /^https?:\/\//i.test(value) || /^file:\/\//i.test(value)
      ? value
      : normalizeWebsiteURL(value) ?? `${SEARCH_FALLBACK_URL}${encodeURIComponent(value)}`;
  }
  return `${SEARCH_FALLBACK_URL}${encodeURIComponent(value)}`;
}

function hasPageURL(url: string | undefined): boolean {
  const value = url?.trim() ?? "";
  return value.length > 0 && value !== "about:blank" && value !== HOME_PAGE_URL;
}

// Chrome for one browser tab. The page is a main-process view positioned over
// the host element. The agent and the address bar drive that same tab: an
// empty state is shown only while the tab has no page.
type WorkspaceBrowserPanelProps = {
  visible?: boolean;
  threadID?: string;
  activeContext?: RuntimeContext;
  activity?: ActivitySession;
  dockTarget?: BrowserDockTarget;
  requestedURL?: WorkspaceBrowserNavigationRequest;
  overlaySuppressed?: boolean;
  // Remove once WorkspacePanels drops the legacy App pause callback; takeover is service-owned.
  onUserInteraction?: (inputGeneration?: number) => void | Promise<void>;
};

export function WorkspaceBrowserPanel(props: WorkspaceBrowserPanelProps): JSX.Element {
  const service = useBrowserControls();
  const { t } = useI18n();
  if (!service?.available) return <div className="workspace-browser-error" role="alert">{t("workspace.browser.previewUnavailable")}</div>;
  return <BrowserPanel {...props} service={service} />;
}

function BrowserPanel({ visible = true, threadID, activeContext, activity, dockTarget,
  requestedURL, overlaySuppressed = false, service,
}: WorkspaceBrowserPanelProps & { service: ApplicationBrowserV1 }): JSX.Element {
  const { t } = useI18n();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const workdir = activeContext?.cwd;
  const agentTabID =
    activity && activity.kind === "browser" && activity.state !== "stopped"
      ? browserTabIDForActivity(activity)
      : undefined;
  const requestedTabID = dockTarget && dockTarget.thread_id === threadID && dockTarget.workdir === workdir
    ? dockTarget.tabID : undefined;
  const sourceKey = JSON.stringify([workdir, threadID, requestedTabID, agentTabID]);
  const [adoptedTab, setAdoptedTab] = useState<{ sourceKey: string; tabID: string }>();
  const selectedTabID = (adoptedTab?.sourceKey === sourceKey ? adoptedTab.tabID : undefined)
    ?? requestedTabID ?? displayedBrowserTabID(activity, threadID);
  const selectedTabIDRef = useRef(selectedTabID);
  selectedTabIDRef.current = selectedTabID;

  const [currentURL, setCurrentURL] = useState("");
  const [draftURL, setDraftURL] = useState("");
  const [status, setStatus] = useState<BrowserStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | undefined>(undefined);
  const [canGoBack, setCanGoBack] = useState(false);
  const [canGoForward, setCanGoForward] = useState(false);
  const [pendingURL, setPendingURL] = useState<string | undefined>(undefined);
  const [lease, setLease] = useState<BrowserSurfaceHandleV1>();
  const leaseRef = useRef<BrowserSurfaceHandleV1 | undefined>(undefined);
  useLayoutEffect(() => {
    if (!workdir || !selectedTabID) { setLease(undefined); leaseRef.current = undefined; return; }
    const next = service.acquireSurface({ workdir, tabID: selectedTabID, threadID });
    leaseRef.current = next;
    setLease(next);
    return () => { leaseRef.current = undefined; next.dispose(); };
  }, [service, workdir, selectedTabID, threadID]);
  const updateLeasePresentation = useCallback((current: BrowserSurfaceHandleV1, update: (lease: BrowserSurfaceHandleV1) => void) => {
    if (current !== leaseRef.current) return;
    try { update(current); }
    catch (cause) {
      // Provider retirement disposes leases before React effect cleanup. Never
      // retry through a new lease; only this proven cancellation is expected.
      if (!isPluginLifecycleCancellation(cause)) throw cause;
    }
  }, []);
  const consumedRequestIDRef = useRef<number | undefined>(undefined);

  const applySurface = useCallback((snapshot: BrowserSurfaceSnapshot) => {
    setCurrentURL(snapshot.url);
    setPendingURL(undefined);
    setCanGoBack(snapshot.canGoBack);
    setCanGoForward(snapshot.canGoForward);
    if (snapshot.error) {
      setStatus("error");
      setErrorMessage(toastErrorMessage(snapshot.error, t("workspace.browser.cannotOpen")));
    } else {
      setStatus(snapshot.loading ? "loading" : "idle");
      setErrorMessage(undefined);
    }
    if (inputRef.current !== document.activeElement) {
      setDraftURL(hasPageURL(snapshot.url) ? snapshot.url : "");
    }
  }, [t]);

  useLayoutEffect(() => {
    // A failed or absent snapshot must not leave another tab's address,
    // navigation controls, or native page visible in this session.
    setCurrentURL("");
    setDraftURL("");
    setStatus("idle");
    setErrorMessage(undefined);
    setCanGoBack(false);
    setCanGoForward(false);
    setPendingURL(undefined);
  }, [workdir, selectedTabID]);

  useEffect(() => {
    if (!lease || lease !== leaseRef.current) return undefined;
    let cancelled = false;
    void lease.read().then((snapshot) => {
      if (cancelled) return;
      if (snapshot && snapshot.tabID === selectedTabID && snapshot.workdir === workdir) {
        applySurface(snapshot);
      } else if (requestedTabID) {
        setStatus("error");
        setErrorMessage(t("workspace.browser.previewUnavailable"));
      }
    }).catch((cause: unknown) => {
      if (cancelled) return;
      setStatus("error");
      setErrorMessage(toastErrorMessage(cause, t("workspace.browser.cannotOpen")));
    });
    return () => {
      cancelled = true;
    };
  }, [lease, applySurface, requestedTabID, selectedTabID, t, workdir]);

  useEffect(() => {
    if (!lease || lease !== leaseRef.current) return undefined;
    return lease.onSurface((snapshot) => {
      if (snapshot.workdir !== workdir || snapshot.tabID !== selectedTabIDRef.current) return;
      applySurface(snapshot);
    });
  }, [lease, applySurface, workdir]);

  useEffect(() => {
    if (!lease || lease !== leaseRef.current) return undefined;
    return lease.onTabAdopted((payload) => {
      if (!workdir || payload.workdir !== workdir) return;
      if (payload.openerTabID !== selectedTabIDRef.current) return;
      setAdoptedTab({ sourceKey, tabID: payload.tabID });
    });
  }, [lease, sourceKey, workdir]);

  useEffect(() => {
    if (!lease || lease !== leaseRef.current) return undefined;
    return lease.onUserInput((payload) => {
      if (!workdir || payload.workdir !== workdir) return;
      if (payload.tabID !== selectedTabIDRef.current) return;
      if (!activity || activity.state === "stopped" || payload.threadID !== activity.thread_id) return;
      void lease.takeOver(activity, payload.inputGeneration).catch(cause => {
        if (leaseRef.current !== lease) return;
        setStatus("error"); setErrorMessage(toastErrorMessage(cause, t("workspace.browser.cannotOpen")));
      });
    });
  }, [lease, activity, t, workdir]);

  const runCommand = useCallback(async (command: "navigate" | "back" | "forward" | "reload" | "stop", url?: string) => {
    const current = leaseRef.current;
    if (!current) return;
    try {
      if (activity && activity.thread_id === threadID && activity.state !== "stopped") {
        await current.takeOver(activity);
      }
      if (current !== leaseRef.current) return;
      const snapshot = await current.command({ command, url });
      if (current === leaseRef.current && snapshot) applySurface(snapshot);
    } catch (cause) {
      if (current !== leaseRef.current) return;
      setStatus("error"); setErrorMessage(toastErrorMessage(cause, t("workspace.browser.cannotOpen")));
    }
  }, [activity, applySurface, t, threadID]);

  const navigate = useCallback((rawInput: string) => {
    const target = resolveNavigationInput(rawInput);
    if (!hasPageURL(target)) return;
    setStatus("loading");
    setErrorMessage(undefined);
    setDraftURL(target);
    setPendingURL(target);
    void runCommand("navigate", target);
  }, [runCommand]);

  useEffect(() => {
    if (!visible || !requestedURL) return;
    if (consumedRequestIDRef.current === requestedURL.requestID) return;
    const currentKey = workspaceBrowserOpenTarget(currentURL)?.reuseKey;
    if (currentKey !== requestedURL.reuseKey) navigate(requestedURL.url);
    consumedRequestIDRef.current = requestedURL.requestID;
    service.consumeNavigation(requestedURL.requestID);
  }, [service, currentURL, navigate, requestedURL, visible]);

  const goBack = useCallback(() => {
    if (!canGoBack) return;
    void runCommand("back");
  }, [canGoBack, runCommand]);

  const goForward = useCallback(() => {
    if (!canGoForward) return;
    void runCommand("forward");
  }, [canGoForward, runCommand]);

  const reload = useCallback(() => {
    void runCommand(status === "loading" ? "stop" : "reload");
  }, [runCommand, status]);

  const showPage = hasPageURL(currentURL) || hasPageURL(pendingURL);
  const paintPage = visible && showPage && status !== "error";

  useEffect(() => {
    if (!lease || lease !== leaseRef.current) return undefined;
    const report: BrowserSurfaceHandleV1["reportBounds"] = (...args) => updateLeasePresentation(lease, current => current.reportBounds(...args));
    if (!paintPage) {
      report(null);
      return undefined;
    }
    let first = true;
    const stop = observeBrowserPanelBounds((rect) => {
      if (rect.width <= 0 || rect.height <= 0) return;
      report(rect, first);
      first = false;
    }, panelRef.current ?? undefined);
    return () => {
      stop();
      report(null);
    };
  }, [lease, paintPage, selectedTabID, workdir, updateLeasePresentation]);

  useEffect(() => {
    if (!lease || lease !== leaseRef.current || !paintPage) return undefined;
    updateLeasePresentation(lease, current => current.suppressOverlay(overlaySuppressed));
    return () => {
      updateLeasePresentation(lease, current => current.suppressOverlay(false));
    };
  }, [lease, overlaySuppressed, paintPage, selectedTabID, workdir, updateLeasePresentation]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    navigate(draftURL);
    inputRef.current?.blur();
  };

  const showChromePage = showPage && status !== "error";
  const isLoading = status === "loading";

  return (
    <div
      ref={panelRef}
      className="workspace-browser-panel"
      data-wuu-component="workspace-browser"
      data-wuu-state={status}
    >
      <div className="workspace-browser-toolbar" data-wuu-component="workspace-browser-toolbar">
        <div className="workspace-browser-nav-cluster">
          <button
            className="icon-button workspace-browser-nav"
            type="button"
            aria-label={t("workspace.browser.back")}
            title={t("workspace.browser.back")}
            disabled={!canGoBack}
            onClick={goBack}
          >
            <ArrowLeft className="icon" />
          </button>
          <button
            className="icon-button workspace-browser-nav"
            type="button"
            aria-label={t("workspace.browser.forward")}
            title={t("workspace.browser.forward")}
            disabled={!canGoForward}
            onClick={goForward}
          >
            <ArrowRight className="icon" />
          </button>
          <button
            className="icon-button workspace-browser-nav"
            type="button"
            aria-label={isLoading ? t("workspace.browser.stop") : t("workspace.browser.refresh")}
            title={isLoading ? t("workspace.browser.stop") : t("workspace.browser.refresh")}
            disabled={!showChromePage && !isLoading}
            onClick={reload}
          >
            {isLoading ? <X className="icon" /> : <RotateCw className="icon" />}
          </button>
        </div>
        <form
          className="workspace-browser-url-form"
          data-wuu-component="workspace-browser-address"
          role="search"
          onSubmit={handleSubmit}
        >
          <input
            ref={inputRef}
            className={`workspace-browser-url-input${status === "error" ? " has-error" : ""}`}
            type="text"
            inputMode="url"
            spellCheck={false}
            autoCorrect="off"
            autoCapitalize="off"
            placeholder={t("workspace.browser.addressPlaceholder")}
            value={draftURL}
            onChange={(event) => setDraftURL(event.target.value)}
            onFocus={(event) => event.currentTarget.select()}
            aria-label={t("workspace.browser.address")}
          />
          <button
            className="icon-button workspace-browser-open-external"
            type="button"
            aria-label={t("workspace.browser.openExternal")}
            title={t("workspace.browser.openExternal")}
            disabled={!showChromePage}
            onClick={() => {
              if (showChromePage) {
                const current = leaseRef.current;
                void service.openExternal(currentURL).catch(cause => {
                  if (leaseRef.current !== current) return;
                  showActionErrorToast(cause);
                });
              }
            }}
          >
            <ArrowUpRight className="icon-sm" />
          </button>
        </form>
        <div
          className="workspace-browser-loading-bar"
          data-active={isLoading ? "true" : "false"}
          aria-hidden="true"
        />
      </div>
      <div className="workspace-browser-frame" data-wuu-component="workspace-browser-content">
        <div className="workspace-browser-host" />
        {status === "error" && errorMessage ? (
          <div className="workspace-browser-error" role="alert">
            <strong>{t("workspace.browser.cannotOpen")}</strong>
            <span>{errorMessage}</span>
            <button
              className="workspace-browser-retry"
              type="button"
              onClick={() => navigate(pendingURL ?? currentURL)}
            >
              {t("workspace.browser.retry")}
            </button>
          </div>
        ) : null}
        {isLoading && !showChromePage ? (
          <div className="workspace-browser-status" role="status">
            {t("workspace.browser.preparing")}
          </div>
        ) : null}
      </div>
    </div>
  );
}
