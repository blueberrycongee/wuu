import { useSyncExternalStore } from "react";

import { useApplicationWorkbench } from "./plugins/ApplicationWorkbenchContext";
import type { ApplicationWorkbenchV1 } from "../../../packages/plugin-sdk/src/ui-contributions";
import { visibleWorkbenchView } from "./plugins/Workbench";

/** True while a primary plugin page is portaled over the conversation pane. */
export function usePrimaryPluginViewCover(
  suppliedController?: ApplicationWorkbenchV1,
): boolean {
  const controller = useApplicationWorkbench(suppliedController);
  const snapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  return visibleWorkbenchView(snapshot, "primary") !== undefined;
}
