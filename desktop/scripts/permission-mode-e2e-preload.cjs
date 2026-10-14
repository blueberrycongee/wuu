const { contextBridge, ipcRenderer } = require("electron");

const cwd = process.env.WUU_PERMISSION_E2E_CWD || process.cwd();
const runtimeContext = { kind: "no_project", cwd };
let currentToolPolicy = { profile: "standard" };
let currentPermissions = permissionPreset("standard");
const updateCalls = [];

function provider(provider = "e2e", model = "mock-permission") {
  return { name: provider, type: "mock", model };
}

function permissionPreset(mode) {
  switch (mode) {
    case "read_only":
      return {
        mode: "read_only",
        permission_profile: "read_only",
        approval_policy: "on_request",
        approvals_reviewer: "user"
      };
    case "unconfined":
      return {
        mode: "unconfined",
        permission_profile: "danger_full_access",
        approval_policy: "never",
        approvals_reviewer: "user"
      };
    case "standard":
    default:
      return {
        mode: "standard",
        permission_profile: "workspace_write",
        approval_policy: "on_request",
        approvals_reviewer: "user"
      };
  }
}

function projectList() {
  return {
    projects: [],
    active_context: runtimeContext
  };
}

contextBridge.exposeInMainWorld("permissionE2E", {
  state: () => ({
    currentToolPolicy,
    updateCalls: [...updateCalls]
  })
});

contextBridge.exposeInMainWorld("wuu", {
  listProjects: async () => projectList(),
  createBlankProject: async () => projectList(),
  chooseProjectFolder: async () => projectList(),
  selectProject: async () => projectList(),
  selectNoProject: async () => projectList(),
  gitStatus: async () => ({
    is_repo: true,
    branch: "permission-e2e",
    branches: ["permission-e2e"],
    dirty_count: 0
  }),
  checkoutGitBranch: async (branch) => ({
    is_repo: true,
    branch,
    branches: [branch],
    dirty_count: 0
  }),
  createCheckoutGitBranch: async (branch) => ({
    status: {
      is_repo: true,
      branch,
      branches: [branch],
      dirty_count: 0
    }
  }),
  commitGitChanges: async () => ({ ok: true }),
  createPullRequest: async () => ({ url: "https://example.test/pr/1" }),
  getThreadExecutionEnvironment: async thread_id => ({ thread_id, environment: { state: "local", root: cwd } }),
  listWorkspaceFiles: async () => ({
    root: cwd,
    paths: [],
    truncated: false
  }),
  listWorkspaceDirectory: async (path = "", _root, target) => ({
    target: target && { thread_id: target.thread_id, environment_id: "local" },
    root: cwd,
    path: path.replace(/\\/g, "/").replace(/^\/+/, "").replace(/\/+$/, ""),
    entries: [],
    truncated: false
  }),
  readWorkspaceFile: async (path, _root, target) => ({
    target: target && { thread_id: target.thread_id, environment_id: "local" },
    root: cwd,
    path,
    absolute_path: path,
    size_bytes: 0,
    binary: false,
    truncated: false,
    text: ""
  }),
  initialize: async () => ({
    protocol_version: "e2e",
    provider: "e2e",
    model: "mock-permission",
    workspace_root: cwd,
    tool_policy: currentToolPolicy,
    permissions: currentPermissions,
    providers: [provider()]
  }),
  getBuildInfo: async () => ({
    core: undefined,
    desktop: { version: "permission-e2e", date: "1970-01-01T00:00:00Z" }
  }),
  loadCodexModels: async (providerName = "e2e") => ({
    provider: providerName,
    model: "mock-permission",
    models: []
  }),
  updateRuntimeSettings: async (providerName, model, effort, connection, variant, permissionMode) => {
    updateCalls.push({
      provider: providerName,
      model,
      effort,
      connection,
      variant,
      permissionMode
    });
    if (permissionMode !== undefined) {
      currentPermissions = permissionPreset(permissionMode);
      currentToolPolicy = { profile: permissionMode };
    }
    return {
      provider: providerName,
      model,
      effort,
      variant,
      tool_policy: currentToolPolicy,
      permissions: currentPermissions,
      providers: [provider(providerName, model)]
    };
  },
  startThread: async () => ({ thread: null }),
  resumeThread: async () => ({ thread: null }),
  forkThread: async () => ({ thread: null }),
  listThreads: async () => ({ threads: [] }),
  listArchivedThreads: async () => ({ threads: [] }),
  searchThreads: async () => ({ threads: [] }),
  pinThread: async (id, pinned) => ({ thread: { id, pinned } }),
  archiveThread: async (id) => ({ thread: { id, archived: true } }),
  startTurn: async () => ({ turn: null }),
  queueTurn: async () => ({ queued: true }),
  dequeueTurn: async () => ({ queued: false }),
  steerTurn: async () => ({ ok: true }),
  unsteerTurn: async () => ({ ok: true }),
  interruptTurn: async () => ({ ok: true }),
  respondToServerRequest: async () => undefined,
  rejectServerRequest: async () => undefined,
  listSkills: async () => ({ skills: [] }),
  startTerminalSession: async () => ({ id: "terminal-e2e" }),
  writeTerminalSession: async () => ({ ok: true }),
  resizeTerminalSession: async () => ({ ok: true }),
  stopTerminalSession: async () => ({ ok: true }),
  onTerminalEvent: () => () => undefined,
  onServerEvent: (handler) => {
    const listener = (_event, payload) => handler(payload);
    ipcRenderer.on("test:server-event", listener);
    return () => ipcRenderer.removeListener("test:server-event", listener);
  },
  onWindowResizeState: () => () => undefined
});
