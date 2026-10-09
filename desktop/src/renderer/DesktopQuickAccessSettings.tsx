import { useEffect, useRef, useState } from "react";
import type { DesktopQuickAccessSnapshot, DesktopQuickAccessUpdate } from "../shared/protocol";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { useI18n } from "./i18n";
import { hostSupports } from "./HostCapabilities";

let nextRecordingID = 0;

export function DesktopQuickAccessSettings(): JSX.Element | null {
  const { t } = useI18n();
  const supported = typeof window.wuu?.getDesktopQuickAccess === "function"
    && typeof window.wuu?.updateDesktopQuickAccess === "function"
    && typeof window.wuu?.setDesktopQuickAccessRecording === "function"
    && typeof window.wuu?.onDesktopQuickAccessRecorded === "function"
    && hostSupports("getDesktopQuickAccess") && hostSupports("updateDesktopQuickAccess")
    && hostSupports("setDesktopQuickAccessRecording") && hostSupports("onDesktopQuickAccessRecorded");
  const [snapshot, setSnapshot] = useState<DesktopQuickAccessSnapshot>();
  const [recording, setRecording] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const recordButton = useRef<HTMLButtonElement>(null);
  const activeRecordingID = useRef<number | null>(null);
  useEffect(() => {
    if (!supported) return;
    let active = true;
    void window.wuu.getDesktopQuickAccess!().then(value => {
      if (active) setSnapshot(value);
    }).catch(() => { if (active) setError(t("quickAccess.loadFailed")); });
    const unsubscribe = window.wuu.onDesktopQuickAccessChange?.(value => setSnapshot(value));
    const unsubscribeRecording = window.wuu.onDesktopQuickAccessRecorded!(event => {
      if (event.recordingID !== activeRecordingID.current) return;
      if (event.cancelled) stopRecording();
      else if (event.error) setError(t(event.error === "reserved" ? "quickAccess.reserved" : "quickAccess.invalid"));
      else void save({ shortcut: event.shortcut });
    });
    return () => {
      active = false;
      unsubscribe?.();
      unsubscribeRecording();
      const recordingID = activeRecordingID.current;
      activeRecordingID.current = null;
      if (recordingID !== null) void window.wuu.setDesktopQuickAccessRecording!(recordingID, false).catch(() => undefined);
    };
  }, [supported, t]);

  async function save(update: DesktopQuickAccessUpdate): Promise<void> {
    setBusy(true);
    setError("");
    stopRecording();
    try {
      const result = await window.wuu.updateDesktopQuickAccess!(update);
      setSnapshot(result.snapshot);
      if (result.error) setError(t(result.error === "invalid_shortcut" ? "quickAccess.invalid" : "quickAccess.unavailable"));
    } catch {
      setError(t("quickAccess.saveFailed"));
    } finally {
      setBusy(false);
    }
  }

  function stopRecording(): void {
    const recordingID = activeRecordingID.current;
    activeRecordingID.current = null;
    setRecording(false);
    if (recordingID !== null) void window.wuu.setDesktopQuickAccessRecording!(recordingID, false).catch(() => undefined);
  }

  async function toggleRecording(): Promise<void> {
    if (activeRecordingID.current !== null) { stopRecording(); return; }
    setError("");
    const recordingID = ++nextRecordingID;
    activeRecordingID.current = recordingID;
    recordButton.current?.focus();
    try {
      await window.wuu.setDesktopQuickAccessRecording!(recordingID, true);
      if (activeRecordingID.current === recordingID) setRecording(true);
    } catch {
      if (activeRecordingID.current !== recordingID) return;
      activeRecordingID.current = null;
      setRecording(false);
      setError(t("quickAccess.recordFailed"));
    }
  }

  if (!supported) return null;
  return <SettingsSection title={t("quickAccess.title")} testID="settings-quick-access">
    <SettingsGroup>
      <SettingsRow title={t("quickAccess.shortcut")} description={t("quickAccess.description")}
        error={error || (snapshot?.shortcutStatus === "unavailable" ? t("quickAccess.unavailable") : undefined)} block>
        <div className="settings-quick-access-controls">
          <button ref={recordButton} className="settings-button" type="button"
            data-testid="quick-access-record" disabled={!snapshot || busy}
            aria-label={t("quickAccess.record")} aria-pressed={recording}
            onBlur={stopRecording} onClick={() => void toggleRecording()}>
            {recording ? t("quickAccess.recording") : snapshot?.shortcut || t("quickAccess.disabled")}
          </button>
          <button className="settings-button settings-button-ghost" type="button" disabled={!snapshot || busy}
            data-testid="quick-access-default" onClick={() => void save({ shortcut: snapshot!.defaultShortcut })}>
            {t("quickAccess.useDefault")}
          </button>
          <button className="settings-button settings-button-ghost" type="button" disabled={!snapshot?.shortcut || busy}
            data-testid="quick-access-disable" onClick={() => void save({ shortcut: "" })}>
            {t("quickAccess.disable")}
          </button>
        </div>
        {recording ? <span className="settings-row-label-description" role="status">
          {t("quickAccess.recordHint")}
        </span> : null}
      </SettingsRow>
      <SettingsRow title={t("quickAccess.popOutOnTop")} description={t("quickAccess.popOutDescription")}>
        <button className="settings-switch" type="button" role="switch" data-testid="quick-access-on-top"
          aria-label={t("quickAccess.popOutOnTop")} aria-checked={snapshot?.popOutAlwaysOnTop ?? false}
          disabled={!snapshot || busy} onClick={() => void save({ popOutAlwaysOnTop: !snapshot!.popOutAlwaysOnTop })}>
          <span className="settings-switch-thumb" aria-hidden="true" />
        </button>
      </SettingsRow>
    </SettingsGroup>
  </SettingsSection>;
}
