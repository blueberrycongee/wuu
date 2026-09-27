import {
  CAPABILITY_PROTOCOL_V2,
  COMPOSER_ACTIONS,
  CONVERSATION_ITEM_ACTIONS,
  FILE_PREVIEW_ACTIONS,
  HEADER_ACTIONS,
  NAVIGATION_ACTIONS,
  PRESENTATION_TARGETS,
  PUBLIC_SYNTAX_TOKEN_NAMES,
  PUBLIC_THEME_TOKEN_NAMES,
  REQUEST_TRANSFORM_CAPABILITY,
  SETTINGS_ACTIONS,
  STATUS_ACTIONS,
  VIEW_PLACEMENT_REGIONS,
  handleRuntimeRequest,
  kernelServiceCall,
  requireKernelService,
  runJSONLRuntime,
  type ComposerSnapshotV1,
  type ConversationItemSnapshotV1,
  type ConversationProcessSnapshotV1,
  type FilePreviewSnapshotV1,
  type HeaderSnapshotV1,
  type HostServiceContracts,
  type NavigationSnapshotV1,
  type RuntimeInitializeResult,
  type RuntimePlugin,
  type PresenterDefinition,
  type SettingsSnapshotV1,
  type StatusSnapshotV1,
  type ToolActivityPresenterDefinition,
  type ToolActivitySnapshot,
  type ViewPlacementContribution,
} from "./index.js";

const kernelCall = kernelServiceCall("storage", "host.storage.compare_exchange", {
  scope: "workspace", key: "state", expected: null, value: "next",
});
if (kernelCall.method !== "host.service.call" || kernelCall.params.service !== "host.storage.compare-exchange" || kernelCall.params.method !== "call") {
  throw new Error("kernel service wrapper contract failed");
}
const kernelRequirement = requireKernelService("host.session.send");
if (kernelRequirement.name !== "host.session.send" || kernelRequirement.major_version !== 1 || !kernelRequirement.required) {
  throw new Error("kernel service requirement contract failed");
}
const steerDelivery: HostServiceContracts["host.session.send"]["params"] = {
  request_id: "follow-up-1",
  session_id: "child-1",
  input: { prompt: "Summarize now" },
  if_running: "steer",
};

if (!PRESENTATION_TARGETS.includes("conversation.tool-activity") || !PRESENTATION_TARGETS.includes("settings")) {
  throw new Error("presentation target contract failed");
}
if (!PUBLIC_THEME_TOKEN_NAMES.includes("--wuu-color-canvas") || !PUBLIC_SYNTAX_TOKEN_NAMES.includes("--wuu-syntax-keyword")) {
  throw new Error("theme token contract failed");
}
if (VIEW_PLACEMENT_REGIONS.join(",") !== "navigation,primary,auxiliary,inspector,settings,overlay") {
  throw new Error("View placement region contract failed");
}
const viewPlacement: ViewPlacementContribution = {
  id: "dashboard",
  view: "views.dashboard",
  region: "auxiliary",
  priority: 10,
};
const genericPresenter: PresenterDefinition = {
  id: "preview",
  target: "content.preview",
  mode: "wrap",
  priority: 10,
  render: ({ contractVersion, fallback }) => contractVersion === 1 ? fallback : null,
};

