import { type RefObject, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useActiveContextMenu } from "./ActiveContextMenu";
import { placeContextMenu, type ContextMenuLayout } from "./ContextMenuPlacement";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export { placeContextMenu } from "./ContextMenuPlacement";

// Right-click edit menu for the composer textarea. Shares the visual
// language of ThreadContextMenu (the conversation row's right-click
// panel): design-token surface, menu-enter animation, 32px flex items.
// Positioning lives in viewport coordinates so the menu stays anchored
// to the cursor even when the textarea itself is scrolled inside a tall
// composer shell.
//
// Placement: the menu prefers to grow down-right from the cursor like a
// native context menu. When a side would clip against the viewport it
// flips to the opposite side of the cursor (so the cursor never lands
// on top of a menu item — the composer sits at the bottom of the
// window, so the flipped-up case is the common one). Clamping to the
// viewport is the fallback for windows too small to fit the menu on
// either side. data-origin carries the anchored corner to CSS so the
// enter animation scales out of the cursor.
//
// Dismissal rules:
//   - Left click outside the menu → close. Right click is intentionally
//     NOT a dismiss signal: the right-click gesture that opened us is the
//     same gesture the textarea uses to reopen us, and dismissing on it
//     would race with reopen. Same rule as WorkspaceTreeContextMenu.
//   - Escape → close.
//   - Any scroll, window resize, or the window losing focus → close.
//     The menu is anchored to a cursor position, not an element; once
//     the content under that position moves there is nothing meaningful
//     to stay anchored to.
//   - Listeners are attached via setTimeout(0) so the burst of pointer
//     events the right-click itself dispatches doesn't immediately
//     dismiss the menu we just opened.
//   - Another context menu opening → close (useActiveContextMenu).

