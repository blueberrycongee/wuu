// Synthetic desktop bridge for sidebar-hover-card-e2e.cjs: two workspaces and
// sessions in every state a hover card describes. No user data, no core.
const { contextBridge } = require("electron");

const repo = process.env.WUU_HOVER_E2E_CWD || process.cwd();
const now = Date.now();
const minutes = (count) => new Date(now - count * 60_000).toISOString();

const workspace = { id: "ws-wuu", name: "wuu", path: repo, created_at: minutes(1e4), updated_at: minutes(1e4) };
const missing = {
  id: "ws-missing", name: "old-prototype", path: "/nonexistent/old-prototype",
  created_at: minutes(2e4), updated_at: minutes(2e4), missing: true,
};
const runtimeContext = { kind: "project", project_id: workspace.id, cwd: repo };

function thread(id, fields = {}) {
  const at = fields.at ?? 30;
  const status = fields.turnStatus ?? "completed";
  return {
    id,
    preview: "",
    title: fields.title ?? id,
    model_provider: "e2e",
    model: fields.model ?? "mock-model",
    engine_id: fields.engine,
    cwd: fields.cwd ?? repo,
    workspace_id: fields.cwd ? undefined : workspace.id,
    workspace_kind: fields.cwd ? "scratch" : "project",
    status: status === "in_progress" ? "in_progress" : "idle",
    source: fields.source,
    project_id: fields.projectID,
    forked_from_id: fields.forkedFrom,
    worktree: fields.worktree ? { path: `${repo}/.wuu/worktrees/${id}` } : undefined,
    created_at: minutes(at + 5),
    updated_at: minutes(at),
    turns: [{
      id: `${id}-turn`,
      status,
      started_at: minutes(at + (status === "in_progress" ? 0 : 2)),
      completed_at: status === "in_progress" ? null : minutes(at),
      items: [],
      items_view: "full",
    }],
  };
}

const threads = [
  thread("hover-running", {
    title: "Review the changes in PR 499", turnStatus: "in_progress", at: 3,
    engine: "codex", model: "gpt-5.1-codex",
  }),
  thread("hover-unread", {
    title: "Tighten sidebar row density", at: 125, engine: "claude", model: "claude-opus-4-5",
  }),
  thread("hover-failed", {
    title: "Refactor the extension host lifecycle so a plugin reload never strands background tasks, then document the recovery contract for community extensions",
    turnStatus: "failed", at: 26 * 60, engine: "wuu", model: "claude-sonnet-5", worktree: true,
  }),
  thread("hover-fork", { title: "Tighten sidebar row density", at: 90, forkedFrom: "hover-unread", model: "claude-opus-4-5", engine: "claude" }),
  thread("hover-project", { title: "Release checklist", source: "project", at: 40 }),
  thread("hover-project-a", { title: "Draft notes", source: "project-session", projectID: "hover-project", turnStatus: "in_progress", at: 1 }),
  thread("hover-project-b", { title: "Verify builds", source: "project-session", projectID: "hover-project", at: 50 }),
  ...Array.from({ length: 10 }, (_, index) => thread(`hover-filler-${index}`, {
    title: `Older conversation ${index + 1}`, at: 3 * 24 * 60 + index * 60,
  })),
  thread("hover-scratch", { title: "Quick question", cwd: "/tmp/wuu-scratch", at: 15 }),
];

const projectList = () => ({ projects: [workspace, missing], active_context: runtimeContext });
const threadsIn = (path) => threads.filter((item) => item.cwd === path);

contextBridge.exposeInMainWorld("wuu", {
  listProjects: async () => projectList(),
  selectProject: async () => projectList(),
  selectNoProject: async () => projectList(),
  gitStatus: async () => ({ is_repo: true, branch: "main", branches: ["main"], dirty_count: 0 }),
  listWorkspaceFiles: async () => ({ root: repo, paths: [], truncated: false }),
  listWorkspaceDirectory: async (path = "") => ({ root: repo, path, entries: [], truncated: false }),
  initialize: async () => ({
    protocol_version: "e2e", provider: "e2e", model: "mock-model", workspace_root: repo,
    providers: [{ name: "e2e", type: "mock", model: "mock-model", connection_locked: true }],
  }),
  resumeThread: async (id) => ({ thread: threads.find((item) => item.id === id) ?? null }),
  listThreads: async (path) => ({ threads: threadsIn(path ?? repo) }),
  listAllThreads: async () => ({ threads }),
  listArchivedThreads: async () => ({ threads: [] }),
  respondToServerRequest: async () => undefined,
  rejectServerRequest: async () => undefined,
  onServerEvent: () => () => undefined,
  onWindowResizeState: () => () => undefined,
});
