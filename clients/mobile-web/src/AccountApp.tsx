import { useEffect, useRef, useState } from "react";
import { accountCredentials, type Credentials } from "@wuu/remote-core";
import {
  AccountPanel,
  type AccountDeviceView,
} from "../../../desktop/src/renderer/AccountPanel";
import { accountDriver, loadAccount } from "./lib/accountStore";
import { webCredStore } from "./lib/credStore";
import PairedApp from "./App";
import ConversationWorkspace from './ConversationWorkspace';
import { reserveAuthorization } from "./lib/native";
import { NotificationSettings } from './NotificationSettings';
import { startPushLifecycle, consumeNotificationHost } from './lib/notifications';
import { useI18n } from '../../../desktop/src/renderer/i18n';
import { PhoneNavigationContext } from '../../../desktop/src/renderer/PhoneNavigationContext';
import { ViewSwitchLoading } from '../../../desktop/src/renderer/LoadingViews';

export default function AccountApp(): React.JSX.Element {
  const { t } = useI18n();
  const [selected, setSelected] = useState<Credentials | null>(null);
  const [pair, setPair] = useState(() =>
    window.location.hash.includes("pair="),
  );
  const [remembered, setRemembered] = useState<Credentials | null>(null);
  const navigating = useRef(0);
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
    void Promise.all([loadAccount(), webCredStore.load(), webCredStore.loadPair()])
      .then(([account, credentials, paired]) => {
        if (active) setRemembered(paired || (!account ? credentials : null));
        if (!active || window.location.hash.includes("pair=")) return;
        if (
          credentials &&
          account &&
          credentials.device_seed === account.device_seed &&
          credentials.relay_url ===
            account.server.replace(/^http/, "ws") + "/v1/connect"
        )
          {
            const saved = { ...credentials, account_username: account.username };
            setSelected(saved);
            void webCredStore.save(saved);
          }
        else if (credentials && !account && !credentials.account_username) {
          void webCredStore.save(credentials);
          setPair(true);
        }
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
    const generation = ++navigating.current;
    try {
      const session = await loadAccount();
      if (!session) throw new Error("请重新登录");
      const credentials = accountCredentials(session, device);
      await webCredStore.save(credentials);
      if (generation === navigating.current) setSelected(credentials);
    } catch (e) {
      setError(String(e));
    }
  };
  const back = () => {
    setSelected(null);
    setPair(false);
    navigating.current++;
    void webCredStore.loadPair().then(setRemembered).catch(e => setError(String(e)));
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
  if (boot) return <main className="account-home account-boot"><ViewSwitchLoading /></main>;
  const inWorkbench = Boolean(selected || pair);
  return (<>
    {inWorkbench && (
      <PhoneNavigationContext.Provider value={{ computer: selected?.host_name || remembered?.host_name, openDevices: back }}>
      <div className="account-workbench">
        <div className="account-workbench-content">
          {selected ? <ConversationWorkspace key={selected.host_pub} credentials={selected} back={back} /> : <PairedApp key="pair" onAccountBack={back} />}
        </div>
      </div>
      </PhoneNavigationContext.Provider>
    )}
    <main className="account-home" hidden={inWorkbench}>
      {remembered && <section className="account-panel account-resume">
        <button className="account-primary" onClick={() => void webCredStore.save(remembered).then(() => setPair(true)).catch(e => setError(String(e)))}>{t('account.resumeConnection')}{remembered.host_name ? ` · ${remembered.host_name}` : ''}</button>
        <button onClick={() => void webCredStore.forgetPair().then(async () => { const active = await webCredStore.load(); if (active?.host_pub === remembered.host_pub && !active.account_username) await webCredStore.clear(); setRemembered(null); }).catch(e => setError(String(e)))}>{t('account.forgetConnection')}</button>
      </section>}
      <AccountPanel active={!inWorkbench} presentation="mobile" reserveAuthorization={reserveAuthorization} driver={accountDriver} onComputer={(d) => void select(d)} onPair={() => void webCredStore.clear().then(() => setPair(true)).catch(e => setError(String(e)))} managementContent={<NotificationSettings />} />
      {error && <p role="alert">{error}</p>}
    </main>
  </>);
}
