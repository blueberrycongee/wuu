import { useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { appearanceDefaults, observeAppearance, readAppearance, saveAppearance, type AppearancePreferences } from "./AppearancePreferences";
import { MessageFlowFontSizeControl } from "./MessageFlowFontSizeSection";
import { SelectMenu } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { isMonospaceFont, listLocalFonts } from "./LocalFonts";

export function AppearanceTypography({ section = "sizes" }: { section?: "sizes" | "fonts" | "motion" }): JSX.Element {
  const { t } = useI18n();
  const [preferences, setPreferences] = useState(readAppearance);
  const [sizeDraft, setSizeDraft] = useState(() => String(readAppearance().uiSize));
  useEffect(() => setSizeDraft(String(preferences.uiSize)), [preferences.uiSize]);
  const [error, setError] = useState(false);
  const [localFonts, setLocalFonts] = useState<string[]>([]);
  const [monoFonts, setMonoFonts] = useState<string[]>([]);
  const [fontStatus, setFontStatus] = useState<"idle" | "loading" | "ready" | "error">("idle");
  async function loadFonts() {
    if (fontStatus === "loading" || fontStatus === "ready") return;
    setFontStatus("loading");
    try {
      const names = await listLocalFonts();
      setLocalFonts(names);
      setMonoFonts(names.filter(isMonospaceFont));
      setFontStatus("ready");
    } catch { setFontStatus("error"); }
  }
  useEffect(() => observeAppearance(() => setPreferences(readAppearance())), []);
  function update(patch: Partial<AppearancePreferences>) {
    try {
      saveAppearance({ ...readAppearance(), ...patch });
      setError(false);
    } catch { setError(true); }
  }
  function commitSize(raw: string) {
    const parsed = raw.trim() ? Number(raw) : NaN;
    const next = Number.isFinite(parsed) ? Math.min(16, Math.max(12, Math.round(parsed))) : preferences.uiSize;
    setSizeDraft(String(next));
    update({ uiSize: next });
  }
  const fonts = [
    { key: "uiFont", label: "settings.uiFont" },
    { key: "codeFont", label: "settings.codeFont" },
  ] as const;
  return <>
    {section === "sizes" && <>
    <SettingsRow title={t("settings.uiSize")} hint={t("settings.uiSizeHint")}>
      <input className="settings-input settings-input-num settings-input-num-center" aria-label={t("settings.uiSize")} type="number" min={12} max={16} step={1} value={sizeDraft}
        onChange={(event) => {
          setSizeDraft(event.target.value);
          const value = event.target.valueAsNumber;
          if (Number.isInteger(value) && value >= 12 && value <= 16) update({ uiSize: value });
        }}
        onBlur={(event) => commitSize(event.currentTarget.value)}
        onKeyDown={(event) => { if (event.key === "Enter") event.currentTarget.blur(); }} />
    </SettingsRow>
    <SettingsRow title={t("settings.contentSize")} hint={t("settings.contentSizeHint")}><MessageFlowFontSizeControl /></SettingsRow></>}
    {section === "fonts" && <>{fonts.map(({ key, label }) => {
      const names = key === "codeFont" ? monoFonts : localFonts;
      const current = preferences[key];
      const options = [{ value: "", label: t("settings.systemFont") }, ...names.map((name) => ({ value: name, label: name }))];
      if (current && !names.includes(current)) options.push({ value: current, label: current });
      return <SettingsRow key={key} title={t(label)} hint={t(key === "uiFont" ? "settings.uiFontHint" : "settings.codeFontHint")}>
        <div className="appearance-font-picker" onClickCapture={() => { void loadFonts(); }} onKeyDownCapture={(event) => { if (["Enter", " ", "ArrowDown", "ArrowUp"].includes(event.key)) void loadFonts(); }}>
          <SelectMenu ariaLabel={t(label)} value={current} onChange={(value) => update({ [key]: value })} options={options} searchable triggerClassName="settings-select-trigger" />
        </div>
      </SettingsRow>;
    })}
    {(fontStatus === "loading" || fontStatus === "error") && <p className="settings-row-label-description" role="status">{t(fontStatus === "error" ? "settings.fontLoadFailed" : "settings.fontLoading")}</p>}
    <SettingsRow title={t("settings.resetFonts")} hint={t("settings.resetFontsHint")}><button type="button" className="settings-button" onClick={() => update({ uiFont: appearanceDefaults.uiFont, codeFont: appearanceDefaults.codeFont })}>{t("settings.resetFonts")}</button></SettingsRow></>}
    {section === "motion" && <SettingsRow title={t("settings.reducedMotion")} hint={t("settings.motionHint")}>
      <SelectMenu ariaLabel={t("settings.reducedMotion")} value={preferences.motion} onChange={(value) => update({ motion: value as AppearancePreferences["motion"] })} options={[{ value: "system", label: t("settings.followSystem") }, { value: "reduce", label: t("settings.motionReduce") }]} triggerClassName="settings-select-trigger" />
    </SettingsRow>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
  </>;
}
