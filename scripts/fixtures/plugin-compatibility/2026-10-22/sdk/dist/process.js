export function requireProcessService() {
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
export async function startProcess(host, input) {
    return await host.call("host.service.call", { service: "host.process", method: "start", params: input });
}
/** Read merged stdout/stderr with a byte cursor; 4 bytes to 1 MiB and a 30s wait.
 * Only processes owned by this plugin generation can be accessed.
 */
export async function readProcess(host, input) {
    return await host.call("host.service.call", { service: "host.process", method: "read", params: input });
}
/** Deliver an event to the process's captured conversation, including private
 * child sessions. The host selects the recipient; callers cannot substitute one.
 * request_id has the same retry/deduplication contract as host.session.send.
 */
export async function sendProcessEvent(host, process_id, message) {
    return await host.call("host.service.call", { service: "host.process", method: "send", params: { process_id, message } });
}
export async function stopProcess(host, process_id) {
    return await host.call("host.service.call", { service: "host.process", method: "stop", params: { process_id } });
}
