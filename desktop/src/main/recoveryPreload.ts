import { ipcRenderer } from "electron";
import type { RecoveryAction, RecoverySnapshot } from "../shared/desktopRecovery";
import { recoveryCopy, recoveryLocale, recoveryNotice, recoveryPreviewOpen, renderRecovery, setRecoveryPreview } from "./recoveryView";

type Outcome = "exported" | "exportCancelled" | "restartCancelled" | "actionFailed";

async function action(value: RecoveryAction): Promise<void> {
  try {
    const reply = await ipcRenderer.invoke("wuu:recovery-action", value) as { snapshot: RecoverySnapshot; outcome?: Outcome };
    renderRecovery(reply.snapshot);
    if (reply.outcome) {
      const tone = reply.outcome === "exported" ? "success" : reply.outcome === "actionFailed" ? "danger" : "muted";
      recoveryNotice(recoveryCopy[reply.snapshot.locale][reply.outcome], tone);
    }
  } catch { recoveryNotice(recoveryCopy[recoveryLocale()].actionFailed, "danger"); }
}

window.addEventListener("DOMContentLoaded", () => {
  for (const [id, name] of Object.entries({ retry: "retry", cancel: "cancel", safe: "restart-safe", normal: "restart-normal", export: "export" } as const)) {
    document.getElementById(id)!.addEventListener("click", () => { void action(name); });
  }
  document.getElementById("preview-toggle")!.addEventListener("click", () => setRecoveryPreview(!recoveryPreviewOpen()));
  ipcRenderer.on("wuu:recovery-changed", (_event, value: RecoverySnapshot) => renderRecovery(value));
  void action("snapshot");
});
