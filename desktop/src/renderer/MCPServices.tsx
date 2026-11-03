import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { MCPServerStatus } from "../shared/protocol";
import type { ApplicationSettingsControlsV1, MCPDefinitionResult } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { Modal } from "./Modal";
import { MCPServerEditor } from "./MCPServerEditor";
import { MCPDiagnostic, MCPFailure } from "./MCPDiagnostic";
import { confirmAction } from "./ConfirmDialog";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import { showExtensionMutationError } from "./extensionMutationError";
import { AlertCircle, AlertTriangle, MoreHorizontal } from "./WuuIcons";
import { ConnectorMark, type ConnectorIconCatalog } from "./ConnectorMark";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { usesAutomaticMCPLogin } from "./MCPLoginTransport";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";

type MCPAction = "connect" | "disconnect" | "refresh" | "identity" | "login" | "logout" | "edit" | "remove";
type MCPActionFailure = { action: MCPAction; diagnostic: string };

const MCPContext = createContext<{ servers: MCPServerStatus[]; retainedConnectors: MCPServerStatus[]; error: string; contextGeneration: number; catalog?: ConnectorIconCatalog; refresh(): Promise<void> } | null>(null);
export function useMCPServices() {
  const value = useContext(MCPContext);
  if (!value) throw new Error("MCP services require a catalog provider");
  return value;
}

export function MCPServicesProvider({ children, contextKey, catalog }: { children: ReactNode; contextKey?: string; catalog?: ConnectorIconCatalog }): JSX.Element {
  const controls = useSettingsControls();
  const [servers, setServers] = useState<MCPServerStatus[]>([]);
  const [contextGeneration, setContextGeneration] = useState(0);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const owner = useRef({ controls, contextKey });
  const snapshot = useRef<{ controls: ApplicationSettingsControlsV1; contextKey?: string; servers: MCPServerStatus[] } | undefined>(undefined);
  if (owner.current.contextKey !== contextKey) snapshot.current = undefined;
  owner.current = { controls, contextKey };
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    try {
      const result = await controls.listMCPServers();
      if (current !== generation.current || owner.current.controls !== controls || owner.current.contextKey !== contextKey) return;
      snapshot.current = { controls, contextKey, servers: result.servers };
      setServers(result.servers);
      setError("");
    } catch (failure) {
      if (current === generation.current && !isPluginLifecycleCancellation(failure)) setError(toastErrorMessage(failure));
    }
  }, [controls, contextKey]);
  useEffect(() => {
    generation.current++;
    setServers([]);
    setError("");
    void refresh();
    const timer = setInterval(() => { void refresh(); }, 5000);
    const unsubscribe = controls.subscribe(() => { void refresh(); });
    const remote = controls.subscribeRemote(() => {
      generation.current++;
      snapshot.current = undefined;
      setServers([]);
      setContextGeneration(value => value + 1);
      void refresh();
    });
    return () => { generation.current++; clearInterval(timer); unsubscribe(); remote(); };
  }, [controls, refresh]);
  const fresh = snapshot.current?.controls === controls && snapshot.current.contextKey === contextKey;
  // A pinned context may retain card labels across a writer gap, never action
  // authority, credentials, identity, or generic MCP editor state.
  const retainedConnectors = !fresh && contextKey && snapshot.current?.contextKey === contextKey
    ? snapshot.current.servers.filter(server => server.plugin_id && server.connector).map(server => ({
      name: server.name, plugin_id: server.plugin_id, connector: { title: server.connector!.title, icon: server.connector!.icon },
      state: "reconnecting", connected: false, tool_count: 0, plugin_active: false, configurable: false,
    })) : [];
  return <MCPContext.Provider value={{ servers: fresh ? servers : [], retainedConnectors, error, contextGeneration, catalog, refresh }}>{children}</MCPContext.Provider>;
}

