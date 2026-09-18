import { ArrowLeft, ArrowRight, LoaderCircle, PanelRightClose, Search } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { isThreadExecuting, isThreadUnread, type ThreadSummary } from "./AppState";
import { sortManagedSessions } from "./ManagedAgentSessions";
import { baseThreadTitle } from "./ThreadTitles";
import { useI18n } from "./i18n";

const PAGE_SIZE = 30;

export function ManagedAgentSessionView({ threads, lastViewedTurnByThreadID, onSelect }: {
  threads: ThreadSummary[];
  lastViewedTurnByThreadID: Record<string, string>;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const results = useMemo(() => {
    const search = query.trim().toLocaleLowerCase();
    return sortManagedSessions(threads).filter(thread => !search ||
      `${baseThreadTitle(thread, threads)} ${thread.cwd} ${thread.id}`.toLocaleLowerCase().includes(search));
  }, [threads, query]);
  const pages = Math.max(1, Math.ceil(results.length / PAGE_SIZE));
  const currentPage = Math.min(page, pages - 1);
  const changePage = (next: number) => {
    setPage(next);
    scrollRef.current?.scrollTo?.({ top: 0 });
  };

  return <section className="managed-session-view" aria-label={t("channels.managedSessions.all")}>
    <label className="catalog-search managed-session-search">
      <Search aria-hidden="true" />
      <input type="search" value={query} autoFocus aria-label={t("channels.searchConversations")}
        placeholder={t("channels.searchConversations")} onChange={event => { setQuery(event.currentTarget.value); changePage(0); }} />
    </label>
    <div className="managed-session-results scroll-region" ref={scrollRef}>
      {results.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE).map(thread => {
        const running = isThreadExecuting(thread);
        const unread = !running && isThreadUnread(thread, lastViewedTurnByThreadID[thread.id]);
        const title = baseThreadTitle(thread, threads);
        return <button key={thread.id} type="button" className="managed-session-result" onClick={() => onSelect(thread.id)}
          title={`${title}\n${thread.cwd}`}>
          <span className="managed-session-result-status">
            {running ? <LoaderCircle className="managed-session-spinner" aria-label={t("threadSidebar.responding")} />
              : unread ? <span className="managed-session-unread" role="img" aria-label={t("channels.managedSessions.unread", { count: 1 })} /> : null}
          </span>
          <span className="managed-session-result-copy"><span>{title}</span></span>
        </button>;
      })}
      {!results.length ? <p className="managed-session-empty">{t(query.trim() ? "channels.noMatchingConversations" : "channels.managedSessions.empty")}</p> : null}
    </div>
    {pages > 1 ? <nav className="managed-session-pagination" aria-label={t("channels.managedSessions.pagination")}>
      <button type="button" className="icon-button" disabled={currentPage === 0} aria-label={t("channels.managedSessions.previous")}
        onClick={() => changePage(currentPage - 1)}><ArrowLeft aria-hidden="true" /></button>
      <span role="status">{currentPage + 1} / {pages}</span>
      <button type="button" className="icon-button" disabled={currentPage === pages - 1} aria-label={t("channels.managedSessions.next")}
        onClick={() => changePage(currentPage + 1)}><ArrowRight aria-hidden="true" /></button>
    </nav> : null}
  </section>;
}

export function ManagedAgentSessionPanel({ name, threads, lastViewedTurnByThreadID, overlay, closing, onClose, onSelect }: {
  name: string;
  threads: ThreadSummary[];
  lastViewedTurnByThreadID: Record<string, string>;
  overlay: boolean;
  closing: boolean;
  onClose: () => void;
  onSelect: (id: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  return <aside inert={closing} className={`session-inspector-extension managed-session-panel${closing ? " closing" : ""}`}
    aria-label={`${name} · ${t("channels.managedSessions.all")}`} onKeyDown={event => {
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); onClose(); }
    }}>
    <header className="session-inspector-header">
      <button type="button" className="icon-button" aria-label={t(overlay ? "channels.backToChat" : "common.close")} onClick={onClose}>
        {overlay ? <ArrowLeft aria-hidden="true" /> : <PanelRightClose aria-hidden="true" />}
      </button>
      <strong>{name} · {t("channels.managedSessions.all")}</strong>
    </header>
    <ManagedAgentSessionView threads={threads} lastViewedTurnByThreadID={lastViewedTurnByThreadID} onSelect={onSelect} />
  </aside>;
}
