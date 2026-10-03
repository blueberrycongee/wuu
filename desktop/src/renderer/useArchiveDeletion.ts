import { useLayoutEffect, useRef, useState } from "react";
import type { Thread } from "../shared/protocol";
import { confirmAction } from "./ConfirmDialog";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";

export type ArchiveDeletionResult = { deleted: number; skipped: number; failed: number };
export type ArchiveDeletionState = {
  pending: boolean;
  progress?: { completed: number; total: number };
  result?: ArchiveDeletionResult;
  failedIDs: readonly string[];
  error: string;
};

/** Keep the destructive operation alive above settings-page navigation. */
export function useArchiveDeletion(
  deleteThread: (id: string) => Promise<void>,
  refreshArchive: (threads: Thread[]) => void,
): ArchiveDeletionState & { removeAll: () => Promise<void>; retry: () => Promise<void> } {
  const { t } = useI18n();
  const locked = useRef(false);
  const opener = useRef<HTMLElement | null>(null);
  const [state, setState] = useState<ArchiveDeletionState>({ pending: false, failedIDs: [], error: "" });

  useLayoutEffect(() => {
    if (state.pending) return;
    // Modal cleanup can run while its initiating button is still disabled.
    // Restore once the pending render re-enables it, without stealing focus
    // after the user has navigated or focused another control.
    if (opener.current?.isConnected && document.activeElement === document.body) {
      opener.current.focus({ preventScroll: true });
    }
    opener.current = null;
  }, [state.pending]);

  async function remove(retryIDs?: readonly string[]): Promise<void> {
    if (locked.current) return;
    locked.current = true;
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setState(current => ({ ...current, pending: true, progress: undefined, error: "" }));
    try {
      // Refresh before confirmation: the cached sidebar catalog can be stale,
      // and search/workspace filters must never redefine “all archived”.
      const listed = await window.wuu.listArchivedThreads();
      refreshArchive(listed.threads);
      const eligible = new Set(listed.threads.filter(thread => thread.archived).map(thread => thread.id));
      const ids = retryIDs ? [...new Set(retryIDs)].filter(id => eligible.has(id)) : [...eligible];
      if (ids.length === 0) {
        setState(current => ({ ...current, failedIDs: [], result: undefined }));
        return;
      }
      const confirmed = await confirmAction({
        title: t("settings.deleteArchivedConfirmTitle", { count: ids.length }),
        message: t("settings.deleteArchivedConfirmMessage"),
        confirmLabel: t("settings.deleteArchivedConfirmAction", { count: ids.length }),
        tone: "danger",
      });
      if (!confirmed) return;

      let deleted = 0;
      let skipped = 0;
      let failedIDs: string[] = [];
      let firstError = "";
      setState(current => ({ ...current, result: undefined, failedIDs: [], progress: { completed: 0, total: ids.length } }));
      // IDs are fixed at confirmation. The server's transactional archived-only
      // guard preserves anything restored while this batch is in progress.
      for (const id of ids) {
        try {
          await deleteThread(id);
          deleted++;
        } catch (error) {
          failedIDs.push(id);
          firstError ||= toastErrorMessage(error, t("thread.deleteFailed"));
        }
        setState(current => ({ ...current, progress: { completed: deleted + failedIDs.length, total: ids.length } }));
      }
      try {
        const remaining = await window.wuu.listArchivedThreads();
        refreshArchive(remaining.threads);
        const remainingIDs = new Set(remaining.threads.filter(thread => thread.archived).map(thread => thread.id));
        // A restore or a successful delete with a lost response is no longer a
        // retry target. Never add newly archived conversations to this batch.
        const retryable = failedIDs.filter(id => remainingIDs.has(id));
        skipped = failedIDs.length - retryable.length;
        failedIDs = retryable;
      } catch (error) {
        firstError = toastErrorMessage(error, t("settings.archiveRefreshFailed"));
      }
      setState(current => ({ ...current, failedIDs, result: { deleted, skipped, failed: failedIDs.length },
        error: failedIDs.length > 0 ? firstError : firstError && skipped === 0 ? firstError : "" }));
    } catch (error) {
      setState(current => ({ ...current, error: toastErrorMessage(error, t("settings.archiveRefreshFailed")) }));
    } finally {
      locked.current = false;
      setState(current => ({ ...current, pending: false, progress: undefined }));
    }
  }

  return { ...state, removeAll: () => remove(), retry: () => remove(state.failedIDs) };
}
