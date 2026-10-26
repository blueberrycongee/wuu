import { useSettingsOperationScope } from "./SettingsOperationScope";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { type ReactNode, useEffect, useRef, useState } from "react";
import type { ProviderSummary } from "../shared/protocol";
import { ChatGPTLogin } from "./ChatGPTLogin";
import { ConnectionPage } from "./ConnectionPage";
import { Modal } from "./Modal";
import { ProviderMark } from "./ProviderMarks";
import { SelectMenu } from "./SelectMenu";
import type { SaveProvider } from "./ModelServicesPage";
import { useI18n } from "./i18n";

export function ChatGPTConnectionDialog({ providers, running, defaultOn, onSave, onClose, onConnected, onProvidersChanged, inline = false, renderAuthMethods, workspaceRoot }: {
  providers: readonly ProviderSummary[];
  running: boolean;
  defaultOn: boolean;
  onSave: SaveProvider;
  onClose: () => void;
  onConnected: (name: string) => void;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  inline?: boolean;
  workspaceRoot?: string;
  renderAuthMethods?: (disabled: boolean) => ReactNode;
}): JSX.Element {
  const scope = useSettingsOperationScope(workspaceRoot);
  const settingsControls = scope.controls;
  const { t } = useI18n();
  const [accounts, setAccounts] = useState<Awaited<ReturnType<typeof settingsControls.listChatGPTAccounts>>["accounts"]>([]);
  const [accountId, setAccountId] = useState("");
  const [revision, setRevision] = useState(0);
  const [models, setModels] = useState<{ id: string; name: string }[]>([]);
  const [model, setModel] = useState("");
  const [catalogAccount, setCatalogAccount] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const saving = useRef<object | undefined>(undefined);
  const [accountsScope, setAccountsScope] = useState<typeof scope>();
  const [signingIn, setSigningIn] = useState(false);
  const [makeDefault, setMakeDefault] = useState(defaultOn);
  const [error, setError] = useState("");
  const [authNotice, setAuthNotice] = useState("");
  const [name, setName] = useState(() => availableName(providers));
  const account = accountsScope === scope ? accounts.find(item => item.id === accountId) : undefined;
  useEffect(() => {
    setAccounts([]); setAccountsScope(undefined);
    setBusy(false); setSigningIn(false); setAuthNotice("");
    saving.current = undefined;
  }, [scope]);
  useEffect(() => {
    setAccountId(""); setModel("");
    setMakeDefault(defaultOn); setName(availableName(providers));
  }, [workspaceRoot]);
  useEffect(() => {
    let active = true;
    void settingsControls.listChatGPTAccounts().then(result => {
      if (!active || !scope.active) return;
      setAccounts(result.accounts);
      setAccountsScope(scope);
      setAccountId(current => result.accounts.some(item => item.id === current) ? current : result.accounts.find(item => item.status === "connected")?.id || "");
    }, cause => { if (active && scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); });
    return () => { active = false; };
  }, [scope, revision]);
  useEffect(() => {
    let active = true;
    setModels([]); setCatalogAccount(""); setError("");
    if (!accountId || account?.status !== "connected") { setLoading(false); return; }
    setLoading(true);
    void settingsControls.listChatGPTModels(accountId).then(result => {
      if (!active || !scope.active) return;
      setModels(result.models); setModel(current => result.models.some(item => item.id === current) ? current : result.models[0]?.id ?? ""); setCatalogAccount(accountId);
    }, cause => { if (active && scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); }).finally(() => { if (active && scope.active) setLoading(false); });
    return () => { active = false; };
  }, [scope, accountId, account?.status, revision]);
  const canSave = !running && !busy && !signingIn && !loading && account?.status === "connected" && catalogAccount === accountId &&
    models.some(item => item.id === model) && !!name.trim() && !providers.some(item => item.name === name.trim());
  async function save(): Promise<void> {
    if (!scope.active || !canSave || saving.current) return;
    const operation = {};
    saving.current = operation;
    setBusy(true); setError("");
    try {
      await onSave(name.trim(), model, undefined, { type: "chatgpt", create_provider: true, chatgpt_account_id: accountId, keep_selection: !makeDefault });
      if (scope.active && saving.current === operation) onConnected(name.trim());
    } catch (cause) { if (scope.active && !isPluginLifecycleCancellation(cause)) setError(String(cause)); }
    finally { if (scope.active && saving.current === operation) { saving.current = undefined; setBusy(false); } }
  }
  function authenticated(id: string): void { setAccountId(id); setRevision(value => value + 1); }
  const Surface = inline ? ConnectionPage : Modal;
  return <Surface title={t("provider.connectTitle", { name: "ChatGPT" })} ariaLabel={t("provider.connectTitle", { name: "ChatGPT" })}
    icon={<ProviderMark id="openai" label="ChatGPT" />} panelClassName="model-dialog model-connect-dialog" closeDisabled={busy} onClose={onClose} asForm onSubmit={() => void save()}
    footer={<>{inline ? null : <button type="button" className="settings-button settings-button-ghost" disabled={busy} onClick={onClose}>{t("common.cancel")}</button>}
      <button type="submit" className="settings-button settings-button-primary" data-testid="settings-provider-connect" disabled={!canSave}>{t(busy ? "provider.connecting" : "provider.connectAction")}</button></>}>
    <div className="model-connect-body">
      {renderAuthMethods?.(busy || signingIn || running)}
      <small className="connection-auth-source" data-auth-source="wuu-siwc">{t("provider.authAppSource")}</small>
      {accounts.length ? <div className="model-connect-field"><span>{t("provider.chatgptAccount")}</span><SelectMenu value={accountId} disabled={busy || signingIn}
        ariaLabel={t("provider.chatgptAccount")} onChange={setAccountId} options={accounts.map(item => ({ value: item.id, label: `${item.email || "ChatGPT"} · ${item.id}` }))} /></div> : null}
      {account && account.status !== "connected" ? <p role="status">{t(account.status === "permission_required" ? "provider.chatgptPermissionRequired" : "provider.chatgptReconnectRequired")}</p> : null}
      <ChatGPTLogin key={account?.status === "connected" ? "new-account" : accountId} accountId={account?.status !== "connected" ? accountId || undefined : undefined}
        onAdvisory={setAuthNotice} onProvidersChanged={onProvidersChanged} enablePlanUsage={account?.status === "permission_required"}
        disabled={busy || running} onBusyChange={setSigningIn} onAuthenticated={authenticated} />
      {account?.status === "connected" && settingsControls.capabilities.chatGPTLogin ? <p className="settings-muted-line">{t("provider.chatgptAddAccountHint")}</p> : null}
      <div className="model-connect-field"><span>{t("provider.modelLabel")}</span><SelectMenu value={model} disabled={!models.length || loading || busy || signingIn}
        ariaLabel={t("provider.modelLabel")} dataTestid="settings-provider-connect-model" options={models.map(item => ({ value: item.id, label: item.name || item.id }))} onChange={setModel}
        placeholder={t(loading ? "settings.loading" : "provider.chatgptNoModels")} /></div>
      <label className="model-connect-field"><span>{t("provider.identifier")}</span><input className="settings-input" value={name} disabled={busy} onChange={event => setName(event.currentTarget.value)} /></label>
      <div className="model-connect-toggle"><span>{t("provider.useAsDefault")}</span><button type="button" className="settings-switch" role="switch" aria-label={t("provider.useAsDefault")} aria-checked={makeDefault} disabled={busy} onClick={() => setMakeDefault(value => !value)}><span className="settings-switch-thumb" /></button></div>
      {authNotice ? <p className="settings-muted-line" role="status">{authNotice}</p> : null}
      {error ? <p className="settings-error" role="alert">{error}</p> : null}
    </div>
  </Surface>;
}

function availableName(providers: readonly ProviderSummary[]): string {
  let candidate = "chatgpt";
  for (let n = 2; providers.some(provider => provider.name === candidate); n++) candidate = `chatgpt-${n}`;
  return candidate;
}
