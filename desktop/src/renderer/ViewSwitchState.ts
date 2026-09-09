import { useCallback, useEffect, useRef, useState } from "react";

export const VIEW_SWITCH_LOADING_DELAY_MS = 180;

export type PendingViewSwitchKind = "thread" | "project" | "runtime";

export type PendingViewSwitch = {
  kind: PendingViewSwitchKind;
  targetID: string;
  visible: boolean;
};

export type ViewSwitchStateController = {
  pendingViewSwitch: PendingViewSwitch | undefined;
  visiblePendingThreadID: string | undefined;
  visiblePendingProjectID: string | undefined;
  viewSwitchPending: boolean;
  viewContextSwitchPending: boolean;
  beginViewSwitch: (
    kind: PendingViewSwitchKind,
    targetID: string,
  ) => number;
  beginInstantThreadSwitch: (targetID?: string) => number;
  finishViewSwitch: (requestID: number) => boolean;
  cancelViewSwitch: () => void;
  isCurrentViewSwitchRequest: (requestID: number) => boolean;
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
    (kind: PendingViewSwitchKind, targetID: string): number => {
      const requestID = viewSwitchRequestRef.current + 1;
      viewSwitchRequestRef.current = requestID;
      clearViewSwitchDelay();
      setPendingViewSwitch({ kind, targetID, visible: false });
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

  const beginInstantThreadSwitch = useCallback(
    (targetID = ""): number => beginViewSwitch("thread", targetID),
    [beginViewSwitch],
  );

  const finishViewSwitch = useCallback(
    (requestID: number): boolean => {
      if (viewSwitchRequestRef.current !== requestID) {
        return false;
      }
      clearViewSwitchDelay();
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

  const isCurrentViewSwitchRequest = useCallback(
    (requestID: number): boolean => viewSwitchRequestRef.current === requestID,
    [],
  );

  const visiblePendingThreadID =
    pendingViewSwitch?.kind === "thread"
      ? pendingViewSwitch.targetID
      : undefined;
  const visiblePendingProjectID =
    pendingViewSwitch?.kind === "project"
      ? pendingViewSwitch.targetID
      : undefined;
  const viewSwitchPending = pendingViewSwitch !== undefined;
  const viewContextSwitchPending =
    pendingViewSwitch?.kind === "project" ||
    (pendingViewSwitch?.kind === "runtime" && pendingViewSwitch.visible);

  return {
    pendingViewSwitch,
    visiblePendingThreadID,
    visiblePendingProjectID,
    viewSwitchPending,
    viewContextSwitchPending,
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
  };
}
