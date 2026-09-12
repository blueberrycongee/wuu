import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { ChannelView } from "../../src/renderer/ChannelView";
import { CollaborationSidebar } from "../../src/renderer/CollaborationSidebar";
import { I18nProvider } from "../../src/renderer/i18n";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import type { ChannelRoom, InitializeResult, NamedAgent } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";

const host = window as any;
document.documentElement.dataset.theme = new URLSearchParams(location.search).get("theme") || "light";
document.documentElement.dataset.platform = "mac";
const created_at = "2026-09-12T14:00:00Z";
let agents: NamedAgent[] = ["日程助手", "研究助手"].map((name, index) => ({
  id: `agent-${index}`, name, role: index ? "对照原始资料，检查引用与结论。" : "管理日程和待办。在指定时间提醒，并清楚说明下一步。",
  memory_dir: "/preview", avatar_key: `abstract-${index + 1}`, provider_override: "openai", model_override: "gpt-6-astra", effort_override: "high", autostart: true, created_at,
}));
const rooms: ChannelRoom[] = [
  { id: "dm", name: "日程助手", kind: "dm", created_by: "human", created_at, members: [{ room_id: "dm", member_type: "agent", member_id: "agent-0", joined_at: created_at }] },
  { id: "group", name: "项目讨论", kind: "channel", created_by: "human", created_at, members: agents.map(agent => ({ room_id: "group", member_type: "agent", member_id: agent.id, joined_at: created_at })) },
];
const initialized = {
  protocol_version: "1", provider: "openai", model: "gpt-6-astra", effort: "high", workspace_root: "/preview",
  providers: [{ name: "openai", type: "openai", model: "gpt-6-astra", models: [
    { id: "gpt-6-astra", display_name: "GPT-6 Astra", supported_efforts: ["low", "medium", "high"], default_effort: "medium" },
    { id: "gpt-5.5", display_name: "GPT-5.5", supported_efforts: ["low", "medium", "high"], default_effort: "medium" },
  ] }],
} as InitializeResult;
host.updates = [];
host.wuu = {
  bootstrapChannels: async () => ({ agents, rooms }),
  listNamedAgents: async () => ({ agents }), listChannelRooms: async () => ({ rooms }),
  listChannelTasks: async () => ({ tasks: [] }), listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }), onEvent: () => () => {}, onServerEvent: () => () => {},
  updateNamedAgent: async (params: any) => {
    host.updates.push(params);
    if (host.failSave) throw new Error("保存失败，请重试");
    agents = agents.map(agent => agent.id === params.agent_id ? { ...agent, ...params } : agent);
    return { agent: agents.find(agent => agent.id === params.agent_id) };
  },
  listChannelMessages: async ({ room_id }: { room_id: string }) => ({ messages: [
    { id: "human", room_id, seq: 1, author_type: "human", author_id: "user", kind: "text", body: "周一上午提醒我确认项目排期，顺便整理本周的待办。", created_at },
    { id: "reply", room_id, seq: 2, author_type: "agent", author_id: "agent-0", kind: "text", body: "好的。本周先完成资料整理和方案评审；周一上午确认项目排期。\n\n你也可以在聊天旁调整我的角色说明和模型。", created_at },
  ] }),
};
function Preview() {
  const [selected, setSelected] = useState("dm");
  const [collapsed, setCollapsed] = useState(false);
  host.selectRoom = setSelected;
  return <div className={`app-shell${collapsed ? " collaboration-rail" : ""}`} style={{ height: "100vh", display: "grid", gridTemplateColumns: "var(--sidebar-width) minmax(0, 1fr)", "--sidebar-width": collapsed ? "88px" : "240px" } as React.CSSProperties}>
    <CollaborationSidebar initialized agents={agents} rooms={rooms} pinnedRoomIDs={[]}
      collapsed={collapsed} selectedRoomID={selected} onToggleCollapsed={() => setCollapsed(!collapsed)}
      onSelectRoom={setSelected} onSelectAgent={() => {}} onManageAgents={() => {}}
      onCreateRoom={() => {}} onSwitchToHarness={() => {}} onOpenSettings={() => {}} />
    <div style={{ minWidth: 0, height: "100vh", display: "flex" }}><ChannelView selectedRoomID={selected} initialized={initialized} /></div>
  </div>;
}
createRoot(document.getElementById("root")!).render(<I18nProvider><WuuUIRoot><Preview /></WuuUIRoot></I18nProvider>);
