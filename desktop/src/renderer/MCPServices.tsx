import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { MCPServerStatus } from "../shared/protocol";
import type { ApplicationSettingsControlsV1, MCPDefinitionResult } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { useSettingsControls } from "./SettingsControlsContext";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { TruncatedText } from "./TruncatedText";
import { Modal } from "./Modal";
import { MCPServerEditor } from "./MCPServerEditor";
import { MCPDiagnostic, MCPFailure } from "./MCPDiagnostic";
import { confirmAction } from "./ConfirmDialog";
import { useI18n } from "./i18n";
import { dismissToast, showActionErrorToast, toastErrorMessage } from "./Toast";
import { showExtensionMutationError } from "./extensionMutationError";
import { AlertCircle, AlertTriangle, MoreHorizontal, Plus } from "./WuuIcons";
import { ConnectorMark, type ConnectorIconCatalog } from "./ConnectorMark";
import mascotIcon from "../../../assets/app-icon-source.svg";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { usesAutomaticMCPLogin } from "./MCPLoginTransport";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";

type MCPAction = "connect" | "disconnect" | "refresh" | "identity" | "login" | "logout" | "edit" | "remove";
type MCPActionFailure = { action: MCPAction; diagnostic: string };
type ConnectorRecovery = { context: string; name: string; pluginID?: string; resource?: string; owner: object; inFlight: boolean };
type DisconnectOutcome = "completed" | "cancelled" | "interrupted";

// A failed cleanup may publish a replacement graph before its feedback is
// rendered. Carry only the recovery intent, never credentials or authority.
const connectorRecoveries = new Map<string, ConnectorRecovery>();
const connectorRecoveryListeners = new Set<() => void>();
function recoveryKey(context: string, name: string): string { return JSON.stringify([context, name]); }
function forgetConnectorRecovery(recovery: ConnectorRecovery): void {
  const key = recoveryKey(recovery.context, recovery.name);
  if (connectorRecoveries.get(key) === recovery) connectorRecoveries.delete(key);
}
function notifyConnectorRecoveries(): void { for (const listener of connectorRecoveryListeners) listener(); }
function rememberConnectorRecovery(context: string, server: MCPServerStatus, owner: object): ConnectorRecovery {
  const recovery = { context, name: server.name, pluginID: server.plugin_id, resource: server.resource_url, owner, inFlight: false };
  connectorRecoveries.set(recoveryKey(context, server.name), recovery);
  window.setTimeout(() => forgetConnectorRecovery(recovery), 60_000);
  notifyConnectorRecoveries();
  return recovery;
}

const MCPContext = createContext<{ servers: MCPServerStatus[]; retainedConnectors: MCPServerStatus[]; error: string; contextGeneration: number; contextKey?: string; catalog?: ConnectorIconCatalog; refresh(): Promise<void> } | null>(null);
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
  return <MCPContext.Provider value={{ servers: fresh ? servers : [], retainedConnectors, error, contextGeneration, contextKey, catalog, refresh }}>{children}</MCPContext.Provider>;
}

type MCPServicesProps = { pluginID?: string; enabled?: boolean; filter?: string; catalogView?: boolean };

export function MCPServices(props: MCPServicesProps): JSX.Element {
  const { contextGeneration } = useMCPServices();
  return <>
    <MCPConnectors key={`${contextGeneration}:${props.pluginID || ""}`} {...props} />
    <MCPServerServices {...props} />
  </>;
}

// Display hints distinguish sources, never merge credentials or action targets.
function hasOtherConnectionSource(server: MCPServerStatus, servers: MCPServerStatus[]): boolean {
  const title = (server.connector?.title || server.display_name || server.name).trim().toLocaleLowerCase();
  return servers.some(other => other.name !== server.name
    && (server.plugin_id ? !other.plugin_id : !!other.plugin_id && !!other.connector)
    && ((other.connector?.title || other.display_name || other.name).trim().toLocaleLowerCase() === title
      || !!server.resource_url && server.resource_url === other.resource_url));
}

