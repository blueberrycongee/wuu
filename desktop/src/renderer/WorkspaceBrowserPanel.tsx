import {
  ArrowLeft,
  ArrowRight,
  Bot,
  Globe,
  Hand,
  RotateCw,
  Search,
  ShieldAlert,
  Square,
  X
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent
} from "react";
import type { ActivitySession, RuntimeContext } from "../shared/protocol";
import { translateCurrent, useI18n } from "./i18n";
import { WorkspacePanelEmpty } from "./WorkspaceFiles";

const HOME_PAGE_URL = "wuu://new-tab";
const SEARCH_FALLBACK_URL = "https://www.google.com/search?igu=1&q=";

type WebviewElement = Electron.WebviewTag;

type BrowserStatus = "idle" | "loading" | "error";

function extractHostname(value: string): string | undefined {
  const match = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\/([^/?#]+)/.exec(value);
  return match?.[1];
}

function looksLikeUrl(input: string): boolean {
  const value = input.trim();
  if (value.length === 0) {
    return false;
  }
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(value)) {
    return true;
  }
  // localhost with port
  if (/^localhost(:\d+)?(\/.*)?$/i.test(value)) {
    return true;
  }
  // 127.0.0.1 / 0.0.0.0 with optional port
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

function isInternalUrl(url: string): boolean {
  return url === HOME_PAGE_URL;
}

// Electron's <webview> element rejects most method calls with a synchronous
// "WebView must be attached and dom-ready" error when invoked before its
// initial dom-ready event has fired. Wrap any call we make through the
// element so a misordered lifecycle can't leak an unhandled exception.
function safeWebview<T>(fn: () => T): T | undefined {
  try {
    return fn();
  } catch {
    return undefined;
  }
}

export function WorkspaceBrowserPanel({
  open = true,
  activeContext,
  activity,
  onActivityTakeover,
  onActivityRelease,
  onActivityStop,
}: {
  open?: boolean;
  activeContext?: RuntimeContext;
  activity?: ActivitySession;
  onActivityTakeover?: () => void;
  onActivityRelease?: () => void;
  onActivityStop?: () => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const webviewRef = useRef<WebviewElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const isWebviewReadyRef = useRef(false);

  const [currentURL, setCurrentURL] = useState<string>(HOME_PAGE_URL);
  const [pageTitle, setPageTitle] = useState<string>(() => t("workspace.browser.newTab"));
  const [draftURL, setDraftURL] = useState<string>("");
  const [status, setStatus] = useState<BrowserStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | undefined>(undefined);
  const [canGoBack, setCanGoBack] = useState(false);
  const [canGoForward, setCanGoForward] = useState(false);
  const [hostHint, setHostHint] = useState<string | undefined>(undefined);
  const [isWebviewReady, setIsWebviewReady] = useState(false);

  const updateCurrentURL = useCallback((url: string) => {
    setCurrentURL(url);
    setDraftURL(url === HOME_PAGE_URL ? "" : url);
    setHostHint(extractHostname(url));
  }, []);

  // Mount the <webview> element once. The element is a custom Electron tag and
  // is not part of the standard JSX intrinsic set, so we create it imperatively
  // and append it to a dedicated container.
  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return undefined;
    }

    const webview = document.createElement("webview") as WebviewElement;
    webview.setAttribute("partition", "persist:wuu-browser");
    webview.setAttribute("allowpopups", "");
    webview.style.flex = "1 1 auto";
    webview.style.width = "100%";
    webview.style.height = "100%";
    webview.style.border = "0";
    webview.style.display = "flex";

    container.appendChild(webview);
    webviewRef.current = webview;

    return () => {
      if (webview.parentNode === container) {
        container.removeChild(webview);
      }
      webviewRef.current = null;
    };
  }, []);

  // Wire up the <webview> event listeners exactly once. State updates use the
  // setter functions directly so the effect does not need to re-run on every
  // navigation.
  useEffect(() => {
    const webview = webviewRef.current;
    if (!webview) {
      return undefined;
    }

    const syncNavigationState = (): void => {
      if (!isWebviewReadyRef.current) {
        return;
      }
      setCanGoBack(Boolean(safeWebview(() => webview.canGoBack())));
      setCanGoForward(Boolean(safeWebview(() => webview.canGoForward())));
    };

    const handleDomReady = (): void => {
      const url = safeWebview(() => webview.getURL());
      if (typeof url !== "string") {
        return;
      }
      isWebviewReadyRef.current = true;
      updateCurrentURL(url);
      const title = safeWebview(() => webview.getTitle());
      if (typeof title === "string" && title.length > 0) {
        setPageTitle(title);
      }
      setStatus("idle");
      setErrorMessage(undefined);
      setIsWebviewReady(true);
      syncNavigationState();
    };

    const handleDidStartLoading = (): void => {
      setStatus("loading");
      setErrorMessage(undefined);
    };

    const handleDidStopLoading = (): void => {
      setStatus("idle");
      syncNavigationState();
    };

    const handleDidNavigate = (event: { url: string }): void => {
      updateCurrentURL(event.url);
    };

    const handleDidNavigateInPage = (event: { url: string }): void => {
      updateCurrentURL(event.url);
    };

    const handlePageTitleUpdated = (event: { title: string }): void => {
      if (event.title) {
        setPageTitle(event.title);
      }
    };

    const handleDidFailLoad = (event: {
      errorCode?: number;
      errorDescription?: string;
      validatedURL?: string;
    }): void => {
      // -3 is "ERR_ABORTED" which fires on programmatic reloads, not a real error.
      if (event.errorCode === -3) {
        return;
      }
      const target = event.validatedURL ?? "";
      setHostHint(extractHostname(target));
      setStatus("error");
      setErrorMessage(
        event.errorDescription ||
          translateCurrent("workspace.browser.loadFailed", {
            code: event.errorCode ?? translateCurrent("workspace.browser.unknownError"),
            target,
          })
      );
    };

    webview.addEventListener("dom-ready", handleDomReady);
    webview.addEventListener("did-start-loading", handleDidStartLoading);
    webview.addEventListener("did-stop-loading", handleDidStopLoading);
    webview.addEventListener("did-navigate", handleDidNavigate);
    webview.addEventListener("did-navigate-in-page", handleDidNavigateInPage);
    webview.addEventListener("page-title-updated", handlePageTitleUpdated);
    webview.addEventListener("did-fail-load", handleDidFailLoad);

    return () => {
      webview.removeEventListener("dom-ready", handleDomReady);
      webview.removeEventListener("did-start-loading", handleDidStartLoading);
      webview.removeEventListener("did-stop-loading", handleDidStopLoading);
      webview.removeEventListener("did-navigate", handleDidNavigate);
      webview.removeEventListener("did-navigate-in-page", handleDidNavigateInPage);
      webview.removeEventListener("page-title-updated", handlePageTitleUpdated);
      webview.removeEventListener("did-fail-load", handleDidFailLoad);
    };
  }, [updateCurrentURL]);

  const navigate = useCallback((rawInput: string) => {
    const target = resolveNavigationInput(rawInput);
    const webview = webviewRef.current;
    if (!webview) {
      return;
    }
    if (target === HOME_PAGE_URL) {
      setCurrentURL(HOME_PAGE_URL);
      setDraftURL("");
      setPageTitle(translateCurrent("workspace.browser.newTab"));
      setHostHint(undefined);
      setStatus("idle");
      setErrorMessage(undefined);
      setCanGoBack(false);
      setCanGoForward(false);
      // Loading a blank page clears the visible page in the same webview.
      safeWebview(() => webview.loadURL("about:blank"));
      return;
    }
    setStatus("loading");
    setErrorMessage(undefined);
    setDraftURL(target);
    safeWebview(() => webview.loadURL(target));
  }, []);

  const goBack = useCallback(() => {
    const webview = webviewRef.current;
    if (!webview || !canGoBack) {
      return;
    }
    safeWebview(() => webview.goBack());
  }, [canGoBack]);

  const goForward = useCallback(() => {
    const webview = webviewRef.current;
    if (!webview || !canGoForward) {
      return;
    }
    safeWebview(() => webview.goForward());
  }, [canGoForward]);

  const reload = useCallback(() => {
    const webview = webviewRef.current;
    if (!webview) {
      return;
    }
    if (status === "loading") {
      safeWebview(() => webview.stop());
    } else {
      safeWebview(() => webview.reload());
    }
  }, [status]);

  useEffect(() => {
    if (open) {
      return;
    }
    const webview = webviewRef.current;
    if (webview) {
      safeWebview(() => webview.stop());
      safeWebview(() => webview.loadURL("about:blank"));
    }
    isWebviewReadyRef.current = false;
    setIsWebviewReady(false);
    setCurrentURL(HOME_PAGE_URL);
    setDraftURL("");
    setPageTitle(translateCurrent("workspace.browser.newTab"));
    setHostHint(undefined);
    setStatus("idle");
    setErrorMessage(undefined);
    setCanGoBack(false);
    setCanGoForward(false);
  }, [open]);

  useEffect(() => {
    if (currentURL === HOME_PAGE_URL) {
      setPageTitle(t("workspace.browser.newTab"));
    }
  }, [currentURL, locale]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    navigate(draftURL);
    inputRef.current?.blur();
  };

  const showWebview = !isInternalUrl(currentURL);
  const isLoading = status === "loading";

  return (
    <div
      className="workspace-browser-panel"
      data-wuu-component="workspace-browser"
      data-wuu-state={status}
    >
      <div className="workspace-browser-toolbar" data-wuu-component="workspace-browser-toolbar">
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
          onClick={reload}
        >
          {isLoading ? <X className="icon" /> : <RotateCw className="icon" />}
        </button>
        <form
          className="workspace-browser-url-form"
          data-wuu-component="workspace-browser-address"
          role="search"
          onSubmit={handleSubmit}
        >
          <span className="workspace-browser-url-icon" aria-hidden="true">
            {status === "error" ? <ShieldAlert className="icon-sm" /> : <Globe className="icon-sm" />}
          </span>
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
        </form>
        <button
          className="icon-button workspace-browser-nav"
          type="button"
          aria-label={t("workspace.browser.home")}
          title={t("workspace.browser.newTab")}
          onClick={() => navigate(HOME_PAGE_URL)}
        >
          <Search className="icon" />
        </button>
      </div>
      <div className="workspace-browser-frame" data-wuu-component="workspace-browser-content">
        <div
          ref={containerRef}
          className="workspace-browser-host"
          hidden={!showWebview}
        />
        {!showWebview ? (
          <WorkspacePanelEmpty
            className="workspace-browser-home"
            title={t("workspace.browser.newTab")}
            hint={
              activeContext?.cwd
                ? t("workspace.browser.homeWorkspaceDescription")
                : t("workspace.browser.homeDescription")
            }
            icon={<Globe size={24} />}
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
        {!isWebviewReady && showWebview ? (
          <div className="workspace-browser-status" role="status">
            {t("workspace.browser.preparing")}
          </div>
        ) : null}
      </div>
      <div
        className="workspace-browser-statusbar"
        data-wuu-component="workspace-browser-statusbar"
        aria-live="polite"
      >
        {showWebview && hostHint ? (
          <span className="workspace-browser-host-hint">{hostHint}</span>
        ) : null}
        {showWebview && pageTitle ? (
          <span className="workspace-browser-title-hint">{pageTitle}</span>
        ) : null}
        {activity && activity.state !== "stopped" ? (
          <span className="workspace-browser-activity">
            <span className={`workspace-browser-activity-state ${activity.controller}`}>
              {browserActivityLabel(activity)}
            </span>
            {activity.controller === "user" ? (
              <button
                className="icon-button workspace-browser-activity-button"
                type="button"
                aria-label={t("workspace.browser.releaseToAgent")}
                title={t("workspace.browser.releaseToAgentShort")}
                onClick={onActivityRelease}
              >
                <Bot className="icon-sm" />
              </button>
            ) : (
              <button
                className="icon-button workspace-browser-activity-button"
                type="button"
                aria-label={t("workspace.browser.takeOver")}
                title={t("workspace.browser.takeOver")}
                onClick={onActivityTakeover}
              >
                <Hand className="icon-sm" />
              </button>
            )}
            <button
              className="icon-button workspace-browser-activity-button"
              type="button"
              aria-label={t("workspace.browser.stopActivity")}
              title={t("workspace.browser.stopActivityShort")}
              onClick={onActivityStop}
            >
              <Square className="icon-sm" />
            </button>
          </span>
        ) : null}
        {isLoading ? <span className="workspace-browser-loading-dot" aria-hidden="true" /> : null}
      </div>
    </div>
  );
}

function browserActivityLabel(activity: ActivitySession): string {
  if (activity.state === "waiting_confirmation") {
    return translateCurrent("workspace.browser.activityWaitingConfirmation");
  }
  if (activity.state === "error") {
    return translateCurrent("workspace.browser.activityError");
  }
  if (activity.controller === "user") {
    return translateCurrent("workspace.browser.activityUserControl");
  }
  if (activity.controller === "agent") {
    return translateCurrent("workspace.browser.activityAgentControl");
  }
  return translateCurrent("workspace.browser.activityUnassigned");
}
