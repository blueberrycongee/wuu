import { useEffect, useState } from "react";
import { accountCredentials, type Credentials } from "@wuu/remote-core";
import {
  AccountPanel,
  type AccountDeviceView,
} from "../../../desktop/src/renderer/AccountPanel";
import { accountDriver, loadAccount } from "./lib/accountStore";
import { webCredStore } from "./lib/credStore";
import PairedApp from "./App";
import { NotificationSettings } from './NotificationSettings';
import { startPushLifecycle, consumeNotificationHost } from './lib/notifications';
import { useI18n } from '../../../desktop/src/renderer/i18n';

export default function AccountApp(): React.JSX.Element {
  const { t } = useI18n();
  const [selected, setSelected] = useState<Credentials | null>(null);
  const [pair, setPair] = useState(() =>
    window.location.hash.includes("pair="),
  );
  const [error, setError] = useState("");
  const [boot, setBoot] = useState(true);
  useEffect(() => { void startPushLifecycle().catch(error => setError(String(error))); }, []);
  useEffect(() => {
    if (boot) return;
    const open = () => {
      const host = consumeNotificationHost(); if (!host) return;
      void accountDriver('status').then(async account => {
        const device = account.devices?.find(device => device.pub === host && device.role === 'host');
        if (!device) throw new Error('通知对应的电脑已不在当前账号中');
        await select(device);
      }).catch(error => setError(String(error)));
    };
    window.addEventListener('wuu:notification-open', open); open();
    return () => window.removeEventListener('wuu:notification-open', open);
  }, [boot]);
  useEffect(() => {
    let active = true;
    void Promise.all([loadAccount(), webCredStore.load()])
      .then(([account, credentials]) => {
        if (!active || window.location.hash.includes("pair=")) return;
        if (
          credentials &&
          account &&
          credentials.device_seed === account.device_seed &&
          credentials.relay_url ===
            account.server.replace(/^http/, "ws") + "/v1/connect"
        )
          setSelected(credentials);
        else if (credentials && !account) setPair(true);
      })
      .catch((e) => {
        if (active) setError(String(e));
      })
      .finally(() => {
        if (active) setBoot(false);
      });
    return () => {
      active = false;
    };
  }, []);
  const select = async (device: AccountDeviceView) => {
    try {
      const session = await loadAccount();
      if (!session) throw new Error("请重新登录");
      const credentials = accountCredentials(session, device);
      await webCredStore.save(credentials);
      setSelected(credentials);
    } catch (e) {
      setError(String(e));
    }
  };
  const back = () => {
    setSelected(null);
    setPair(false);
    void webCredStore.clear();
  };
  useEffect(() => {
    const handler = (event: Event) => {
      if (!selected && !pair) return;
      event.preventDefault();
      // Give existing dialogs and drawers the first opportunity to close.
      const overlays = document.querySelectorAll('[role="dialog"], [role="menu"]');
      const overlay = overlays.item(overlays.length - 1);
      if (overlay) {
        overlay.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
        return;
      }
      if (!window.dispatchEvent(new Event("wuu:workbench-back", { cancelable: true }))) return;
      back();
    };
    window.addEventListener("wuu:native-back", handler);
    return () => window.removeEventListener("wuu:native-back", handler);
  }, [selected, pair]);
  if (boot) return <main className="account-home">{t('account.restoring')}</main>;
  if (selected || pair)
    return (
      <div className="account-workbench">
        <header className="account-toolbar">
          <button onClick={back}>‹ {t('account.backToDevices')}</button>
          <span>{selected ? selected.host_name || t('account.computer') : t('account.pairing')}</span>
        </header>
        <div className="account-workbench-content">
          <PairedApp key={selected?.host_pub || "pair"} onAccountBack={selected ? back : undefined} />
        </div>
      </div>
    );
  return (
    <main className="account-home">
      <AccountPanel driver={accountDriver} onComputer={(d) => void select(d)} />
      <NotificationSettings />
      {error && <p role="alert">{error}</p>}
      <button className="account-pair-link" onClick={() => setPair(true)}>
        {t('account.pairLink')}
      </button>
    </main>
  );
}
