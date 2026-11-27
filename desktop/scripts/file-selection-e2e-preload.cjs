const withDefaultShell = require("./default-shell-e2e.cjs");
const { contextBridge } = require("electron");
const path = require("node:path");

// This bridge is entirely in memory: no core process, credentials, or provider.
const cwd = process.env.WUU_FILE_SELECTION_E2E_CWD || process.cwd();
const now = new Date().toISOString();
const project = { id: "file-selection-workspace", name: "File selection workspace", path: cwd, created_at: now, updated_at: now };
const files = {
  "selection-guide.md": "# Selection guide\n\nSelect this Markdown passage for review.\n\nKeep **formatted words** beside ordinary text.\n\n```ts\nconst example = 42;\n```\n",
  "selection-code.ts": "export const selectionValue = 42;\nexport const secondValue = 7;\n",
  "selection-notes.txt": "Plain text selection for review.\nA second line stays unchanged.\n",
};
files["selection-wrapped.ts"] = [
  ...Array.from({ length: 18 }, (_, index) => `// Context line ${index + 1}`),
  `export const wrappedValue = "${"A deliberately long wrapped source segment 中文 😀 ".repeat(6)}";`,
  ...Array.from({ length: 30 }, (_, index) => `// Following line ${index + 1}`),
].join("\n");
const thread = {
  id: "file-selection-thread", preview: "File selection verification",
  model_provider: "e2e", model: "mock-selection", cwd, status: "idle",
  workspace_id: project.id, workspace_kind: "project",
  created_at: now, updated_at: now,
  turns: [{ id: "selection-initial", status: "completed", items_view: "full",
    started_at: now, completed_at: now, items: [
      { id: "selection-welcome", type: "agent_message", status: "completed", text: "Open a fixture file to review its content." },
    ] }],
};
const listeners = new Set();
const submissions = [];
const sideChats = new Map();
let sideSequence = 0;
const reads = [];
const clone = value => JSON.parse(JSON.stringify(value));
const projects = () => ({ projects: [project], active_context: { kind: "project", project_id: project.id, cwd } });
const providers = [{ name: "e2e", type: "mock", model: "mock-selection", connection_locked: true, api_key_configured: true }];
function emit(method, params) {
  const event = { kind: "notification", message: { jsonrpc: "2.0", method, params } };
  for (const listener of listeners) listener(clone(event));
}

contextBridge.exposeInMainWorld("selectionE2E", {
  snapshot: () => clone({ submissions, reads, files, thread, sideChats: [...sideChats.values()] }),
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

contextBridge.exposeInMainWorld("wuu", withDefaultShell({
  initialLanguagePreference: "en-US", initialSystemLocale: "en-US", initialMessageFlowFontSize: 14,
  listProjects: async () => projects(), selectProject: async () => projects(),
  initialize: async () => ({
    protocol_version: "e2e", provider: "e2e", model: "mock-selection", workspace_root: cwd, providers,
  }),
  updateRuntimeSettings: async () => ({ provider: "e2e", model: "mock-selection", providers }),
  listEngines: async () => ({ engines: [] }),
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
  resumeThread: async id => ({ thread: clone(sideChats.get(id) ?? thread) }),
  listThreads: async (_cwd, side) => ({ threads: side?.parentThreadId === thread.id
    ? [...sideChats.values()].filter(item => item.placement === "side").reverse().map(clone)
    : [clone(thread)] }),
  listAllThreads: async () => ({ threads: [thread, ...[...sideChats.values()].filter(item => !item.placement)].map(clone) }),
  forkThread: async (id, _turn, _item, _mode, _target, options) => {
    if (id !== thread.id || options?.placement !== "side" || options?.anchor !== "latest_completed") {
      throw new Error("A side chat must fork the last completed turn of its owner");
    }
    const childID = `selection-side-${++sideSequence}`;
    const chat = { ...clone(thread), id: childID, status: "idle", placement: "side", parent_id: id, forked_from_id: id,
      title: "", preview: "", inherited_through_seq: 2,
      turns: [{ ...thread.turns[0], id: `${childID}-inherited`, items: [
        { id: `${childID}-inherited-user`, type: "user_message", text: "Prior parent question", seq: 1, status: "completed" },
        { id: `${childID}-inherited-answer`, type: "agent_message", text: "Prior parent answer", seq: 2, status: "completed", terminal: true },
      ] }],
    };
    sideChats.set(childID, chat);
    return { thread: clone(chat) };
  },
  promoteThread: async id => {
    const chat = sideChats.get(id);
    if (!chat?.placement) throw new Error("Unknown attached side chat");
    delete chat.placement;
    delete chat.parent_id;
    emit("thread/started", { thread: chat });
    return { thread: clone(chat) };
  },
  deleteThread: async id => {
    if (!sideChats.delete(id)) throw new Error("Unknown attached side chat");
    emit("thread/deleted", { thread_id: id });
    return { thread_id: id };
  },
  getSessionOrganization: async () => ({ organization: { folders: [] } }),
  listArchivedThreads: async () => ({ threads: [] }),
  startTurn: async (threadId, prompt, images = [], inputFiles = [], permissionMode, activeDocument, contentParts) => {
    const target = sideChats.get(threadId) ?? (threadId === thread.id ? thread : undefined);
    if (!target) throw new Error(`Unexpected thread: ${threadId}`);
    if (target.status === "running") throw new Error("Previous fixture turn has not completed");
    const id = `selection-turn-${submissions.length + 1}`;
    submissions.push(clone({ threadId, prompt, images, files: inputFiles, permissionMode, activeDocument, contentParts }));
    const turn = { id, status: "in_progress", items_view: "full", started_at: new Date().toISOString(), items: [
      { id: `${id}-user`, type: "user_message", status: "completed", text: prompt, content_parts: contentParts, images, files: inputFiles },
    ] };
    target.turns.push(turn);
    target.status = "running";
    if (target !== thread) {
      turn.items[0].seq = 3;
      setTimeout(() => {
        turn.status = "completed";
        turn.items.push({ id: `${id}-answer`, type: "agent_message", text: "Side question answered.", status: "completed", terminal: true, seq: 4 });
        target.title = prompt;
        target.status = "idle";
        target.latest_completed_turn_id = turn.id;
        target.updated_at = new Date().toISOString();
        emit("turn/completed", { thread_id: target.id, turn });
        emit("thread/updated", { thread: target });
      }, 50);
    }
    return { turn: clone(turn) };
  },
  interruptTurn: async () => ({ ok: true }),
  respondToServerRequest: async () => undefined, rejectServerRequest: async () => undefined,
  onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  onWindowResizeState: () => () => undefined,
}));
