import { useCallback, useEffect, useRef, useState } from "react";

export const VIEW_SWITCH_LOADING_DELAY_MS = 180;

export type PendingViewSwitchKind = "thread" | "project" | "runtime";

export type PendingViewSwitch = {
  kind: PendingViewSwitchKind;
  targetID: string;
  visible: boolean;
  background?: boolean;
};

export type ViewSwitchStateController = {
  pendingViewSwitch: PendingViewSwitch | undefined;
  visiblePendingThreadID: string | undefined;
  visiblePendingProjectID: string | undefined;
  viewSwitchPending: boolean;
  submissionTargetPending: boolean;
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
    (targetID = ""): number => {
      const requestID = viewSwitchRequestRef.current + 1;
      viewSwitchRequestRef.current = requestID;
      clearViewSwitchDelay();
      // Submissions carry their own destination; cached history hydration is
      // not a prerequisite for accepting the next message.
      setPendingViewSwitch({ kind: "thread", targetID, visible: false, background: true });
      return requestID;
    },
    [clearViewSwitchDelay],
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

  const isCurrentViewSwitchRequest = useCallback(
    (requestID: number): boolean => viewSwitchRequestRef.current === requestID,
    [],
  );

  // Background resume must not mark cached tabs, sidebar rows, or extension
  // headers busy when no loading UI is needed.
  const visiblePendingThreadID =
    pendingViewSwitch?.kind === "thread" && pendingViewSwitch.visible
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
    submissionTargetPending: viewSwitchPending && !pendingViewSwitch?.background,
    viewContextSwitchPending,
    beginViewSwitch,
    beginInstantThreadSwitch,
    finishViewSwitch,
    cancelViewSwitch,
    isCurrentViewSwitchRequest,
  };
}
