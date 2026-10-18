import { useSyncExternalStore } from "react";

import { desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { visibleWorkbenchView, type WorkbenchController } from "./plugins/Workbench";
import type { RegisteredViewType } from "./plugins/PluginHost";

export function usePrimaryPluginViewCover(
  controller: WorkbenchController = desktopWorkbenchController,
): boolean {
  return usePrimaryPluginViewDefinition(controller) !== undefined;
}

/** The visible primary plugin page owns its presentation metadata, never runtime authority. */
export function usePrimaryPluginViewDefinition(
  controller: WorkbenchController = desktopWorkbenchController,
): RegisteredViewType | undefined {
  const snapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  return visibleWorkbenchView(snapshot, "primary")?.definition;
}
