import { type ReactNode, useEffect, useRef, useState } from "react";
import type { ProviderSummary } from "../shared/protocol";
import { ChatGPTLogin } from "./ChatGPTLogin";
import { ConnectionPage } from "./ConnectionPage";
import { hostSupports } from "./HostCapabilities";
import { Modal } from "./Modal";
import { ProviderMark } from "./ProviderMarks";
import { SelectMenu } from "./SelectMenu";
import type { SaveProvider } from "./ModelServicesPage";
import { useI18n } from "./i18n";

export function ChatGPTConnectionDialog({ providers, running, defaultOn, onSave, onClose, onConnected, onProvidersChanged, inline = false, renderAuthMethods }: {
  providers: readonly ProviderSummary[];
  running: boolean;
  defaultOn: boolean;
  onSave: SaveProvider;
  onClose: () => void;
  onConnected: (name: string) => void;
  onProvidersChanged?: (providers: ProviderSummary[]) => void;
  inline?: boolean;
  renderAuthMethods?: (disabled: boolean) => ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const [accounts, setAccounts] = useState<Awaited<ReturnType<typeof window.wuu.listChatGPTAccounts>>["accounts"]>([]);
  const [accountId, setAccountId] = useState("");
  const [revision, setRevision] = useState(0);
  const [models, setModels] = useState<{ id: string; name: string }[]>([]);
  const [model, setModel] = useState("");
  const [catalogAccount, setCatalogAccount] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const [signingIn, setSigningIn] = useState(false);
  const [makeDefault, setMakeDefault] = useState(defaultOn);
  const [error, setError] = useState("");
  const [authNotice, setAuthNotice] = useState("");
  const [name, setName] = useState(() => {
    let candidate = "chatgpt";
    for (let n = 2; providers.some(provider => provider.name === candidate); n++) candidate = `chatgpt-${n}`;
    return candidate;
  });
  const account = accounts.find(item => item.id === accountId);
  useEffect(() => {
    let active = true;
    void window.wuu.listChatGPTAccounts().then(result => {
      if (!active) return;
      setAccounts(result.accounts);
      setAccountId(current => current || result.accounts.find(item => item.status === "connected")?.id || "");
    }, cause => { if (active) setError(String(cause)); });
    return () => { active = false; };
  }, [revision]);
  useEffect(() => {
    let active = true;
    setModels([]); setModel(""); setCatalogAccount(""); setError("");
    if (!accountId || account?.status !== "connected") { setLoading(false); return; }
    setLoading(true);
    void window.wuu.listChatGPTModels(accountId).then(result => {
      if (!active) return;
      setModels(result.models); setModel(result.models[0]?.id ?? ""); setCatalogAccount(accountId);
    }, cause => { if (active) setError(String(cause)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [accountId, account?.status, revision]);
  const canSave = !running && !busy && !signingIn && !loading && account?.status === "connected" && catalogAccount === accountId &&
    models.some(item => item.id === model) && !!name.trim() && !providers.some(item => item.name === name.trim());
  async function save(): Promise<void> {
    if (!canSave || saving.current) return;
    saving.current = true;
    setBusy(true); setError("");
    try {
      await onSave(name.trim(), model, undefined, { type: "chatgpt", create_provider: true, chatgpt_account_id: accountId, keep_selection: !makeDefault });
      onConnected(name.trim());
    } catch (cause) { setError(String(cause)); }
    finally { saving.current = false; setBusy(false); }
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
      {account?.status === "connected" && hostSupports("startChatGPTLogin") ? <p className="settings-muted-line">{t("provider.chatgptAddAccountHint")}</p> : null}
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
