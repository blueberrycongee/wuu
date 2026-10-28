import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import type { MCPServerStatus } from "../shared/protocol";
import type { MCPDefinitionResult } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { Modal } from "./Modal";
import { MCPServerEditor } from "./MCPServerEditor";
import { MCPDiagnostic, MCPFailure } from "./MCPDiagnostic";
import { confirmAction } from "./ConfirmDialog";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import { AlertCircle, AlertTriangle, MoreHorizontal, Plug } from "./WuuIcons";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";

type MCPAction = "connect" | "disconnect" | "refresh" | "login" | "logout" | "edit" | "remove";
type MCPActionFailure = { action: MCPAction; diagnostic: string };

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
  const [errors, setErrors] = useState<Record<string, MCPActionFailure>>({});
  const [busy, setBusy] = useState("");
  const inFlight = useRef(false);
  const mounted = useRef(true);
  const currentControls = useRef(controls);
  currentControls.current = controls;
  const [editor, setEditor] = useState<{ name: string; definition?: MCPDefinitionResult } | null>(null);
  const [login, setLogin] = useState<MCPServerStatus | null>(null);
  const [menu, setMenu] = useState<{ name: string; x: number; y: number } | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { setErrors({}); setBusy(""); inFlight.current = false; }, [controls]);
  useEffect(() => {
    // A dismissed login can still finish connecting. An authoritative ready
    // snapshot settles that failure, but not an unrelated edit or refresh.
    setErrors(previous => {
      const recovered = servers.filter(server => server.connected && ["ready", "connected"].includes(server.state) && !server.error
        && (previous[server.name]?.action === "connect" || previous[server.name]?.action === "login"));
      if (!recovered.length) return previous;
      const next = { ...previous };
      for (const server of recovered) delete next[server.name];
      return next;
    });
  }, [servers]);
  const visible = servers.filter(server => (server.plugin_id || undefined) === pluginID
    && (server.display_name || server.name).toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()));

  function clearError(name: string): void {
    setErrors(previous => { const next = { ...previous }; delete next[name]; return next; });
  }

  function current(): boolean {
    return mounted.current && currentControls.current === controls;
  }

  async function act(name: string, kind: MCPAction, action: () => Promise<unknown>): Promise<void> {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(name);
    clearError(name);
    try { await action(); }
    catch (failure) { if (current()) setErrors(previous => ({ ...previous, [name]: { action: kind, diagnostic: toastErrorMessage(failure) } })); }
    finally {
      if (current()) {
        await refresh();
        if (current()) { inFlight.current = false; setBusy(""); }
      }
    }
  }

  async function remove(server: MCPServerStatus): Promise<void> {
    const definition = await controls.readMCPDefinition(server.name);
    if (!current()) return;
    if (!await confirmAction({ title: t("mcp.removeTitle", { name: server.name }), message: t("mcp.removeHint"), confirmLabel: t("common.remove"), tone: "danger" })) return;
    if (!current()) return;
    await controls.configureMCPServers({ servers: { [server.name]: null }, revision: definition.revision });
  }

  if ((pluginID || filter.trim()) && visible.length === 0 && !loadError) return null;
  return <SettingsSection testID="mcp-services" title={pluginID ? "MCP" : t("mcp.services")} actions={!pluginID ? <button type="button" className="settings-button" onClick={() => setEditor({ name: "" })}>{t("mcp.add")}</button> : undefined}>
    {loadError ? <div className="mcp-load-failure">
      <MCPFailure message={t("mcp.listFailed")} diagnostic={loadError} />
      <button type="button" className="settings-button settings-button-ghost" data-testid="mcp-list-retry" onClick={() => void refresh()}>{t("mcp.refresh")}</button>
    </div> : null}
    {!enabled && visible.length > 0 ? <p className="settings-section-description">{t("mcp.enablePlugin")}</p> : null}
    {visible.length ? <SettingsGroup>{visible.map(server => {
      const name = server.display_name || server.name;
      const failure = errors[server.name];
      const working = busy === server.name && !failure;
      const needsAuth = ["auth_required", "needs_auth", "needs_client_registration"].includes(server.state);
      const diagnostic = working || login?.name === server.name ? "" : failure?.diagnostic || server.error;
      const status = working ? t("mcp.working")
        : failure?.action === "disconnect" ? t("mcp.disconnectFailed")
        : failure?.action === "refresh" ? t("mcp.refreshFailed")
        : failure?.action === "logout" ? t("mcp.logoutFailed")
        : failure?.action === "login" ? t("mcp.loginFailed")
        : failure?.action === "edit" ? t("mcp.editFailed")
        : failure?.action === "remove" ? t("mcp.removeFailed")
        : needsAuth ? t("mcp.needsAuth")
        : failure || server.state === "failed" || server.state === "error" || server.error ? t("mcp.connectionFailed")
        : server.connected ? `${t("mcp.connected")} · ${t("mcp.toolCount", { count: server.tool_count })}`
        : ["starting", "connecting", "reconnecting"].includes(server.state) ? t("mcp.connecting")
        : server.state === "disabled" ? t("mcp.disabledByConfig") : t("mcp.disconnected");
      const attention = working ? undefined : needsAuth ? "auth" : failure || server.error || ["error", "failed"].includes(server.state) ? "error" : undefined;
      const actions: ThreadContextMenuItem[] = [];
      if (enabled && server.state !== "disabled") {
        if (server.connected) actions.push({ label: t("mcp.refresh"), onSelect: () => act(server.name, "refresh", () => controls.refreshMCPServer(server.name)) });
        if (server.auth_status === "oauth") actions.push({ label: t("mcp.removeLogin"), onSelect: () => act(server.name, "logout", () => controls.removeMCPAuth(server.name)) });
      }
      if (server.editable) {
        actions.push({ label: t("common.edit"), onSelect: () => act(server.name, "edit", async () => { const definition = await controls.readMCPDefinition(server.name); if (current()) setEditor({ name: server.name, definition }); }) });
        actions.push({ label: t("common.remove"), danger: true, onSelect: () => act(server.name, "remove", () => remove(server)) });
      }
      return <div className="mcp-service" key={server.name} data-mcp={server.name} aria-busy={busy === server.name}>
        <Plug className="icon mcp-service-icon" aria-hidden="true" />
        <div className="mcp-service-copy">
          <div className="mcp-service-name">{name}</div>
          <div className="mcp-service-status" data-attention={attention} role={attention || working ? "status" : undefined}>
            {attention === "auth" ? <AlertTriangle className="icon-sm" aria-hidden="true" /> : attention ? <AlertCircle className="icon-sm" aria-hidden="true" /> : null}
            <span>{status}</span>
          </div>
          {diagnostic ? <MCPDiagnostic key={diagnostic} message={diagnostic} /> : null}
          {!pluginID && !server.editable ? <p className="mcp-service-status">{t("mcp.projectManaged")}</p> : null}
        </div>
        <div className="mcp-service-actions">
          {enabled && server.state !== "disabled" ? <>
            {needsAuth ? <button type="button" className="settings-button" disabled={!!busy} onClick={() => setLogin(server)}>{t("mcp.oauthLogin")}</button>
              : <button type="button" className="settings-button" disabled={!!busy} onClick={() => void act(server.name, server.connected ? "disconnect" : "connect", () => server.connected ? controls.disconnectMCPServer(server.name) : controls.connectMCPServer(server.name))}>{t(server.connected ? "mcp.disconnect" : "mcp.connect")}</button>}
          </> : null}
          {actions.length ? <button type="button" className="settings-button settings-button-ghost settings-icon-button" disabled={!!busy}
            aria-label={t("provider.moreActions", { name })} aria-haspopup="menu" aria-expanded={menu?.name === server.name}
            onClick={event => { const bounds = event.currentTarget.getBoundingClientRect(); setMenu({ name: server.name, x: bounds.right, y: bounds.bottom }); }}>
            <MoreHorizontal className="icon" aria-hidden="true" />
          </button> : null}
          {menu?.name === server.name ? <ThreadContextMenu x={menu.x} y={menu.y} items={actions} onClose={() => setMenu(null)} /> : null}
        </div>
      </div>;
    })}</SettingsGroup> : !loadError ? <p className="settings-group-empty">{t(filter ? "skills.noMatches" : "mcp.empty")}</p> : null}
    {editor ? <MCPServerEditor name={editor.name} definition={editor.definition} onClose={() => setEditor(null)} onSaved={() => { if (current()) { setEditor(null); void refresh(); } }} /> : null}
    {login ? <MCPLogin server={login} onClose={failure => {
      if (failure) setErrors(previous => ({ ...previous, [login.name]: failure }));
      setLogin(null);
      void refresh();
    }} onConnected={() => { clearError(login.name); setLogin(null); void refresh(); }} /> : null}
  </SettingsSection>;
}

