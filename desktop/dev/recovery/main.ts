// Recovery window states, without Electron. The real self-contained document
// is served by the vite config; this module feeds synthetic snapshots through
// the same view layer the sandboxed preload uses.
// Query parameters: mode=normal|diagnostic (default normal), lang=en,
// phase=idle|loading|loaded|failed|cancelled|timed-out, attempts=0..3,
// theme=dark, restarting, noRetry, preview (diagnostics open).
import type { RecoveryRetryState, RecoverySnapshot } from "../../src/shared/desktopRecovery";
import { recoveryCopy, recoveryNotice, recoveryPreviewOpen, renderRecovery, setRecoveryPreview } from "../../src/main/recoveryView";

const params = new URLSearchParams(location.search);
const mode = params.get("mode") === "diagnostic" ? "diagnostic" : "normal";
const locale = params.get("lang") === "en" ? "en" : "zh";
const phases: RecoveryRetryState["phase"][] = ["idle", "loading", "loaded", "failed", "cancelled", "timed-out"];
const requested = params.get("phase");
const retryAvailable = mode === "normal" && !params.has("noRetry");

const retry: RecoveryRetryState = {
  phase: phases.includes(requested as RecoveryRetryState["phase"]) ? requested as RecoveryRetryState["phase"] : "idle",
  pending: (requested === "loading") && retryAvailable,
  attempts: Math.min(3, Math.max(0, Number(params.get("attempts") ?? 0) || 0)),
};

const snapshot = (): RecoverySnapshot => ({
  mode, locale, retryAvailable, restarting: restarting || false,
  retry: { ...retry },
  diagnosticPreview: JSON.stringify({
    format: "wuu-bootstrap-diagnostics-v1",
    version: "2026.1108.0",
    platform: "darwin",
    mode,
    scope: "bootstrap-only",
    excluded: ["logs", "configuration", "environment", "paths", "conversation-content"],
  }, null, 2) + "\n",
});

let restarting = params.has("restarting");
const render = () => renderRecovery(snapshot());
const c = () => recoveryCopy[locale];

const id = (value: string) => document.getElementById(value)!;
id("retry").addEventListener("click", () => {
  retry.attempts += 1; retry.phase = "loading"; retry.pending = true; render();
  setTimeout(() => { retry.pending = false; retry.phase = params.get("fails") ? "failed" : "loaded"; render(); }, 1500);
});
id("cancel").addEventListener("click", () => { retry.pending = false; retry.phase = "cancelled"; render(); });
id("safe").addEventListener("click", () => { restarting = true; render(); setTimeout(() => { restarting = false; recoveryNotice(c().restartCancelled); }, 900); });
id("normal").addEventListener("click", () => { restarting = true; render(); setTimeout(() => { restarting = false; recoveryNotice(c().restartCancelled); }, 900); });
id("export").addEventListener("click", () => recoveryNotice(c().exported, "success"));
id("preview-toggle").addEventListener("click", () => setRecoveryPreview(!recoveryPreviewOpen()));
if (params.has("preview")) setRecoveryPreview(true);
render();
