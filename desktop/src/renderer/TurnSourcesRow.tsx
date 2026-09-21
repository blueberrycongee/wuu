import {
  useEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from "react";
import type { TurnSource } from "./ToolActivityHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import {
  openExternalURL,
  useWorkspaceBrowserOpen,
  workspaceBrowserClickModifiers,
  type WorkspaceBrowserOpenModifiers,
} from "./WorkspaceBrowserOpen";

/**
 * How many host icons we render inside the pill before collapsing the
 * rest into an "+N" overflow badge. Six keeps the pill readable while
 * staying narrow enough to leave room for the toggle label on the left
 * (`flex: 1 1 auto` on the toggle row would otherwise be squashed at
 * 20+ unique hosts). The remainder is exposed via the overflow badge
 * rather than clipped, so the user can always see exactly which
 * domains the turn consulted.
 */
const VISIBLE_SOURCE_LIMIT = 6;

/**
 * "来源 N" pill rendered beside an assistant turn's process header.
 * It stacks one favicon per unique host the turn consulted through
 * `web_search` or `web_fetch` and opens the chosen URL on click. One slot
 * per host (not per URL), only after at least
 * one web tool resolved, with a first-letter avatar as a fallback for
 * hosts whose favicon can't be fetched.
 *
 * The accessible name + native tooltip carry the full URL, not just the
 * host. The favicon lookup dedupes on host, but the host alone can still
 * cover several pages, so the tooltip shows the consulted URL.
 *
 * Single-source shortcut: when the turn only consulted one URL, the
 * whole pill becomes a `<button>` so hovering or clicking the "来源"
 * label anywhere opens the page. The icon stack is still the same
 * visual, just rendered as an inert avatar inside the button instead
 * of a nested click target.
 *
 * Multi-source overflow: when there are more than
 * `VISIBLE_SOURCE_LIMIT` unique hosts, the pill shows the first
 * `VISIBLE_SOURCE_LIMIT` icons plus a "+N" badge that opens a
 * dropdown listing the remaining URLs. Without this cap a long search
 * run across many distinct sites would overflow the process header row
 * and squeeze the "查看思考过程" toggle to zero width.
 *
 * The component never decides policy on its own. `sources` is fed by
 * `collectTurnSources`. Clicks open the workspace browser when the host
 * provides that path; modifier clicks and missing hosts still use
 * `window.wuu.openExternal`. The `onOpen` prop lets tests inject a spy.
 */
