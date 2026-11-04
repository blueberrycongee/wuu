import { useState, useSyncExternalStore } from "react";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import { Modal } from "./Modal";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

type RecoveryItems = NonNullable<ApplicationSnapshotV1["restartRecovery"]>;

export function RestartRecovery({ application }: { application: ApplicationServiceV1 }): JSX.Element | null {
  const items = useSyncExternalStore(application.subscribe, () => application.getSnapshot().restartRecovery);
  if (!items?.length) return null;
  return <RestartRecoveryDialog items={items} onResolve={threadIds => application.invoke("conversation.resolve-recovery", { threadIds })} />;
}

export function RestartRecoveryDialog({ items, onResolve }: { items: RecoveryItems; onResolve: (threadIds: string[]) => Promise<void> }): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  async function resolve(ids: string[]): Promise<void> {
    if (busy) return;
    setBusy(true);
    try { await onResolve(ids); }
    catch { showErrorToast(t("restartRecovery.actionFailed")); }
    finally { setBusy(false); }
  }
  return <Modal title={t("restartRecovery.title")} ariaLabel={t("restartRecovery.title")}
    panelClassName="confirm-dialog restart-recovery-dialog" onClose={() => void resolve([])} closeDisabled={busy}
    showCloseButton={false} initialFocus="none" footer={<>
      <button type="button" className="settings-button settings-button-ghost" autoFocus disabled={busy} onClick={() => void resolve([])}>{t("restartRecovery.defer")}</button>
      <button type="button" className="settings-button settings-button-primary" disabled={busy} onClick={() => void resolve(items.map(item => item.threadId))}>
        {busy ? t("restartRecovery.applying") : t("restartRecovery.continue")}
      </button>
    </>}>
    <p className="confirm-dialog-message" aria-busy={busy}>{t("restartRecovery.description", { count: items.length })}</p>
  </Modal>;
}
