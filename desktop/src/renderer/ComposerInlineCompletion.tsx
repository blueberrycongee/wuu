import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type RefObject,
} from "react";
import type { ComposerInlineCompletionContext } from "../shared/workbench";
import type { PluginHost, RegisteredComposerInlineCompletionProvider } from "./plugins/PluginHost";
import { parseComposerSlashDraft } from "./ComposerSlashCommands";

export interface ComposerInlineCompletionActions {
  accept(): boolean;
  dismiss(): boolean;
}

type Observation = {
  key: string;
  providers: readonly RegisteredComposerInlineCompletionProvider[];
  context: ComposerInlineCompletionContext;
  eligible: boolean;
};
type Suggestion = {
  observation: Observation;
  provider: RegisteredComposerInlineCompletionProvider;
  text: string;
};

// This lives beside the synchronous textarea value. App's persisted draft and
// the surrounding composer chrome may still describe an earlier keystroke.
export function useComposerInlineCompletion({
  host, inputRef, value, enabled, kind, contextKey, revisionKey, threadId, sideThreadId, running, compositionActive, onInsert,
}: {
  host: PluginHost;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  value: string;
  enabled: boolean;
  kind: ComposerInlineCompletionContext["kind"];
  contextKey: string;
  revisionKey: string;
  threadId?: string;
  sideThreadId?: string;
  running: boolean;
  compositionActive: boolean;
  onInsert: (value: string) => void;
}) {
  const mountId = useId();
  const identity = useRef({ contextKey, kind, epoch: 0 });
  if (identity.current.contextKey !== contextKey || identity.current.kind !== kind) {
    identity.current = { contextKey, kind, epoch: identity.current.epoch + 1 };
  }
  const composerId = `${mountId}:${identity.current.epoch}`;
  const subscribe = useCallback((listener: () => void) => host.subscribeComposerInlineCompletionProviders(listener), [host]);
  const getProviders = useCallback(() => host.getComposerInlineCompletionProviders(), [host]);
  const providers = useSyncExternalStore(subscribe, getProviders, getProviders);
  const composing = useRef(compositionActive);
  composing.current = compositionActive;
  const windowFocused = useRef(true);
  const revision = useRef(0);
  const observed = useRef<Observation | null>(null);
  const request = useRef<AbortController | null>(null);
  const dismissed = useRef<Observation | null>(null);
  const [observation, setObservation] = useState<Observation | null>(null);
  const [suggestion, setSuggestion] = useState<Suggestion | null>(null);

  function observe(composition?: boolean): Observation | null {
    if (composition !== undefined) composing.current = composition;
    const input = inputRef.current;
    if (!input || (providers.length === 0 && observed.current === null)) return null;
    const focused = document.activeElement === input;
    const menuOpen = Boolean(document.querySelector(
      '[data-floating-menu-owner], .composer-textarea-context-menu, [aria-modal="true"]',
    ));
    const eligible = enabled && providers.length > 0 && focused && !input.disabled && !input.readOnly
      && windowFocused.current && !composing.current && !menuOpen && !input.closest("[hidden], [inert]")
      && input.selectionStart === input.value.length && input.selectionEnd === input.value.length
      && !parseComposerSlashDraft(input.value);
    const key = JSON.stringify([composerId, kind, revisionKey, threadId, sideThreadId, running, input.value,
      input.selectionStart, input.selectionEnd, focused, windowFocused.current, composing.current, eligible]);
    const previous = observed.current;
    if (previous?.key === key && previous.providers === providers) return previous;
    request.current?.abort();
    const next: Observation = {
      key, providers, eligible,
      context: Object.freeze({ contractVersion: 1, kind, composerId, revision: ++revision.current,
        ...(threadId === undefined ? {} : { threadId }),
        ...(sideThreadId === undefined ? {} : { sideThreadId }), running, draftText: input.value,
        selectionStart: input.selectionStart, selectionEnd: input.selectionEnd }),
    };
    observed.current = next;
    setSuggestion(null);
    setObservation(next);
    return next;
  }
  const observeRef = useRef(observe);
  observeRef.current = observe;
  // Includes external same-text resets and running/turn transitions. Do this
  // before paint so a stale suggestion cannot flash over a restored draft.
  useLayoutEffect(() => { observe(); });
  useEffect(() => {
    if (providers.length === 0) return;
    const input = inputRef.current;
    if (!input) return;
    const refresh = () => { observeRef.current(); };
    const blurWindow = () => { windowFocused.current = false; refresh(); };
    const focusWindow = () => { windowFocused.current = true; refresh(); };
    document.addEventListener("selectionchange", refresh);
    window.addEventListener("blur", blurWindow);
    window.addEventListener("focus", focusWindow);
    // Menus may be owned by toolbar plugins rather than Composer props.
    const layer = document.querySelector("[data-wuu-layer-host]");
    const menus = layer ? new MutationObserver(refresh) : null;
    if (layer) menus?.observe(layer, { childList: true, subtree: true });
    return () => {
      document.removeEventListener("selectionchange", refresh);
      window.removeEventListener("blur", blurWindow);
      window.removeEventListener("focus", focusWindow);
      menus?.disconnect();
    };
  }, [inputRef, providers.length]);
  useEffect(() => {
    if (!observation?.eligible || dismissed.current === observation) return;
    const controller = new AbortController();
    request.current = controller;
    void (async () => {
      for (const provider of observation.providers) {
        const result = await provider.provideCompletion(observation.context, controller.signal);
        if (controller.signal.aborted || observed.current !== observation) return;
        if (!result?.text) continue;
        // Textareas normalize line endings; preview and accepted text must agree.
        const text = result.text.replace(/\r\n?/g, "\n");
        setSuggestion({ observation, provider, text });
        return;
      }
    })().finally(() => {
      if (request.current === controller) request.current = null;
    });
    return () => controller.abort();
  }, [observation]);
  useEffect(() => () => request.current?.abort(), []);

  function currentSuggestion(): Suggestion | null {
    const current = observe();
    return suggestion && current === suggestion.observation && current.eligible
      && dismissed.current !== current && host.getComposerInlineCompletionProviders().includes(suggestion.provider)
      ? suggestion : null;
  }
  const actions: ComposerInlineCompletionActions = {
    accept() {
      const current = currentSuggestion();
      const input = inputRef.current;
      if (!current || !input) return false;
      dismissed.current = current.observation;
      request.current?.abort();
      setSuggestion(null);
      // Chromium's native insertion keeps the accepted completion in its undo
      // history. Other hosts fall back to the standard textarea edit operation.
      const inserted = document.execCommand?.("insertText", false, current.text);
      if (!inserted) {
        input.setRangeText(current.text, input.selectionStart, input.selectionEnd, "end");
      }
      if (observed.current?.context.draftText !== input.value) onInsert(input.value);
      observe();
      return true;
    },
    dismiss() {
      const current = currentSuggestion();
      const active = observed.current;
      if (!current && !(active?.eligible && request.current && !request.current.signal.aborted)) return false;
      dismissed.current = active;
      request.current?.abort();
      setSuggestion(null);
      return true;
    },
  };
  const visible = suggestion && suggestion.observation === observation && observation.eligible
    && observation.context.draftText === value && dismissed.current !== observation
    && providers.includes(suggestion.provider) ? suggestion.text : "";
  return { text: visible, observe, actions, descriptionId: `${mountId}-inline-completion` };
}

