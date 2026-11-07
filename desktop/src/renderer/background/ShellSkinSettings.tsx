import { useSettingsControls } from "../SettingsControlsContext";
import { useEffect, useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { BackgroundSettings } from "./BackgroundSettings";
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
    className="shell-skin-chip"
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
  const [mode, setMode] = useState(controls.currentAppliedTheme);
  const [error, setError] = useState(false);
  useEffect(() => {
    setMode(controls.currentAppliedTheme());
    return controls.observeAppliedTheme(setMode);
  }, [controls]);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  const unavailable = selected !== "default" && !skins.some(skin => skin.key === selected);
  return <>
    {(skins.length > 0 || unavailable) && (
      <SettingsRow title={t("settings.shellSkin")}>
        <div className="theme-segmented shell-skin-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
          <SkinChip mode={mode} selected={selected === "default"} choose={() => choose("default")} title={t("settings.skinDefault")} />
          {skins.map(skin => <SkinChip key={skin.key} skin={skin} mode={mode} selected={selected === skin.key} choose={() => choose(skin.key)} title={skin.name} />)}
        </div>
      </SettingsRow>
    )}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
    <BackgroundSettings />
  </>;
}
