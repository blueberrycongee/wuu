import { useSettingsControls } from "./SettingsControlsContext";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useEffect, useRef, useState } from "react";
import type { ProviderSummary } from "../shared/protocol";
import { useI18n } from "./i18n";

/** Authorization is owned by the host; tokens and authorization URLs never enter this view. */
export function ChatGPTLogin({ onAuthenticated, onBusyChange, disabled = false, accountId, onProvidersChanged, enablePlanUsage = false, onAdvisory }: {
  onAuthenticated: (accountId: string) => void;
  onAdvisory?: (message: string) => void;
  accountId?: string;
  enablePlanUsage?: boolean;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  onBusyChange?: (busy: boolean) => void;
  disabled?: boolean;
}): JSX.Element {
  const settingsControls = useSettingsControls();
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef<{ id?: string; timer?: number } | undefined>(undefined);
  const mounted = useRef(true);
  const authority = useRef(settingsControls);
  authority.current = settingsControls;
  const authenticated = useRef(onAuthenticated);
  authenticated.current = onAuthenticated;
  useEffect(() => {
    mounted.current = true;
    setBusy(false); setError("");
    return () => {
      mounted.current = false;
      const current = operation.current;
      operation.current = undefined;
      if (current?.timer) window.clearTimeout(current.timer);
      if (current?.id) void settingsControls.cancelChatGPTLogin(current.id).catch(() => {});
      if (current) onBusyChange?.(false);
    };
  }, [settingsControls]);

  function finish(): void {
    operation.current = undefined;
    setBusy(false);
    onBusyChange?.(false);
  }

  async function start(): Promise<void> {
    if (!settingsControls.capabilities.chatGPTLogin || operation.current || busy || disabled) return;
    const current: { id?: string; timer?: number } = {};
    operation.current = current;
    setError("");
    setBusy(true);
    onBusyChange?.(true);
    try {
      const login = await settingsControls.startChatGPTLogin(accountId, enablePlanUsage);
      current.id = login.login_id;
      if (operation.current !== current) {
        await settingsControls.cancelChatGPTLogin(login.login_id);
        if (mounted.current && authority.current === settingsControls && !operation.current) finish();
        return;
      }
      const poll = async (): Promise<void> => {
        try {
          const result = await settingsControls.pollChatGPTLogin(login.login_id);
          if (operation.current !== current) return;
          if (result.status === "pending") {
            current.timer = window.setTimeout(() => void poll(), Math.max(250, login.interval_ms));
            return;
          }
          finish();
          if (result.status === "success" && result.account_id) {
            if (result.providers) onProvidersChanged?.(result.providers);
            authenticated.current(result.account_id);
            onAdvisory?.(result.error || "");
            if (!onAdvisory) setError(result.error || "");
          }
          else setError(result.error || t("provider.chatgptLoginIncomplete"));
        } catch (cause) {
          if (operation.current !== current) return;
          // Cancel the host operation when polling can no longer observe its result.
          void settingsControls.cancelChatGPTLogin(login.login_id).catch(() => {});
          finish();
          if (!isPluginLifecycleCancellation(cause)) setError(cause instanceof Error ? cause.message : String(cause));
        }
      };
      current.timer = window.setTimeout(() => void poll(), Math.max(250, login.interval_ms));
    } catch (cause) {
      if (!mounted.current || authority.current !== settingsControls || (operation.current && operation.current !== current)) return;
      finish();
      if (!isPluginLifecycleCancellation(cause)) setError(cause instanceof Error ? cause.message : String(cause));
    }
  }

  async function cancel(): Promise<void> {
    const current = operation.current;
    if (!current) return;
    if (current.timer) window.clearTimeout(current.timer);
    operation.current = undefined;
    if (current.id) {
      try { await settingsControls.cancelChatGPTLogin(current.id); }
      catch (cause) { if (mounted.current && authority.current === settingsControls && !isPluginLifecycleCancellation(cause)) setError(cause instanceof Error ? cause.message : String(cause)); }
      finally { if (mounted.current && authority.current === settingsControls) finish(); }
    }
    // A start still awaiting admission cancels its returned ID before finishing.
  }

  if (!settingsControls.capabilities.chatGPTLogin) return <p className="settings-muted-line" role="status">{t("provider.chatgptDesktopLoginRequired")}</p>;

  return <div className="settings-engine-auth" aria-busy={busy}>
    <div className="settings-engine-auth-actions">
      <button type="button" className="settings-button" data-testid="chatgpt-login-start" disabled={busy || disabled} onClick={() => void start()}>{t("provider.chatgptContinue")}</button>
      {busy ? <button type="button" className="settings-button" data-testid="chatgpt-login-cancel" onClick={() => void cancel()}>{t("common.cancel")}</button> : null}
    </div>
    {busy ? <p className="settings-muted-line" role="status">{t("provider.chatgptWaiting")}</p> : null}
    {error ? <p className="environment-dialog-error" role="alert">{error}</p> : null}
  </div>;
}
