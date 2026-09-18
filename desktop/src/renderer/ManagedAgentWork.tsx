import { List } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { isThreadExecuting, type ThreadSummary } from "./AppState";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { sortManagedSessions } from "./ManagedAgentSessions";
import { useI18n } from "./i18n";

export function ManagedAgentWork({ threads, expanded, onShowAll, onSelect }: {
  threads: ThreadSummary[];
  expanded: boolean;
  onShowAll: () => void;
  onSelect?: (threadID: string) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLButtonElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const hovered = useRef(false);
  const cardID = useId();
  const inside = (node: Node | null): boolean => Boolean(node && (anchorRef.current?.contains(node) || cardRef.current?.contains(node)));
  const clearClose = (): void => { clearTimeout(closeTimer.current); };
  const show = (): void => { clearClose(); setOpen(true); };
  const dismiss = (): void => {
    clearClose();
    hovered.current = false;
    if (cardRef.current?.contains(document.activeElement)) anchorRef.current?.focus();
    setOpen(false);
  };
  const leave = (): void => {
    clearClose();
    // Keep the card alive while crossing the gap or moving keyboard focus into it.
    closeTimer.current = setTimeout(() => {
      if (!hovered.current && !inside(document.activeElement)) setOpen(false);
    }, 200);
  };
  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useEffect(() => {
    if (!open) return;
    const keyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); dismiss(); }
    };
    const pointerDown = (event: PointerEvent): void => { if (!inside(event.target as Node)) dismiss(); };
    document.addEventListener("keydown", keyDown, true);
    document.addEventListener("pointerdown", pointerDown, true);
    return () => {
      document.removeEventListener("keydown", keyDown, true);
      document.removeEventListener("pointerdown", pointerDown, true);
    };
  }, [open]);
  const working = threads.filter(isThreadExecuting);
  const label = working.length ? t("channels.managedSessions.running", { count: working.length }) : t("channels.managedSessions.history");
  const preview = sortManagedSessions(threads).slice(0, 5);
  const showAll = (): void => { dismiss(); onShowAll(); };
  return <>
    <button ref={anchorRef} type="button" className="jump-to-latest-pill managed-agent-work-pill" aria-expanded={expanded} onClick={showAll}
      aria-label={`${label} · ${t("channels.managedSessions.all")}`} aria-describedby={open ? cardID : undefined}
      onPointerEnter={event => { if (event.pointerType !== "touch") { hovered.current = true; show(); } }}
      onPointerLeave={() => { hovered.current = false; leave(); }} onFocus={show} onBlur={leave}>
      {!working.length ? <List size={14} aria-hidden="true" /> : null}
      <span>{label}</span>
    </button>
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="managed-sessions" placement="above" align="center" width={360}>
      <div ref={cardRef} id={cardID} className="conversation-status-preview-card" role="region" aria-label={t("channels.managedSessions.history")}
        onPointerEnter={() => { hovered.current = true; clearClose(); }} onPointerLeave={() => { hovered.current = false; leave(); }} onFocus={clearClose} onBlur={leave}>
        <div className="conversation-status-todo-card-header"><strong>{label}</strong></div>
        {preview.length ? <ul className="managed-session-preview-list">
          {preview.map(thread => <li key={thread.id}>
            <button type="button" className="managed-session-preview-item" onClick={() => { dismiss(); onSelect?.(thread.id); }}>
              <span className="managed-session-preview-title">{thread.title || t("channels.sessions.untitled")}</span>
            {isThreadExecuting(thread) ? <span className="managed-session-preview-state">{t("channels.sessions.state.running")}</span> : null}
            </button>
          </li>)}
        </ul> : <p className="conversation-status-todo-explanation">{t("channels.managedSessions.empty")}</p>}
        {preview.length ? <button type="button" className="managed-session-preview-all" onClick={() => { dismiss(); if (!expanded) onShowAll(); }}>{t("channels.managedSessions.all")}</button> : null}
      </div>
    </FloatingMenuPortal> : null}
  </>;
}