type MCPServicesProps = { pluginID?: string; enabled?: boolean; filter?: string };

export function MCPServices(props: MCPServicesProps): JSX.Element {
  const { contextGeneration } = useMCPServices();
  return <>
    <MCPConnectors key={`${contextGeneration}:${props.pluginID || ""}`} {...props} />
    <MCPServerServices {...props} />
  </>;
}

function MCPServerServices({ pluginID, enabled = true, filter = "" }: MCPServicesProps): JSX.Element | null {
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
  const visible = servers.filter(server => !(server.plugin_id && server.connector) && (server.plugin_id || undefined) === pluginID
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
        try { await refresh(); }
        finally { if (current()) { inFlight.current = false; setBusy(""); } }
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
        <ConnectorMark server={server} />
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

type ConnectorAuth = { result?: Awaited<ReturnType<ApplicationSettingsControlsV1["getMCPAuthStatus"]>>; error?: string };

function MCPConnectors({ pluginID, enabled = true, filter = "" }: MCPServicesProps): JSX.Element | null {
  const controls = useSettingsControls();
  const { servers, retainedConnectors, catalog, refresh } = useMCPServices();
  const retained = !servers.length && retainedConnectors.length > 0;
  const { t } = useI18n();
  const scope = useMemo(() => ({}), [controls]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const confirmation = useRef<AbortController | null>(null);
  const [menu, setMenu] = useState<{ name: string; x: number; y: number } | null>(null);
  const [details, setDetails] = useState<string>();
  const [auth, setAuth] = useState<Record<string, ConnectorAuth>>({});
  const [busy, setBusy] = useState("");
  const [login, setLogin] = useState<{ server: MCPServerStatus; controls: ApplicationSettingsControlsV1; managed: boolean } | null>(null);
  const [preparing, setPreparing] = useState<string>();
  const claimedScope = useRef<object | undefined>(undefined);
  const loginIntentRevision = useRef(0);
  const inFlight = useRef(false);
  const mounted = useRef(true);
  const currentControls = useRef(controls);
  currentControls.current = controls;
  const visible = (retained ? retainedConnectors : servers).filter(server => server.plugin_id && server.connector && (!pluginID || server.plugin_id === pluginID)
    && `${server.connector.title} ${server.connector.description || ""} ${server.name}`.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()));

  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    setAuth({}); setMenu(null); setDetails(undefined); setBusy(""); setLogin(null); setPreparing(undefined); inFlight.current = false;
    return () => { confirmation.current?.abort(); };
  }, [controls]);
  useEffect(() => {
    if (retained || claimedScope.current === scope || !enabled) return;
    let name: string | undefined, take: ApplicationSettingsControlsV1["takeMCPConnectorLogin"];
    try { take = controls.takeMCPConnectorLogin; name = controls.getPendingMCPConnectorLogin?.(); }
    catch (error) { if (isPluginLifecycleCancellation(error)) return; throw error; }
    if (!take) return;
    if (!name || !visible.some(server => server.name === name)) return;
    claimedScope.current = scope;
    const revision = loginIntentRevision.current;
    setPreparing(name);
    void take().then(server => {
      if (!current() || revision !== loginIntentRevision.current) return;
      setPreparing(undefined);
      if (server) attachLogin(server, true);
    }).catch(failure => {
      if (!current() || revision !== loginIntentRevision.current || isPluginLifecycleCancellation(failure)) return;
      setPreparing(undefined);
      showExtensionMutationError(failure, t("connector.connectFailed"));
    });
    // Server refreshes must not retire an already claimed receipt. The current
    // scope and explicit-cancel revision fence its eventual result instead.
  }, [controls, servers, enabled, scope, retained]);
  useEffect(() => {
    let cancelled = false;
    void Promise.all(servers.filter(server => server.plugin_id && server.connector && server.plugin_active !== false && !server.configuration_unavailable_reason).map(async server => {
      try { return [server.name, { result: await controls.getMCPAuthStatus(server.name) }] as const; }
      catch (failure) { return [server.name, { error: toastErrorMessage(failure) }] as const; }
    })).then(entries => { if (!cancelled) setAuth(Object.fromEntries(entries)); });
    return () => { cancelled = true; };
  }, [controls, servers]);

  const listedLoginAvailable = login && servers.some(server => server.name === login.server.name
    && server.configurable && server.plugin_active !== false && !server.connector?.setup_required?.trim()
    && (!login.managed || server.configuration_enabled === true && server.resource_url === login.server.resource_url));
  const loginAvailable = login && login.controls === controls && enabled && (login.managed || listedLoginAvailable);
  useEffect(() => {
    if (!login) return;
    if (!loginAvailable) {
      if (login.controls === controls && !retained) {
        try { controls.cancelMCPConnectorLogin?.(); }
        catch (error) { if (!isPluginLifecycleCancellation(error)) throw error; }
      }
      setLogin(null);
      return;
    }
    if (!login.managed || listedLoginAvailable || retained) return;
    let cancelled = false;
    // A presentation refresh may have started before the owner's admission
    // read. Only a fresh owner validation can revoke its active continuation.
    void Promise.resolve().then(() => controls.takeMCPConnectorLogin!()).then(server => {
      if (!cancelled && current() && !server) setLogin(previous => previous === login ? null : previous);
    }).catch(failure => {
      if (!cancelled && current()) showExtensionMutationError(failure, t("connector.connectFailed"));
    });
    return () => { cancelled = true; };
  }, [login, loginAvailable, listedLoginAvailable, controls, retained, servers]);

  function attachLogin(server: MCPServerStatus, owned: boolean): void {
    const managed = owned && !!controls.startMCPConnectorLogin && !!controls.pollMCPConnectorLogin && !!controls.retryMCPConnectorLogin
      && usesAutomaticMCPLogin(server, !!controls.capabilities.mcpBrowserLogin);
    claimedScope.current = scope;
    setLogin({ server, controls, managed });
  }
  function current(): boolean { return mounted.current && currentControls.current === controls && currentScope.current === scope; }
  async function confirm(request: Parameters<typeof confirmAction>[0]): Promise<boolean> {
    const owner = new AbortController();
    confirmation.current = owner;
    try { return await confirmAction(request, owner.signal); }
    finally { if (confirmation.current === owner) confirmation.current = null; }
  }
  // Connector failures are reported once as toasts; the card keeps showing
  // the backend's state, and the raw diagnostic stays in connection details.
  const failureMessages = {
    connect: "connector.connectFailed", disconnect: "connector.disconnectIncomplete", identity: "connector.identityRetryFailed", edit: "connector.openFailed",
  } as const;
  async function act(name: string, action: keyof typeof failureMessages, operation: () => Promise<void>): Promise<void> {
    if (inFlight.current || login || preparing) return;
    inFlight.current = true;
    setBusy(name);
    try { await operation(); }
    catch (failure) { if (current()) showExtensionMutationError(failure, t(failureMessages[action])); }
    finally {
      if (current()) {
        try { await refresh(); }
        finally { if (current()) { inFlight.current = false; setBusy(""); } }
      }
    }
  }
  async function connect(server: MCPServerStatus): Promise<void> {
    if (controls.requestMCPConnectorLogin && controls.takeMCPConnectorLogin && controls.getPendingMCPConnectorLogin && controls.cancelMCPConnectorLogin) {
      setPreparing(server.name);
      const revision = loginIntentRevision.current;
      try {
        await controls.requestMCPConnectorLogin(server.name);
        if (!current() || revision !== loginIntentRevision.current) return;
        // An already-enabled connector needs no graph publication. The owner
        // still validates the current provider/resource and consumes once.
        const ready = await controls.takeMCPConnectorLogin();
        if (ready && current() && revision === loginIntentRevision.current) { setPreparing(undefined); attachLogin(ready, true); }
      }
      catch (error) { if (current()) setPreparing(undefined); throw error; }
      return; // Only committed successor controls may open OAuth.
    }
    const definition = await controls.readMCPDefinition(server.name);
    if (!current()) return;
    await controls.configureMCPServers({ servers: { [server.name]: { enabled: true } }, revision: definition.revision });
    if (current()) attachLogin(server, false);
  }
  async function disconnect(server: MCPServerStatus): Promise<void> {
    if (!await confirm({ title: t("connector.disconnectTitle", { name: server.connector!.title }), message: t("connector.disconnectHint", { name: server.connector!.title }), confirmLabel: t("mcp.disconnect"), tone: "danger" })) return;
    if (!current()) return;
    // Cleanup is best-effort: a failed runtime stop must not prevent removing
    // local credentials or recording the user's disabled preference.
    const failures: string[] = [];
    for (const operation of [
      () => controls.disconnectMCPServer(server.name),
      () => controls.removeMCPAuth(server.name),
      async () => {
        const definition = await controls.readMCPDefinition(server.name);
        if (current()) await controls.configureMCPServers({ servers: { [server.name]: { enabled: false } }, revision: definition.revision });
      },
    ]) {
      if (!current()) return;
      try { await operation(); }
      catch (failure) { failures.push(toastErrorMessage(failure)); }
    }
    if (failures.length) {
      console.warn("Connector disconnect was incomplete:", failures.join("\n"));
      throw new Error(t("connector.disconnectIncomplete"));
    }
  }

  if (!visible.length && !loginAvailable && !preparing) return null;
  const locked = retained || !!busy || !!login || !!preparing;
  function describe(server: MCPServerStatus) {
    const connector = server.connector!;
    const setupRequired = connector.setup_required?.trim();
    const authorization = retained ? undefined : auth[server.name];
    const authorized = authorization?.result?.authenticated;
    const identity = authorized ? authorization.result?.identity : undefined;
    const account = identity?.state === "verified" ? identity.account : undefined;
    const workspace = account ? identity?.workspace : undefined;
    const accountLabel = account ? `${account.label || account.email || account.id}${account.kind === "bot" ? ` (${t("connector.bot")})` : ""}` : "";
    const active = !retained && enabled && server.plugin_active !== false;
    const available = active && server.configurable && !setupRequired;
    const connected = server.connected && ["ready", "connected"].includes(server.state) && !server.error;
    const working = retained || busy === server.name || preparing === server.name || login?.server.name === server.name;
    const needsAuth = ["auth_required", "needs_auth", "needs_client_registration"].includes(server.state);
    const failed = !needsAuth && (!!server.error || ["failed", "error"].includes(server.state));
    const attention = !working && failed;
    const saved = server.configuration_enabled || server.connected || authorized || server.auth_status === "oauth";
    const reconnect = connected || authorized || server.auth_status === "oauth";
    const status = working ? t("mcp.working") : setupRequired ? t("connector.setupRequired")
      : !active ? t("connector.inactive")
      : !server.configurable ? t("connector.unavailable")
      : needsAuth ? t("connector.needsSignIn")
      : failed ? t("connector.connectionFailed")
      : connected ? [t("mcp.connected"), accountLabel, workspace ? workspace.label || workspace.id : ""].filter(Boolean).join(" · ")
      : ["starting", "connecting", "reconnecting"].includes(server.state) ? t("mcp.connecting")
      : saved ? t("mcp.disconnected")
      : connector.description || t("mcp.disconnected");
    let documentationURL: string | undefined;
    if (connector.documentation_url) {
      try { if (new URL(connector.documentation_url).protocol === "https:") documentationURL = connector.documentation_url; }
      catch { /* An invalid plugin link must not launch an external application. */ }
    }
    return { connector, setupRequired, authorization, authorized, identity, account, workspace, accountLabel, active, available, connected, working, needsAuth, attention, saved, reconnect, status, documentationURL };
  }
  function verifyIdentity(server: MCPServerStatus): Promise<void> {
    return act(server.name, "identity", async () => {
      const result = await controls.refreshMCPIdentity(server.name);
      if (current()) {
        setAuth(previous => ({ ...previous, [server.name]: { result } }));
        // A credential-store failure cannot persist its own diagnostic.
        if (result.identity?.error_code === "identity_credentials_unavailable") throw new Error(t("connector.identityRetryFailed"));
      }
    });
  }
  const detailServer = details ? visible.find(server => server.name === details) : undefined;
  return <SettingsSection testID="mcp-connectors" title={t("connector.title")}>
    <SettingsGroup>{visible.map(server => {
      const view = describe(server);
      const items: ThreadContextMenuItem[] = [];
      if (view.available && view.connected) items.push({ label: t("connector.reconnect"), onSelect: () => act(server.name, "connect", () => connect(server)) });
      if (view.available && view.connected && view.authorized && view.connector.identity_probe && !view.account) items.push({ label: t("connector.verifyIdentity"), onSelect: () => verifyIdentity(server) });
      // A connector that was never connected offers just Connect; details and
      // links matter once there is a connection or a reason it cannot connect.
      const explains = !retained && (view.saved || !!view.setupRequired || !view.active || !server.configurable);
      if (explains) items.push({ label: t("mcp.connectionDetails"), onSelect: () => setDetails(server.name) });
      if (explains && view.documentationURL) items.push({ label: t("connector.documentation"), onSelect: () => act(server.name, "edit", () => controls.openExternal(view.documentationURL!)) });
      if (view.active && server.configurable && view.saved) items.push({ separator: true }, { label: t("mcp.disconnect"), danger: true, onSelect: () => act(server.name, "disconnect", () => disconnect(server)) });
      return <div className="mcp-service mcp-connector" key={server.name} data-connector={server.name} data-connected={view.connected} data-authorized={view.authorized === undefined ? "unknown" : view.authorized} aria-busy={view.working}>
        <ConnectorMark server={server} catalog={catalog} />
        <div className="mcp-service-copy">
          <div className="mcp-service-name">{view.connector.title}</div>
          <div className="mcp-service-status" data-attention={view.attention ? "error" : undefined} data-connector-identity={view.account ? "" : undefined}
            role={view.attention || view.working || view.needsAuth ? "status" : undefined}>
            {view.attention ? <AlertCircle className="icon-sm" aria-hidden="true" /> : null}<span>{view.status}</span>
          </div>
        </div>
        <div className="mcp-service-actions">
          {view.available && !view.connected ? <button type="button" className="settings-button" data-connector-action={view.reconnect ? "reconnect" : "connect"} disabled={locked}
            onClick={() => void act(server.name, "connect", () => connect(server))}>{t(view.reconnect ? "connector.reconnect" : "mcp.connect")}</button> : null}
          {items.length ? <button type="button" className="settings-button settings-button-ghost settings-icon-button" data-connector-menu disabled={locked}
            aria-label={t("provider.moreActions", { name: view.connector.title })} aria-haspopup="menu" aria-expanded={menu?.name === server.name}
            onClick={event => { const bounds = event.currentTarget.getBoundingClientRect(); setMenu({ name: server.name, x: bounds.right, y: bounds.bottom }); }}>
            <MoreHorizontal className="icon" aria-hidden="true" />
          </button> : null}
          {menu?.name === server.name ? <ThreadContextMenu x={menu.x} y={menu.y} items={items} onClose={() => setMenu(null)} /> : null}
        </div>
      </div>;
    })}</SettingsGroup>
    {detailServer ? (() => {
      const view = describe(detailServer);
      const { account, workspace, identity, authorization, authorized } = view;
      return <Modal ariaLabel={view.connector.title} icon={<ConnectorMark server={detailServer} catalog={catalog} />} title={view.connector.title} subtitle={view.status}
        panelClassName="mcp-connector-dialog" onClose={() => setDetails(undefined)}
        footer={view.available && view.connected && authorized && view.connector.identity_probe ? <button type="button" className="settings-button" data-connector-action="identity" disabled={locked}
          onClick={() => void verifyIdentity(detailServer)}>{t(account ? "connector.refreshIdentity" : "connector.verifyIdentity")}</button> : undefined}>
        <div className="mcp-dialog-body" data-connector-details={detailServer.name}>
          {view.setupRequired ? <p className="mcp-connector-description">{view.setupRequired}</p> : null}
          {detailServer.configuration_unavailable_reason ? <p className="mcp-connector-description">{detailServer.configuration_unavailable_reason}</p> : null}
          <dl className="mcp-connector-details">
            {detailServer.resource_url ? <><dt>{t("connector.endpoint")}</dt><dd data-connector-detail="endpoint">{detailServer.resource_url}</dd></> : null}
            <dt>{t("connector.authorization")}</dt><dd>{!view.active || detailServer.configuration_unavailable_reason ? t("connector.unavailable") : authorization?.error ? t("connector.authUnavailable") : authorized === undefined ? t("connector.authLoading") : t(authorized ? "connector.authorized" : "connector.notAuthorized")}</dd>
            {authorized ? <><dt>{t("connector.account")}</dt><dd data-connector-detail="account" data-verified={!!account}>{account ? view.accountLabel : t(identity?.state === "unavailable" ? "connector.identityUnavailable" : "connector.accountUnknown")}</dd></> : null}
            {account ? <>
              <dt>{t("connector.accountID")}</dt><dd data-connector-detail="account-id">{account.id}</dd>
              {account.email ? <><dt>{t("connector.email")}</dt><dd data-connector-detail="email">{account.email}</dd></> : null}
              {workspace ? <><dt>{t("connector.workspace")}</dt><dd data-connector-detail="workspace">{workspace.label || workspace.id}</dd>
                <dt>{t("connector.workspaceID")}</dt><dd data-connector-detail="workspace-id">{workspace.id}</dd></> : null}
              {identity?.verified_at ? <><dt>{t("connector.verifiedAt")}</dt><dd data-connector-detail="verified-at">{identity.verified_at}</dd></> : null}
            </> : null}
            {authorization?.result ? <><dt>{t("connector.scopes")}</dt><dd data-connector-detail="scopes">{authorization.result.scopes?.length ? authorization.result.scopes.join(", ") : t("connector.noScopes")}</dd></> : null}
          </dl>
          <p className="mcp-connector-description">{t("connector.accountScope")}</p>
          {[detailServer.error, authorization?.error].filter(Boolean).map(message => <MCPDiagnostic key={message} message={message!} />)}
        </div>
      </Modal>;
    })() : null}
    {preparing ? <Modal ariaLabel={t("mcp.loginNamed", { name: visible.find(server => server.name === preparing)?.connector?.title || preparing })}
      title={t("mcp.loginNamed", { name: visible.find(server => server.name === preparing)?.connector?.title || preparing })}
      onClose={() => { loginIntentRevision.current++; controls.cancelMCPConnectorLogin?.(); setPreparing(undefined); }}
      footer={<button type="button" className="settings-button" onClick={() => { loginIntentRevision.current++; controls.cancelMCPConnectorLogin?.(); setPreparing(undefined); }}>{t("mcp.cancelLogin")}</button>}>
      <div className="mcp-dialog-body"><p role="status">{t("mcp.loginStarting")}</p></div>
    </Modal> : null}
    {loginAvailable ? <MCPLogin server={login.server} managed={login.managed} icon={<ConnectorMark server={login.server} catalog={catalog} />} onClose={() => {
      // The sign-in dialog already showed any failure; dismissing it acknowledges it.
      if (!current()) return;
      controls.cancelMCPConnectorLogin?.();
      setLogin(null);
      void refresh();
    }} onConnected={() => { if (current()) { controls.cancelMCPConnectorLogin?.(); setLogin(null); void refresh(); } }} /> : null}
  </SettingsSection>;
}

