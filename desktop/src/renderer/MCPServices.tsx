import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { MCPServerStatus } from "../shared/protocol";
import type { MCPDefinitionResult } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { Modal } from "./Modal";
import { MCPServerEditor } from "./MCPServerEditor";
import { confirmAction } from "./ConfirmDialog";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import { Plug } from "./WuuIcons";

const MCPContext = createContext<{ servers: MCPServerStatus[]; error: string; refresh(): Promise<void> } | null>(null);
export function useMCPServices() {
  const value = useContext(MCPContext);
  if (!value) throw new Error("MCP services require a catalog provider");
  return value;
}

export function MCPServicesProvider({ children }: { children: ReactNode }): JSX.Element {
  const controls = useSettingsControls();
  const [servers, setServers] = useState<MCPServerStatus[]>([]);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    try {
      const result = await controls.listMCPServers();
      if (current !== generation.current) return;
      setServers(result.servers);
      setError("");
    } catch (failure) {
      if (current === generation.current) setError(toastErrorMessage(failure));
    }
  }, [controls]);
  useEffect(() => {
    generation.current++;
    void refresh();
    const timer = setInterval(() => { void refresh(); }, 5000);
    const unsubscribe = controls.subscribe(() => { void refresh(); });
    const remote = controls.subscribeRemote(() => {
      generation.current++;
      void refresh();
    });
    return () => { generation.current++; clearInterval(timer); unsubscribe(); remote(); };
  }, [controls, refresh]);
  return <MCPContext.Provider value={{ servers, error, refresh }}>{children}</MCPContext.Provider>;
}

export function MCPServices({ pluginID, enabled = true, filter = "" }: { pluginID?: string; enabled?: boolean; filter?: string }): JSX.Element | null {
  const controls = useSettingsControls();
  const { servers, error: loadError, refresh } = useMCPServices();
  const { t } = useI18n();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const inFlight = useRef(false);
  const mounted = useRef(true);
  const [editor, setEditor] = useState<{ name: string; definition?: MCPDefinitionResult } | null>(null);
  const [login, setLogin] = useState<MCPServerStatus | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const visible = servers.filter(server => (server.plugin_id || undefined) === pluginID
    && (server.display_name || server.name).toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()));

  async function act(name: string, action: () => Promise<unknown>): Promise<void> {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(name);
    setError("");
    try { await action(); }
    catch (failure) { if (mounted.current) setError(toastErrorMessage(failure)); }
    finally {
      inFlight.current = false;
      if (mounted.current) { setBusy(""); await refresh(); }
    }
  }

  async function remove(server: MCPServerStatus): Promise<void> {
    const definition = await controls.readMCPDefinition(server.name);
    if (!await confirmAction({ title: t("mcp.removeTitle", { name: server.name }), message: t("mcp.removeHint"), confirmLabel: t("common.remove"), tone: "danger" })) return;
    await controls.configureMCPServers({ servers: { [server.name]: null }, revision: definition.revision });
  }

  if ((pluginID || filter.trim()) && visible.length === 0 && !loadError) return null;
  return <SettingsSection testID="mcp-services" title={pluginID ? "MCP" : t("mcp.services")} actions={!pluginID ? <button type="button" className="settings-button" onClick={() => setEditor({ name: "" })}>{t("mcp.add")}</button> : undefined}>
    {loadError || error ? <p className="mcp-error" role="alert">{error || loadError}</p> : null}
    {!enabled && visible.length > 0 ? <p className="settings-section-description">{t("mcp.enablePlugin")}</p> : null}
    {visible.length ? <SettingsGroup>{visible.map(server => {
      const name = server.display_name || server.name;
      const needsAuth = ["auth_required", "needs_auth", "needs_client_registration"].includes(server.state);
      const status = server.connected ? t("mcp.connected") : needsAuth ? t("mcp.needsAuth")
        : server.state === "failed" || server.state === "error" ? t("mcp.failed")
        : ["starting", "connecting", "reconnecting"].includes(server.state) ? t("mcp.connecting")
        : server.state === "disabled" ? t("mcp.disabledByConfig") : t("mcp.disconnected");
      return <div className="mcp-service" key={server.name} data-mcp={server.name} aria-busy={busy === server.name}>
        <Plug className="icon mcp-service-icon" aria-hidden="true" />
        <div className="mcp-service-copy">
          <div className="mcp-service-name">{name}</div>
          <div className="mcp-service-status" data-attention={needsAuth || undefined}>{status}{server.connected ? ` · ${t("mcp.toolCount", { count: server.tool_count })}` : ""}</div>
          {server.error ? <details className="mcp-connection-detail"><summary>{t("mcp.connectionDetails")}</summary><p className="mcp-error">{server.error}</p></details> : null}
          {!pluginID && !server.editable ? <p className="mcp-service-status">{t("mcp.projectManaged")}</p> : null}
        </div>
        <div className="mcp-service-actions">
          {enabled && server.state !== "disabled" ? <>
            {needsAuth ? <button type="button" className="settings-button" disabled={!!busy} onClick={() => setLogin(server)}>{t("mcp.oauthLogin")}</button>
              : <button type="button" className="settings-button" disabled={!!busy} onClick={() => void act(server.name, () => server.connected ? controls.disconnectMCPServer(server.name) : controls.connectMCPServer(server.name))}>{t(server.connected ? "mcp.disconnect" : "mcp.connect")}</button>}
            {server.connected ? <button type="button" className="settings-button settings-button-ghost" disabled={!!busy} onClick={() => void act(server.name, () => controls.refreshMCPServer(server.name))}>{t("mcp.refresh")}</button> : null}
            {server.auth_status === "oauth" ? <button type="button" className="settings-button settings-button-ghost" disabled={!!busy} onClick={() => void act(server.name, () => controls.removeMCPAuth(server.name))}>{t("mcp.removeLogin")}</button> : null}
          </> : null}
          {server.editable ? <>
            <button type="button" className="settings-button settings-button-ghost" disabled={!!busy} onClick={() => void act(server.name, async () => { const definition = await controls.readMCPDefinition(server.name); if (mounted.current) setEditor({ name: server.name, definition }); })}>{t("common.edit")}</button>
            <button type="button" className="settings-button settings-button-ghost" disabled={!!busy} onClick={() => void act(server.name, () => remove(server))}>{t("common.remove")}</button>
          </> : null}
        </div>
      </div>;
    })}</SettingsGroup> : !loadError ? <p className="settings-group-empty">{t(filter ? "skills.noMatches" : "mcp.empty")}</p> : null}
    {editor ? <MCPServerEditor name={editor.name} definition={editor.definition} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void refresh(); }} /> : null}
    {login ? <MCPLogin server={login} onClose={() => { setLogin(null); void refresh(); }} /> : null}
  </SettingsSection>;
}

