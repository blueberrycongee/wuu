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
export declare function requireProcessService(): ServiceRequirement;
/** Start from a live conversation tool callback; the host retains its shell
 * permissions, working directory and sandbox. The process belongs to this
 * plugin generation after the callback returns. Repeating a key with identical
 * arguments returns the existing process while its receipt is retained.
 * Zero timeout means no deadline. Disable, reload and host shutdown stop the
 * process tree, as does conversation archival/deletion. With follow_parent,
 * a completed private child hands its recipient to its parent conversation.
 * Output and exit do not themselves wake a conversation.
 */
export declare function startProcess(host: RuntimeHost, input: {
    key: string;
    command: string;
    cwd?: string;
    timeout_ms?: number;
    follow_parent?: boolean;
}): Promise<PluginProcessSnapshot>;
/** Read merged stdout/stderr with a byte cursor; 4 bytes to 1 MiB and a 30s wait.
 * Only processes owned by this plugin generation can be accessed.
 */
export declare function readProcess(host: RuntimeHost, input: {
    process_id: string;
    offset_bytes?: number;
    max_bytes?: number;
    wait_ms?: number;
}): Promise<PluginProcessOutput>;
/** Deliver an event to the process's captured conversation, including private
 * child sessions. The host selects the recipient; callers cannot substitute one.
 * request_id has the same retry/deduplication contract as host.session.send.
 */
export declare function sendProcessEvent(host: RuntimeHost, process_id: string, message: Omit<HostServiceContracts["host.session.send"]["params"], "session_id">): Promise<HostServiceContracts["host.session.send"]["result"]>;
export declare function stopProcess(host: RuntimeHost, process_id: string): Promise<PluginProcessSnapshot>;
