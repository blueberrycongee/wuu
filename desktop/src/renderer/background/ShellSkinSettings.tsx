import { useSettingsControls } from "../SettingsControlsContext";
import { useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { useI18n } from "../i18n";
import { SettingsGroup } from "../SettingsSection";
import { BackgroundSettings } from "./BackgroundSettings";
import { useBackground } from "./useBackground";
import { useShellSkins, useSkinImage, type AvailableShellSkin } from "./ShellSkin";

function SkinCard({ skin, mode, selected, choose, title, image }: {
  skin?: AvailableShellSkin; mode: "light" | "dark"; selected: boolean; choose: () => void;
  title: string; image?: string;
}): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const loaded = useSkinImage(skin, mode, controls);
  const variant = skin?.[mode];
  const url = loaded.url || image;
  const style: CSSProperties = {
    backgroundColor: variant?.background ?? "var(--sidebar-material-fill)",
    color: variant?.foreground ?? (mode === "light" ? "#171719" : "#f5f5f7"),
    backgroundImage: url ? `linear-gradient(${variant?.overlay ?? "transparent"}, ${variant?.overlay ?? "transparent"}), url(${JSON.stringify(url)})` : undefined,
    backgroundPosition: variant?.position ?? "center",
  };
  return <button type="button" role="radio" aria-checked={selected} className="shell-skin-card" onClick={choose}>
    <span className="shell-skin-preview" style={style} aria-hidden="true">
      <span className="shell-skin-preview-rail"><i /><i /><i /></span>
      <span className="shell-skin-preview-page"><i /><i /><i /></span>
    </span>
    <span>{title}</span>
    {skin && <small>{loaded.error ? t("settings.skinImageFailed") : skin.pluginName}</small>}
  </button>;
}

export function ShellSkinSettings({ children }: { children?: ReactNode }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { skins, selected } = useShellSkins(controls);
  const { preferences } = useBackground(controls);
  const [mode, setMode] = useState(controls.currentAppliedTheme);
  const [image, setImage] = useState("");
  const [error, setError] = useState(false);
  useEffect(() => {
    setMode(controls.currentAppliedTheme());
    return controls.observeAppliedTheme(setMode);
  }, [controls]);
  useEffect(() => {
    if (!preferences) { setImage(""); return; }
    const controller = new AbortController();
    let url = "";
    setImage("");
    void controls.processBackground(preferences.image, mode, controller.signal).then(blob => {
      if (controller.signal.aborted) return;
      url = URL.createObjectURL(blob); setImage(url);
    }).catch(() => {});
    return () => { controller.abort(); if (url) URL.revokeObjectURL(url); };
  }, [controls, preferences?.image, mode]);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  const unavailable = !["default", "custom"].includes(selected) && !skins.some(skin => skin.key === selected);
  return <>
    {(skins.length > 0 || preferences || unavailable) && <div className="settings-form">
      <span className="settings-row-label-title">{t("settings.shellSkin")}</span>
      <div className="shell-skin-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
        <SkinCard mode={mode} selected={selected === "default" || (selected === "custom" && !preferences)} choose={() => choose("default")} title={t("settings.skinDefault")} />
        {skins.map(skin => <SkinCard key={skin.key} skin={skin} mode={mode} selected={selected === skin.key} choose={() => choose(skin.key)} title={skin.name} />)}
        {preferences && <SkinCard mode={mode} selected={selected === "custom"} choose={() => choose("custom")} title={t("settings.skinCustom")} image={image} />}
      </div>
    </div>}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
    <SettingsGroup><BackgroundSettings />{children}</SettingsGroup>
  </>;
}
