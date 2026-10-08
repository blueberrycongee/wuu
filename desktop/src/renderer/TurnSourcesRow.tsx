import { useId, useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import type { TurnSource } from "./ToolActivityHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { CollapsibleDetails } from "./CollapsibleMotion";
import { useConversationRenderActive, useConversationRevealSnap } from "./ConversationRenderActivity";
import { useReducedMotion } from "./motion";
import { ArrowUpRight, ChevronDown } from "./WuuIcons";
import {
  openExternalURL,
  useWorkspaceBrowserOpen,
  workspaceBrowserClickModifiers,
  type WorkspaceBrowserOpenModifiers,
} from "./WorkspaceBrowserOpen";
import "./TurnSourcesRow.css";

const VISIBLE_SOURCE_LIMIT = 6;

/** Compact, host-deduplicated sources belonging to one tool-call group. */
export function TurnSourcesRow({ sources, running = false, onOpen }: {
  sources: TurnSource[];
  running?: boolean;
  onOpen?: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const renderActive = useConversationRenderActive();
  const revealingConversation = useConversationRevealSnap();
  const reducedMotion = useReducedMotion();
  const [expanded, setExpanded] = useState(false);
  const moreRef = useRef<HTMLButtonElement>(null);
  // Receipt belongs to the group, not to an icon's mount lifetime. In
  // particular, opening overflow or returning to a cached pane is not news.
  const seenHosts = useRef(new Set(sources.map(source => source.host)));
  const mayAnimate = running && renderActive && !revealingConversation && !reducedMotion;
  const newHosts = sources.filter(source => !seenHosts.current.has(source.host));
  const reveal = mayAnimate && seenHosts.current.size === 0 && newHosts.length > 0;
  useLayoutEffect(() => {
    for (const source of sources) seenHosts.current.add(source.host);
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
    <div className={`turn-web-research${reveal ? " is-revealing" : ""}`} role="group" aria-label={label}
      onKeyDown={event => {
        if (event.key === "Escape" && expanded) {
          event.stopPropagation();
          moreRef.current?.focus();
          setExpanded(false);
        }
      }}
    >
      <div className="web-research-sources">
        {sources.slice(0, VISIBLE_SOURCE_LIMIT).map(source => (
          <SourceLink key={source.host} source={source} onOpen={openURL}
            arrive={mayAnimate && !seenHosts.current.has(source.host)}
            motionAllowed={mayAnimate} index={newHosts.findIndex(item => item.host === source.host)} />
        ))}
        {sources.length > VISIBLE_SOURCE_LIMIT ? (
          <button type="button" ref={moreRef} className="web-research-more"
            aria-expanded={expanded} aria-controls={listID}
            aria-label={expanded ? t("sources.showLess") : t("sources.viewMore", { count: sources.length - VISIBLE_SOURCE_LIMIT })}
            onClick={() => setExpanded(value => !value)}
          >
            <span>+{sources.length - VISIBLE_SOURCE_LIMIT}</span>
            <ChevronDown aria-hidden="true" />
          </button>
        ) : null}
        <span className="web-research-count">{label}</span>
      </div>
      {sources.length > VISIBLE_SOURCE_LIMIT ? (
        <CollapsibleDetails id={listID} expanded={expanded} className="web-research-disclosure" innerClassName="web-research-overflow">
          <div className="web-research-source-list" inert={!expanded}>
            {sources.slice(VISIBLE_SOURCE_LIMIT).map(source => (
              <SourceLink key={source.host} source={source} onOpen={openURL}
                arrive={mayAnimate && expanded && !seenHosts.current.has(source.host)}
                motionAllowed={mayAnimate} index={newHosts.findIndex(item => item.host === source.host)} detail />
            ))}
          </div>
        </CollapsibleDetails>
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
        {detail ? <>
          <span className="web-source-description">
            <span className="web-source-title">{source.title || source.host}</span>
            {source.title ? <span className="web-source-host">{source.host}</span> : null}
          </span>
          <ArrowUpRight className="web-source-open-icon" aria-hidden="true" />
        </> : null}
      </button>
    </Tooltip>
  );
}
