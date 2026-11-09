import type { CSSProperties } from "react";
import { useSettingsControls } from "../SettingsControlsContext";
import { useI18n } from "../i18n";
import { useSkinImage, type AvailableShellSkin } from "./ShellSkin";

const FALLBACK = {
  light: { canvas: "#fcfcfd", ink: "#151515" },
  dark: { canvas: "#191a1c", ink: "#e0e1e3" },
};
const line = (ink: string, canvas: string, alpha: number): CSSProperties => ({
  background: `color-mix(in srgb, ${ink} ${Math.round(alpha * 100)}%, ${canvas})`,
});

function PreviewHalf({ canvas, ink, url, onImageError }: { canvas: string; ink: string; url: string; onImageError: () => void }): JSX.Element {
  return <span className="settings-skin-half" style={{ background: canvas }}>
    {url ? <img src={url} alt="" onError={onImageError} /> : null}
    <i className="settings-skin-half-rail" style={line(ink, canvas, 0.10)} />
    <span className="settings-skin-half-lines">
      <i style={{ width: "46%", ...line(ink, canvas, 0.30) }} />
      <i style={{ width: "74%", ...line(ink, canvas, 0.16) }} />
      <i style={{ width: "34%", ...line(ink, canvas, 0.16) }} />
    </span>
  </span>;
}

export function SkinPreviewCard({ skin, selected, choose, title, previewClassName }: {
  skin?: AvailableShellSkin; selected: boolean; choose: () => void; title: string; previewClassName?: string;
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
    <span className={`settings-theme-preview settings-skin-preview${previewClassName ? ` ${previewClassName}` : ""}`} aria-hidden="true">
      <PreviewHalf {...lv} url={light.url} onImageError={light.retry} />
      <PreviewHalf {...dv} url={dark.url} onImageError={dark.retry} />
    </span>
    <span className="settings-theme-card-label">{title}</span>
  </button>;
}
