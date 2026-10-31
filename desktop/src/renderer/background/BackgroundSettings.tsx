import { useSettingsControls } from "../SettingsControlsContext";
import { useEffect, useMemo, useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { useBackground } from "./useBackground";
import { isPluginLifecycleCancellation } from "../../shared/PluginLifecycle";

export function BackgroundSettings(): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { preferences, error: readError, loading } = useBackground(controls);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<"image" | "save" | null>(null);
  const operations = useMemo(() => ({ active: true, importer: null as AbortController | null }), [controls]);
  useEffect(() => {
    operations.active = true;
    setBusy(false);
    setError(null);
    return () => {
      operations.active = false;
      operations.importer?.abort();
    };
  }, [operations]);
  async function save(action: () => Promise<void>) {
    if (!operations.active) return;
    setBusy(true); setError(null);
    try { await action(); } catch (reason) {
      if (operations.active && !isPluginLifecycleCancellation(reason)) setError("save");
    } finally { if (operations.active) setBusy(false); }
  }
  async function importImage(file: File) {
    if (!operations.active) return;
    operations.importer?.abort();
    const controller = new AbortController();
    operations.importer = controller;
    const { signal } = controller;
    setBusy(true); setError(null);
    let stage: "image" | "save" = "image";
    try {
      const image = await controls.processBackground(file, "import", signal);
      if (signal.aborted) return;
      stage = "save";
      await controls.updateBackground(current => {
        // Opening IndexedDB can outlive this page; recheck before changing storage.
        if (signal.aborted) return current;
        return { image, imageID: crypto.randomUUID(), name: file.name };
      });
      if (!signal.aborted) controls.selectShellSkin("custom");
    } catch (reason) {
      if (!signal.aborted && !isPluginLifecycleCancellation(reason)) setError(stage);
    } finally {
      if (operations.importer === controller) {
        operations.importer = null;
        if (!signal.aborted) setBusy(false);
      }
    }
  }
  return <>
    <SettingsRow title={t("settings.backgroundImage")} description={preferences?.name} hint={t("settings.shellSkinDescription")}>
      <label className={`settings-button background-file-picker${busy || loading ? " disabled" : ""}`}>
        {t(busy ? "settings.backgroundLoading" : "settings.backgroundChoose")}
        <input type="file" accept="image/png,image/jpeg,image/webp" aria-label={t("settings.backgroundChoose")} disabled={busy || loading}
          onChange={event => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importImage(file); }} />
      </label>
      {preferences && <button type="button" className="settings-button" disabled={busy} onClick={() => void save(() => controls.updateBackground(() => null))}>{t("settings.backgroundRemove")}</button>}
    </SettingsRow>
    {(error || readError) && <p role="alert">{t(error === "image" ? "settings.backgroundInvalid" : "settings.saveFailed")}</p>}
  </>;
}
