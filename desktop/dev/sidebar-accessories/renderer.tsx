// Isolated production sidebar: no preload, credentials, or real workspace data.
import { createRef, useLayoutEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { AppSidebar } from "../../src/renderer/AppSidebar";
import { initialState, type ThreadSummary } from "../../src/renderer/AppState";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { CLEAR_UNREAD_HINT_SEEN_KEY } from "../../src/renderer/SidebarBrand";
import "../../src/renderer/styles.css";

const noop = () => {};
// Keep onboarding copy out of the accessory comparison in this isolated profile.
localStorage.setItem(CLEAR_UNREAD_HINT_SEEN_KEY, "true");
const date = "2026-09-17T00:00:00Z";
const params = new URLSearchParams(location.search);
const project = { id: "preview", name: "wuu", path: "/preview", created_at: date, updated_at: date };
const threads: ThreadSummary[] = [
  ["idle", "普通会话：长标题应该在操作区域之前省略"],
  ["running", "正在运行的会话"],
  ["fork", "分支会话：消息气泡长内容折叠显示"],
  ["fork-running", "正在运行的分支会话"],
  ["fork-unread", "有未读结果的分支会话"],
].map(([id, preview]) => ({
  id, preview, pinned: id === "idle", cwd: "/preview", model_provider: "preview", model: "preview",
  status: id.includes("running") ? "in_progress" : "idle",
  created_at: date, updated_at: date, turn_count: 1, turns: [],
  latest_completed_turn_id: "done",
  ...(id.startsWith("fork") ? { forked_from_id: "idle" } : {}),
}));

function Fixture() {
  const [active, setActive] = useState("idle");
  const [expanded, setExpanded] = useState(new Set([project.id]));
  const [collapsedFolderIDs, setCollapsedFolderIDs] = useState<Set<string>>(() => new Set());
  useLayoutEffect(() => {
    applyMessageFlowFontSize(Number(params.get("size")) || 14);
    document.documentElement.dataset.theme = params.get("theme") || "light";
  }, []);
  const empty = params.has("empty");
  const [unreadViewOpen, setUnreadViewOpen] = useState(false);
  const visible = empty ? [] : threads;
  return <WuuUIRoot><div className="app-shell" style={{ height: "100dvh", gridTemplateColumns: "var(--sidebar-open-width) 1fr", "--sidebar-open-width": `${Number(params.get("width")) || 296}px` } as React.CSSProperties}>
    <AppSidebar
      state={{ ...initialState, projects: [project], threads: visible.map(thread => ({ ...thread, turns: [] })),
        initialized: { protocol_version: "wuu-app-server/v0.1", provider: "preview", model: "preview", workspace_root: "/preview" },
        activeContext: { kind: "project", project_id: project.id, cwd: project.path },
        lastViewedTurnByThreadID: { idle: "done", running: "done", fork: "done", "fork-running": "done" },
      }}
      sidebarProjects={[project]} pinnedThreads={empty ? [] : [threads[0]]}
      activeThreadID={active} activeProjectID={project.id}
      collapsedSidebarSectionIDs={new Set()} expandedSidebarSectionIDs={expanded}
      collapsedFolderIDs={collapsedFolderIDs} setCollapsedFolderIDs={setCollapsedFolderIDs}
      projectThreadsByProjectID={{ [project.id]: visible }}
      projectMenuOpen={false} projectMenuRef={createRef()} searchOpen={false}
      sectionOrder={[project.id]} onStartNewThread={noop} onOpenSkillsTab={noop}
      onToggleConversationSearch={noop} onSelectThread={setActive}
      onTogglePinned={noop} onArchiveThread={noop} onDeleteThread={noop} onRenameThread={noop}
      onToggleProjectMenu={noop} onCreateProject={noop} onOpenProjectFolder={noop}
      onToggleSidebarSectionCollapsed={id => setExpanded(current => current.has(id) ? new Set() : new Set([id]))}
      onStartNewThreadForProject={noop} onSelectProjectThread={(_project, id) => setActive(id)}
      onRemoveProject={noop} onRelocateProject={noop} onOpenSettings={noop} onMarkThreadsViewed={noop}
      unreadViewOpen={unreadViewOpen} onToggleUnreadView={() => setUnreadViewOpen(open => !open)}
      sidebarCollapsed={false} onToggleSidebar={noop}
    />
    <main style={{ padding: 24 }}>Sidebar accessory preview · {params.get("theme") || "light"} · {params.get("size") || 14}px</main>
  </div></WuuUIRoot>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
