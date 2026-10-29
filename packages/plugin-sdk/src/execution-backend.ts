import type { JSONLInput, JSONLOutput, ToolContentPart, ToolResult } from "./index.js";
import type { ToolServiceCall } from "./tool-service.js";

/** Execution transport version, independent of Extension API and host.process versions. */
export const EXECUTION_BACKEND_PROTOCOL_VERSION = 4;
export const EXECUTION_BACKEND_MAX_FRAME_BYTES = 16 * 1024 * 1024;

export interface ExecutionBackendBindingV4 {
  readonly version: 4;
  readonly root: string;
  readonly session: string;
  readonly identity?: string;
  readonly environment?: Readonly<Record<string, string>>;
  readonly require_existing_workspace?: boolean;
  readonly remove_workspace_on_exit?: boolean;
}
export type ExecutionPermissionModeV4 = "" | "standard" | "read_only" | "unconfined";
export interface ExecutionToolRequestV4 {
  call: ToolServiceCall;
  permission_mode: ExecutionPermissionModeV4;
  actor: string;
  git_attribution_enabled?: boolean;
  shell_early_handoff_disabled?: boolean;
}
export interface ExecutionProcessStartV4 {
  command: string;
  cwd?: string;
  permission_mode: ExecutionPermissionModeV4;
  actor: string;
  git_attribution_enabled?: boolean;
  /** Host-minted reconciliation identity, not permission or a replay instruction. */
  operation_id?: string;
}
export interface ExecutionProcessV4 {
  id: string;
  operation_id?: string;
  owner_kind: "main_agent" | "subagent";
  owner_id: string;
  root_thread_id: string;
  host_generation_id?: string;
  lifecycle: "session" | "managed";
  completion_mode?: "resume" | "detached";
  status: "starting" | "running" | "stopping" | "stopped" | "failed";
  pid: number;
  pgid: number;
  process_start_time?: string;
  tty?: boolean;
  /** Backend-local diagnostic path; the host must not open it. */
  log_path: string;
  log_base_offset?: number;
  command: string;
  cwd: string;
  started_at: string;
  updated_at: string;
  stopped_at?: string;
  exit_code?: number;
  last_error?: string;
  sandbox_mode?: string;
  sandbox_denied?: boolean;
  sandbox_runner_failed?: boolean;
  sandbox_denial_signatures?: string[];
  sandbox_runner_failure_signatures?: string[];
  terminal_cause?: "natural_exit" | "requested_stop";
  completion_delivered_at?: string;
  completion_consumed_by?: string;
  notifications_cancelled_at?: string;
  recheck_minutes?: number;
  next_recheck_at?: string;
  pending_recheck_at?: string;
  action?: string;
}
/** Go wire casing and nanosecond durations are intentional; do not use milliseconds here. */
export interface ExecutionProcessReadOptionsV4 {
  MaxBytes: number;
  OffsetBytes: number | null;
  Wait: number;
  MinDwell: number;
}
export interface ExecutionProcessOutputV4 {
  Process: ExecutionProcessV4;
  Output: string;
  Truncated: boolean;
  StartOffset: number;
  EndOffset: number;
  TotalBytes: number;
  TimedOut: boolean;
  /** Nanoseconds, not milliseconds. */
  Duration: number;
}
export interface ExecutionProcessEventV4 {
  Type: "started" | "updated" | "failed" | "stopped" | "cleaned_up" | "recheck_due";
  Cause: "" | "natural_exit" | "requested_stop";
  Process: ExecutionProcessV4;
}
export interface ExecutionCodeRequestV4 {
  /** Enforce no session state for the entire program when the capability is advertised. */
  disable_state?: boolean;
  program: {
    code: string;
    tools: { name: string; description: string; input_schema: Record<string, unknown> }[];
    result_view?: "compact" | "data";
  };
  timeout_ms: number;
  permission_mode: ExecutionPermissionModeV4;
  actor: string;
  git_attribution_enabled?: boolean;
}
export interface ExecutionCodeResultV4 {
  Logs: string[] | null;
  Value: unknown;
  Error: string;
  Media: ToolContentPart[] | null;
  CallSummary?: string;
}
type ProcessID = { id: string };
export interface ExecutionBackendMethodsV4 {
  execute: { input: ExecutionToolRequestV4; result: ToolResult };
  run_code: { input: ExecutionCodeRequestV4; result: ExecutionCodeResultV4 };
  "process/start": { input: ExecutionProcessStartV4; result: ExecutionProcessV4 };
  "process/list": { input: null; result: ExecutionProcessV4[] | null };
  "process/get": { input: ProcessID; result: ExecutionProcessV4 };
  "process/read": { input: ProcessID & { options: ExecutionProcessReadOptionsV4 }; result: ExecutionProcessOutputV4 };
  "process/write": { input: ProcessID & { input: string }; result: ExecutionProcessV4 };
  "process/resize": { input: ProcessID & { cols: number; rows: number }; result: ExecutionProcessV4 };
  "process/stop": { input: ProcessID; result: ExecutionProcessV4 };
  "process/input_available": { input: ProcessID; result: boolean };
  "process/completion_pending": { input: ProcessID; result: boolean };
  "process/completion_delivered": { input: ProcessID & { consumer: string }; result: ExecutionProcessV4 };
  "process/recheck": { input: ProcessID & { minutes: number }; result: ExecutionProcessV4 };
  "process/cancel_notifications": { input: ProcessID; result: ExecutionProcessV4 };
  "process/completion_mode": { input: ProcessID & { mode: "resume" | "detached" }; result: ExecutionProcessV4 };
  "process/recheck_delivered": { input: ProcessID; result: ExecutionProcessV4 };
  "process/cleanup": { input: null; result: { Cleaned: ExecutionProcessV4[] | null } };
  "export/read": { input: { token: string; offset: number }; result: { data: string; eof: boolean } };
  "export/close": { input: { token: string }; result: Record<string, never> };
}
export interface ExecutionBackendContextV4 {
  readonly requestId: string;
  readonly binding: ExecutionBackendBindingV4;
  /** Addressed RPC cancellation; check before effects and drain before returning. */
  readonly signal: AbortSignal;
  /** EOF/transport failure; accepted processes must be cleaned up by close(). */
  readonly lifetimeSignal: AbortSignal;
  /** May outlive a start callback, but not this transport. Event records stay in this session. */
  emitProcess(event: ExecutionProcessEventV4): Promise<void>;
  /** Only valid during run_code; the host retains that parent call's tool authority. */
  invokeTool(call: ToolServiceCall): Promise<ToolResult>;
}
export interface ExecutionBackendV4 {
  /** Explicit promise, not inferred from method presence. process.start.v1 means
   * operation_id is returned AND persisted in process/list records; it does not
   * promise duplicate-start deduplication. code.stateless.v1 promises that
   * disable_state prevents all program access to persistent Code Mode state.
   * Unsupported capabilities are not emulated. */
  capabilities?: readonly ("process.start.v1" | "code.stateless.v1")[];
  initialize(binding: ExecutionBackendBindingV4, context: ExecutionBackendContextV4): void | Promise<void>;
  methods: { [M in keyof ExecutionBackendMethodsV4]?: (
    input: ExecutionBackendMethodsV4[M]["input"], context: ExecutionBackendContextV4,
  ) => ExecutionBackendMethodsV4[M]["result"] | Promise<ExecutionBackendMethodsV4[M]["result"]> };
  /** Called once after request cancellation/drain, including failed initialization.
   * Stop owned process trees and release exports/resources before resolving. */
  close?(): void | Promise<void>;
}