function connectorConnected(server: MCPServerStatus): boolean {
  return server.connected && ["ready", "connected"].includes(server.state) && !server.error;
}

function MCPServerServices({ pluginID, enabled = true, filter = "", catalogView = false }: MCPServicesProps): JSX.Element | null {
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

  if ((catalogView || pluginID || filter.trim()) && visible.length === 0 && !loadError && !editor) return null;
  return <SettingsSection testID="mcp-services" title={catalogView ? undefined : pluginID ? "MCP" : t("mcp.services")} actions={!catalogView && !pluginID ? <button type="button" className="settings-button" onClick={() => setEditor({ name: "" })}>{t("mcp.add")}</button> : undefined}>
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
        if (catalogView && server.connected) actions.push({ label: t("mcp.disconnect"), onSelect: () => act(server.name, "disconnect", () => controls.disconnectMCPServer(server.name)) });
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
          <TruncatedText as="div" className="mcp-service-name" text={name} />
          {hasOtherConnectionSource(server, servers) || !pluginID && !server.editable ? <div className="mcp-service-status" data-mcp-source>{t(server.plugin_id ? "mcp.pluginConnection" : server.editable ? "mcp.manualConnection" : "mcp.projectManaged")}</div> : null}
          <TruncatedText as="div" className="mcp-service-status" data-attention={attention} role={attention || working ? "status" : undefined} text={status}>
            {attention === "auth" ? <AlertTriangle className="icon-sm" aria-hidden="true" /> : attention ? <AlertCircle className="icon-sm" aria-hidden="true" /> : null}
            <span>{status}</span>
          </TruncatedText>
          {diagnostic ? <MCPDiagnostic key={diagnostic} message={diagnostic} /> : null}
        </div>
        <div className="mcp-service-actions">
          {enabled && server.state !== "disabled" && !(catalogView && server.connected) ? <>
            {catalogView && !needsAuth ? <button type="button" className="settings-button settings-button-ghost settings-icon-button" aria-label={t("mcp.connect")} title={t("mcp.connect")} disabled={!!busy} onClick={() => void act(server.name, "connect", () => controls.connectMCPServer(server.name))}><Plus className="icon" aria-hidden="true" /></button> : needsAuth ? <button type="button" className="settings-button" disabled={!!busy} onClick={() => setLogin(server)}>{t("mcp.oauthLogin")}</button>
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

function MCPConnectors({ pluginID, enabled = true, filter = "", catalogView = false }: MCPServicesProps): JSX.Element | null {
  const controls = useSettingsControls();
  const { servers, retainedConnectors, contextKey, catalog, refresh } = useMCPServices();
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
  const loginRef = useRef(login);
  const preparingRef = useRef(preparing);
  loginRef.current = login;
  preparingRef.current = preparing;
  const recoveryToast = useRef<number | undefined>(undefined);
  const claimedScope = useRef<object | undefined>(undefined);
  const loginIntentRevision = useRef(0);
  const inFlight = useRef(false);
  const mounted = useRef(true);
  const currentControls = useRef(controls);
  currentControls.current = controls;
  // A connector that cannot be used in this context is left out rather than
  // explained; retained placeholders only bridge a same-context refresh.
  const visible = (retained ? retainedConnectors : servers).filter(server => server.plugin_id && server.connector && (!pluginID || server.plugin_id === pluginID)
    && (retained || enabled && server.plugin_active !== false && server.configurable && !server.connector.setup_required?.trim())
    && `${server.connector.title} ${server.connector.description || ""} ${server.name}`.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()));

  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    setAuth({}); setMenu(null); setDetails(undefined); setBusy(""); setLogin(null); setPreparing(undefined); inFlight.current = false;
    return () => { confirmation.current?.abort(); dismissRecoveryToast(); };
  }, [controls]);
  useEffect(() => {
    setMenu(previous => previous && !servers.some(server => server.name === previous.name && connectorConnected(server)) ? null : previous);
  }, [servers]);
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
    void Promise.all(servers.filter(server => server.plugin_id && server.connector && server.plugin_active !== false && server.configurable && !server.connector.setup_required?.trim()).map(async server => {
      try { return [server.name, { result: await controls.getMCPAuthStatus(server.name) }] as const; }
      catch (failure) { return [server.name, { error: toastErrorMessage(failure) }] as const; }
    })).then(entries => { if (!cancelled) setAuth(Object.fromEntries(entries)); });
    return () => { cancelled = true; };
  }, [controls, servers]);
  useEffect(() => {
    const recover = () => {
      if (!contextKey || !current() || !enabled || retained) return;
      for (const recovery of connectorRecoveries.values()) {
        if (recovery.context !== contextKey || recovery.owner === scope || recovery.inFlight) continue;
        const server = visible.find(candidate => candidate.name === recovery.name && candidate.plugin_id === recovery.pluginID
          && candidate.resource_url === recovery.resource);
        if (!server) continue;
        recovery.owner = scope;
        reportConnectorFailure(server, "disconnect", true, recovery);
        break;
      }
    };
    connectorRecoveryListeners.add(recover);
    recover();
    return () => { connectorRecoveryListeners.delete(recover); };
  }, [contextKey, controls, scope, servers, enabled, retained, pluginID, filter]);

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
  function dismissRecoveryToast(): void {
    if (recoveryToast.current !== undefined) dismissToast(recoveryToast.current);
    recoveryToast.current = undefined;
  }
  async function confirm(request: Parameters<typeof confirmAction>[0]): Promise<boolean> {
    const owner = new AbortController();
    confirmation.current = owner;
    try { return await confirmAction(request, owner.signal); }
    finally { if (confirmation.current === owner) confirmation.current = null; }
  }
  const failureMessages = {
    connect: "connector.connectFailed", disconnect: "connector.disconnectIncomplete", identity: "connector.identityRetryFailed",
  } as const;
  async function act(name: string, action: keyof typeof failureMessages, operation: () => Promise<void | DisconnectOutcome>): Promise<void> {
    if (inFlight.current || loginRef.current || preparingRef.current) return;
    inFlight.current = true;
    dismissRecoveryToast();
    const pending = contextKey && action === "disconnect" ? connectorRecoveries.get(recoveryKey(contextKey, name)) : undefined;
    if (pending) pending.inFlight = true;
    if (contextKey && action !== "disconnect") {
      const recovery = connectorRecoveries.get(recoveryKey(contextKey, name));
      if (recovery) forgetConnectorRecovery(recovery);
    }
    setBusy(name);
    let failed = false;
    let failure: unknown;
    let outcome: void | DisconnectOutcome = undefined;
    try { outcome = await operation(); }
    catch (error) { failed = true; failure = error; }
    finally {
      if (current()) {
        try { await refresh(); }
        finally { if (current()) { inFlight.current = false; setBusy(""); } }
      }
    }
    if (!failed) {
      if (pending) {
        if (outcome === "completed" || outcome === "cancelled" && current()) forgetConnectorRecovery(pending);
        else { pending.inFlight = false; notifyConnectorRecoveries(); }
      }
      return;
    }
    if (isPluginLifecycleCancellation(failure)) {
      if (pending) { pending.inFlight = false; notifyConnectorRecoveries(); }
      return;
    }
    const server = servers.find(candidate => candidate.name === name);
    if (server && action === "disconnect" && contextKey) {
      const recovery = rememberConnectorRecovery(contextKey, server, scope);
      if (current() && recovery.owner === scope) reportConnectorFailure(server, action, true, recovery);
      return;
    }
    if (!current()) return;
    if (server && (action === "disconnect" || action === "connect" && (server.configuration_enabled || server.authorization_id || auth[name]?.result?.authenticated))) {
      reportConnectorFailure(server, action, true);
    } else {
      showExtensionMutationError(failure, t(failureMessages[action]));
    }
  }
  function reportConnectorFailure(server: MCPServerStatus, action: "connect" | "disconnect" | "login", canClear: boolean, pending?: ConnectorRecovery): void {
    dismissRecoveryToast();
    const message = t(action === "login" ? "connector.signInFailed" : failureMessages[action]);
    const recovery = canClear ? {
      label: t(action === "disconnect" ? "mcp.retry" : "connector.clearAuthorization"),
      onClick: () => {
        if (!current()) return;
        dismissRecoveryToast();
        void act(server.name, "disconnect", () => retryCleanup(server, action !== "disconnect"));
      },
    } : undefined;
    recoveryToast.current = showActionErrorToast(message, "", recovery, pending ? () => forgetConnectorRecovery(pending) : undefined);
  }
  async function retryCleanup(server: MCPServerStatus, clearAuthorization: boolean): Promise<DisconnectOutcome> {
    const list = await controls.listMCPServers();
    if (!current()) return "interrupted";
    const target = list.servers.find(candidate => candidate.name === server.name);
    if (!target?.connector || !target.configurable || target.plugin_active === false
      || target.plugin_id !== server.plugin_id || target.resource_url !== server.resource_url) {
      throw new Error("Connector is no longer available.");
    }
    return disconnect(target, clearAuthorization);
  }
  async function connect(server: MCPServerStatus): Promise<void> {
    // A previous OAuth grant can survive a failed transport connection. Reuse
    // it unless the server explicitly says that authorization is required.
    if (!server.connected && server.configuration_enabled && !["auth_required", "needs_auth", "needs_client_registration"].includes(server.state)) {
      const authorization = await controls.getMCPAuthStatus(server.name);
      if (!current()) return;
      if (authorization.authenticated) {
        await controls.connectMCPServer(server.name);
        return;
      }
    }
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
  async function disconnect(server: MCPServerStatus, clearAuthorization = false): Promise<DisconnectOutcome> {
    if (!await confirm({ title: t(clearAuthorization ? "connector.clearAuthorizationTitle" : "connector.disconnectTitle", { name: server.connector!.title }),
      message: t(clearAuthorization ? "connector.clearAuthorizationHint" : "connector.disconnectHint", { name: server.connector!.title }),
      confirmLabel: t(clearAuthorization ? "connector.clearAuthorization" : "mcp.disconnect"), tone: "danger" })) return current() ? "cancelled" : "interrupted";
    if (!current()) return "interrupted";
    // Cleanup is best-effort: a failed runtime stop must not prevent removing
    // local credentials or recording the user's disabled preference.
    const failures: string[] = [];
    let interrupted = false;
    for (const operation of [
      () => controls.disconnectMCPServer(server.name),
      () => controls.removeMCPAuth(server.name),
      async () => {
        const definition = await controls.readMCPDefinition(server.name);
        if (!current()) return false;
        await controls.configureMCPServers({ servers: { [server.name]: { enabled: false } }, revision: definition.revision });
        return true;
      },
    ]) {
      if (!current()) { interrupted = true; break; }
      try { if (await operation() === false) { interrupted = true; break; } }
      catch (failure) { failures.push(toastErrorMessage(failure)); }
    }
    if (failures.length) {
      console.warn("Connector disconnect was incomplete:", failures.join("\n"));
      throw new Error(t("connector.disconnectIncomplete"));
    }
    return interrupted ? "interrupted" : "completed";
  }

  if (!visible.length && !loginAvailable && !preparing) return null;
  const locked = retained || !!busy || !!login || !!preparing;
  function describe(server: MCPServerStatus) {
    const connector = server.connector!;
    const authorization = retained ? undefined : auth[server.name];
    const authorized = authorization?.result?.authenticated;
    const identity = authorized ? authorization.result?.identity : undefined;
    const account = identity?.state === "verified" ? identity.account : undefined;
    const workspace = account ? identity?.workspace : undefined;
    const available = !retained;
    const connected = connectorConnected(server);
    const working = retained || busy === server.name || preparing === server.name || login?.server.name === server.name;
    // The description says what a service does. Accounts appear only to tell
    // apart connections that share a name.
    const duplicate = visible.some(other => other.name !== server.name && other.connector?.title === connector.title);
    const accountLabel = account ? [`${account.label || account.email || account.id}${account.kind === "bot" ? ` (${t("connector.bot")})` : ""}`, workspace ? workspace.label || workspace.id : ""].filter(Boolean).join(" · ") : "";
    const secondary = working ? t("mcp.working") : duplicate && accountLabel ? accountLabel : connector.description || "";
    const diagnostic = [server.error, authorization?.error].filter(Boolean).join("\n\n");
    return { connector, authorized, account, available, connected, working, secondary, diagnostic };
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
  return <SettingsSection testID="mcp-connectors" title={catalogView ? undefined : t("connector.title")}>
    <SettingsGroup>{visible.map(server => {
      const view = describe(server);
      const items: ThreadContextMenuItem[] = [];
      if (view.available && view.connected) {
        items.push({ label: t("connector.reconnect"), onSelect: () => act(server.name, "connect", () => connect(server)) });
        if (view.authorized && view.connector.identity_probe && !view.account) items.push({ label: t("connector.verifyIdentity"), onSelect: () => verifyIdentity(server) });
        if (view.diagnostic) items.push({ label: t("connector.errorDetails"), onSelect: () => setDetails(server.name) });
        items.push({ separator: true });
        items.push({ label: t("mcp.disconnect"), danger: true, onSelect: () => act(server.name, "disconnect", () => disconnect(server)) });
      }
      return <div className="mcp-service mcp-connector" key={server.name} data-connector={server.name} data-connected={view.connected} data-authorized={view.authorized === undefined ? "unknown" : view.authorized} aria-busy={view.working}>
        <ConnectorMark server={server} catalog={catalog} />
        <div className="mcp-service-copy">
          <TruncatedText as="div" className="mcp-service-name" text={view.connector.title} />
          {hasOtherConnectionSource(server, servers) ? <div className="mcp-service-status" data-mcp-source>{t("mcp.pluginConnection")} · {t(view.connected ? "mcp.connected" : "mcp.notConnected")}</div> : null}
          {view.secondary ? <TruncatedText as="div" className="mcp-service-status" data-connector-identity={view.account && view.secondary !== view.connector.description ? "" : undefined}
            role={view.working ? "status" : undefined} text={view.secondary} /> : null}
        </div>
        <div className="mcp-service-actions">
          {view.available && !view.connected ? <button type="button" className="settings-button settings-button-ghost settings-icon-button" data-connector-action="connect" disabled={locked}
              title={t("mcp.connect")} onClick={() => void act(server.name, "connect", () => connect(server))}>
              <Plus className="icon" aria-hidden="true" /><span className="sr-only">{t("mcp.connect")}</span>
            </button> : null}
          {items.length ? <button type="button" className="settings-button settings-button-ghost settings-icon-button" data-connector-menu disabled={locked}
            aria-label={t("provider.moreActions", { name: view.connector.title })} aria-haspopup="menu" aria-expanded={menu?.name === server.name}
            onClick={event => { const bounds = event.currentTarget.getBoundingClientRect(); setMenu({ name: server.name, x: bounds.right, y: bounds.bottom }); }}>
            <MoreHorizontal className="icon" aria-hidden="true" />
          </button> : null}
          {items.length && menu?.name === server.name ? <ThreadContextMenu x={menu.x} y={menu.y} items={items} onClose={() => setMenu(null)} /> : null}
        </div>
      </div>;
    })}</SettingsGroup>
    {detailServer ? <Modal ariaLabel={t("connector.errorDetails")} title={t("connector.errorDetails")} subtitle={detailServer.connector!.title}
      panelClassName="mcp-connector-dialog" onClose={() => setDetails(undefined)}>
      <pre className="settings-mcp-diagnostic-text" data-connector-diagnostic={detailServer.name}>{describe(detailServer).diagnostic}</pre>
    </Modal> : null}
    {preparing ? (() => {
      const server = visible.find(item => item.name === preparing);
      const cancel = () => { loginIntentRevision.current++; controls.cancelMCPConnectorLogin?.(); setPreparing(undefined); };
      return <ConnectorHandshake server={server} name={server?.connector?.title || preparing} catalog={catalog} onClose={cancel}
        footer={<button type="button" className="settings-button" onClick={cancel}>{t("common.cancel")}</button>}>
        <p role="status">{t("connector.opening")}</p>
      </ConnectorHandshake>;
    })() : null}
    {loginAvailable ? <MCPLogin server={login.server} managed={login.managed} catalog={catalog}
      onClearAuthorization={auth[login.server.name]?.result?.authenticated ? () => {
        if (!current()) return;
        controls.cancelMCPConnectorLogin?.();
        setLogin(null);
        loginRef.current = null;
        void act(login.server.name, "disconnect", () => retryCleanup(login.server, true));
      } : undefined} onClose={failure => {
      if (!current()) return;
      controls.cancelMCPConnectorLogin?.();
      setLogin(null);
      if (failure) reportConnectorFailure(login.server, failure.action === "connect" ? "connect" : "login",
        failure.action === "connect" || !!login.server.authorization_id || !!auth[login.server.name]?.result?.authenticated);
      void refresh();
    }} onConnected={() => { if (current()) { controls.cancelMCPConnectorLogin?.(); setLogin(null); void refresh(); } }} /> : null}
  </SettingsSection>;
}

/** The connector sign-in frame: Wuu and the service, the step, and its actions. */
function ConnectorHandshake({ server, name, catalog, onClose, asForm, onSubmit, footer, children }: {
  server?: MCPServerStatus; name: string; catalog?: ConnectorIconCatalog; onClose(): void;
  asForm?: boolean; onSubmit?(): void; footer: ReactNode; children: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const title = t("connector.connectTitle", { name });
  return <Modal ariaLabel={title} panelClassName="connector-handshake-dialog" onClose={onClose} asForm={asForm} onSubmit={onSubmit} footer={footer}>
    <div className="connector-handshake">
      <div className="connector-handshake-marks" aria-hidden="true">
        <span className="connector-mark" data-branded=""><img className="plugin-icon-asset" src={mascotIcon} alt="" draggable={false} /></span>
        <span className="connector-handshake-dots"><i /><i /><i /></span>
        {server ? <ConnectorMark server={server} catalog={catalog} /> : <span className="connector-mark" />}
      </div>
      <h2>{title}</h2>
      {children}
    </div>
  </Modal>;
}

function MCPLogin({ server, managed = false, catalog, onClose, onConnected, onClearAuthorization }: { server: MCPServerStatus; managed?: boolean; catalog?: ConnectorIconCatalog; onClose(failure?: MCPActionFailure): void; onConnected(): void; onClearAuthorization?(): void }): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const [error, setError] = useState("");
  const [manual, setManual] = useState(false);
  const [ready, setReady] = useState(false);
  const [code, setCode] = useState("");
  const [finishing, setFinishing] = useState(false);
  const [authorized, setAuthorized] = useState(false);
  const [retry, setRetry] = useState(0);
  const attempt = useRef<{ loginID?: string; state?: string; authorized?: boolean; connecting?: boolean; cancelled: boolean }>({ cancelled: false });
  const complete = useRef(onConnected);
  const close = useRef(onClose);
  complete.current = onConnected;
  close.current = onClose;
  function fail(current: typeof attempt.current, failure: unknown): void {
    if (current.cancelled || isPluginLifecycleCancellation(failure)) return;
    const diagnostic = toastErrorMessage(failure);
    if (server.connector) {
      current.cancelled = true;
      console.warn(`Connector sign-in for ${server.name} failed:`, diagnostic);
      close.current({ action: current.authorized ? "connect" : "login", diagnostic });
    } else {
      setError(diagnostic);
    }
  }
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
        if (result.status === "error") {
          if (result.authorized) { current.authorized = true; setAuthorized(true); }
          fail(current, result.error || t("mcp.loginFailed"));
          return;
        }
        setReady(result.phase !== "starting");
        timer = setTimeout(() => { void poll(); }, 750);
      } catch (failure) { fail(current, failure); }
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
      } catch (failure) { fail(current, failure); }
    })();
    return () => { current.cancelled = true; clearTimeout(timer); if (!managed) void cancel().catch(() => {}); };
  }, [controls, server.name, server.oauth_redirect_uri, t, retry, managed]);

  async function finish(): Promise<void> {
    const current = attempt.current;
    if (current.connecting) return;
    current.connecting = true;
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
    } catch (failure) { fail(current, failure); }
    finally { current.connecting = false; if (!current.cancelled) setFinishing(false); }
  }

  const title = t("mcp.loginNamed", { name: server.connector?.title || server.display_name || server.name });
  const dismiss = () => onClose(error ? { action: authorized ? "connect" : "login", diagnostic: error } : undefined);
  const retryLogin = () => {
    if (attempt.current.authorized) { void finish(); return; }
    if (managed) void controls.retryMCPConnectorLogin!().then(() => setRetry(value => value + 1)).catch(failure => {
      if (!attempt.current.cancelled) setError(toastErrorMessage(failure));
    });
    else setRetry(value => value + 1);
  };
  const codeField = manual && !authorized ? <label className="mcp-field">{t("mcp.callbackCode")}<input className="settings-input" value={code} onChange={event => setCode(event.target.value)} autoComplete="off" /></label> : null;
  const finishButton = manual ? <button type="submit" className="settings-button settings-button-primary" disabled={(!authorized && !code.trim()) || finishing}>{t(authorized ? "mcp.connect" : "mcp.finishLogin")}</button> : null;
  if (server.connector) {
    return <ConnectorHandshake server={server} name={server.connector.title} catalog={catalog} onClose={dismiss} asForm onSubmit={() => void finish()} footer={<>
      {onClearAuthorization ? <button type="button" className="settings-button" onClick={onClearAuthorization}>{t("connector.clearAuthorization")}</button> : null}
      <button type="button" className="settings-button" onClick={dismiss}>{t("common.cancel")}</button>
      {finishButton}
    </>}>
      <p role="status">{t(finishing && authorized ? "mcp.connecting" : ready ? manual ? "mcp.manualHint" : "connector.browserWaiting" : "connector.opening")}</p>
      {codeField}
    </ConnectorHandshake>;
  }
  return <Modal ariaLabel={title} title={title} onClose={dismiss} asForm onSubmit={() => void finish()} footer={<>
    <button type="button" className="settings-button" onClick={dismiss}>{t("mcp.cancelLogin")}</button>
    {error && !manual ? <button type="button" className="settings-button settings-button-primary" data-testid="mcp-login-retry" onClick={retryLogin}>{t("mcp.retry")}</button> : null}
    {finishButton}
  </>}>
    <div className="mcp-dialog-body">
      {error ? <MCPFailure message={t(authorized ? "mcp.connectionFailed" : "mcp.loginFailed")} diagnostic={error} /> : <p role="status">{t(finishing && authorized ? "mcp.connecting" : ready ? manual ? "mcp.manualHint" : "mcp.browserWaiting" : "mcp.loginStarting")}</p>}
      {codeField}
    </div>
  </Modal>;
}
