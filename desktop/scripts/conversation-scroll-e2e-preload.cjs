// Each send returns a distinct turn, as the real server does, so repeated
// submissions in one conversation exercise placement rather than a replaced turn.
const { contextBridge } = require("electron");

const expose = contextBridge.exposeInMainWorld.bind(contextBridge);
let sends = 0;
contextBridge.exposeInMainWorld = (key, api) => expose(key, key !== "wuu" ? api : {
  ...api,
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
