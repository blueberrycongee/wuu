import React from "react";
import { createRoot } from "react-dom/client";
import { ChannelView } from "../../src/renderer/ChannelView";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

const host = window as any;
const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "mac";
const created_at = "2026-09-12T14:00:00Z";
const agents = ["Alice", "Bob"].map((name, index) => ({
  id: `a${index}`, name, memory_dir: "/preview", avatar_key: `abstract-${index + 1}`,
  autostart: true, created_at, activity_status: "idle", activity_room_ids: [],
}));
const room = { id: "room", name: "项目协作", kind: "channel", created_by: "human", created_at,
  members: agents.map(a => ({ room_id: "room", member_type: "agent", member_id: a.id, joined_at: created_at })) };
const initialized = { protocol_version: "1", provider: "openai", model: "gpt-5.5", effort: "medium", workspace_root: "/preview", providers: [] };
host.coordination = { state: query.get("state") || "working", session_ref: "private-room-session", agent_ids: ["a0"], error: "模型服务暂时不可用，请稍后重试。" };
host.retryCount = 0;
host.responses = [];
host.setScene = (state: string, memberStates: string[] = []) => {
  host.coordination = { ...host.coordination, state, agent_ids: memberStates.map((_, index) => agents[index].id) };
  host.responses = memberStates.map((state, index) => ({ id: `response-${index}`, room_id: "room", agent_id: agents[index].id,
    session_ref: `member-session-${index}`, turn_id: "turn", state, body: "", created_at }));
};
host.arrangements = [
 { id:"weekly",owner_id:"a0",room_id:"room",scope:"agent",mode:"message",note:"每周五提醒我整理本周进展，列出下周需要继续跟进的事项。",state:"active",next_at:"2026-09-18T01:00:00Z",schedule:"0 9 * * 5",timezone:"Asia/Shanghai",revision:1 },
 { id:"research",owner_id:"a1",room_id:"room",scope:"session",mode:"wake",note:"等调研会话结束后，结合结果继续检查我们的长期任务设计。",state:"paused",when_session:"research-session",next_at:created_at,revision:2 },
];
host.notebook = {"preferences.md": {name:"preferences.md",revision:"r1",content:"# 沟通偏好\n\n每周五上午汇总进展。先讲结论，必要时附上依据。\n\n来源：2026 年 9 月 13 日的房间讨论。"}};
host.wuu = {
  initialLanguagePreference: query.get("locale") || "zh-CN",
  channelContinuity: async (p:any) => {
    if (p.action==="list") return {arrangements:host.arrangements};
    if (p.action==="control") {host.arrangements=host.arrangements.map((item:any)=>item.id===p.id?{...item,state:p.state,revision:item.revision+1}:item);return {};}
    const m=p.memory;
    if(m.action==="list") return {entries:Object.values(host.notebook)};
    if(m.action==="read") return {entries:[host.notebook[m.name]]};
    if(m.action==="delete") delete host.notebook[m.name];
    if(m.action==="write") host.notebook[m.name]={name:m.name,revision:"r2",content:m.content};
    return {entries:[]};
  },
  bootstrapChannels: async () => ({ agents, rooms: [room] }),
  listNamedAgents: async () => ({ agents }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelTasks: async () => ({ tasks: [] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onEvent: () => () => {},
  onServerEvent: () => () => {},
  listChannelMessages: async () => ({
    messages: [{ id: "human", room_id: "room", seq: 1, author_type: "human", author_id: "user", kind: "text", body: "检查登录恢复的问题，安排合适的成员处理。", created_at }],
    responses: host.responses, coordinator: host.coordination,
  }),
  readChannelSession: async ({ sessionRef }: { sessionRef: string }) => {
    host.lastReadSession = sessionRef;
    return { session: { session_ref: sessionRef, principal_id: "a0", named_agent_id: "a0", room_id: "room", purpose: "conversation", state: "running", created_at, updated_at: created_at },
      thread: { id: sessionRef, turns: [{ id: "turn", status: "in_progress", items: [{ id: "request", type: "user_message", text: "Inspect the login recovery" }] }] } };
  },
  resumeChannelSession: async ({ sessionRef }: { sessionRef: string }) => {
    if (sessionRef !== "private-room-session") throw new Error("Wrong retry target");
    host.retryCount++;
    host.coordination = { ...host.coordination, state: "working", error: "" };
    return {};
  },
};
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><ImagePreviewProvider>
  <div style={{ height: "100vh", display: "flex" }}><ChannelView selectedRoomID="room" initialized={initialized} /></div>
</ImagePreviewProvider></WuuUIRoot></I18nProvider>);
