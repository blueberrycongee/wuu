// Real fold components with synthetic rows; no product bridge or user data.
import { Folder, FolderOpen } from "../../src/renderer/WuuIcons";
import { useState } from "react";
import { createRoot } from "react-dom/client";
import { SidebarCollapseBody, SidebarSection } from "../../src/renderer/SidebarSection";
import { I18nProvider } from "../../src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { WorkspaceGroup } from "../../src/renderer/ThreadSidebar";
import { summarizeThreadsForSidebar } from "../../src/renderer/AppState";
import type { Thread } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.style.setProperty("--sidebar-open-width", `${Number(params.get("width")) || 296}px`);
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

function ProjectHistoryFixture(): JSX.Element {
  const count = Number(params.get("count") ?? 30);
  const busy = params.get("busy") !== "false";
  const project = { id: "history", name: "Wuu · Session history", path: "/synthetic/history", created_at: "", updated_at: "" };
  const makeThread = (index: number, running = busy && index >= 10 && index < 14): Thread => ({
    id: `thread-${index}`, title: `Conversation ${index + 1}: a long title with descenders gyp`, preview: "",
    cwd: project.path, workspace_kind: "project", model_provider: "synthetic", model: "fixture",
    created_at: "", updated_at: "", status: running ? "in_progress" : "idle",
    forked_from_id: index === 5 ? "thread-4" : undefined,
    turns: [{ id: `turn-${index}`, status: running ? "in_progress" : "completed", items: [], items_view: "full" }],
  });
  const [threads, setThreads] = useState(() => Array.from({ length: count }, (_, index) => makeThread(index)));
  const [viewed, setViewed] = useState<Record<string, string>>(() => Object.fromEntries(
    threads.filter((_, index) => !busy || index < 5 || index >= 10).map(thread => [thread.id, thread.turns[0].id]),
  ));
  const [active, setActive] = useState("thread-0");
  const [expanded, setExpanded] = useState(true);
  const [pending, setPending] = useState(busy ? 2 : 0);
  return <div className="app-shell" style={{ display: "flex", height: "100dvh" }}>
    <aside className="sidebar" style={{ width: Number(params.get("width")) || 296, flexShrink: 0, overflow: "auto" }}>
      <div className="sidebar-content">
        <section className="project-section">
          <WorkspaceGroup project={project} activeID={project.id}
            expandedSidebarSectionIDs={new Set(expanded ? [project.id] : [])}
            threadsByWorkspaceID={{ [project.id]: threads.map(thread => summarizeThreadsForSidebar([thread])[0]) }}
            activeThreadID={active} lastViewedTurnByThreadID={viewed}
            pendingConversations={Array.from({ length: pending }, (_, index) => ({
              id: `pending-${index}`, title: `Creating conversation ${index + 1}`,
              context: { kind: "project", project_id: project.id, cwd: project.path },
            }))}
            scratchPseudoWorkspaceID="scratch" scratchPseudoActive={false}
            onToggleSidebarSectionCollapsed={() => setExpanded(value => !value)}
            onStartNewThread={() => setPending(value => value + 1)}
            onSelectThread={(_, id) => {
              setActive(id);
              setViewed(current => ({ ...current, [id]: threads.find(thread => thread.id === id)!.turns[0].id }));
            }}
            onToggleThreadPinned={thread => setThreads(current => current.filter(item => item.id !== thread.id))}
            onArchiveThread={thread => setThreads(current => current.filter(item => item.id !== thread.id))}
            onDeleteThread={thread => setThreads(current => current.filter(item => item.id !== thread.id))}
          />
        </section>
        <section className="project-section" data-testid="following">
          <SidebarSection expanded={false} iconKind="project" CollapsedIcon={Folder} ExpandedIcon={FolderOpen}
            label="Another workspace" ariaLabel="Another workspace" title="Another workspace" onToggle={() => {}} />
        </section>
      </div>
    </aside>
    <main style={{ padding: 24, minWidth: 0 }}>
      <button data-testid="grow" onClick={() => setThreads(current => [
        ...current, ...Array.from({ length: 20 }, (_, index) => makeThread(current.length + index, true)),
      ])}>Add running conversations</button>
      <button data-testid="font" onClick={() => applyMessageFlowFontSize(20)}>Large text</button>
      <button data-testid="clear" onClick={() => { setThreads([]); setPending(0); }}>Clear conversations</button>
      <button data-testid="pending-only" onClick={() => { setThreads([]); setPending(12); }}>Creating only</button>
      <button data-testid="select-old" onClick={() => setActive("thread-29")}>Select oldest</button>
    </main>
  </div>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider>
  {params.get("mode") === "history" ? <ProjectHistoryFixture /> : <Fixture />}
</I18nProvider>);
