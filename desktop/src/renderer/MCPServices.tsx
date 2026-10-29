import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
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
import { AlertCircle, AlertTriangle, ChevronRight, MoreHorizontal, Plug } from "./WuuIcons";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";

type MCPAction = "connect" | "disconnect" | "refresh" | "identity" | "login" | "logout" | "edit" | "remove";
type MCPActionFailure = { action: MCPAction; diagnostic: string };

const MCPContext = createContext<{ servers: MCPServerStatus[]; retainedConnectors: MCPServerStatus[]; error: string; contextGeneration: number; refresh(): Promise<void> } | null>(null);
export function useMCPServices() {
  const value = useContext(MCPContext);
  if (!value) throw new Error("MCP services require a catalog provider");
  return value;
}

export function MCPServicesProvider({ children, contextKey }: { children: ReactNode; contextKey?: string }): JSX.Element {
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
      name: server.name, plugin_id: server.plugin_id, connector: { title: server.connector!.title },
      state: "reconnecting", connected: false, tool_count: 0, plugin_active: false, configurable: false,
    })) : [];
  return <MCPContext.Provider value={{ servers: fresh ? servers : [], retainedConnectors, error, contextGeneration, refresh }}>{children}</MCPContext.Provider>;
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

type ConnectorAuth = { result?: Awaited<ReturnType<ApplicationSettingsControlsV1["getMCPAuthStatus"]>>; error?: string };

