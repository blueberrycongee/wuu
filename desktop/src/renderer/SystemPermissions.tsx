import { useEffect, useState } from "react";
import type { SystemPermission, SystemPermissionsState } from "../shared/protocol";
import { Check, Hand, LoaderCircle, Monitor, RefreshCw } from "./WuuIcons";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function SystemPermissionsPanel({ preview = false }: { preview?: boolean }): JSX.Element {
  const { t } = useI18n();
  const [state, setState] = useState<SystemPermissionsState>();
  const [pending, setPending] = useState<SystemPermission>();
  const [error, setError] = useState<"read" | "request">();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (preview) {
      setState({ available: true, screen: false, accessibility: false });
      return;
    }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    let checking = false;
    const refresh = async (): Promise<void> => {
      if (checking || !active) return;
      checking = true;
      clearTimeout(timer);
      try {
        const next = await window.wuu.getSystemPermissions!();
        if (active) { setState(next); setError(current => current === "read" ? undefined : current); }
      } catch { if (active) setError("read"); }
      finally {
        checking = false;
        if (active) timer = setTimeout(() => void refresh(), 1_500);
      }
    };
    const onFocus = () => void refresh();
    window.addEventListener("focus", onFocus);
    void refresh();
    return () => {
      active = false;
      clearTimeout(timer);
      window.removeEventListener("focus", onFocus);
      void window.wuu.closeSystemPermissionGuide?.().catch(() => undefined);
    };
  }, [preview, attempt]);

  async function request(permission: SystemPermission): Promise<void> {
    if (preview) {
      setState(current => current && { ...current, [permission]: true });
      return;
    }
    setPending(permission);
    setError(undefined);
    try { await window.wuu.requestSystemPermission!(permission); }
    catch { setError("request"); }
    finally { setPending(undefined); }
  }

  return (
    <div className="system-permissions" data-testid="system-permissions">
      <SettingsGroup>
        {(["screen", "accessibility"] as const).map(permission => {
          const Icon = permission === "screen" ? Monitor : Hand;
          const granted = state?.[permission] === true && error !== "read";
          return <div className="system-permission-row" key={permission}>
            <Icon className="system-permission-icon" aria-hidden="true" />
            <SettingsRow title={t(`permissions.${permission}`)} description={t(`permissions.${permission}Description`)}>
              {granted ? <span className="system-permission-granted settings-row-control-value" role="status"><Check className="icon" aria-hidden="true" />{t("permissions.granted")}</span>
                : <button className="settings-button" type="button" disabled={!state?.available || pending !== undefined}
                    onClick={() => void request(permission)} aria-label={t("permissions.enableNamed", { permission: t(`permissions.${permission}`) })}>
                    {pending === permission ? <LoaderCircle className="icon settings-spin" aria-hidden="true" /> : null}
                    {t("permissions.enable")}
                  </button>}
            </SettingsRow>
          </div>;
        })}
      </SettingsGroup>
      {!state && !error ? <p className="system-permissions-note" role="status">{t("permissions.checking")}</p> : null}
      {state && !state.available ? <p className="system-permissions-note" role="status">{t("permissions.unavailable")}</p> : null}
      {error ? <div className="system-permissions-error" role="alert">
        <p className="settings-error">{t("permissions.error")}</p>
        <button type="button" className="settings-button settings-button-ghost" onClick={() => { setError(undefined); setAttempt(value => value + 1); }}>
          <RefreshCw className="icon" aria-hidden="true" />{t("permissions.retry")}
        </button>
      </div> : null}
    </div>
  );
}

export function SystemPermissionsSection(): JSX.Element | null {
  const { t } = useI18n();
  if (window.wuu?.platform !== "darwin" || !window.wuu.getSystemPermissions) return null;
  return <SettingsSection title={t("settings.appContext.permissions")} testID="settings-system-permissions"><SystemPermissionsPanel /></SettingsSection>;
}
