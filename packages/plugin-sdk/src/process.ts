import type { HostServiceContracts, RuntimeHost, ServiceRequirement } from "./index.js";

export interface PluginProcessSnapshot {
  id: string;
  session_id: string;
  state: "starting" | "running" | "stopping" | "stopped" | "failed";
  exit_code: number;
  reason?: "timeout" | "requested_stop" | "generation_closed" | "session_closed";
  error?: string;
}
export interface PluginProcessOutput {
  process: PluginProcessSnapshot;
  output: string;
  next_offset: number;
  total_bytes: number;
  truncated: boolean;
}

export function requireProcessService(): ServiceRequirement {
  return { name: "host.process", major_version: 1, required: true };
}

/** Start from a live conversation tool callback; the host retains its shell
 * permissions, working directory and sandbox. The process belongs to this
 * plugin generation after the callback returns. Repeating a key with identical
 * arguments returns the existing process while its receipt is retained.
 * Zero timeout means no deadline. Disable, reload and host shutdown stop the
 * process tree, as does conversation archival/deletion. With follow_parent,
 * a completed private child hands its recipient to its parent conversation.
 * Output and exit do not themselves wake a conversation.
 */
export async function startProcess(host: RuntimeHost, input: {
  key: string; command: string; cwd?: string; timeout_ms?: number; follow_parent?: boolean;
}): Promise<PluginProcessSnapshot> {
  return await host.call("host.service.call", { service: "host.process", method: "start", params: input }) as PluginProcessSnapshot;
}

/** Read merged stdout/stderr with a byte cursor; 4 bytes to 1 MiB and a 30s wait.
 * Only processes owned by this plugin generation can be accessed.
 */
export async function readProcess(host: RuntimeHost, input: {
  process_id: string; offset_bytes?: number; max_bytes?: number; wait_ms?: number;
}): Promise<PluginProcessOutput> {
  return await host.call("host.service.call", { service: "host.process", method: "read", params: input }) as PluginProcessOutput;
}

/** Deliver an event to the process's captured conversation, including private
 * child sessions. The host selects the recipient; callers cannot substitute one.
 * request_id has the same retry/deduplication contract as host.session.send.
 */
export async function sendProcessEvent(host: RuntimeHost, process_id: string,
  message: Omit<HostServiceContracts["host.session.send"]["params"], "session_id">,
): Promise<HostServiceContracts["host.session.send"]["result"]> {
  return await host.call("host.service.call", { service: "host.process", method: "send", params: { process_id, message } }) as HostServiceContracts["host.session.send"]["result"];
}

export async function stopProcess(host: RuntimeHost, process_id: string): Promise<PluginProcessSnapshot> {
  return await host.call("host.service.call", { service: "host.process", method: "stop", params: { process_id } }) as PluginProcessSnapshot;
}
