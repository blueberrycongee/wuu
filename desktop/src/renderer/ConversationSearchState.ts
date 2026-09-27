import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import type {
  RuntimeContext,
  ThreadSearchResultItem,
  Turn,
} from "../shared/protocol";
import {
  sameRuntimeContext,
  type AppState,
} from "./AppState";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { translateCurrent } from "./i18n";

const CONVERSATION_SEARCH_RESULT_LIMIT = 100;
const CONVERSATION_SEARCH_PREVIEW_LIMIT = 4;

export type CloseConversationSearchOptions = {
  immediate?: boolean;
};

export type ConversationSearchState = {
  open: boolean;
  closing: boolean;
  query: string;
  loading: boolean;
  error: string;
  results: ThreadSearchResultItem[];
  selectedIndex: number;
  // Preview pane (right column). previewedThreadID is the cache key —
  // when selection points at the same thread we already have turns for, we
  // skip the network round-trip. previewedTurns is the loaded content;
  // previewLoading/Error are the transient UI flags.
  previewedThreadID: string;
  previewedTurns: Turn[];
  previewLoading: boolean;
  previewError: string;
};

const initialConversationSearch: ConversationSearchState = {
  open: false,
  closing: false,
  query: "",
  loading: false,
  error: "",
  results: [],
  selectedIndex: 0,
  previewedThreadID: "",
  previewedTurns: [],
  previewLoading: false,
  previewError: "",
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
  onSelectThread: (threadID: string) => void;
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
  const conversationSearchPreviewRequestRef = useRef(0);
  const conversationSearchCloseTimerRef = useRef<number | undefined>(
    undefined,
  );
  const conversationSearchResults = conversationSearch.results;

  useEffect(() => {
    return () => {
      conversationSearchRequestRef.current += 1;
      conversationSearchPreviewRequestRef.current += 1;
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

  // Preview pane: when the selected search result points at a thread we have
  // not yet previewed, fetch the first few turns lazily. previewedThreadID
  // is the cache key, so re-selecting the same thread (e.g. mouse hover off
  // and back on) is instant. Stale responses are discarded via the request
  // counter so fast keyboard navigation never paints the wrong thread's
  // content into the preview pane.
  useEffect(() => {
    if (!conversationSearch.open || conversationSearch.closing) {
      return;
    }
    const target = currentSelectedSearchThreadID(conversationSearch);
    if (!target) return;
    if (target === conversationSearch.previewedThreadID) return;

    const requestID = conversationSearchPreviewRequestRef.current + 1;
    conversationSearchPreviewRequestRef.current = requestID;
    setConversationSearch((current) => ({
      ...current,
      previewedThreadID: target,
      previewedTurns: [],
      previewLoading: true,
      previewError: "",
    }));

    void window.wuu
      .getThreadPreview(target, CONVERSATION_SEARCH_PREVIEW_LIMIT)
      .then((result) => {
        if (requestID !== conversationSearchPreviewRequestRef.current) {
          return;
        }
        setConversationSearch((current) => {
          if (currentSelectedSearchThreadID(current) !== target) {
            return current;
          }
          return {
            ...current,
            previewedThreadID: target,
            previewedTurns: result.turns,
            previewLoading: false,
            previewError: "",
          };
        });
      })
      .catch((error: unknown) => {
        if (requestID !== conversationSearchPreviewRequestRef.current) {
          return;
        }
        setConversationSearch((current) => {
          if (currentSelectedSearchThreadID(current) !== target) {
            return current;
          }
          return {
            ...current,
            previewLoading: false,
            previewError:
              error instanceof Error ? error.message : translateCurrent("conversationSearch.previewLoadFailed"),
          };
        });
      });
  }, [
    conversationSearch.open,
    conversationSearch.closing,
    conversationSearch.results,
    conversationSearch.selectedIndex,
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
      // Refresh the preview only after fresh search results arrive.
      previewedThreadID: "",
      previewedTurns: [],
      previewLoading: false,
      previewError: "",
    }));
    window.requestAnimationFrame(() =>
      conversationSearchInputRef.current?.focus(),
    );
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
    const closeImmediately = options.immediate || prefersReducedMotion();
    setConversationSearch((current) => ({
      ...current,
      open: false,
      closing: !closeImmediately,
      loading: false,
      error: "",
      previewedThreadID: "",
      previewedTurns: [],
      previewLoading: false,
      previewError: "",
    }));
    conversationSearchPreviewRequestRef.current += 1;
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
    closeConversationSearch();
    onSelectThread(result.thread.id);
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
    // under a new query, even before its request has been sent.
    conversationSearchRequestRef.current += 1;
    conversationSearchPreviewRequestRef.current += 1;
    setConversationSearch((current) => ({
      ...current,
      query,
      loading: true,
      error: "",
      results: [],
      previewedThreadID: "",
      previewedTurns: [],
      previewLoading: false,
      previewError: "",
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

function currentSelectedSearchThreadID(state: ConversationSearchState): string {
  if (state.results.length === 0) return "";
  const idx = Math.max(0, Math.min(state.selectedIndex, state.results.length - 1));
  return state.results[idx]?.thread.id ?? "";
}
