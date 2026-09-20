// Isolated production sidebar: no preload, credentials, or real workspace data.
import { createRef, useLayoutEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { AppSidebar } from "../../src/renderer/AppSidebar";
import { CollaborationSidebar } from "../../src/renderer/CollaborationSidebar";
import { ChannelView } from "../../src/renderer/ChannelView";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import type { ChannelRoom, NamedAgent, WuuDesktopApi } from "../../src/shared/protocol";
import { initialState, type ThreadSummary } from "../../src/renderer/AppState";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { CLEAR_UNREAD_HINT_SEEN_KEY } from "../../src/renderer/AppModeSwitch";
import "../../src/renderer/styles.css";

const noop = () => {};
// Keep onboarding copy out of the accessory comparison in this isolated profile.
localStorage.setItem(CLEAR_UNREAD_HINT_SEEN_KEY, "true");
const date = "2026-09-17T00:00:00Z";
const params = new URLSearchParams(location.search);
const project = { id: "preview", name: "wuu", path: "/preview", created_at: date, updated_at: date };
const agents: NamedAgent[] = ["小葵", "团子", "奶龙"].map((name, i) => ({
  id: `agent-${i}`, name, avatar_key: `abstract-${i + 1}`, memory_dir: "", autostart: true, created_at: date,
  activity_status: i === 0 ? "thinking" : "idle",
}));
const rooms: ChannelRoom[] = agents.map((agent, i) => ({
  id: `dm-${i}`, kind: "dm", name: agent.name, created_by: "human", created_at: date,
  members: [{ room_id: `dm-${i}`, member_id: agent.id, member_type: "agent", joined_at: date }],
  unread_count: i === 0 ? 124 : 0,
}));
rooms.push({ id: "group", kind: "channel", name: "奶龙、团子和小葵的设计协作小组", created_by: "human", created_at: date, members: [] });
if (params.has("agents")) window.wuu = {
  bootstrapChannels: async () => ({ agents, rooms }),
  listNamedAgents: async () => ({ agents }),
  listChannelRooms: async () => ({ rooms }),
  listChannelMessages: async ({ room_id }) => ({ messages: [{ id: `${room_id}-message`, room_id, seq: 1, kind: "text",
    author_type: "agent", author_id: "agent-0", body: "我正在处理这几个会话，完成后会在这里汇总结果。", created_at: date }], responses: [] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onServerEvent: () => () => {},
} as Partial<WuuDesktopApi> as WuuDesktopApi;
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
  const [active, setActive] = useState(params.has("agents") ? "dm-0" : "idle");
  const [expanded, setExpanded] = useState(new Set([project.id]));
  useLayoutEffect(() => {
    applyMessageFlowFontSize(Number(params.get("size")) || 14);
    document.documentElement.dataset.theme = params.get("theme") || "light";
  }, []);
  const empty = params.has("empty");
  const agentNav = params.has("agents");
  const [unreadViewOpen, setUnreadViewOpen] = useState(false);
  const managed: ThreadSummary[] = empty ? [] : Array.from({ length: 68 }, (_, i) => ({
    ...threads[i % threads.length], id: `managed-${i}`, title: `会话 ${i + 1}：验证长标题与独立的状态显示`,
    status: !params.has("idle") && i < 3 ? "in_progress" : "idle",
    updated_at: new Date(Date.UTC(2026, 8, 17, 0, i)).toISOString(),
  }));
  const select = (id: string) => setActive(id);
  const visible = empty ? [] : threads;
  return <WuuUIRoot><ImagePreviewProvider><div className="app-shell" style={{ height: "100dvh", gridTemplateColumns: "var(--sidebar-open-width) 1fr", "--sidebar-open-width": `${Number(params.get("width")) || 296}px` } as React.CSSProperties}>
    <AppSidebar
      state={{ ...initialState, projects: [project], threads: visible.map(thread => ({ ...thread, turns: [] })),
        initialized: { protocol_version: "wuu-app-server/v0.1", provider: "preview", model: "preview", workspace_root: "/preview" },
        activeContext: { kind: "project", project_id: project.id, cwd: project.path },
        lastViewedTurnByThreadID: { idle: "done", running: "done", fork: "done", "fork-running": "done" },
      }}
      sidebarProjects={[project]} pinnedThreads={empty ? [] : [threads[0]]}
      collaborationNavigation={agentNav ? <CollaborationSidebar embedded initialized
        agents={empty ? [] : agents} rooms={empty ? [] : rooms} selectedRoomID={active}
        pinnedRoomIDs={["dm-1"]}
        onSelectAgent={select} onSelectRoom={select} onManageAgents={noop}
        onCreateRoom={noop} onSwitchToHarness={noop} onOpenSettings={noop} /> : undefined}
      activeThreadID={agentNav ? undefined : active} activeProjectID={project.id}
      collapsedSidebarSectionIDs={new Set()} expandedSidebarSectionIDs={expanded}
      projectThreadsByProjectID={{ [project.id]: visible }}
      projectMenuOpen={false} projectMenuRef={createRef()} searchOpen={false}
      sectionOrder={[project.id]} onStartNewThread={noop} onOpenSkillsTab={noop}
      onToggleConversationSearch={noop} onSelectThread={select}
      onTogglePinned={noop} onArchiveThread={noop} onDeleteThread={noop} onRenameThread={noop}
      onToggleProjectMenu={noop} onCreateProject={noop} onOpenProjectFolder={noop}
      onToggleSidebarSectionCollapsed={id => setExpanded(current => current.has(id) ? new Set() : new Set([id]))}
      onStartNewThreadForProject={noop} onSelectProjectThread={(_project, id) => setActive(id)}
      onRemoveProject={noop} onRelocateProject={noop} onOpenSettings={noop} onMarkThreadsViewed={noop}
      unreadViewOpen={unreadViewOpen} onToggleUnreadView={() => setUnreadViewOpen(open => !open)}
      sidebarCollapsed={false} onToggleSidebar={noop}
    />
    {agentNav ? <main className="conversation-pane collaboration-room-pane">
      <ChannelView selectedRoomID={active} onSelectRoom={select} directoryAgents={agents} directoryRooms={rooms}
        managedThreadsByAgentID={{ [agents[0].id]: managed }} onOpenSession={id => { document.body.dataset.openedSession = id; }}
        initialized={{ protocol_version: "wuu-app-server/v0.1", provider: "preview", model: "preview", workspace_root: "/preview" }} /></main>
      : <main style={{ padding: 24 }}>Sidebar accessory preview · {params.get("theme") || "light"} · {params.get("size") || 14}px</main>}
  </div></ImagePreviewProvider></WuuUIRoot>;
}

createRoot(document.getElementById("root")!).render(<I18nProvider><Fixture /></I18nProvider>);
