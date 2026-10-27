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

const refCounts = new WeakMap<Document, number>();

function apply(next: FocusModality, scope: Document): void {
  if (scope.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE) === next) {
    return;
  }
  scope.documentElement.setAttribute(FOCUS_MODALITY_ATTRIBUTE, next);
}

function isTextEntry(target: EventTarget | null): boolean {
  if (!target || !("nodeType" in target) || target.nodeType !== 1) {
    return false;
  }
  const element = target as HTMLElement;
  if (element.isContentEditable) {
    return true;
  }
  if (element.localName === "textarea") {
    return true;
  }
  return element.localName === "input" && !POINTER_ONLY_INPUT_TYPES.has((element as HTMLInputElement).type);
}

function onPointerDown(event: Event): void {
  apply("pointer", event.currentTarget as Document);
}

function onTabKeyDown(event: KeyboardEvent): void {
  // Wait for the input's handler: Tab can insert a completion without moving
  // focus. Preserve the current modality when that handler consumes the key.
  if (event.key === "Tab" && !event.defaultPrevented) {
    apply("keyboard", event.currentTarget as Document);
  }
}

function onKeyDown(event: KeyboardEvent): void {
  if (!KEYBOARD_FOCUS_KEYS.has(event.key) || isTextEntry(event.target)) {
    return;
  }
  apply("keyboard", event.currentTarget as Document);
}

/** Install the document-wide modality listeners. Returns the uninstaller. */
export function startFocusModality(targetDocument?: Document): () => void {
  if (!targetDocument && typeof document === "undefined") return () => {};
  const scope = targetDocument ?? document;
  const count = refCounts.get(scope) ?? 0;
  if (count === 0) {
    apply("pointer", scope);
    scope.addEventListener("pointerdown", onPointerDown, true);
    scope.addEventListener("keydown", onKeyDown, true);
    scope.addEventListener("keydown", onTabKeyDown);
  }
  refCounts.set(scope, count + 1);
  let stopped = false;
  return () => {
    if (stopped) return;
    stopped = true;
    const remaining = (refCounts.get(scope) ?? 1) - 1;
    if (remaining > 0) { refCounts.set(scope, remaining); return; }
    refCounts.delete(scope);
    scope.removeEventListener("pointerdown", onPointerDown, true);
    scope.removeEventListener("keydown", onKeyDown, true);
    scope.removeEventListener("keydown", onTabKeyDown);
  };
}

export function readFocusModality(): FocusModality {
  const value = document.documentElement.getAttribute(FOCUS_MODALITY_ATTRIBUTE);
  return value === "keyboard" ? "keyboard" : "pointer";
}