// A sibling overlay preserves the native textarea's dimensions, caret, IME,
// selection, and hit targets. Computed styles cover document-grid, hero, large
// type and mobile layouts without maintaining a second typography policy.
export function ComposerInlineCompletionOverlay({ inputRef, value, text }: {
  inputRef: RefObject<HTMLTextAreaElement | null>;
  value: string;
  text: string;
}): JSX.Element {
  const viewportRef = useRef<HTMLDivElement>(null);
  const mirrorRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const input = inputRef.current;
    const viewport = viewportRef.current;
    const mirror = mirrorRef.current;
    if (!input || !viewport || !mirror) return;
    function align() {
      const style = getComputedStyle(input!);
      viewport!.style.left = `${input!.offsetLeft + input!.clientLeft}px`;
      viewport!.style.top = `${input!.offsetTop + input!.clientTop}px`;
      viewport!.style.width = `${input!.clientWidth}px`;
      viewport!.style.height = `${input!.clientHeight}px`;
      for (const property of ["font-family", "font-size", "font-weight", "font-style", "font-stretch",
        "font-variant", "font-feature-settings", "font-variation-settings", "font-optical-sizing",
        "font-kerning", "font-synthesis", "line-height", "letter-spacing",
        "word-spacing", "text-align", "text-indent", "text-transform", "direction", "unicode-bidi", "tab-size",
        "padding-top", "padding-right", "padding-bottom", "padding-left", "word-break", "overflow-wrap"]) {
        mirror!.style.setProperty(property, style.getPropertyValue(property));
      }
      mirror!.style.width = `${input!.clientWidth}px`;
      mirror!.style.transform = `translate(${-input!.scrollLeft}px, ${-input!.scrollTop}px)`;
    }
    align();
    const observer = new ResizeObserver(align);
    observer.observe(input);
    const preferences = new MutationObserver(align);
    preferences.observe(document.documentElement, { attributes: true });
    preferences.observe(document.body, { attributes: true });
    input.addEventListener("scroll", align);
    document.fonts?.addEventListener("loadingdone", align);
    return () => {
      observer.disconnect();
      preferences.disconnect();
      input.removeEventListener("scroll", align);
      document.fonts?.removeEventListener("loadingdone", align);
    };
  }, [inputRef, value, text]);
  return <div ref={viewportRef} className="composer-inline-completion" data-wuu-component="composer-inline-completion" aria-hidden="true">
    <div ref={mirrorRef} className="composer-inline-completion-mirror">
      <span className="composer-inline-completion-prefix">{value}</span><span className="composer-inline-completion-text" data-wuu-component="composer-inline-completion-text">{text}</span>
    </div>
  </div>;
}
