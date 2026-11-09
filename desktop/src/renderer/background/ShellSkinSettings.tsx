import { useSettingsControls } from "../SettingsControlsContext";
import { useEffect, useState } from "react";
import { useI18n } from "../i18n";
import { showActionErrorToast } from "../Toast";
import { SettingsRow } from "../SettingsRow";
import { BackgroundSettings } from "./BackgroundSettings";
import { showsUserImage, useShellSkins, useSkinImage, type AvailableShellSkin } from "./ShellSkin";

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
  useEffect(() => {
    setMode(controls.currentAppliedTheme());
    return controls.observeAppliedTheme(setMode);
  }, [controls]);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); } catch (reason) { showActionErrorToast(reason, t("settings.saveFailed")); }
  };
  const skin = skins.find(candidate => candidate.key === selected);
  const unavailable = selected !== "default" && !skin;
  return <>
    {(skins.length > 0 || unavailable) && (
      <SettingsRow title={t("settings.shellSkin")} description={t("settings.shellSkinDescription")}>
        <div className="theme-segmented shell-skin-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
          <SkinChip mode={mode} selected={selected === "default"} choose={() => choose("default")} title={t("settings.skinDefault")} />
          {skins.map(skin => <SkinChip key={skin.key} skin={skin} mode={mode} selected={selected === skin.key} choose={() => choose(skin.key)} title={skin.name} />)}
        </div>
      </SettingsRow>
    )}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {/* The picture is the selected skin's artwork, so only skins that can show it offer it. */}
    {showsUserImage(skin) && <BackgroundSettings canvasSkin={skin?.placement === "canvas" ? skin.name : undefined} />}
  </>;
}
