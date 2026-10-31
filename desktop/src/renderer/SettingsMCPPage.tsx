import { useEffect, useRef, useState } from "react";
import { AlertCircle, AlertTriangle, Check, KeyRound, Loader2, LogOut, Plug, PlugZap, RefreshCw, X } from "./WuuIcons";
import type { InitializeResult, MCPAuthStartResult, MCPServerStatus, RuntimeGeneralSettingsUpdate } from "../shared/protocol";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useSettingsControls } from "./SettingsControlsContext";
import { MCPDiagnostic } from "./MCPDiagnostic";
import { SettingsRow } from "./SettingsRow";
import { SettingsGroup, SettingsPageHeader, SettingsSection, type SettingsStatusTone } from "./SettingsSection";
import { toastErrorMessage } from "./Toast";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

type Translate = ReturnType<typeof useI18n>["t"];

type MCPActionFailure = {
  action: "connect" | "disconnect" | "refresh" | "login" | "logout";
  message: string;
};

export function SettingsMCPPage({ initialized, running, onGeneralSave, onStatusChange }: {
  initialized: InitializeResult | undefined;
  running: boolean;
  onGeneralSave: (settings: RuntimeGeneralSettingsUpdate) => Promise<void>;
  onStatusChange: (status: { busy: boolean; hasError: boolean }) => void;
}): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const configuredMCPEnabled = initialized?.general_settings?.mcp_server_enabled ?? {};
  const configuredMCPKey = stableBoolRecordSignature(configuredMCPEnabled);
  const [mcpEnabledDraft, setMCPEnabledDraft] = useState<Record<string, boolean>>(() => ({ ...configuredMCPEnabled }));
  const [mcpToggleBusy, setMCPToggleBusy] = useState("");
  const [mcpToggleErrors, setMCPToggleErrors] = useState<Record<string, string>>({});
  const [mcpAuthStates, setMCPAuthStates] = useState<Record<string, string>>({});
  const [mcpAuthCodes, setMCPAuthCodes] = useState<Record<string, string>>({});

  useEffect(() => {
    setMCPEnabledDraft({ ...configuredMCPEnabled });
  }, [configuredMCPKey]);

  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const [mcpServers, setMCPServers] = useState<MCPServerStatus[]>([]);
  const [mcpLoading, setMCPLoading] = useState(false);
  const [mcpError, setMCPError] = useState("");
  const [mcpActionErrors, setMCPActionErrors] = useState<Record<string, MCPActionFailure>>({});
  const [mcpListRevision, setMCPListRevision] = useState(0);
  const [mcpBusyServer, setMCPBusyServer] = useState("");
  const currentMCPControls = useRef(controls);
  currentMCPControls.current = controls;
  useEffect(() => {
    setMCPActionErrors({});
    setMCPBusyServer("");
    setMCPServers([]);
    setMCPAuthStates({});
    setMCPAuthCodes({});
    setMCPToggleErrors({});
    setMCPToggleBusy("");
  }, [controls]);

  useEffect(() => {
    let cancelled = false;
    setMCPLoading(true);
    setMCPError("");
    void controls
      .listMCPServers()
      .then((result) => {
        if (!cancelled) {
          setMCPServers(result.servers ?? []);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled && !isPluginLifecycleCancellation(err)) {
          setMCPError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setMCPLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [controls, mcpListRevision]);

  async function runMCPAction(name: string, action: "connect" | "disconnect" | "refresh"): Promise<void> {
    setMCPBusyServer(name);
    setMCPActionErrors((errors) => withoutRecordKey(errors, name));
    try {
      const result =
        action === "connect"
          ? await controls.connectMCPServer(name)
          : action === "disconnect"
            ? await controls.disconnectMCPServer(name)
            : await controls.refreshMCPServer(name);
      if (mcpActionCurrent()) setMCPServers((servers) => upsertMCPServerStatus(servers, result.status));
    } catch (err) {
      await recordMCPActionFailure(name, action, err);
    } finally {
      if (mcpActionCurrent()) setMCPBusyServer("");
    }
  }

  function mcpActionCurrent(): boolean {
    return mounted.current && currentMCPControls.current === controls;
  }

  async function recordMCPActionFailure(name: string, action: MCPActionFailure["action"], error: unknown): Promise<void> {
    if (!mcpActionCurrent() || isPluginLifecycleCancellation(error)) return;
    setMCPActionErrors((errors) => ({ ...errors, [name]: { action, message: error instanceof Error ? error.message : String(error) } }));
    // Failed actions reject over IPC, but the manager retains the classified
    // state. Read it through the same public control rather than parsing logs.
    try {
      const result = await controls.listMCPServers();
      if (mcpActionCurrent()) {
        const status = result.servers.find((server) => server.name === name);
        if (status) setMCPServers((servers) => upsertMCPServerStatus(servers, status));
        setMCPError("");
      }
    } catch (statusError) {
      if (!isPluginLifecycleCancellation(statusError)) console.error("Failed to read MCP status after an action failure", statusError);
    }
  }

  async function startMCPAuth(name: string): Promise<MCPAuthStartResult | undefined> {
    setMCPBusyServer(name);
    setMCPActionErrors((errors) => withoutRecordKey(errors, name));
    try {
      const result = await controls.startMCPAuth(name);
      if (!mcpActionCurrent()) return undefined;
      await controls.openExternal(result.authorization_url);
      if (!mcpActionCurrent()) return undefined;
      return result;
    } catch (err) {
      await recordMCPActionFailure(name, "login", err);
      return undefined;
    } finally {
      if (mcpActionCurrent()) setMCPBusyServer("");
    }
  }

  async function finishMCPAuth(name: string, state: string, code: string): Promise<boolean> {
    setMCPBusyServer(name);
    setMCPActionErrors((errors) => withoutRecordKey(errors, name));
    try {
      const result = await controls.finishMCPAuth(name, state, code);
      if (!mcpActionCurrent()) return false;
      setMCPServers((servers) => upsertMCPServerStatus(servers, result.server));
      return true;
    } catch (err) {
      await recordMCPActionFailure(name, "login", err);
      return false;
    } finally {
      if (mcpActionCurrent()) setMCPBusyServer("");
    }
  }

  async function removeMCPAuth(name: string): Promise<void> {
    setMCPBusyServer(name);
    setMCPActionErrors((errors) => withoutRecordKey(errors, name));
    try {
      const result = await controls.removeMCPAuth(name);
      if (mcpActionCurrent()) setMCPServers((servers) => upsertMCPServerStatus(servers, result.server));
    } catch (err) {
      await recordMCPActionFailure(name, "logout", err);
    } finally {
      if (mcpActionCurrent()) setMCPBusyServer("");
    }
  }

  const busy = mcpLoading || Boolean(mcpBusyServer);
  const hasError = Boolean(mcpError || Object.keys(mcpActionErrors).length || mcpServers.some((server) => server.error));
  useEffect(() => { onStatusChange({ busy, hasError }); }, [busy, hasError, onStatusChange]);
  useEffect(() => () => onStatusChange({ busy: false, hasError: false }), [onStatusChange]);
  async function toggleMCPServer(name: string, enabled: boolean): Promise<void> {
    const previous = mcpEnabledDraft;
    const next = { ...previous, [name]: enabled };
    setMCPEnabledDraft(next);
    setMCPToggleBusy(name);
    setMCPToggleErrors((errors) => withoutRecordKey(errors, name));
    try {
      await onGeneralSave({ mcp_enabled_toggles: next });
    } catch (toggleError) {
      if (!mcpActionCurrent() || isPluginLifecycleCancellation(toggleError)) return;
      setMCPEnabledDraft(previous);
      setMCPToggleErrors((errors) => ({ ...errors, [name]: toastErrorMessage(toggleError, t("settings.saveFailed")) }));
    } finally {
      if (mcpActionCurrent()) setMCPToggleBusy("");
    }
  }

  async function beginMCPAuth(name: string): Promise<void> {
    const result = await startMCPAuth(name);
    if (!result || !mcpActionCurrent()) {
      return;
    }
    setMCPAuthStates((states) => ({ ...states, [name]: result.state }));
    setMCPAuthCodes((codes) => ({ ...codes, [name]: "" }));
  }

  async function completeMCPAuth(name: string): Promise<void> {
    const state = mcpAuthStates[name]?.trim() ?? "";
    const code = mcpAuthCodes[name]?.trim() ?? "";
    if (!state || !code) {
      return;
    }
    if (await finishMCPAuth(name, state, code) && mcpActionCurrent()) {
      setMCPAuthStates((states) => withoutRecordKey(states, name));
      setMCPAuthCodes((codes) => withoutRecordKey(codes, name));
    }
  }

  const mcpServerByName = new Map(mcpServers.map((server) => [server.name, server]));
  const mcpRowNames = Array.from(
    new Set([...mcpServers.map((server) => server.name), ...Object.keys(mcpEnabledDraft), ...Object.keys(mcpActionErrors)]),
  ).sort((a, b) => a.localeCompare(b));

  // Servers are defined only in the user configuration; the page says where,
  // since it cannot add one itself.
  const configLocation = { file: "~/.wuu/config.json", key: "mcp_servers" } as const;
  const description = t("settings.mcpDescription").split(/(\{\w+\})/).map((part, index) => {
    const name = /^\{(\w+)\}$/.exec(part)?.[1] as keyof typeof configLocation | undefined;
    return name && name in configLocation ? <code key={index}>{configLocation[name]}</code> : part;
  });

  return (
    <>
      <SettingsPageHeader title={t("settings.mcpServers")} description={description} />
      <SettingsSection testID="settings-mcp">
        {mcpLoading || mcpRowNames.length > 0 || !mcpError ? <SettingsGroup>
          {mcpLoading && mcpRowNames.length === 0 ? (
            <p className="settings-group-empty">{t("settings.loading")}</p>
          ) : mcpRowNames.length > 0 ? (
            mcpRowNames.map((name) => {
              const server = mcpServerByName.get(name);
              const busy = mcpBusyServer === name || mcpToggleBusy === name;
              const controlsBusy = Boolean(mcpBusyServer || mcpToggleBusy);
              const connected = server ? server.connected || server.state === "connected" || server.state === "ready" : false;
              const disabledByConfig = server?.state === "disabled";
              const enabled = mcpEnabledDraft[name] ?? !disabledByConfig;
              const oauthPending = Boolean(mcpAuthStates[name]);
              const oauthCode = mcpAuthCodes[name] ?? "";
              const failure = mcpActionErrors[name];
              const working = busy && !failure && !mcpToggleErrors[name];
              const needsAuth = server?.state === "auth_required" || server?.state === "needs_auth" || server?.state === "needs_client_registration";
              const diagnostic = working ? "" : mcpToggleErrors[name] || failure?.message || (!oauthPending ? server?.error : "") || "";
              const state = working ? "starting" : needsAuth ? server!.state : diagnostic ? "error" : server?.state ?? "stopped";
              const status = working ? t("mcp.working")
                : mcpToggleErrors[name] ? t("mcp.saveFailed")
                : failure?.action === "login" ? t("mcp.loginFailed")
                : failure?.action === "logout" ? t("mcp.logoutFailed")
                : failure?.action === "disconnect" ? t("mcp.disconnectFailed")
                : failure?.action === "refresh" ? t("mcp.refreshFailed")
                : oauthPending ? t("mcp.enterAuthCode")
                : needsAuth ? t("mcp.loginToConnect")
                : diagnostic || mcpStateTone(state) === "danger" ? t("mcp.connectionFailed")
                : server ? formatMCPServerMeta(server, t) : "";
              return (
                <SettingsRow
                  key={name}
                  title={name}
                  description={status ? (
                    <span className="settings-mcp-status" role={diagnostic || needsAuth || busy ? "status" : undefined}>
                      <MCPStateMark state={state} label={failure || mcpToggleErrors[name] || busy ? status : undefined} />
                      <span>{status}</span>
                    </span>
                  ) : undefined}
                  detail={diagnostic ? <MCPDiagnostic key={diagnostic} message={diagnostic} /> : undefined}
                >
                  {server ? (
                    oauthPending ? (
                      <>
                        <input
                          className="settings-input settings-mcp-code-input"
                          aria-label={t("mcp.authCodeNamed", { name })}
                          autoComplete="off"
                          placeholder={t("mcp.authCode")}
                          value={oauthCode}
                          disabled={controlsBusy}
                          onChange={(event) => {
                            const value = event.currentTarget.value;
                            setMCPAuthCodes((codes) => ({ ...codes, [name]: value }));
                          }}
                        />
                        <Tooltip asChild content={t("mcp.finishLogin")}><button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          aria-label={t("mcp.finishLoginNamed", { name })}
                          disabled={controlsBusy || oauthCode.trim() === ""}
                          onClick={() => void completeMCPAuth(name)}
                        >
                          <Check className="icon" aria-hidden="true" />
                        </button></Tooltip>
                        <Tooltip asChild content={t("mcp.cancelLogin")}><button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          aria-label={t("mcp.cancelLoginNamed", { name })}
                          disabled={controlsBusy}
                          onClick={() => {
                            setMCPAuthStates((states) => withoutRecordKey(states, name));
                            setMCPAuthCodes((codes) => withoutRecordKey(codes, name));
                          }}
                        >
                          <X className="icon" aria-hidden="true" />
                        </button></Tooltip>
                      </>
                    ) : (
                      <div className="settings-row-actions">
                        {server.auth_status === "not_logged_in" || needsAuth ? (
                          <button
                            className="settings-button settings-button-ghost"
                            type="button"
                            aria-label={t("mcp.loginNamed", { name })}
                            disabled={controlsBusy || disabledByConfig}
                            onClick={() => void beginMCPAuth(name)}
                          >
                            <KeyRound className="icon" aria-hidden="true" />
                            {t("mcp.login")}
                          </button>
                        ) : server.auth_status === "oauth" ? (
                          <Tooltip asChild content={t("mcp.removeLogin")}><button
                            className="settings-button settings-button-ghost settings-icon-button"
                            type="button"
                            aria-label={t("mcp.removeLoginNamed", { name })}
                            disabled={controlsBusy}
                            onClick={() => void removeMCPAuth(name)}
                          >
                            <LogOut className="icon" aria-hidden="true" />
                          </button></Tooltip>
                        ) : null}
                        <Tooltip asChild content={t("mcp.refresh")}><button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          aria-label={t("mcp.refreshNamed", { name })}
                          disabled={controlsBusy || disabledByConfig}
                          onClick={() => void runMCPAction(name, "refresh")}
                        >
                          <RefreshCw className="icon" aria-hidden="true" />
                        </button></Tooltip>
                        <Tooltip asChild content={disabledByConfig ? t("mcp.disabledByConfig") : connected ? t("mcp.disconnect") : t("mcp.connect")}><button
                          className="settings-button settings-button-ghost settings-icon-button"
                          type="button"
                          aria-label={`${connected ? t("mcp.disconnect") : t("mcp.connect")} ${name}`}
                          disabled={controlsBusy || disabledByConfig}
                          onClick={() => void runMCPAction(name, connected ? "disconnect" : "connect")}
                        >
                          {connected ? <PlugZap className="icon" aria-hidden="true" /> : <Plug className="icon" aria-hidden="true" />}
                        </button></Tooltip>
                      </div>
                    )
                  ) : null}
                  <button
                    className="settings-switch"
                    type="button"
                    role="switch"
                    aria-checked={enabled}
                    data-testid={`settings-mcp-enabled-${name}`}
                    disabled={running || !initialized || controlsBusy}
                    onClick={() => void toggleMCPServer(name, !enabled)}
                  >
                    <span className="settings-switch-thumb" aria-hidden="true" />
                    <span className="sr-only">{enabled ? t("mcp.disableNamed", { name }) : t("mcp.enableNamed", { name })}</span>
                  </button>
                </SettingsRow>
              );
            })
          ) : (
            <p className="settings-group-empty">{t("settings.noMcpServers")}</p>
          )}
        </SettingsGroup> : null}
        {mcpError ? (
          <div className="settings-mcp-list-failure">
            <p className="settings-section-note" role="status">{t("mcp.listFailed")}</p>
            <button className="settings-button settings-button-ghost" type="button" data-testid="mcp-list-retry" disabled={mcpLoading} onClick={() => setMCPListRevision((revision) => revision + 1)}>
              <RefreshCw className="icon" aria-hidden="true" />{t("mcp.refresh")}
            </button>
            <MCPDiagnostic key={mcpError} message={mcpError} />
          </div>
        ) : null}
      </SettingsSection>
    </>
  );
}

function stableBoolRecordSignature(record: Record<string, boolean>): string {
  return Object.keys(record)
    .sort((a, b) => a.localeCompare(b))
    .map((key) => `${key}:${record[key] ? "1" : "0"}`)
    .join("|");
}

function upsertMCPServerStatus(servers: MCPServerStatus[], status: MCPServerStatus): MCPServerStatus[] {
  const next = [...servers];
  const index = next.findIndex((item) => item.name === status.name);
  if (index >= 0) {
    next[index] = status;
  } else {
    next.push(status);
  }
  next.sort((a, b) => a.name.localeCompare(b.name));
  return next;
}

function withoutRecordKey<T>(values: Record<string, T>, key: string): Record<string, T> {
  const next = { ...values };
  delete next[key];
  return next;
}

function formatMCPServerMeta(server: MCPServerStatus, t: Translate): string {
  const pieces = [t("mcp.toolCount", { count: server.tool_count ?? 0 })];
  if (server.auth_status && server.auth_status !== "unsupported") {
    pieces.push(mcpAuthLabel(server.auth_status, t));
  }
  return pieces.join(" · ");
}

// A connected or idle server shows only its tools; the connect control and
// the switch already say whether it runs. Work in progress and states that
// need a look get one mark named by the state.
function MCPStateMark({ state, label }: { state: string; label?: string }): JSX.Element | null {
  const { t } = useI18n();
  const tone = mcpStateTone(state);
  const busy = state === "starting" || state === "connecting" || state === "reconnecting";
  if (!busy && tone !== "danger" && tone !== "warning") return null;
  const statusLabel = label ?? mcpStateLabel(state, t);
  return (
    <Tooltip asChild content={statusLabel}><span className="settings-row-attention" data-tone={busy ? "neutral" : tone} role="img" aria-label={statusLabel}>
      {busy ? <Loader2 className="icon-sm settings-spin" aria-hidden="true" />
        : tone === "danger" ? <AlertCircle className="icon-sm" aria-hidden="true" />
        : <AlertTriangle className="icon-sm" aria-hidden="true" />}
    </span></Tooltip>
  );
}

function mcpStateLabel(state: string, t: Translate): string {
  switch (state) {
    case "ready":
    case "connected":
      return t("mcp.connected");
    case "starting":
    case "connecting":
      return t("mcp.connecting");
    case "error":
    case "failed":
      return t("mcp.failed");
    case "disabled":
      return t("mcp.disconnected");
    case "auth_required":
    case "needs_auth":
      return t("mcp.needsAuth");
    case "needs_client_registration":
      return t("mcp.needsRegistration");
    case "reconnecting":
      return t("mcp.reconnecting");
    case "stopped":
    case "configured":
      return t("mcp.configured");
    default:
      return state || t("mcp.unknown");
  }
}

function mcpStateTone(state: string): SettingsStatusTone {
  switch (state) {
    case "ready":
    case "connected":
      return "success";
    case "error":
    case "failed":
      return "danger";
    case "auth_required":
    case "needs_auth":
    case "needs_client_registration":
      return "warning";
    default:
      return "neutral";
  }
}

function mcpAuthLabel(status: string, t: Translate): string {
  switch (status) {
    case "bearer_token":
      return t("mcp.headerAuth");
    case "not_logged_in":
      return t("mcp.notLoggedIn");
    case "oauth":
      return "OAuth";
    default:
      return status;
  }
}

