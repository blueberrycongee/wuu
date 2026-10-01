const { contextBridge, ipcRenderer } = require("electron");

const cwd = process.env.WUU_RESIZE_E2E_CWD || process.cwd();
const runtimeContext = { kind: "no_project", cwd };
const liveLayout = process.env.WUU_RESIZE_LIVE_E2E === "1";
let generalSettings = { git_attribution_enabled: true, ptc: { enabled: true } };
const now = new Date().toISOString();
const terminalListeners = new Set();
let terminalSessionCounter = 1;
const terminalSessions = new Map();
const workspacePaths = Array.from({ length: 220 }, (_value, index) => {
  const file = String(index).padStart(3, "0");
  return `resize-fixture-${file}.ts`;
});
const heavyAnswer = Array.from({ length: 12 }, (_value, index) => {
  const item = index + 1;
  return [
    `### Section ${item}`,
    "This resize fixture intentionally keeps a dense message flow visible while the BrowserWindow width changes.",
    "The line contains enough words to force browser line wrapping and markdown layout work on every horizontal resize step.",
    "",
    "| path | status | notes |",
    "| --- | --- | --- |",
    `| desktop/src/renderer/App.tsx | changed | resize measurement row ${item} |`,
    `| desktop/src/renderer/styles.css | changed | layout wrapping row ${item} |`,
    "",
    "```ts",
    `export const resizeFixture${item} = { width: \"responsive\", stable: true };`,
    "```"
  ].join("\n");
}).join("\n\n");
const turns = Array.from({ length: 36 }, (_value, index) => ({
  id: `resize-turn-${index}`,
  status: "completed",
  items_view: "full",
  started_at: now,
  completed_at: now,
  duration_ms: 42 + index,
  items: [
    {
      id: `resize-user-${index}`,
      type: "user_message",
      text: `Resize checkpoint ${index + 1}: keep right-edge live resize aligned with this message flow.`,
      status: "completed"
    },
    {
      id: `resize-agent-${index}`,
      type: "agent_message",
      text: heavyAnswer,
      status: "completed"
    }
  ]
}));
const resizeThread = {
  id: "resize-thread",
  preview: "Resize fixture with a long active session title that must truncate before title actions",
  model_provider: "e2e",
  model: "mock-resize",
  cwd,
  status: "idle",
  created_at: now,
  updated_at: now,
  turns
};

// Exercise the same footer clearance while process output is still running.
if (process.env.WUU_HEIGHT_RUNNING_E2E === "1") {
  resizeThread.status = "running";
  turns.at(-1).status = "in_progress";
  turns.at(-1).items.at(-1).status = "in_progress";
}

// Pull-to-new-session must make room for its animation even with a live TODO
// capsule above the composer (the mobile overlap regression).
if (process.env.WUU_PULL_SESSION_E2E === "1") {
  resizeThread.status = "running";
  turns.at(-1).status = "in_progress";
  turns.at(-1).items.push({
    id: "pull-session-todo",
    type: "tool_call",
    status: "completed",
    display: { capability: "todo" },
    arguments: JSON.stringify({ todos: [{ content: "Check mobile gesture", status: "in_progress" }] }),
  });
}

// A related-session message exercises the same split navigation used by plugins.
turns.at(-1).items[0].related_session_id = "resize-related-thread";
turns.at(-1).items[0].input_text = "Related session completed its resize check.";

const sideSummary = {
  side_thread_id: "resize-side-thread",
  main_thread_id: resizeThread.id,
  status: "idle",
  revision: 1,
  created_at: now,
  updated_at: now
};

function projectList() {
  return {
    projects: [],
    active_context: runtimeContext
  };
}

// Keep synthetic snapshots consistent with notifications, including a later resume.
// The handler runs once, before renderer subscribers receive each notification.
if (liveLayout) {
  ipcRenderer.on("test:server-event", (_event, payload) => {
    const { method, params } = payload.message || {};
    if (params?.thread_id !== resizeThread.id) return;
    if (method === "turn/started" || method === "turn/completed") {
      const index = turns.findIndex(turn => turn.id === params.turn.id);
      if (index < 0) turns.push(params.turn);
      else turns[index] = params.turn;
      resizeThread.status = method === "turn/started" ? "running" : "idle";
      return;
    }
    const turn = turns.find(turn => turn.id === params.turn_id);
    if (turn && method === "item/started") {
      if (!turn.items.some(item => item.id === params.item.id)) turn.items.push(params.item);
    } else if (turn && method === "item/agentMessage/delta") {
      const item = turn.items.find(item => item.id === params.item_id);
      if (item) item.text += params.delta;
    }
  });
}
let submittedTurns = 0;

