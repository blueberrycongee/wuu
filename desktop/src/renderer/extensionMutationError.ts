import { translateCurrent } from "./i18n";
import { showErrorToast, showToast, toastErrorMessage } from "./Toast";

/** Reports a failed extension change, turning admission conflicts into guidance. */
export function showExtensionMutationError(error: unknown, fallback: string): void {
  // These admission errors currently cross IPC as text, without a typed code.
  // Keep their translation here, shared by refresh and package actions.
  const message = toastErrorMessage(error);
  const busyExecution = /^cannot .+ plugin packages while (?:a turn is running or background work remains on thread .+|another app-server is running a turn or background work)$/.test(message);
  const busyMutation = /^cannot .+ plugin packages while (?:another plugin change is running|another app-server is changing the plugin catalog)$/.test(message);
  if (busyExecution || busyMutation) {
    showToast({
      message: translateCurrent(busyExecution ? "skills.executionBusy" : "skills.changeBusy"),
      tone: "info",
      dedupeKey: busyExecution ? "extensions-execution-busy" : "extensions-change-busy",
    });
    return;
  }
  showErrorToast(error, fallback);
}
