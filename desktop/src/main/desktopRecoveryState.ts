import type { RecoveryMode, RecoveryRetryState } from "../shared/desktopRecovery";

export function recoveryLaunchArguments(args: readonly string[], mode: RecoveryMode): string[] {
  return [...args.filter(arg => arg !== "--safe-mode"), ...(mode === "diagnostic" ? ["--safe-mode"] : [])];
}

// Export a schema, not logs: arbitrary error strings can contain credentials,
// local paths and user content. Unknown caller fields never enter the report.
export function recoveryDiagnostics(input: { version: string; platform: string; mode: RecoveryMode }) {
  return {
    format: "wuu-bootstrap-diagnostics-v1",
    version: /^[0-9][0-9A-Za-z.+-]{0,63}$/.test(input.version) ? input.version : "unknown",
    platform: ["darwin", "win32", "linux"].includes(input.platform) ? input.platform : "unknown",
    mode: input.mode === "diagnostic" ? "diagnostic" : "normal",
    scope: "bootstrap-only",
    excluded: ["logs", "configuration", "environment", "paths", "conversation-content"],
  };
}

/** Cancelling stops waiting, not an already-issued navigation or backend work. */
export class RecoveryRetry {
  private state: RecoveryRetryState = { phase: "idle", pending: false, attempts: 0 };
  private timer?: ReturnType<typeof setTimeout>;
  private disposed = false;
  constructor(private readonly run: () => Promise<void>, private readonly changed: () => void) {}
  snapshot(): RecoveryRetryState { return { ...this.state }; }
  start(): void {
    if (this.disposed || this.state.pending || this.state.attempts >= 3) return;
    this.state = { phase: "loading", pending: true, attempts: this.state.attempts + 1, startedAt: Date.now() };
    this.changed();
    this.timer = setTimeout(() => {
      this.state = { ...this.state, phase: "timed-out" };
      this.changed();
    }, 30_000);
    let operation: Promise<void>;
    try { operation = this.run(); } catch { operation = Promise.reject(new Error("Reload failed")); }
    void operation.then(() => this.finish("loaded"), () => this.finish("failed"));
  }
  private finish(phase: "loaded" | "failed"): void {
    clearTimeout(this.timer);
    if (this.disposed) return;
    this.state = { ...this.state, pending: false, ...(this.state.phase === "loading" ? { phase } : {}) };
    this.changed();
  }
  cancel(): void {
    if (!this.state.pending || this.state.phase !== "loading") return;
    clearTimeout(this.timer);
    this.state = { ...this.state, phase: "cancelled" };
    this.changed();
  }
  dispose(): void { this.disposed = true; clearTimeout(this.timer); }
}