function MCPLogin({ server, managed = false, icon, onClose, onConnected }: { server: MCPServerStatus; managed?: boolean; icon?: ReactNode; onClose(failure?: MCPActionFailure): void; onConnected(): void }): JSX.Element {
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
        const result = managed ? await controls.pollMCPConnectorLogin!() : await controls.pollMCPLogin(current.loginID!);
        if (current.cancelled) return;
        if (result.status === "success") { complete.current(); return; }
        if (result.status === "error") { setError(result.error || t("mcp.loginFailed")); return; }
        setReady(result.phase !== "starting");
        timer = setTimeout(() => { void poll(); }, 750);
      } catch (failure) { if (!current.cancelled && !isPluginLifecycleCancellation(failure)) setError(toastErrorMessage(failure)); }
    };
    void (async () => {
      try {
        if (server.oauth_redirect_uri) new URL(server.oauth_redirect_uri);
        const automatic = usesAutomaticMCPLogin(server, !!controls.capabilities.mcpBrowserLogin);
        if (automatic) {
          current.loginID = (await (managed ? controls.startMCPConnectorLogin!() : controls.startMCPLogin(server.name))).login_id;
          if (current.cancelled) { if (!managed) await cancel(); return; }
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
      } catch (failure) { if (!current.cancelled && !isPluginLifecycleCancellation(failure)) setError(toastErrorMessage(failure)); }
    })();
    return () => { current.cancelled = true; clearTimeout(timer); if (!managed) void cancel().catch(() => {}); };
  }, [controls, server.name, server.oauth_redirect_uri, t, retry, managed]);

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
    } catch (failure) { if (!current.cancelled && !isPluginLifecycleCancellation(failure)) setError(toastErrorMessage(failure)); }
    finally { if (!current.cancelled) setFinishing(false); }
  }

  const title = t("mcp.loginNamed", { name: server.connector?.title || server.display_name || server.name });
  const dismiss = () => onClose(error ? { action: authorized ? "connect" : "login", diagnostic: error } : undefined);
  return <Modal ariaLabel={title} icon={icon} title={title} onClose={dismiss} asForm onSubmit={() => void finish()} footer={<>
    <button type="button" className="settings-button" onClick={dismiss}>{t("mcp.cancelLogin")}</button>
    {error && !manual ? <button type="button" className="settings-button settings-button-primary" data-testid="mcp-login-retry" onClick={() => {
      if (managed) void controls.retryMCPConnectorLogin!().then(() => setRetry(value => value + 1)).catch(failure => {
        if (!attempt.current.cancelled) setError(toastErrorMessage(failure));
      });
      else setRetry(value => value + 1);
    }}>{t("mcp.retry")}</button> : null}
    {manual ? <button type="submit" className="settings-button settings-button-primary" disabled={(!authorized && !code.trim()) || finishing}>{t(authorized ? "mcp.connect" : "mcp.finishLogin")}</button> : null}
  </>}>
    <div className="mcp-dialog-body">
      {error ? <MCPFailure message={t(authorized ? "mcp.connectionFailed" : "mcp.loginFailed")} diagnostic={error} /> : <p role="status">{t(ready ? manual ? "mcp.manualHint" : "mcp.browserWaiting" : "mcp.loginStarting")}</p>}
      {manual && !authorized ? <label className="mcp-field">{t("mcp.callbackCode")}<input className="settings-input" value={code} onChange={event => setCode(event.target.value)} autoComplete="off" /></label> : null}
    </div>
  </Modal>;
}
