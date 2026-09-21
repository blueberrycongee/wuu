import { useSyncExternalStore } from "react";

import { desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { visibleWorkbenchView, type WorkbenchController } from "./plugins/Workbench";

/** True while a primary plugin page is portaled over the conversation pane. */
export function usePrimaryPluginViewCover(
  controller: WorkbenchController = desktopWorkbenchController,
): boolean {
  const snapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  return visibleWorkbenchView(snapshot, "primary") !== undefined;
}