export function TurnSourcesRow({
  sources,
  onOpen,
}: {
  sources: TurnSource[];
  onOpen?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const openWorkspaceURL = useWorkspaceBrowserOpen((url, modifiers) => {
    if (onOpen) {
      if (modifiers) onOpen(url, modifiers);
      else onOpen(url);
      return;
    }
    openExternalURL(url);
  });
  if (sources.length === 0) return null;
  // "来源" alone reads better for a single hit. With more, the count
  // helps users decide whether to expand the pill in the future.
  const label = sources.length === 1
    ? t("sources.label")
    : t("sources.labelCount", { count: sources.length });

  // Single source → the entire pill is the click target. The label and
  // the icon stack both belong to the same button, so there is no dead
  // area where hovering shows the URL but clicking does nothing.
  if (sources.length === 1) {
    const source = sources[0];
    const tooltip = sourceTooltip(source);
    const handleClick = (event: ReactMouseEvent<HTMLButtonElement>): void => {
      event.preventDefault();
      openWorkspaceURL(source.url, workspaceBrowserClickModifiers(event));
    };
    return (
      <Tooltip content={tooltip}>
        <button
          type="button"
          className="turn-sources-pill turn-sources-pill-single"
          aria-label={t("sources.openNamed", { name: tooltip })}
          onClick={handleClick}
        >
          <span className="turn-source-icon-frame">
            <SourceAvatar source={source} />
          </span>
          <span className="turn-sources-label">{label}</span>
        </button>
      </Tooltip>
    );
  }

  // Multi-source → the pill is a passive group; each host keeps its own
  // button so users can pick which URL to open without ambiguity.
  // First VISIBLE_SOURCE_LIMIT icons render in-line; the rest are
  // collapsed into the overflow badge so the row can't grow without
  // bound as the agent fans out to more distinct domains.
  const visibleSources = sources.slice(0, VISIBLE_SOURCE_LIMIT);
  const overflowSources = sources.slice(VISIBLE_SOURCE_LIMIT);
  return (
    <div className="turn-sources-pill" role="group" aria-label={label}>
      <div className="turn-sources-icons">
        {visibleSources.map((source) => (
          <SourceIcon key={source.host} source={source} onOpen={openWorkspaceURL} />
        ))}
        {overflowSources.length > 0 ? (
          <OverflowBadge
            count={overflowSources.length}
            sources={overflowSources}
            onOpen={openWorkspaceURL}
          />
        ) : null}
      </div>
      <span className="turn-sources-label">{label}</span>
    </div>
  );
}

function sourceTooltip(source: TurnSource): string {
  // Show the full URL so users can verify which page on the host was
  // consulted. Title (when present) reads as the human-readable label
  // and the URL is the unambiguous link target. Host alone is not
  // enough — see the file header.
  return source.title ? `${source.title} — ${source.url}` : source.url;
}

function SourceIcon({
  source,
  onOpen,
}: {
  source: TurnSource;
  onOpen: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
}): JSX.Element {
  const { t } = useI18n();
  const tooltip = sourceTooltip(source);
  const handleClick = (event: ReactMouseEvent<HTMLButtonElement>): void => {
    event.preventDefault();
    onOpen(source.url, workspaceBrowserClickModifiers(event));
  };
  return (
    <Tooltip content={tooltip}>
      <button
        type="button"
        className="turn-source-icon"
        aria-label={t("sources.openNamed", { name: tooltip })}
        onClick={handleClick}
      >
        <SourceAvatar source={source} />
      </button>
    </Tooltip>
  );
}

function SourceAvatar({ source }: { source: TurnSource }): JSX.Element {
  const [loadState, setLoadState] = useState<"loading" | "loaded" | "failed">(
    "loading",
  );
  // Resolve a 32x32 PNG for any host with no API key. If the host has no
  // favicon, the request 404s and `onError` flips us into the
  // first-letter avatar fallback so the stack still reads as one.
  const faviconURL = `https://www.google.com/s2/favicons?domain=${encodeURIComponent(source.host)}&sz=32`;
  return (
    <span className="turn-source-avatar" data-load-state={loadState}>
      {loadState === "failed" ? (
        <span className="turn-source-fallback" aria-hidden>
          {source.host[0]?.toUpperCase() ?? "·"}
        </span>
      ) : (
        // `alt=""` because the surrounding button already carries the
        // accessible label for this source; a redundant alt would force
        // screen readers to read the favicon URL twice.
        <img
          src={faviconURL}
          alt=""
          loading="lazy"
          onLoad={() => setLoadState("loaded")}
          onError={() => setLoadState("failed")}
        />
      )}
    </span>
  );
}

function OverflowBadge({
  count,
  sources,
  onOpen,
}: {
  count: number;
  sources: TurnSource[];
  onOpen: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  // Anchors the document-level listeners used to dismiss the popover.
  // The popover itself renders inside this wrapper so a click on a menu
  // item still counts as "inside" and doesn't trigger the close path
  // before the URL handler runs.
  const rootRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: MouseEvent): void => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleKey = (event: KeyboardEvent): void => {
      if (event.key === "Escape") setOpen(false);
    };
    // mousedown fires before click, so dismissing on mousedown avoids
    // a frame where the badge's own click toggles `open` back to true
    // and then the document click closes it again.
    document.addEventListener("mousedown", handlePointerDown);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("mousedown", handlePointerDown);
      document.removeEventListener("keydown", handleKey);
    };
  }, [open]);

  const toggle = (event: ReactMouseEvent<HTMLButtonElement>): void => {
    event.preventDefault();
    event.stopPropagation();
    setOpen((current) => !current);
  };
  return (
    <span ref={rootRef} className="turn-source-overflow">
      <button
        type="button"
        className="turn-source-icon turn-source-overflow-badge"
        aria-label={t("sources.viewMore", { count })}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t("sources.moreTitle", { count })}
        onClick={toggle}
      >
        +{count}
      </button>
      {open ? (
        <OverflowList
          sources={sources}
          onOpen={onOpen}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </span>
  );
}

function OverflowList({
  sources,
  onOpen,
  onClose,
}: {
  sources: TurnSource[];
  onOpen: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <ul className="turn-source-overflow-list" role="menu">
      {sources.map((source) => {
        const tooltip = sourceTooltip(source);
        const handleClick = (
          event: ReactMouseEvent<HTMLButtonElement>,
        ): void => {
          event.preventDefault();
          const modifiers = workspaceBrowserClickModifiers(event);
          if (modifiers) onOpen(source.url, modifiers);
          else onOpen(source.url);
          onClose();
        };
        return (
          <li key={source.host} role="none">
            <Tooltip content={tooltip}>
              <button
                type="button"
                role="menuitem"
                className="turn-source-overflow-item"
                aria-label={t("sources.openNamed", { name: tooltip })}
                onClick={handleClick}
              >
                <span className="turn-source-overflow-item-title">
                  {source.title ?? source.url}
                </span>
                <span className="turn-source-overflow-item-host">
                  {source.host}
                </span>
              </button>
            </Tooltip>
          </li>
        );
      })}
    </ul>
  );
}


