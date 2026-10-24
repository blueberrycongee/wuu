import { useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import type { ApplicationWebWorkspaceV1 as WebWorkspaceApi } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import type { WebWorkspaceLibraryStateV1 as WebWorkspaceLibraryState } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { Bookmark, Clock3 } from "./WuuIcons";
import { composerMenuWidth, FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";
import { NativeBrowserPopover } from "./NativeBrowserPopover";

/** The toolbar and start page share local address completion and keyboard behavior. */
export function WebWorkspaceAddress({
  api, destinationId, pageKey, active, inputRef, value, disabled, invalid, errorID,
  onChange, onSubmit, onEscape, className = "", leading, children,
}: {
  api?: WebWorkspaceApi;
  destinationId?: string;
  pageKey: string;
  active: boolean;
  inputRef: RefObject<HTMLInputElement | null>;
  value: string;
  disabled: boolean;
  invalid: boolean;
  errorID?: string;
  onChange: (value: string) => void;
  onSubmit: (value: string) => boolean;
  onEscape: () => void;
  className?: string;
  leading?: ReactNode;
  children?: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const formRef = useRef<HTMLFormElement>(null);
  const suggestionsRef = useRef<HTMLUListElement>(null);
  const composingAddress = useRef(false);
  const [addressFocused, setAddressFocused] = useState(false);
  const [suggestionsOpen, setSuggestionsOpen] = useState(false);
  const [selectedSuggestionURL, setSelectedSuggestionURL] = useState<string>();
  const [libraryState, setLibraryState] = useState<WebWorkspaceLibraryState>();
  const suggestionsID = useId();
  const suggestions = useMemo(() => {
    const query = value.trim().toLowerCase();
    if (!addressFocused || !query || !libraryState) return [];
    const entries = new Map<string, { url: string; title: string; searchable: string; bookmarked: boolean; time: number }>();
    for (const entry of libraryState.history) {
      entries.set(entry.url, { url: entry.url, title: entry.title, searchable: `${entry.title}\n${entry.url}`.toLowerCase(), bookmarked: false, time: entry.visitedAt });
    }
    for (const entry of libraryState.bookmarks) {
      const visit = entries.get(entry.url);
      entries.set(entry.url, { url: entry.url, title: entry.title, searchable: `${entry.title}\n${visit?.searchable ?? entry.url}`.toLowerCase(), bookmarked: true, time: visit?.time ?? entry.createdAt });
    }
    const words = query.split(/\s+/);
    const prefix = query.replace(/^https?:\/\/(?:www\.)?/, "");
    // The persisted library is capped by its owner. Rank locally and render a
    // short list; suggestions never fetch remote search results or favicons.
    return [...entries.values()].filter(entry => words.every(word => entry.searchable.includes(word)))
      .map(entry => ({ ...entry, rank: entry.url.toLowerCase().replace(/^https?:\/\/(?:www\.)?/, "").startsWith(prefix) ? 0
        : entry.title.toLowerCase().startsWith(query) ? 1 : 2 }))
      .sort((a, b) => a.rank - b.rank || Number(b.bookmarked) - Number(a.bookmarked) || b.time - a.time)
      .slice(0, 8);
  }, [value, addressFocused, libraryState]);
  const showSuggestions = active && addressFocused && suggestionsOpen && suggestions.length > 0;
  const selectedSuggestion = showSuggestions ? suggestions.findIndex(entry => entry.url === selectedSuggestionURL) : -1;

  useEffect(() => {
    const library = api?.library;
    if (!library || !addressFocused || !active) return;
    let disposed = false;
    let changed = false;
    const unsubscribe = library.onState(next => { changed = true; setLibraryState(next); });
    void library.getState().then(next => {
      if (!disposed && !changed) setLibraryState(next);
    }).catch(() => {
      // Navigation remains usable if the optional local library cannot load.
      if (!disposed && !changed) setLibraryState(undefined);
    });
    return () => { disposed = true; unsubscribe(); };
  }, [api, addressFocused, active]);

  useEffect(() => {
    if (!showSuggestions) return;
    const outside = (event: PointerEvent): void => {
      if (event.target instanceof Node && !formRef.current?.contains(event.target) && !suggestionsRef.current?.contains(event.target)) {
        setSuggestionsOpen(false);
        setSelectedSuggestionURL(undefined);
      }
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [showSuggestions]);

  useEffect(() => {
    if (showSuggestions && selectedSuggestion >= 0) suggestionsRef.current?.children[selectedSuggestion]?.scrollIntoView({ block: "nearest" });
  }, [showSuggestions, selectedSuggestion]);

  useEffect(() => {
    setSuggestionsOpen(false);
    setSelectedSuggestionURL(undefined);
  }, [pageKey, active]);

  const submit = (input: string): void => {
    if (composingAddress.current || !onSubmit(input)) return;
    setSuggestionsOpen(false);
    setSelectedSuggestionURL(undefined);
    inputRef.current?.blur();
  };
  const blurAddress = (): void => {
    setAddressFocused(false); setSuggestionsOpen(false); setSelectedSuggestionURL(undefined); setLibraryState(undefined); composingAddress.current = false;
  };

  return <>
    <form ref={formRef} className={`workspace-browser-url-form web-workspace-address ${className}`} onSubmit={event => {
      event.preventDefault();
      submit(selectedSuggestion >= 0 ? suggestions[selectedSuggestion].url : value);
    }}>
      {leading}
      <input ref={inputRef} className="workspace-browser-url-input" value={value} type="text" inputMode="url" spellCheck={false} autoCorrect="off" autoCapitalize="off" autoComplete="off"
        placeholder={t("webWorkspace.addressSearch")} aria-label={t("webWorkspace.address")} disabled={disabled} aria-invalid={invalid} aria-describedby={invalid ? errorID : undefined}
        role="combobox" aria-autocomplete="list" aria-expanded={showSuggestions} aria-controls={showSuggestions ? suggestionsID : undefined}
        aria-activedescendant={selectedSuggestion >= 0 ? `${suggestionsID}-${selectedSuggestion}` : undefined}
        onChange={event => { onChange(event.target.value); setAddressFocused(true); setSuggestionsOpen(true); setSelectedSuggestionURL(undefined); }}
        onFocus={event => { setAddressFocused(true); event.currentTarget.select(); }}
        onBlur={event => {
          // Moving into another native contents has no DOM relatedTarget.
          // Let the overlay deliver its click; native outside/blur dismissal
          // still closes it when focus instead leaves the address interaction.
          if (!event.relatedTarget && suggestionsRef.current && suggestionsRef.current.ownerDocument !== document) return;
          blurAddress();
        }}
        onCompositionStart={() => { composingAddress.current = true; }} onCompositionEnd={() => { composingAddress.current = false; }}
        onKeyDown={event => {
          if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229 || composingAddress.current) return;
          if ((event.key === "ArrowDown" || event.key === "ArrowUp") && suggestions.length) {
            event.preventDefault();
            const next = selectedSuggestion < 0 ? (event.key === "ArrowDown" ? 0 : suggestions.length - 1)
              : (selectedSuggestion + (event.key === "ArrowDown" ? 1 : -1) + suggestions.length) % suggestions.length;
            setSuggestionsOpen(true);
            setSelectedSuggestionURL(suggestions[next].url);
          } else if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            if (showSuggestions) { setSuggestionsOpen(false); setSelectedSuggestionURL(undefined); return; }
            event.currentTarget.blur();
            onEscape();
          } else if (event.key === "Tab") { setSuggestionsOpen(false); setSelectedSuggestionURL(undefined); }
        }} />
      {children}
    </form>
    {showSuggestions ? <NativeBrowserPopover controls={api} focus={false} onDismiss={blurAddress}><FloatingMenuPortal anchorRef={formRef} owner="web-workspace-address" placement="below" align="left" width={composerMenuWidth(480)} matchAnchorWidth offset={4}>
      <ul ref={suggestionsRef} id={suggestionsID} className="web-workspace-address-suggestions" role="listbox" aria-label={t("webWorkspace.addressSearch")}
        data-web-workspace-address-suggestions="" data-wuu-layer="popover" data-web-workspace-owner={destinationId}>
        {suggestions.map((entry, index) => <li key={entry.url} id={`${suggestionsID}-${index}`} role="option" aria-selected={index === selectedSuggestion}
          className="web-workspace-address-suggestion" data-address-suggestion-url={entry.url} data-address-suggestion-source={entry.bookmarked ? "bookmark" : "history"}
          title={entry.url} onPointerDown={event => { if (event.button === 0) event.preventDefault(); }} onClick={() => submit(entry.url)}>
          {entry.bookmarked ? <Bookmark className="icon" aria-hidden="true" /> : <Clock3 className="icon" aria-hidden="true" />}
          <span className="web-workspace-address-suggestion-text"><span>{entry.title || entry.url}</span><span className="web-workspace-address-suggestion-url">{entry.url}</span></span>
        </li>)}
      </ul>
    </FloatingMenuPortal></NativeBrowserPopover> : null}
  </>;
}
