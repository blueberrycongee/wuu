import { AppServerResponseError } from "./appServerErrors";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import type { PluginRuntimeResponse } from "../shared/pluginRuntimeIPC";

/** Electron's rejected invoke strips custom fields, so encode the outer response. */
export async function encodePluginRuntimeResponse<T>(invoke: () => Promise<T>): Promise<PluginRuntimeResponse<T>> {
  try {
    return { ok: true, value: await invoke() };
  } catch (error) {
    return {
      ok: false,
      ...(error instanceof PluginLifecycleCancellationError ? { scopeRetired: true as const } : {}),
      error: {
        message: error instanceof Error ? error.message : String(error),
        code: error instanceof AppServerResponseError || error instanceof PluginLifecycleCancellationError ? error.code : undefined,
      },
    };
  }
}
