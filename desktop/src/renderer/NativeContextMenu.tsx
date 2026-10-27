import { useEffect, useRef } from "react";
import type { ApplicationDesktopContextMenuV1, ApplicationDesktopMenuItemV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { useActiveContextMenu } from "./ActiveContextMenu";

export type NativeContextMenuItem = ApplicationDesktopMenuItemV1 & { onSelect?: () => void | Promise<void> };

/** Keeps callbacks in the owning UI generation; only menu data crosses IPC. */
export function NativeContextMenu({ api, x, y, items, onClose }: {
  api: ApplicationDesktopContextMenuV1;
  x?: number;
  y?: number;
  items: NativeContextMenuItem[];
  onClose: () => void;
}): null {
  useActiveContextMenu(onClose);
  const latest = useRef({ items, onClose });
  latest.current = { items, onClose };
  const model = JSON.stringify(items);
  useEffect(() => {
    const token = crypto.randomUUID();
    const actions = latest.current.items;
    let active = true;
    // Deferring also lets React's strict-effect cleanup cancel a discarded mount.
    const timer = window.setTimeout(() => {
      void api.show({ token, x, y, items: JSON.parse(model) }).then(id => {
        if (!active) return;
        latest.current.onClose();
        const action = actions.find(item => !("separator" in item) && item.id === id);
        if (action && !("separator" in action) && !action.disabled) void action.onSelect?.();
      }, error => {
        if (!active) return;
        console.error("[desktop] context menu failed", error);
        latest.current.onClose();
      });
    }, 0);
    return () => {
      active = false;
      window.clearTimeout(timer);
      void api.dismiss(token).catch(error => console.error("[desktop] context menu cleanup failed", error));
    };
  }, [api, x, y, model]);
  return null;
}
