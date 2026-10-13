import { useEffect, useRef, useState } from "react";
import { normalizeAppContextShortcut } from "../shared/appContextShortcut";
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
  const recordingAttempt = useRef(0);
  const [recording, setRecording] = useState(false);
  const [recordingReady, setRecordingReady] = useState(false);
  const stopRecording = (): void => {
    recordingAttempt.current += 1;
    setRecording(false);
    setRecordingReady(false);
    void window.wuu.setAppContextShortcutRecording?.(false).catch(reason => setError((reason as Error).message));
  };

  useEffect(() => {
    window.addEventListener("blur", stopRecording);
    return () => {
      window.removeEventListener("blur", stopRecording);
      recordingAttempt.current += 1;
      void window.wuu.setAppContextShortcutRecording?.(false).catch(() => undefined);
    };
  }, []);

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
  const shortcutLabel = recording ? t("composer.appContext.recordShortcut") : (settings?.shortcut ?? "").split("+")
    .map(part => ({ Command: "⌘", Control: "⌃", Alt: "⌥", Shift: "⇧" }[part] ?? part)).join(" ");
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
          <button className="settings-button" type="button" data-app-context-shortcut
            aria-label={`${t("composer.appContext.shortcut")}: ${shortcutLabel}`} aria-pressed={recording}
            disabled={disabled || !settings || !window.wuu.setAppContextShortcutRecording}
            onClick={async event => {
              if (recording) return;
              event.currentTarget.focus();
              const attempt = ++recordingAttempt.current;
              setError(""); setRecording(true);
              try {
                await window.wuu.setAppContextShortcutRecording!(true);
                if (attempt === recordingAttempt.current) setRecordingReady(true);
              } catch (reason) {
                if (attempt === recordingAttempt.current) { stopRecording(); setError((reason as Error).message); }
              }
            }}
            onBlur={stopRecording}
            onKeyDown={event => {
              if (!recording) return;
              event.stopPropagation();
              if (event.key === "Tab" || event.key === "Escape") {
                if (event.key === "Escape") event.preventDefault();
                stopRecording(); return;
              }
              event.preventDefault();
              if (!recordingReady || event.repeat || ["Meta", "Control", "Alt", "Shift"].includes(event.key)) return;
              // Option can turn letters into symbols on macOS. Use the physical
              // key only when the event no longer contains an accelerator key.
              const key = event.code === "Space" ? "Space" : /^[a-z0-9]$/i.test(event.key) || /^F\d+$/.test(event.key)
                ? event.key : /^(Key[A-Z]|Digit[0-9])$/.test(event.code) ? event.code.replace(/^(Key|Digit)/, "") : event.key;
              try {
                const shortcut = normalizeAppContextShortcut([
                  event.metaKey && "Command", event.ctrlKey && "Control", event.altKey && "Alt", event.shiftKey && "Shift", key,
                ].filter(Boolean).join("+"));
                stopRecording();
                void perform(() => window.wuu.updateAppContextSettings!({ ...settings!, shortcut }), true);
              } catch { setError(t("composer.appContext.shortcutInvalid")); }
            }}>
            {shortcutLabel}
          </button>
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
