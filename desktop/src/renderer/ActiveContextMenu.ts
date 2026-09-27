import { useLayoutEffect, useRef } from "react";

/**
 * At most one cursor-anchored context menu is open at a time, whichever
 * component renders it. Each caller owns its own open state, so a second
 * right-click would otherwise mount another menu on top of the first; enter
 * animations on those stacked copies read as ghosted cards. Registering a
 * menu therefore dismisses every other one before the browser paints.
 *
 * Native context menus are modal for hover, so hover hints consult this
 * registry too: they stay closed while any context menu is open.
 */
const openContextMenus = new Set<() => void>();
const openListeners = new Set<() => void>();

export function useActiveContextMenu(onClose: () => void): void {
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useLayoutEffect(() => {
    const close = (): void => {
      onCloseRef.current();
    };
    const others = [...openContextMenus];
    openContextMenus.clear();
    openContextMenus.add(close);
    for (const other of others) {
      other();
    }
    for (const listener of [...openListeners]) {
      listener();
    }
    return () => {
      openContextMenus.delete(close);
    };
  }, []);
}

export function hasActiveContextMenu(): boolean {
  return openContextMenus.size > 0;
}

/** Calls `listener` whenever a context menu opens. Returns the unsubscribe. */
export function onContextMenuOpen(listener: () => void): () => void {
  openListeners.add(listener);
  return () => {
    openListeners.delete(listener);
  };
}
