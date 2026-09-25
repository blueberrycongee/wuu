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
const room: ChannelRoom = { id: "layout-room", name: agent.name, kind: "dm", created_by: "human", created_at,
  members: [{ room_id: "layout-room", member_type: "agent", member_id: agent.id, joined_at: created_at }] };
const running = !params.has("idle");
const runningCount = Number(params.get("running") || 3);
const sampleText = "请核对 **本轮改动**，保留 `session_id` 与 [资料链接](https://example.com)。\n\n> 引用内容应保持可读。\n\n```ts\nconst session = 'collaboration';\n```";
const threads: ThreadSummary[] = Array.from({ length: 65 }, (_, i) => ({
  id: `layout-session-${i}`, title: `会话 ${i + 1}：核对协作布局、运行状态与长标题边界`, cwd: "/fixture/collaboration-layout", status: running && i < runningCount ? "in_progress" : "idle",
  model: "fixture", model_provider: "fixture", preview: "", turns: [], turn_count: 0,
  created_at, updated_at: new Date(Date.UTC(2026, 8, 17, 10, i)).toISOString(),
}));
window.wuu = {
  bootstrapChannels: async () => ({ agents: [agent], rooms: [room] }),
  listNamedAgents: async () => ({ agents: [agent] }), listChannelRooms: async () => ({ rooms: [room] }),
  listChannelSessions: async () => ({ sessions: [] }), markChannelRoomRead: async () => ({ read: true }),
  onEvent: () => () => {}, onServerEvent: () => () => {},
  listChannelMessages: async () => ({ messages: Array.from({ length: 40 }, (_, i) => ({
    id: `message-${i}`, room_id: room.id, seq: i, author_type: i % 2 ? "agent" : "human", author_id: i % 2 ? agent.id : "human",
    kind: "text", body: i === 39 && params.has('bubbles') ? `${sampleText}\n\n${"这是一段较长的 agent 回复，用于检查多段正文与气泡内边距，附件应保持独立可用。".repeat(12)}\n\n最后一段：消息边界应清晰。` : i >= 38 ? sampleText : i % 2 ? "正在核对运行中的小球、会话入口和输入框。滚动记录不应影响右栏拖动。" : "请保留这条聊天及输入草稿，检查宽窄窗口下的会话列表。", created_at,
    ...(i === 39 && params.has('bubbles') ? { files: [{ media_type: 'application/pdf', data: 'cGRm', filename: 'review-notes.pdf' }], images: [{ media_type: 'image/svg+xml', data: btoa('<svg xmlns="http://www.w3.org/2000/svg" width="120" height="60"><rect width="120" height="60" fill="#8c9aa8"/></svg>') }] } : {}),
  })), responses: running ? [{ id: "layout-response", room_id: room.id, agent_id: agent.id, session_ref: "layout-primary", turn_id: "turn-running", state: "thinking", body: "", created_at }] : [] }),
} as unknown as WuuDesktopApi;

function Preview() {
  const [collapsed, setCollapsed] = useState(false);
  const [sectionCollapsed, setSectionCollapsed] = useState(false);
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
      sectionCollapsed={sectionCollapsed} onToggleSectionCollapsed={() => setSectionCollapsed((value) => !value)}
      selectedRoomID={room.id} onSelectRoom={() => {}} onSelectAgent={() => {}} onManageAgents={() => {}}
      onCreateRoom={() => {}} onSwitchToHarness={() => {}} onOpenSettings={() => {}} /></div>
    <div className="conversation-pane collaboration-room-pane" style={{ minWidth: 0, height: "100dvh", display: "flex" }}><ChannelView selectedRoomID={room.id}
      navigation={<button type="button" className="icon-button" aria-label="Toggle fixture sidebar" onClick={() => setCollapsed(!collapsed)}>☰</button>}
      managedThreadsByAgentID={{ [agent.id]: empty ? [] : threads }} onOpenSession={id => Object.assign(window, { selectedFixtureSession: id })} /></div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider><Preview /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
