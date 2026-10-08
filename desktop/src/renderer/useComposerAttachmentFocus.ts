import { useLayoutEffect, useRef, type RefObject, type MouseEvent } from "react";

/** Return from a native picker to its draft, unless navigation or new focus supersedes it. */
export function useComposerAttachmentFocus(
  textareaRef: RefObject<HTMLTextAreaElement | null>,
  sessionID: string | undefined,
  interactionOwner: string | undefined,
  disabled: boolean,
): (event: MouseEvent<HTMLInputElement>) => void {
  const pending = useRef<{ cancel: () => void } | null>(null);

  useLayoutEffect(() => () => pending.current?.cancel(), [sessionID, interactionOwner, disabled]);

  function onClick(event: MouseEvent<HTMLInputElement>): void {
    pending.current?.cancel();
    const textarea = textareaRef.current;
    if (!textarea || disabled) return;
    const input = event.currentTarget;
    const opener = document.activeElement;
    const { selectionStart, selectionEnd, selectionDirection } = textarea;
    let frame: number | undefined;
    const cancel = (): void => {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      document.removeEventListener("focusin", onFocus);
      document.removeEventListener("pointerdown", cancel, true);
      input.removeEventListener("change", restore);
      input.removeEventListener("cancel", restore);
      pending.current = null;
    };
    const onFocus = (event: FocusEvent): void => {
      // Native dialogs may return focus to their input or opening control.
      // Any other focus choice, even one that later blurs, belongs to the user.
      if (event.target !== input && event.target !== opener
        && event.target !== document.body && event.target !== document.documentElement) cancel();
    };
    const restore = (): void => {
      if (frame !== undefined) return;
      // Let the closing menu and native picker finish their focus changes.
      frame = window.requestAnimationFrame(() => {
        const active = document.activeElement;
        cancel();
        if (textareaRef.current !== textarea || !textarea.isConnected || textarea.disabled
          || textarea.closest("[hidden], [inert]")
          || (active !== document.body && active !== document.documentElement
            && active !== input && active !== opener)) return;
        textarea.focus({ preventScroll: true });
        textarea.setSelectionRange(selectionStart, selectionEnd, selectionDirection);
      });
    };
    document.addEventListener("focusin", onFocus);
    document.addEventListener("pointerdown", cancel, true);
    input.addEventListener("change", restore);
    input.addEventListener("cancel", restore);
    pending.current = { cancel };
  }

  return onClick;
}
