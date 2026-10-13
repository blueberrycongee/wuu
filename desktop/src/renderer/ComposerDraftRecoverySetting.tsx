import { useEffect, useState } from "react";
import { composerDraftRecoveryEnabled, observeComposerDraftRecovery, setComposerDraftRecoveryEnabled } from "./ComposerDraftRecovery";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function ComposerDraftRecoverySetting(): JSX.Element {
  const { t } = useI18n();
  const [enabled, setEnabled] = useState(composerDraftRecoveryEnabled);
  const [failed, setFailed] = useState(false);
  useEffect(() => observeComposerDraftRecovery(() => setEnabled(composerDraftRecoveryEnabled())), []);
  function toggle(): void {
    try {
      setComposerDraftRecoveryEnabled(!enabled);
      setEnabled(composerDraftRecoveryEnabled());
      setFailed(false);
    } catch {
      setEnabled(composerDraftRecoveryEnabled());
      setFailed(true);
    }
  }
  return (
    <SettingsRow title={t("settings.draftRecovery")} description={t("settings.draftRecoveryDescription")}
      error={failed ? t("settings.draftRecoveryUnavailable") : undefined}>
      <button className="settings-switch" type="button" role="switch" aria-checked={enabled}
        aria-label={t("settings.draftRecovery")} data-testid="settings-draft-recovery" onClick={toggle}>
        <span className="settings-switch-thumb" aria-hidden="true" />
      </button>
    </SettingsRow>
  );
}
