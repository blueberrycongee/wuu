/**
 * @wuu/plugin-sdk
 *
 * Public SDK for building Wuu plugins. This package provides TypeScript
 * types, interfaces, and helper utilities that plugin developers depend on.
 * Plugins must not import Wuu private source; all public API surfaces
 * through this package.
 *
 * @module
 */
export * from "./process.js";
export { LEGACY_THEME_TOKEN_ALIASES, PUBLIC_ICON_NAMES, PUBLIC_SYNTAX_TOKEN_NAMES, PUBLIC_THEME_TOKEN_NAMES, canonicalThemeTokenName, isPublicIconName, isPublicSyntaxTokenName, isPublicThemeTokenName, } from "./theme-contract.generated.js";
/** Where a view instance appears. */
/** Stable host-owned semantic regions available for View placement. */
export const VIEW_PLACEMENT_REGIONS = [
    "navigation",
    "primary",
    "auxiliary",
    "inspector",
    "settings",
    "overlay",
];
// ---------------------------------------------------------------------------
// Executable runtime contract
// ---------------------------------------------------------------------------
export const RUNTIME_PROTOCOL_V1 = 1;
export const CAPABILITY_PROTOCOL_V2 = 2;
export const CAPABILITY_PROTOCOL_V3 = 3;
export const REQUEST_TRANSFORM_CAPABILITY = "agent.request.transform";
export const AGENT_PRE_STEP_CAPABILITY = "agent.pre_step";
export const AGENT_TURN_COMPLETED_CAPABILITY = "agent.turn.completed";
export const AGENT_TURN_LIFECYCLE_CAPABILITY = "agent.turn.lifecycle";
export const AGENT_TURN_INTERRUPTED_CAPABILITY = "agent.turn.interrupted";
export const SYSTEM_PROMPT_SECTION_CAPABILITY = "agent.system_prompt.section";
/** @experimental No distributed first-party consumer has proven this contract. */
export const COMPACTION_CAPABILITY = "agent.compaction";
export const PLUGIN_CLIENT_REQUEST_CAPABILITY = "plugin.client.request";
export const HOST_SERVICE_METHODS = [
    "host.storage.get",
    "host.storage.set",
    "host.storage.delete",
    "host.storage.keys",
    "host.storage.compare_exchange",
    "host.settings.get",
    "host.settings.list",
    "host.credentials.request",
    "host.session.create",
    "host.session.send",
    "host.session.list",
    "host.session.cancel",
    "host.session.inspect",
    "host.session.control",
    "host.session.history.read",
    "host.session.history.search",
    "host.workspace.status",
    "host.workspace.apply",
    "host.workspace.discard",
    "host.service.call",
];
export const KERNEL_SERVICE_METHOD = "call";
export const EXECUTION_UPDATE_SERVICE = "execution.update";
export const INVOKE_TOOL_SERVICE = "execution.invoke-tool";
export const USER_QUESTION_ASK_SERVICE = "host.user-question.ask";
export const USER_QUESTION_OFFER_SERVICE = "host.user-question.offer";
export const ARTIFACT_IMPORT_SERVICE = "host.artifact.import";
export const FILE_LOCK_SERVICE = "host.file-lock";
export const MODEL_REQUEST_SERVICE = "host.model.request";
export const SECURITY_AUTHORIZE_SERVICE = "security.authorize";
export const PROCESS_SANDBOX_SERVICE = "sandbox.process";
export const SECURITY_AUTHORIZE_METHOD = "authorize";
export const PROCESS_SANDBOX_METHOD = "confine";
export const KERNEL_SERVICE_NAMES = {
    "host.storage.get": "host.storage.get",
    "host.storage.set": "host.storage.set",
    "host.storage.delete": "host.storage.delete",
    "host.storage.keys": "host.storage.keys",
    "host.storage.compare_exchange": "host.storage.compare-exchange",
    "host.settings.get": "host.settings.get",
    "host.settings.list": "host.settings.list",
    "host.credentials.request": "host.credentials.request",
    "host.session.create": "host.session.create",
    "host.session.send": "host.session.send",
    "host.session.list": "host.session.list",
    "host.session.cancel": "host.session.cancel",
    "host.session.inspect": "host.session.inspect",
    "host.session.control": "host.session.control",
    "host.session.history.read": "host.session.history.read",
    "host.session.history.search": "host.session.history.search",
    "host.workspace.status": "host.workspace.status",
    "host.workspace.apply": "host.workspace.apply",
    "host.workspace.discard": "host.workspace.discard",
};
/** Build a typed kernel service call through the Service Registry gateway. */
export function kernelServiceCall(id, method, params) {
    return {
        id,
        method: "host.service.call",
        params: { service: KERNEL_SERVICE_NAMES[method], method: KERNEL_SERVICE_METHOD, params },
    };
}
export function requireKernelService(method) {
    return { name: KERNEL_SERVICE_NAMES[method], major_version: 1, required: true };
}
export function requireArtifactImportService() {
    return { name: ARTIFACT_IMPORT_SERVICE, major_version: 1, required: true };
}
export function requireModelRequestService() {
    return { name: MODEL_REQUEST_SERVICE, major_version: 1, required: true };
}
/** Optional plugin-owned durable user notebook; declare major 1 with required: false. */
export const MEMORY_NOTEBOOK_SERVICE = "memory.notebook";
export const AUTHORIZATION_SERVICE_DESCRIPTOR = {
    name: SECURITY_AUTHORIZE_SERVICE,
    version: "1.0.0",
    methods: [{ name: SECURITY_AUTHORIZE_METHOD, input_schema: "security.authorize.input.v1", output_schema: "security.authorize.output.v1" }],
};
export const PROCESS_SANDBOX_SERVICE_DESCRIPTOR = {
    name: PROCESS_SANDBOX_SERVICE,
    version: "1.0.0",
    methods: [{ name: PROCESS_SANDBOX_METHOD, input_schema: "sandbox.process.input.v1", output_schema: "sandbox.process.output.v1" }],
};
export class RuntimeHostServiceError extends Error {
    code;
    constructor(error) {
        super(error.message);
        this.name = "RuntimeHostServiceError";
        this.code = error.code;
    }
}
/** Invoke an enabled child tool from a live orchestrator execution. Requires execution.invoke-tool v1. */
export async function invokeTool(host, params) {
    return await host.call("host.service.call", {
        service: INVOKE_TOOL_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params,
    });
}
/**
 * Run one bounded, tools-free model request through Wuu's configured provider.
 * Requires host.model.request v1 and a host scoped to a live tool, capability,
 * or service callback, including a generation-owned background job's callback.
 * The request is cancelled when that execution ends or is cancelled.
 */