export function ComposerContextMenu({
  textareaRef,
  x,
  y,
  hasSelection,
  disabled = false,
  onClose,
  onValueChange
}: {
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  x: number;
  y: number;
  hasSelection: boolean;
  disabled?: boolean;
  onClose: () => void;
  onValueChange: (next: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const ref = useRef<HTMLDivElement | null>(null);
  useActiveContextMenu(onClose);
  // The menu mounts at the cursor, but until React commits the first
  // paint its own size isn't known — measure on the layout effect that
  // runs just before paint and place relative to the viewport, so the
  // user never sees an unpositioned frame. The layout is recomputed
  // unconditionally on every x/y change: the component stays mounted
  // when a second right-click re-anchors it, so the placement must
  // always be derived from the latest props, never from the mount-time
  // coordinates. offsetWidth/offsetHeight are used instead of
  // getBoundingClientRect because the enter animation's first frame
  // applies a scale/translate transform that would skew the measurement.
  const [layout, setLayout] = useState<ContextMenuLayout | null>(null);
  useLayoutEffect(() => {
    const menuElement = ref.current;
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
        window.innerHeight
      )
    );
  }, [x, y]);

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (event.button !== 0) {
        return;
      }
      const menuElement = ref.current;
      if (!menuElement) {
        return;
      }
      if (event.target instanceof Node && menuElement.contains(event.target)) {
        return;
      }
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    // The menu never scrolls internally (it is always placed fully
    // visible via flip + clamp), so any scroll means the content under
    // the cursor anchor moved — close unconditionally.
    const handleScroll = () => {
      onClose();
    };
    let active = true;
    const id = window.setTimeout(() => {
      if (!active) {
        return;
      }
      document.addEventListener("pointerdown", handlePointerDown);
      document.addEventListener("keydown", handleKeyDown);
      document.addEventListener("scroll", handleScroll, true);
      window.addEventListener("resize", onClose);
      window.addEventListener("blur", onClose);
    }, 0);
    return () => {
      active = false;
      window.clearTimeout(id);
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("scroll", handleScroll, true);
      window.removeEventListener("resize", onClose);
      window.removeEventListener("blur", onClose);
    };
  }, [onClose]);

  // Read the current selection and value at action time, not at render
  // time — the user can open the menu, then change their selection with
  // the keyboard before clicking an item, and the action should reflect
  // the live state of the textarea. Synchronous action handlers also
  // guarantee the menu closes before React re-renders, so we never
  // observe the textarea mid-update.
  const readSelection = (): {
    textarea: HTMLTextAreaElement;
    start: number;
    end: number;
    value: string;
  } | null => {
    const textarea = textareaRef.current;
    if (!textarea) {
      return null;
    }
    return {
      textarea,
      start: textarea.selectionStart,
      end: textarea.selectionEnd,
      value: textarea.value
    };
  };

  const runCut = (): void => {
    if (disabled) {
      return;
    }
    const selection = readSelection();
    if (!selection || selection.start === selection.end) {
      onClose();
      return;
    }
    const { textarea, start, end, value } = selection;
    const selectedText = value.slice(start, end);
    const nextValue = value.slice(0, start) + value.slice(end);
    onValueChange(nextValue);
    // Best-effort clipboard write; failure (e.g. permission denied) should
    // not block the cut. The text has already been removed from the
    // textarea, and re-inserting it on clipboard failure would be more
    // surprising than simply having the cut take effect.
    void navigator.clipboard.writeText(selectedText);
    // Place the caret at the cut boundary after React re-renders the
    // textarea with the new value.
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start, start);
    });
    onClose();
  };

  const runCopy = (): void => {
    if (disabled) {
      return;
    }
    const selection = readSelection();
    if (!selection || selection.start === selection.end) {
      onClose();
      return;
    }
    const { textarea, start, end, value } = selection;
    const selectedText = value.slice(start, end);
    void navigator.clipboard.writeText(selectedText);
    // Restore focus so the user can keep editing from the same selection
    // after the menu closes.
    textarea.focus();
    onClose();
  };

  const runPaste = async (): Promise<void> => {
    if (disabled) {
      return;
    }
    const selection = readSelection();
    if (!selection) {
      onClose();
      return;
    }
    const { textarea, start, end, value } = selection;
    let clipboardText = "";
    try {
      clipboardText = await navigator.clipboard.readText();
    } catch {
      // Permission denied or no clipboard access — fail closed without
      // touching the textarea.
      onClose();
      return;
    }
    if (clipboardText.length === 0) {
      onClose();
      return;
    }
    const nextValue = value.slice(0, start) + clipboardText + value.slice(end);
    onValueChange(nextValue);
    const caret = start + clipboardText.length;
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(caret, caret);
    });
    onClose();
  };

  const runSelectAll = (): void => {
    if (disabled) {
      return;
    }
    const textarea = textareaRef.current;
    if (!textarea) {
      onClose();
      return;
    }
    textarea.focus();
    textarea.select();
    onClose();
  };

  const runDelete = (): void => {
    if (disabled) {
      return;
    }
    const selection = readSelection();
    if (!selection || selection.start === selection.end) {
      onClose();
      return;
    }
    const { textarea, start, end, value } = selection;
    const nextValue = value.slice(0, start) + value.slice(end);
    onValueChange(nextValue);
    requestAnimationFrame(() => {
      textarea.focus();
      textarea.setSelectionRange(start, start);
    });
    onClose();
  };

  // Cut/Copy/Delete need an active selection to do anything meaningful;
  // grey them out when there isn't one. Copy and 全选 are always allowed so
  // users can still extract text from a read-only composer — only the
  // mutating actions (cut/paste/delete) are blocked when the textarea is
  // disabled.
  const canMutate = hasSelection && !disabled;
  const canCopy = hasSelection;

  const items: Array<{ label: string; run: () => void; itemDisabled: boolean }> = [
    { label: t("contextMenu.cut"), run: runCut, itemDisabled: !canMutate },
    { label: t("contextMenu.copy"), run: runCopy, itemDisabled: !canCopy },
    {
      label: t("contextMenu.paste"),
      run: () => {
        void runPaste();
      },
      itemDisabled: disabled
    },
    { label: t("contextMenu.selectAll"), run: runSelectAll, itemDisabled: false },
    { label: t("contextMenu.delete"), run: runDelete, itemDisabled: !canMutate }
  ];

  return (
    <UILayerPortal layer="menu">
      <div
        ref={ref}
        className="composer-textarea-context-menu"
        data-wuu-component="menu"
        data-wuu-layer="menu"
        data-wuu-state="open"
        role="menu"
        data-origin={layout?.origin ?? "top-left"}
        style={
          layout
            ? { left: layout.left, top: layout.top }
            : // First render: not yet measured. Park at the cursor but
              // invisible — the layout effect above replaces this before
              // the browser ever paints.
              { left: x, top: y, visibility: "hidden" }
        }
        onContextMenu={(event) => event.preventDefault()}
      >
        {items.map((item) => (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            className="composer-textarea-context-menu-item"
            onClick={item.run}
            disabled={item.itemDisabled}
          >
            <span>{item.label}</span>
          </button>
        ))}
      </div>
    </UILayerPortal>
  );
}
