import {
  useCallback,
  useEffect,
  useRef,
  useState
} from "react";
import type {
  RuntimeContext,
  SideThreadEventEnvelope,
  SideThreadHistoryResult,
  SideThreadOpenResult,
  SideThreadSendParams,
  SideThreadSendResult,
  SideThreadSelection
} from "../shared/protocol";
import {
  createInitialSideThreadStore,
  ensureSideThreadEntry,
  reduceSideThreadStore,
  sideThreadRequestError,
  type SideThreadAction,
  type SideThreadStoreState
} from "./SideThreadState";
import { useI18n } from "./i18n";

const SIDE_THREAD_RECOVERY_POLL_MS = 2_000;

export type SideThreadController = {
  entry: SideThreadStoreState["byThread"][string] | undefined;
  open: () => void;
  close: () => void;
  toggle: () => void;
  setDraft: (draft: string) => void;
  setDraftSelection: (selection: SideThreadSelection | undefined) => void;
  sendMessage: (prompt: string) => void;
  interrupt: () => void;
  reset: () => void;
  sendDisabledReason?: string;
  requestError?: string;
};

export type SideThreadControllerOptions = {
  activeThreadId: string | undefined;
  activeContext?: RuntimeContext;
  ipc?: SideThreadIPC;
  disabled?: boolean;
  disabledReason?: string;
};

export type SideThreadIPC = {
  openSideThread: (mainThreadId: string) => Promise<SideThreadOpenResult>;
  getSideThreadHistory: (
    mainThreadId: string
  ) => Promise<SideThreadHistoryResult | null>;
  sendSideThreadMessage: (
    params: SideThreadSendParams
  ) => Promise<SideThreadSendResult>;
  interruptSideThread: (mainThreadId: string) => Promise<{ ok: boolean }>;
  resetSideThread: (mainThreadId: string) => Promise<{ ok: boolean }>;
  onSideThreadEvent: (
    handler: (envelope: SideThreadEventEnvelope) => void
  ) => () => void;
};

