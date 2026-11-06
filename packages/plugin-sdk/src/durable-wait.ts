import type { RuntimeHost } from "./index.js";
import type { ModelToolCallV1 } from "./model.js";

export const DURABLE_WAIT_SERVICE = "host.durable-wait" as const;
export const DURABLE_WAIT_MODULE = "wuu.durable-wait" as const;
export interface DurableWaitIntent {
 readonly version: 1;
 readonly id: string;
 readonly expires_at: string;
 readonly question: string;
 readonly kind?: "approval" | "input" | "event";
 readonly batch_id?: string;
 readonly calls?: readonly ModelToolCallV1[];
}
export interface DurableWaitRecord {
 readonly version: 1;
 readonly id: string;
 readonly expires_at: string;
 readonly question: string;
 readonly kind?: string;
 readonly batch_id?: string;
 readonly calls_digest?: string;
 readonly owner: { readonly plugin_id: string; readonly session_id: string; readonly execution_id: string; readonly call_id: string };
 readonly status: "pending" | "approved" | "denied" | "expired" | "fenced" | "resolved";
 readonly effect_state: "not_started" | "unknown";
 readonly created_at: string;
 readonly decided_at?: string;
 readonly input_boundary?: { readonly session_id: string; readonly seq: number };
 readonly response_ref?: { readonly session_id: string; readonly seq: number };
 readonly decision?: "approve" | "deny";
}

/** Expiring host-owned metadata. Approval never bypasses ordinary permissions.
 * Only the authenticated human transport can decide; plugins cannot self-approve.
 * Registration requires a live driver gateway. Inspection survives that gateway.
 */
export function durableWait(host: RuntimeHost, execution_id: string) {
 async function call(service: string, method: string, params: object): Promise<DurableWaitRecord> { return await host.call("host.service.call", {service, method, params}) as DurableWaitRecord; }
 return {
 resolve: (wait_id: string) => call("driver.turn", "resolve_wait", { execution_id, wait_id }),
 register: (intent: DurableWaitIntent) => call("driver.turn", "register_wait", { execution_id, ...intent }),
 inspect: (session_id: string, wait_id: string) => call(DURABLE_WAIT_SERVICE, "inspect", { session_id, wait_id }),
 fence: (session_id: string, wait_id: string) => call(DURABLE_WAIT_SERVICE, "fence", { session_id, wait_id }),
 };
}
