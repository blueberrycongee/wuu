import { useMemo } from "react";
import type { ApplicationCatalogV1, CatalogIconDescriptorV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import { PluginIcon } from "./PublicIcon";
import { Plug } from "./WuuIcons";

export function CatalogPluginIcon({ catalog, pluginId, icon, className }: {
  catalog?: ApplicationCatalogV1;
  pluginId?: string;
  icon?: CatalogIconDescriptorV1;
  className?: string;
}): JSX.Element {
  const loadIcon = useMemo(() => (id: string, fingerprint: string, path: string) => catalog!.loadIcon(id, fingerprint, path), [catalog]);
  const record = catalog?.getSnapshot().inventory.find(item => item.kind === "plugin" && item.provenance.plugin_id === pluginId);
  if (!record) return <Plug className={className} aria-hidden="true" />;
  return <PluginIcon className={className} loadIcon={loadIcon} icon={icon ?? record.icon} pluginId={record.id} fingerprint={record.fingerprint ?? ""} />;
}
