import { useSettingsControls } from "../SettingsControlsContext";
import { useState } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { BackgroundSettings } from "./BackgroundSettings";
import { SkinPreviewCard } from "./ShellSkinCard";
import { showsUserImage, useShellSkins } from "./ShellSkin";

// The row stays a three-card summary: the default frame, the current choice
// (or the first featured skin), and the entry into the full gallery.
const FEATURED_SKINS = 1;

function MoreCard({ count, label, open }: { count: number; label: string; open: () => void }): JSX.Element {
  return <button type="button" className="settings-theme-card" onClick={open}>
    <span className="settings-theme-preview settings-skin-more-box" aria-hidden="true">
      <span>{`+${count}`}</span>
    </span>
    <span className="settings-theme-card-label">{label}</span>
  </button>;
}

export function ShellSkinSettings({ onOpenGallery }: { onOpenGallery?: () => void }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { skins, selected } = useShellSkins(controls);
  const [error, setError] = useState(false);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  const skin = skins.find(candidate => candidate.key === selected);
  const unavailable = selected !== "default" && !skin;
  const featured = skins.slice(0, FEATURED_SKINS);
  const representative = skin ?? featured[0];
  const extra = skins.length - (representative ? 1 : 0);
  const canBrowse = onOpenGallery && skins.length > (representative ? 1 : 0);
  return <>
    {(skins.length > 0 || unavailable) && (
      <SettingsRow block title={t("settings.shellSkin")} description={t("settings.shellSkinDescription")}>
        <div className="settings-theme-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
          <SkinPreviewCard selected={selected === "default"} choose={() => choose("default")} title={t("settings.skinDefault")} />
          {representative && <SkinPreviewCard key={representative.key} skin={representative} selected={selected === representative.key} choose={() => choose(representative.key)} title={representative.name} />}
          {canBrowse && <MoreCard count={extra} label={t("settings.skinMore")} open={onOpenGallery} />}
        </div>
      </SettingsRow>
    )}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
    {/* The picture is the selected skin's artwork, so only skins that can show it offer it. */}
    {showsUserImage(skin) && <BackgroundSettings canvasSkin={skin?.placement === "canvas" ? skin.name : undefined} />}
  </>;
}
