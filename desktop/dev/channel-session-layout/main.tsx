import { useState } from "react";
import { createRoot } from "react-dom/client";
import type { ChannelRoom, NamedAgent, WuuDesktopApi } from "../../src/shared/protocol";
import type { ThreadSummary } from "../../src/renderer/AppState";
import { ChannelView } from "../../src/renderer/ChannelView";
import { CollaborationSidebar } from "../../src/renderer/CollaborationSidebar";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { ThreadItemView } from "../../src/renderer/ThreadItemView";
import "../../src/renderer/styles.css";

// A deterministic renderer fixture: no app-server, credentials or real sessions.
const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
document.documentElement.dataset.platform = "mac";
const font = Number(params.get("font") || 14);
document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
document.documentElement.style.setProperty("--appearance-scale", String(font / 14));
const created_at = "2026-09-17T10:00:00Z";
const agent: NamedAgent = { id: "layout-agent", name: "研究助手", memory_dir: "/fixture", avatar_key: "mascot-v1:round:none:150", autostart: false, created_at };
const room: ChannelRoom = { id: "layout-room", name: agent.name, kind: "dm", workspace_root: "/fixture/catalog", created_by: "human", created_at,
  members: [{ room_id: "layout-room", member_type: "agent", member_id: agent.id, joined_at: created_at }] };
const running = !params.has("idle");
const runningCount = Number(params.get("running") || 3);
const sampleText = "请核对 **本轮改动**，保留 `session_id` 与 [资料链接](https://example.com)。\n\n> 引用内容应保持可读。\n\n```ts\nconst session = 'collaboration';\n```";
const threads: ThreadSummary[] = Array.from({ length: 65 }, (_, i) => ({
  id: `layout-session-${i}`, title: `会话 ${i + 1}：核对协作布局、运行状态与长标题边界`, cwd: "/fixture/collaboration-layout", status: running && i < runningCount ? "in_progress" : "idle",
  model: "fixture", model_provider: "fixture", preview: "", turns: [], turn_count: 0,
  created_at, updated_at: new Date(Date.UTC(2026, 8, 17, 10, i)).toISOString(),
}));
const candidate = { session_id: "layout-session-0", turn_id: "turn-1", work_id: "work-1", goal_revision: 1, root: "/fixture/worktree", base_repo: "/fixture/catalog", base_revision: "a".repeat(40), revision: "b".repeat(40), diff: "diff --git a/search.ts b/search.ts\n--- a/search.ts\n+++ b/search.ts\n@@ -1 +1 @@\n-export const pageSize = 20;\n+export const pageSize = 50;\n", report: { result: "搜索已切换为服务端分页，公开 API 保持兼容。", implicit_choices: ["使用游标分页"], evidence_refs: ["分页边界测试通过", "现有公开 API 的调用用例通过"], unresolved_items: [] } };
const artifact = { id: "artifact-1", work_id: "work-1", run_id: "run-1", kind: "candidate", uri: "/fixture/candidate.json", created_at };
const work = { id: "work-1", room_id: room.id, title: "搜索服务端分页", brief: "每页 50 条，保留公开 API。", revision: 4, goal_revision: 1, candidate_revision: 1, state: "checking", candidate_artifact_ref: artifact.id, verification_required: true, verification_state: "pass", artifacts: [artifact], runs: [{ id: "run-1", session_ref: candidate.session_id, kind: "producer", state: "completed" }], verification: { decision: "pass", report: "已检查边界条件与 API 兼容性。" } };
window.wuu = {
  channelWorkCandidate: async ({ action }: { action: string }) => ({ candidate, artifact: { ...artifact, disposition: action === "get" ? undefined : action === "apply" ? "applied" : "discarded" }, work_revision: 4, stale: false }),
  channelContinuity: async ({ action, memory }: { action: string; memory?: { action: string; name: string } }) => action === "memory" ? { entries: [{ name: memory?.name || "MEMORY.md", revision: "fixture-1", content: "测试前先启动本地搜索服务。" }] } : { arrangements: [{ id: "timer-1", note: "每个工作日上午检查搜索项目的未决问题。", state: "active", next_at: "2026-09-26T09:00:00+08:00", revision: 1 }] },
  listProjects: async () => ({ projects: [{ id: "catalog", name: "Catalog", path: "/fixture/catalog" }] }),
  bootstrapChannels: async () => ({ agents: [agent], rooms: [room] }),
  listNamedAgents: async () => ({ agents: [agent] }), listChannelRooms: async () => ({ rooms: [room] }),
  listChannelSessions: async () => ({ sessions: [] }), markChannelRoomRead: async () => ({ read: true }),
  onEvent: () => () => {}, onServerEvent: () => () => {},
  listChannelMessages: async () => ({ messages: params.has("review") ? [{ id: "request-1", room_id: room.id, seq: 1, author_type: "human", author_id: "human", kind: "text", body: "把搜索改为服务端分页，每页 50 条，保留公开 API。", created_at }, { id: work.id, room_id: room.id, seq: 2, author_type: "agent", author_id: agent.id, kind: "task", task_title: work.title, task_state: "checking", body: work.brief, work, created_at }] : Array.from({ length: 40 }, (_, i) => ({
    id: `message-${i}`, room_id: room.id, seq: i, author_type: i % 2 ? "agent" : "human", author_id: i % 2 ? agent.id : "human",
    kind: "text", body: i === 39 && params.has('bubbles') ? `${sampleText}\n\n${"这是一段较长的 agent 回复，用于检查多段正文与气泡内边距，附件应保持独立可用。".repeat(12)}\n\n最后一段：消息边界应清晰。` : i >= 38 ? sampleText : i % 2 ? "正在核对运行中的小球、会话入口和输入框。滚动记录不应影响右栏拖动。" : "请保留这条聊天及输入草稿，检查宽窄窗口下的会话列表。", created_at,
    ...(i === 39 && params.has('bubbles') ? { files: [{ media_type: 'application/pdf', data: 'cGRm', filename: 'review-notes.pdf' }], images: [{ media_type: 'image/svg+xml', data: btoa('<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60"><rect width="120" height="60" fill="#8c9aa8"/></svg>') }] } : {}),
  })), responses: running ? [{ id: "layout-response", room_id: room.id, agent_id: agent.id, session_ref: "layout-primary", turn_id: "turn-running", state: "thinking", body: "", created_at }] : [] }),
} as unknown as WuuDesktopApi;