function MCPConnectors({ pluginID, enabled = true, filter = "" }: MCPServicesProps): JSX.Element | null {
  const controls = useSettingsControls();
  const { servers, retainedConnectors, refresh } = useMCPServices();
  const retained = !servers.length && retainedConnectors.length > 0;
  const { t } = useI18n();
  const scope = useMemo(() => ({}), [controls]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const confirmation = useRef<AbortController | null>(null);
  const detailsID = useId();
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [auth, setAuth] = useState<Record<string, ConnectorAuth>>({});
  const [errors, setErrors] = useState<Record<string, MCPActionFailure>>({});
  const [busy, setBusy] = useState("");
  const [login, setLogin] = useState<{ server: MCPServerStatus; controls: ApplicationSettingsControlsV1 } | null>(null);
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
    setErrors({}); setAuth({}); setExpanded({}); setBusy(""); setLogin(null); setPreparing(undefined); inFlight.current = false;
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
      if (server) setLogin({ server, controls });
    }).catch(failure => {
      if (!current() || revision !== loginIntentRevision.current || isPluginLifecycleCancellation(failure)) return;
      setPreparing(undefined);
      setErrors(previous => ({ ...previous, [name]: { action: "login", diagnostic: toastErrorMessage(failure) } }));
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

  const loginAvailable = login && login.controls === controls && enabled && servers.some(server => server.name === login.server.name
    && server.configurable && server.plugin_active !== false && !server.connector?.setup_required?.trim());
  useEffect(() => { if (login && !loginAvailable) setLogin(null); }, [login, loginAvailable]);

  function current(): boolean { return mounted.current && currentControls.current === controls && currentScope.current === scope; }
  async function confirm(request: Parameters<typeof confirmAction>[0]): Promise<boolean> {
    const owner = new AbortController();
    confirmation.current = owner;
    try { return await confirmAction(request, owner.signal); }
    finally { if (confirmation.current === owner) confirmation.current = null; }
  }
  function clearError(name: string): void {
    setErrors(previous => { const next = { ...previous }; delete next[name]; return next; });
  }
  async function act(name: string, action: MCPAction, operation: () => Promise<void>): Promise<void> {
    if (inFlight.current || login || preparing) return;
    inFlight.current = true;
    setBusy(name);
    clearError(name);
    try { await operation(); }
    catch (failure) { if (current() && !isPluginLifecycleCancellation(failure)) setErrors(previous => ({ ...previous, [name]: { action, diagnostic: toastErrorMessage(failure) } })); }
    finally {
      if (current()) {
        try { await refresh(); }
        finally { if (current()) { inFlight.current = false; setBusy(""); } }
      }
    }
  }
  async function connect(server: MCPServerStatus, reconnect: boolean): Promise<void> {
    if (reconnect && !await confirm({ title: t("connector.reconnectTitle", { name: server.connector!.title }), message: t("connector.reconnectHint"), confirmLabel: t("connector.reconnect") })) return;
    if (!current()) return;
    if (controls.requestMCPConnectorLogin && controls.takeMCPConnectorLogin && controls.getPendingMCPConnectorLogin && controls.cancelMCPConnectorLogin) {
      setPreparing(server.name);
      const revision = loginIntentRevision.current;
      try {
        await controls.requestMCPConnectorLogin(server.name);
        if (!current() || revision !== loginIntentRevision.current) return;
        // An already-enabled connector needs no graph publication. The owner
        // still validates the current provider/resource and consumes once.
        const ready = await controls.takeMCPConnectorLogin();
        if (ready && current() && revision === loginIntentRevision.current) { setPreparing(undefined); setLogin({ server: ready, controls }); }
      }
      catch (error) { if (current()) setPreparing(undefined); throw error; }
      return; // Only committed successor controls may open OAuth.
    }
    const definition = await controls.readMCPDefinition(server.name);
    if (!current()) return;
    await controls.configureMCPServers({ servers: { [server.name]: { enabled: true } }, revision: definition.revision });
    if (current()) setLogin({ server, controls });
  }
  async function disconnect(server: MCPServerStatus): Promise<void> {
    if (!await confirm({ title: t("connector.disconnectTitle", { name: server.connector!.title }), message: t("connector.disconnectHint"), confirmLabel: t("mcp.disconnect"), tone: "danger" })) return;
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
    if (failures.length) throw new Error(failures.join("\n"));
  }

  if (!visible.length) return null;
  return <SettingsSection testID="mcp-connectors" title={t("connector.title")}>
    <SettingsGroup>{visible.map(server => {
      const connector = server.connector!;
      const setupRequired = connector.setup_required?.trim();
      const detailsOpen = !!expanded[server.name];
      const detailsTarget = `${detailsID}-${server.name}`;
      const authorization = retained ? undefined : auth[server.name];
      const authorized = authorization?.result?.authenticated;
      const identity = authorized ? authorization.result?.identity : undefined;
      const account = identity?.state === "verified" ? identity.account : undefined;
      const workspace = account ? identity?.workspace : undefined;
      const accountLabel = account ? `${account.label || account.email || account.id}${account.kind === "bot" ? ` (${t("connector.bot")})` : ""}` : "";
      const active = !retained && enabled && server.plugin_active !== false;
      const available = active && server.configurable && !setupRequired;
      const connected = server.connected && ["ready", "connected"].includes(server.state) && !server.error;
      const failure = errors[server.name];
      const working = retained || busy === server.name || preparing === server.name || login?.server.name === server.name;
      const needsAuth = ["auth_required", "needs_auth", "needs_client_registration"].includes(server.state);
      const attention = !working && (failure || !needsAuth && server.error);
      const saved = server.configuration_enabled || server.connected || authorized || server.auth_status === "oauth" || failure?.action === "disconnect";
      const reconnect = connected || authorized || server.auth_status === "oauth";
      const diagnostic = working || login?.server.name === server.name ? "" : failure?.diagnostic || server.error;
      const status = working ? t("mcp.working") : setupRequired ? t("connector.setupRequired")
        : !active ? t("connector.inactive")
        : !server.configurable ? t("connector.unavailable")
        : failure?.action === "disconnect" ? t("connector.disconnectIncomplete")
        : (!failure || failure.action === "identity") && needsAuth ? t("mcp.needsAuth")
        : (failure && failure.action !== "identity") || server.error || ["failed", "error"].includes(server.state) ? t("mcp.connectionFailed")
        : connected ? `${t("mcp.connected")} · ${t("mcp.toolCount", { count: server.tool_count })}`
        : ["starting", "connecting", "reconnecting"].includes(server.state) ? t("mcp.connecting")
        : ["auth_required", "needs_auth", "needs_client_registration"].includes(server.state) ? t("mcp.needsAuth")
        : t("mcp.disconnected");
      let documentationURL: string | undefined;
      if (connector.documentation_url) {
        try { if (new URL(connector.documentation_url).protocol === "https:") documentationURL = connector.documentation_url; }
        catch { /* An invalid plugin link must not launch an external application. */ }
      }
      return <div className="mcp-service mcp-connector" key={server.name} data-connector={server.name} data-connected={connected} data-authorized={authorized === undefined ? "unknown" : authorized} aria-busy={working}>
        <Plug className="icon mcp-service-icon" aria-hidden="true" />
        <div className="mcp-service-copy">
          <div className="mcp-service-name">{connector.title}</div>
          <div className="mcp-service-status" data-attention={attention ? "error" : undefined} role={failure || working || needsAuth ? "status" : undefined}>
            {attention ? <AlertCircle className="icon-sm" aria-hidden="true" /> : null}<span>{status}</span>
          </div>
          {account ? <div className="mcp-service-status" data-connector-identity>{accountLabel}{workspace ? ` · ${workspace.label || workspace.id}` : ""}</div> : null}
          {diagnostic ? <MCPDiagnostic message={diagnostic} /> : null}
        </div>
        <div className="mcp-service-actions">
          {available ? <button type="button" className="settings-button" data-connector-action={reconnect ? "reconnect" : "connect"} disabled={!!busy || !!login || !!preparing}
            onClick={() => void act(server.name, "connect", () => connect(server, !!reconnect))}>{t(reconnect ? "connector.reconnect" : "mcp.connect")}</button> : null}
          {active && server.configurable && saved ? <button type="button" className="settings-button settings-button-ghost" data-connector-action="disconnect" disabled={!!busy || !!login || !!preparing}
            onClick={() => void act(server.name, "disconnect", () => disconnect(server))}>{t("mcp.disconnect")}</button> : null}
          {documentationURL ? <button type="button" className="settings-button settings-button-ghost" data-connector-action="documentation" disabled={retained || !!busy || !!login || !!preparing}
            onClick={() => void act(server.name, "edit", () => controls.openExternal(documentationURL!))}>{t("connector.documentation")}</button> : null}
          <button type="button" className="settings-button settings-button-ghost" disabled={retained} data-connector-details-toggle aria-expanded={detailsOpen} aria-controls={detailsTarget}
            onClick={() => setExpanded(previous => ({ ...previous, [server.name]: !detailsOpen }))}>
            <ChevronRight className="icon-sm settings-disclosure-chevron" aria-hidden="true" />{t("mcp.connectionDetails")}
          </button>
        </div>
        {detailsOpen ? <div className="mcp-connector-expanded" id={detailsTarget}>
          {connector.description ? <p className="mcp-connector-description">{connector.description}</p> : null}
          {setupRequired ? <p className="mcp-connector-description">{setupRequired}</p> : null}
          {server.configuration_unavailable_reason ? <p className="mcp-connector-description">{server.configuration_unavailable_reason}</p> : null}
          <p className="mcp-connector-description">{t("connector.accountScope")}</p>
          {connector.identity_probe ? <p className="mcp-connector-description">{t("connector.identityHint")}</p> : null}
          <dl className="mcp-connector-details">
            {server.resource_url ? <><dt>{t("connector.endpoint")}</dt><dd data-connector-detail="endpoint">{server.resource_url}</dd></> : null}
            <dt>{t("connector.authorization")}</dt><dd>{!active || server.configuration_unavailable_reason ? t("connector.unavailable") : authorization?.error ? t("connector.authUnavailable") : authorized === undefined ? t("connector.authLoading") : t(authorized ? "connector.authorized" : "connector.notAuthorized")}</dd>
            {authorized ? <><dt>{t("connector.account")}</dt><dd data-connector-detail="account" data-verified={!!account}>{account ? accountLabel : t(identity?.state === "unavailable" ? "connector.identityUnavailable" : "connector.accountUnknown")}</dd></> : null}
            {account ? <>
              <dt>{t("connector.accountID")}</dt><dd data-connector-detail="account-id">{account.id}</dd>
              {account.email ? <><dt>{t("connector.email")}</dt><dd data-connector-detail="email">{account.email}</dd></> : null}
              {workspace ? <><dt>{t("connector.workspace")}</dt><dd data-connector-detail="workspace">{workspace.label || workspace.id}</dd>
                <dt>{t("connector.workspaceID")}</dt><dd data-connector-detail="workspace-id">{workspace.id}</dd></> : null}
              {identity?.verified_at ? <><dt>{t("connector.verifiedAt")}</dt><dd data-connector-detail="verified-at">{identity.verified_at}</dd></> : null}
            </> : null}
            {authorization?.result ? <><dt>{t("connector.scopes")}</dt><dd data-connector-detail="scopes">{authorization.result.scopes?.length ? authorization.result.scopes.join(", ") : t("connector.noScopes")}</dd></> : null}
          </dl>
          {available && connected && authorized && connector.identity_probe ? <button type="button" className="settings-button settings-button-ghost" data-connector-action="identity" disabled={!!busy || !!login}
            onClick={() => void act(server.name, "identity", async () => {
              const result = await controls.refreshMCPIdentity(server.name);
              if (current()) {
                setAuth(previous => ({ ...previous, [server.name]: { result } }));
                // A credential-store failure cannot persist its own diagnostic.
                if (result.identity?.error_code === "identity_credentials_unavailable") throw new Error(t("connector.identityRetryFailed"));
              }
            })}>{t(account ? "connector.refreshIdentity" : "connector.verifyIdentity")}</button> : null}
          {authorization?.error ? <MCPDiagnostic message={authorization.error} /> : null}
        </div> : null}
      </div>;
    })}</SettingsGroup>
    {preparing ? <Modal ariaLabel={t("mcp.loginNamed", { name: visible.find(server => server.name === preparing)?.connector?.title || preparing })}
      title={t("mcp.working")} onClose={() => { loginIntentRevision.current++; controls.cancelMCPConnectorLogin?.(); setPreparing(undefined); }}
      footer={<button type="button" className="settings-button" onClick={() => { loginIntentRevision.current++; controls.cancelMCPConnectorLogin?.(); setPreparing(undefined); }}>{t("mcp.cancelLogin")}</button>} /> : null}
    {loginAvailable ? <MCPLogin server={login.server} onClose={failure => {
      if (!current()) return;
      if (failure) setErrors(previous => ({ ...previous, [login.server.name]: failure }));
      setLogin(null);
      void refresh();
    }} onConnected={() => { if (current()) { clearError(login.server.name); setLogin(null); void refresh(); } }} /> : null}
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

  const title = t("mcp.loginNamed", { name: server.connector?.title || server.display_name || server.name });
  const dismiss = () => onClose(error ? { action: authorized ? "connect" : "login", diagnostic: error } : undefined);
  return <Modal ariaLabel={title} title={title} onClose={dismiss} asForm onSubmit={() => void finish()} footer={<>
    <button type="button" className="settings-button" onClick={dismiss}>{t("mcp.cancelLogin")}</button>
    {error && !manual ? <button type="button" className="settings-button settings-button-primary" data-testid="mcp-login-retry" onClick={() => setRetry(value => value + 1)}>{t("mcp.retry")}</button> : null}
    {manual ? <button type="submit" className="settings-button settings-button-primary" disabled={(!authorized && !code.trim()) || finishing}>{t(authorized ? "mcp.connect" : "mcp.finishLogin")}</button> : null}
  </>}>
    <div className="mcp-form">
      {error ? <MCPFailure message={t(authorized ? "mcp.connectionFailed" : "mcp.loginFailed")} diagnostic={error} /> : <p role="status">{t(ready ? manual ? "mcp.manualHint" : "mcp.browserWaiting" : "mcp.loginStarting")}</p>}
      {server.connector?.identity_probe ? <p>{t("connector.identityHint")}</p> : null}
      {manual && !authorized ? <label className="mcp-field">{t("mcp.callbackCode")}<input className="settings-input" value={code} onChange={event => setCode(event.target.value)} autoComplete="off" /></label> : null}
    </div>
  </Modal>;
}
