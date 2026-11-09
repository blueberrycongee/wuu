/** Expected retirement, proven by the owning generation rather than error text. */
export class PluginLifecycleCancellationError extends Error {
  readonly code = "WUU_PLUGIN_SCOPE_RETIRED";
  constructor(cause?: unknown) {
    super("Plugin scope is no longer active (retired)", { cause });
    this.name = "AbortError";
  }
}
/** The old scope retired before the action reached its provider. */
export class PluginActionNotStartedError extends PluginLifecycleCancellationError {}
export function isPluginLifecycleCancellation(error: unknown): error is PluginLifecycleCancellationError {
  return error instanceof PluginLifecycleCancellationError;
}
