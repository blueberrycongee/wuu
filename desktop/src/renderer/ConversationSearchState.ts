import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import type {
  RuntimeContext,
  ThreadSearchResultItem,
} from "../shared/protocol";
import {
  sameRuntimeContext,
  type AppState,
} from "./AppState";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { translateCurrent } from "./i18n";

const CONVERSATION_SEARCH_RESULT_LIMIT = 100;

export type CloseConversationSearchOptions = {
  immediate?: boolean;
  /** Choosing a result hands focus to the conversation it opens instead. */
  restoreFocus?: boolean;
};

export type ConversationSearchState = {
  open: boolean;
  closing: boolean;
  query: string;
  loading: boolean;
  error: string;
  results: ThreadSearchResultItem[];
  selectedIndex: number;
};

const initialConversationSearch: ConversationSearchState = {
  open: false,
  closing: false,
  query: "",
  loading: false,
  error: "",
  results: [],
  selectedIndex: 0,
};

export function useConversationSearch({
  activeContext,
  getAppState,
  cacheThreads,
  onOpen,
  onSelectThread,
}: {
  activeContext?: RuntimeContext;
  getAppState: () => AppState;
  cacheThreads: (threads: ThreadSearchResultItem["thread"][]) => void;
  onOpen: () => void;
  onSelectThread: (result: ThreadSearchResultItem, query: string) => void;
}): {
  conversationSearch: ConversationSearchState;
  conversationSearchResults: ThreadSearchResultItem[];
  conversationSearchRef: React.RefObject<HTMLDivElement | null>;
  conversationSearchInputRef: React.RefObject<HTMLInputElement | null>;
  toggleConversationSearch: () => void;
  closeConversationSearch: (options?: CloseConversationSearchOptions) => void;
  selectConversationSearchResult: (result: ThreadSearchResultItem) => void;
  handleConversationSearchKeyDown: (
    event: ReactKeyboardEvent<HTMLInputElement>,
  ) => void;
  setConversationSearchQuery: (query: string, composing?: boolean) => void;
  clearConversationSearchQuery: () => void;
  setConversationSearchSelectedIndex: (index: number) => void;
} {
  const [conversationSearch, setConversationSearch] =
    useState<ConversationSearchState>(initialConversationSearch);
  const conversationSearchRef = useRef<HTMLDivElement>(null);
  const conversationSearchInputRef = useRef<HTMLInputElement>(null);
  const conversationSearchRequestRef = useRef(0);
  const queryRef = useRef({ query: "", composing: false });
  const [composing, setComposing] = useState(false);
  const resultsRequestRef = useRef(-1);
  const conversationSearchCloseTimerRef = useRef<number | undefined>(
    undefined,
  );
  const focusBeforeOpenRef = useRef<HTMLElement | null>(null);
  const conversationSearchResults = conversationSearch.results;

  useEffect(() => {
    return () => {
      conversationSearchRequestRef.current += 1;
      if (conversationSearchCloseTimerRef.current !== undefined) {
        window.clearTimeout(conversationSearchCloseTimerRef.current);
        conversationSearchCloseTimerRef.current = undefined;
      }
    };
  }, []);

  useEffect(() => {
    if (!conversationSearch.open || conversationSearch.closing || composing) {
      return undefined;
    }
    const delay = conversationSearch.query.trim() ? 140 : 0;
    const timer = window.setTimeout(() => {
      void refreshConversationSearchThreads(conversationSearch.query);
    }, delay);
    return () => window.clearTimeout(timer);
  }, [
    conversationSearch.closing,
    conversationSearch.open,
    conversationSearch.query,
    composing,
  ]);

  function toggleConversationSearch(): void {
    if (conversationSearch.open) {
      closeConversationSearch();
      return;
    }
    openConversationSearch();
  }

  function openConversationSearch(): void {
    if (!activeContext) {
      return;
    }
    if (conversationSearchCloseTimerRef.current !== undefined) {
      window.clearTimeout(conversationSearchCloseTimerRef.current);
      conversationSearchCloseTimerRef.current = undefined;
    }
    onOpen();
    const focused = document.activeElement;
    focusBeforeOpenRef.current =
      focused instanceof HTMLElement && focused !== document.body ? focused : null;
    queryRef.current.composing = false;
    setComposing(false);
    setConversationSearch((current) => ({
      ...current,
      open: true,
      closing: false,
      loading: true,
      error: "",
      selectedIndex: 0,
      results: [],
    }));
    // The last query comes back selected: typing replaces it, and the
    // arrow keys can still pick up where the previous search left off.
    window.requestAnimationFrame(() => {
      conversationSearchInputRef.current?.focus();
      conversationSearchInputRef.current?.select();
    });
  }

  function closeConversationSearch(
    options: CloseConversationSearchOptions = {},
  ): void {
    if (!conversationSearch.open && !conversationSearch.closing) {
      return;
    }
    conversationSearchRequestRef.current += 1;
    if (conversationSearchCloseTimerRef.current !== undefined) {
      window.clearTimeout(conversationSearchCloseTimerRef.current);
      conversationSearchCloseTimerRef.current = undefined;
    }
    const opener = focusBeforeOpenRef.current;
    focusBeforeOpenRef.current = null;
    // A control inside a collapsed rail would pin the drawer open by focus.
    if (
      options.restoreFocus !== false &&
      opener?.isConnected &&
      !opener.closest(".sidebar-collapsed :is(.sidebar, .settings-sidebar)")
    ) {
      opener.focus({ preventScroll: true });
    }
    const closeImmediately = options.immediate || prefersReducedMotion();
    setConversationSearch((current) => ({
      ...current,
      open: false,
      closing: !closeImmediately,
      loading: false,
      error: "",
    }));
    if (closeImmediately) {
      return;
    }
    conversationSearchCloseTimerRef.current = window.setTimeout(() => {
      conversationSearchCloseTimerRef.current = undefined;
      setConversationSearch((current) =>
        current.open ? current : { ...current, closing: false },
      );
    }, motionDurationMs("--search-exit-duration", 180));
  }

  async function refreshConversationSearchThreads(
    query = conversationSearch.query,
  ): Promise<void> {
    const sourceContext = getAppState().activeContext;
    if (!sourceContext) {
      return;
    }
    const requestID = conversationSearchRequestRef.current + 1;
    conversationSearchRequestRef.current = requestID;
    setConversationSearch((current) => ({
      ...current,
      loading: true,
      error: "",
    }));
    try {
      const search = await window.wuu.searchThreads(
        query,
        CONVERSATION_SEARCH_RESULT_LIMIT,
      );
      if (
        requestID !== conversationSearchRequestRef.current ||
        !sameRuntimeContext(sourceContext, getAppState().activeContext)
      ) {
        return;
      }
      const threads = search.results.map((result) => result.thread);
      resultsRequestRef.current = requestID;
      cacheThreads(threads);
      setConversationSearch((current) => ({
        ...current,
        results: search.results,
        loading: false,
        error: "",
        selectedIndex: Math.max(
          0,
          Math.min(current.selectedIndex, search.results.length - 1),
        ),
      }));
    } catch (error) {
      if (
        requestID !== conversationSearchRequestRef.current ||
        !sameRuntimeContext(sourceContext, getAppState().activeContext)
      ) {
        return;
      }
      setConversationSearch((current) => ({
        ...current,
        loading: false,
        error: error instanceof Error ? error.message : translateCurrent("conversationSearch.searchFailed"),
      }));
    }
  }

  function selectConversationSearchResult(result: ThreadSearchResultItem): void {
    if (
      resultsRequestRef.current !== conversationSearchRequestRef.current ||
      !conversationSearchResults.includes(result)
    ) return;
    closeConversationSearch({ restoreFocus: false });
    onSelectThread(result, conversationSearch.query);
  }

  function handleConversationSearchKeyDown(
    event: ReactKeyboardEvent<HTMLInputElement>,
  ): void {
    if (
      queryRef.current.composing || event.nativeEvent.isComposing || event.keyCode === 229
    ) return;
    if (event.key === "Escape") {
      event.preventDefault();
      closeConversationSearch();
      return;
    }
    if (event.key === "ArrowDown" && conversationSearchResults.length > 0) {
      event.preventDefault();
      setConversationSearch((current) => ({
        ...current,
        selectedIndex:
          (current.selectedIndex + 1) % conversationSearchResults.length,
      }));
      return;
    }
    if (event.key === "ArrowUp" && conversationSearchResults.length > 0) {
      event.preventDefault();
      setConversationSearch((current) => ({
        ...current,
        selectedIndex:
          (current.selectedIndex - 1 + conversationSearchResults.length) %
          conversationSearchResults.length,
      }));
      return;
    }
    if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
      const index = Number(event.key) - 1;
      const result = conversationSearchResults[index];
      if (result) {
        event.preventDefault();
        selectConversationSearchResult(result);
      }
      return;
    }
    const selectedResult =
      conversationSearchResults[
        Math.max(
          0,
          Math.min(
            conversationSearch.selectedIndex,
            conversationSearchResults.length - 1,
          ),
        )
      ];
    if (event.key === "Enter" && selectedResult) {
      event.preventDefault();
      selectConversationSearchResult(selectedResult);
    }
  }

  function setConversationSearchQuery(query: string, composing = false): void {
    if (queryRef.current.query === query && queryRef.current.composing === composing) return;
    queryRef.current = { query, composing };
    setComposing(composing);
    // Invalidate before debounce: old responses cannot become selectable
    // under a new query, even before its request has been sent. The previous
    // results stay on screen until the new ones arrive, so each keystroke
    // does not collapse the palette to a loading line and back.
    conversationSearchRequestRef.current += 1;
    setConversationSearch((current) => ({
      ...current,
      query,
      loading: true,
      error: "",
      selectedIndex: 0,
    }));
  }

  function clearConversationSearchQuery(): void {
    setConversationSearchQuery("");
  }

  function setConversationSearchSelectedIndex(index: number): void {
    setConversationSearch((current) => ({
      ...current,
      selectedIndex: index,
    }));
  }

  return {
    conversationSearch,
    conversationSearchResults,
    conversationSearchRef,
    conversationSearchInputRef,
    toggleConversationSearch,
    closeConversationSearch,
    selectConversationSearchResult,
    handleConversationSearchKeyDown,
    setConversationSearchQuery,
    clearConversationSearchQuery,
    setConversationSearchSelectedIndex,
  };
}
