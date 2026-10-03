import { useCallback, useEffect, useRef, useState } from "react";

export const VIEW_SWITCH_LOADING_DELAY_MS = 180;

export type PendingViewSwitchKind = "thread" | "workspace" | "runtime";

export type PendingViewSwitch = {
  kind: PendingViewSwitchKind;
  targetID: string;
  visible: boolean;
  contextSwitching?: boolean;
  restoreRequestID?: number;
};

export type ViewSwitchStateController = {
  pendingViewSwitch: PendingViewSwitch | undefined;
  visiblePendingThreadID: string | undefined;
  visiblePendingWorkspaceID: string | undefined;
  viewSwitchPending: boolean;
  submissionTargetPending: boolean;
  viewContextSwitchPending: boolean;
  beginViewSwitch: (
    kind: PendingViewSwitchKind,
    targetID: string,
    contextSwitching?: boolean,
  ) => number;
  prepareThreadReveal: (requestID: number) => boolean;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  isCurrentViewSwitchRequest: (requestID: number) => boolean;
  getCurrentViewSwitchRequestID: () => number;
};

export function useViewSwitchState({
  loadingDelayMs = VIEW_SWITCH_LOADING_DELAY_MS,
}: {
  loadingDelayMs?: number;
} = {}): ViewSwitchStateController {
  const [pendingViewSwitch, setPendingViewSwitch] = useState<
    PendingViewSwitch | undefined
  >();
  const viewSwitchRequestRef = useRef(0);
  const viewSwitchDelayTimerRef = useRef<number | undefined>(undefined);

  const clearViewSwitchDelay = useCallback((): void => {
    if (viewSwitchDelayTimerRef.current === undefined) {
      return;
    }
    window.clearTimeout(viewSwitchDelayTimerRef.current);
    viewSwitchDelayTimerRef.current = undefined;
  }, []);

  useEffect(() => {
    return () => {
      clearViewSwitchDelay();
    };
  }, [clearViewSwitchDelay]);

  const beginViewSwitch = useCallback(
    (kind: PendingViewSwitchKind, targetID: string, contextSwitching = false): number => {
      const requestID = viewSwitchRequestRef.current + 1;
      viewSwitchRequestRef.current = requestID;
      clearViewSwitchDelay();
      setPendingViewSwitch({
        kind,
        targetID,
        visible: kind === "thread",
        ...(contextSwitching || kind !== "thread" ? { contextSwitching: true } : {}),
      });
      if (kind === "thread") return requestID;
      viewSwitchDelayTimerRef.current = window.setTimeout(() => {
        viewSwitchDelayTimerRef.current = undefined;
        if (viewSwitchRequestRef.current !== requestID) {
          return;
        }
        setPendingViewSwitch((current) =>
          current?.kind === kind && current.targetID === targetID
            ? { ...current, visible: true }
            : current,
        );
      }, loadingDelayMs);
      return requestID;
    },
    [clearViewSwitchDelay, loadingDelayMs],
  );

  const prepareThreadReveal = useCallback(
    (requestID: number): boolean => {
      if (viewSwitchRequestRef.current !== requestID) return false;
      setPendingViewSwitch(current => current?.kind === "thread"
        ? { ...current, restoreRequestID: requestID }
        : current);
      return true;
    },
    [],
  );

  const finishViewSwitch = useCallback(
    (requestID: number): boolean => {
      if (viewSwitchRequestRef.current !== requestID) {
        return false;
      }
      clearViewSwitchDelay();
      // Content readiness wins over animation: never impose a minimum display
      // time or wait for an exit animation before releasing the conversation.
      setPendingViewSwitch(undefined);
      return true;
    },
    [clearViewSwitchDelay],
  );

  const cancelViewSwitch = useCallback((): void => {
    viewSwitchRequestRef.current += 1;
    clearViewSwitchDelay();
    setPendingViewSwitch(undefined);
  }, [clearViewSwitchDelay]);

  const getCurrentViewSwitchRequestID = useCallback(() => viewSwitchRequestRef.current, []);

  const isCurrentViewSwitchRequest = useCallback(
    (requestID: number): boolean => viewSwitchRequestRef.current === requestID,
    [],
  );

  const visiblePendingThreadID =
    pendingViewSwitch?.kind === "thread" && pendingViewSwitch.visible
      ? pendingViewSwitch.targetID
      : undefined;
  const visiblePendingWorkspaceID =
    pendingViewSwitch?.kind === "workspace"
      ? pendingViewSwitch.targetID
      : undefined;
  const viewSwitchPending = pendingViewSwitch !== undefined;
  const viewContextSwitchPending =
    pendingViewSwitch?.kind === "workspace" ||
    (pendingViewSwitch?.kind === "runtime" && pendingViewSwitch.visible);

  return {
    pendingViewSwitch,
    visiblePendingThreadID,
    visiblePendingWorkspaceID,
    viewSwitchPending,
    submissionTargetPending: viewSwitchPending,
    viewContextSwitchPending,
    beginViewSwitch,
    prepareThreadReveal,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
    getCurrentViewSwitchRequestID,
  };
}
