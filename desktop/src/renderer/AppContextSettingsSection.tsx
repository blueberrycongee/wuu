import { useEffect, useRef, useState } from "react";
import type { AppContextSettings, AppContextState } from "../shared/protocol";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function AppContextSettingsSection(): JSX.Element | null {
  const { t } = useI18n();
  const supported = window.wuu?.platform === "darwin" && Boolean(window.wuu.getAppContextState);
  const [state, setState] = useState<AppContextState>();
  const [settings, setSettings] = useState<AppContextSettings>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);

  useEffect(() => {
    if (!supported) return;
    let active = true;
    const refresh = async (): Promise<void> => {
      const current = ++generation.current;
      try {
        const next = await window.wuu.getAppContextState!();
        if (!active || current !== generation.current) return;
        setState(next);
        setSettings(value => value ?? next.settings);
      } catch (reason) {
        if (active && current === generation.current) setError((reason as Error).message);
      }
    };
    const unsubscribe = window.wuu.onAppContextChanged?.(() => { void refresh(); });
    void refresh();
    return () => { active = false; unsubscribe?.(); };
  }, [supported]);

  if (!supported) return null;
  const disabled = busy || !state?.available;
  const perform = async (operation: () => Promise<AppContextState>, save = false): Promise<void> => {
    generation.current += 1;
    setBusy(true);
    setError("");
    try {
      const next = await operation();
      generation.current += 1;
      setState(next);
      if (save) setSettings(next.settings);
    } catch (reason) { setError((reason as Error).message); }
    finally { setBusy(false); }
  };

  return (
    <SettingsSection title={t("composer.appContext.title")} testID="settings-app-context">
      <SettingsGroup>
        <SettingsRow title={t("composer.appContext.enable")}>
          <button className="settings-switch" type="button" role="switch" aria-checked={settings?.enabled ?? false}
            aria-label={t("composer.appContext.enable")} disabled={disabled || !settings}
            onClick={() => settings && void perform(() => window.wuu.updateAppContextSettings!({ ...settings, enabled: !settings.enabled }), true)}>
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
        </SettingsRow>
        <SettingsRow title={t("composer.appContext.shortcut")}>
          <input className="settings-input" aria-label={t("composer.appContext.shortcut")} value={settings?.shortcut ?? ""}
            disabled={disabled || !settings} spellCheck={false}
            onChange={event => settings && setSettings({ ...settings, shortcut: event.target.value })}
            onBlur={() => settings && settings.shortcut !== state?.settings.shortcut && void perform(() => window.wuu.updateAppContextSettings!(settings), true)}
            onKeyDown={event => { if (event.key === "Enter") event.currentTarget.blur(); }} />
        </SettingsRow>
        <SettingsRow title={t("composer.appContext.includeText")}>
          <button className="settings-switch" type="button" role="switch" aria-checked={settings?.include_text ?? false}
            aria-label={t("composer.appContext.includeText")} disabled={disabled || !settings}
            onClick={() => settings && void perform(() => window.wuu.updateAppContextSettings!({ ...settings, include_text: !settings.include_text }), true)}>
            <span className="settings-switch-thumb" aria-hidden="true" />
          </button>
        </SettingsRow>
        <SettingsRow title={t("settings.appContext.permissions")}>
          <div className="settings-row-control-block">
            <button className="settings-button" type="button" disabled={disabled}
              onClick={() => void perform(() => window.wuu.requestAppContextPermission!("screen"))}>{t("composer.appContext.screenPermission")}</button>
            {settings?.include_text ? <button className="settings-button" type="button" disabled={disabled}
              onClick={() => void perform(() => window.wuu.requestAppContextPermission!("text"))}>{t("composer.appContext.textPermission")}</button> : null}
          </div>
        </SettingsRow>
      </SettingsGroup>
      {error || state?.error ? <p className="settings-error" role="alert">{error || state?.error}</p> : null}
      {state && !state.available ? <p className="settings-error" role="alert">{t("composer.appContext.unavailable")}</p> : null}
    </SettingsSection>
  );
}