export async function requestModel(host, input) {
    return await host.call("host.service.call", {
        service: MODEL_REQUEST_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params: input,
    });
}
export async function reportExecutionUpdate(host, update) {
    await host.call("host.service.call", {
        service: EXECUTION_UPDATE_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params: update,
    });
}
export async function askUserQuestions(host, questions) {
    return await host.call("host.service.call", {
        service: USER_QUESTION_ASK_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params: { questions },
    });
}
/** Offer a non-blocking question; any answer arrives through the conversation later. */
export async function offerUserQuestions(host, questions) {
    return await host.call("host.service.call", {
        service: USER_QUESTION_OFFER_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params: { questions },
    });
}
/** Copy a generated file into storage owned by the current tool execution's thread. */
export async function importArtifact(host, artifact) {
    return await host.call("host.service.call", {
        service: ARTIFACT_IMPORT_SERVICE,
        method: KERNEL_SERVICE_METHOD,
        params: artifact,
    });
}
/**
 * Acquire a cross-process OS lock through the host retained from initialize/activate.
 * An execution ID lets the kernel cancel a blocked acquisition. Wait for its
 * reply even after cancellation so a successfully acquired lock can be released.
 * The host releases outstanding locks when the runtime closes.
 */
export async function acquireFileLock(host, params, executionId) {
    return await host.call("host.service.call", {
        service: FILE_LOCK_SERVICE,
        method: "acquire",
        execution_id: executionId,
        params,
    });
}
/**
 * Release a lock idempotently. Use the host retained from initialize/activate
 * for cleanup after execution cancellation; an execution-scoped host is aborted.
 */
