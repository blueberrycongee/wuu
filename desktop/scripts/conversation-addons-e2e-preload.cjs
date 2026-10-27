// Reuse the default-product fixture; only history and external inventory differ.
const { ipcRenderer } = require("electron");
module.exports = api => {
  const cwd = process.env.WUU_RESIZE_E2E_CWD;
  const now = new Date().toISOString();
  // Enough real paragraphs above and below the reading point to avoid either
  // endpoint clamping masquerading as reading-anchor preservation.
  const answer = id => Array.from({ length: id === "single" ? 32 : 24 }, (_, index) =>
    `Native answer ${id}, paragraph ${index + 1}. This deterministic history keeps a reader away from the latest output while independently installed annotations change the preceding turn's height.`).join("\n\n");
  const turn = (id, count) => ({ id, status: "completed", items_view: "full", duration_ms: 1200,
    input_tokens: 8, output_tokens: 0, started_at: now, completed_at: now,
    items: [{ id: `${id}-user`, type: "user_message", text: `Inspect ${count} reported tool results.`, status: "completed" },
      ...Array.from({ length: count }, (_, index) => ({ id: `${id}-tool-${index}`, type: "tool_call", name: "exec",
        display: { capability: "command.bash" }, arguments: '{"command":"echo reported-result"}', result: `Reported result ${index}`, status: "completed" })),
      { id: `${id}-answer`, type: "agent_message", terminal: true, text: answer(id), status: "completed" }],
  });
  const thread = { id: "resize-thread", title: "Conversation addon acceptance", preview: "Conversation addon acceptance",
    cwd, model_provider: "e2e", model: "mock-resize", status: "idle", created_at: now, updated_at: now,
    turns: [turn("single", 1), turn("multiple", 2)] };
  const sideSummary = { side_thread_id: "addon-side-thread", main_thread_id: thread.id,
    status: "idle", revision: 1, created_at: now, updated_at: now };
  let enabled = [], epoch = 0, baseInventory = [];
  const listeners = new Set();
  const inventory = () => enabled.map(name => ({ id: `user:conversation-addon-${name}`, kind: "plugin", name: `Conversation addon ${name}`,
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: `conversation-addon-${name}` },
    state: "granted", approval_state: "granted", enabled: true, runtime_state: "active",
    graph_generation: "default-shell-e2e-graph", fingerprint: `conversation-addon-${name}`, desktop: { entry: "desktop.js" } }));
  ipcRenderer.on("test:conversation-addons", (_event, value) => {
    enabled = value;
    for (const listener of listeners) listener({ kind: "notification", workdir: cwd,
      message: { method: "plugin/inventory/changed", params: { epoch: ++epoch, extension_inventory: [...baseInventory, ...inventory()], skills: [] } } });
  });
  return { ...api,
    initialize: async () => { const initial = await api.initialize(); baseInventory = initial.extension_inventory; return { ...initial, extension_inventory: [...baseInventory, ...inventory()] }; },
    startThread: async () => ({ thread }), resumeThread: async () => ({ thread }),
    listThreads: async () => ({ threads: [thread] }), listAllThreads: async () => ({ threads: [thread] }),
    openSideThread: async () => ({ summary: sideSummary }),
    getSideThreadHistory: async () => ({ summary: sideSummary, messages: [{
      id: "addon-side-answer", side_thread_id: sideSummary.side_thread_id, role: "assistant", status: "completed", created_at: now,
      text: "The preview owns this independent conversation.", items: [
        { id: "addon-side-tool", type: "tool_call", name: "exec", display: { capability: "command.bash" },
          arguments: '{"command":"echo preview-result"}', result: "Reported preview result", status: "completed" },
        { id: "addon-side-message", type: "agent_message", terminal: true, text: "The preview owns this independent conversation.", status: "completed" },
      ],
    }] }),
    loadPluginDesktopModule: async request => !request.id.startsWith("user:conversation-addon-") ? api.loadPluginDesktopModule(request)
      : { ...request, digest: "0".repeat(64), url: `wuu-plugin://${request.id.slice("user:conversation-addon-".length)}/desktop.js` },
    onServerEvent: listener => { listeners.add(listener); const off = api.onServerEvent?.(listener); return () => { listeners.delete(listener); off?.(); }; },
  };
};
