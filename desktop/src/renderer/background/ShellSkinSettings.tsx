import { useSettingsControls } from "../SettingsControlsContext";
import { useState, type CSSProperties } from "react";
import { useI18n } from "../i18n";
import { SettingsRow } from "../SettingsRow";
import { BackgroundSettings } from "./BackgroundSettings";
import { showsUserImage, useShellSkins, useSkinImage, type AvailableShellSkin } from "./ShellSkin";

// Cards before the more entry: 默认 + this many skins.
const FEATURED_SKINS = 4;
const FALLBACK = {
  light: { canvas: "#fcfcfd", ink: "#151515" },
  dark: { canvas: "#191a1c", ink: "#e0e1e3" },
};
const line = (ink: string, canvas: string, alpha: number): CSSProperties => ({
  background: `color-mix(in srgb, ${ink} ${Math.round(alpha * 100)}%, ${canvas})`,
});

function PreviewHalf({ canvas, ink, url }: { canvas: string; ink: string; url: string }): JSX.Element {
  return <span className="settings-skin-half" style={{ background: canvas }}>
    {url ? <img src={url} alt="" /> : null}
    <i className="settings-skin-half-rail" style={line(ink, canvas, 0.10)} />
    <span className="settings-skin-half-lines">
      <i style={{ width: "46%", ...line(ink, canvas, 0.30) }} />
      <i style={{ width: "74%", ...line(ink, canvas, 0.16) }} />
      <i style={{ width: "34%", ...line(ink, canvas, 0.16) }} />
    </span>
  </span>;
}

function SkinCard({ skin, selected, choose, title }: {
  skin?: AvailableShellSkin; selected: boolean; choose: () => void; title: string;
}): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const light = useSkinImage(skin, "light", controls);
  const dark = useSkinImage(skin, "dark", controls);
  const failed = light.error || dark.error;
  const lv = skin ? { canvas: skin.light.background, ink: skin.light.foreground } : FALLBACK.light;
  const dv = skin ? { canvas: skin.dark.background, ink: skin.dark.foreground } : FALLBACK.dark;
  return <button
    type="button"
    role="radio"
    aria-checked={selected}
    className={`settings-theme-card${selected ? " active" : ""}`}
    onClick={choose}
    title={skin ? (failed ? t("settings.skinImageFailed") : skin.pluginName) : undefined}
  >
    <span className="settings-theme-preview settings-skin-preview" aria-hidden="true">
      <PreviewHalf {...lv} url={light.url} />
      <PreviewHalf {...dv} url={dark.url} />
    </span>
    <span className="settings-theme-card-label">{title}</span>
  </button>;
}

function MoreCard({ expand, count, label, toggle }: { expand: boolean; count: number; label: string; toggle: () => void }): JSX.Element {
  return <button type="button" className="settings-theme-card" onClick={toggle}>
    <span className="settings-theme-preview settings-skin-more-box" aria-hidden="true">
      <span>{expand ? `+${count}` : "−"}</span>
    </span>
    <span className="settings-theme-card-label">{label}</span>
  </button>;
}

export function ShellSkinSettings(): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { skins, selected } = useShellSkins(controls);
  const [expanded, setExpanded] = useState(false);
  const [error, setError] = useState(false);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  const skin = skins.find(candidate => candidate.key === selected);
  const unavailable = selected !== "default" && !skin;
  const featured = skins.slice(0, FEATURED_SKINS);
  const rest = skins.slice(FEATURED_SKINS);
  const showAll = expanded || Boolean(skin && !featured.some(item => item.key === skin.key));
  const visible = showAll ? skins : featured;
  return <>
    {(skins.length > 0 || unavailable) && (
      <SettingsRow title={t("settings.shellSkin")} description={t("settings.shellSkinDescription")}>
        <div className="settings-theme-options" role="radiogroup" aria-label={t("settings.shellSkin")}>
          <SkinCard selected={selected === "default"} choose={() => choose("default")} title={t("settings.skinDefault")} />
          {visible.map(entry => <SkinCard key={entry.key} skin={entry} selected={selected === entry.key} choose={() => choose(entry.key)} title={entry.name} />)}
          {rest.length > 0 && !showAll && <MoreCard expand count={rest.length} label={t("settings.skinMore")} toggle={() => setExpanded(true)} />}
          {rest.length > 0 && showAll && <MoreCard expand={false} count={0} label={t("settings.skinCollapse")} toggle={() => setExpanded(false)} />}
        </div>
      </SettingsRow>
    )}
    {unavailable && <p className="settings-description" role="status">{t("settings.skinUnavailable")}</p>}
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
    {/* The picture is the selected skin's artwork, so only skins that can show it offer it. */}
    {showsUserImage(skin) && <BackgroundSettings canvasSkin={skin?.placement === "canvas" ? skin.name : undefined} />}
  </>;
}