export interface ExecutionBackendPeerTransportV4 {
  input: JSONLInput;
  output: JSONLOutput;
  /** Close admission and drain the explicitly attached peer's physical resources. */
  close(): Promise<void>;
}
export type ExecutionBackendDelegateOptionsV4 = (
  | {
    command: readonly string[]; cwd?: string; env?: Readonly<Record<string, string>>; transport?: never;
    /** EOF-to-exit cleanup budget, default 10,000 ms; integer 1..2,147,483,647.
     * Timeout terminates the peer but cannot prove remote resource cleanup. */
    shutdown_grace_ms?: number;
  }
  | { transport: ExecutionBackendPeerTransportV4; command?: never; cwd?: never; env?: never; shutdown_grace_ms?: never }
) & {
  /** Required from the selected peer before this backend can initialize. */
  capabilities?: readonly ("process.start.v1" | "code.stateless.v1")[];
};

/** Explicit same-world delegation, not error recovery. No peer is started until
 * initialize. Missing peers, mismatched bindings and unsupported capabilities
 * fail without replay, reconnect or selecting the native worker. Override only
 * coherent methods in the returned methods map; a different filesystem world
 * must not retain unrelated delegated process handlers/capabilities.
 */
export function delegateExecutionBackendV4(options: ExecutionBackendDelegateOptionsV4): ExecutionBackendV4 {
  if ((options.command !== undefined) === (options.transport !== undefined) ||
    options.command !== undefined && (!options.command.length || !options.command[0])) throw new Error("select exactly one explicit execution backend peer");
  if (options.transport !== undefined && options.shutdown_grace_ms !== undefined) throw new Error("shutdown_grace_ms requires a command peer; attached transports own cleanup");
  const shutdownGraceMS = options.shutdown_grace_ms === undefined ? 10_000 : options.shutdown_grace_ms;
  if (!Number.isInteger(shutdownGraceMS) || shutdownGraceMS < 1 || shutdownGraceMS > 2_147_483_647) throw new Error("shutdown_grace_ms must be an integer from 1 to 2147483647");
  const capabilities = Object.freeze([...options.capabilities ?? []]);
  const lifetime = new AbortController();
  let transport: ExecutionBackendPeerTransportV4 | undefined;
  let opening: Promise<ExecutionBackendPeerTransportV4> | undefined;
  let initializing: Promise<void> | undefined;
  let bindingJSON: string | undefined;
  let initializeContext: ExecutionBackendContextV4 | undefined;
  let outputEvents: (JSONLOutput & Partial<Pick<NodeJS.EventEmitter, "on" | "removeListener">>) | undefined;
  let reading: Promise<void> | undefined;
  let closing: Promise<void> | undefined;
  let closed = false, token = "", sequence = 0;
  let failure: Error | undefined;
  let writing = Promise.resolve();
  const pending = new Map<string, {
    method: string; context: ExecutionBackendContextV4;
    resolve(value: unknown): void; reject(error: Error): void;
  }>();
  const bridges = new Set<Promise<void>>();
  const fail = (error: unknown) => {
    failure ??= error instanceof Error ? error : new Error(String(error));
    lifetime.abort(failure);
    for (const call of pending.values()) call.reject(failure);
    pending.clear();
    // A terminal transport error owns physical cleanup; an idle outer host
    // must not keep the failed peer and its processes alive until another EOF.
    if (!closed) void close().catch(() => {});
  };
  const send = (frame: object): Promise<void> => {
    const line = JSON.stringify({ ...frame, token }) + "\n";
    if (Buffer.byteLength(line) > EXECUTION_BACKEND_MAX_FRAME_BYTES) return Promise.reject(new Error("execution delegate request exceeds transport limit"));
    const next = writing.then(async () => {
      if (failure) throw failure;
      if (!transport) throw new Error("execution delegate is unavailable");
      await transport.output.write(line);
    });
    writing = next.catch(fail);
    return next;
  };
  const read = async (): Promise<void> => {
    try {
      for await (const line of executionFrames(transport!.input, lifetime.signal)) {
        const response = JSON.parse(line) as { id?: string; parent_id?: string; method?: string; data?: unknown; error?: string };
        if (!response || typeof response !== "object") throw new Error("invalid execution delegate response");
        if (response.method === "process") {
          if (!initializeContext?.lifetimeSignal.aborted) await initializeContext?.emitProcess(response.data as ExecutionProcessEventV4);
          continue;
        }
        if (response.method === "tool") {
          if (typeof response.id !== "string" || !response.id) throw new Error("invalid execution delegate callback identity");
          const call = pending.get(response.parent_id ?? "");
          const bridge = (async () => {
            const reply: { id: string; data?: ToolResult; error?: string } = { id: response.id! };
            try {
              if (!call || call.method !== "run_code" || closed) throw new Error("parent execution scope is closed");
              call.context.signal.throwIfAborted();
              reply.data = await call.context.invokeTool(response.data as ToolServiceCall);
            } catch (error) { reply.error = error instanceof Error ? error.message : String(error); }
            await send({ id: reply.id, method: "callback", data: reply });
          })();
          bridges.add(bridge);
          void bridge.then(() => bridges.delete(bridge), error => { bridges.delete(bridge); fail(error); });
          continue;
        }
        if (typeof response.id !== "string" || response.method) throw new Error("invalid execution delegate response identity");
        const call = pending.get(response.id);
        if (!call) continue;
        pending.delete(response.id);
        if (response.error) call.reject(new Error(response.error)); else call.resolve(response.data);
      }
      if (!closed || pending.size) fail(new Error("execution delegate disconnected; the last operation may have completed"));
    } catch (error) { if (!closed || pending.size) fail(error); }
  };
  const call = async (method: string, data: unknown, context: ExecutionBackendContextV4): Promise<unknown> => {
    if (closed) throw new Error("execution delegate is closed");
    if (failure) throw failure;
    if (!transport) throw new Error("execution delegate is not initialized");
    if (JSON.stringify(context.binding) !== bindingJSON) throw new Error("execution delegate binding identity mismatch");
    context.signal.throwIfAborted();
    const id = String(++sequence);
    const reply = new Promise<unknown>((resolve, reject) => pending.set(id, { method, context, resolve, reject }));
    let timer: ReturnType<typeof setTimeout> | undefined;
    const abort = () => {
      void send({ id, method: "cancel" }).catch(fail);
      // As with native process admission, wait for the physical terminal reply.
      // A non-cooperating peer is closed, not reused for another uncertain run.
      timer = setTimeout(() => {
        fail(new Error("execution delegate cancellation did not drain; outcome may be unknown"));
      }, 35_000);
    };
    context.signal.addEventListener("abort", abort, { once: true });
    try {
      const [, result] = await Promise.all([send({ id, method, data }), reply]);
      return result;
    } finally {
      pending.delete(id); context.signal.removeEventListener("abort", abort);
      if (timer) clearTimeout(timer);
    }
  };
  const close = (): Promise<void> => {
    if (closing) return closing;
    closed = true;
    closing = (async () => {
      try { await (transport ?? await opening)?.close(); }
      finally {
        fail(new Error("execution delegate is closed"));
        await reading;
        await Promise.allSettled([...bridges]);
        await writing;
        outputEvents?.removeListener?.("error", fail);
      }
    })();
    return closing;
  };
  const methods: ExecutionBackendV4["methods"] = {};
  for (const method of ["execute", "run_code", "process/start", "process/list", "process/get", "process/read", "process/write", "process/resize", "process/stop", "process/input_available", "process/completion_pending", "process/completion_delivered", "process/recheck", "process/cancel_notifications", "process/completion_mode", "process/recheck_delivered", "process/cleanup", "export/read", "export/close"] as const) {
    Object.defineProperty(methods, method, { enumerable: true, value: (input: unknown, context: ExecutionBackendContextV4) => call(method, input, context) });
  }
  return {
    capabilities, methods, close,
    async initialize(binding, context) {
      if (closed) throw new Error("execution delegate is closed");
      const serialized = JSON.stringify(binding);
      if (JSON.stringify(context.binding) !== serialized) throw new Error("execution delegate binding identity mismatch");
      if (bindingJSON !== undefined && bindingJSON !== serialized) throw new Error("execution delegate binding identity mismatch");
      bindingJSON = serialized;
      if (!initializing) initializing = (async () => {
        try {
          initializeContext = context;
          opening = (async () => {
            const { randomBytes } = await import("node:crypto");
            context.signal.throwIfAborted();
            token = randomBytes(32).toString("hex");
            return options.transport ?? await spawnExecutionPeer(options as { command: readonly string[]; cwd?: string; env?: Readonly<Record<string, string>> }, shutdownGraceMS);
          })();
          transport = await opening;
          if (closed) throw new Error("execution delegate is closed");
          outputEvents = transport.output as typeof outputEvents;
          outputEvents?.on?.("error", fail);
          reading = read();
          const hello = await call("initialize", binding, context) as { version?: number; root?: string; capabilities?: string[] };
          if (!hello || hello.version !== EXECUTION_BACKEND_PROTOCOL_VERSION || hello.root !== binding.root) throw new Error("execution delegate protocol or workspace binding mismatch");
          for (const capability of capabilities) if (!Array.isArray(hello.capabilities) || !hello.capabilities.includes(capability)) throw new Error(`execution delegate capability ${capability} is unavailable`);
        } catch (error) { fail(error); throw error; }
      })();
      return await initializing;
    },
  };
}

