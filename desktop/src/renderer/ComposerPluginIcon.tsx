import { useMemo } from "react";
import type { MCPServerStatus, PluginReference } from "../shared/protocol";
import { useComposerPluginServices } from "./ComposerServicesContext";
import { PluginIcon, PublicIcon } from "./PublicIcon";

/** Menu choices and draft references resolve assets from the same owning package. */
export function ComposerPluginIcon({ plugin, server, className }: {
  plugin: PluginReference;
  server?: MCPServerStatus;
  className: string;
}): JSX.Element {
  const catalog = useComposerPluginServices()?.catalog;
  const loadIcon = useMemo(() => catalog && ((id: string, fingerprint: string, path: string) => catalog.loadIcon(id, fingerprint, path)), [catalog]);
  const owner = plugin.plugin_id
    ? catalog?.getSnapshot().inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === plugin.plugin_id)
    : undefined;
  const connector = server?.name === plugin.mcp_server && server?.plugin_id === plugin.plugin_id ? server?.connector : undefined;
  return <span className={className} aria-hidden="true">
    {owner
      ? <PluginIcon icon={connector?.icon ?? owner.icon} pluginId={owner.id} fingerprint={owner.fingerprint ?? ""} loadIcon={loadIcon} />
      : <PublicIcon name={plugin.mcp_server ? "plug" : undefined} />}
  </span>;
}
