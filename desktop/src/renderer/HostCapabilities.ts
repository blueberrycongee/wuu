import type { WuuDesktopApi } from "../shared/protocol";

/** Required host operations are available unless the adapter explicitly
 * excludes them. Optional integrations still require a presence check. */
export function hostSupports(operation: keyof WuuDesktopApi): boolean {
  return !window.wuu?.unsupportedMethods?.includes(operation);
}
