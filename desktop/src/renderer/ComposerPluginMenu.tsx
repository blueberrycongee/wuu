import { useEffect, useId, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import type { MCPServerStatus, PluginReference } from "../shared/protocol";
import { composerInputElement, type ComposerInputHandle } from "./ComposerInput";
import { draftPluginReferences, pluginReferenceToken } from "./ComposerPluginReferences";
import { useComposerPluginServices } from "./ComposerServicesContext";
import { useI18n } from "./i18n";
import { Puzzle } from "./WuuIcons";
import { ComposerPluginIcon } from "./ComposerPluginIcon";
import { ScopedToast, showActionErrorToast } from "./Toast";
import "./ComposerPluginMenu.css";

// The plus menu mounts on every open. Keep the MCP server status beside the
// composer so reopening it never swaps a loading row for the real list.
const pluginServers = new WeakMap<Pick<ApplicationSettingsControlsV1, "listMCPServers">, MCPServerStatus[]>();
const pluginServerRequests = new WeakMap<Pick<ApplicationSettingsControlsV1, "listMCPServers">, Promise<MCPServerStatus[]>>();

function loadPluginServers(controls: Pick<ApplicationSettingsControlsV1, "listMCPServers">): Promise<MCPServerStatus[]> {
  const pending = pluginServerRequests.get(controls);
  if (pending) return pending;
  const request = Promise.resolve().then(() => controls.listMCPServers()).then(result => {
    pluginServers.set(controls, result.servers);
    return result.servers;
  }).finally(() => {
    if (pluginServerRequests.get(controls) === request) pluginServerRequests.delete(controls);
  });
  pluginServerRequests.set(controls, request);
  return request;
}

export function useComposerPluginMenu(inputRef: RefObject<ComposerInputHandle | null>, onChange: (value: string) => void, owner?: string, disabled = false) {
  const services = useComposerPluginServices();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [fromMention, setFromMention] = useState(false);
  const range = useRef<[number, number] | undefined>(undefined);
  const id = useId();
  const settingsControls = services?.settingsControls;
  useEffect(() => { setOpen(false); range.current = undefined; }, [owner, disabled]);
  useEffect(() => {
    if (!settingsControls || pluginServers.has(settingsControls)) return;
    // Fetch before the first open so the menu paints its rows together.
    void loadPluginServers(settingsControls).catch(() => undefined);
  }, [settingsControls]);
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
  return { id, available: Boolean(services), open, query, fromMention, setOpen: toggle, close: () => setOpen(false), observe, choose, handleKeyDown,
    inputElement: () => inputRef.current ? composerInputElement(inputRef.current) : null };
}
export type ComposerPluginMenu = ReturnType<typeof useComposerPluginMenu>;

export function ComposerPluginChoices({ menu }: { menu: ComposerPluginMenu }): JSX.Element | null {
  const services = useComposerPluginServices();
  const { t } = useI18n();
  const settingsControls = services?.settingsControls;
  const cachedServers = settingsControls ? pluginServers.get(settingsControls) : undefined;
  const [servers, setServers] = useState<MCPServerStatus[]>(cachedServers ?? []);
  const [loading, setLoading] = useState(!cachedServers);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!settingsControls) return;
    let active = true;
    const cached = pluginServers.get(settingsControls);
    if (cached) { setServers(cached); setLoading(false); }
    else { setServers([]); setLoading(true); }
    setError("");
    // Refresh in the background; a cached list stays visible while it does.
    void loadPluginServers(settingsControls).then(result => {
      if (active) { setServers(result); setLoading(false); }
    }, failure => {
      if (!active) return;
      if (!pluginServers.has(settingsControls)) setError(String(failure));
      setLoading(false);
    });
    return () => { active = false; };
  }, [settingsControls, revision]);
  if (!services) return null;
  const inventory = services.catalog.getSnapshot().inventory;
  const supported = services.conversationPluginsSupported === true;
  const matches = (text: string) => text.toLocaleLowerCase().includes(menu.query.toLocaleLowerCase());
  // Only what can be referenced now is listed; connecting and enabling happen
  // in the plugin catalog, reached from the last row.
  const entries = [
    ...servers.flatMap(server => {
      const name = server.display_name ?? server.name;
      const duplicateName = servers.some(other => other.name !== server.name && (other.display_name ?? other.name) === name);
      const ready = server.connected && server.configuration_enabled !== false && server.state !== "disabled" && server.plugin_active !== false && !!server.tool_names?.length;
      if (!ready) return [];
      return [{ key: `mcp:${server.name}`, name, description: inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id)?.description,
        // Internal server names appear only to tell same-named servers apart.
        detail: duplicateName ? server.name : "",
        plugin: { plugin_id: server.plugin_id, mcp_server: server.name, resource_url: server.resource_url, authorization_id: server.authorization_id } as PluginReference,
        server }];
    }),
    ...(services.includePluginTools === false ? [] : inventory).filter(item => item.kind === "plugin" && item.conversation_tools?.length && item.enabled !== false && item.runtime_state === "active").map(item => ({
      key: `plugin:${item.id}`, name: item.name, description: item.description, detail: "",
      plugin: { plugin_id: item.provenance.plugin_id }, server: undefined,
    })),
  ].filter(item => matches([item.name, item.description, item.detail].filter(Boolean).join(" ")));
  function openPlugins(): void {
    menu.close();
    void services!.openPlugins?.().catch(showActionErrorToast);
  }
  return <div className="composer-plugin-choices">
    {!supported ? <ScopedToast message={t("composer.pluginsEngineUnsupported")} tone="error" dedupeKey="composer-plugins-engine-unsupported" /> : null}
    {loading ? <p className="composer-plugin-notice" role="status">{t("composer.pluginsLoading")}</p> : null}
    {error ? <div className="composer-plugin-notice" role="alert">{t("composer.pluginsLoadFailed")} <button type="button" onClick={() => setRevision(value => value + 1)}>{t("settings.errorRetry")}</button></div> : null}
    {!loading && !error && !entries.length && !menu.query ? <p className="composer-plugin-notice">{t("composer.pluginsEmpty")}</p> : null}
    {entries.map(entry => {
      const secondary = entry.detail || entry.description;
      return <button type="button" role="menuitem" className="composer-plugin-row" key={entry.key} data-plugin-choice data-plugin-key={entry.key}
        disabled={!supported} onClick={() => menu.choose(`@${entry.name}`, entry.plugin)}>
        <ComposerPluginIcon plugin={entry.plugin} server={entry.server} className="composer-plugin-mark" />
        <span className="composer-plugin-name">{entry.name}</span>
        {secondary ? <span className="composer-plugin-detail">{secondary}</span> : null}
      </button>;
    })}
    {services.openPlugins ? <button type="button" role="menuitem" className="composer-plugin-row composer-plugin-browse" onClick={() => openPlugins()}><Puzzle aria-hidden="true" /><span className="composer-plugin-name">{t("composer.pluginsBrowse")}</span></button> : null}
  </div>;
}
