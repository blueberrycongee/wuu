import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { RemoteControlSnapshot } from "../shared/protocol";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsFailure } from "./SettingsFailure";
import { SettingsRemotePage } from "./SettingsRemotePage";
import { useI18n } from "./i18n";

/** Remote state belongs to this settings provider, not to a default off switch. */
export function SettingsRemotePageContainer(): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const [snapshot, setSnapshot] = useState<RemoteControlSnapshot | null>(null);
  const [readError, setReadError] = useState("");
  const [actionError, setActionError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const authority = useMemo(() => ({ active: true }), [controls]);
  const revision = useRef(0);
  const refresh = useCallback(async () => {
    const request = ++revision.current;
    setLoading(true);
    try {
      const next = await controls.getRemoteControlSnapshot();
      if (!authority.active || request !== revision.current) return;
      setSnapshot(next);
      setReadError("");
    } catch (error) {
      if (authority.active && request === revision.current && !isPluginLifecycleCancellation(error)) setReadError(error instanceof Error ? error.message : String(error));
    } finally {
      if (authority.active && request === revision.current) setLoading(false);
    }
  }, [controls, authority]);
  useEffect(() => {
    authority.active = true;
    setSnapshot(null); setReadError(""); setActionError(""); setBusy(false);
    void refresh();
    const off = controls.subscribeRemote(() => { void refresh(); });
    return () => { authority.active = false; off(); };
  }, [controls, authority, refresh]);
  const run = async (action: () => Promise<RemoteControlSnapshot>) => {
    if (!authority.active) return;
    // Reads started before this mutation must not restore the old switch state.
    ++revision.current;
    setLoading(false); setBusy(true); setActionError("");
    try {
      const next = await action();
      if (authority.active) { ++revision.current; setLoading(false); setSnapshot(next); setReadError(""); }
    } catch (error) {
      if (authority.active && !isPluginLifecycleCancellation(error)) setActionError(error instanceof Error ? error.message : String(error));
    } finally {
      if (authority.active) setBusy(false);
    }
  };
  const failure = readError ? <SettingsFailure message={t("remote.readFailed")} diagnostic={readError}
    onRetry={() => { void refresh(); }} retrying={loading} /> : null;
  if (!snapshot) return failure ?? <p role="status">{t("settings.loading")}</p>;
  return <>{failure}<SettingsRemotePage status={snapshot.status ?? null} statusError={actionError || snapshot.status_error || ""}
    hostRunning={snapshot.host_running ?? false} hostEnabled={snapshot.host_enabled ?? snapshot.host_running ?? false}
    pairUri={snapshot.pair_uri ?? null} webUrl={snapshot.web_url ?? null} busy={busy}
    onToggleHost={enabled => { void run(() => controls.setRemoteHostEnabled(enabled)); }}
    onOpenPairing={() => { void run(() => controls.startRemotePairing()); }}
    onRemoveDevice={device => { void run(() => controls.removeRemoteDevice(device.fingerprint)); }} />
  </>;
}
