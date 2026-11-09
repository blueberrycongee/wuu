import { useSettingsControls } from "../SettingsControlsContext";
import { useState } from "react";
import { useI18n } from "../i18n";
import { ConnectionPage } from "../ConnectionPage";
import { SkinPreviewCard } from "./ShellSkinCard";
import { useShellSkins } from "./ShellSkin";

/** Second-level picker for every shell skin: the settings row only carries the
    current choice, so browsing happens here at a size where artwork reads. */
export function ShellSkinGallery({ onBack }: { onBack: () => void }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const { skins, selected } = useShellSkins(controls);
  const [error, setError] = useState(false);
  const choose = (key: string) => {
    try { controls.selectShellSkin(key); setError(false); } catch { setError(true); }
  };
  return <ConnectionPage
    title={t("settings.shellSkin")}
    subtitle={t("settings.shellSkinDescription")}
    backLabel={t("settings.appearance")}
    backTestId="settings-skin-gallery-back"
    onClose={onBack}
  >
    <div className="settings-skin-gallery" role="radiogroup" aria-label={t("settings.shellSkin")}>
      <SkinPreviewCard selected={selected === "default"} choose={() => choose("default")} title={t("settings.skinDefault")} previewClassName="settings-skin-preview-large" />
      {skins.map(entry => <SkinPreviewCard key={entry.key} skin={entry} selected={selected === entry.key} choose={() => choose(entry.key)} title={entry.name} previewClassName="settings-skin-preview-large" />)}
    </div>
    {error && <p role="alert">{t("settings.saveFailed")}</p>}
  </ConnectionPage>;
}
