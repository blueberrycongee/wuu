import { useEffect, useRef, useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { updateBackground } from "./preferences";
import { processBackground } from "./image";
import { useBackground } from "./useBackground";
import { selectShellSkin } from "./ShellSkin";

export function BackgroundSettings(): JSX.Element {
  const { t } = useI18n();
  const { preferences, error: readError, loading } = useBackground();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<"image" | "save" | null>(null);
  const importController = useRef<AbortController | null>(null);
  useEffect(() => () => importController.current?.abort(), []);
  async function save(action: () => Promise<void>) {
    setBusy(true); setError(null);
    try { await action(); } catch { setError("save"); }
    finally { setBusy(false); }
  }
  async function importImage(file: File) {
    importController.current?.abort();
    const controller = new AbortController();
    importController.current = controller;
    const { signal } = controller;
    setBusy(true); setError(null);
    let stage: "image" | "save" = "image";
    try {
      const image = await processBackground(file, "import", signal);
      if (signal.aborted) return;
      stage = "save";
      await updateBackground(current => {
        // Opening IndexedDB can outlive this page; recheck before changing storage.
        if (signal.aborted) return current;
        return { image, imageID: crypto.randomUUID(), name: file.name };
      });
      if (!signal.aborted) selectShellSkin("custom");
    } catch {
      if (!signal.aborted) setError(stage);
    } finally {
      if (importController.current === controller) {
        importController.current = null;
        if (!signal.aborted) setBusy(false);
      }
    }
  }
  return <>
    <SettingsRow title={t("settings.backgroundImage")} description={preferences?.name}>
      <label className={`settings-button background-file-picker${busy || loading ? " disabled" : ""}`}>
        {t(busy ? "settings.backgroundLoading" : "settings.backgroundChoose")}
        <input type="file" accept="image/png,image/jpeg,image/webp" aria-label={t("settings.backgroundChoose")} disabled={busy || loading}
          onChange={event => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importImage(file); }} />
      </label>
      {preferences && <button type="button" className="settings-button" disabled={busy} onClick={() => void save(() => updateBackground(() => null))}>{t("settings.backgroundRemove")}</button>}
    </SettingsRow>
    {(error || readError) && <p role="alert">{t(error === "image" ? "settings.backgroundInvalid" : "settings.saveFailed")}</p>}
  </>;
}
