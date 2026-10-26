import { useLayoutEffect, useMemo, useState } from "react";
import { confirmAction } from "./ConfirmDialog";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
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
  source: {
    list: () => Promise<readonly { id: string; archived?: boolean }[]>;
    remove: (id: string) => Promise<void>;
  },
): ArchiveDeletionState & { removeAll: () => Promise<void>; retry: () => Promise<void> } {
  const { t } = useI18n();
  const operation = useMemo(() => ({ active: true, locked: false, opener: null as HTMLElement | null }), [source]);
  useLayoutEffect(() => {
    operation.active = true;
    setState({ pending: false, failedIDs: [], error: "" });
    return () => { operation.active = false; operation.opener = null; };
  }, [operation]);
  const [state, setState] = useState<ArchiveDeletionState>({ pending: false, failedIDs: [], error: "" });

  useLayoutEffect(() => {
    if (state.pending) return;
    // Modal cleanup can run while its initiating button is still disabled.
    // Restore once the pending render re-enables it, without stealing focus
    // after the user has navigated or focused another control.
    if (operation.opener?.isConnected && document.activeElement === document.body) {
      operation.opener.focus({ preventScroll: true });
    }
    operation.opener = null;
  }, [state.pending, operation]);

  async function remove(retryIDs?: readonly string[]): Promise<void> {
    if (operation.locked || !operation.active) return;
    operation.locked = true;
    operation.opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setState(current => ({ ...current, pending: true, progress: undefined, error: "" }));
    try {
      // Refresh before confirmation: the cached sidebar catalog can be stale,
      // and search/workspace filters must never redefine “all archived”.
      const listed = await source.list();
      if (!operation.active) return;
      const eligible = new Set(listed.filter(thread => thread.archived).map(thread => thread.id));
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
      if (!confirmed || !operation.active) return;

      let deleted = 0;
      let skipped = 0;
      let failedIDs: string[] = [];
      let firstError = "";
      setState(current => ({ ...current, result: undefined, failedIDs: [], progress: { completed: 0, total: ids.length } }));
      // IDs are fixed at confirmation. The server's transactional archived-only
      // guard preserves anything restored while this batch is in progress.
      for (const id of ids) {
        if (!operation.active) return;
        try {
          await source.remove(id);
          deleted++;
        } catch (error) {
          if (!operation.active || isPluginLifecycleCancellation(error)) return;
          failedIDs.push(id);
          firstError ||= toastErrorMessage(error, t("thread.deleteFailed"));
        }
        if (!operation.active) return;
        setState(current => ({ ...current, progress: { completed: deleted + failedIDs.length, total: ids.length } }));
      }
      try {
        const remaining = await source.list();
        if (!operation.active) return;
        const remainingIDs = new Set(remaining.filter(thread => thread.archived).map(thread => thread.id));
        // A restore or a successful delete with a lost response is no longer a
        // retry target. Never add newly archived conversations to this batch.
        const retryable = failedIDs.filter(id => remainingIDs.has(id));
        skipped = failedIDs.length - retryable.length;
        failedIDs = retryable;
      } catch (error) {
        if (!operation.active || isPluginLifecycleCancellation(error)) return;
        firstError = toastErrorMessage(error, t("settings.archiveRefreshFailed"));
      }
      setState(current => ({ ...current, failedIDs, result: { deleted, skipped, failed: failedIDs.length },
        error: failedIDs.length > 0 ? firstError : firstError && skipped === 0 ? firstError : "" }));
    } catch (error) {
      if (operation.active && !isPluginLifecycleCancellation(error)) setState(current => ({ ...current, error: toastErrorMessage(error, t("settings.archiveRefreshFailed")) }));
    } finally {
      operation.locked = false;
      if (operation.active) setState(current => ({ ...current, pending: false, progress: undefined }));
    }
  }

  return { ...state, removeAll: () => remove(), retry: () => remove(state.failedIDs) };
}
