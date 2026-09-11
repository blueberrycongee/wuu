import { ArrowLeft } from "lucide-react";
import { AccountPanel, type AccountDriver } from "./AccountPanel";
import { useI18n } from "./i18n";
import "./AccountScreen.css";

/** Account entry replaces the workbench without entering the settings shell. */
export function AccountScreen({ driver, onBack }: {
  driver: AccountDriver;
  onBack: () => void;
}): React.JSX.Element {
  const { t } = useI18n();
  return <div className="account-screen">
    <header className="account-screen-titlebar">
      <button type="button" className="account-screen-back" onClick={onBack}>
        <ArrowLeft size={18} aria-hidden="true" />{t("settings.backToApp")}
      </button>
    </header>
    <main className="account-screen-content">
      <div className="account-screen-brand" aria-hidden="true">Wuu</div>
      <AccountPanel desktopConnectionFlow driver={driver} />
    </main>
  </div>;
}
