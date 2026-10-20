export type WebWorkspaceDownloadState = {
  id: string;
  websiteId: string;
  filename: string;
  savePath: string;
  receivedBytes: number;
  totalBytes: number;
  state: "progressing" | "completed" | "cancelled" | "interrupted";
  paused: boolean;
  canResume: boolean;
  done: boolean;
  startedAt: number;
  /** The record remains usable in memory but cannot be saved in download history. */
  persistenceError?: "read" | "write";
};

export type WebWorkspaceDownloadCommand = "pause" | "resume" | "cancel" | "open" | "reveal";
