// Test-only backend for the production renderer. The plugin and host are real;
// all conversations and model replies are synthetic and make no network calls.
const { ipcRenderer } = require("electron");

module.exports = (api) => {
  const fixture = ipcRenderer.sendSync("test:next-message-fixture");
  let context = fixture.context;
  let enabled = true;
  let generation = 1;
  const listeners = new Set();
  const threads = fixture.threads;
  const inventory = () => enabled ? [{
    ...fixture.plugin,
    id: "builtin:next-message", name: "Next message", kind: "plugin",
    provenance: { kind: "plugin", source: "builtin", scope: "user", plugin_id: "next-message" },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    fingerprint: `next-message-e2e-${generation}`, desktop: { entry: "desktop.js" },
  }] : [];
  const projects = () => ({ projects: fixture.projects, active_project_id: context.project_id, active_context: context });
  ipcRenderer.on("test:server-event", (_event, event) => {
    const { method, params } = event.message ?? {};
    const thread = threads.find(item => item.id === params?.thread_id && item.cwd === event.workdir);
    if (thread && (method === "turn/started" || method === "turn/completed")) {
      const index = thread.turns.findIndex(item => item.id === params.turn.id);
      if (index < 0) thread.turns.push(params.turn); else thread.turns[index] = params.turn;
      thread.status = method === "turn/started" ? "running" : "idle";
    }
  });
  ipcRenderer.on("test:next-message-inventory", (_event, change) => {
    enabled = change.enabled;
    generation++;
    for (const listener of listeners) listener({ kind: "notification", workdir: context.cwd,
      message: { method: "plugin/inventory/changed", params: { epoch: generation, extension_inventory: inventory(), skills: [] } } });
  });
  return {
    ...api,
    initialThemePreference: "light",
    initialize: async () => ({ ...await api.initialize(),
      workspace_root: context.cwd, provider: "synthetic", model: "mock-next-message",
      model_aliases: { "synthetic-fast-model": { provider: "synthetic", model: "mock-next-message" } },
      providers: [{ name: "synthetic", type: "mock", model: "mock-next-message", api_key_configured: true,
        models: [{ id: "mock-next-message", capabilities: { context_window: 272000 }, variants: [{ id: "low" }, { id: "high" }] }] }],
      extension_inventory: [...(await api.initialize()).extension_inventory, ...inventory()],
    }),
    listProjects: async () => projects(),
    selectProject: async (id) => {
      const project = fixture.projects.find(item => item.id === id);
      context = { kind: "project", project_id: id, cwd: project.path };
      return projects();
    },
    listThreads: async (cwd) => ({ threads: threads.filter(item => !cwd || item.cwd === cwd) }),
    resumeThread: async (id) => ({ thread: threads.find(item => item.id === id && (id !== "next-message-thread" || item.cwd === context.cwd)) }),
    startThread: async () => ({ thread: threads.find(item => item.cwd === context.cwd) }),
    startTurn: async (threadId, text) => {
      ipcRenderer.send("test:next-message-submitted", { threadId, text });
      const turn = { id: `submitted-${Date.now()}`, kind: "user", status: "in_progress", items_view: "full",
        started_at: new Date().toISOString(), items: [{ id: "submitted-user", type: "user_message", status: "completed", text }] };
      threads.find(item => item.id === threadId)?.turns.push(turn);
      return { turn };
    },
    openSideThread: async () => ({ summary: { side_thread_id: "next-message-side", main_thread_id: "next-message-thread", status: "idle", revision: 1, created_at: new Date().toISOString(), updated_at: new Date().toISOString() } }),
    getSideThreadHistory: async () => ({ summary: { side_thread_id: "next-message-side", main_thread_id: "next-message-thread", status: "idle", revision: 1 },
      messages: [{ id: "synthetic-side-answer", side_thread_id: "next-message-side", role: "assistant", text: "Synthetic side conversation", status: "completed", created_at: new Date().toISOString() }] }),
    getPluginConflictPreferences: async () => ({}),
    requestPluginRuntime: (params) => ipcRenderer.invoke("test:next-message-runtime", params),
    readPluginVisibleHistory: async (request) => ({ turns: request.kind === "main"
      ? threads.find(item => item.id === request.threadId && item.cwd === request.workspaceRoot)?.turns
        .filter(turn => turn.id !== request.beforeTurnId && turn.status === "completed")
        .map(turn => ({ id: turn.id, userText: turn.items.find(item => item.type === "user_message")?.text ?? "",
          assistantText: turn.items.find(item => item.type === "agent_message" && item.terminal)?.text ?? "",
          imageCount: 0, fileCount: 0 })).filter(turn => turn.userText.trim() && turn.assistantText.trim()).slice(-20) ?? []
      : [] }),
    cancelPluginRuntime: (params) => ipcRenderer.invoke("test:next-message-cancel", params),
    loadPluginDesktopModule: async ({ id, fingerprint }) => ({
      id, fingerprint, digest: fixture.pluginDigest,
      url: `wuu-plugin://next-message/${fingerprint}/desktop.js`,
    }),
    onServerEvent: (listener) => {
      listeners.add(listener);
      const dispose = api.onServerEvent(listener);
      return () => { listeners.delete(listener); dispose(); };
    },
  };
};
