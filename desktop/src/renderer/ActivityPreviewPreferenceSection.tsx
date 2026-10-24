import { useSettingsControls } from "./SettingsControlsContext";
import { useEffect, useRef, useState } from "react";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function ActivityPreviewPreferenceSection(): JSX.Element | null {
  const settingsControls = useSettingsControls();
  const { t } = useI18n();
  const supported = settingsControls.capabilities.activityPreview;
  const [autoShow, setAutoShow] = useState<boolean>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const revision = useRef(0);

  useEffect(() => {
    if (!supported) return;
    let active = true;
    const current = ++revision.current;
    const unsubscribe = settingsControls.capabilities.activityPreviewChanges ? settingsControls.onActivityPreviewAutoShowChange(enabled => {
      revision.current += 1;
      setAutoShow(enabled);
    }) : undefined;
    void settingsControls.getActivityPreviewAutoShow!().then(enabled => {
      if (active && current === revision.current) setAutoShow(enabled);
    }).catch(reason => {
      if (active && current === revision.current) setError(reason instanceof Error ? reason.message : t("settings.saveFailed"));
    });
    return () => { active = false; unsubscribe?.(); };
  }, [supported, t]);

  if (!supported) return null;
  const save = async (): Promise<void> => {
    if (autoShow === undefined || busy) return;
    const next = !autoShow;
    setBusy(true);
    setError("");
    revision.current += 1;
    try {
      await settingsControls.setActivityPreviewAutoShow!(next);
      setAutoShow(next);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("settings.saveFailed"));
    } finally { setBusy(false); }
  };

  return (
    <SettingsSection title={t("settings.liveActivity")} testID="settings-live-activity">
      <SettingsGroup>
        <SettingsRow title={t("settings.activityPreviewAutoShow")} description={t("settings.activityPreviewDescription")} error={error}>
          <button className="settings-switch" type="button" role="switch"
            aria-label={t("settings.activityPreviewAutoShow")} aria-checked={autoShow ?? true}
            disabled={autoShow === undefined || busy} onClick={() => void save()}>
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
        </SettingsRow>
      </SettingsGroup>
    </SettingsSection>
  );
}
