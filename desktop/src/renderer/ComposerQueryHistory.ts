import type { ComposerInputHandle } from "./ComposerInput";
import {
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
  useEffect,
  useRef
} from "react";

function textareaSelectionAtStart(textarea: ComposerInputHandle): boolean {
  return textarea.selectionStart === 0 && textarea.selectionEnd === 0;
}

function textareaSelectionAtEnd(textarea: ComposerInputHandle): boolean {
  const end = textarea.value.length;
  return textarea.selectionStart === end && textarea.selectionEnd === end;
}

export function useComposerQueryHistory({
  disabled,
  prompt,
  queryHistory = [],
  queryHistorySessionID,
  setPrompt,
  textareaRef
}: {
  disabled: boolean;
  prompt: string;
  queryHistory?: string[];
  queryHistorySessionID?: string;
  setPrompt: (value: string) => void;
  textareaRef: RefObject<ComposerInputHandle | null>;
}): {
  resetQueryHistoryNavigation: () => void;
  handleQueryHistoryKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => boolean;
} {
  const draftRef = useRef("");
  const indexRef = useRef<number | null>(null);

  function resetQueryHistoryNavigation(): void {
    draftRef.current = "";
    indexRef.current = null;
  }

  function setPromptFromHistory(value: string): void {
    setPrompt(value);
    window.requestAnimationFrame(() => {
      const textarea = textareaRef.current;
      if (!textarea) {
        return;
      }
      textarea.focus();
      textarea.setSelectionRange(value.length, value.length);
    });
  }

  function navigateQueryHistory(direction: -1 | 1): boolean {
    if (queryHistory.length === 0) {
      return false;
    }
    const currentIndex = indexRef.current;
    if (direction === -1) {
      const nextIndex = currentIndex === null ? queryHistory.length - 1 : Math.max(0, currentIndex - 1);
      if (currentIndex === null) {
        draftRef.current = prompt;
      }
      indexRef.current = nextIndex;
      setPromptFromHistory(queryHistory[nextIndex]);
      return true;
    }
    if (currentIndex === null) {
      return false;
    }
    if (currentIndex >= queryHistory.length - 1) {
      const draft = draftRef.current;
      resetQueryHistoryNavigation();
      setPromptFromHistory(draft);
      return true;
    }
    const nextIndex = currentIndex + 1;
    indexRef.current = nextIndex;
    setPromptFromHistory(queryHistory[nextIndex]);
    return true;
  }

  function handleQueryHistoryKeyDown(event: ReactKeyboardEvent<HTMLElement>): boolean {
    if (!textareaRef.current || disabled || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return false;
    }
    if (event.key === "ArrowUp") {
      if (indexRef.current === null && !textareaSelectionAtStart(textareaRef.current!)) {
        return false;
      }
      if (navigateQueryHistory(-1)) {
        event.preventDefault();
        return true;
      }
    }
    if (event.key === "ArrowDown") {
      if (indexRef.current === null || !textareaSelectionAtEnd(textareaRef.current!)) {
        return false;
      }
      if (navigateQueryHistory(1)) {
        event.preventDefault();
        return true;
      }
    }
    return false;
  }

  useEffect(() => {
    resetQueryHistoryNavigation();
  }, [queryHistorySessionID]);

  useEffect(() => {
    if (prompt.length === 0) {
      resetQueryHistoryNavigation();
    }
  }, [prompt]);

  return { resetQueryHistoryNavigation, handleQueryHistoryKeyDown };
}
