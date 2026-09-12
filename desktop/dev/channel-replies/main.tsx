import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { ChannelView } from "../../src/renderer/ChannelView";
import { CollaborationSidebar } from "../../src/renderer/CollaborationSidebar";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

const host = window as any;
host.stage = 0;
const listeners = new Set<(event: any) => void>();
host.emitSession = (thread_id: string) => listeners.forEach(listener => listener({ kind: "notification", message: { method: "item/agentMessage/delta", params: { thread_id } } }));
document.documentElement.dataset.theme = new URLSearchParams(location.search).get("theme") || "light";
document.documentElement.dataset.platform = "mac";
const created_at = "2026-09-12T14:00:00Z";
const agents = ["Andy", "Andy2"].map((name, index) => ({ id: `a${index}`, name, memory_dir: "/preview", avatar_key: `abstract-${index + 1}`, model_override: "gpt-5.5", effort_override: "xhigh", autostart: true, created_at, activity_status: "thinking", activity_room_ids: ["room"] }));
const room = { id: "room", name: "General", kind: "channel", created_by: "human", created_at, members: agents.map(a => ({ room_id: "room", member_type: "agent", member_id: a.id, joined_at: created_at })) };
const human = { id: "human", room_id: "room", seq: 1, author_type: "human", author_id: "user", kind: "text", body: "请检查群聊的消息顺序。", created_at };
const replies = agents.map(a => ({ id: `reply-${a.id}`, room_id: "room", agent_id: a.id, session_ref: a.id, turn_id: "turn-23", state: "thinking", body: "", created_at }));
host.wuu = {
  bootstrapChannels: async () => ({ agents, rooms: [room] }),
  listNamedAgents: async () => ({ agents }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelTasks: async () => ({ tasks: [] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onEvent: () => () => {},
  onServerEvent: (listener: (event: any) => void) => { listeners.add(listener); return () => listeners.delete(listener); },
  readChannelSession: async ({ sessionRef }: { sessionRef: string }) => {
    host.lastReadSession = sessionRef;
    return {
      session: { session_ref: sessionRef, principal_id: sessionRef, named_agent_id: sessionRef, room_id: "room", state: "running", purpose: "conversation", created_at, updated_at: created_at },
      thread: { id: sessionRef, turns: Array.from({length: 24}, (_, index) => ({ id: `turn-${index}`, status: index === 23 ? "in_progress" : "completed", items: [
        { id: `user-${index}`, type: "user_message", text: `历史问题 ${index}` },
        ...(index === 23 ? [{ id: "compaction", type: "context_compaction", status: "failed", text: "Context compaction failed; continuing without compacting history." }] : []),
        { id: `tool-${index}`, type: "tool_call", name: index === 23 ? "chat_read" : "read_file", status: "completed", result: `已读取第 ${index} 份资料。`,
          ...(index === 23 ? { result_detail: { content: [
            { type: "text", text: JSON.stringify({ messages: Array.from({length: 100}, (_, seq) => ({seq, body: `Raw message ${seq}`})) }) },
            { type: "image", mime_type: "image/svg+xml", data: btoa('<svg xmlns="http://www.w3.org/2000/svg" width="80" height="40"><rect width="80" height="40" fill="lightblue"/></svg>'), name: "Screenshot" },
          ] } } : {}),
        },
        { id: `answer-${index}`, type: "agent_message", phase: "final_answer", text: index === 23 ? `实时输出版本 ${host.stage}，对应 ${sessionRef}` : `历史回答 ${index}` },
      ] })) },
    };
  },
  listChannelMessages: async () => {
    const responses = replies.map((r, i) => ({ ...r, state: host.stage > (i === 1 ? 0 : 1) ? "responding" : "thinking", body: host.stage > (i === 1 ? 0 : 1) ? `${agents[i].name}：消息有内容后才出现，并保持阅读顺序。` : "" }));
    const messages: (typeof human & { source_session_ref?: string; source_turn_id?: string })[] = [human];
    for (const i of [0, 1]) if (host.stage >= i + 3) messages.push({ ...human, id: responses[i].id, seq: i + 2, author_type: "agent", author_id: agents[i].id, body: responses[i].body, source_session_ref: agents[i].id, source_turn_id: "turn-2" });
    return { messages, responses };
  },
};
function Preview() {
  const [collapsed, setCollapsed] = useState(true);
  const [selected, setSelected] = useState("room");
  if (!new URLSearchParams(location.search).has("rail")) return <div style={{ height: "100vh", display: "flex" }}>
    <ChannelView selectedRoomID="room" />
  </div>;
  const directory = Array.from({length: 16}, (_, index) => ({...agents[index % 2], id: `a${index}`, name: `Agent ${index + 1}`, avatar_key: `abstract-${index % 8 + 1}`}));
  const groups = [{...room, unread_count: 4}, {...room, id: "second", name: "Research", unread_count: 2}];
  return <div className={`app-shell${collapsed ? " collaboration-rail" : ""}`} style={{height: "100vh", display: "grid", gridTemplateColumns: "var(--sidebar-width) minmax(0, 1fr)", "--sidebar-width": collapsed ? "88px" : "296px", "--sidebar-open-width": collapsed ? "88px" : "296px"} as React.CSSProperties}>
    <CollaborationSidebar initialized agents={directory as any} rooms={groups as any} pinnedRoomIDs={["second"]}
      collapsed={collapsed} selectedRoomID={selected} selectedAgentID={selected} onToggleCollapsed={() => setCollapsed(!collapsed)}
      onSelectAgent={setSelected} onSelectRoom={setSelected} onManageAgents={() => {}} onCreateAgent={() => {}}
      onCreateRoom={() => {}} onSwitchToHarness={() => {}} onOpenSettings={() => {}} />
    <div style={{minWidth: 0, height: "100vh", display: "flex"}}><ChannelView selectedRoomID="room" /></div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider><Preview /></ImagePreviewProvider></WuuUIRoot></I18nProvider>);
