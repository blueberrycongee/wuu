import type { ComposerInputHandle } from "./ComposerInput";

// Shared touch/mobile focus policy for the composer textarea.
//
// Mobile browsers can require a user gesture to open the software keyboard.
// Deferring focus into a requestAnimationFrame can lose that gesture and leave
// a blinking caret with no keyboard. Desktop keeps the post-layout rAF so
// expand/collapse ordering and draft-commit timing stay unchanged.

export function isTouchWebShell(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  if (document.documentElement.dataset.hostKind !== "web") {
    return false;
  }
  return window.matchMedia("(pointer: coarse)").matches;
}

/**
 * Focus a composer textarea from a user gesture.
 *
 * On the touch web shell, focus is synchronous (inside the gesture) so the
 * keyboard can open, and only the selection is deferred until React commits
 * the controlled value. On desktop the whole call stays on a rAF so existing
 * layout/commit behavior is preserved.
 */
export function focusComposerTextarea(
  textarea: ComposerInputHandle | null,
  selection?: number | "end",
): void {
  if (!textarea) {
    return;
  }
  if (isTouchWebShell()) {
    textarea.focus({ preventScroll: true });
    if (selection !== undefined) {
      window.requestAnimationFrame(() => {
        const end = selection === "end" ? textarea.value.length : selection;
        textarea.setSelectionRange(end, end);
      });
    }
    return;
  }
  window.requestAnimationFrame(() => {
    textarea.focus();
    if (selection !== undefined) {
      const end = selection === "end" ? textarea.value.length : selection;
      textarea.setSelectionRange(end, end);
    }
  });
}
