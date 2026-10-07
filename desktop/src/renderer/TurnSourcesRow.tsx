import { useId, useRef, useState, type CSSProperties } from "react";
import type { TurnSource } from "./ToolActivityHelpers";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { CollapsibleDetails } from "./CollapsibleMotion";
import { useConversationRenderActive } from "./ConversationRenderActivity";
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
  const [expanded, setExpanded] = useState(false);
  const moreRef = useRef<HTMLButtonElement>(null);
  const initialHosts = useRef(new Set(sources.map(source => source.host)));
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
    <div className="turn-web-research" role="group" aria-label={label}
      onKeyDown={event => {
        if (event.key === "Escape" && expanded) {
          event.stopPropagation();
          moreRef.current?.focus();
          setExpanded(false);
        }
      }}
    >
      <div className="web-research-sources">
        {sources.slice(0, VISIBLE_SOURCE_LIMIT).map((source, index) => (
          <SourceCircle key={source.host} source={source} onOpen={openURL}
            arrive={running && renderActive && !initialHosts.current.has(source.host)} index={index} />
        ))}
        {sources.length > VISIBLE_SOURCE_LIMIT ? (
          <button type="button" ref={moreRef} className="web-research-more"
            aria-expanded={expanded} aria-controls={listID}
            aria-label={expanded ? t("sources.showLess") : t("sources.viewMore", { count: sources.length - VISIBLE_SOURCE_LIMIT })}
            onClick={() => setExpanded(value => !value)}
          >
            {expanded ? "−" : "+"}{sources.length - VISIBLE_SOURCE_LIMIT}
          </button>
        ) : null}
        <span className="web-research-count">{label}</span>
      </div>
      {sources.length > VISIBLE_SOURCE_LIMIT ? (
        <CollapsibleDetails id={listID} expanded={expanded} innerClassName="web-research-overflow">
          <div className="web-research-sources" inert={!expanded}>
            {sources.slice(VISIBLE_SOURCE_LIMIT).map((source, index) => (
              <SourceCircle key={source.host} source={source} onOpen={openURL}
                arrive={running && renderActive && !initialHosts.current.has(source.host)} index={index} />
            ))}
          </div>
        </CollapsibleDetails>
      ) : null}
    </div>
  );
}

function SourceCircle({ source, onOpen, arrive, index }: {
  source: TurnSource;
  onOpen: (url: string, modifiers?: WorkspaceBrowserOpenModifiers) => void;
  arrive: boolean;
  index: number;
}): JSX.Element {
  const { t } = useI18n();
  // Capture arrival once: later streaming updates must not replay it.
  const [animate] = useState(arrive);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const tooltip = source.title ? `${source.title} — ${source.url}` : source.url;
  return (
    <Tooltip content={tooltip}>
      <button type="button"
        className={`web-source-circle${animate ? " is-arriving" : ""}`}
        style={{ "--source-order": Math.min(index, VISIBLE_SOURCE_LIMIT - 1) } as CSSProperties}
        aria-label={t("sources.openNamed", { name: tooltip })}
        onClick={event => {
          event.preventDefault();
          onOpen(source.url, workspaceBrowserClickModifiers(event));
        }}
      >
        <span className="web-source-avatar" aria-hidden="true">
          {!loaded ? <span>{source.host[0]?.toUpperCase() ?? "·"}</span> : null}
          {!failed ? <img
            src={`https://www.google.com/s2/favicons?domain=${encodeURIComponent(source.host)}&sz=32`}
            alt="" loading="lazy" data-loaded={loaded}
            onLoad={() => setLoaded(true)} onError={() => { setFailed(true); setLoaded(false); }}
          /> : null}
        </span>
      </button>
    </Tooltip>
  );
}
