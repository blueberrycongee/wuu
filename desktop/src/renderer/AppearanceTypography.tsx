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
  const [sizeDraft, setSizeDraft] = useState(() => String(readAppearance().codeSize));
  useEffect(() => setSizeDraft(String(preferences.codeSize)), [preferences.codeSize]);
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
    const next = Number.isFinite(parsed) ? Math.min(24, Math.max(9, Math.round(parsed))) : preferences.codeSize;
    setSizeDraft(String(next));
    update({ codeSize: next });
  }
  const fonts = [
    { key: "uiFont", label: "settings.uiFont" },
    { key: "codeFont", label: "settings.codeFont" },
  ] as const;
  return <>
    {section === "sizes" && <>
    <SettingsRow title={t("settings.uiSize")} hint={t("settings.uiSizeHint")}><MessageFlowFontSizeControl /></SettingsRow>
    <SettingsRow title={t("settings.codeSize")} hint={t("settings.codeSizeHint")}>
      <input className="settings-input settings-input-num settings-input-num-center" aria-label={t("settings.codeSize")} type="number" min={9} max={24} step={1} value={sizeDraft}
        onChange={(event) => {
          setSizeDraft(event.target.value);
          const value = event.target.valueAsNumber;
          if (Number.isInteger(value) && value >= 9 && value <= 24) update({ codeSize: value });
        }}
        onBlur={(event) => commitSize(event.currentTarget.value)}
        onKeyDown={(event) => { if (event.key === "Enter") event.currentTarget.blur(); }} />
    </SettingsRow>
    </>}
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
