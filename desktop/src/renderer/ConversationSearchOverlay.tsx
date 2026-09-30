import { Search, X } from "./WuuIcons";
import { useEffect, useRef, type KeyboardEvent, type RefObject } from "react";
import type { DesktopProject, Thread, ThreadSearchResultItem } from "../shared/protocol";
import { conversationSearchPattern, conversationSearchVisibleSnippet } from "./ConversationSearchDisplay";
import { primaryShortcutLabel } from "./platform";
import type { ConversationSearchState } from "./ConversationSearchState";
import { conversationSearchContextLabel } from "./AppState";
import { threadDisplayTitle } from "./ThreadTitles";
import { useI18n } from "./i18n";

function SearchMatchText({ text, query }: { text: string; query: string }): JSX.Element {
  const pattern = conversationSearchPattern(query);
  const parts: (string | JSX.Element)[] = [];
  let cursor = 0;
  if (pattern) {
    for (const match of text.matchAll(pattern)) {
      parts.push(text.slice(cursor, match.index));
      parts.push(<mark key={match.index}>{match[0]}</mark>);
      cursor = match.index + match[0].length;
    }
  }
  parts.push(text.slice(cursor));
  return <>{parts}</>;
}

export function ConversationSearchOverlay({
  state, results, threads, projects, activeThreadID, pendingThreadID,
  dialogRef, inputRef, onClose, onQueryChange, onClearQuery, onKeyDown,
  onSelectIndex, onSelectResult,
}: {
  state: ConversationSearchState;
  results: ThreadSearchResultItem[];
  threads: Thread[];
  projects: DesktopProject[];
  activeThreadID?: string;
  pendingThreadID?: string;
  dialogRef: RefObject<HTMLDivElement | null>;
  inputRef: RefObject<HTMLInputElement | null>;
  onClose: () => void;
  onQueryChange: (query: string, composing?: boolean) => void;
  onClearQuery: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLInputElement>) => void;
  onSelectIndex: (index: number) => void;
  onSelectResult: (result: ThreadSearchResultItem) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const composingRef = useRef(false);
  useEffect(() => { composingRef.current = false; }, [state.open]);
  useEffect(() => {
    dialogRef.current?.querySelector("[aria-selected=true]")?.scrollIntoView?.({ block: "nearest" });
  }, [state.selectedIndex, results, dialogRef]);
  if (!state.open && !state.closing) return null;

  return (
    <div
      className={`app-modal-backdrop conversation-search-overlay${state.closing ? " closing" : ""}`}
      onPointerDown={event => { if (event.target === event.currentTarget) onClose(); }}
    >
      <div className="conversation-search-dialog" role="dialog" aria-modal="true"
        aria-label={t("search.conversations")} ref={dialogRef}>
        <div className="menu-search conversation-search-input-wrap">
          <Search className="icon-lg" aria-hidden="true" />
          <input
            ref={inputRef}
            value={state.query}
            aria-label={t("search.conversations")}
            role="combobox"
            aria-expanded="true"
            aria-controls="conversation-search-results"
            aria-autocomplete="list"
            aria-activedescendant={results[state.selectedIndex] ? `conversation-search-option-${state.selectedIndex}` : undefined}
            placeholder={t("search.placeholder")}
            onChange={event => onQueryChange(event.target.value, composingRef.current)}
            onCompositionStart={event => {
              composingRef.current = true;
              onQueryChange(event.currentTarget.value, true);
            }}
            onCompositionEnd={event => {
              composingRef.current = false;
              onQueryChange(event.currentTarget.value, false);
            }}
            onKeyDown={onKeyDown}
          />
          {state.query ? (
            <button className="conversation-search-clear" type="button" aria-label={t("search.clear")}
              onClick={() => { onClearQuery(); inputRef.current?.focus(); }}>
              <X className="icon" />
            </button>
          ) : (
            <kbd className="conversation-search-shortcut-hint" aria-hidden="true">{primaryShortcutLabel("P")}</kbd>
          )}
        </div>
        {state.error ? <div className="conversation-search-error" role="alert">{state.error}</div> : null}
        <div className="conversation-search-results" id="conversation-search-results" role="listbox"
          aria-label={t("search.conversations")} aria-busy={state.loading}>
          {results.map((result, index) => {
            const thread = result.thread;
            const title = threadDisplayTitle(thread, threads, t("search.untitledConversation"));
            const context = conversationSearchContextLabel(thread, projects);
            // Title matches already explain the result; repeating the title as
            // an excerpt costs a row without helping distinguish conversations.
            const snippet = conversationSearchPattern(state.query)?.test(title) && !result.message_seq
              ? ""
              : conversationSearchVisibleSnippet({ query: state.query, snippet: result.snippet, title });
            const active = thread.id === activeThreadID;
            const selected = state.selectedIndex === index;
            return (
              <button
                key={thread.id} id={`conversation-search-option-${index}`} role="option"
                className={`conversation-search-result${pendingThreadID === thread.id ? " pending" : ""}${selected ? " selected" : ""}`}
                type="button" tabIndex={-1} aria-current={active ? "page" : undefined} aria-selected={selected}
                onMouseEnter={() => onSelectIndex(index)}
                onClick={() => onSelectResult(result)}
              >
                <span className="conversation-search-result-title" title={title}>
                  <SearchMatchText text={title} query={state.query} />
                </span>
                <span className="conversation-search-result-context" title={context}>{context}</span>
                <span className="conversation-search-result-status">
                  {thread.archived ? <span className="conversation-search-result-archived">{t("search.archived")}</span> : null}
                  {active ? <span className="conversation-search-result-current">{t("search.currentConversation")}</span> : null}
                  <kbd className={`conversation-search-result-shortcut${index < 9 ? "" : " empty"}`} aria-hidden="true">
                    {index < 9 ? primaryShortcutLabel(index + 1) : ""}
                  </kbd>
                </span>
                {snippet ? (
                  <span className="conversation-search-result-snippet">
                    <SearchMatchText text={snippet} query={state.query} />
                  </span>
                ) : null}
              </button>
            );
          })}
        </div>
        {results.length === 0 ? (
          <div className="conversation-search-empty" role="status" data-loading={state.loading || undefined}>
            {state.loading ? t("search.searching") : state.query.trim() ? t("search.noMatches") : t("search.noConversations")}
          </div>
        ) : null}
      </div>
    </div>
  );
}
