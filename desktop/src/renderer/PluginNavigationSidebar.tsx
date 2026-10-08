import { useSyncExternalStore, type PointerEventHandler, type ReactNode } from "react";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { PluginIcon } from "./PublicIcon";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { useI18n } from "./i18n";
import { desktopPluginHost, desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { NavigationPresentation, type NavigationSourceNode } from "./plugins/NavigationPresentation";
import type { PluginHost } from "./plugins/PluginHost";
import type { WorkbenchController } from "./plugins/Workbench";
import { primaryViewNavigation } from "./plugins/PrimaryViewNavigation";

export function PluginNavigationSidebar({
  hidden, catalogActive, hasRuntimeContext, sidebarCollapsed, onToggleSidebar,
  onOpenCatalog, onOpenPlugin, onPointerEnter, onPointerLeave, footer,
  pluginHost = desktopPluginHost, workbenchController = desktopWorkbenchController,
}: {
  pluginHost?: PluginHost;
  workbenchController?: WorkbenchController;
  hidden: boolean;
  catalogActive: boolean;
  hasRuntimeContext: boolean;
  sidebarCollapsed: boolean;
  onToggleSidebar?: () => void;
  onOpenCatalog: () => void;
  onOpenPlugin: (pluginId: string, viewTypeId: string, instanceId?: string) => void;
  onPointerEnter?: PointerEventHandler<HTMLElement>;
  onPointerLeave?: PointerEventHandler<HTMLElement>;
  footer: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const declaredEntries = useSyncExternalStore(
    (listener) => pluginHost.subscribe(listener),
    () => pluginHost.getNavigationEntries(),
    () => pluginHost.getNavigationEntries(),
  );
  const snapshot = useSyncExternalStore(
    workbenchController.subscribe,
    workbenchController.getSnapshot,
    workbenchController.getSnapshot,
  );
  const entries = primaryViewNavigation(declaredEntries, snapshot);
  const nodes: NavigationSourceNode[] = [
    { id: "command:skills", kind: "command", label: t("plugins.manage"), icon: "plugin-blocks", active: catalogActive, disabled: !hasRuntimeContext, onActivate: onOpenCatalog },
    ...entries.map((entry): NavigationSourceNode => ({
      id: `plugin:${entry.pluginId}:${entry.id}`, kind: "command", label: entry.title,
      icon: entry.icon && "name" in entry.icon ? entry.icon.name : "plugin-blocks",
      active: entry.instanceId !== undefined && snapshot.activeViewByRegion.primary === entry.instanceId,
      onActivate: () => onOpenPlugin(entry.pluginId, entry.view, entry.instanceId),
    })),
  ];
  return (
    <div data-wuu-navigation-owner="plugins" hidden={hidden} style={{ display: hidden ? "none" : "contents" }}>
      <NavigationPresentation nodes={nodes} fallback={
        <aside hidden={hidden} className="sidebar plugin-navigation-sidebar" data-wuu-component="plugin-navigation-sidebar" onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
          <div className="sidebar-content">
            <div className="traffic-spacer">
              {onToggleSidebar ? <button type="button" className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle" data-wuu-component="sidebar-toggle" aria-label={t(sidebarCollapsed ? "app.expandLeftSidebar" : "app.collapseLeftSidebar")} aria-pressed={!sidebarCollapsed} onClick={onToggleSidebar}><SidePanelToggleIcon side="left" open={!sidebarCollapsed} /></button> : null}
            </div>
            <nav className="primary-nav" aria-label={t("skills.sectionPlugins")}>
              <button type="button" className="nav-item" data-wuu-component="plugin-catalog-navigation" aria-current={catalogActive ? "page" : undefined} disabled={!hasRuntimeContext} onClick={onOpenCatalog}>
                <PluginBlocksIcon className="icon-lg" /><span>{t("plugins.manage")}</span>
              </button>
              {entries.map((entry) => {
                const active = entry.instanceId !== undefined && snapshot.activeViewByRegion.primary === entry.instanceId;
                return <button key={`${entry.pluginId}:${entry.id}`} type="button" className={`nav-item plugin-navigation-item${active ? " active" : ""}`} data-wuu-component="plugin-navigation-item" data-wuu-plugin={entry.pluginId} aria-current={active ? "page" : undefined} title={entry.description || entry.title} onClick={() => onOpenPlugin(entry.pluginId, entry.view, entry.instanceId)}>
                  <PluginIcon icon={entry.icon} pluginId={entry.pluginId} fingerprint={entry.generation} className="icon-lg" /><span>{entry.title}</span>
                </button>;
              })}
            </nav>
            <div className="sidebar-settings">{footer}</div>
          </div>
        </aside>
      } />
    </div>
  );
}
