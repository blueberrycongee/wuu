// Each send returns a distinct turn, as the real server does, so repeated
// submissions in one conversation exercise placement rather than a replaced turn.
const { contextBridge, ipcRenderer } = require("electron");

// Search reads only the synthetic snapshots sent by this fixture.
const searchThreads = new Map();
ipcRenderer.on("test:server-event", (_event, payload) => {
  const thread = payload.message?.params?.thread;
  if (thread) searchThreads.set(thread.id, thread);
});

const expose = contextBridge.exposeInMainWorld.bind(contextBridge);
let sends = 0;
contextBridge.exposeInMainWorld = (key, api) => expose(key, key !== "wuu" ? api : {
  ...api,
  searchThreads: async (query) => ({
    results: [...searchThreads.values()].flatMap(thread => {
      const item = thread.turns.flatMap(turn => turn.items).find(item =>
        item.type === "user_message" && item.text?.toLowerCase().includes(query.trim().toLowerCase()));
      return item ? [{ thread, message_seq: item.seq, snippet: item.text }] : [];
    }),
  }),
  startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => {
    sends += 1;
    return {
      turn: {
        id: `sent-turn-${sends}`,
        status: "in_progress",
        items_view: "full",
        started_at: new Date().toISOString(),
        items: [{ id: `sent-user-${sends}`, type: "user_message", status: "completed", text, source_id: clientId, images }],
      },
    };
  },
});
require("./streaming-e2e-preload.cjs");
