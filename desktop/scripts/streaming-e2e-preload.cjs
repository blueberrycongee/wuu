const { contextBridge, ipcRenderer } = require("electron");

const cwd = process.env.WUU_STREAM_E2E_CWD || process.cwd();
const runtimeContext = { kind: "no_project", cwd };
let startedThreadCount = 0;
let workspacePreviewReadFailed = false;
const threads = new Map();

function projectList() {
  return {
    projects: process.env.WUU_WORKSPACE_NEW_TAB_E2E ? [{
      id: "tabbar-demo", name: "Tabbar demo", path: cwd,
      created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
    }] : [],
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

// Opt-in sidebar overflow for the scroll-fade regression using the same bridge.
const sidebarSeedTime = Date.now();
for (let index = 0; index < Number(process.env.WUU_STREAM_E2E_SIDEBAR_THREADS || 0); index++) {
  const thread = mockThread(`sidebar-fade-${index}`);
  thread.preview = `Conversation ${String(index + 1).padStart(2, "0")}: scroll boundary regression`;
  thread.created_at = thread.updated_at = new Date(sidebarSeedTime - index * 1000).toISOString();
  threads.set(thread.id, thread);
}

if (process.env.WUU_PROJECT_PANEL_E2E) {
  for (const [id, preview, source, projectID] of [
    ["project-a", "Project Alpha", "project"],
    ["project-b", "Project Beta", "project"],
    ["worker-a", "Alpha worker", "project-session", "project-a"],
    ["worker-b", "Beta worker", "project-session", "project-b"],
    ["ordinary", "Ordinary conversation"],
  ]) {
    threads.set(id, { ...mockThread(id, source), preview, project_id: projectID });
  }
}

const projectWorks = new Map(["a", "b"].map((suffix) => ["project-" + suffix, {
  id: "work-" + suffix, project_id: "project-" + suffix, version: 1, revision: 1,
  title: suffix === "a" ? "Compatible pagination with existing callers" : "Review migration behavior",
  brief: "Preserve the public API and leave database indexes unchanged.",
  acceptance: "Existing callers pass the compatibility suite.", authority: "Local edits and tests only.",
  phase: "planning", lead_id: "worker-" + suffix, workspace: "worktree", usage: [],
  lead_dispatch: { session_id: "worker-" + suffix, state: "running", delivery_state: "consumed", turn_id: "brief-turn" },
}]));

// Only the workspace-tab scenario enables these sanitized in-memory resources.
const workspaceDemoFiles = {
  "README.md": "# Tabbar demo\n\nA small workspace for navigation and reading checks.\n",
  "src/a-long-workspace-file-name-for-checking-new-page-truncation.md": [
    "# Workspace navigation notes", "",
    "Keep the current document close while moving between conversations and files.", "",
    "## Reading and navigation", "",
    "- Select a file from the workspace tree.",
    "- Keep several resources open without losing your place.",
    "- Use the tab title to return to a document after checking another view.", "",
    "## Keyboard access", "",
    "Arrow keys move between tabs. Home and End jump to the first and last resource.", "",
    "Long names stay available in the tab tooltip. Close controls keep their own space.", "",
  ].join("\n"),
  "src/navigation.ts": "export const destinations = ['conversations', 'files'];\n",
  "docs/review-notes.md": "# Review notes\n\nCheck readable labels, stable close targets, and retained document state.\n",
};
function workspaceDemoPath(path) {
  const normalized = path.replace(/\\/g, "/");
  return normalized === cwd ? "" : normalized.startsWith(cwd + "/") ? normalized.slice(cwd.length + 1) : normalized;
}

contextBridge.exposeInMainWorld("wuu", {
  projectWork: async (params) => {
    const work = projectWorks.get(params.project_id);
    if (params.operation === "list") return { project_id: params.project_id, works: work ? [work] : [], usage: [] };
    if (!work || params.revision !== work.revision) throw new Error("stale work revision");
    if (params.operation === "stop") { work.phase = "stopped"; work.revision++; }
    if (params.operation === "resume") { work.phase = "planning"; work.revision++; }
    return { ...work };
  },
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
    ...(process.env.WUU_WORKSPACE_NEW_TAB_E2E ? { extension_inventory: [{
      id: "user:workspace-e2e", name: "Workspace E2E", kind: "plugin",
      provenance: { kind: "plugin", source: "user", scope: "user" },
      state: "granted", approval_state: "granted", enabled: true, fingerprint: "workspace-e2e",
      desktop: { entry: "desktop.js" },
      contributions: { workspace_tools: [{
        id: "notes", view: "notes", title: "Workspace notes with a long descriptive title",
        description: "An installed extension exposed through the public workspace tool contract.",
      }] },
    }] } : {}),
    features: { project_agent: Boolean(process.env.WUU_PROJECT_PANEL_E2E) },
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
  ...(process.env.WUU_WORKSPACE_NEW_TAB_E2E ? {
    listWorkspaceFiles: async () => ({ root: cwd, paths: Object.keys(workspaceDemoFiles), truncated: false }),
    listWorkspaceDirectory: async (path = "") => {
      const relative = workspaceDemoPath(path).replace(/\/$/, "");
      const prefix = relative ? relative + "/" : "";
      const entries = new Map();
      for (const file of Object.keys(workspaceDemoFiles)) {
        if (!file.startsWith(prefix)) continue;
        const rest = file.slice(prefix.length);
        const name = rest.split("/")[0];
        const kind = rest.includes("/") ? "directory" : "file";
        entries.set(name, { kind, name, path: prefix + name + (kind === "directory" ? "/" : "") });
      }
      return { root: cwd, path: relative, entries: [...entries.values()], truncated: false };
    },
    readWorkspaceFile: async (path) => {
      const relative = workspaceDemoPath(path);
      if (relative === process.env.WUU_WORKSPACE_FILE_RETRY_E2E && !workspacePreviewReadFailed) {
        workspacePreviewReadFailed = true;
        throw new Error("File temporarily unavailable");
      }
      const text = workspaceDemoFiles[relative];
      if (text === undefined) throw new Error("Unknown demo file: " + relative);
      return { root: cwd, path: relative, absolute_path: cwd + "/" + relative,
        size_bytes: new TextEncoder().encode(text).length, mtime_ms: 1000,
        sha256: "a".repeat(64), binary: false, truncated: false, text };
    },
    loadPluginDesktopModule: async ({ id, fingerprint }) => ({
      id, fingerprint, digest: "a".repeat(64), url: "wuu-plugin://module/workspace-new-tab-e2e.js",
    }),
  } : {}),
  startThread: async (params = {}) => {
    if (process.env.WUU_REQUEST_LIFECYCLE_E2E) await ipcRenderer.invoke("test:request-lifecycle", "thread/start");
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
  editThreadMessage: async (threadId) => {
    await ipcRenderer.invoke("test:request-lifecycle", "thread/edit-message", { threadId });
    const thread = { ...threads.get(threadId), turns: [], status: "idle" };
    threads.set(threadId, thread);
    return { thread };
  },
  listThreads: async () => ({ threads: process.env.WUU_STREAM_E2E_SIDEBAR_THREADS || process.env.WUU_PROJECT_PANEL_E2E ? [...threads.values()] : [] }),
  listArchivedThreads: async () => ({ threads: [] }),
  queueTurn: async (threadId, text, _images, id, _files, _permission, _document, _parts, _context, hold) => {
    ipcRenderer.send("test:queued-input", { threadId, text, id, hold });
    return { queued: { id, thread_id: threadId } };
  },
  steerTurn: async (threadId, turnId, text, _images, id) => {
    ipcRenderer.send("test:queued-input", { threadId, turnId, text, id });
    return { turn_id: turnId };
  },
  startTurn: async (threadId, text, images = [], _files, _permission, _document, _parts, _context, clientId) => {
    const turnId = process.env.WUU_REQUEST_LIFECYCLE_E2E ? `turn-${clientId}` : `turn-${threadId}`;
    if (process.env.WUU_REQUEST_LIFECYCLE_E2E) await ipcRenderer.invoke("test:request-lifecycle", "turn/start", { threadId, turnId, text, clientId });
    const now = new Date().toISOString();
    return {
      turn: {
        id: turnId,
        items: [
          {
            id: `user-${threadId}`,
            type: "user_message",
            status: "completed",
            text,
            source_id: clientId,
            images
          }
        ],
        items_view: "full",
        status: "in_progress",
        started_at: now
      }
    };
  },
  interruptTurn: async () => {
    if (process.env.WUU_REQUEST_LIFECYCLE_E2E) await ipcRenderer.invoke("test:request-lifecycle", "turn/interrupt");
    return { ok: true };
  },
  ...(process.env.WUU_REQUEST_LIFECYCLE_E2E ? {
    listUserQuestions: async () => ({ questions: [] }),
    answerUserQuestion: (requestId, answer) => ipcRenderer.invoke("test:request-lifecycle", "user-question/respond", { requestId, answer }),
    cancelUserQuestion: (requestId) => ipcRenderer.invoke("test:request-lifecycle", "user-question/cancel", { requestId }),
    holdUserQuestion: (requestId) => ipcRenderer.invoke("test:request-lifecycle", "user-question/hold", { requestId }),
  } : {}),
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
