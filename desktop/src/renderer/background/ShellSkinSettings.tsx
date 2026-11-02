import { useSettingsControls } from "../SettingsControlsContext";
import { useEffect, useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { SettingsGroup } from "../SettingsSection";
import { BackgroundSettings } from "./BackgroundSettings";
import { useBackground } from "./useBackground";
import { useShellSkins, useSkinImage, type AvailableShellSkin } from "./ShellSkin";

function SkinChip({ skin, mode, selected, choose, title }: {
  skin?: AvailableShellSkin; mode: "light" | "dark"; selected: boolean; choose: () => void;
  title: string;
}): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const loaded = useSkinImage(skin, mode, controls);
  return <button
    type="button"
    role="radio"
    aria-checked={selected}
    className={`shell-skin-chip${selected ? " active" : ""}`}
    onClick={choose}
    title={skin ? (loaded.error ? t("settings.skinImageFailed") : skin.pluginName) : undefined}
  >
    <span>{title}</span>
  </button>;
}

export function ShellSkinSettings(): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { skins, selected } = useShellSkins(controls);
  const { preferences } = useBackground(controls);
  const [mode, setMode] = useState(controls.currentAppliedTheme);
  const [error, setError] = useState(false);
  useEffect(() => {
    setMode(controls.currentAppliedTheme());
    return controls.observeAppliedTheme(setMode);
  }, [controls]);
  useEffect(() => {
    if (!preferences) return;
    const controller = new AbortController();
    void controls.processBackground(preferences.image, mode, controller.signal).catch(() => {});
    return () => { controller.abort(); };
  }, [controls, preferences?.image, mode]);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  const unavailable = !["default", "custom"].includes(selected) && !skins.some(skin => skin.key === selected);
  const hasMultipleSkins = skins.length > 0 || Boolean(preferences) || unavailable;
  return <SettingsGroup>
    {hasMultipleSkins ? (
      <SettingsRow title={t("settings.shellSkin")}>
        <div className="shell-skin-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
          <SkinChip mode={mode} selected={selected === "default" || (selected === "custom" && !preferences)} choose={() => choose("default")} title={t("settings.skinDefault")} />
          {skins.map(skin => <SkinChip key={skin.key} skin={skin} mode={mode} selected={selected === skin.key} choose={() => choose(skin.key)} title={skin.name} />)}
          {preferences && <SkinChip mode={mode} selected={selected === "custom"} choose={() => choose("custom")} title={t("settings.skinCustom")} />}
        </div>
      </SettingsRow>
    ) : (
      <div className="shell-skin-options" role="radiogroup" aria-label={t("settings.shellSkin")} hidden>
        <SkinChip mode={mode} selected={true} choose={() => choose("default")} title={t("settings.skinDefault")} />
      </div>
    )}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
    <BackgroundSettings />
  </SettingsGroup>;
}
