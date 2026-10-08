import { useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import type { TurnSource } from "./ToolActivityHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { useConversationRenderActive, useConversationRevealSnap } from "./ConversationRenderActivity";
import { useReducedMotion } from "./motion";
import { openExternalURL, useWorkspaceBrowserOpen, workspaceBrowserClickModifiers, type WorkspaceBrowserOpenModifiers } from "./WorkspaceBrowserOpen";
import "./TurnSourcesRow.css";

type SourceProps = {
  sources: TurnSource[];
  onOpen?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
};

/** Source links and a count control for the owning process disclosure. */
export function TurnSourcesRow({ sources, running = false, onOpen, inline = false, expanded = false, onExpandedChange, detailsID }: SourceProps & {
  running?: boolean;
  inline?: boolean;
  expanded?: boolean;
  onExpandedChange?: (open: boolean, trigger: HTMLButtonElement) => void;
  detailsID?: string;
}): JSX.Element | null {
  const { t } = useI18n();
  const renderActive = useConversationRenderActive();
  const revealingConversation = useConversationRevealSnap();
  const reducedMotion = useReducedMotion();
  const rowRef = useRef<HTMLDivElement>(null);
  const [visibleLimit, setVisibleLimit] = useState(6);
  useLayoutEffect(() => {
    if (!inline) { setVisibleLimit(6); return; }
    const owner = rowRef.current?.closest<HTMLElement>(".process-surface-inline-controls");
    if (!owner) return;
    const measure = () => {
      if (owner.clientWidth > 0) setVisibleLimit(Math.max(1, Math.min(6, Math.floor((owner.clientWidth * 0.48 - 90) / 34))));
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
    observer?.observe(owner);
    return () => observer?.disconnect();
  }, [inline, sources.length > 0]);
  const seenURLs = useRef(new Set(sources.map(source => source.url)));
  const mayAnimate = running && renderActive && !revealingConversation && !reducedMotion;
  const newURLs = sources.filter(source => !seenURLs.current.has(source.url));
  useLayoutEffect(() => { for (const source of sources) seenURLs.current.add(source.url); }, [sources]);
  const openURL = useWorkspaceBrowserOpen((url, modifiers) => {
    if (onOpen) { if (modifiers) onOpen(url, modifiers); else onOpen(url); }
    else openExternalURL(url);
  });
  if (sources.length === 0) return null;
  const label = sources.length === 1 ? t("sources.label") : t("sources.labelCount", { count: sources.length });
  return <div ref={rowRef} className={`turn-web-research${inline ? " is-inline" : ""}`} role="group" aria-label={label}>
    <div className="web-research-sources">
      {sources.slice(0, visibleLimit).map(source => <SourceLink key={source.url} source={source} onOpen={openURL}
        arrive={mayAnimate && !seenURLs.current.has(source.url)} motionAllowed={mayAnimate}
        index={newURLs.findIndex(item => item.url === source.url)} />)}
      <button type="button" className="web-research-more" aria-expanded={expanded} aria-controls={detailsID}
        aria-label={label}
        onClick={event => onExpandedChange?.(!expanded, event.currentTarget)}>{label}</button>
    </div>
  </div>;
}

/** The whole group's sources, including those shown as collapsed icons. */
export function TurnSourceChips({ sources, onOpen }: SourceProps): JSX.Element | null {
  const { t } = useI18n();
  const [showAll, setShowAll] = useState(false);
  const openURL = useWorkspaceBrowserOpen((url, modifiers) => {
    if (onOpen) { if (modifiers) onOpen(url, modifiers); else onOpen(url); }
    else openExternalURL(url);
  });
  if (sources.length === 0) return null;
  const label = sources.length === 1 ? t("sources.label") : t("sources.labelCount", { count: sources.length });
  return <div className="web-research-expanded" role="group" aria-label={label} onKeyDownCapture={event => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
    const links = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button")];
    const index = links.indexOf(event.target as HTMLButtonElement);
    if (index < 0) return;
    event.preventDefault();
    event.stopPropagation();
    const next = event.key === "Home" ? 0 : event.key === "End" ? links.length - 1
      : (index + (["ArrowRight", "ArrowDown"].includes(event.key) ? 1 : -1) + links.length) % links.length;
    links[next]?.focus();
  }}>
    <div className="web-research-source-list">
      {(showAll ? sources : sources.slice(0, 8)).map(source => <SourceLink key={source.url} source={source}
        onOpen={openURL} arrive={false} motionAllowed={false} index={0} detail />)}
      {sources.length > 8 ? <button type="button" className="web-research-reveal" onClick={() => setShowAll(value => !value)}>
        {showAll ? t("sources.showLess") : t("sources.viewMore", { count: sources.length - 8 })}
      </button> : null}
    </div>
  </div>;
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
      <button type="button"
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
