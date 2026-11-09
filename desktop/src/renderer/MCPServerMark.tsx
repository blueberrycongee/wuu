import { useMemo } from "react";
import type { ApplicationCatalogV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import type { MCPServerStatus } from "../shared/protocol";
import { PluginIcon } from "./PublicIcon";
import { Plug } from "./WuuIcons";

export type MCPIconCatalog = Pick<ApplicationCatalogV1, "getSnapshot" | "loadIcon">;

/**
 * A plugin-owned server shows its plugin icon, served from the owning package
 * generation; anything else keeps the generic plug.
 */
export function MCPServerMark({ server, catalog, className = "" }: {
  server: MCPServerStatus;
  catalog?: MCPIconCatalog;
  className?: string;
}): JSX.Element {
  const loadIcon = useMemo(() => catalog && ((id: string, fingerprint: string, path: string) => catalog.loadIcon(id, fingerprint, path)), [catalog]);
  const owner = server.plugin_id
    ? catalog?.getSnapshot().inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id && item.fingerprint)
    : undefined;
  const icon = owner?.icon;
  return <span className={`mcp-server-mark ${className}`} data-branded={owner && icon ? "" : undefined} aria-hidden="true">
    {owner && icon && loadIcon
      ? <PluginIcon icon={icon} pluginId={owner.id} fingerprint={owner.fingerprint!} loadIcon={loadIcon} />
      : <Plug className="icon" />}
  </span>;
}
