import { useSyncExternalStore } from "react";
import { useApplicationWorkbench } from "./plugins/ApplicationWorkbenchContext";
import type { ApplicationWorkbenchV1 } from "../../../packages/plugin-sdk/src/ui-contributions";
import { visibleWorkbenchView } from "./plugins/Workbench";

export function usePrimaryPluginViewCover(controller?: ApplicationWorkbenchV1): boolean {
  return usePrimaryPluginViewDefinition(controller) !== undefined;
}

/** Presentation metadata belongs to the selected workbench, never runtime authority. */
export function usePrimaryPluginViewDefinition(suppliedController?: ApplicationWorkbenchV1) {
  const controller = useApplicationWorkbench(suppliedController);
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  return visibleWorkbenchView(snapshot, "primary")?.definition;
}
