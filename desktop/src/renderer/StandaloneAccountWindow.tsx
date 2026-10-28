import { useEffect, useState } from "react";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { AccountScreen } from "./AccountScreen";
import { DesktopControlsProvider } from "./DesktopControlsContext";
import { LinuxWindowControls } from "./LinuxWindowControls";
import { MacWindowControls } from "./MacWindowControls";
import { ApplicationDesktop } from "./plugins/ApplicationDesktop";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";

/** The auxiliary account window owns window authority, never the main window's pet relay. */
export function StandaloneAccountWindow(): React.JSX.Element | null {
  const [controls, setControls] = useState<{ desktop: ApplicationDesktopControlsV1; openExternal: (url: string) => Promise<void> }>();
  useEffect(() => {
    const lifetime = new AbortController();
    const host = new ApplicationDesktop();
    setControls({ desktop: host.createScope(lifetime.signal), openExternal: createApplicationSettingsControls().openExternal });
    return () => { lifetime.abort(); host.dispose(); };
  }, []);
  if (!controls) return null;
  const { desktop, openExternal } = controls;
  if (!desktop.account) return <div role="alert">Account service is unavailable.</div>;
  return <DesktopControlsProvider controls={desktop}>
    <AccountScreen standalone driver={desktop.account} openExternal={openExternal} onBack={() => { void desktop.closeAccountWindow?.().catch(() => {}); }} />
    <LinuxWindowControls desktop={desktop} />
    <MacWindowControls desktop={desktop} />
  </DesktopControlsProvider>;
}
