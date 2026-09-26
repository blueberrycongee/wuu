import { useEffect, useId, useRef, useState } from "react";
import type { Thread } from "../shared/protocol";
import { isThreadExecuting } from "./AppState";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { ProjectSessionList } from "./ProjectSessionList";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

/**
 * The coordinator's title control: how many sessions the project manages and
 * how many are running. It opens the session list; a row opens that session.
 */
export function ProjectSessionsControl({ projectID, sessions, lastViewedTurnByThreadID, onOpenSession }: {
  projectID: string;
  sessions: Thread[];
  lastViewedTurnByThreadID: Record<string, string>;
  onOpenSession: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [pendingBySessionID, setPendingBySessionID] = useState<ReadonlyMap<string, number>>(new Map());
  const anchorRef = useRef<HTMLButtonElement>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const cardID = useId();
  const inside = (node: Node | null): boolean => Boolean(node && (anchorRef.current?.contains(node) || cardRef.current?.contains(node)));
  const dismiss = (): void => {
    if (cardRef.current?.contains(document.activeElement)) anchorRef.current?.focus();
    setOpen(false);
  };
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
  // Pending counts are read when the list opens; decisions happen in sessions.
  useEffect(() => {
    if (!open || !window.wuu.projectCandidate) return;
    let current = true;
    window.wuu.projectCandidate({ action: "list", project_id: projectID }).then(result => {
      if (!current) return;
      const counts = new Map<string, number>();
      for (const candidate of result.candidates ?? []) {
        if (!candidate.disposition) counts.set(candidate.session_id, (counts.get(candidate.session_id) ?? 0) + 1);
      }
      setPendingBySessionID(counts);
    }).catch(error => { if (current) showErrorToast(error); });
    return () => { current = false; };
  }, [open, projectID]);
  const running = sessions.filter(isThreadExecuting).length;
  const count = t(sessions.length === 1 ? "projects.sessionsOne" : "projects.sessions", { count: sessions.length });
  const label = running ? t("projects.sessionsRunning", { sessions: count, count: running }) : count;
  return <>
    <button ref={anchorRef} type="button" className="settings-button settings-button-ghost project-sessions-button"
      aria-expanded={open} aria-controls={open ? cardID : undefined} onClick={() => setOpen(value => !value)}>
      <span>{label}</span>
    </button>
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="project-sessions" placement="below" align="right" width={360} flip>
      <div ref={cardRef} id={cardID} className="project-sessions-menu" role="region" aria-label={t("projects.sessionList")}>
        <ProjectSessionList sessions={sessions} pendingBySessionID={pendingBySessionID}
          lastViewedTurnByThreadID={lastViewedTurnByThreadID}
          onSelect={id => { dismiss(); onOpenSession(id); }} />
      </div>
    </FloatingMenuPortal> : null}
  </>;
}