const conversationItem: ConversationItemSnapshotV1 = Object.freeze({
  contractVersion: 1,
  id: "message-1",
  kind: "assistant-message",
  status: "completed",
  content: Object.freeze([{ type: "markdown", text: "Done" }] as const),
  attachments: Object.freeze([{ id: "attachment-1", name: "result.png", mimeType: "image/png", width: 640, height: 480 }]),
  toolReferences: Object.freeze([{ id: "call-1", name: "read_file", capability: "workspace.read", status: "completed" }] as const),
});
const conversationProcess: ConversationProcessSnapshotV1 = Object.freeze({
  contractVersion: 1,
  kind: "mixed",
  status: "running",
  streaming: true,
  active: true,
  items: Object.freeze([
    Object.freeze({ id: "reason-1", kind: "reasoning", status: "running", text: "Checking" }),
    Object.freeze({ id: "tool-1", kind: "tool-activity", status: "completed", toolName: "read_file", capability: "workspace.read" }),
  ]),
});
const composer: ComposerSnapshotV1 = Object.freeze({
  contractVersion: 1,
  draftText: "Continue",
  threadId: "thread-1",
  availableSubmissionModes: Object.freeze(["send", "queue"] as const),
  model: Object.freeze({ id: "model-1", label: "Model", providerId: "provider-1" }),
  contextUsage: Object.freeze({ usedTokens: 100, limitTokens: 1_000, percent: 10 }),
});
const header: HeaderSnapshotV1 = Object.freeze({
  contractVersion: 1,
  scope: "conversation",
  title: "Conversation",
  tabs: Object.freeze([{ id: "thread-1", title: "Conversation" }] as const),
  activeTabId: "thread-1",
});
const navigation: NavigationSnapshotV1 = Object.freeze({
  contractVersion: 1,
  nodes: Object.freeze([
    { id: "workspace", kind: "section", label: "Workspace", depth: 0 },
    { id: "thread-1", kind: "thread", label: "Conversation", parentId: "workspace", depth: 1, active: true },
  ] as const),
  activeNodeId: "thread-1",
});
const status: StatusSnapshotV1 = Object.freeze({
  contractVersion: 1,
  items: Object.freeze([{ id: "runtime", label: "Running", kind: "progress", busy: true, actionId: STATUS_ACTIONS.activateItem }] as const),
});
const preview: FilePreviewSnapshotV1 = Object.freeze({
  contractVersion: 1,
  resourceId: "resource-1",
  workspaceRelativePath: "src/index.ts",
  contentType: "text/typescript",
  text: "export {};",
  selection: Object.freeze({ startLine: 1, startColumn: 1 }),
});
const settings: SettingsSnapshotV1 = Object.freeze({
  contractVersion: 1,
  activePageId: "providers",
  availablePages: Object.freeze([{ id: "providers", label: "Providers" }]),
  providers: Object.freeze([{ id: "provider-1", label: "Provider", configured: true }]),
});

const actionIds = [
  CONVERSATION_ITEM_ACTIONS.copy,
  COMPOSER_ACTIONS.submit,
  HEADER_ACTIONS.selectTab,
  NAVIGATION_ACTIONS.activateNode,
  STATUS_ACTIONS.activateItem,
  FILE_PREVIEW_ACTIONS.open,
  SETTINGS_ACTIONS.openPage,
];
if (actionIds.some((action) => !action.includes("."))) throw new Error("presentation action ID contract failed");

const snapshot: ToolActivitySnapshot = Object.freeze({
  contractVersion: 1,
  id: "call",
  toolName: "echo",
  status: "running",
  argumentsText: '{"partial":',
});
const presenter: ToolActivityPresenterDefinition = {
  id: "echo",
  key: "tool.echo",
  render: ({ activity, fallback }) => activity.id === "call" ? activity.argumentsText : fallback,
};
presenter.render({ activity: snapshot, host: {} as never, fallback: "native" });

const plugin: RuntimePlugin = {
  initialize(params) {
    return {
      protocol_version: CAPABILITY_PROTOCOL_V2,
      capabilities: [{ id: REQUEST_TRANSFORM_CAPABILITY, kind: "transform", version: 1 }],
      tools: [{ id: "echo", description: "Echo input", input_schema: { type: "object" } }],
      required_host_services: [],
    };
  },
  invokeCapability(params) {
    return { output: params.output };
  },
  executeTool() {
    return { result: { content: [{ type: "text", text: "ok" }] } };
  },
};

const initialized = await handleRuntimeRequest(plugin, {
  id: "init",
  method: "initialize",
  params: { protocol_version: 1, capability_protocol_version: 2, plugin_id: "test", plugin_root: ".", project_root: ".", wuu_home: "." },
});
const initializedResult = "result" in initialized ? initialized.result as RuntimeInitializeResult | null : null;
if (initializedResult === null || initializedResult.protocol_version === undefined) {
  throw new Error("initialize did not return the v2 descriptor");
}

async function* input(): AsyncIterable<string> {
  yield '{"id":"tool","method":"tool.execute","params":{"tool_id":"echo","cwd":".","call_id":"1","tool":"echo","arguments":{}}}\n';
  yield "not-json\n";
}
const lines: string[] = [];
await runJSONLRuntime(plugin, { input: input(), output: { write: (line) => lines.push(line) } });
if (lines.length !== 2 || !lines[0]?.includes('"text":"ok"') || !lines[1]?.includes('"id":"invalid"')) {
  throw new Error(`unexpected JSONL responses: ${lines.join("")}`);
}

