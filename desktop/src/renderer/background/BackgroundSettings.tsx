import { useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { SelectMenu } from "../SelectMenu";
import { backgroundEffects, updateBackground, type BackgroundEffect } from "./preferences";
import { processBackground } from "./image";
import { useBackground } from "./useBackground";

export function BackgroundSettings(): JSX.Element {
  const { t } = useI18n();
  const { preferences, error: readError, loading } = useBackground();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<"image" | "save" | null>(null);
  async function save(action: () => Promise<void>) {
    setBusy(true); setError(null);
    try { await action(); } catch { setError("save"); }
    finally { setBusy(false); }
  }
  async function importImage(file: File) {
    setBusy(true); setError(null);
    let image: Blob;
    try { image = await processBackground(file, "none", false); }
    catch { setError("image"); setBusy(false); return; }
    await save(() => updateBackground(current => ({ image, imageID: crypto.randomUUID(), name: file.name, effect: current?.effect ?? "none", opacity: current?.opacity ?? 0.15 })));
  }
  return <>
    <SettingsRow title={t("settings.backgroundImage")} hint={t("settings.backgroundImageHint")}>
      <label className={`settings-button background-file-picker${busy || loading ? " disabled" : ""}`}>
        {t(busy ? "settings.backgroundLoading" : "settings.backgroundChoose")}
        <input type="file" accept="image/png,image/jpeg,image/webp" aria-label={t("settings.backgroundChoose")} disabled={busy || loading}
          onChange={event => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importImage(file); }} />
      </label>
      {preferences && <button type="button" className="settings-button" disabled={busy} onClick={() => void save(() => updateBackground(() => null))}>{t("settings.backgroundRemove")}</button>}
    </SettingsRow>
    {preferences && <>
      <SettingsRow title={t("settings.backgroundEffect")} description={preferences.name}>
        <SelectMenu ariaLabel={t("settings.backgroundEffect")} value={preferences.effect} disabled={busy}
          options={backgroundEffects.map(value => ({ value, label: t(`settings.backgroundEffect.${value}`) }))}
          onChange={value => void save(() => updateBackground(current => current && { ...current, effect: value as BackgroundEffect }))} triggerClassName="settings-select-trigger" />
      </SettingsRow>
      <SettingsRow title={t("settings.backgroundStrength")}>
        <SelectMenu ariaLabel={t("settings.backgroundStrength")} value={String(Math.round(preferences.opacity * 100))} disabled={busy}
          options={[5, 10, 15, 20, 25, 30].map(value => ({ value: String(value), label: `${value}%` }))}
          onChange={value => void save(() => updateBackground(current => current && { ...current, opacity: Number(value) / 100 }))} triggerClassName="settings-select-trigger" />
      </SettingsRow>
    </>}
    {(error || readError) && <p role="alert">{t(error === "image" ? "settings.backgroundInvalid" : "settings.saveFailed")}</p>}
  </>;
}
