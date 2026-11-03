import { useMemo } from "react";
import type { ApplicationCatalogV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import type { MCPServerStatus } from "../shared/protocol";
import { PluginIcon } from "./PublicIcon";
import { Plug } from "./WuuIcons";

export type ConnectorIconCatalog = Pick<ApplicationCatalogV1, "getSnapshot" | "loadIcon">;

/**
 * A connector's brand image in a neutral tile. Only a plugin-owned connector
 * can show its image, because assets are served from the owning package
 * generation; anything else keeps the generic plug.
 */
export function ConnectorMark({ server, catalog, className = "" }: {
  server: MCPServerStatus;
  catalog?: ConnectorIconCatalog;
  className?: string;
}): JSX.Element {
  const loadIcon = useMemo(() => catalog && ((id: string, fingerprint: string, path: string) => catalog.loadIcon(id, fingerprint, path)), [catalog]);
  const icon = server.connector?.icon;
  const owner = icon && server.plugin_id
    ? catalog?.getSnapshot().inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === server.plugin_id && item.fingerprint)
    : undefined;
  return <span className={`connector-mark ${className}`} data-branded={owner ? "" : undefined} aria-hidden="true">
    {owner && icon && loadIcon
      ? <PluginIcon icon={icon} pluginId={owner.id} fingerprint={owner.fingerprint!} loadIcon={loadIcon} />
      : <Plug className="icon" />}
  </span>;
}
