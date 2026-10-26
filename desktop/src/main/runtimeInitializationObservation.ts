import type { InitializeResult } from "../shared/protocol";

export type InitializationObservation = { phase: "started"; requestId: number }
  | { phase: "completed"; requestId: number; initialized: InitializeResult };
export type InitializationRecovery = { phase: "started"; recoveryId: string; afterRequestId: number }
  | { phase: "completed"; recoveryId: string; initialized: InitializeResult };

/** Orders ordinary initialize reads and main-owned graph recovery in one document. */
export class RuntimeInitializationObservation {
  private request = 0;
  private recovery?: { id: string; request: number };
  private readonly listeners = new Set<(event: InitializationObservation) => void>();
  subscribe = (listener: (event: InitializationObservation) => void): (() => void) => {
    this.listeners.add(listener); return () => this.listeners.delete(listener);
  };
  begin(): number {
    const requestId = ++this.request;
    for (const listener of this.listeners) listener({ phase: "started", requestId });
    return requestId;
  }
  complete(requestId: number, initialized: InitializeResult): void {
    if (requestId !== this.request) return;
    for (const listener of this.listeners) listener({ phase: "completed", requestId, initialized });
  }
  recover(event: InitializationRecovery): void {
    if (event.phase === "started") {
      // A newer renderer request can have started before main receives its IPC.
      if (event.afterRequestId !== this.request) return;
      this.recovery = { id: event.recoveryId, request: this.begin() };
    } else if (this.recovery?.id === event.recoveryId) {
      this.complete(this.recovery.request, event.initialized);
      this.recovery = undefined;
    }
  }
}
