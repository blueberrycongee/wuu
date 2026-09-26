// Isolated Projects preview: production sidebar, conversation views, title
// controls and right-panel project views over a mocked window.wuu. No
// preload, credentials, or real workspace.
// From desktop: ./node_modules/.bin/vite --config dev/projects/vite.config.ts,
// then open http://127.0.0.1:5243/dev/projects/. Query parameters:
// view=coordinator|session|draft, panel=project|proposal|none, theme=dark,
// size=20, empty (no projects yet), publisher=0 (hides Open PR).
// capture.cjs writes screenshots to artifacts/projects.
import { createRef, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { createRoot } from "react-dom/client";
import type { ProjectCandidate, ProjectCandidateParams, Thread, Turn } from "../../src/shared/protocol";
import { AppSidebar } from "../../src/renderer/AppSidebar";
import { initialState, summarizeThreadsForSidebar, type AppState } from "../../src/renderer/AppState";
import { ConversationTitleActions } from "../../src/renderer/ConversationShellRenderers";
import { EmptyConversationHome } from "../../src/renderer/LoadingViews";
import { ProjectActionsProvider, type ProjectActions } from "../../src/renderer/ProjectActions";
import { ProjectStatusStrip, useTurnProposals } from "../../src/renderer/ProjectViews";
import { TurnView } from "../../src/renderer/TurnView";
import { WorkspaceRightPanel } from "../../src/renderer/WorkspacePanels";
import { workspaceProjectViewTab, workspaceProposalViewTab, type WorkspaceViewTab } from "../../src/renderer/WorkspaceViewTabs";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { desktopPluginHost } from "../../src/renderer/plugins/DesktopPluginRuntime";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { CLEAR_UNREAD_HINT_SEEN_KEY } from "../../src/renderer/SidebarBrand";
import { ArrowUp, Plus } from "../../src/renderer/WuuIcons";
import "../../src/renderer/styles.css";
import "./fixture.css";

const noop = () => {};
localStorage.setItem(CLEAR_UNREAD_HINT_SEEN_KEY, "true");
const params = new URLSearchParams(location.search);
const date = "2026-09-25T08:00:00Z";
const workspace = { id: "preview", name: "wuu", path: "/preview", created_at: date, updated_at: date };
const control = { manager_id: "project", manager_name: "Search overhaul", revision: 3 };

function thread(id: string, title: string, overrides: Partial<Thread> = {}): Thread {
  return {
    id, title, preview: title, cwd: "/preview", workspace_id: workspace.id, workspace_kind: "project",
    model_provider: "preview", model: "preview", status: "idle", created_at: date, updated_at: date,
    latest_completed_turn_id: `${id}-turn`, turns: [{ id: `${id}-turn`, status: "completed", items: [], items_view: "full" }],
    ...overrides,
  };
}

const projectEvent = (id: string, cause: string, session: string, name: string, text: string) => ({
  id, type: "user_message" as const, status: "completed" as const, origin: "plugin", cause,
  presentation_kind: "session_message", related_session_id: session, name, read_only: true, text,
});

const coordinatorTurn: Turn = { id: "project-turn", status: "completed", duration_ms: 12000, items_view: "full", items: [
  { id: "goal", type: "user_message", status: "completed", text: "把目录搜索改成服务端分页，每页 50 条。保持公开 API 不变。" },
  { id: "plan", type: "agent_message", status: "completed", text: "我会拆成两块：重建搜索索引、给结果分页。两个会话各自在 worktree 里工作，完成后我会告诉你哪些可以审阅。" },
  projectEvent("result", "project_result", "paginate", "Paginate search results",
    "Session \"Paginate search results\" finished a turn: completed.\n\nAdded cursor pagination with a page size of 50 and tests.\n\nProposal awaiting the user's review (every change of the session not yet delivered): internal/search/paginate.go, internal/search/paginate_test.go, desktop/src/renderer/SearchResults.tsx"),
  { id: "ready", type: "agent_message", terminal: true, status: "completed", text: "分页已完成，测试通过，可以审阅「Paginate search results」的改动。索引重建还在运行。" },
  projectEvent("peer", "project_message", "index", "Rebuild the search index", "分页接口已确认，索引计数使用过滤后的总行数。"),
  projectEvent("adopted", "project_adopted", "latency", "Measure search latency",
    "The user added the conversation \"Measure search latency\" to this project."),
] };

const sessionTurn: Turn = { id: "paginate-turn", status: "completed", duration_ms: 64000, items_view: "full", items: [
  { id: "brief", type: "user_message", status: "completed", origin: "plugin", cause: "project", name: "Search overhaul", read_only: true,
    text: "Implement cursor pagination for catalog search with a page size of 50. Keep the public API unchanged. Add tests." },
  { id: "report", type: "agent_message", terminal: true, status: "completed",
    text: "Added cursor pagination with a page size of 50 in `internal/search/paginate.go`, kept `Search()` unchanged, and covered the boundaries in `paginate_test.go`." },
] };

const empty = params.has("empty");
const threads: Thread[] = [
  ...(empty ? [] : [
    thread("project", "Search overhaul", { source: "project", permission_mode: "standard", pending_candidates: 1, turns: [coordinatorTurn], latest_completed_turn_id: "project-turn" }),
    thread("index", "Rebuild the search index", {
      source: "project-session", project_id: "project", project_role: "side", status: "in_progress", session_control: { ...control, state: params.get("side-control") === "taken_over" ? "taken_over" : "active" },
    }),
    thread("paginate", "Paginate search results", {
      source: "project-session", project_id: "project", pending_candidates: 1, turns: [sessionTurn], latest_completed_turn_id: "paginate-turn",
      session_control: { ...control, state: "active" },
    }),
    thread("latency", "Measure search latency", { source: "project-session", project_id: "project", session_control: { ...control, state: "taken_over" } }),
    thread("release", "Release checklist", { source: "project", permission_mode: "standard", created_at: "2026-09-24T08:00:00Z" }),
  ]),
  thread("chat", "Explain the release checklist"),
  thread("chat-2", "Why does the composer jump on paste"),
];

if (params.has("long-titles")) {
  const names: Record<string, string> = {
    index: "docs vs code: desktop sessions and workspaces",
    paginate: "docs vs code: startup, configuration and command line",
    latency: "docs vs code: extensions, plugins and automation",
  };
  for (const item of threads) {
    if (names[item.id]) item.title = names[item.id];
  }
}

const candidates: ProjectCandidate[] = [
  {
    session_id: "paginate", turn_id: "paginate-turn", base_repo: "/preview", base_revision: "a".repeat(40),
    revision: "b".repeat(40), created_at: "2026-09-25T09:30:00Z",
    changed_files: ["internal/search/paginate.go", "internal/search/paginate_test.go", "desktop/src/renderer/SearchResults.tsx"],
  },
];

const diff = `diff --git a/internal/search/paginate.go b/internal/search/paginate.go
index 1111111..2222222 100644
--- a/internal/search/paginate.go
+++ b/internal/search/paginate.go
@@ -12,7 +12,9 @@ func Page(results []Result, cursor string) ([]Result, string) {
 	start := decodeCursor(cursor)
-	end := start + 50
+	end := start + pageSize
+	if end > len(results) {
+		end = len(results)
+	}
 	return results[start:end], encodeCursor(end)
 }
`;

Object.assign(window, {
  wuu: {
    projectCandidate: async (request: ProjectCandidateParams) => {
      if (request.action === "list") {
        return { candidates: candidates.filter(candidate => "project_id" in request || candidate.session_id === request.session_id) };
      }
      const candidate = candidates.find(item => item.session_id === request.session_id && item.turn_id === request.turn_id)!;
      if (request.action === "get") return { candidate: { ...candidate, diff } };
      candidate.disposition = request.action === "apply" ? "applied" : request.action === "publish" ? "published" : "discarded";
      return { candidate };
    },
    listWorkspaceDirectory: async () => ({ root: "/preview", path: "", truncated: false, entries: [] }),
  },
});

// A publisher makes Open PR visible, as the Git Delivery example does.
if (params.get("publisher") !== "0") {
  void desktopPluginHost.activateGeneration({
    pluginId: "preview:git-delivery",
    generation: "one",
    register(api) {
      api.registerCommand({ id: "open-pr", title: "GitHub draft PR", contexts: ["project-candidate.publish"],
        execute: async () => ({ url: "https://github.com/example/wuu/pull/1" }) });
    },
  });
}

function Conversation({ current }: { current: Thread }) {
  const renderTurnProposal = useTurnProposals(current);
  return <div className="conversation-width session-flow">
    {current.turns.filter(turn => turn.items.length).map(turn => <div key={turn.id}>
      <TurnView turn={turn} onStreamFrame={noop} isLatestTurn cwd="/preview" />
      {renderTurnProposal(turn.id)}
    </div>)}
  </div>;
}

function Fixture() {
  const view = params.get("view") ?? "coordinator";
  const [active, setActive] = useState(view === "session" ? "paginate" : "project");
  const [expanded, setExpanded] = useState(new Set([workspace.id]));
  const [collapsedFolderIDs, setCollapsedFolderIDs] = useState<Set<string>>(() => new Set());
  const initialTabs: WorkspaceViewTab[] = params.get("panel") === "none" || empty ? [] : [
    workspaceProjectViewTab("project", "Search overhaul"),
    ...(params.get("panel") === "proposal" ? [workspaceProposalViewTab("paginate", "Paginate search results")] : []),
  ];
  const [tabs, setTabs] = useState(initialTabs);
  const [activeTab, setActiveTab] = useState(initialTabs.at(-1)?.id);
  const environmentToggleRef = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    applyMessageFlowFontSize(Number(params.get("size")) || 14);
    document.documentElement.dataset.theme = params.get("theme") || "light";
  }, []);
  const state: AppState = {
    ...initialState, projects: [workspace], threads, activeProjectId: workspace.id,
    initialized: { protocol_version: "wuu-app-server/v0.1", provider: "preview", model: "preview", workspace_root: "/preview" },
    activeContext: { kind: "project", project_id: workspace.id, cwd: workspace.path },
    lastViewedTurnByThreadID: Object.fromEntries(threads.map(item => [item.id, item.id === "latency" ? "" : item.latest_completed_turn_id ?? ""])),
  };
  const openTab = (tab: WorkspaceViewTab) => {
    setTabs(currentTabs => currentTabs.some(item => item.id === tab.id) ? currentTabs : [...currentTabs, tab]);
    setActiveTab(tab.id);
  };
  const actions = useMemo<ProjectActions>(() => ({
    threads, openThread: setActive,
    openProjectPanel: (project) => openTab(workspaceProjectViewTab(project.id, project.title ?? "")),
    openProposal: (session) => openTab(workspaceProposalViewTab(session.id, session.title ?? "")),
    takeOver: noop, returnToProject: noop, release: noop,
  }), []);
  const current = threads.find(item => item.id === active);
  const draft = view === "draft" || empty;
  return <WuuUIRoot><ProjectActionsProvider value={actions}>
    <div className={`app-shell sample-shell${tabs.length ? " right-panel-open" : ""}`} style={{ height: "100dvh", "--workspace-right-panel-width": params.has("panel-width") ? `${Number(params.get("panel-width"))}px` : undefined } as CSSProperties}>
      <AppSidebar
        state={state}
        sidebarWorkspaces={[workspace]} pinnedThreads={[]}
        activeThreadID={draft ? undefined : active} activeWorkspaceID={workspace.id}
        collapsedSidebarSectionIDs={new Set()} expandedSidebarSectionIDs={expanded}
        collapsedFolderIDs={collapsedFolderIDs} setCollapsedFolderIDs={setCollapsedFolderIDs}
        workspaceThreadsByWorkspaceID={{ [workspace.id]: summarizeThreadsForSidebar(threads) }}
        workspaceMenuOpen={false} workspaceMenuRef={createRef()} searchOpen={false}
        sectionOrder={[workspace.id]} onStartNewThread={noop} onOpenSkillsTab={noop}
        onToggleConversationSearch={noop} onSelectThread={setActive}
        onTogglePinned={noop} onArchiveThread={noop} onDeleteThread={noop} onRenameThread={noop}
        onToggleWorkspaceMenu={noop} onCreateWorkspace={noop} onOpenWorkspaceFolder={noop}
        onToggleSidebarSectionCollapsed={id => setExpanded(currentIDs => currentIDs.has(id) ? new Set() : new Set([id]))}
        onStartNewThreadInWorkspace={noop} onSelectWorkspaceThread={(_workspace, id) => setActive(id)}
        onRemoveWorkspace={noop} onRelocateWorkspace={noop} onCreateProject={noop} onAdoptIntoProject={noop}
        onOpenSettings={noop} onMarkThreadsViewed={noop}
        unreadViewOpen={false} onToggleUnreadView={noop}
        sidebarCollapsed={false} onToggleSidebar={noop}
      />
      <main className="conversation-pane" style={{ "--dock-composer-height": "96px" } as CSSProperties}>
        <header className="titlebar"><div className="title-block"><span>{draft ? "新项目" : current?.title}</span></div>
          <ConversationTitleActions state={{ ...state, thread: draft ? undefined : current }} onStartNewThread={noop}
            environmentToggleRef={environmentToggleRef} environmentPanelVisible={false} onToggleEnvironmentPanel={noop}
            rightPanelOpen={tabs.length > 0} onToggleRightPanel={noop} />
        </header>
        <div className={`scroll-region${draft ? " empty-scroll-region" : ""}`}>
          {draft ? <EmptyConversationHome title="新项目" /> : current ? <Conversation current={current} /> : null}
        </div>
        <footer className="composer-wrap dock-composer-wrap"><div className="composer-stack"><div className="composer-shell">
          {!draft && current?.source === "project" ? <div className="composer-status-accessory"><ProjectStatusStrip project={current} /></div> : null}
          <div className="composer-frame-shell"><div className="composer-frame"><div className="composer">
            <textarea aria-label="示例输入" placeholder={draft ? "描述目标和约束…" : "即刻开始"} />
            <div className="composer-bar"><div className="composer-bar-left"><button className="composer-tool-button" aria-label="附件"><Plus className="icon" /></button></div>
              <div className="composer-bar-right"><button className="codex-runtime-trigger">Wuu · 示例模型</button><button className="composer-action-button composer-send-button" disabled aria-label="发送"><ArrowUp className="icon" /></button></div></div>
          </div></div></div>
        </div></div></footer>
      </main>
      <WorkspaceRightPanel open={tabs.length > 0} present={tabs.length > 0} tabs={tabs} activeTabID={activeTab}
        activeContext={state.activeContext} workspaceContext={state.activeContext} onSelectTab={setActiveTab} onOpenTool={noop}
        onShowTools={() => setActiveTab(undefined)} onCloseTab={id => { setTabs(currentTabs => currentTabs.filter(tab => tab.id !== id)); setActiveTab(undefined); }}
        onReorderTabs={noop} onOpenFile={noop} onClose={() => setTabs([])} globalized={false} onToggleGlobalize={noop} />
    </div>
  </ProjectActionsProvider></WuuUIRoot>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
