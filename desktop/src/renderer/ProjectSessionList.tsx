import { ArrowLeft, ArrowRight, LoaderCircle, Search } from "./WuuIcons";
import { useMemo, useRef, useState } from "react";
import type { Thread } from "../shared/protocol";
import { isThreadExecuting, isThreadUnread } from "./AppState";
import { baseThreadTitle } from "./ThreadTitles";
import { useI18n } from "./i18n";

const PAGE_SIZE = 30;
// A short list needs no filter row.
const SEARCH_THRESHOLD = 8;

/** A project's managed sessions, newest activity first; running sessions lead. */
export function ProjectSessionList({ sessions, pendingBySessionID, lastViewedTurnByThreadID, onSelect }: {
  sessions: Thread[];
  pendingBySessionID: ReadonlyMap<string, number>;
  lastViewedTurnByThreadID: Record<string, string>;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const results = useMemo(() => {
    const search = query.trim().toLocaleLowerCase();
    return sessions.filter(thread => !search ||
      `${baseThreadTitle(thread, sessions)} ${thread.id}`.toLocaleLowerCase().includes(search));
  }, [sessions, query]);
  const pages = Math.max(1, Math.ceil(results.length / PAGE_SIZE));
  const currentPage = Math.min(page, pages - 1);
  const changePage = (next: number) => {
    setPage(next);
    scrollRef.current?.scrollTo?.({ top: 0 });
  };

  return <section className="project-session-view" aria-label={t("projects.sessionList")}>
    {sessions.length > SEARCH_THRESHOLD ? <label className="menu-search">
      <Search className="icon-sm" aria-hidden="true" />
      <input type="search" value={query} autoFocus aria-label={t("projects.searchSessions")}
        placeholder={t("projects.searchSessions")} onChange={event => { setQuery(event.currentTarget.value); changePage(0); }} />
    </label> : null}
    <div className="project-session-results" ref={scrollRef}>
      {results.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE).map(thread => {
        const running = isThreadExecuting(thread);
        const unread = !running && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
        const title = baseThreadTitle(thread, sessions);
        const pending = pendingBySessionID.get(thread.id) ?? 0;
        const control = thread.session_control;
        const details = [
          t(running ? "projects.running" : "projects.idle"),
          control ? t(`sessionControl.${control.state === "taken_over" ? "takenOver" : control.state}`) : "",
          pending ? t("projects.pendingCandidates", { count: pending }) : "",
        ].filter(Boolean).join(" · ");
        return <button key={thread.id} type="button" className="project-session-result" onClick={() => onSelect(thread.id)}
          title={title}>
          <span className="project-session-result-status">
            {running ? <LoaderCircle className="project-session-spinner" aria-hidden="true" />
              : unread ? <span className="project-session-unread" aria-hidden="true" /> : null}
          </span>
          <span className="project-session-result-copy">
            <span>{title}</span>
            <small>{details}</small>
          </span>
        </button>;
      })}
      {!results.length ? <p className="project-session-empty">{t(query.trim() ? "projects.noMatchingSessions" : "projects.noSessions")}</p> : null}
    </div>
    {pages > 1 ? <nav className="project-session-pagination" aria-label={t("projects.sessionList")}>
      <button type="button" className="icon-button" disabled={currentPage === 0} aria-label={t("projects.previousPage")}
        onClick={() => changePage(currentPage - 1)}><ArrowLeft aria-hidden="true" /></button>
      <span role="status">{currentPage + 1} / {pages}</span>
      <button type="button" className="icon-button" disabled={currentPage === pages - 1} aria-label={t("projects.nextPage")}
        onClick={() => changePage(currentPage + 1)}><ArrowRight aria-hidden="true" /></button>
    </nav> : null}
  </section>;
}