function defaultIPC(): SideThreadIPC | undefined {
  if (typeof window === "undefined") {
    return undefined;
  }
  const candidate = (window as unknown as { wuu?: Partial<SideThreadIPC> }).wuu;
  if (
    typeof candidate?.openSideThread !== "function" ||
    typeof candidate.getSideThreadHistory !== "function" ||
    typeof candidate.sendSideThreadMessage !== "function" ||
    typeof candidate.interruptSideThread !== "function" ||
    typeof candidate.resetSideThread !== "function" ||
    typeof candidate.onSideThreadEvent !== "function"
  ) {
    return undefined;
  }
  return candidate as SideThreadIPC;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function useSideThreadController(
  options: SideThreadControllerOptions
): SideThreadController {
  const { t } = useI18n();
  const { activeThreadId, activeContext, ipc, disabled, disabledReason } = options;
  const ipcImpl = ipc ?? defaultIPC();
  const effectiveDisabled = Boolean(disabled || !ipcImpl);
  const effectiveReason = !ipcImpl
    ? t("sideThread.unsupported")
    : disabled
      ? disabledReason ?? t("sideThread.unavailable")
      : undefined;

  const [store, setStore] = useState<SideThreadStoreState>(() =>
    createInitialSideThreadStore()
  );
  const storeRef = useRef(store);
  const openGenerationRef = useRef(new Map<string, number>());
  const pendingSendTasksRef = useRef(new Map<string, Promise<void>>());
  const lastActiveRuntimeKeyRef = useRef<string | undefined>(undefined);
  const activeRuntimeKey =
    activeThreadId && activeContext?.cwd
      ? `${activeContext.cwd}\0${activeThreadId}`
      : undefined;
  const activeRuntimeKeyRef = useRef(activeRuntimeKey);
  activeRuntimeKeyRef.current = activeRuntimeKey;
  storeRef.current = store;
  const entry = activeThreadId ? store.byThread[activeThreadId] : undefined;
  const dispatch = useCallback((action: SideThreadAction) => {
    setStore((previous) => reduceSideThreadStore(previous, action));
  }, []);

  useEffect(() => {
    if (!ipcImpl) {
      return;
    }
    return ipcImpl.onSideThreadEvent((envelope) => {
      if (envelope.workdir !== activeContext?.cwd) {
        return;
      }
      setStore((previous) =>
        reduceSideThreadStore(previous, {
          type: "applyEvent",
          event: envelope.event
        })
      );
    });
  }, [activeContext?.cwd, ipcImpl]);

  useEffect(() => {
    if (!activeThreadId) {
      return;
    }
    setStore((previous) =>
      ensureSideThreadEntry(previous, activeThreadId).store
    );
  }, [activeThreadId]);

  useEffect(() => {
    if (!activeThreadId || !activeRuntimeKey || !ipcImpl) {
      lastActiveRuntimeKeyRef.current = activeRuntimeKey;
      return;
    }
    if (lastActiveRuntimeKeyRef.current === activeRuntimeKey) {
      return;
    }
    lastActiveRuntimeKeyRef.current = activeRuntimeKey;
    if (!storeRef.current.byThread[activeThreadId]?.open) {
      return;
    }

    let cancelled = false;
    const generation = (openGenerationRef.current.get(activeRuntimeKey) ?? 0) + 1;
    openGenerationRef.current.set(activeRuntimeKey, generation);
    const isCurrentRequest = () =>
      !cancelled &&
      activeRuntimeKeyRef.current === activeRuntimeKey &&
      openGenerationRef.current.get(activeRuntimeKey) === generation;

    void (async () => {
      try {
        const opened = await ipcImpl.openSideThread(activeThreadId);
        const openedSummary = opened.summary;
        if (!isCurrentRequest() || !openedSummary) {
          return;
        }
        setStore((previous) =>
          reduceSideThreadStore(previous, {
            type: "mergeSummary",
            mainThreadId: activeThreadId,
            summary: openedSummary
          })
        );

        const history = await ipcImpl.getSideThreadHistory(activeThreadId);
        if (!isCurrentRequest() || !history) {
          return;
        }
        await pendingSendTasksRef.current.get(activeThreadId);
        if (!isCurrentRequest()) {
          return;
        }
        setStore((previous) =>
          reduceSideThreadStore(previous, {
            type: "mergeHistory",
            mainThreadId: activeThreadId,
            summary: history.summary,
            messages: history.messages
          })
        );
      } catch {
        // Activation refresh is best-effort recovery. The explicit open/send
        // paths surface actionable failures in the panel.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [activeRuntimeKey, activeThreadId, ipcImpl]);

  useEffect(() => {
    if (
      !activeThreadId ||
      !activeRuntimeKey ||
      !ipcImpl ||
      !entry?.open ||
      entry.summary?.status !== "running"
    ) {
      return;
    }
    let cancelled = false;
    let requestInFlight = false;
    const refresh = async () => {
      if (
        cancelled ||
        requestInFlight ||
        activeRuntimeKeyRef.current !== activeRuntimeKey
      ) {
        return;
      }
      requestInFlight = true;
      try {
        const history = await ipcImpl.getSideThreadHistory(activeThreadId);
        if (
          !cancelled &&
          history &&
          activeRuntimeKeyRef.current === activeRuntimeKey
        ) {
          setStore((previous) =>
            reduceSideThreadStore(previous, {
              type: "mergeHistory",
              mainThreadId: activeThreadId,
              summary: history.summary,
              messages: history.messages
            })
          );
        }
      } catch {
        // Notifications remain the primary path; polling only repairs missed
        // terminal events and peer-process updates.
      } finally {
        requestInFlight = false;
      }
    };
    const timer = window.setInterval(() => void refresh(), SIDE_THREAD_RECOVERY_POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [activeRuntimeKey, activeThreadId, entry?.open, entry?.summary?.status, ipcImpl]);

  const open = useCallback(() => {
    if (!activeThreadId) {
      return;
    }
    dispatch({ type: "open", mainThreadId: activeThreadId });
    dispatch({ type: "setError", mainThreadId: activeThreadId, error: undefined });
    if (!ipcImpl || disabled || !activeContext || !activeRuntimeKey) {
      return;
    }

    const generation = (openGenerationRef.current.get(activeRuntimeKey) ?? 0) + 1;
    openGenerationRef.current.set(activeRuntimeKey, generation);
    const isCurrentRequest = () =>
      activeRuntimeKeyRef.current === activeRuntimeKey &&
      openGenerationRef.current.get(activeRuntimeKey) === generation;

    void (async () => {
      try {
        const opened = await ipcImpl.openSideThread(activeThreadId);
        const openedSummary = opened.summary;
        if (!isCurrentRequest() || !openedSummary) {
          return;
        }
        setStore((previous) =>
          reduceSideThreadStore(previous, {
            type: "mergeSummary",
            mainThreadId: activeThreadId,
            summary: openedSummary
          })
        );

        const history = await ipcImpl.getSideThreadHistory(activeThreadId);
        if (!isCurrentRequest() || !history) {
          return;
        }
        await pendingSendTasksRef.current.get(activeThreadId);
        if (!isCurrentRequest()) {
          return;
        }
        setStore((previous) =>
          reduceSideThreadStore(previous, {
            type: "mergeHistory",
            mainThreadId: activeThreadId,
            summary: history.summary,
            messages: history.messages
          })
        );
      } catch (error) {
        if (isCurrentRequest()) {
          dispatch({
            type: "setError",
            mainThreadId: activeThreadId,
            error: errorMessage(error)
          });
        }
      }
    })();
  }, [activeContext, activeRuntimeKey, activeThreadId, disabled, dispatch, ipcImpl]);

  const close = useCallback(() => {
    if (activeThreadId) {
      dispatch({ type: "close", mainThreadId: activeThreadId });
    }
  }, [activeThreadId, dispatch]);

  const toggle = useCallback(() => {
    if (!activeThreadId) {
      return;
    }
    if (storeRef.current.byThread[activeThreadId]?.open) {
      close();
    } else {
      open();
    }
  }, [activeThreadId, close, open]);

  const setDraft = useCallback(
    (draft: string) => {
      if (activeThreadId) {
        dispatch({ type: "setDraft", mainThreadId: activeThreadId, draft });
      }
    },
    [activeThreadId, dispatch]
  );

  const setDraftSelection = useCallback((selection: SideThreadSelection | undefined) => {
    if (activeThreadId) dispatch({ type: "setDraftSelection", mainThreadId: activeThreadId, selection });
  }, [activeThreadId, dispatch]);

  const sendMessage = useCallback(
    (prompt: string) => {
      if (!activeThreadId || !activeContext || !ipcImpl || effectiveDisabled) {
        return;
      }
      const trimmed = prompt.trim();
      const currentEntry = storeRef.current.byThread[activeThreadId];
      const selection = currentEntry?.draftSelection;
      if (
        !trimmed ||
        currentEntry?.streaming ||
        pendingSendTasksRef.current.has(activeThreadId)
      ) {
        return;
      }

      const optimisticId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      const optimisticMessage = {
        id: optimisticId,
        side_thread_id: currentEntry?.summary?.side_thread_id ?? "",
        role: "user" as const,
        text: trimmed,
        ...(selection ? { selection } : {}),
        created_at: new Date().toISOString()
      };
      setStore((previous) => {
        let next = reduceSideThreadStore(previous, {
          type: "appendMessage",
          mainThreadId: activeThreadId,
          message: optimisticMessage
        });
        next = reduceSideThreadStore(next, {
          type: "setDraft",
          mainThreadId: activeThreadId,
          draft: ""
        });
        next = reduceSideThreadStore(next, {
          type: "setDraftSelection",
          mainThreadId: activeThreadId,
          selection: undefined
        });
        next = reduceSideThreadStore(next, {
          type: "setStreaming",
          mainThreadId: activeThreadId,
          streaming: true
        });
        return reduceSideThreadStore(next, {
          type: "setError",
          mainThreadId: activeThreadId,
          error: undefined
        });
      });

      const task = (async () => {
        try {
          const result = await ipcImpl.sendSideThreadMessage({
            main_thread_id: activeThreadId,
            prompt: trimmed,
            ...(selection ? { selection } : {})
          });
          setStore((previous) => {
            let next = reduceSideThreadStore(previous, {
              type: "mergeSummary",
              mainThreadId: activeThreadId,
              summary: result.summary
            });
            next = reduceSideThreadStore(next, {
              type: "removeMessage",
              mainThreadId: activeThreadId,
              messageId: result.user_message_id
            });
            return reduceSideThreadStore(next, {
              type: "updateMessage",
              mainThreadId: activeThreadId,
              messageId: optimisticId,
              patch: {
                id: result.user_message_id,
                side_thread_id: result.summary.side_thread_id
              }
            });
          });
        } catch (error) {
          setStore((previous) => {
            let next = reduceSideThreadStore(previous, {
              type: "removeMessage",
              mainThreadId: activeThreadId,
              messageId: optimisticId
            });
            next = reduceSideThreadStore(next, {
              type: "setStreaming",
              mainThreadId: activeThreadId,
              streaming: false
            });
            next = reduceSideThreadStore(next, {
              type: "setError",
              mainThreadId: activeThreadId,
              error: errorMessage(error)
            });
            const nextDraft = next.byThread[activeThreadId];
            // Text and its selected source belong to one submission. Restoring
            // either field alone can attach failed context to a newer question.
            if (!nextDraft?.draft && !nextDraft?.draftSelection) {
              next = reduceSideThreadStore(next, {
                type: "setDraft",
                mainThreadId: activeThreadId,
                draft: trimmed
              });
              next = reduceSideThreadStore(next, { type: "setDraftSelection", mainThreadId: activeThreadId, selection });
            }
            return next;
          });
        }
      })();
      pendingSendTasksRef.current.set(activeThreadId, task);
      void task.finally(() => {
        if (pendingSendTasksRef.current.get(activeThreadId) === task) {
          pendingSendTasksRef.current.delete(activeThreadId);
        }
      });
    },
    [activeContext, activeThreadId, effectiveDisabled, ipcImpl]
  );

  const interrupt = useCallback(() => {
    if (!activeThreadId || !ipcImpl) {
      return;
    }
    void ipcImpl.interruptSideThread(activeThreadId).catch((error: unknown) => {
      dispatch({
        type: "setError",
        mainThreadId: activeThreadId,
        error: errorMessage(error)
      });
    });
  }, [activeThreadId, dispatch, ipcImpl]);

  const reset = useCallback(() => {
    if (!activeThreadId || !ipcImpl || effectiveDisabled) {
      return;
    }
    // The server's reset broadcast also clears this entry; dispatching
    // locally just avoids a visible round-trip delay.
    void ipcImpl
      .resetSideThread(activeThreadId)
      .then(() => {
        dispatch({ type: "reset", mainThreadId: activeThreadId });
      })
      .catch((error: unknown) => {
        dispatch({
          type: "setError",
          mainThreadId: activeThreadId,
          error: errorMessage(error)
        });
      });
  }, [activeThreadId, dispatch, effectiveDisabled, ipcImpl]);

  return {
    entry,
    open,
    close,
    toggle,
    setDraft,
    setDraftSelection,
    sendMessage,
    interrupt,
    reset,
    sendDisabledReason:
      effectiveDisabled || !activeContext
        ? effectiveReason ?? t("sideThread.selectWorkspace")
        : undefined,
    requestError: entry ? sideThreadRequestError(entry) : undefined
  };
}
