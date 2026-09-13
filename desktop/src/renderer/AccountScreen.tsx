import { useEffect, useState } from "react";
import { ArrowLeft } from "lucide-react";
import { AccountPanel, type AccountDriver } from "./AccountPanel";
import { LinuxWindowControls } from "./LinuxWindowControls";
import { useI18n } from "./i18n";
import "./AccountScreen.css";

/** Shared content for the native device-linking window and web fallback. */
export function AccountScreen({ driver, onBack, standalone = false }: {
  standalone?: boolean;
  driver: AccountDriver;
  onBack: () => void;
}): React.JSX.Element {
  const { t } = useI18n();
  const [connected, setConnected] = useState(false);
  useEffect(() => {
    if (standalone) document.title = `Wuu · ${t("account.linkDevices")}`;
  }, [standalone, t]);
  return <div className={`account-screen${standalone ? " account-screen-window" : ""}`}>
    <header className="account-screen-titlebar">
      {!standalone && <button type="button" className="account-screen-back" onClick={onBack}>
        <ArrowLeft size={18} aria-hidden="true" />{t("settings.backToApp")}
      </button>}
      <div className="account-screen-titlebar-actions">
        <LinuxWindowControls />
      </div>
    </header>
    <main className="account-screen-content">
      <AccountPanel desktopConnectionFlow driver={driver} onSignedIn={() => setConnected(true)} />
      {standalone && connected && <div className="account-panel account-window-done"><button className="account-primary" type="button" onClick={onBack}>{t("account.linkFinish")}</button></div>}
    </main>
  </div>;
}
