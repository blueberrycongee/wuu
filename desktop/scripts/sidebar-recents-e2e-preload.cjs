// Synthetic bridge for the production renderer. No accounts or model calls.
const { contextBridge, ipcRenderer } = require("electron");
const repo = process.env.WUU_RECENTS_E2E_CWD;
const at = (minute) => new Date(Date.UTC(2026, 0, 1, 12, minute)).toISOString();
const project = { id: "recent-project", name: "Atlas", path: repo, created_at: at(0), updated_at: at(0) };
const context = { kind: "project", project_id: project.id, cwd: repo };
const threads = Array.from({ length: 14 }, (_, index) => ({
  id: `recent-${index}`, title: index === 0 ? "Review navigation and keyboard access" : `Conversation ${index + 1}`,
  cwd: repo, workspace_id: project.id, workspace_kind: "project", preview: "",
  model_provider: "e2e", model: "mock", status: "idle",
  created_at: at(-100 - index), updated_at: at(-index), recency_at: at(-index),
  turns: [], turn_count: 0,
}));
threads.push({
  ...threads[0], id: "scratch", title: "Quick question", cwd: "/tmp/wuu-recents-scratch",
  workspace_id: undefined, workspace_kind: "scratch", created_at: at(-50), recency_at: at(-5),
});
for (const [id, fields] of Object.entries({
  pinned: { pinned: true }, archived: { archived: true }, child: { parent_id: "recent-0" },
  coordinator: { source: "project" }, temporary: { ephemeral: true },
})) threads.push({ ...threads[0], id, title: id, ...fields });
const listeners = new Set();
const resumed = [];
ipcRenderer.on("test:recents-event", (_event, message) => {
  const { thread_id, turn, thread } = message.params;
  const record = threads.find((item) => item.id === (thread_id ?? thread?.id));
  if (record && thread) Object.assign(record, thread);
  if (record && turn) {
    record.turns = [turn];
    record.status = turn.status === "in_progress" ? "in_progress" : "idle";
    record.updated_at = turn.completed_at ?? turn.started_at;
    if (message.method === "turn/started") record.recency_at = turn.started_at;
  }
  for (const listener of listeners) listener({ kind: "notification", workdir: record?.cwd ?? repo, message });
});
const projects = () => ({ projects: [project], active_context: context });
contextBridge.exposeInMainWorld("wuu", {
  initialThemePreference: "light",
  listProjects: async () => projects(),
  selectProject: async () => projects(),
  selectNoProject: async () => projects(),
  gitStatus: async () => ({ is_repo: true, branch: "main", branches: ["main"], dirty_count: 0 }),
  listWorkspaceFiles: async () => ({ root: repo, paths: [], truncated: false }),
  listWorkspaceDirectory: async (path = "") => ({ root: repo, path, entries: [], truncated: false }),
  initialize: async () => ({
    protocol_version: "e2e", provider: "e2e", model: "mock", workspace_root: repo,
    providers: [{ name: "e2e", type: "mock", model: "mock", connection_locked: true, api_key_configured: true }],
  }),
  resumeThread: async (id) => { resumed.push(id); return { thread: threads.find((item) => item.id === id) }; },
  listThreads: async (cwd) => ({ threads: threads.filter((item) => item.cwd === (cwd ?? repo) && !item.archived) }),
  listAllThreads: async () => ({ threads: threads.filter((item) => !item.archived) }),
  listArchivedThreads: async () => ({ threads: [] }),
  respondToServerRequest: async () => undefined,
  rejectServerRequest: async () => undefined,
  onServerEvent: (listener) => { listeners.add(listener); return () => listeners.delete(listener); },
  onWindowResizeState: () => () => undefined,
});
contextBridge.exposeInMainWorld("recentsEvidence", { resumed: () => [...resumed] });
