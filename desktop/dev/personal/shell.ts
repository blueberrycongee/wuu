/** In-memory bridge for the production desktop shell. No real accounts or writes. */
import type { WuuDesktopApi } from "../../src/shared/protocol";
import type { PluginHostOptions } from "../../src/renderer/plugins/PluginHost";

export function installFixtureBridge(invoke: NonNullable<PluginHostOptions["invokeRuntime"]>): void {
  const workspace = "/synthetic/wuu";
  const projects = { projects: [], active_context: { kind: "no_project", cwd: workspace } };
  const thread = { id: "synthetic-coding", preview: "合成 Coding 对话", title: "", model_provider: "synthetic", model: "synthetic", cwd: workspace, workspace_kind: "scratch", status: "idle", pinned: false, archived: false, created_at: "2026-10-19T00:00:00Z", updated_at: "2026-10-19T00:00:00Z", turns: [] };
  window.wuu = {
    reportPluginDesktopActivation: async () => ({accepted: true}),
    getBuildInfo: async () => ({ desktop: { version: "synthetic-fixture" } }),
    listMCPServers: async () => ({ servers: [] }),
    listEngines: async () => ({ engines: [] }),
    listProjects: async () => projects,
    selectNoProject: async () => projects,
    initialize: async () => ({ protocol_version: "wuu-app-server/v0.1", provider: "synthetic", model: "synthetic", workspace_root: workspace, permissions: { mode: "standard" }, providers: [{ name: "synthetic", type: "openai-compatible", model: "synthetic", api_key_configured: true }], extension_inventory: ["personal", "personal-calendar", "automations"].map(id => ({id, name:id, kind:"plugin", enabled:true, state:"active", approval_state:"official", fingerprint:"synthetic-ui-fixture", provenance:{kind:"plugin",source:"fixture",scope:"user",plugin_id:id}})), advanced_settings: { max_steps: 64, max_context_tokens: 0, temperature: 0, disable_auto_compact: false } }),
    listThreads: async () => ({ threads: [] }),
    listArchivedThreads: async () => ({ threads: [] }),
    resumeThread: async () => ({ thread: undefined }),
    startThread: async () => ({ thread }),
    getActiveGoalSummary: async () => null,
    gitStatus: async () => ({ is_repo: false, dirty_count: 0, files: [] }),
    listUserQuestions: async () => ({ questions: [] }),
    answerUserQuestion: async () => ({ resolved: true }),
    cancelUserQuestion: async () => ({ resolved: true }),
    onServerEvent: () => () => {},
    onWindowResizeState: () => () => {},
    onTerminalEvent: () => () => {},
    requestPluginRuntime: async (request: Parameters<WuuDesktopApi["requestPluginRuntime"]>[0]) => ({ result: await invoke({ pluginId: request.id, generation: request.fingerprint, graphGeneration:request.graph_generation, service:request.service, method: request.method, input: request.input, userSpaceId: request.user_space_id, productDomain: request.product_domain, compositionProfile: request.composition_profile, userInput: request.user_input && { requestId: request.user_input.request_id, prompt: request.user_input.prompt, resumeReceiptId: request.user_input.resume_receipt_id, relation: request.user_input.relation, attachments: request.user_input.attachments } }) }),
    cancelPluginRuntime: async () => ({ cancelled: false }),
  } as unknown as WuuDesktopApi;
}
