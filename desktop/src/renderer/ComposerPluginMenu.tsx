import { useEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import type { MCPServerStatus, PluginReference } from "../shared/protocol";
import { composerInputElement, type ComposerInputHandle } from "./ComposerInput";
import { draftPluginReferences, pluginReferenceToken } from "./ComposerPluginReferences";
import { useComposerServices } from "./ComposerServicesContext";
import { useI18n } from "./i18n";
import { Puzzle, Settings } from "./WuuIcons";
import { showActionErrorToast } from "./Toast";
import "./ComposerPluginMenu.css";

export function useComposerPluginMenu(inputRef: RefObject<ComposerInputHandle | null>, onChange: (value: string) => void, owner?: string, disabled = false) {
  const services = useComposerServices();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [fromMention, setFromMention] = useState(false);
  const range = useRef<[number, number] | undefined>(undefined);
  const id = useId();
  useEffect(() => { setOpen(false); range.current = undefined; }, [owner, disabled]);
  function observe(value: string): void {
    if (!services || disabled) return;
    const input = inputRef.current;
    const caret = input?.selectionStart ?? value.length;
    const match = /(?:^|\s)@([^\s@]*)$/.exec(value.slice(0, caret));
    if (!match || draftPluginReferences(value).some(ref => caret > ref.start && caret <= ref.end)) {
      if (fromMention) setOpen(false);
      return;
    }
    range.current = [caret - match[1].length - 1, caret];
    setQuery(match[1]); setFromMention(true); setOpen(true);
  }
  function toggle(next: boolean): void {
    if (next) {
      const input = inputRef.current;
      range.current = input ? [input.selectionStart, input.selectionEnd] : undefined;
      setQuery(""); setFromMention(false);
    }
    setOpen(next);
  }
  function choose(text: string, plugin: PluginReference): void {
    const input = inputRef.current;
    if (!input) return;
    const value = input.value;
    const [start, end] = range.current ?? [value.length, value.length];
    const token = pluginReferenceToken(text, plugin) + " ";
    onChange(value.slice(0, start) + token + value.slice(end));
    setOpen(false);
    requestAnimationFrame(() => {
      inputRef.current?.focus({ preventScroll: true });
      inputRef.current?.setSelectionRange(start + token.length, start + token.length);
    });
  }
  function handleKeyDown(event: KeyboardEvent<HTMLElement>): boolean {
    if (!open) return false;
    if (event.key === "Escape") { event.preventDefault(); setOpen(false); return true; }
    if (["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key)) {
      const buttons = document.getElementById(id)?.querySelectorAll<HTMLButtonElement>("[data-plugin-choice]:not(:disabled)");
      const button = event.key === "ArrowUp" ? buttons?.[buttons.length - 1] : buttons?.[0];
      if (button) {
        event.preventDefault();
        if (event.key === "Enter" || event.key === "Tab") button.click(); else button.focus();
        return true;
      }
      if (event.key === "Enter") { event.preventDefault(); return true; }
    }
    return false;
  }
  return { id, available: Boolean(services), open, query, setQuery, fromMention, setOpen: toggle, close: () => setOpen(false), observe, choose, handleKeyDown,
    inputElement: () => inputRef.current ? composerInputElement(inputRef.current) : null };
}
export type ComposerPluginMenu = ReturnType<typeof useComposerPluginMenu>;

export function ComposerPluginChoices({ menu }: { menu: ComposerPluginMenu }): JSX.Element | null {
  const services = useComposerServices();
  const { t } = useI18n();
  const [servers, setServers] = useState<MCPServerStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!services) return;
    let active = true;
    setLoading(true); setError(""); setServers([]);
    void services.settingsControls.listMCPServers().then(result => {
      if (active) { setServers(result.servers); setLoading(false); }
    }, failure => { if (active) { setError(String(failure)); setLoading(false); } });
    return () => { active = false; };
  }, [services?.settingsControls, revision]);
  if (!services) return null;
  const inventory = services.catalog.getSnapshot().inventory;
  const supported = services.conversationPluginsSupported === true;
  const matches = (text: string) => text.toLocaleLowerCase().includes(menu.query.toLocaleLowerCase());
  const entries = [
    ...servers.map(server => {
      const identity = server.identity;
      const account = identity?.state === "verified" ? [identity.account?.label ?? identity.account?.email ?? identity.account?.id, identity.workspace?.label ?? identity.workspace?.id].filter(Boolean).join(" · ") : "";
      const name = server.connector?.title ?? server.display_name ?? server.name;
      const duplicateName = servers.some(other => other.name !== server.name && (other.connector?.title ?? other.display_name ?? other.name) === name);
      const ready = server.connected && server.configuration_enabled !== false && server.state !== "disabled" && server.plugin_active !== false && !!server.tool_names?.length;
      return { key: `mcp:${server.name}`, name, description: server.connector?.description ?? inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id)?.description,
        detail: account ? `${account}${duplicateName ? ` · ${server.name}` : ""}` : (ready && server.auth_status === "oauth" ? `${t("composer.pluginsIdentityUnavailable")}${duplicateName ? ` · ${server.name}` : ""}` : name !== server.name ? server.name : ""),
        reason: server.connector?.setup_required || (server.plugin_active === false ? t("composer.pluginsDisabled") : ""),
        plugin: { plugin_id: server.plugin_id, mcp_server: server.name, resource_url: server.resource_url, authorization_id: server.authorization_id,
          ...(identity?.state === "verified" ? { account_id: identity.account?.id, workspace_id: identity.workspace?.id } : {}) } as PluginReference,
        ready, server };
    }),
    ...inventory.filter(item => item.kind === "plugin" && item.conversation_tools?.length).map(item => ({
      key: `plugin:${item.id}`, name: item.name, description: item.description, detail: item.id,
      reason: item.enabled === false || item.runtime_state !== "active" ? t("composer.pluginsDisabled") : "",
      plugin: { plugin_id: item.provenance.plugin_id }, ready: item.enabled !== false && item.runtime_state === "active", server: undefined,
    })),
  ].filter(item => matches([item.name, item.description, item.detail].filter(Boolean).join(" ")))
    .sort((a, b) => Number(b.ready) - Number(a.ready));
  function manage(pluginId?: string, server?: MCPServerStatus): void {
    menu.close();
    void services!.openPlugins?.(pluginId, server?.connector && !server.connector.setup_required && server.plugin_active !== false ? server.name : undefined).catch(showActionErrorToast);
  }
  return <div className="composer-plugin-choices">
    <input className="composer-plugin-search" aria-label={t("composer.pluginsSearch")} placeholder={t("composer.pluginsSearch")}
      value={menu.query} onChange={event => menu.setQuery(event.target.value)} />
    <div className="composer-plus-menu-section">{t("composer.plugins")}</div>
    {!supported ? <p className="composer-plugin-notice" role="status">{t("composer.pluginsEngineUnsupported")}</p> : null}
    {loading ? <p className="composer-plugin-notice" role="status">{t("composer.pluginsLoading")}</p> : null}
    {error ? <div className="composer-plugin-notice" role="alert">{t("composer.pluginsLoadFailed")} <button type="button" onClick={() => setRevision(value => value + 1)}>{t("settings.errorRetry")}</button></div> : null}
    {!loading && !error && !entries.length ? <p className="composer-plugin-notice">{t("composer.pluginsEmpty")}</p> : null}
    {entries.map(entry => <div className="composer-plugin-row" key={entry.key}>
      <button type="button" role="menuitem" data-plugin-choice data-plugin-key={entry.key} disabled={!supported || !entry.ready}
        title={entry.description} onClick={() => menu.choose(`@${entry.name}`, entry.plugin)}>
        <Puzzle className="icon-lg" aria-hidden="true" />
        <span className="composer-plugin-row-copy"><span>{entry.name}</span>
          {entry.description ? <span className="composer-plugin-detail">{entry.description}</span> : null}
          {entry.detail ? <span className="composer-plugin-detail">{entry.detail}</span> : null}
          {entry.reason ? <span className="composer-plugin-detail">{entry.reason}</span> : null}</span>
      </button>
      {services.openPlugins ? <button type="button" className="composer-plugin-manage" aria-label={t(entry.ready || entry.server?.connector?.setup_required || entry.server?.plugin_active === false ? "composer.pluginsManageNamed" : "composer.pluginsConnectNamed", { name: entry.name })}
        onClick={() => manage(entry.plugin.plugin_id, entry.ready ? undefined : entry.server)}>{entry.ready || entry.server?.connector?.setup_required || entry.server?.plugin_active === false ? <Settings className="icon-md" /> : t("composer.pluginsConnect")}</button> : null}
    </div>)}
    {services.openPlugins ? <button type="button" role="menuitem" onClick={() => manage()}><Puzzle className="icon-lg" />{t("composer.pluginsBrowse")}</button> : null}
  </div>;
}
