import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { VIEW_SWITCH_LOADING_DELAY_MS, type PendingViewSwitch } from "../ViewSwitchState";
import { showErrorToast } from "../Toast";

/** Navigation ownership is public; the shell owns only its delayed loading cover. */
export function useApplicationNavigationState(application: ApplicationServiceV1) {
  const tabs = useSyncExternalStore(application.subscribe, () => application.getSnapshot().tabs);
  const pending = tabs.pending;
  const [visibleRequest, setVisibleRequest] = useState<number>();
  useEffect(() => {
    if (!pending || pending.kind === "thread") return;
    const timer = window.setTimeout(() => setVisibleRequest(pending.requestId), VIEW_SWITCH_LOADING_DELAY_MS);
    return () => window.clearTimeout(timer);
  }, [pending?.requestId, pending?.kind]);
  const pendingViewSwitch: PendingViewSwitch | undefined = pending && {
    kind: pending.kind === "tab" ? "thread" : pending.kind,
    targetID: pending.targetId,
    visible: pending.kind === "thread" || visibleRequest === pending.requestId,
    contextSwitching: pending.contextSwitching,
    restoreRequestID: pending.contentReady ? pending.requestId : undefined,
  };
  const finishViewSwitch = useCallback((requestId: number): boolean => {
    if (application.getSnapshot().tabs.pending?.requestId !== requestId) return false;
    void application.invoke("navigation.reveal-complete", { requestId }).catch(showErrorToast);
    return true;
  }, [application]);
  const cancelViewSwitch = useCallback(() => {
    void application.invoke("navigation.cancel", {}).catch(showErrorToast);
  }, [application]);
  const getCurrentViewSwitchRequestID = useCallback(() => application.getSnapshot().tabs.navigationRevision, [application]);
  const isCurrentViewSwitchRequest = useCallback((requestId: number) => application.getSnapshot().tabs.navigationRevision === requestId, [application]);
  return { pendingViewSwitch, finishViewSwitch, cancelViewSwitch, getCurrentViewSwitchRequestID, isCurrentViewSwitchRequest,
    visiblePendingThreadID: pendingViewSwitch?.kind === "thread" && pendingViewSwitch.visible ? pendingViewSwitch.targetID : undefined,
    visiblePendingWorkspaceID: pendingViewSwitch?.kind === "workspace" ? pendingViewSwitch.targetID : undefined,
    viewSwitchPending: Boolean(pending), submissionTargetPending: Boolean(pending),
    viewContextSwitchPending: pendingViewSwitch?.kind === "workspace" || pendingViewSwitch?.kind === "runtime" && pendingViewSwitch.visible,
  };
}
