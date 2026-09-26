import { useState } from "react";
import { useI18n } from "./i18n";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";
import { saveVimNavigationEnabled, useVimNavigationEnabled, vimCommands, vimKeyLabel } from "./VimNavigation";

export function KeyboardShortcutsSettings(): JSX.Element {
  const { t } = useI18n();
  const enabled = useVimNavigationEnabled();
  const [error, setError] = useState(false);
  return <>
    <SettingsPageHeader title={t("vim.title")} description={t("vim.description")} />
    <SettingsSection>
      <SettingsGroup>
        <SettingsRow title={t("vim.enable")} description={t("vim.enableHint")}>
          <button className="settings-switch" type="button" role="switch" aria-checked={enabled}
            aria-label={t("vim.enable")} data-testid="vim-navigation-toggle" onClick={() => {
              try { saveVimNavigationEnabled(!enabled); setError(false); }
              catch { setError(true); }
            }}><span className="settings-switch-thumb" aria-hidden="true" /></button>
        </SettingsRow>
      </SettingsGroup>
      {error ? <p className="settings-error" role="alert">{t("settings.saveFailed")}</p> : null}
    </SettingsSection>
    {(["reading", "conversations", "workspace"] as const).map(group =>
      <SettingsSection key={group} title={t(`vim.group.${group}`)}>
        <SettingsGroup>
          {vimCommands.filter(command => command.group === group).map(command =>
            <SettingsRow key={command.action} title={t(`vim.${command.action}`)}>
              <kbd className="vim-key">{vimKeyLabel(command.keys, t("vim.space"))}</kbd>
            </SettingsRow>,
          )}
          {group === "conversations" ? <SettingsRow title={t("vim.escape")} description={t("vim.escapeHint")}><kbd className="vim-key">Esc</kbd></SettingsRow> : null}
        </SettingsGroup>
      </SettingsSection>,
    )}
    <p className="settings-hint">{t("vim.inputHint")}</p>
  </>;
}
