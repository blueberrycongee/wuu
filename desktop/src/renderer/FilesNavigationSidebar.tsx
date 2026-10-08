import { useState, type PointerEventHandler } from "react";
import type { DesktopProject } from "../shared/protocol";
import { FolderPlus } from "./WuuIcons";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { WorkspaceFileTree } from "./WorkspaceFiles";
import { useI18n } from "./i18n";
import "./styles/files-navigation.css";

/** Browses registered roots without selecting an agent runtime or starting a conversation. */
export function FilesNavigationSidebar({
  projects, selectedRoot, onSelectRoot, onOpenFile, selectedFilePath, open, hidden = false,
  sidebarCollapsed = false, onToggleSidebar, onPointerEnter, onPointerLeave, onAddRoot, addingRoot = false,
}: {
  hidden?: boolean;
  projects: DesktopProject[];
  selectedRoot?: string;
  onSelectRoot: (path: string) => void;
  onOpenFile: (path: string, cwd: string) => void;
  selectedFilePath?: string;
  open: boolean;
  sidebarCollapsed?: boolean;
  onAddRoot?: () => void;
  addingRoot?: boolean;
  onToggleSidebar?: () => void;
  onPointerEnter?: PointerEventHandler<HTMLElement>;
  onPointerLeave?: PointerEventHandler<HTMLElement>;
}): JSX.Element {
  const { t } = useI18n();
  // Destination switches and sidebar folds preserve the loaded tree and its
  // navigation state. Initial hidden mount must not start background browsing.
  const [opened, setOpened] = useState(open);
  if (open && !opened) setOpened(true);
  const project = projects.find((candidate) => candidate.path === selectedRoot);
  const context = project ? { kind: "project" as const, project_id: project.id, cwd: project.path } : undefined;
  return (
    <aside hidden={hidden} style={hidden ? { display: "none" } : undefined} className="sidebar files-navigation-sidebar" data-wuu-component="files-navigation-sidebar" onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <div className="sidebar-content">
        <div className="traffic-spacer">
          {onToggleSidebar ? (
            <button type="button" className="icon-button side-panel-toggle-button sidebar-toggle-button sidebar-collapse-toggle" data-wuu-component="sidebar-toggle" aria-label={t(sidebarCollapsed ? "app.expandLeftSidebar" : "app.collapseLeftSidebar")} aria-pressed={!sidebarCollapsed} onClick={onToggleSidebar}>
              <SidePanelToggleIcon side="left" open={!sidebarCollapsed} />
            </button>
          ) : null}
        </div>
        <div className="files-navigation-heading">
          <div className="files-navigation-heading-row">
            <h2>{t("workspace.tool.files")}</h2>
            {onAddRoot ? <button type="button" className="icon-button" aria-label={t("sidebar.addWorkspace")} title={t("sidebar.addWorkspace")} disabled={addingRoot} onClick={onAddRoot}><FolderPlus className="icon" /></button> : null}
          </div>
          <select aria-label={t("composer.selectWorkspace")} value={project?.path ?? ""} onChange={(event) => onSelectRoot(event.currentTarget.value)}>
            <option value="" disabled>{t("composer.selectWorkspace")}</option>
            {projects.map((entry) => <option key={entry.id} value={entry.path}>{entry.name}</option>)}
          </select>
          {project ? <span className="files-navigation-root" title={project.path}>{project.path}</span> : null}
        </div>
        <div className="files-navigation-tree">
          <WorkspaceFileTree activeContext={context} open={opened || open} selectedFilePath={selectedFilePath} onOpenFile={(path) => { if (project) onOpenFile(path, project.path); }} />
        </div>
      </div>
    </aside>
  );
}
