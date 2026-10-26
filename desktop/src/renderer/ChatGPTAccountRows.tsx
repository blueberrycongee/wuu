import { useSettingsOperationScope } from "./SettingsOperationScope";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useEffect, useRef, useState } from "react";
import type { ProviderSummary } from "../shared/protocol";
import { ChatGPTLogin } from "./ChatGPTLogin";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function ChatGPTAccountRows({ provider, disabled, onProvidersChanged, onRefresh, onBusyChange }: {
  provider: ProviderSummary;
  disabled: boolean;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  onRefresh: () => void;
  onBusyChange?: (busy: boolean) => void;
}): JSX.Element {
  const scope = useSettingsOperationScope(JSON.stringify([provider.name, provider.chatgpt_account_id]));
  const settingsControls = scope.controls;
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  useEffect(() => { onBusyChange?.(busy); return () => onBusyChange?.(false); }, [busy, onBusyChange]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const accountId = provider.chatgpt_account_id;
  const signingOut = useRef<object | undefined>(undefined);
  const [accountLabel, setAccountLabel] = useState("");
  useEffect(() => {
    let active = true;
    setAccountLabel(""); setBusy(false); setNotice(""); setError("");
    signingOut.current = undefined;
    void settingsControls.listChatGPTAccounts().then(result => {
      if (active && scope.active) setAccountLabel(result.accounts.find(account => account.id === accountId)?.email ?? "");
    }, cause => { if (active && scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); });
    return () => { active = false; };
  }, [scope, accountId, provider.auth_status]);
  async function signOut(): Promise<void> {
    if (!scope.active || !accountId || busy || signingOut.current) return;
    const operation = {};
    signingOut.current = operation;
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await settingsControls.signOutChatGPTAccount(accountId);
      if (!scope.active || signingOut.current !== operation) return;
      onProvidersChanged?.(result.providers);
      setNotice(t(result.revocation_confirmed ? "provider.chatgptSignedOut" : "provider.chatgptLocalSignOut"));
    } catch (cause) { if (scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); }
    finally { if (scope.active && signingOut.current === operation) { signingOut.current = undefined; setBusy(false); } }
  }
  return <>
    {accountLabel ? <SettingsRow title={t("provider.chatgptAccount")}><span className="settings-row-control-value model-service-value">{accountLabel}</span></SettingsRow> : null}
    <ChatGPTLogin onAdvisory={setNotice} accountId={accountId} enablePlanUsage={provider.auth_status === "permission_required"} disabled={disabled || busy} onBusyChange={setBusy} onProvidersChanged={onProvidersChanged} onAuthenticated={() => { setNotice(""); onRefresh(); }} />
    <SettingsRow title={t("provider.chatgptAccountId")}><span className="settings-row-control-value model-service-value">{accountId}</span></SettingsRow>
    <SettingsRow title={t("provider.modelsSection")}><button type="button" className="settings-button" disabled={disabled || busy || provider.auth_status !== "connected"} onClick={onRefresh}>{t("provider.chatgptRefreshModels")}</button></SettingsRow>
    <SettingsRow title={t("provider.accountUsage")} description={t("provider.chatgptUsageHint")}><button type="button" className="settings-button" onClick={() => void settingsControls.openExternal("https://chatgpt.com/settings/usage").catch(cause => { if (scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); })}>{t("provider.chatgptManageUsage")}</button></SettingsRow>
    <SettingsRow title={t("provider.chatgptSignOut")}><button type="button" className="settings-button" disabled={disabled || busy || !accountId} onClick={() => void signOut()}>{t("provider.chatgptSignOut")}</button></SettingsRow>
    {notice ? <p className="settings-muted-line" role="status">{notice}</p> : null}
    {error ? <p className="settings-error" role="alert">{error}</p> : null}
  </>;
}
