/** In-memory bridge for the production desktop shell. No real accounts or writes. */
import type { WuuDesktopApi } from "../../src/shared/protocol";
import type { PluginHostOptions } from "../../src/renderer/plugins/PluginHost";

export function installFixtureBridge(invoke: NonNullable<PluginHostOptions["invokeRuntime"]>): void {
  const scoped = (window as unknown as {__scopedFixture?: {call(method:string, params?:unknown):Promise<any>}}).__scopedFixture;
  const workspace = "/synthetic/wuu";
  const projects = { projects: [], active_context: { kind: "no_project", cwd: workspace } };
  const thread = { id: "synthetic-coding", preview: "合成 Coding 对话", title: "", model_provider: "synthetic", model: "synthetic", cwd: workspace, workspace_kind: "scratch", status: "idle", pinned: false, archived: false, created_at: "2026-10-19T00:00:00Z", updated_at: "2026-10-19T00:00:00Z", turns: [] };
  window.wuu = {
    reportPluginDesktopActivation: async (params: {graph_generation:string}) => scoped && params.graph_generation !== "fixture-graph" ? scoped.call("plugin/desktop-activation",params) : ({accepted: true}),
    initializePluginScope: async (params: unknown) => scoped ? scoped.call("initialize-scope",params) : [],
    onPluginScopeInvalidated: () => () => {},
    loadPluginDesktopModule: async (params: unknown) => {
      if (!scoped) throw new Error("Scoped fixture host is unavailable");
      const module = await scoped.call("plugin/desktop-module/read", params);
      return {id:module.id, fingerprint:module.fingerprint, url:URL.createObjectURL(new Blob([module.source], {type:"text/javascript"}))};
    },
    getPluginStorage: async ({id,key,scope}: {id:string;key:string;scope:string}) => ({value: localStorage.getItem(`fixture:${id}:${scope}:${key}`)}),
    setPluginStorage: async ({id,key,scope,value}: {id:string;key:string;scope:string;value:string}) => { localStorage.setItem(`fixture:${id}:${scope}:${key}`, value); },
    getBuildInfo: async () => ({ desktop: { version: "synthetic-fixture" } }),
    listMCPServers: async () => ({ servers: [] }),
    listEngines: async () => ({ engines: [] }),
    listProjects: async () => projects,
    selectNoProject: async () => projects,
    initialize: async () => ({ protocol_version: "wuu-app-server/v0.1", provider: "synthetic", model: "synthetic", workspace_root: workspace, permissions: { mode: "standard" }, providers: [{ name: "synthetic", type: "openai-compatible", model: "synthetic", api_key_configured: true }], extension_inventory: [...(scoped && new URLSearchParams(location.search).has("commands") ? (await scoped.call("initialize-scope")).filter((item: any) => item.provenance?.plugin_id === "personal") : []), ...[...(scoped && new URLSearchParams(location.search).has("commands") ? [] : ["personal"]), "personal-calendar", "automations", ...(new URLSearchParams(location.search).has("genui") ? ["generative-ui"] : [])].map(id => ({id, name:id, kind:"plugin", enabled:true, state:"active", approval_state:"official", fingerprint:id === "generative-ui" ? "fixture" : "synthetic-ui-fixture", provenance:{kind:"plugin",source:"fixture",scope:"user",plugin_id:id}}))], advanced_settings: { max_steps: 64, max_context_tokens: 0, temperature: 0, disable_auto_compact: false } }),
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
    requestPluginRuntime: async (request: Parameters<WuuDesktopApi["requestPluginRuntime"]>[0]) => ({ ok: true as const, value: await (scoped && (request.method.startsWith("view-state.") || new URLSearchParams(location.search).has("commands") && request.method.startsWith("agents.")) ? scoped.call("plugin/client/request", request) : ({ result: await invoke({ pluginId: request.id, generation: request.fingerprint, graphGeneration:request.graph_generation, service:request.service, method: request.method, input: request.input, userSpaceId: request.user_space_id, productDomain: request.product_domain, compositionProfile: request.composition_profile, userInput: request.user_input && { requestId: request.user_input.request_id, prompt: request.user_input.prompt, resumeReceiptId: request.user_input.resume_receipt_id, relation: request.user_input.relation, attachments: request.user_input.attachments } }) })) }),
    cancelPluginRuntime: async () => ({ cancelled: false }),
  } as unknown as WuuDesktopApi;
}
