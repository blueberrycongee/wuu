const { contextBridge, ipcRenderer } = require("electron");

const cwd = process.env.WUU_STREAM_E2E_CWD || process.cwd();
const runtimeContext = { kind: "no_project", cwd };
let startedThreadCount = 0;
const threads = new Map();

function projectList() {
  return {
    projects: [],
    active_context: runtimeContext
  };
}

function mockThread(id, source) {
  const now = new Date().toISOString();
  return {
    id,
    preview: "",
    model_provider: "e2e",
    model: "mock-stream",
    cwd,
    status: "idle",
    ...(source ? { source } : {}),
    created_at: now,
    updated_at: now,
    turns: []
  };
}

contextBridge.exposeInMainWorld("wuu", {
  listProjects: async () => projectList(),
  createBlankProject: async () => projectList(),
  chooseProjectFolder: async () => projectList(),
  selectProject: async () => projectList(),
  selectNoProject: async () => projectList(),
  gitStatus: async () => ({
    is_repo: true,
    branch: "streaming-e2e",
    branches: ["streaming-e2e"],
    dirty_count: 0
  }),
  checkoutGitBranch: async (branch) => ({
    is_repo: true,
    branch,
    branches: [branch],
    dirty_count: 0
  }),
  listWorkspaceFiles: async () => ({
    root: cwd,
    paths: [],
    truncated: false
  }),
  listWorkspaceDirectory: async (path = "") => ({
    root: cwd,
    path: path.replace(/\\/g, "/").replace(/^\/+/, "").replace(/\/+$/, ""),
    entries: [],
    truncated: false
  }),
  readWorkspaceFile: async (path) => ({
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
    model: "mock-stream",
    workspace_root: cwd,
    providers: [{ name: "e2e", type: "mock", model: "mock-stream", connection_locked: true }]
  }),
  updateRuntimeSettings: async (provider, model) => ({
    provider,
    model,
    providers: [{ name: provider, type: "mock", model, connection_locked: true }]
  }),
  startThread: async (params = {}) => {
    startedThreadCount += 1;
    const id = startedThreadCount === 1
      ? "thread-immediate-title-e2e"
      : startedThreadCount === 2
        ? "thread-streaming-e2e"
        : `thread-started-e2e-${startedThreadCount}`;
    const thread = mockThread(id);
    threads.set(id, thread);
    return { thread };
  },
  resumeThread: async (id) => ({ thread: threads.get(id) ?? null }),
  forkThread: async () => ({ thread: null }),
  listThreads: async () => ({ threads: [] }),
  listArchivedThreads: async () => ({ threads: [] }),
  startTurn: async (threadId, text, images = []) => {
    const now = new Date().toISOString();
    return {
      turn: {
        id: `turn-${threadId}`,
        items: [
          {
            id: `user-${threadId}`,
            type: "user_message",
            status: "completed",
            text,
            images
          }
        ],
        items_view: "full",
        status: "in_progress",
        started_at: now
      }
    };
  },
  interruptTurn: async () => ({ ok: true }),
  respondToServerRequest: async () => undefined,
  rejectServerRequest: async () => undefined,
  onServerEvent: (handler) => {
    const listener = (_event, payload) => {
      const thread = payload.message?.params?.thread;
      if (thread) threads.set(thread.id, thread);
      handler(payload);
    };
    ipcRenderer.on("test:server-event", listener);
    return () => ipcRenderer.removeListener("test:server-event", listener);
  },
  onWindowResizeState: () => () => undefined
});
