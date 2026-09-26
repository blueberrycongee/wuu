// Isolated Projects preview: production sidebar, title controls and candidate
// cards over a mocked window.wuu. No preload, credentials, or real workspace.
// From desktop: ./node_modules/.bin/vite --config dev/projects/vite.config.ts,
// then open http://127.0.0.1:5243/dev/projects/ (?theme=dark&size=20&width=260,
// ?publisher=0 hides Open PR). capture.cjs writes screenshots to artifacts/projects.
import { createRef, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { ProjectCandidate, ProjectCandidateParams, Thread } from "../../src/shared/protocol";
import { AppSidebar } from "../../src/renderer/AppSidebar";
import { initialState, summarizeThreadsForSidebar, type AppState } from "../../src/renderer/AppState";
import { ConversationTitleActions } from "../../src/renderer/ConversationShellRenderers";
import { ProjectCandidateReview } from "../../src/renderer/ProjectCandidateReview";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { desktopPluginHost } from "../../src/renderer/plugins/DesktopPluginRuntime";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { CLEAR_UNREAD_HINT_SEEN_KEY } from "../../src/renderer/SidebarBrand";
import "../../src/renderer/styles.css";

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

const threads: Thread[] = [
  thread("project", "Search overhaul", { source: "project", permission_mode: "read_only" }),
  thread("index", "Rebuild the search index", {
    source: "project-session", project_id: "project", status: "in_progress", updated_at: "2026-09-25T09:00:00Z",
    session_control: { ...control, state: "active" },
  }),
  thread("paginate", "Paginate search results", {
    source: "project-session", project_id: "project",
    session_control: { ...control, state: "paused" },
  }),
  thread("chat", "Explain the release checklist"),
];

const candidates: ProjectCandidate[] = [
  {
    session_id: "paginate", turn_id: "paginate-turn-2", base_repo: "/preview", base_revision: "a".repeat(40),
    revision: "b".repeat(40), created_at: "2026-09-25T09:30:00Z",
    changed_files: ["internal/search/paginate.go", "internal/search/paginate_test.go", "desktop/src/renderer/SearchResults.tsx"],
  },
  {
    session_id: "paginate", turn_id: "paginate-turn-1", base_repo: "/preview", base_revision: "c".repeat(40),
    revision: "a".repeat(40), created_at: "2026-09-25T08:30:00Z", disposition: "applied",
    changed_files: ["internal/search/cursor.go"],
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
      candidate.disposition = request.action === "apply" ? "applied" : "discarded";
      return { candidate };
    },
    returnManagedSession: async () => ({ control: { ...control, state: "active" } }),
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

function Fixture() {
  const [active, setActive] = useState("project");
  const [expanded, setExpanded] = useState(new Set([workspace.id]));
  const [collapsedFolderIDs, setCollapsedFolderIDs] = useState<Set<string>>(() => new Set());
  const environmentToggleRef = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    applyMessageFlowFontSize(Number(params.get("size")) || 14);
    document.documentElement.dataset.theme = params.get("theme") || "light";
  }, []);
  const state: AppState = {
    ...initialState, projects: [workspace], threads, activeProjectId: workspace.id,
    initialized: { protocol_version: "wuu-app-server/v0.1", provider: "preview", model: "preview", workspace_root: "/preview" },
    activeContext: { kind: "project", project_id: workspace.id, cwd: workspace.path },
    lastViewedTurnByThreadID: Object.fromEntries(threads.map(item => [item.id, item.latest_completed_turn_id ?? ""])),
  };
  const titleActions = (id: string) => <ConversationTitleActions
    state={{ ...state, thread: threads.find(item => item.id === id) }}
    onStartNewThread={noop} onOpenSession={setActive}
    environmentToggleRef={environmentToggleRef} environmentPanelVisible={false} onToggleEnvironmentPanel={noop}
    rightPanelOpen={false} onToggleRightPanel={noop}
  />;
  return <WuuUIRoot><div className="app-shell" style={{ height: "100dvh", gridTemplateColumns: "var(--sidebar-open-width) 1fr", "--sidebar-open-width": `${Number(params.get("width")) || 296}px` } as React.CSSProperties}>
    <AppSidebar
      state={state}
      sidebarWorkspaces={[workspace]} pinnedThreads={[]}
      activeThreadID={active} activeWorkspaceID={workspace.id}
      collapsedSidebarSectionIDs={new Set()} expandedSidebarSectionIDs={expanded}
      collapsedFolderIDs={collapsedFolderIDs} setCollapsedFolderIDs={setCollapsedFolderIDs}
      workspaceThreadsByWorkspaceID={{ [workspace.id]: summarizeThreadsForSidebar(threads) }}
      workspaceMenuOpen={false} workspaceMenuRef={createRef()} searchOpen={false}
      sectionOrder={[workspace.id]} onStartNewThread={noop} onOpenSkillsTab={noop}
      onToggleConversationSearch={noop} onSelectThread={setActive}
      onTogglePinned={noop} onArchiveThread={noop} onDeleteThread={noop} onRenameThread={noop}
      onToggleWorkspaceMenu={noop} onCreateWorkspace={noop} onOpenWorkspaceFolder={noop}
      onToggleSidebarSectionCollapsed={id => setExpanded(current => current.has(id) ? new Set() : new Set([id]))}
      onStartNewThreadInWorkspace={noop} onSelectWorkspaceThread={(_workspace, id) => setActive(id)}
      onRemoveWorkspace={noop} onRelocateWorkspace={noop} onCreateProject={noop}
      onOpenSettings={noop} onMarkThreadsViewed={noop}
      unreadViewOpen={false} onToggleUnreadView={noop}
      sidebarCollapsed={false} onToggleSidebar={noop}
    />
    <main style={{ minWidth: 0, overflow: "auto" }}>
      <header className="titlebar"><div className="title-block">Search overhaul</div>{titleActions("project")}</header>
      <header className="titlebar"><div className="title-block">Paginate search results</div>{titleActions("paginate")}</header>
      <div className="conversation-width session-flow">
        <ProjectCandidateReview thread={threads.find(item => item.id === "paginate")!} />
      </div>
    </main>
  </div></WuuUIRoot>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