let markExecutionStarted!: () => void;
const executionStarted = new Promise<void>((resolve) => { markExecutionStarted = resolve; });
let markExecutionCancelled!: () => void;
const executionCancelled = new Promise<void>((resolve) => { markExecutionCancelled = resolve; });
const cancellablePlugin: RuntimePlugin = {
  initialize: () => ({ protocol_version: 3 }),
  async executeTool(params, _host, execution) {
    if (params.execution_id !== "exec-ts" || execution.executionId !== "exec-ts") {
      throw new Error(`execution identity was not preserved: ${JSON.stringify(params)}`);
    }
    markExecutionStarted();
    await new Promise<void>((resolve) => {
      if (execution.signal.aborted) {
        resolve();
        return;
      }
      execution.signal.addEventListener("abort", () => resolve(), { once: true });
    });
    markExecutionCancelled();
    return { result: { content: [{ type: "text", text: "cancelled" }] } };
  },
};
async function* cancellationInput(): AsyncIterable<string> {
  yield '{"id":"tool-cancel","method":"tool.execute","params":{"tool_id":"wait","execution_id":"exec-ts","cwd":".","call_id":"1","tool":"wait","arguments":{}}}\n';
  await executionStarted;
  yield '{"id":"cancel","method":"execution.cancel","params":{"execution_id":"exec-ts"}}\n';
  await executionCancelled;
}
const cancellationLines: string[] = [];
await runJSONLRuntime(cancellablePlugin, {
  input: cancellationInput(),
  output: { write: (line) => cancellationLines.push(line) },
});
if (cancellationLines.length !== 1 || !cancellationLines[0]?.includes('"text":"cancelled"')) {
  throw new Error(`execution.cancel emitted a response or failed to abort: ${cancellationLines.join("")}`);
}

// --- Service provide/consume (capability protocol v3) ---

const servicePlugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3,
      provided_services: [{
        name: "search.provider",
        version: "1.0.0",
        methods: [{ name: "query", input_schema: "search.query.request.v1", output_schema: "search.query.response.v1" }],
      }],
      required_services: [{ name: "memory.index", major_version: 1 }],
    };
  },
  async invokeService(params, host) {
    if (params.caller !== "notes" || params.method !== "query") {
      throw new Error(`unexpected service.invoke params: ${JSON.stringify(params)}`);
    }
    await host.call("host.service.call", { service: "memory.index", method: "lookup" });
    return { hits: ["a"] };
  },
  serviceChanged(params) {
    if (params.service !== "search.provider") {
      throw new Error(`unexpected service.changed params: ${JSON.stringify(params)}`);
    }
  },
};

const serviceInit = await handleRuntimeRequest(servicePlugin, {
  id: "init",
  method: "initialize",
  params: { protocol_version: 1, capability_protocol_version: 3, plugin_id: "search", plugin_root: ".", project_root: ".", wuu_home: "." },
});
const serviceInitResult = "result" in serviceInit ? serviceInit.result as RuntimeInitializeResult | null : null;
if (serviceInitResult === null || serviceInitResult.provided_services === undefined || serviceInitResult.required_services === undefined) {
  throw new Error("initialize did not return the v3 service descriptor");
}

const serviceInvoke = await handleRuntimeRequest(servicePlugin, {
  id: "invoke",
  method: "service.invoke",
  params: { service: "search.provider", method: "query", caller: "notes", execution_id: "exec-service", params: { q: "x" } },
}, {
  supports: () => true,
  call: async (method, params) => {
    if (method !== "host.service.call" || (params as { execution_id?: string }).execution_id !== "exec-service") {
      throw new Error(`nested service call lost execution identity: ${JSON.stringify(params)}`);
    }
    return { found: true };
  },
}, { executionId: "exec-service", signal: new AbortController().signal });
if (!("result" in serviceInvoke) || JSON.stringify(serviceInvoke.result) !== '{"hits":["a"]}') {
  throw new Error(`unexpected service.invoke response: ${JSON.stringify(serviceInvoke)}`);
}

const serviceChanged = await handleRuntimeRequest(servicePlugin, {
  id: "changed",
  method: "service.changed",
  params: { service: "search.provider", reason: "provider_closed" },
});
if (!("result" in serviceChanged)) {
  throw new Error(`unexpected service.changed response: ${JSON.stringify(serviceChanged)}`);
}