contextBridge.exposeInMainWorld("wuu", {
  listProjects: async () => projectList(),
  createBlankProject: async () => projectList(),
  chooseProjectFolder: async () => projectList(),
  selectProject: async () => projectList(),
  selectNoProject: async () => projectList(),
  gitStatus: async () => ({
    is_repo: true,
    branch: "resize-e2e",
    branches: ["resize-e2e"],
    dirty_count: 0,
    diff: { files: 0, additions: 0, deletions: 0 },
    staged_diff: { files: 0, additions: 0, deletions: 0 }
  }),
  listGitChanges: async () => ({
    is_repo: true,
    root: cwd,
    files: []
  }),
  readGitFileDiff: async (path) => ({
    is_repo: true,
    path,
    status: "unknown",
    additions: 0,
    deletions: 0,
    patch: "",
    truncated: false
  }),
  checkoutGitBranch: async (branch) => ({
    is_repo: true,
    branch,
    branches: [branch],
    dirty_count: 0
  }),
  createCheckoutGitBranch: async (branch) => ({
    is_repo: true,
    branch,
    branches: [branch],
    dirty_count: 0,
    created: true
  }),
  commitGitChanges: async () => ({ ok: true, committed: false }),
  createPullRequest: async () => ({ url: "", already_exists: false }),
  listWorkspaceFiles: async () => ({
    root: cwd,
    paths: workspacePaths,
    truncated: false
  }),
  getSettingsUsage: async () => ({
    total_sessions: 0,
    generated_at: now,
    metrics: {
      prompt_tokens: 0,
      context_tokens: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
      date_range: [now.slice(0, 10), now.slice(0, 10)],
      active_days: 0
    },
    model_breakdowns: [],
    days: [],
    entries: []
  }),
  listMCPServers: async () => ({ servers: [] }),
  listWorkspaceDirectory: async (path = "") => ({
    root: cwd,
    path: path.replace(/\\/g, "/").replace(/^\/+/, "").replace(/\/+$/, ""),
    entries: path
      ? []
      : workspacePaths.map((filePath) => ({
          name: filePath,
          path: filePath,
          kind: "file"
        })),
    truncated: false
  }),
  readWorkspaceFile: async (path) => ({
    root: cwd,
    path,
    absolute_path: path,
    size_bytes: 32,
    binary: false,
    truncated: false,
    text: `export const path = ${JSON.stringify(path)};\n`
  }),
  startTerminalSession: async () => {
    const id = `mock-terminal-${terminalSessionCounter++}`;
    terminalSessions.set(id, { input: "" });
    queueMicrotask(() => {
      for (const listener of terminalListeners) {
        listener({ type: "data", id, text: "~/wuu resize-e2e $ " });
      }
    });
    return {
      id,
      cwd,
      shell: "/bin/mock",
      started_at: new Date().toISOString()
    };
  },
  writeTerminalSession: async (id, data) => {
    const session = terminalSessions.get(id);
    if (!session) {
      return { ok: false };
    }
    if (data === "\r") {
      const command = session.input.trim();
      session.input = "";
      for (const listener of terminalListeners) {
        listener({ type: "data", id, text: `\r\nmock terminal output: ${command}\r\n~/wuu resize-e2e $ ` });
      }
      return { ok: true };
    }
    if (data === "\u007f") {
      session.input = session.input.slice(0, -1);
      for (const listener of terminalListeners) {
        listener({ type: "data", id, text: "\b \b" });
      }
      return { ok: true };
    }
    session.input += data;
    for (const listener of terminalListeners) {
      listener({ type: "data", id, text: data });
    }
    return { ok: true };
  },
  resizeTerminalSession: async (id) => ({ ok: terminalSessions.has(id) }),
  stopTerminalSession: async (id) => {
    const existed = terminalSessions.delete(id);
    return { ok: existed };
  },
  initialize: async () => ({
    protocol_version: "e2e",
    features: process.argv.includes("--project-agent-e2e-enabled")
      ? { project_agent: true }
      : process.argv.includes("--project-agent-e2e-disabled") ? { project_agent: false } : undefined,
    general_settings: generalSettings,
    provider: "e2e",
    model: "mock-resize",
    workspace_root: cwd,
    providers: [{ name: "e2e", type: "mock", model: "mock-resize" }]
  }),
  updateGeneralSettings: async settings => {
    generalSettings = { ...generalSettings, ...settings };
    return { general_settings: generalSettings };
  },
  getBuildInfo: async () => ({
    core: undefined,
    desktop: { version: "resize-e2e", date: "1970-01-01T00:00:00Z" }
  }),
  updateRuntimeSettings: async (provider, model) => ({
    provider,
    model,
    providers: [{ name: provider, type: "mock", model }]
  }),
  loadCodexModels: async (provider) => ({
    provider,
    models: [{ id: "mock-resize", name: "mock-resize" }]
  }),
  startThread: async () => ({ thread: resizeThread }),
  resumeThread: async (id) => ({
    thread: id === "resize-related-thread"
      ? { ...resizeThread, id, turns: turns.slice(-2) }
      : resizeThread
  }),
  listThreads: async () => ({ threads: [resizeThread] }),
  listArchivedThreads: async () => ({ threads: [] }),
  openSideThread: async () => ({ summary: sideSummary }),
  getSideThreadHistory: async () => ({
    summary: sideSummary,
    messages: [{
      id: "resize-side-answer",
      side_thread_id: sideSummary.side_thread_id,
      role: "assistant",
      text: heavyAnswer,
      status: "completed",
      created_at: now
    }]
  }),
  sendSideThreadMessage: async () => { throw new Error("Unexpected side-thread send in layout fixture"); },
  interruptSideThread: async () => ({ ok: true }),
  resetSideThread: async () => ({ ok: true }),
  onSideThreadEvent: () => () => undefined,
  pinThread: async (_id, pinned) => ({ pinned }),
  archiveThread: async () => ({ ok: true }),
  startTurn: async (_threadID, text, images = [], _files, _permission, _document, _parts, _context, clientID) => {
    if (!liveLayout) return { turn: null };
    submittedTurns += 1;
    const turn = {
      id: `resize-submitted-turn-${submittedTurns}`,
      status: "in_progress",
      items_view: "full",
      started_at: new Date().toISOString(),
      items: [{ id: `resize-submitted-user-${submittedTurns}`, type: "user_message", status: "completed", text, images, source_id: clientID }]
    };
    turns.push(turn);
    resizeThread.status = "running";
    return { turn };
  },
  interruptTurn: async () => ({ ok: true }),
  respondToServerRequest: async () => undefined,
  rejectServerRequest: async () => undefined,
  onServerEvent: liveLayout ? handler => {
    const listener = (_event, payload) => handler(payload);
    ipcRenderer.on("test:server-event", listener);
    return () => ipcRenderer.removeListener("test:server-event", listener);
  } : () => () => undefined,
  // These report renderer host geometry only; no embedded browser is launched.
  ...(liveLayout ? {
    browserSurface: async (workdir, tabID) => ({ workdir, tabID, url: "https://resize.invalid/local-fixture", title: "Local responsive fixture", loading: false, canGoBack: false, canGoForward: false }),
    reportBrowserBounds: (workdir, tabID, rect) => ipcRenderer.send("test:browser-bounds", { workdir, tabID, rect }),
    reportBrowserPiPHostLayout: payload => ipcRenderer.send("test:pip-host", payload),
    onBrowserSurface: () => () => undefined,
    onBrowserInvalidate: () => () => undefined,
    suppressBrowserOverlay: () => undefined
  } : {}),
  onTerminalEvent: (handler) => {
    terminalListeners.add(handler);
    return () => terminalListeners.delete(handler);
  },
  onWindowResizeState: () => () => undefined
});
