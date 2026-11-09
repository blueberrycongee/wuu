import { useSettingsControls } from "./SettingsControlsContext";
import type { SettingsAppearanceV1 as AppearancePreferences } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { MessageFlowFontSizeControl } from "./MessageFlowFontSizeSection";
import { SelectMenu } from "./SelectMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { Switch } from "./ui/Switch";
import { SegmentedControl } from "./ui/SegmentedControl";
import { SettingsStepper } from "./ui/SettingsStepper";
import { showActionErrorToast } from "./Toast";

export function AppearanceTypography({ section = "text" }: { section?: "text" | "motion" }): JSX.Element {
  const controls = useSettingsControls();
  const { appearanceDefaults, readAppearance, saveAppearance, observeAppearance } = controls;
  const { t } = useI18n();
  const [preferences, setPreferences] = useState(readAppearance);
  const [sizeDraft, setSizeDraft] = useState(() => String(readAppearance().codeSize));
  useEffect(() => setSizeDraft(String(preferences.codeSize)), [preferences.codeSize]);
  const [localFonts, setLocalFonts] = useState<string[]>([]);
  const [monoFonts, setMonoFonts] = useState<string[]>([]);
  const [fontStatus, setFontStatus] = useState<"idle" | "loading" | "ready" | "error">("idle");
  async function loadFonts() {
    if (fontStatus === "loading" || fontStatus === "ready") return;
    setFontStatus("loading");
    try {
      const fonts = await controls.listLocalFonts();
      setLocalFonts(fonts.names);
      setMonoFonts(fonts.monospace);
      setFontStatus("ready");
    } catch { setFontStatus("error"); }
  }
  useEffect(() => {
    setPreferences(readAppearance());
    return observeAppearance(() => setPreferences(readAppearance()));
  }, [observeAppearance, readAppearance]);
  function update(patch: Partial<AppearancePreferences>) {
    try {
      saveAppearance({ ...readAppearance(), ...patch });
    } catch (reason) { showActionErrorToast(reason, t("settings.saveFailed")); }
  }
  function commitSize(raw: string) {
    const parsed = raw.trim() ? Number(raw) : NaN;
    const next = Number.isFinite(parsed) ? Math.min(24, Math.max(9, Math.round(parsed))) : preferences.codeSize;
    setSizeDraft(String(next));
    update({ codeSize: next });
  }
  if (section === "motion") {
    const reduce = preferences.motion === "reduce";
    return <SettingsSection title={t("settings.sectionMotion")} testID="settings-motion">
      <SettingsGroup>
        <SettingsRow title={t("settings.reduceMotion")}>
          <Switch checked={reduce} aria-label={t("settings.reduceMotion")}
            onClick={() => update({ motion: reduce ? "system" : "reduce" })} />
        </SettingsRow>
      </SettingsGroup>
    </SettingsSection>;
  }
  const fontPicker = (key: "uiFont" | "codeFont"): JSX.Element => {
    const label = key === "codeFont" ? "settings.codeFont" : "settings.uiFont";
    const names = key === "codeFont" ? monoFonts : localFonts;
    const current = preferences[key];
    const defaultLabel = t("settings.systemFont");
    const options = [{ value: "", label: defaultLabel }, ...names.map((name) => ({ value: name, label: name }))];
    if (current && !names.includes(current)) options.push({ value: current, label: current });
    return <div className="appearance-font-picker" onClickCapture={() => { void loadFonts(); }} onKeyDownCapture={(event) => { if (["Enter", " ", "ArrowDown", "ArrowUp"].includes(event.key)) void loadFonts(); }}>
      <SelectMenu ariaLabel={t(label)} value={current} onChange={(value) => update(key === "uiFont" ? { uiFont: value, contentFont: "" } : { codeFont: value })} options={options} searchable
        triggerClassName="settings-select-trigger" panelClassName="appearance-font-menu" minMenuWidthEm={20} />
    </div>;
  };
  const fontsAreDefault = preferences.uiFont === appearanceDefaults.uiFont && preferences.contentFont === appearanceDefaults.contentFont && preferences.codeFont === appearanceDefaults.codeFont;
  // Resetting is offered only when there is something to reset.
  return <SettingsSection
    title={t("settings.sectionText")}
    testID="settings-typography"
    actions={fontsAreDefault ? undefined : <button type="button" className="settings-button settings-button-ghost"
      onClick={() => update({ uiFont: appearanceDefaults.uiFont, contentFont: appearanceDefaults.contentFont, codeFont: appearanceDefaults.codeFont })}>{t("settings.resetFonts")}</button>}
  >
    <SettingsGroup>
      <SettingsRow title={t("settings.messageStyle")}>
        <SegmentedControl label={t("settings.messageStyle")} value={preferences.messageStyle}
          options={[
            { value: "bubble", label: t("settings.messageStyleBubble") },
            { value: "im", label: t("settings.messageStyleIM") },
          ]}
          onChange={(messageStyle) => update({ messageStyle })} />
      </SettingsRow>
      <SettingsRow title={t("settings.uiFont")} description={t("settings.uiFontHint")}>
        {fontPicker("uiFont")}
      </SettingsRow>
      <SettingsRow title={t("settings.uiSize")} description={t("settings.uiSizeHint")}>
        <MessageFlowFontSizeControl />
      </SettingsRow>
      <SettingsRow title={t("settings.codeFont")} description={t("settings.codeFontHint")}>
        {fontPicker("codeFont")}
      </SettingsRow>
      <SettingsRow title={t("settings.codeSize")} description={t("settings.codeSizeHint")}>
        <SettingsStepper onStep={(direction) => commitSize(String(preferences.codeSize + direction))} atMin={preferences.codeSize <= 9} atMax={preferences.codeSize >= 24}>
          <input className="settings-input settings-stepper-input" aria-label={t("settings.codeSize")} type="number" min={9} max={24} step={1} value={sizeDraft}
            onChange={(event) => {
              setSizeDraft(event.target.value);
              const value = event.target.valueAsNumber;
              if (Number.isInteger(value) && value >= 9 && value <= 24) update({ codeSize: value });
            }}
            onBlur={(event) => commitSize(event.currentTarget.value)}
            onKeyDown={(event) => { if (event.key === "Enter") event.currentTarget.blur(); }} />
        </SettingsStepper>
      </SettingsRow>
      {controls.capabilities.platform === "darwin" && (
        <SettingsRow title={t("settings.fontSmoothing")}>
          <SegmentedControl label={t("settings.fontSmoothing")} value={preferences.fontSmoothing}
            options={[
              { value: "grayscale", label: t("settings.fontSmoothingGrayscale") },
              { value: "system", label: t("settings.systemFont") },
            ]}
            onChange={(fontSmoothing) => update({ fontSmoothing })} />
        </SettingsRow>
      )}
    </SettingsGroup>
    {(fontStatus === "loading" || fontStatus === "error") && <p className="settings-section-note" role="status">{t(fontStatus === "error" ? "settings.fontLoadFailed" : "settings.fontLoading")}</p>}
  </SettingsSection>;
}
