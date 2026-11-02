import { useEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import type { MCPServerStatus, PluginReference } from "../shared/protocol";
import { composerInputElement, type ComposerInputHandle } from "./ComposerInput";
import { draftPluginReferences, pluginReferenceToken } from "./ComposerPluginReferences";
import { useComposerServices } from "./ComposerServicesContext";
import { useI18n } from "./i18n";
import { CatalogPluginIcon } from "./CatalogPluginIcon";
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
    if (!event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && ["ArrowDown", "ArrowUp", "Enter", "Tab"].includes(event.key)) {
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
  const availableServers = servers.filter(server => server.connected && server.configuration_enabled !== false && server.state !== "disabled"
    && server.plugin_active !== false && !!server.tool_names?.length && !(server.plugin_id && server.connector?.setup_required?.trim()));
  const entries = [
    ...availableServers.map(server => {
      const identity = server.identity;
      const account = identity?.state === "verified" ? [identity.account?.label ?? identity.account?.email ?? identity.account?.id, identity.workspace?.label ?? identity.workspace?.id].filter(Boolean).join(" · ") : "";
      const name = server.connector?.title ?? server.display_name ?? server.name;
      const sameName = availableServers.filter(other => other.name !== server.name && (other.connector?.title ?? other.display_name ?? other.name) === name);
      const duplicateIdentity = sameName.some(other => (other.identity?.state === "verified" ? [other.identity.account?.label ?? other.identity.account?.email ?? other.identity.account?.id, other.identity.workspace?.label ?? other.identity.workspace?.id].filter(Boolean).join(" · ") : "") === account);
      const localName = server.display_name && server.display_name !== name && !sameName.some(other => other.display_name === server.display_name) ? server.display_name : server.name;
      return { key: `mcp:${server.name}`, name, description: server.connector?.description ?? inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id)?.description,
        detail: sameName.length ? (account ? `${account}${duplicateIdentity ? ` · ${localName}` : ""}` : localName) : "",
        account,
        plugin: { plugin_id: server.plugin_id, mcp_server: server.name, resource_url: server.resource_url, authorization_id: server.authorization_id,
          ...(identity?.state === "verified" ? { account_id: identity.account?.id, workspace_id: identity.workspace?.id } : {}) } as PluginReference,
        server };
    }),
    ...inventory.filter(item => item.kind === "plugin" && item.conversation_tools?.length && item.enabled !== false && item.runtime_state === "active").map(item => ({
      key: `plugin:${item.id}`, name: item.name, description: item.description, detail: "", account: "",
      plugin: { plugin_id: item.provenance.plugin_id }, server: undefined,
    })),
  ].filter(item => supported && matches([item.name, item.description, item.account, item.detail, item.server?.name, item.plugin.plugin_id].filter(Boolean).join(" ")));
  return <div className="composer-plugin-choices">
    {!menu.fromMention ? <input className="composer-plugin-search" aria-label={t("composer.pluginsSearch")} placeholder={t("composer.pluginsSearch")}
      value={menu.query} onChange={event => menu.setQuery(event.target.value)} /> : null}
    <div className="composer-plus-menu-section">{t("composer.plugins")}</div>
    {!supported ? <p className="composer-plugin-notice" role="status">{t("composer.pluginsEngineUnsupported")}</p> : null}
    {supported && loading ? <p className="composer-plugin-notice" role="status">{t("composer.pluginsLoading")}</p> : null}
    {supported && error ? <div className="composer-plugin-notice" role="alert">{t("composer.pluginsLoadFailed")} <button type="button" onClick={() => setRevision(value => value + 1)}>{t("settings.errorRetry")}</button></div> : null}
    {supported && !loading && !error && !entries.length ? <p className="composer-plugin-notice">{t(menu.query ? "composer.pluginsEmpty" : "composer.pluginsNone")}</p> : null}
    {entries.map(entry => <div className="composer-plugin-row" key={entry.key}>
      <button type="button" role="menuitem" data-plugin-choice data-plugin-key={entry.key}
        title={[entry.name, entry.detail || entry.account, entry.description].filter(Boolean).join(" · ")} onClick={() => menu.choose(`@${entry.name}`, entry.plugin)}>
        <span className="composer-plugin-mark" aria-hidden="true"><CatalogPluginIcon catalog={services.catalog} pluginId={entry.plugin.plugin_id} icon={entry.server?.connector?.icon} /></span>
        <span className="composer-plugin-name">{entry.name}</span>
        {entry.detail ? <span className="composer-plugin-detail">{entry.detail}</span> : null}
      </button>
    </div>)}
  </div>;
}
