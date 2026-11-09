// Private, per-view port. Capture sources come only from the desktop host.
export type LinuxPreviewCommand =
  | { type: "capture"; epoch: number; sourceID: string }
  | { type: "suspend"; epoch: number; clear: boolean };

export type LinuxPreviewReply =
  | { type: "ready" }
  | { type: "frame"; epoch: number; width: number; height: number }
  | { type: "ended"; epoch: number }
  | { type: "error"; epoch: number; message: string };