function MCPLogin({ server, onClose, onConnected }: { server: MCPServerStatus; onClose(failure?: MCPActionFailure): void; onConnected(): void }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const [error, setError] = useState("");
  const [manual, setManual] = useState(false);
  const [ready, setReady] = useState(false);
  const [code, setCode] = useState("");
  const [finishing, setFinishing] = useState(false);
  const [authorized, setAuthorized] = useState(false);
  const [retry, setRetry] = useState(0);
  const attempt = useRef<{ loginID?: string; state?: string; authorized?: boolean; cancelled: boolean }>({ cancelled: false });
  const complete = useRef(onConnected);
  complete.current = onConnected;
  useEffect(() => {
    const current: typeof attempt.current = { cancelled: false };
    attempt.current = current;
    setAuthorized(false);
    setManual(false);
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
        if (result.status === "success") { complete.current(); return; }
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
  }, [controls, server.name, server.oauth_redirect_uri, t, retry]);

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
      if (!current.cancelled) onConnected();
    } catch (failure) { if (!current.cancelled) setError(toastErrorMessage(failure)); }
    finally { if (!current.cancelled) setFinishing(false); }
  }

  const title = t("mcp.loginNamed", { name: server.display_name || server.name });
  const dismiss = () => onClose(error ? { action: authorized ? "connect" : "login", diagnostic: error } : undefined);
  return <Modal ariaLabel={title} title={title} onClose={dismiss} asForm onSubmit={() => void finish()} footer={<>
    <button type="button" className="settings-button" onClick={dismiss}>{t("mcp.cancelLogin")}</button>
    {error && !manual ? <button type="button" className="settings-button settings-button-primary" data-testid="mcp-login-retry" onClick={() => setRetry(value => value + 1)}>{t("mcp.retry")}</button> : null}
    {manual ? <button type="submit" className="settings-button settings-button-primary" disabled={(!authorized && !code.trim()) || finishing}>{t(authorized ? "mcp.connect" : "mcp.finishLogin")}</button> : null}
  </>}>
    <div className="mcp-form">
      {error ? <MCPFailure message={t(authorized ? "mcp.connectionFailed" : "mcp.loginFailed")} diagnostic={error} /> : <p role="status">{t(ready ? manual ? "mcp.manualHint" : "mcp.browserWaiting" : "mcp.loginStarting")}</p>}
      {manual && !authorized ? <label className="mcp-field">{t("mcp.callbackCode")}<input className="settings-input" value={code} onChange={event => setCode(event.target.value)} autoComplete="off" /></label> : null}
    </div>
  </Modal>;
}
