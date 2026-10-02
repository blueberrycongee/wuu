import type { KeyboardEvent as ReactKeyboardEvent } from "react";

const MENU_KEYS = new Set(["ArrowDown", "ArrowUp", "Home", "End"]);

/**
 * Moves focus across a vertical menu's enabled items for the arrow, Home and
 * End keys, entering from either end when focus is still on the trigger.
 * Returns whether the key was one of them; dismissal stays with the caller,
 * which knows where focus returns.
 */
export function moveMenuFocus(event: ReactKeyboardEvent, menu: HTMLElement | null): boolean {
  if (!MENU_KEYS.has(event.key)) return false;
  event.preventDefault();
  const items = Array.from(
    menu?.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)') ?? [],
  );
  if (items.length === 0) return true;
  const index = items.indexOf(document.activeElement as HTMLElement);
  const step = event.key === "ArrowDown" ? 1 : -1;
  const next = event.key === "Home"
    ? 0
    : event.key === "End"
      ? items.length - 1
      : index < 0
        ? (step > 0 ? 0 : items.length - 1)
        : (index + step + items.length) % items.length;
  items[next].focus();
  return true;
}
