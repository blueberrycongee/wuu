import { CircleAlert } from "./WuuIcons";
import { useI18n } from "./i18n";

/** Durable draft state stays beside its editor; dismissing feedback never deletes it. */
export function DraftRecoveryNotice({ interrupted, onOpen }: { interrupted: boolean; onOpen(): void }): JSX.Element {
  const { t } = useI18n();
  return <div className="composer-recovery-notice" data-wuu-component="draft-recovery" role="status">
    <CircleAlert className="icon-sm" aria-hidden="true" />
    <span>{t(interrupted ? "composer.recoveredInterruptedDraft" : "composer.failedDraftSaved")}</span>
    <button type="button" className="settings-button settings-button-ghost" onClick={onOpen}>{t("composer.openFailedDraft")}</button>
  </div>;
}
