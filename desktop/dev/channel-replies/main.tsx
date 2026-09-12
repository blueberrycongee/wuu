import React from "react";
import { createRoot } from "react-dom/client";
import { ChannelView } from "../../src/renderer/ChannelView";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";

const host = window as any;
host.stage = 0;
document.documentElement.dataset.theme = new URLSearchParams(location.search).get("theme") || "light";
document.documentElement.dataset.platform = "mac";
const created_at = "2026-09-12T14:00:00Z";
const agents = ["Andy", "Andy2"].map((name, index) => ({ id: `a${index}`, name, memory_dir: "/preview", avatar_key: `abstract-${index + 1}`, autostart: true, created_at, activity_status: "thinking", activity_room_ids: ["room"] }));
const room = { id: "room", name: "General", kind: "channel", created_by: "human", created_at, members: agents.map(a => ({ room_id: "room", member_type: "agent", member_id: a.id, joined_at: created_at })) };
const human = { id: "human", room_id: "room", seq: 1, author_type: "human", author_id: "user", kind: "text", body: "请检查群聊的消息顺序。", created_at };
const replies = agents.map(a => ({ id: `reply-${a.id}`, room_id: "room", agent_id: a.id, session_ref: a.id, turn_id: a.id, state: "thinking", body: "", created_at }));
host.wuu = {
  bootstrapChannels: async () => ({ agents, rooms: [room] }),
  listNamedAgents: async () => ({ agents }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelTasks: async () => ({ tasks: [] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  onEvent: () => () => {},
  listChannelMessages: async () => {
    const responses = replies.map((r, i) => ({ ...r, state: host.stage > (i === 1 ? 0 : 1) ? "responding" : "thinking", body: host.stage > (i === 1 ? 0 : 1) ? `${agents[i].name}：消息有内容后才出现，并保持阅读顺序。` : "" }));
    const messages = [human];
    for (const i of [0, 1]) if (host.stage >= i + 3) messages.push({ ...human, id: responses[i].id, seq: i + 2, author_type: "agent", author_id: agents[i].id, body: responses[i].body });
    return { messages, responses };
  },
};
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><div style={{ height: "100vh", display: "flex" }}><ChannelView selectedRoomID="room" /></div></WuuUIRoot></I18nProvider>);
