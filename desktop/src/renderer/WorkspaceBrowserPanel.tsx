import {
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  Globe,
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
import type { ActivitySession, BrowserDockTarget, BrowserSurfaceSnapshot, RuntimeContext } from "../shared/protocol";
import { useI18n } from "./i18n";
import { browserTabIDForActivity, displayedBrowserTabID, observeBrowserPanelBounds } from "./BrowserVisibility";
import { openExternalURL, workspaceBrowserOpenTarget } from "./WorkspaceBrowserOpen";
import {
  useWorkspaceBrowserNavigationConsumer,
  type WorkspaceBrowserNavigationRequest,
} from "./WorkspaceBrowserNavigation";
import { WorkspacePanelEmpty } from "./WorkspaceFiles";

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
  if (/^localhost(:\d+)?(\/.*)?$/i.test(value)) {
    return true;
  }
  // IPv4 loopback / unspecified address with optional port
  if (/^(?:127\.0\.0\.1|0\.0\.0\.0)(?::\d+)?(?:\/.*)?$/.test(value)) {
    return true;
  }
  // bare host with optional port and path/query
  if (/^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+(:\d+)?(\/.*)?$/.test(value)) {
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
      : `https://${value}`;
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
export function WorkspaceBrowserPanel({
  visible = true,
  threadID,
  activeContext,
  activity,
  dockTarget,
  requestedURL,
  overlaySuppressed = false,
  onUserInteraction,
}: {
  visible?: boolean;
  threadID?: string;
  activeContext?: RuntimeContext;
  activity?: ActivitySession;
  dockTarget?: BrowserDockTarget;
  requestedURL?: WorkspaceBrowserNavigationRequest;
  overlaySuppressed?: boolean;
  onUserInteraction?: () => void | Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const inputRef = useRef<HTMLInputElement | null>(null);
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
  const showingAgentTab = Boolean(agentTabID && selectedTabID === agentTabID);

  const [currentURL, setCurrentURL] = useState("");
  const [draftURL, setDraftURL] = useState("");
  const [status, setStatus] = useState<BrowserStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | undefined>(undefined);
  const [canGoBack, setCanGoBack] = useState(false);
  const [canGoForward, setCanGoForward] = useState(false);
  const [pendingURL, setPendingURL] = useState<string | undefined>(undefined);
  const consumeNavigation = useWorkspaceBrowserNavigationConsumer();
  const consumedRequestIDRef = useRef<number | undefined>(undefined);

  const applySurface = useCallback((snapshot: BrowserSurfaceSnapshot) => {
    setCurrentURL(snapshot.url);
    setPendingURL(undefined);
    setCanGoBack(snapshot.canGoBack);
    setCanGoForward(snapshot.canGoForward);
    if (snapshot.error) {
      setStatus("error");
      setErrorMessage(snapshot.error);
    } else {
      setStatus(snapshot.loading ? "loading" : "idle");
      setErrorMessage(undefined);
    }
    if (inputRef.current !== document.activeElement) {
      setDraftURL(hasPageURL(snapshot.url) ? snapshot.url : "");
    }
  }, []);

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
    if (!workdir || !selectedTabID) return undefined;
    const read = window.wuu?.browserSurface;
    if (typeof read !== "function") return undefined;
    let cancelled = false;
    void read(workdir, selectedTabID).then((snapshot) => {
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
      setErrorMessage(cause instanceof Error ? cause.message : String(cause));
    });
    return () => {
      cancelled = true;
    };
  }, [applySurface, requestedTabID, selectedTabID, t, workdir]);

  useEffect(() => {
    const subscribe = window.wuu?.onBrowserSurface;
    if (typeof subscribe !== "function") return undefined;
    return subscribe((snapshot) => {
      if (snapshot.workdir !== workdir || snapshot.tabID !== selectedTabIDRef.current) return;
      applySurface(snapshot);
    });
  }, [applySurface, workdir]);

  useEffect(() => {
    const subscribe = window.wuu?.onBrowserTabAdopted;
    if (typeof subscribe !== "function") return undefined;
    return subscribe((payload) => {
      if (!workdir || payload.workdir !== workdir) return;
      if (payload.openerTabID !== selectedTabIDRef.current) return;
      setAdoptedTab({ sourceKey, tabID: payload.tabID });
    });
  }, [sourceKey, workdir]);

  useEffect(() => {
    const subscribe = window.wuu?.onBrowserUserInput;
    if (typeof subscribe !== "function") return undefined;
    return subscribe((payload) => {
      if (!workdir || payload.workdir !== workdir) return;
      if (payload.tabID !== selectedTabIDRef.current) return;
      if (!showingAgentTab || !activity || activity.state === "stopped") return;
      void onUserInteraction?.();
    });
  }, [activity, onUserInteraction, showingAgentTab, workdir]);

  const runCommand = useCallback(async (command: "navigate" | "back" | "forward" | "reload" | "stop", url?: string) => {
    const tabID = selectedTabIDRef.current;
    const send = window.wuu?.browserCommand;
    if (!workdir || !tabID || typeof send !== "function") return;
    if (showingAgentTab && activity && activity.state !== "stopped") {
      await onUserInteraction?.();
    }
    if (tabID !== selectedTabIDRef.current) return;
    const snapshot = await send({ workdir, tabID, command, url });
    if (snapshot && snapshot.tabID === selectedTabIDRef.current) applySurface(snapshot);
  }, [activity, applySurface, onUserInteraction, showingAgentTab, workdir]);

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
    consumeNavigation(requestedURL.requestID);
  }, [consumeNavigation, currentURL, navigate, requestedURL, visible]);

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
    const report = window.wuu?.reportBrowserBounds;
    if (typeof report !== "function" || !workdir || !selectedTabID) return undefined;
    if (!paintPage) {
      report(workdir, selectedTabID, null);
      return undefined;
    }
    let first = true;
    const stop = observeBrowserPanelBounds((rect) => {
      if (rect.width <= 0 || rect.height <= 0) return;
      report(workdir, selectedTabID, rect, first);
      first = false;
    });
    return () => {
      stop();
      report(workdir, selectedTabID, null);
    };
  }, [paintPage, selectedTabID, workdir]);

  useEffect(() => {
    const suppress = window.wuu?.suppressBrowserOverlay;
    if (typeof suppress !== "function" || !workdir || !selectedTabID || !paintPage) return undefined;
    suppress(workdir, selectedTabID, overlaySuppressed);
    return () => {
      suppress(workdir, selectedTabID, false);
    };
  }, [overlaySuppressed, paintPage, selectedTabID, workdir]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    navigate(draftURL);
    inputRef.current?.blur();
  };

  const showChromePage = showPage && status !== "error";
  const isLoading = status === "loading";

  return (
    <div
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
                openExternalURL(currentURL);
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
        {!showChromePage ? (
          <WorkspacePanelEmpty
            className="workspace-browser-home"
            title={t("workspace.browser.startBrowsing")}
            hint={t("workspace.browser.startBrowsingHint")}
            icon={<Globe size={28} strokeWidth={1.6} />}
          />
        ) : null}
        {status === "error" && errorMessage ? (
          <div className="workspace-browser-error" role="alert">
            <strong>{t("workspace.browser.cannotOpen")}</strong>
            <span>{errorMessage}</span>
            <button
              className="workspace-browser-retry"
              type="button"
              onClick={() => navigate(currentURL)}
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
