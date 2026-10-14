// Production renderer with a synthetic bridge, no provider or local file reads.
const { ipcRenderer } = require("electron");
module.exports = (api) => {
  let context = ipcRenderer.sendSync("test:draft-context");
  async function threads() {
    const base = (await api.listThreads()).threads[0];
    const otherCwd = await ipcRenderer.invoke("test:draft-other-cwd");
    const baseThreads = ["resize-thread", "draft-other"].map(id => {
      const thread = { ...base, id, cwd: id === "draft-other" && otherCwd ? otherCwd : context.cwd,
        preview: id === "draft-other" ? "Other recovery conversation" : "Primary recovery conversation",
        turns: base.turns.map(turn => ({ ...turn, items: turn.items.map(item => ({ ...item })) })) };
      if (id === "resize-thread") {
        const last = thread.turns.at(-1);
        last.items[0] = { ...last.items[0], origin: "host", presentation_kind: "session_message", related_session_id: "draft-other" };
      }
      return thread;
    });
    return [...baseThreads, ...await ipcRenderer.invoke("test:draft-created-threads")];
  }
  return {
    ...api,
    getThreadExecutionEnvironment: async thread_id => ({ thread_id, environment: { state: "local", root: context.cwd } }),
    listWorkspaceFiles: async () => ({ root: context.cwd, paths: ["private-selection.md"], truncated: false }),
    listWorkspaceDirectory: async (path = "", _root, target) => ({ target: target && { thread_id: target.thread_id, environment_id: "local" }, root: context.cwd, path, entries: path ? [] : [{ name: "private-selection.md", path: "private-selection.md", kind: "file" }], truncated: false }),
    readWorkspaceFile: async (path, _root, target) => ({ target: target && { thread_id: target.thread_id, environment_id: "local" }, root: context.cwd, path, absolute_path: context.cwd + "/" + path, size_bytes: 80, binary: false, truncated: false, text: "# Selection fixture\n\nReference marker must stay out of recovery.\n" }),
    listProjects: async () => ({ projects: await ipcRenderer.invoke("test:draft-projects"), active_context: context }),
    selectProject: async id => {
      const projects = await ipcRenderer.invoke("test:draft-projects");
      const project = projects.find(project => project.id === id);
      context = { kind: "project", project_id: id, cwd: project.path };
      return { projects, active_context: context };
    },
    initialize: async () => {
      await ipcRenderer.invoke("test:draft-initialize");
      const initialized = await api.initialize();
      return { ...initialized, providers: initialized.providers.map(provider => ({ ...provider, api_key_configured: true })) };
    },
    listThreads: async () => ({ threads: await threads() }),
    resumeThread: async id => ({ thread: (await threads()).find(thread => thread.id === id) }),
    startThread: async () => ({ thread: await ipcRenderer.invoke("test:draft-create", { ...(await api.listThreads()).threads[0], cwd: context.cwd }) }),
    startTurn: async (threadID, text) => {
      const failure = await ipcRenderer.invoke("test:draft-submit", { threadID, text });
      if (failure) throw new Error(failure);
      return { turn: { id: "draft-accepted", status: "completed", items_view: "full",
        items: [{ id: "draft-accepted-user", type: "user_message", status: "completed", text }] } };
    },
  };
};
