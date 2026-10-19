// Real renderer recovery fixture, reusing the shared synthetic backend.
const { ipcRenderer } = require("electron");
const prompt = "Confirm the native conversation before sending this human supplement again.\n" + "Preserved original content. ".repeat(12);
module.exports = (api) => ({
  ...api,
  initialThemePreference: process.env.WUU_STEER_UI_THEME || "light",
  initialLanguagePreference: process.env.WUU_STEER_UI_LANGUAGE || "en-US",
  initialMessageFlowFontSize: Number(process.env.WUU_STEER_UI_SIZE || 14),
  getLanguagePreference: async () => process.env.WUU_STEER_UI_LANGUAGE || "en-US",
  getMessageFlowFontSize: async () => Number(process.env.WUU_STEER_UI_SIZE || 14),
  getThemePreference: async () => process.env.WUU_STEER_UI_THEME || "light",
  resumeThread: async (id) => ({
    ...await api.resumeThread(id),
    held_user_messages: [
      { id: "unknown-native", thread_id: id, origin: "steer", prompt, delivery_unconfirmed: true },
      { id: "ordinary-held", thread_id: id, origin: "steer", prompt: "Ordinary held input remains editable." },
    ],
  }),
  ...Object.fromEntries(["queueTurn", "steerTurn", "requeueTurn", "unsteerTurn", "dequeueTurn", "updateQueuedTurn"].map((method) => [method, async (...args) => {
    ipcRenderer.send("test:steer-mutation", { method, args });
    throw new Error("Unexpected mutation in read-only delivery recovery fixture");
  }])),
});
module.exports.prompt = prompt;
