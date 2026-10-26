import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

/** Retirement is normal navigation, not a failed action in the new workspace. */
export function showApplicationActionError(error: unknown): void {
  if (isPluginLifecycleCancellation(error)) return;
  // An abort without proven retirement is still a failure. Keep its transport
  // details in diagnostics rather than presenting an opaque browser exception.
  if ((error instanceof Error || error instanceof DOMException) && error.name === "AbortError") {
    console.error("Application action interrupted", error);
    showErrorToast(translateCurrent("common.actionInterrupted"));
    return;
  }
  showErrorToast(error);
}
