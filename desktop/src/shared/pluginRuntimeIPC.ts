import { PluginLifecycleCancellationError } from "./PluginLifecycle";

import type { PluginRuntimeResponse } from "./protocol";
export type { PluginRuntimeResponse } from "./protocol";

export function decodePluginRuntimeResponse<T>(response: PluginRuntimeResponse<T>): T {
  if (response.ok) return response.value;
  if (response.scopeRetired) {
    throw new PluginLifecycleCancellationError(response.error);
  }
  throw Object.assign(new Error(response.error.message, { cause: response.error }), { code: response.error.code === "WUU_PLUGIN_SCOPE_RETIRED" ? "service_unavailable" : response.error.code });
}
