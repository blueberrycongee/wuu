import { useState, useSyncExternalStore } from "react";
import type { ApplicationServiceV1, ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import { X } from "./WuuIcons";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { UILayerPortal } from "./ui/layers/UILayerHost";

type RecoveryItems = NonNullable<ApplicationSnapshotV1["restartRecovery"]>;

export function RestartRecovery({ application }: { application: ApplicationServiceV1 }): JSX.Element | null {
  const items = useSyncExternalStore(application.subscribe, () => application.getSnapshot().restartRecovery);
  if (!items?.length) return null;
  return <RestartRecoveryNotice items={items} onResolve={threadIds => application.invoke("conversation.resolve-recovery", { threadIds })} />;
}

function RestartRecoveryNotice({ items, onResolve }: { items: RecoveryItems; onResolve: (threadIds: string[]) => Promise<void> }): JSX.Element {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  async function resolve(ids: string[]): Promise<void> {
    if (busy) return;
    setBusy(true);
    setFailed(false);
    try { await onResolve(ids); }
    catch { setFailed(true); }
    finally { setBusy(false); }
  }
  return <UILayerPortal layer="notice">
    <div className="archive-tip restart-recovery-notice" data-wuu-layer="notice" role="status" aria-live="polite">
      <span className="archive-tip-message" aria-busy={busy}>{t("restartRecovery.description", { count: items.length })}{failed ? <> {t("restartRecovery.actionFailed")}</> : null}</span>
      <button type="button" className="archive-tip-action" disabled={busy} onClick={() => void resolve(items.map(item => item.threadId))}>
        {busy ? t("restartRecovery.applying") : t("restartRecovery.continue")}
      </button>
      <Tooltip asChild content={t("restartRecovery.defer")}><button type="button" className="archive-tip-dismiss"
        aria-label={t("restartRecovery.defer")} disabled={busy} onClick={() => void resolve([])}><X className="icon-sm" /></button></Tooltip>
    </div>
  </UILayerPortal>;
}
