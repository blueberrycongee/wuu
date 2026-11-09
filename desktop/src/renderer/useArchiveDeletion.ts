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
    list: (signal?: AbortSignal) => Promise<readonly { id: string; archived?: boolean }[]>;
    remove: (id: string) => Promise<void>;
  },
): ArchiveDeletionState & { removeAll: () => Promise<void>; retry: () => Promise<void> } {
  const { t } = useI18n();
  const operation = useMemo(() => ({ active: true, locked: false, confirmation: null as AbortController | null, opener: null as HTMLElement | null }), [source]);
  useLayoutEffect(() => {
    operation.active = true;
    setState({ pending: false, failedIDs: [], error: "" });
    return () => { operation.active = false; operation.confirmation?.abort(); operation.opener = null; };
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
      // Show the dialog before the authoritative read, without making a stale
      // cached count or filtered view the scope of an irreversible operation.
      let ids: string[] | undefined;
      const confirmation = new AbortController();
      operation.confirmation = confirmation;
      const confirmed = await confirmAction({
        title: t("settings.deleteAllArchived"),
        message: t("settings.deleteArchivedConfirmMessage"),
        confirmLabel: t("common.loadingEllipsis"),
        tone: "danger",
        prepare: async () => {
          const listed = await source.list(confirmation.signal);
          if (!operation.active) return null;
          const eligible = new Set(listed.filter(thread => thread.archived).map(thread => thread.id));
          ids = retryIDs ? [...new Set(retryIDs)].filter(id => eligible.has(id)) : [...eligible];
          return ids.length === 0 ? null : {
            title: t("settings.deleteArchivedConfirmTitle", { count: ids.length }),
            message: t("settings.deleteArchivedConfirmMessage"),
            confirmLabel: t("settings.deleteArchivedConfirmAction", { count: ids.length }),
            tone: "danger",
          };
        },
      }, confirmation.signal);
      if (operation.active && ids?.length === 0) {
        setState(current => ({ ...current, failedIDs: [], result: undefined }));
      }
      if (!confirmed || !operation.active || !ids?.length) return;

      const confirmedIDs = ids;
      let deleted = 0;
      let skipped = 0;
      let failedIDs: string[] = [];
      let firstError = "";
      setState(current => ({ ...current, result: undefined, failedIDs: [], progress: { completed: 0, total: confirmedIDs.length } }));
      // IDs are fixed at confirmation. The server's transactional archived-only
      // guard preserves anything restored while this batch is in progress.
      for (const id of confirmedIDs) {
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
        setState(current => ({ ...current, progress: { completed: deleted + failedIDs.length, total: confirmedIDs.length } }));
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
      operation.confirmation?.abort();
      operation.confirmation = null;
      operation.locked = false;
      if (operation.active) setState(current => ({ ...current, pending: false, progress: undefined }));
    }
  }

  return { ...state, removeAll: () => remove(), retry: () => remove(state.failedIDs) };
}
