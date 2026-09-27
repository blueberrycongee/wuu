const { contextBridge, ipcRenderer } = require("electron");

// Only the transport is synthetic. The shipped React renderer, Selection/Range,
// composer state and argument serialization run unchanged in Chromium.
const cwd = process.env.WUU_SELECTION_E2E_CWD || process.cwd();
const now = new Date().toISOString();
const answer = "Native drag selection starts here.\n\nRepeated 😀 café 中文 target. Middle separator. Repeated 😀 café 中文 target.\n\nLast paragraph stays visible.";
function thread(id) {
  return {
    id, preview: id === "selection-main" ? "Selection main fixture" : "Selection other fixture",
    cwd, model_provider: "e2e", model: "mock-selection", status: "idle", created_at: now, updated_at: now,
    turns: [{ id: `${id}-turn`, terminal: true, status: "completed", items_view: "full", started_at: now, completed_at: now,
      items: [
        { id: `${id}-user`, type: "user_message", status: "completed", text: "Select part of the answer.", ...(id === "selection-main" ? { related_session_id: "selection-other", input_text: "Related selection fixture" } : {}) },
        { id: `${id}-answer`, type: "agent_message", status: "completed", text: answer },
      ] }],
  };
}
const threads = [thread("selection-main"), thread("selection-other")];
let settings = { git_attribution_enabled: false, ptc: { enabled: false } };
const listeners = new Set();
ipcRenderer.on("selection:server-event", (_event, event) => {
  for (const listener of listeners) listener(event);
});
const projectList = () => ({ projects: [], active_context: { kind: "no_project", cwd } });
function record(method, args) {
  ipcRenderer.send("selection:bridge-call", { method, args });
}
contextBridge.exposeInMainWorld("wuu", {
  initialize: async () => ({ protocol_version: "e2e", general_settings: settings, provider: "e2e", model: "mock-selection", workspace_root: cwd, providers: [{ name: "e2e", type: "mock", model: "mock-selection" }] }),
  listProjects: async () => projectList(), selectNoProject: async () => projectList(),
  updateGeneralSettings: async value => ({ general_settings: settings = { ...settings, ...value } }),
  getBuildInfo: async () => ({ desktop: { version: "selection-e2e", date: now } }),
  listThreads: async () => ({ threads }), listArchivedThreads: async () => ({ threads: [] }),
  startThread: async () => ({ thread: threads[0] }),
  resumeThread: async id => ({ thread: threads.find(value => value.id === id) || threads[0] }),
  loadCodexModels: async provider => ({ provider, models: [{ id: "mock-selection", name: "mock-selection" }] }),
  gitStatus: async () => ({ is_repo: false, branches: [], dirty_count: 0 }),
  listGitChanges: async () => ({ is_repo: false, root: cwd, files: [] }),
  listWorkspaceFiles: async () => ({ root: cwd, paths: [], truncated: false }),
  listWorkspaceDirectory: async () => ({ root: cwd, path: "", entries: [], truncated: false }),
  listMCPServers: async () => ({ servers: [] }),
  startTurn: async (...args) => {
    record("startTurn", args);
    return { turn: { id: `submitted-${args[8]}`, terminal: true, status: "completed", items_view: "full", started_at: now, completed_at: now,
      items: [{ id: `submitted-user-${args[8]}`, type: "user_message", status: "completed", text: args[1], content_parts: args[6] }] } };
  },
  queueTurn: async (...args) => { record("queueTurn", args); return { queued: { id: args[3], thread_id: args[0] } }; },
  steerTurn: async (...args) => { record("steerTurn", args); return { steer: { id: args[4], thread_id: args[0] } }; },
  interruptTurn: async () => ({ ok: true }),
  onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  onWindowResizeState: () => () => undefined,
});
