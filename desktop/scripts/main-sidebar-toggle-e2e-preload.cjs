const { contextBridge } = require("electron");
const expose = contextBridge.exposeInMainWorld.bind(contextBridge);
const created_at = "2026-09-17T00:00:00Z";
const agent = { id: "sidebar-fixture-agent", name: "Sidebar fixture agent", avatar_key: "abstract-1", memory_dir: "/fixture", autostart: false, created_at };
const room = { id: "sidebar-fixture-room", name: agent.name, kind: "dm", created_by: "human", created_at, members: [{ room_id: "sidebar-fixture-room", member_type: "agent", member_id: agent.id, joined_at: created_at }] };
contextBridge.exposeInMainWorld = (key, api) => expose(key, key !== "wuu" ? api : {
  ...api,
  listNamedAgents: async () => ({ agents: [agent] }),
  listChannelRooms: async () => ({ rooms: [room] }),
  listChannelMessages: async () => ({ messages: [], responses: [] }),
  listChannelSessions: async () => ({ sessions: [] }),
  markChannelRoomRead: async () => ({ read: true }),
  openChannelDirectMessage: async () => ({ room }),
});
require("./resize-e2e-preload.cjs");
