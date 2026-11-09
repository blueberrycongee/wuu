import type { HostServiceContracts, RuntimeHost, ServiceRequirement } from "./index.js";

/** Pinned execution location, not a selector or an authorization credential. */
export interface PluginProcessEnvironment {
  /** "local" or the selected execution backend's identifier. */
  backend: string;
  /** Workspace root on that backend; remote paths are never host paths. */
  root: string;
  /** Opaque selected-environment identity when supplied by the backend. */
  identity?: string;
}

export interface PluginProcessSnapshot {
  id: string;
  /** Host-minted operation identity for reconciling an uncertain remote start. */
  operation_id?: string;
  session_id: string;
  state: "starting" | "running" | "stopping" | "stopped" | "failed";
  exit_code: number;
  reason?: "timeout" | "requested_stop" | "generation_closed" | "session_closed";
  error?: string;
  /** Captured at acceptance; changing defaults or the event recipient does not
   * move this process. Absent on hosts before host.process 1.1, not proof of local execution. */
  environment?: PluginProcessEnvironment;
}
export interface PluginProcessOutput {
  /** Present when a separate stream was requested (host.process 1.2). */
  stream?: "stdout" | "stderr";
  process: PluginProcessSnapshot;
  output: string;
  next_offset: number;
  total_bytes: number;
  truncated: boolean;
}

export interface PluginProcessStartInput {
  /** Unique within this conversation and plugin generation while retained. */
  key: string;
  command: string;
  /** Interpreted inside the selected environment, not on the plugin's machine. */
  cwd?: string;
  /** Non-negative process deadline in milliseconds; zero or absent means none. */
  timeout_ms?: number;
  /** Transfer event delivery to the parent when a private child completes. */
  follow_parent?: boolean;
}

export interface PluginProcessReadInput {
  /** Independent byte cursor for this stream; omission reads merged output. */
  stream?: "stdout" | "stderr";
  process_id: string;
  offset_bytes?: number;
  max_bytes?: number;
  wait_ms?: number;
}

export interface PluginProcessCallOptions {
  /** Cancels this request, not an already accepted process. Use stopProcess for
   * explicit process cancellation. Cannot remove the callback's own signal. */
  signal?: AbortSignal;
}

export function requireProcessService(): ServiceRequirement {
  return { name: "host.process", major_version: 1, required: true };
}

/** Inspect the live tool callback's pinned execution location without connecting
 * or provisioning it. Requires host.process 1.1; an older host fails explicitly.
 * No caller-supplied session, workspace or backend can redirect this query.
 */
export async function getProcessEnvironment(host: RuntimeHost, options?: PluginProcessCallOptions): Promise<PluginProcessEnvironment> {
  return await host.call("host.service.call", { service: "host.process", method: "environment", params: {} }, options) as PluginProcessEnvironment;
}

/** Start from a live conversation tool callback; the host retains its shell
 * permissions and pinned local or remote workspace. A remote failure never
 * falls back to local execution. The accepted process belongs to this plugin
 * generation after the callback returns. Repeating a key with identical
 * arguments returns the existing process while its receipt is retained.
 * This helper never retries an unknown outcome. A retained failed-start receipt
 * returns the same error, not another launch. Inspect/reconcile an uncertain
 * operation before another start rather than assuming an error means no effects.
 * Zero timeout means no deadline. Disable, generation closure and host shutdown
 * stop the process tree, as does conversation archival/deletion. With follow_parent,
 * a completed private child hands its recipient to its parent conversation.
 * Output and exit do not themselves wake a conversation.
 */
export async function startProcess(host: RuntimeHost, input: PluginProcessStartInput, options?: PluginProcessCallOptions): Promise<PluginProcessSnapshot> {
  return await host.call("host.service.call", { service: "host.process", method: "start", params: input }, options) as PluginProcessSnapshot;
}

/** Read output with a byte cursor; 4 bytes to 1 MiB and a 30s wait.
 * Separate stdout/stderr requires host.process 1.2 and backend support.
 * Unsupported stream selection fails explicitly; it never returns merged data.
 * Only processes owned by this plugin generation can be accessed.
 */
export async function readProcess(host: RuntimeHost, input: PluginProcessReadInput, options?: PluginProcessCallOptions): Promise<PluginProcessOutput> {
  const result = await host.call("host.service.call", { service: "host.process", method: "read", params: input }, options) as PluginProcessOutput;
  if (input.stream && result.stream !== input.stream) throw new Error("host.process does not support separate output streams");
  return result;
}

/** Deliver an event to the process's captured conversation, including private
 * child sessions. The host selects the recipient; callers cannot substitute one.
 * request_id has the same retry/deduplication contract as host.session.send.
 */
export async function sendProcessEvent(host: RuntimeHost, process_id: string,
  message: Omit<HostServiceContracts["host.session.send"]["params"], "session_id">,
  options?: PluginProcessCallOptions,
): Promise<HostServiceContracts["host.session.send"]["result"]> {
  return await host.call("host.service.call", { service: "host.process", method: "send", params: { process_id, message } }, options) as HostServiceContracts["host.session.send"]["result"];
}

export async function stopProcess(host: RuntimeHost, process_id: string, options?: PluginProcessCallOptions): Promise<PluginProcessSnapshot> {
  return await host.call("host.service.call", { service: "host.process", method: "stop", params: { process_id } }, options) as PluginProcessSnapshot;
}
