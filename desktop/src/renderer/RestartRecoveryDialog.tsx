import { useState, useSyncExternalStore } from "react";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import { Modal } from "./Modal";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";
import "./styles/restart-recovery.css";

type RecoveryItems = NonNullable<ApplicationSnapshotV1["restartRecovery"]>;

export function RestartRecovery({ application }: { application: ApplicationServiceV1 }): JSX.Element | null {
  const items = useSyncExternalStore(application.subscribe, () => application.getSnapshot().restartRecovery);
  if (!items?.length) return null;
  return <RestartRecoveryDialog items={items} onResolve={threadIds => application.invoke("conversation.resolve-recovery", { threadIds })} />;
}

export function RestartRecoveryDialog({ items, onResolve }: { items: RecoveryItems; onResolve: (threadIds: string[]) => Promise<void> }): JSX.Element {
  const { t, locale } = useI18n();
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [busy, setBusy] = useState(false);
  const selectedIDs = items.filter(item => selected.has(item.threadId)).map(item => item.threadId);
  async function resolve(ids: string[]): Promise<void> {
    if (busy) return;
    setBusy(true);
    try { await onResolve(ids); }
    catch { showErrorToast(t("restartRecovery.actionFailed")); }
    finally { setBusy(false); }
  }
  return <Modal title={t("restartRecovery.title")} ariaLabel={t("restartRecovery.title")}
    panelClassName="restart-recovery-dialog" onClose={() => void resolve([])} closeDisabled={busy}
    showCloseButton={false} initialFocus="none" footer={<>
      <button type="button" className="settings-button settings-button-ghost" autoFocus disabled={busy} onClick={() => void resolve([])}>{t("restartRecovery.defer")}</button>
      <button type="button" className="settings-button settings-button-primary" disabled={busy || selectedIDs.length === 0} onClick={() => void resolve(selectedIDs)}>
        {busy ? t("restartRecovery.applying") : t("restartRecovery.continue", { count: selectedIDs.length })}
      </button>
    </>}>
    <p className="restart-recovery-description">{t("restartRecovery.description")}</p>
    <div className="restart-recovery-list" aria-label={t("restartRecovery.tasks")} aria-busy={busy}>
      {items.map(item => <label key={item.threadId} className="restart-recovery-row">
        <input type="checkbox" checked={selected.has(item.threadId)} disabled={busy} onChange={event => {
          const checked = event.currentTarget.checked;
          setSelected(previous => { const next = new Set(previous); if (checked) next.add(item.threadId); else next.delete(item.threadId); return next; });
        }} />
        <span className="restart-recovery-details">
          <span className="restart-recovery-title">{item.title}</span>
          <span className="restart-recovery-meta">{item.workspace}<span aria-hidden="true"> · </span><time dateTime={item.updatedAt}>{new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(item.updatedAt))}</time></span>
          <span className="restart-recovery-reason">{t(item.reason === "interrupted" ? "restartRecovery.interrupted" : "restartRecovery.backgroundResult")}</span>
        </span>
      </label>)}
    </div>
    <p className="restart-recovery-hint">{t("restartRecovery.hint")}</p>
  </Modal>;
}