async function spawnExecutionPeer(options: { command: readonly string[]; cwd?: string; env?: Readonly<Record<string, string>> }, shutdownGraceMS: number): Promise<ExecutionBackendPeerTransportV4> {
  const { spawn } = await import("node:child_process");
  const child = spawn(options.command[0]!, [...options.command.slice(1)], { cwd: options.cwd, env: options.env, stdio: ["pipe", "pipe", "pipe"] });
  let inputFailure: Error | undefined;
  // Writable streams also emit an error after invoking the failing write's
  // callback. Keep that event observed instead of crashing before cleanup.
  child.stdin.on("error", error => { inputFailure = error; });
  let diagnostic = "";
  child.stderr.on("data", data => { diagnostic = (diagnostic + String(data)).slice(-8192); });
  const exited = new Promise<{ code: number | null; signal: string | null }>((resolve, reject) => {
    child.once("error", reject); child.once("close", (code, signal) => resolve({ code, signal }));
  });
  // Spawn failure may happen before a caller begins close().
  void exited.catch(() => {});
  await new Promise<void>((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
  let closing: Promise<void> | undefined;
  return {
    input: child.stdout,
    output: { write: (chunk: string) => new Promise<void>((resolve, reject) => {
      if (inputFailure) { reject(inputFailure); return; }
      child.stdin.write(chunk, error => error ? reject(error) : resolve());
    }) },
    close() {
      if (!closing) closing = (async () => {
        child.stdin.end();
        let expired = false;
        const timeout = setTimeout(() => { expired = true; child.kill("SIGKILL"); }, shutdownGraceMS);
        try {
          const result = await exited;
          if (expired || result.code !== 0) throw new Error(`execution delegate cleanup failed${expired ? " (timed out; resource outcome may be unknown)" : ""}: ${diagnostic || result.signal || result.code}`);
        } finally { clearTimeout(timeout); }
      })();
      return closing;
    },
  };
}

const workspaceTools = new Set(["read_file", "write_file", "edit_file", "apply_patch", "list_files", "grep", "glob", "bash", "process", "git", "present_artifact"]);
type Frame = { id: string; method: string; token?: string; data?: unknown };
type Callback = { resolve(value: ToolResult): void; reject(error: Error): void };

/** Serve an explicitly trusted execution backend over the existing version-4
 * transport. This is not a sandbox: the provider owns filesystem policy, shell
 * confinement, credentials, receipt durability and process-tree cleanup.
 * Unsupported methods fail, effects are never retried, and initialization pins
 * one session/environment for this connection. stdout is protocol-only.
 */
export async function runExecutionBackendV4(backend: ExecutionBackendV4, options: { input?: JSONLInput; output?: JSONLOutput } = {}): Promise<void> {
  const { timingSafeEqual, randomUUID } = await import("node:crypto");
  const { isAbsolute } = await import("node:path");
  if (backend.capabilities?.includes("process.start.v1") && (!backend.methods["process/start"] || !backend.methods["process/list"] || !backend.close)) {
    throw new Error("process.start.v1 requires process/start, process/list and close handlers with durable operation_id records and owned-process cleanup");
  }
  if (backend.capabilities?.includes("code.stateless.v1") && !backend.methods.run_code) throw new Error("code.stateless.v1 requires run_code");
  const capabilities = Object.freeze([...backend.capabilities ?? []]);
  const input = options.input ?? process.stdin;
  const output = options.output ?? { write: (chunk: string) => new Promise<void>((resolve, reject) => {
    process.stdout.write(chunk, error => error ? reject(error) : resolve());
  }) };
  const lifetime = new AbortController();
  const pending = new Map<string, { controller: AbortController; done: Promise<void> }>();
  const callbacks = new Map<string, Callback>();
  let token: Buffer | undefined;
  let binding: ExecutionBackendBindingV4 | undefined;
  let bindingJSON: string | undefined;
  let initializing = false;
  let initializationFailed = false;
  let writing = Promise.resolve();
  let outputFailure: unknown;
  const outputEvents = (options.output ?? process.stdout) as JSONLOutput & Partial<Pick<NodeJS.EventEmitter, "on" | "removeListener">>;
  const onOutputError = (error: unknown) => { outputFailure = error; lifetime.abort(error); };
  outputEvents.on?.("error", onOutputError);
  const write = (frame: object): Promise<void> => {
    const line = JSON.stringify(frame) + "\n";
    if (Buffer.byteLength(line) > EXECUTION_BACKEND_MAX_FRAME_BYTES) return Promise.reject(new Error("execution response exceeds transport limit"));
    const next = writing.then(async () => { await output.write(line); });
    writing = next.catch(error => { outputFailure = error; lifetime.abort(error); });
    return next;
  };
  const verifyProcess = (record: unknown, selected: ExecutionBackendBindingV4): void => {
    const value = record as Partial<ExecutionProcessV4> | undefined;
    if (!value || typeof value.id !== "string" || !value.id || value.root_thread_id !== selected.session) throw new Error("process belongs to another execution session");
  };
  const normalizeBinding = (value: unknown): ExecutionBackendBindingV4 => {
    const raw = value as Partial<ExecutionBackendBindingV4> | undefined;
    if (!raw || raw.version !== EXECUTION_BACKEND_PROTOCOL_VERSION) throw new Error("execution worker protocol mismatch: expected version 4");
    if (typeof raw.root !== "string" || !isAbsolute(raw.root) || raw.root.includes("\0") ||
      typeof raw.session !== "string" || !raw.session || /[/\\\0]/.test(raw.session) || raw.session === "." || raw.session === "..") throw new Error("invalid execution worker identity or workspace");
    if (raw.identity !== undefined && (typeof raw.identity !== "string" || /[/\\\0]/.test(raw.identity) || raw.identity === "." || raw.identity === "..")) throw new Error("invalid environment identity");
    for (const key of ["require_existing_workspace", "remove_workspace_on_exit"] as const) {
      if (raw[key] !== undefined && typeof raw[key] !== "boolean") throw new Error(`invalid ${key}`);
    }
    let environment: Readonly<Record<string, string>> | undefined;
    if (raw.environment !== undefined) {
      if (!raw.environment || typeof raw.environment !== "object" || Array.isArray(raw.environment)) throw new Error("invalid execution environment values");
      const values: Record<string, string> = {};
      for (const [name, value] of Object.entries(raw.environment).sort(([a], [b]) => a.localeCompare(b))) {
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || typeof value !== "string" || value.includes("\0")) throw new Error("invalid execution environment value");
        Object.defineProperty(values, name, { value, enumerable: true });
      }
      environment = Object.freeze(values);
    }
    return Object.freeze({ version: 4, root: raw.root, session: raw.session,
      ...(raw.identity === undefined ? {} : { identity: raw.identity }),
      ...(environment === undefined ? {} : { environment }),
      ...(raw.require_existing_workspace === undefined ? {} : { require_existing_workspace: raw.require_existing_workspace }),
      ...(raw.remove_workspace_on_exit === undefined ? {} : { remove_workspace_on_exit: raw.remove_workspace_on_exit }),
    });
  };
  const context = (request: Frame, selected: ExecutionBackendBindingV4, controller: AbortController): ExecutionBackendContextV4 => Object.freeze({
    requestId: request.id, binding: selected, signal: controller.signal, lifetimeSignal: lifetime.signal,
    async emitProcess(event: ExecutionProcessEventV4) {
      lifetime.signal.throwIfAborted();
      if (!binding) throw new Error("execution worker is not initialized");
      verifyProcess(event.Process, selected);
      await write({ id: "", method: "process", data: event });
    },
    async invokeTool(call: ToolServiceCall) {
      controller.signal.throwIfAborted();
      if (request.method !== "run_code" || !pending.has(request.id)) throw new Error("parent code execution scope is closed");
      const id = randomUUID();
      const reply = new Promise<ToolResult>((resolve, reject) => callbacks.set(id, { resolve, reject }));
      const abort = () => { callbacks.get(id)?.reject(new Error("parent execution cancelled")); callbacks.delete(id); };
      controller.signal.addEventListener("abort", abort, { once: true });
      try {
        const [, result] = await Promise.all([write({ id, parent_id: request.id, method: "tool", data: call }), reply]);
        return result;
      } finally { callbacks.delete(id); controller.signal.removeEventListener("abort", abort); }
    },
  });
  const invoke = async (request: Frame, controller: AbortController): Promise<unknown> => {
    if (request.method === "initialize") {
      if (initializing || initializationFailed) throw new Error("execution initialization is pending or failed; open a new transport");
      const selected = normalizeBinding(request.data), serialized = JSON.stringify(selected);
      if (binding) {
        if (serialized !== bindingJSON) throw new Error("execution worker binding identity mismatch");
      } else {
        initializing = true;
        try {
          await backend.initialize(selected, context(request, selected, controller));
          controller.signal.throwIfAborted();
          binding = selected; bindingJSON = serialized;
        } catch (error) { initializationFailed = true; throw error; }
        finally { initializing = false; }
      }
      return { version: EXECUTION_BACKEND_PROTOCOL_VERSION, root: binding.root, capabilities };
    }
    if (!binding || initializing || initializationFailed) throw new Error("execution worker is not initialized");
    const method = request.method as keyof ExecutionBackendMethodsV4;
    if (!Object.hasOwn(backend.methods, method) || !backend.methods[method]) throw new Error(`unsupported execution backend method ${method}`);
    if (method === "execute" || method === "run_code" || method === "process/start") {
      const data = request.data as Partial<ExecutionToolRequestV4> | undefined;
      if (!data || typeof data.actor !== "string" || !data.actor || !["", "standard", "read_only", "unconfined"].includes(data.permission_mode ?? "")) throw new Error("invalid execution actor or permission mode");
      if (method === "execute" && (!data.call || !workspaceTools.has(data.call.name))) throw new Error("tool is not an execution environment tool");
    }
    const handler = backend.methods[method] as (input: unknown, ctx: ExecutionBackendContextV4) => unknown;
    const result = await handler(request.data, context(request, binding, controller));
    if (method.startsWith("process/")) {
      if (method === "process/list") {
        if (result !== null && !Array.isArray(result)) throw new Error("invalid process list");
        for (const record of result ?? []) verifyProcess(record, binding);
      } else if (method === "process/read") verifyProcess((result as ExecutionProcessOutputV4)?.Process, binding);
      else if (method === "process/cleanup") {
        const records = (result as { Cleaned?: unknown[] | null })?.Cleaned;
        if (records !== null && !Array.isArray(records)) throw new Error("invalid process cleanup result");
        for (const record of records ?? []) verifyProcess(record, binding);
      } else if (method !== "process/input_available" && method !== "process/completion_pending") verifyProcess(result, binding);
      if (method === "process/start") {
        const start = request.data as ExecutionProcessStartV4;
        if ((result as ExecutionProcessV4).owner_id !== start.actor) throw new Error("process start returned another actor's process");
        const operation = start.operation_id;
        if (operation && (result as ExecutionProcessV4).operation_id !== operation) throw new Error("process start did not preserve operation_id; outcome may be unknown");
      }
    }
    return result;
  };
  const dispatch = async (value: unknown): Promise<void> => {
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid execution request frame");
    const request = value as Frame;
    if (typeof request.id !== "string" || !request.id || typeof request.method !== "string" || !request.method) throw new Error("execution request requires id and method");
    const supplied = typeof request.token === "string" ? Buffer.from(request.token) : Buffer.alloc(0);
    if (!token && request.method === "initialize" && supplied.length >= 32) token = supplied;
    if (!token || supplied.length !== token.length || !timingSafeEqual(token, supplied)) {
      await write({ id: request.id, error: "execution transport authentication failed" }); return;
    }
    if (request.method === "cancel") { pending.get(request.id)?.controller.abort(new Error("execution cancelled")); return; }
    if (request.method === "callback") {
      const reply = callbacks.get(request.id);
      if (!reply) return;
      const data = request.data as { id?: string; data?: ToolResult; error?: string } | undefined;
      if (!data || data.id !== request.id) { reply.reject(new Error("invalid execution callback identity")); callbacks.delete(request.id); return; }
      callbacks.delete(request.id);
      if (data.error) reply.reject(new Error(data.error)); else reply.resolve(data.data ?? {});
      return;
    }
    if (pending.has(request.id)) { await write({ id: request.id, error: "missing or duplicate request id" }); return; }
    const controller = new AbortController();
    const abort = () => controller.abort(lifetime.signal.reason);
    lifetime.signal.addEventListener("abort", abort, { once: true });
    // Defer invocation until its identity is registered, including synchronous providers.
    const done = Promise.resolve().then(async () => {
      let response: object;
      try { response = { id: request.id, data: await invoke(request, controller) }; }
      catch (error) { response = { id: request.id, error: error instanceof Error ? error.message : String(error) }; }
      // Revoke callback authority before a peer can observe the terminal reply.
      controller.abort(new Error("execution ended"));
      try { await write(response); }
      finally {
        pending.delete(request.id); lifetime.signal.removeEventListener("abort", abort);
      }
    });
    pending.set(request.id, { controller, done });
    // Output failures cancel the transport and are rethrown after draining.
    void done.catch(error => { outputFailure = error; lifetime.abort(error); });
  };
  try {
    for await (const frame of executionFrames(input, lifetime.signal)) {
      lifetime.signal.throwIfAborted();
      await dispatch(JSON.parse(frame));
    }
  } finally {
    lifetime.abort(new Error("execution transport closed"));
    await Promise.allSettled([...pending.values()].map(value => value.done));
    try { await backend.close?.(); }
    finally { await writing; outputEvents.removeListener?.("error", onOutputError); }
  }
  if (outputFailure) throw outputFailure;
}

async function* executionFrames(input: JSONLInput, signal: AbortSignal): AsyncGenerator<string> {
  let parts: Buffer[] = [], size = 0;
  const iterator = input[Symbol.asyncIterator]();
  try {
    while (true) {
      signal.throwIfAborted();
      const next = await new Promise<IteratorResult<Uint8Array | string>>((resolve, reject) => {
        const abort = () => reject(signal.reason);
        signal.addEventListener("abort", abort, { once: true });
        Promise.resolve(iterator.next()).then(value => {
          signal.removeEventListener("abort", abort); resolve(value);
        }, error => { signal.removeEventListener("abort", abort); reject(error); });
      });
      if (next.done) break;
      const value = next.value;
      const bytes = typeof value === "string" ? Buffer.from(value) : Buffer.from(value.buffer, value.byteOffset, value.byteLength);
      let start = 0;
      while (start < bytes.length) {
        const newline = bytes.indexOf(10, start), end = newline < 0 ? bytes.length : newline;
        const part = bytes.subarray(start, end);
        size += part.length;
        if (size + 1 > EXECUTION_BACKEND_MAX_FRAME_BYTES) throw new Error("execution request exceeds transport limit");
        parts.push(part);
        if (newline < 0) break;
        yield Buffer.concat(parts, size).toString("utf8");
        parts = []; size = 0; start = newline + 1;
      }
    }
    if (size) yield Buffer.concat(parts, size).toString("utf8");
  } finally {
    // An output failure must still release provider resources when a custom
    // input iterable cannot interrupt its outstanding next().
    if (signal.aborted) {
      if ("destroy" in input && typeof input.destroy === "function") input.destroy();
      void Promise.resolve(iterator.return?.()).catch(() => {});
    } else await iterator.return?.();
  }
}
