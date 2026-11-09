import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { RemoteControlSnapshot } from "../shared/protocol";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsFailure } from "./SettingsFailure";
import { SettingsRemotePage } from "./SettingsRemotePage";
import { useI18n } from "./i18n";
import { readRetainedSettingsData, writeRetainedSettingsData } from "./SettingsPageData";
import { showActionErrorToast } from "./Toast";

const REMOTE_SNAPSHOT_DATA = "remote.snapshot";

/** Remote state belongs to this settings provider, not to a default off switch. */
export function SettingsRemotePageContainer(): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const retainedSnapshot = readRetainedSettingsData<RemoteControlSnapshot>(controls, REMOTE_SNAPSHOT_DATA);
  const [snapshot, setSnapshot] = useState<RemoteControlSnapshot | null>(() => retainedSnapshot ?? null);
  const [snapshotControls, setSnapshotControls] = useState(() => retainedSnapshot ? controls : undefined);
  const [readError, setReadError] = useState("");
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
      setSnapshotControls(controls);
      writeRetainedSettingsData(controls, REMOTE_SNAPSHOT_DATA, next);
      setReadError("");
    } catch (error) {
      if (authority.active && request === revision.current && !isPluginLifecycleCancellation(error)) setReadError(error instanceof Error ? error.message : String(error));
    } finally {
      if (authority.active && request === revision.current) setLoading(false);
    }
  }, [controls, authority]);
  useEffect(() => {
    authority.active = true;
    const retained = readRetainedSettingsData<RemoteControlSnapshot>(controls, REMOTE_SNAPSHOT_DATA);
    setSnapshot(retained ?? null);
    setSnapshotControls(retained ? controls : undefined);
    setReadError(""); setBusy(false);
    void refresh();
    const off = controls.subscribeRemote(() => { void refresh(); });
    return () => { authority.active = false; off(); };
  }, [controls, authority, refresh]);
  const run = async (action: () => Promise<RemoteControlSnapshot>) => {
    if (!authority.active) return;
    // Reads started before this mutation must not restore the old switch state.
    ++revision.current;
    setLoading(false); setBusy(true);
    try {
      const next = await action();
      if (authority.active) {
        ++revision.current;
        setLoading(false); setSnapshot(next); setSnapshotControls(controls);
        writeRetainedSettingsData(controls, REMOTE_SNAPSHOT_DATA, next);
        setReadError("");
      }
    } catch (error) {
      if (authority.active) showActionErrorToast(error);
    } finally {
      if (authority.active) setBusy(false);
    }
  };
  const currentSnapshot = snapshotControls === controls ? snapshot : null;
  const failure = readError ? <SettingsFailure message={t("remote.readFailed")} diagnostic={readError}
    onRetry={() => { void refresh(); }} retrying={loading} /> : null;
  if (!currentSnapshot) return failure ?? <p role="status">{t("settings.loading")}</p>;
  return <>{failure}<SettingsRemotePage status={currentSnapshot.status ?? null} statusError={currentSnapshot.status_error || ""}
    hostRunning={currentSnapshot.host_running ?? false} hostEnabled={currentSnapshot.host_enabled ?? currentSnapshot.host_running ?? false}
    pairUri={currentSnapshot.pair_uri ?? null} webUrl={currentSnapshot.web_url ?? null} busy={busy}
    onToggleHost={enabled => { void run(() => controls.setRemoteHostEnabled(enabled)); }}
    onOpenPairing={() => { void run(() => controls.startRemotePairing()); }}
    onRemoveDevice={device => { void run(() => controls.removeRemoteDevice(device.fingerprint)); }} />
  </>;
}