function MCPLogin({ server, onClose }: { server: MCPServerStatus; onClose(): void }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const [error, setError] = useState("");
  const [manual, setManual] = useState(false);
  const [ready, setReady] = useState(false);
  const [code, setCode] = useState("");
  const [finishing, setFinishing] = useState(false);
  const [authorized, setAuthorized] = useState(false);
  const attempt = useRef<{ loginID?: string; state?: string; authorized?: boolean; cancelled: boolean }>({ cancelled: false });
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    const current: typeof attempt.current = { cancelled: false };
    attempt.current = current;
    setAuthorized(false);
    setReady(false);
    setError("");
    let timer: ReturnType<typeof setTimeout>;
    const cancel = async () => {
      if (current.loginID) await controls.cancelMCPLogin(current.loginID);
      else if (current.state) await controls.cancelMCPAuth(server.name, current.state);
    };
    const poll = async () => {
      try {
        const result = await controls.pollMCPLogin(current.loginID!);
        if (current.cancelled) return;
        if (result.status === "success") { close.current(); return; }
        if (result.status === "error") { setError(result.error || t("mcp.loginFailed")); return; }
        timer = setTimeout(() => { void poll(); }, 750);
      } catch (failure) { if (!current.cancelled) setError(toastErrorMessage(failure)); }
    };
    void (async () => {
      try {
        const uri = server.oauth_redirect_uri ? new URL(server.oauth_redirect_uri) : null;
        const automatic = controls.capabilities.mcpBrowserLogin && (!uri || (uri.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(uri.hostname) && !uri.search && !uri.hash));
        if (automatic) {
          current.loginID = (await controls.startMCPLogin(server.name)).login_id;
          if (current.cancelled) { await cancel(); return; }
          setReady(true);
          void poll();
        } else {
          const auth = await controls.startMCPAuth(server.name);
          current.state = auth.state;
          if (current.cancelled) { await cancel(); return; }
          const url = new URL(auth.authorization_url);
          if (!["https:", "http:"].includes(url.protocol)) throw new Error(t("mcp.invalidAuthURL"));
          setManual(true);
          setReady(true);
          await controls.openExternal(auth.authorization_url);
        }
      } catch (failure) { if (!current.cancelled) setError(toastErrorMessage(failure)); }
    })();
    return () => { current.cancelled = true; clearTimeout(timer); void cancel().catch(() => {}); };
  }, [controls, server.name, server.oauth_redirect_uri, t]);

  async function finish(): Promise<void> {
    if (finishing) return;
    const current = attempt.current;
    setFinishing(true);
    setError("");
    try {
      if (!current.authorized) {
        let value = code.trim();
        if (/^https?:\/\//i.test(value)) {
          const callback = new URL(value);
          if (callback.searchParams.get("state") !== current.state) throw new Error(t("mcp.invalidState"));
          value = callback.searchParams.get("code") || "";
        }
        if (!value) throw new Error(t("mcp.authCode"));
        await controls.finishMCPAuth(server.name, current.state!, value);
        if (current.cancelled) return;
        current.authorized = true;
        current.state = undefined;
        setAuthorized(true);
      }
      await controls.connectMCPServer(server.name);
      if (!current.cancelled) onClose();
    } catch (failure) { if (!current.cancelled) setError(toastErrorMessage(failure)); }
    finally { if (!current.cancelled) setFinishing(false); }
  }

  const title = t("mcp.loginNamed", { name: server.display_name || server.name });
  return <Modal ariaLabel={title} title={title} onClose={onClose} asForm onSubmit={() => void finish()} footer={<>
    <button type="button" className="settings-button" onClick={onClose}>{t("mcp.cancelLogin")}</button>
    {manual ? <button type="submit" className="settings-button settings-button-primary" disabled={(!authorized && !code.trim()) || finishing}>{t(authorized ? "mcp.connect" : "mcp.finishLogin")}</button> : null}
  </>}>
    <div className="mcp-form">
      {error ? <p className="mcp-error" role="alert">{error}</p> : <p role="status">{t(ready ? manual ? "mcp.manualHint" : "mcp.browserWaiting" : "mcp.loginStarting")}</p>}
      {manual && !authorized ? <label className="mcp-field">{t("mcp.callbackCode")}<input className="settings-input" value={code} onChange={event => setCode(event.target.value)} autoComplete="off" /></label> : null}
    </div>
  </Modal>;
}
