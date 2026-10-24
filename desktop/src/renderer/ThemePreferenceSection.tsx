import { useSettingsControls } from "./SettingsControlsContext";
import { useEffect, useState } from "react";
import type { ExtensionInventoryRecord, ThemePreference } from "../shared/protocol";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup } from "./SettingsSection";
import { SelectMenu } from "./SelectMenu";
import { useI18n } from "./i18n";

export function ContentThemeControl({ inventory }: { inventory?: readonly ExtensionInventoryRecord[] }): JSX.Element | null {
  const settingsControls = useSettingsControls();
  const { t } = useI18n();
  const { currentAppliedTheme, observeAppliedTheme, availableExtensionThemes, selectedExtensionThemeKey, selectExtensionTheme } = settingsControls;
  const [mode, setMode] = useState(currentAppliedTheme);
  const [, refresh] = useState(0);
  useEffect(() => {
    setMode(currentAppliedTheme());
    return observeAppliedTheme(setMode);
  }, []);
  useEffect(() => {
    const changed = () => refresh(value => value + 1);
    return settingsControls.observeContentThemeSelection(changed);
  }, []);
  const themes = availableExtensionThemes(inventory).filter(theme => theme.base === mode);
  if (themes.length === 0) return null;
  const saved = selectedExtensionThemeKey(mode);
  const selected = themes.find(theme => theme.key === saved || theme.legacyKeys.includes(saved));
  return <SettingsGroup><SettingsRow title={t("settings.contentTheme")}>
    <SelectMenu ariaLabel={t("settings.contentTheme")} value={selected?.key ?? ""}
      options={[{ value: "", label: t("settings.skinDefault") }, ...themes.map(theme => ({ value: theme.key, label: theme.name }))]}
      onChange={key => selectExtensionTheme(mode, key)} triggerClassName="settings-select-trigger" />
  </SettingsRow></SettingsGroup>;
}

function ThemePreviewWindow({ theme }: { theme: "light" | "dark" }): JSX.Element {
  return (
    <span className={`settings-theme-preview-window settings-theme-preview-window-${theme}`}>
      <span className="settings-theme-preview-sidebar" />
      <span className="settings-theme-preview-body">
        <span className="settings-theme-preview-line settings-theme-preview-line-title" />
        <span className="settings-theme-preview-line" />
        <span className="settings-theme-preview-line settings-theme-preview-line-short" />
      </span>
    </span>
  );
}

/**
 * 外观 row body: visual radio cards with a tiny window preview per option.
 * System lays the dark window over the light one and clips it at the middle,
 * so both halves share one skeleton and meet at a single seam. Reads and
 * persists through the selected settings service, and applies the choice to
 * <html data-theme> immediately so the user sees the switch without a save
 * step.
 */
export function ThemePreferenceControl(): JSX.Element {
  const settingsControls = useSettingsControls();
  const { t } = useI18n();
  const themeOptions: Array<{ value: ThemePreference; label: string }> = [
    { value: "system", label: t("settings.followSystem") },
    { value: "light", label: t("settings.themeLight") },
    { value: "dark", label: t("settings.themeDark") },
  ];
  const [preference, setPreference] = useState<ThemePreference>(
    () => settingsControls.initialThemePreference ?? "system",
  );
  useEffect(() => {
    let cancelled = false;
    void settingsControls
      .getThemePreference?.()
      .then((stored) => {
        if (!cancelled && stored) {
          setPreference(stored);
        }
      })
      .catch(() => {
        // Keep the preload-provided initial value.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Reflect switches made in other windows: the preference is app-global,
  // so the segmented control must follow the main-process broadcast too.
  useEffect(
    () =>
      settingsControls.capabilities.themeChanges ? settingsControls.onThemePreferenceChange((theme) => {
        setPreference(theme);
      }) : undefined,
    [],
  );

  function choose(next: ThemePreference): void {
    setPreference(next);
    settingsControls.applyThemePreference(next);
    void settingsControls.setThemePreference?.(next).catch(() => {
      // Persistence failure leaves the applied theme for this window;
      // the next launch falls back to the stored preference.
    });
  }

  return (
    <div className="settings-theme-options" role="radiogroup" aria-label={t("settings.themeGroup")}>
      {themeOptions.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={preference === option.value}
          className={`settings-theme-card${preference === option.value ? " active" : ""}`}
          data-testid={`settings-theme-${option.value}`}
          onClick={() => choose(option.value)}
        >
          <span className={`settings-theme-preview settings-theme-preview-${option.value}`} aria-hidden="true">
            {option.value === "system" ? (
              <>
                <ThemePreviewWindow theme="light" />
                <ThemePreviewWindow theme="dark" />
              </>
            ) : (
              <ThemePreviewWindow theme={option.value} />
            )}
          </span>
          <span className="settings-theme-card-label">
            <span className="settings-theme-card-radio" aria-hidden="true" />
            {option.label}
          </span>
        </button>
      ))}
    </div>
  );
}
