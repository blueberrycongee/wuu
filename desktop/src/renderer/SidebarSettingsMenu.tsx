import { Cog } from "./WuuIcons";
import { useI18n } from "./i18n";
import "./SidebarAccount.css";

/** One-click Settings entry at the foot of the sidebar. */
export function SidebarSettingsMenu({ disabled, onOpenSettings }: {
  disabled: boolean;
  onOpenSettings: (page?: "providers" | "usage") => void;
}): React.JSX.Element {
  const { t } = useI18n();
  return <div className="sidebar-account">
    <button className="sidebar-account-trigger sidebar-settings-trigger" type="button" disabled={disabled}
      aria-label={t("sidebar.settings")} data-settings-entry="settings" onClick={() => onOpenSettings()}>
      <span className="sidebar-account-avatar" aria-hidden="true"><Cog size={18} /></span><span className="sidebar-account-name">{t("sidebar.settings")}</span>
    </button>
  </div>;
}