export async function releaseFileLock(host, lockId, options) {
    return await host.call("host.service.call", {
        service: FILE_LOCK_SERVICE,
        method: "release",
        params: { lock_id: lockId },
    }, options);
}
export async function handleRuntimeRequest(plugin, request, host = unavailableRuntimeHost, execution = idleRuntimeExecution, lifecycle = idleRuntimeExecution) {
    const executionHost = scopedRuntimeHost(host, execution);
    try {
        switch (request.method) {
            case "initialize": {
                const result = await plugin.initialize(request.params, host, lifecycle);
                return { id: request.id, result: {
                        ...result,
                        protocol_version: result.required_services?.length || result.provided_services?.length
                            ? CAPABILITY_PROTOCOL_V3 : result.protocol_version,
                        lifecycle_version: 2,
                    } };
            }
            case "prepare":
                await plugin.prepare?.(host, lifecycle);
                return { id: request.id, result: null };
            case "activate":
                await plugin.activate?.(host, lifecycle);
                return { id: request.id, result: null };
            case "capability.invoke":
                if (!plugin.invokeCapability)
                    throw new Error("capability.invoke is not implemented");
                // Read the live signal after queue/lifecycle waiting, before user code.
                execution.signal.throwIfAborted();
                return { id: request.id, result: await plugin.invokeCapability(request.params, executionHost, execution) };
            case "service.invoke":
                if (!plugin.invokeService)
                    throw new Error("service.invoke is not implemented");
                execution.signal.throwIfAborted();
                return { id: request.id, result: await plugin.invokeService(request.params, executionHost, execution) };
            case "service.changed":
                await plugin.serviceChanged?.(request.params, host);
                return { id: request.id, result: null };
            case "tool.execute":
                if (!plugin.executeTool)
                    throw new Error("tool.execute is not implemented");
                execution.signal.throwIfAborted();
                return { id: request.id, result: await plugin.executeTool(request.params, executionHost, execution) };
            case "shutdown":
                await plugin.shutdown?.();
                return { id: request.id, result: null };
        }
    }
    catch (error) {
        return { id: request.id, error: { message: error instanceof Error ? error.message : String(error) } };
    }
}
function scopedRuntimeHost(host, execution) {
    return {
        supports: (method) => host.supports(method),
        call: (method, params, options) => {
            const signal = options?.signal
                ? AbortSignal.any([execution.signal, options.signal])
                : execution.signal;
            const serviceParams = method === "host.service.call"
                ? params
                : { service: KERNEL_SERVICE_NAMES[method], method: KERNEL_SERVICE_METHOD, params };
            return host.call("host.service.call", {
                ...serviceParams,
                ...(execution.executionId ? { execution_id: execution.executionId } : {}),
            }, { signal });
        },
    };
}
const unavailableRuntimeHost = {
    supports: () => false,
    call: async (method) => { throw new Error(`host service ${method} is unavailable outside a runtime connection`); },
};
const idleRuntimeExecutionController = new AbortController();
const idleRuntimeExecution = {
    executionId: "",
    signal: idleRuntimeExecutionController.signal,
};
class JSONLRuntimeHost {
    send;
    sequence = 0;
    closed = false;
    supported = new Set();
    pending = new Map();
    constructor(send) {
        this.send = send;
    }
    configure(methods) {
        this.supported = new Set(methods ?? []);
    }
    supports(method) {
        return this.supported.has("host.service.call") || this.supported.has(method);
    }
    call(method, params, options) {
        const signal = options?.signal;
        if (signal?.aborted)
            return Promise.reject(signal.reason);
        if (this.closed)
            return Promise.reject(new Error("runtime transport closed"));
        if (!this.supports(method)) {
            return Promise.reject(new Error(`host service ${method} is not supported`));
        }
        const id = `plugin-${++this.sequence}`;
        return new Promise((resolve, reject) => {
            const cleanup = () => {
                this.pending.delete(id);
                signal?.removeEventListener("abort", abort);
            };
            const abort = () => {
                cleanup();
                reject(signal.reason);
            };
            const fail = (error) => {
                cleanup();
                reject(error);
            };
            this.pending.set(id, {
                resolve(value) { cleanup(); resolve(value); },
                reject: fail,
            });
            signal?.addEventListener("abort", abort, { once: true });
            const frame = method === "host.service.call"
                ? { id, method, params }
                : kernelServiceCall(id, method, params);
            void this.send(frame).catch(fail);
        });
    }
    route(value) {
        if (typeof value !== "object" || value === null)
            return false;
        const response = value;
        if (typeof response.id !== "string" || response.method !== undefined
            || (!("result" in response) && !("error" in response)))
            return false;
        const pending = this.pending.get(response.id);
        // Cancelled calls may still receive a late response; never dispatch it as a request.
        if (!pending)
            return true;
        this.pending.delete(response.id);
        if (response.error !== undefined) {
            const error = response.error;
            pending.reject(new RuntimeHostServiceError({
                code: typeof error.code === "string" ? error.code : "host_error",
                message: typeof error.message === "string" ? error.message : "host service failed",
            }));
        }
        else {
            pending.resolve(response.result);
        }
        return true;
    }
    close() {
        this.closed = true;
        for (const pending of this.pending.values()) {
            pending.reject(new Error("runtime transport closed"));
        }
        this.pending.clear();
    }
}
// Bound tool bodies without consuming the separate reentrant callback lane.
const MAX_CONCURRENT_TOOLS = 8;
/** Runs a plugin over Wuu's full-duplex JSON-lines transport. */
export async function runJSONLRuntime(plugin, streams) {
    let writes = Promise.resolve();
    const send = (value) => {
        const next = writes.then(async () => { await streams.output.write(`${JSON.stringify(value)}\n`); });
        writes = next.catch(() => undefined);
        return next;
    };
    const host = new JSONLRuntimeHost(send);
    const lifetime = new AbortController();
    const executions = new Map();
    const active = new Set();
    const concurrentServices = new Set(plugin.concurrentServices ?? []);
    const concurrentCapabilities = new Set(["model.adapter", "model.middleware", ...plugin.concurrentCapabilities ?? []]);
    const concurrentTools = new Set();
    const queued = [];
    let running = 0;
    let exclusive = false;
    // Only adjacent explicitly safe tools share slots. Classification happens
    // after preceding work, including initialize, publishes its registrations.
    const pump = () => {
        while (queued.length > 0 && !exclusive && running < MAX_CONCURRENT_TOOLS) {
            const next = queued[0];
            const safe = next.canOverlap();
            if (!safe && running > 0)
                return;
            queued.shift();
            running++;
            exclusive = !safe;
            const finished = () => {
                running--;
                if (!safe)
                    exclusive = false;
                pump();
            };
            void next.run().then(finished, finished);
        }
    };
    const schedule = (run, canOverlap = () => false) => {
        const task = new Promise((resolve, reject) => {
            queued.push({ run: () => Promise.resolve().then(run).then(resolve, reject), canOverlap });
        });
        pump();
        return task;
    };
    let lifecycle = Promise.resolve();
    let shuttingDown = false;
    const track = (task) => {
        active.add(task);
        void task.then(() => active.delete(task), () => active.delete(task));
    };
    const enqueueResponse = (value) => {
        track(schedule(() => send(value)));
    };
    const processLine = (line) => {
        if (line.trim() === "")
            return;
        let parsed;
        try {
            parsed = JSON.parse(line);
        }
        catch (error) {
            enqueueResponse({ id: "invalid", error: { message: error instanceof Error ? error.message : String(error) } });
            return;
        }
        if (host.route(parsed))
            return;
        if (isExecutionCancelRequest(parsed)) {
            for (const controller of executions.get(parsed.params.execution_id) ?? [])
                controller.abort();
            return;
        }
        if (!isRuntimeRequest(parsed)) {
            enqueueResponse({ id: "invalid", error: { message: "invalid runtime request" } });
            return;
        }
        if (shuttingDown) {
            track(send({ id: parsed.id, error: { message: "runtime is shutting down" } }));
            return;
        }
        const isLifecycle = parsed.method === "initialize" || parsed.method === "prepare" || parsed.method === "activate" || parsed.method === "shutdown";
        if (parsed.method === "shutdown") {
            shuttingDown = true;
            // Background work may hold an active handler; stop it before waiting for the barrier.
            lifetime.abort();
        }
        const concurrent = (parsed.method === "service.invoke" && concurrentServices.has(parsed.params.service))
            || (parsed.method === "capability.invoke" && concurrentCapabilities.has(parsed.params.capability));
        const executionId = runtimeRequestExecutionId(parsed);
        const controller = new AbortController();
        // Even legacy requests without an execution id must stop on transport loss.
        let controllers = executions.get(executionId);
        if (!controllers)
            executions.set(executionId, controllers = new Set());
        controllers.add(controller);
        const execution = { executionId, signal: controller.signal };
        // A lifecycle frame waits for everything already admitted. Opted-in
        // handlers may bypass ordinary work, but never an earlier lifecycle
        // frame. Capture the barrier now so later frames cannot create a cycle.
        const ready = isLifecycle ? Promise.all([...active]) : concurrent ? lifecycle : Promise.resolve();
        const run = async () => {
            await ready;
            if (parsed.method === "initialize") {
                host.configure(parsed.params.supported_host_services);
                concurrentTools.clear();
            }
            const response = await handleRuntimeRequest(plugin, parsed, host, execution, { signal: lifetime.signal });
            if (parsed.method === "initialize" && "result" in response) {
                const definition = response.result;
                for (const tool of definition.tools ?? []) {
                    if (tool.activity?.concurrency_safe === true)
                        concurrentTools.add(tool.id);
                }
            }
            await send(response);
        };
        const task = (concurrent ? run() : schedule(run, () => parsed.method === "tool.execute" && concurrentTools.has(parsed.params.tool_id)))
            .finally(() => {
            const controllers = executions.get(executionId);
            controllers?.delete(controller);
            if (controllers?.size === 0)
                executions.delete(executionId);
            controller.abort();
        });
        if (isLifecycle)
            lifecycle = task.catch(() => undefined);
        track(task);
    };
    const decoder = new TextDecoder();
    let buffered = "";
    try {
        for await (const chunk of streams.input) {
            buffered += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
            const lines = buffered.split("\n");
            buffered = lines.pop() ?? "";
            for (const line of lines)
                processLine(line);
        }
        buffered += decoder.decode();
        if (buffered.trim() !== "")
            processLine(buffered);
    }
    finally {
        lifetime.abort();
        host.close();
        for (const controllers of executions.values()) {
            for (const controller of controllers)
                controller.abort();
        }
        await Promise.all(active);
        await writes;
    }
}
function isExecutionCancelRequest(value) {
    if (typeof value !== "object" || value === null)
        return false;
    const request = value;
    return typeof request.id === "string"
        && request.method === "execution.cancel"
        && typeof request.params?.execution_id === "string"
        && request.params.execution_id.trim() !== "";
}
function runtimeRequestExecutionId(request) {
    switch (request.method) {
        case "capability.invoke":
        case "service.invoke":
        case "tool.execute":
            return request.params.execution_id?.trim() ?? "";
        default:
            return "";
    }
}
function isRuntimeRequest(value) {
    if (typeof value !== "object" || value === null)
        return false;
    const request = value;
    if (typeof request.id !== "string" || typeof request.method !== "string")
        return false;
    if (request.method === "prepare" || request.method === "activate" || request.method === "shutdown")
        return true;
    return ["initialize", "capability.invoke", "service.invoke", "service.changed", "tool.execute"].includes(request.method)
        && typeof request.params === "object" && request.params !== null;
}
export const PRESENTATION_TARGETS = [
    "conversation.item", "conversation.process", "conversation.tool-activity",
    "conversation.composer", "header.conversation", "header.workspace",
    "navigation.primary", "app.status", "content.preview", "settings",
];
export const CONVERSATION_ITEM_ACTIONS = {
    copy: "conversation.item.copy",
    edit: "conversation.item.edit",
    retry: "conversation.item.retry",
    openAttachment: "conversation.item.open-attachment",
    openTool: "conversation.item.open-tool",
    cancelProcess: "conversation.item.cancel-process",
};
export const COMPOSER_ACTIONS = {
    setDraft: "conversation.composer.set-draft",
    addAttachment: "conversation.composer.add-attachment",
    removeAttachment: "conversation.composer.remove-attachment",
    setSubmissionMode: "conversation.composer.set-submission-mode",
    submit: "conversation.composer.submit",
    stop: "conversation.composer.stop",
};
export const HEADER_ACTIONS = {
    selectTab: "header.select-tab",
    closeTab: "header.close-tab",
    navigateBack: "header.navigate-back",
    navigateForward: "header.navigate-forward",
};
export const NAVIGATION_ACTIONS = {
    activateNode: "navigation.activate-node",
    pinNode: "navigation.pin-node",
    unpinNode: "navigation.unpin-node",
};
export const STATUS_ACTIONS = { activateItem: "status.activate-item" };
export const FILE_PREVIEW_ACTIONS = {
    open: "file-preview.open",
    reveal: "file-preview.reveal",
    select: "file-preview.select",
    save: "file-preview.save",
    reload: "file-preview.reload",
};
export const SETTINGS_ACTIONS = {
    openPage: "settings.open-page",
    updateValue: "settings.update-value",
    refresh: "settings.refresh",
};
// ---------------------------------------------------------------------------
// Plugin manifest helpers
// ---------------------------------------------------------------------------
export function validateManifest(manifest) {
    const errors = [];
    if (!manifest.id || typeof manifest.id !== "string") {
        errors.push("manifest.id is required (string)");
    }
    if (manifest.schema_version !== 1) {
        errors.push("manifest.schema_version must be 1");
    }
    if (!manifest.version || typeof manifest.version !== "string") {
        errors.push("manifest.version is required (string)");
    }
    return errors;
}
export function createManifest(options) {
    return {
        schema_version: 1,
        id: options.id,
        name: options.name ?? options.id,
        version: options.version ?? "0.1.0",
        description: options.description ?? `A Wuu plugin: ${options.id}`,
        ...(options.icon ? { icon: options.icon } : {}),
        ...(options.requires ? { requires: options.requires } : {}),
        ...(options.dependencies ? { dependencies: options.dependencies } : {}),
    };
}
export * from "./bundle-contract.js";
export * from "./model.js";
