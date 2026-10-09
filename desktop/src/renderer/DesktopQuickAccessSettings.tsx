import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { DesktopQuickAccessSnapshot, DesktopQuickAccessUpdate } from "../shared/protocol";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { useI18n } from "./i18n";
import { hostSupports } from "./HostCapabilities";

export function DesktopQuickAccessSettings(): JSX.Element | null {
  const { t } = useI18n();
  const supported = typeof window.wuu?.getDesktopQuickAccess === "function"
    && typeof window.wuu?.updateDesktopQuickAccess === "function"
    && hostSupports("getDesktopQuickAccess") && hostSupports("updateDesktopQuickAccess");
  const [snapshot, setSnapshot] = useState<DesktopQuickAccessSnapshot>();
  const [recording, setRecording] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const recordButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!supported) return;
    let active = true;
    void window.wuu.getDesktopQuickAccess!().then(value => {
      if (active) setSnapshot(value);
    }).catch(() => { if (active) setError(t("quickAccess.loadFailed")); });
    const unsubscribe = window.wuu.onDesktopQuickAccessChange?.(value => setSnapshot(value));
    return () => { active = false; unsubscribe?.(); };
  }, [supported, t]);

  async function save(update: DesktopQuickAccessUpdate): Promise<void> {
    setBusy(true);
    setError("");
    setRecording(false);
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

  function capture(event: KeyboardEvent<HTMLButtonElement>): void {
    if (!recording || event.nativeEvent.isComposing) return;
    if (event.key === "Tab") { setRecording(false); return; }
    event.preventDefault();
    event.stopPropagation();
    if (event.key === "Escape") { setRecording(false); return; }
    if (["Meta", "Control", "Alt", "Shift"].includes(event.key) || event.repeat) return;
    const key = event.code === "Space" ? "Space"
      : /^Key[A-Z]$/.test(event.code) ? event.code.slice(3)
      : /^Digit[0-9]$/.test(event.code) ? event.code.slice(5)
      : event.key.replace(/^Arrow/, "").toUpperCase();
    const normalizedKey = ["UP", "DOWN", "LEFT", "RIGHT"].includes(key) ? key[0] + key.slice(1).toLowerCase() : key;
    if (!(event.metaKey || event.ctrlKey || event.altKey)
      || !/^(?:[A-Z0-9]|F(?:[1-9]|1[0-9]|2[0-4])|Space|Up|Down|Left|Right)$/.test(normalizedKey)) {
      setError(t("quickAccess.invalid"));
      return;
    }
    const modifiers = [
      ...(event.metaKey ? [window.wuu.platform === "darwin" ? "Command" : "Super"] : []),
      ...(event.ctrlKey ? ["Control"] : []),
      ...(event.altKey ? ["Alt"] : []),
      ...(event.shiftKey ? ["Shift"] : []),
    ];
    void save({ shortcut: [...modifiers, normalizedKey].join("+") });
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
            onBlur={() => setRecording(false)} onKeyDown={capture}
            onClick={() => { setError(""); setRecording(value => !value); recordButton.current?.focus(); }}>
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
