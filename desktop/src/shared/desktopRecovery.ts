export type RecoveryMode = "normal" | "diagnostic";
export type RecoveryRetryState = {
  phase: "idle" | "loading" | "loaded" | "failed" | "cancelled" | "timed-out";
  pending: boolean;
  attempts: number;
  startedAt?: number;
};
export type RecoverySnapshot = {
  mode: RecoveryMode;
  locale: "en" | "zh";
  retryAvailable: boolean;
  retry: RecoveryRetryState;
  restarting: boolean;
  diagnosticPreview: string;
};
export type RecoveryAction = "snapshot" | "retry" | "cancel" | "export" | "restart-safe" | "restart-normal";
