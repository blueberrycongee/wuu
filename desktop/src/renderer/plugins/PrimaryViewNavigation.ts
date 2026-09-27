import type { RegisteredPluginViewEntry } from "./PluginHost";
import type { WorkbenchSnapshot } from "./Workbench";

/** Sidebar destinations include API-opened instances without a declared entry. */
export function primaryViewNavigation(
  entries: readonly RegisteredPluginViewEntry[],
  snapshot: WorkbenchSnapshot,
): readonly (RegisteredPluginViewEntry & { instanceId?: string })[] {
  const views = snapshot.views.filter((view) => view.region === "primary");
  const rows: (RegisteredPluginViewEntry & { instanceId?: string })[] = [];
  const included = new Set<string>();
  for (const entry of entries) {
    const instances = views.filter((view) => view.pluginId === entry.pluginId && view.viewTypeId === entry.view);
    if (instances.length === 0) rows.push(entry);
    for (const view of instances) {
      if (included.has(view.id)) continue;
      included.add(view.id);
      rows.push({ ...entry, id: view.id, instanceId: view.id });
    }
  }
  for (const view of views) {
    if (included.has(view.id)) continue;
    const definition = snapshot.viewTypes.find((item) => item.pluginId === view.pluginId && item.id === view.viewTypeId);
    rows.push({
      id: view.id,
      instanceId: view.id,
      pluginId: view.pluginId,
      generation: view.generation,
      view: view.viewTypeId,
      title: definition?.title ?? view.viewTypeId,
    });
  }
  // Multiple instances of one view must remain individually discoverable.
  return rows.map((row) => {
    const siblings = rows.filter((other) => other.pluginId === row.pluginId && other.view === row.view);
    return siblings.length > 1 ? { ...row, title: `${row.title} · ${siblings.indexOf(row) + 1}` } : row;
  });
}
