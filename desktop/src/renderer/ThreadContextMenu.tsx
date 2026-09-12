import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { placeContextMenu, type ContextMenuLayout } from "./ContextMenuPlacement";
import { UILayerPortal } from "./ui/layers/UILayerHost";

/**
 * ThreadContextMenu is a lightweight right-click menu for thread rows in the
 * session tree. Items trigger an action (typically a clipboard write) and the
 * menu auto-closes on selection, Escape, or an outside click.
 *
 * The menu is positioned via fixed coordinates so callers can pass raw
 * clientX/clientY from a contextmenu event without computing offsets against
 * any parent container.
 */
export type ThreadContextMenuItem =
  | {
      /** Display label. */
      label: string;
      /**
       * Action to perform when the item is selected. The promise is intentionally
       * not awaited so the menu closes immediately even if the action is slow
       * (e.g. a clipboard write with feedback animation).
       */
      onSelect: () => void | Promise<void>;
      /** When true, the item is rendered disabled and clicks are ignored. */
      disabled?: boolean;
      icon?: ReactNode;
      danger?: boolean;
    }
  | {
      /** Render a horizontal divider between items. */
      separator: true;
    };

export function ThreadContextMenu({
  x,
  y,
  items,
  onClose
}: {
  x: number;
  y: number;
  items: ThreadContextMenuItem[];
  onClose: () => void;
}): JSX.Element {
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [layout, setLayout] = useState<ContextMenuLayout | null>(null);

  useLayoutEffect(() => {
    const menuElement = menuRef.current;
    if (!menuElement) {
      return;
    }
    setLayout(
      placeContextMenu(
        x,
        y,
        menuElement.offsetWidth,
        menuElement.offsetHeight,
        window.innerWidth,
        window.innerHeight,
      ),
    );
  }, [x, y, items]);

  useEffect(() => {
    function handleMouseDown(event: MouseEvent): void {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        onClose();
      }
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        onClose();
      }
    }
    document.addEventListener("mousedown", handleMouseDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleMouseDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [onClose]);

  return (
    <UILayerPortal layer="menu">
      <div
        ref={menuRef}
        role="menu"
        className="thread-row-context-menu"
        data-wuu-component="menu"
        data-wuu-layer="menu"
        data-wuu-state="open"
        data-origin={layout?.origin ?? "top-left"}
        style={
          layout
            ? { left: layout.left, top: layout.top }
            : { left: x, top: y, visibility: "hidden" }
        }
        data-testid="thread-row-context-menu"
      >
        {items.map((item, idx) => {
          if ("separator" in item) {
            return (
              <div
                key={`sep-${idx}`}
                role="separator"
                className="thread-row-context-menu-separator"
              />
            );
          }
          return (
            <button
              key={`item-${idx}-${item.label}`}
              role="menuitem"
              type="button"
              className="thread-row-context-menu-item"
              disabled={item.disabled}
              style={item.danger ? { color: "var(--danger)" } : undefined}
              onClick={() => {
                if (item.disabled) return;
                void item.onSelect();
                onClose();
              }}
            >
              {item.icon}
              <span>{item.label}</span>
            </button>
          );
        })}
      </div>
    </UILayerPortal>
  );
}

/**
 * copyToClipboard writes text to the system clipboard and returns whether the
 * write succeeded. The error is intentionally swallowed because clipboard
 * failures (denied permission, focus loss) are not actionable in this UI;
 * the caller can show its own "failed" state if it tracks copy feedback.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (text.trim() === "") {
    return false;
  }
  try {
    const clipboard = navigator.clipboard;
    if (!clipboard?.writeText) {
      return false;
    }
    await clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
