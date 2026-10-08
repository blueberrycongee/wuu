import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import type { TurnSource } from "./ToolActivityHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import {
  FloatingMenuPortal,
  handleFloatingMenuKeyDown,
  menuOpeningKey,
  useFloatingMenuFocus,
} from "./ComposerFloatingMenu";
import { useConversationRenderActive, useConversationRevealSnap } from "./ConversationRenderActivity";
import { useReducedMotion } from "./motion";
import { ChevronDown } from "./WuuIcons";
import {
  openExternalURL,
  useWorkspaceBrowserOpen,
  workspaceBrowserClickModifiers,
  type WorkspaceBrowserOpenModifiers,
} from "./WorkspaceBrowserOpen";
import "./TurnSourcesRow.css";

const VISIBLE_SOURCE_LIMIT = 6;

/** Compact source links belonging to one tool-call group. */
export function TurnSourcesRow({ sources, running = false, onOpen, inline = false }: {
  sources: TurnSource[];
  running?: boolean;
  inline?: boolean;
  onOpen?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const renderActive = useConversationRenderActive();
  const revealingConversation = useConversationRevealSnap();
  const reducedMotion = useReducedMotion();
  const [expanded, setExpanded] = useState(false);
  const rowRef = useRef<HTMLDivElement>(null);
  const [visibleLimit, setVisibleLimit] = useState(VISIBLE_SOURCE_LIMIT);
  useLayoutEffect(() => {
    if (!inline) { setVisibleLimit(VISIBLE_SOURCE_LIMIT); return; }
    const owner = rowRef.current?.closest<HTMLElement>(".process-surface-inline-controls");
    if (!owner) return;
    const measure = () => {
      if (owner.clientWidth <= 0) return;
      const budget = owner.clientWidth * 0.48;
      setVisibleLimit(Math.max(1, Math.min(VISIBLE_SOURCE_LIMIT, Math.floor((budget - 54) / 34))));
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(owner);
    return () => observer?.disconnect();
  }, [inline, sources.length > 0]);
  const moreRef = useRef<HTMLButtonElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const [focusFromEnd, setFocusFromEnd] = useState(false);
  const [showAll, setShowAll] = useState(false);
  const overflowSources = sources.slice(visibleLimit);
  const displayedOverflow = showAll ? overflowSources : overflowSources.slice(0, 8);
  const hasOverflow = sources.length > visibleLimit;
  const open = expanded && renderActive && hasOverflow;
  useFloatingMenuFocus(popoverRef, "", open, focusFromEnd);
  useEffect(() => { if (!open) setShowAll(false); }, [open]);
  useEffect(() => {
    if (!renderActive || !hasOverflow) setExpanded(false);
  }, [renderActive, hasOverflow]);
  useEffect(() => {
    if (!open) return;
    const dismissOutside = (event: Event) => {
      const target = event.target;
      if (target instanceof Node && !moreRef.current?.contains(target) && !popoverRef.current?.contains(target)) {
        setExpanded(false);
      }
    };
    const dismissEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      moreRef.current?.focus({ preventScroll: true });
      setExpanded(false);
    };
    document.addEventListener("pointerdown", dismissOutside, true);
    document.addEventListener("focusin", dismissOutside);
    document.addEventListener("keydown", dismissEscape);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside, true);
      document.removeEventListener("focusin", dismissOutside);
      document.removeEventListener("keydown", dismissEscape);
    };
  }, [open]);
  // Receipt belongs to the group, not to an icon's mount lifetime. In
  // particular, opening overflow or returning to a cached pane is not news.
  const seenURLs = useRef(new Set(sources.map(source => source.url)));
  const mayAnimate = running && renderActive && !revealingConversation && !reducedMotion;
  const newURLs = sources.filter(source => !seenURLs.current.has(source.url));
  const reveal = mayAnimate && seenURLs.current.size === 0 && newURLs.length > 0;
  useLayoutEffect(() => {
    for (const source of sources) seenURLs.current.add(source.url);
  }, [sources]);
  const listID = useId();
  const openURL = useWorkspaceBrowserOpen((url, modifiers) => {
    if (onOpen) {
      if (modifiers) onOpen(url, modifiers);
      else onOpen(url);
    } else openExternalURL(url);
  });
  if (sources.length === 0) return null;
  const label = sources.length === 1 ? t("sources.label") : t("sources.labelCount", { count: sources.length });

  return (
    <div ref={rowRef} className={`turn-web-research${inline ? " is-inline" : ""}${reveal ? " is-revealing" : ""}`} role="group" aria-label={label}>
      <div className="web-research-sources">
        {sources.slice(0, visibleLimit).map(source => (
          <SourceLink key={source.url} source={source} onOpen={openURL}
            arrive={mayAnimate && !seenURLs.current.has(source.url)}
            motionAllowed={mayAnimate} index={newURLs.findIndex(item => item.url === source.url)} />
        ))}
        {hasOverflow ? (
          <button type="button" ref={moreRef} className="web-research-more"
            aria-expanded={open} aria-controls={open ? listID : undefined} aria-haspopup="menu"
            aria-label={open ? t("sources.showLess") : t("sources.viewMore", { count: sources.length - visibleLimit })}
            onClick={() => {
              setFocusFromEnd(false);
              if (open) moreRef.current?.focus({ preventScroll: true });
              setExpanded(value => !value);
            }}
            onKeyDown={event => {
              const entry = menuOpeningKey(event);
              if (!entry) return;
              setFocusFromEnd(entry === "end");
              setExpanded(true);
              if (open) {
                const items = popoverRef.current?.querySelectorAll<HTMLButtonElement>("button");
                (entry === "end" ? items?.[items.length - 1] : items?.[0])?.focus();
              }
            }}
          >
            <span>+{sources.length - visibleLimit}</span>
            <ChevronDown aria-hidden="true" />
          </button>
        ) : null}
        <span className="web-research-count">{label}</span>
      </div>
      {open ? (
        <FloatingMenuPortal anchorRef={moreRef} owner="turn-sources" placement="below" align="left" width={360} offset={6} flip>
          <div ref={popoverRef} id={listID} className="web-research-popover" role="menu" aria-label={label}
            onFocus={event => {
              // Initial menu focus avoids scrolling ancestors. Reveal its row
              // in this scrollport only, including ArrowUp entry at the end.
              const panel = event.currentTarget;
              const target = event.target.getBoundingClientRect();
              const style = getComputedStyle(panel);
              const innerTop = panel.getBoundingClientRect().top + panel.clientTop;
              const top = innerTop + (Number.parseFloat(style.paddingTop) || 0);
              const bottom = innerTop + panel.clientHeight - (Number.parseFloat(style.paddingBottom) || 0);
              if (target.top < top) panel.scrollTop += target.top - top;
              else if (target.bottom > bottom) panel.scrollTop += target.bottom - bottom;
            }}
            onKeyDown={event => {
              if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
                const links = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button")];
                const index = links.indexOf(event.target as HTMLButtonElement);
                if (index >= 0) {
                  event.preventDefault();
                  event.stopPropagation();
                  links[(index + (event.key === "ArrowRight" ? 1 : -1) + links.length) % links.length]?.focus();
                  return;
                }
              }
              handleFloatingMenuKeyDown(event, () => setExpanded(false), moreRef.current);
            }}>
            <div className="web-research-source-list">
              {displayedOverflow.map(source => (
                <SourceLink key={source.url} source={source} onOpen={openURL}
                  arrive={mayAnimate && !seenURLs.current.has(source.url)}
                  motionAllowed={mayAnimate} index={newURLs.findIndex(item => item.url === source.url)} detail />
              ))}
              {overflowSources.length > 8 ? <button type="button" role="menuitem" tabIndex={-1}
                className="web-research-reveal" onClick={() => setShowAll(value => !value)}>
                {showAll ? t("sources.showLess") : t("sources.viewMore", { count: overflowSources.length - 8 })}
              </button> : null}
            </div>
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function SourceLink({ source, onOpen, arrive, motionAllowed, index, detail = false }: {
  source: TurnSource;
  onOpen: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
  arrive: boolean;
  motionAllowed: boolean;
  index: number;
  detail?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const [animate, setAnimate] = useState(arrive);
  // A streaming parent may rerender after the receipt ledger advances. Keep
  // this entrance's delay, rather than moving its clock to the first slot.
  const [arrivalOrder] = useState(index);
  useLayoutEffect(() => {
    if (!motionAllowed) setAnimate(false);
  }, [motionAllowed]);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const tooltip = source.title ? `${source.title} — ${source.url}` : source.url;
  return (
    <Tooltip content={tooltip} propagateEscape>
      <button type="button" role={detail ? "menuitem" : undefined} tabIndex={detail ? -1 : undefined}
        className={`web-source-link ${detail ? "web-source-detail" : "web-source-circle"}`}
        style={{ "--source-order": Math.max(0, Math.min(arrivalOrder, 3)) } as CSSProperties}
        aria-label={t("sources.openNamed", { name: tooltip })}
        onClick={event => {
          event.preventDefault();
          onOpen(source.url, workspaceBrowserClickModifiers(event));
        }}
      >
        <span className={`web-source-avatar${animate && motionAllowed ? " is-arriving" : ""}`} aria-hidden="true"
          onAnimationEnd={() => setAnimate(false)}>
          <span className="web-source-fallback" data-loaded={loaded}>{source.host[0]?.toUpperCase() ?? "·"}</span>
          {!failed ? <img
            src={`https://www.google.com/s2/favicons?domain=${encodeURIComponent(source.host)}&sz=32`}
            alt="" loading="lazy" data-loaded={loaded}
            onLoad={() => setLoaded(true)} onError={() => { setFailed(true); setLoaded(false); }}
          /> : null}
        </span>
        {detail ? <span className="web-source-host">{source.host}</span> : null}
      </button>
    </Tooltip>
  );
}
