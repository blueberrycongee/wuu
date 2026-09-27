/*
 * Focus modality — the single owner of `data-focus-modality` on <html>.
 *
 * Chromium treats a mouse click into a text field as `:focus-visible`, so a
 * stylesheet that keys rings off that pseudo-class still paints a harsh frame
 * on ordinary pointing. The attribute tells CSS whether the last focus-moving
 * input was a pointer or the keyboard (Tab and other navigation keys).
 *
 * `startFocusModality()` is ref-counted: product entry points, previews that
 * mount WuuUIRoot, and tests can start it independently without stacking
 * duplicate listeners for the lifetime of the document.
 */

export const FOCUS_MODALITY_ATTRIBUTE = "data-focus-modality";

export type FocusModality = "keyboard" | "pointer";

const KEYBOARD_FOCUS_KEYS = new Set([
  "Tab",
  "ArrowUp",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "Home",
  "End",
  "PageUp",
  "PageDown",
  "Escape",
]);

const POINTER_ONLY_INPUT_TYPES = new Set([
  "button",
  "checkbox",
  "color",
  "file",
  "hidden",
  "image",
  "radio",
  "range",
  "reset",
  "submit",
]);

let refCount = 0;

function apply(next: FocusModality): void {
  if (document.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE) === next) {
    return;
  }
  document.documentElement.setAttribute(FOCUS_MODALITY_ATTRIBUTE, next);
}

function isTextEntry(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.isContentEditable) {
    return true;
  }
  if (target instanceof HTMLTextAreaElement) {
    return true;
  }
  return target instanceof HTMLInputElement && !POINTER_ONLY_INPUT_TYPES.has(target.type);
}

function onPointerDown(): void {
  apply("pointer");
}

function onKeyDown(event: KeyboardEvent): void {
  if (event.key === "Tab") {
    apply("keyboard");
    return;
  }
  if (!KEYBOARD_FOCUS_KEYS.has(event.key) || isTextEntry(event.target)) {
    return;
  }
  apply("keyboard");
}

/** Install the document-wide modality listeners. Returns the uninstaller. */
export function startFocusModality(): () => void {
  if (typeof document === "undefined") {
    return () => {};
  }
  if (refCount === 0) {
    apply("pointer");
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown, true);
  }
  refCount += 1;
  return () => {
    refCount = Math.max(0, refCount - 1);
    if (refCount > 0) {
      return;
    }
    document.removeEventListener("pointerdown", onPointerDown, true);
    document.removeEventListener("keydown", onKeyDown, true);
  };
}

export function readFocusModality(): FocusModality {
  const value = document.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE);
  return value === "keyboard" ? "keyboard" : "pointer";
}