function Preview() {
  const [collapsed, setCollapsed] = useState(false);
  const [empty, setEmpty] = useState(false);
  Object.assign(window, { layoutFixture: { setEmpty } });
  if (params.has('session')) return <main className="conversation-pane" style={{ height: '100dvh', overflow: 'auto', padding: 32 }}>
    <section className="conversation-width session-flow" style={{ width: '100%', maxWidth: 720, margin: '0 auto', display: 'flex', flexDirection: 'column', gap: 24 }}>
      <h2>Session · current message components</h2>
      <ThreadItemView turnID="reference" turnStatus="completed" streaming={false} onStreamFrame={() => {}}
        item={{ id: 'reference-human', type: 'user_message', text: sampleText }} />
      <ThreadItemView turnID="reference" turnStatus="completed" streaming={false} onStreamFrame={() => {}}
        item={{ id: 'reference-agent', type: 'agent_message', text: sampleText, terminal: true, status: 'completed' }} />
    </section>
  </main>;
  return <div className="app-shell" style={{ height: "100dvh", display: "grid", gridTemplateColumns: `${collapsed ? 0 : 240}px minmax(0, 1fr)` }}>
    {params.has('bubbles') && !collapsed ? <div className="sidebar-resizer" role="separator" aria-label="Existing sidebar divider reference" style={{ left: 235 }} /> : null}
    <div style={{ minWidth: 0, overflow: "hidden" }}><CollaborationSidebar embedded initialized agents={[agent]} rooms={[room]} pinnedRoomIDs={[]}
      selectedRoomID={room.id} onSelectRoom={() => {}} onSelectAgent={() => {}} onManageAgents={() => {}}
      onCreateRoom={() => {}} onSwitchToHarness={() => {}} onOpenSettings={() => {}} /></div>
    <div className="conversation-pane collaboration-room-pane" style={{ minWidth: 0, height: "100dvh", display: "flex" }}><ChannelView selectedRoomID={room.id}
      navigation={<button type="button" className="icon-button" aria-label="Toggle fixture sidebar" onClick={() => setCollapsed(!collapsed)}>☰</button>}
      managedThreadsByAgentID={{ [agent.id]: empty ? [] : threads }} onOpenSession={id => Object.assign(window, { selectedFixtureSession: id })} /></div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider><Preview /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
