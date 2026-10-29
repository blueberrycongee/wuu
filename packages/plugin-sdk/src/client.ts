import type { RuntimeHost, ServiceRequirement } from "./index.js";

/** Host-authenticated, content-free notification sent as plugin/client/invalidated.
 * Consumers re-read scoped plugin state; missed hints are recovered by polling.
 */
export interface PluginClientInvalidation {
  readonly plugin_id: string;
  readonly fingerprint: string;
  readonly user_space_id: string;
  readonly product_domain: "personal" | "coding";
  readonly topic: string;
}

export function requireClientInvalidationService(): ServiceRequirement {
  return { name: "host.client.invalidate", major_version: 1, required: true };
}

/** Invalidate plugin-owned client state after a durable write, without starting
 * a turn, creating history, or sending user attention. Topics are lowercase
 * identifiers matching [a-z][a-z0-9.-]{0,63}. Delivery is best effort: failure
 * must not be interpreted as failure of an already committed state change.
 */
export async function invalidateClient(host: RuntimeHost, topic: string): Promise<void> {
  await host.call("host.service.call", { service: "host.client.invalidate", method: "call", params: { topic } });
}
