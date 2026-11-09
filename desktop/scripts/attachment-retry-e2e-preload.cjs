// Extends the shared synthetic backend; never starts a core or a provider.
const { ipcRenderer } = require("electron");
module.exports = (api) => ({
  ...api,
  listThreads: async () => {
    const result = await api.listThreads();
    return { ...result, threads: [...result.threads, { ...result.threads[0], id: "attachment-other", preview: "Attachment other conversation" }] };
  },
  resumeThread: async (id) => {
    if (id !== "attachment-other") return api.resumeThread(id);
    const result = await api.resumeThread("resize-thread");
    await ipcRenderer.invoke("test:attachment-resume");
    return { ...result, thread: { ...result.thread, id, preview: "Attachment other conversation" } };
  },
  startTurn: async (...args) => {
    ipcRenderer.send("test:attachment-submitted", { threadId: args[0], text: args[1], images: args[2], files: args[3] });
    const error = await ipcRenderer.invoke("test:attachment-submit-error");
    if (error) throw new Error(error);
    return api.startTurn(...args);
  },
});
