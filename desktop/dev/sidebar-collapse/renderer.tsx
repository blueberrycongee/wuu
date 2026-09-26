// Real fold components with synthetic rows; no product bridge or user data.
import { Folder, FolderOpen } from "../../src/renderer/WuuIcons";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { SidebarCollapseBody, SidebarSection } from "../../src/renderer/SidebarSection";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("size")) || 14);

function Fixture(): JSX.Element {
  const [expanded, setExpanded] = useState(true);
  const [workspaceExpanded, setWorkspaceExpanded] = useState(true);
  const [rows, setRows] = useState(params.has("empty") ? 0 : Number(params.get("rows")) || 12);
  return <div className="app-shell" style={{ display: "flex", height: "100dvh" }}>
    <aside className="sidebar" style={{ width: Number(params.get("width")) || 296, overflow: "auto" }}>
      <section className="sidebar-functional-group" data-testid="group">
        <div className="sidebar-functional-heading">
          <button className="sidebar-functional-heading-toggle" aria-expanded={expanded}
            onClick={() => setExpanded(value => !value)} data-testid="toggle">
            <span className="sidebar-functional-heading-label">{params.get("pinned") === "true" ? "Pinned" : "Workspaces"}</span>
          </button>
        </div>
        <SidebarCollapseBody expanded={expanded} className="sidebar-functional-group-collapse">
          <div className="sidebar-functional-group-body">
            <section className="project-section" data-testid="project">
              <SidebarSection expanded={workspaceExpanded} iconKind="project"
                CollapsedIcon={Folder} ExpandedIcon={FolderOpen} label="Workspace with nested conversations"
                ariaLabel="Toggle workspace" title="Toggle workspace" onToggle={() => setWorkspaceExpanded(value => !value)}>
                <div className="thread-list">
                  {Array.from({ length: rows }, (_, i) => <button key={i} className="thread-row sidebar-session-row"
                    data-testid="row" style={{ textAlign: "left", flexShrink: 0 }}>
                    <span className="thread-row-main"><span className="thread-row-title">
                      Conversation {i + 1}: a long title for the folding sidebar
                    </span></span>
                  </button>)}
                </div>
              </SidebarSection>
            </section>
            {params.get("pinned") === "true" ? <div className="pinned-append-drop-zone" /> : null}
          </div>
        </SidebarCollapseBody>
      </section>
      <section className="sidebar-functional-group" data-testid="following">
        <div className="sidebar-functional-heading">Folders</div>
      </section>
    </aside>
    <main style={{ padding: 24 }}>
      <button data-testid="add" onClick={() => setRows(count => count + 4)}>Add conversations</button>
      <button data-testid="empty" onClick={() => setRows(0)}>Clear conversations</button>
    </main>
  </div>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
