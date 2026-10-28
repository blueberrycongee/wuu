import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import type { ApplicationSearchActionMapV1, ApplicationSearchResultV1, ApplicationSearchViewV1 } from "../../../packages/plugin-sdk/src/ui-search";
import { showErrorToast } from "./Toast";

export interface ConversationSearchApplication {
  getSnapshot(): { readonly search: ApplicationSearchViewV1 };
  subscribe(listener: () => void): () => void;
  invoke<A extends keyof ApplicationSearchActionMapV1>(action: A, input: ApplicationSearchActionMapV1[A]["input"]): Promise<ApplicationSearchActionMapV1[A]["result"]>;
}
import { motionDurationMs, prefersReducedMotion } from "./motion";

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
  results: readonly ApplicationSearchResultV1[];
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
  application,
  open,
  onOpenChange,
  onOpen,
  onSelect,
}: {
  application: ConversationSearchApplication;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onOpen: () => void;
  onSelect: () => void;
}): {
  conversationSearch: ConversationSearchState;
  conversationSearchResults: readonly ApplicationSearchResultV1[];
  conversationSearchRef: React.RefObject<HTMLDivElement | null>;
  conversationSearchInputRef: React.RefObject<HTMLInputElement | null>;
  toggleConversationSearch: () => void;
  closeConversationSearch: (options?: CloseConversationSearchOptions) => void;
  selectConversationSearchResult: (result: ApplicationSearchResultV1) => void;
  handleConversationSearchKeyDown: (
    event: ReactKeyboardEvent<HTMLInputElement>,
  ) => void;
  setConversationSearchQuery: (query: string, composing?: boolean) => void;
  clearConversationSearchQuery: () => void;
  setConversationSearchSelectedIndex: (index: number) => void;
} {
  const [presentation, setConversationSearch] =
    useState<ConversationSearchState>(initialConversationSearch);
  const conversationSearchRef = useRef<HTMLDivElement>(null);
  const conversationSearchInputRef = useRef<HTMLInputElement>(null);
  const search = useSyncExternalStore(application.subscribe, () => application.getSnapshot().search);
  const queryRef = useRef({ query: search.query, composing: search.composing });
  const conversationSearch: ConversationSearchState = { ...presentation, ...search, error: search.error ?? "",
    selectedIndex: Math.max(0, Math.min(presentation.selectedIndex, search.results.length - 1)) };
  const conversationSearchCloseTimerRef = useRef<number | undefined>(
    undefined,
  );
  const focusBeforeOpenRef = useRef<HTMLElement | null>(null);
  const interactionVersionRef = useRef(0);
  const openingFocusRef = useRef<{ application: ConversationSearchApplication; interactionVersion: number; origin: Element | null } | null>(null);
  const conversationSearchResults = conversationSearch.results;

  useEffect(() => {
    const observeInteraction = () => { interactionVersionRef.current++; };
    window.addEventListener("pointerdown", observeInteraction, true);
    window.addEventListener("keydown", observeInteraction, true);
    return () => {
      window.removeEventListener("pointerdown", observeInteraction, true);
      window.removeEventListener("keydown", observeInteraction, true);
      openingFocusRef.current = null;
      if (conversationSearchCloseTimerRef.current !== undefined) {
        window.clearTimeout(conversationSearchCloseTimerRef.current);
        conversationSearchCloseTimerRef.current = undefined;
      }
    };
  }, []);

  useLayoutEffect(() => {
    const request = openingFocusRef.current;
    if (!presentation.open || !request) return;
    openingFocusRef.current = null;
    // A frame can run before React commits the dialog. Its committed input,
    // not an arbitrary frame, owns this one opening focus transfer.
    const input = conversationSearchInputRef.current;
    if (!input || request.application !== application
      || request.interactionVersion !== interactionVersionRef.current
      || (document.activeElement !== request.origin && document.activeElement !== document.body)) return;
    input.focus();
    input.select();
  }, [application, presentation.open]);

  useEffect(() => {
    if (open === undefined || open === conversationSearch.open) return;
    if (open) openConversationSearch();
    else closeConversationSearch();
  }, [open]);

  function toggleConversationSearch(): void {
    if (conversationSearch.open) {
      closeConversationSearch();
      return;
    }
    openConversationSearch();
  }

  function openConversationSearch(): void {
    if (conversationSearchCloseTimerRef.current !== undefined) {
      window.clearTimeout(conversationSearchCloseTimerRef.current);
      conversationSearchCloseTimerRef.current = undefined;
    }
    if (open !== true) onOpenChange?.(true);
    onOpen();
    const focused = document.activeElement;
    focusBeforeOpenRef.current =
      focused instanceof HTMLElement && focused !== document.body ? focused : null;
    openingFocusRef.current = { application, origin: focused, interactionVersion: interactionVersionRef.current };
    queryRef.current.composing = false;
    void application.invoke("search.run", {}).catch(showErrorToast);
    setConversationSearch((current) => ({
      ...current,
      open: true,
      closing: false,
      loading: true,
      error: "",
      selectedIndex: 0,
      results: [],
    }));
  }

  function closeConversationSearch(
    options: CloseConversationSearchOptions = {},
  ): void {
    openingFocusRef.current = null;
    if (!conversationSearch.open && !conversationSearch.closing) {
      return;
    }
    if (open !== false) onOpenChange?.(false);
    if (options.restoreFocus !== false) void application.invoke("search.cancel", {}).catch(showErrorToast);
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

  function selectConversationSearchResult(result: ApplicationSearchResultV1): void {
    const current = application.getSnapshot().search;
    if (current.loading || current.composing || !current.results.some(row => row.resultId === result.resultId)) return;
    // Locate claims its address before dismissal; dismissal must not cancel it.
    void application.invoke("search.locate", { resultId: result.resultId }).catch(showErrorToast);
    closeConversationSearch({ restoreFocus: false });
    onSelect();
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
    void application.invoke("search.query", { query, composing }).catch(showErrorToast);
    setConversationSearch(current => ({ ...current, selectedIndex: 0 }));
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
