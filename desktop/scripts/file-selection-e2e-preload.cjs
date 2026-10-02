const { contextBridge } = require("electron");
const path = require("node:path");

// This bridge is entirely in memory: no core process, credentials, or provider.
const cwd = process.env.WUU_FILE_SELECTION_E2E_CWD || process.cwd();
const now = new Date().toISOString();
const files = {
  "selection-guide.md": "# Selection guide\n\nSelect this Markdown passage for review.\n\nKeep **formatted words** beside ordinary text.\n\n```ts\nconst example = 42;\n```\n",
  "selection-code.ts": "export const selectionValue = 42;\nexport const secondValue = 7;\n",
  "selection-notes.txt": "Plain text selection for review.\nA second line stays unchanged.\n",
};
const thread = {
  id: "file-selection-thread", preview: "File selection verification",
  model_provider: "e2e", model: "mock-selection", cwd, status: "idle",
  created_at: now, updated_at: now,
  turns: [{ id: "selection-initial", status: "completed", items_view: "full",
    started_at: now, completed_at: now, items: [
      { id: "selection-welcome", type: "agent_message", status: "completed", text: "Open a fixture file to review its content." },
    ] }],
};
const listeners = new Set();
const submissions = [];
const reads = [];
const clone = value => JSON.parse(JSON.stringify(value));
const projects = () => ({ projects: [], active_context: { kind: "no_project", cwd } });
const providers = [{ name: "e2e", type: "mock", model: "mock-selection", connection_locked: true }];
function emit(method, params) {
  const event = { kind: "notification", message: { jsonrpc: "2.0", method, params } };
  for (const listener of listeners) listener(clone(event));
}

contextBridge.exposeInMainWorld("selectionE2E", {
  snapshot: () => clone({ submissions, reads, files, thread }),
  complete: (changes = {}) => {
    for (const [name, text] of Object.entries(changes)) {
      if (!(name in files) || typeof text !== "string") throw new Error("Invalid fixture update");
      files[name] = text;
    }
    const turn = thread.turns.at(-1);
    if (turn.status !== "in_progress") throw new Error("No fixture turn is running");
    turn.status = "completed";
    turn.completed_at = new Date().toISOString();
    turn.items.push({ id: `${turn.id}-answer`, type: "agent_message", status: "completed", text: "Fixture request completed." });
    thread.status = "idle";
    emit("turn/completed", { thread_id: thread.id, turn });
  },
});

contextBridge.exposeInMainWorld("wuu", {
  initialLanguagePreference: "en-US", initialSystemLocale: "en-US", initialMessageFlowFontSize: 14,
  listProjects: async () => projects(), selectNoProject: async () => projects(),
  initialize: async () => ({ protocol_version: "e2e", provider: "e2e", model: "mock-selection", workspace_root: cwd, providers }),
  updateRuntimeSettings: async () => ({ provider: "e2e", model: "mock-selection", providers }),
  loadCodexModels: async () => ({ provider: "e2e", models: [{ id: "mock-selection", name: "mock-selection" }] }),
  getBuildInfo: async () => ({ desktop: { version: "selection-e2e", date: now } }),
  gitStatus: async () => ({ is_repo: true, branch: "selection-e2e", branches: ["selection-e2e"], dirty_count: 0,
    diff: { files: 0, additions: 0, deletions: 0 }, staged_diff: { files: 0, additions: 0, deletions: 0 } }),
  listGitChanges: async () => ({ is_repo: true, root: cwd, files: [] }),
  listMCPServers: async () => ({ servers: [] }),
  listWorkspaceFiles: async () => ({ root: cwd, paths: Object.keys(files), truncated: false }),
  listWorkspaceDirectory: async (directory = "") => ({ root: cwd, path: directory, truncated: false,
    entries: directory ? [] : Object.keys(files).map(name => ({ name, path: name, kind: "file" })) }),
  readWorkspaceFile: async name => {
    name = path.isAbsolute(name) ? path.relative(cwd, name) : name;
    if (!(name in files)) throw new Error(`Unknown fixture file: ${name}`);
    reads.push({ path: name, text: files[name] });
    return { root: cwd, path: name, absolute_path: path.join(cwd, name), size_bytes: Buffer.byteLength(files[name]),
      binary: false, truncated: false, text: files[name] };
  },
  startThread: async () => ({ thread: clone(thread) }),
  resumeThread: async () => ({ thread: clone(thread) }),
  listThreads: async () => ({ threads: [clone(thread)] }),
  listArchivedThreads: async () => ({ threads: [] }),
  startTurn: async (threadId, prompt, images = [], inputFiles = [], permissionMode, activeDocument, contentParts) => {
    if (threadId !== thread.id) throw new Error(`Unexpected thread: ${threadId}`);
    if (thread.status === "running") throw new Error("Previous fixture turn has not completed");
    const id = `selection-turn-${submissions.length + 1}`;
    submissions.push(clone({ threadId, prompt, images, files: inputFiles, permissionMode, activeDocument, contentParts }));
    const turn = { id, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [
      { id: `${id}-user`, type: "user_message", status: "completed", text: prompt, content_parts: contentParts, images, files: inputFiles },
    ] };
    thread.turns.push(turn);
    thread.status = "running";
    return { turn: clone(turn) };
  },
  // Selection-chip capture uses a lazy, empty side thread and never runs inference.
  openSideThread: async mainThreadId => {
    if (mainThreadId !== thread.id) throw new Error(`Unexpected side owner: ${mainThreadId}`);
    return { summary: null };
  },
  getSideThreadHistory: async () => null,
  sendSideThreadMessage: async () => { throw new Error("Side submission is outside this visual fixture"); },
  interruptSideThread: async () => ({ ok: true }),
  resetSideThread: async () => ({ ok: true }),
  onSideThreadEvent: () => () => undefined,
  interruptTurn: async () => ({ ok: true }),
  respondToServerRequest: async () => undefined, rejectServerRequest: async () => undefined,
  onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  onWindowResizeState: () => () => undefined,
});
