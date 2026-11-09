// Private helper/main/renderer wire contract, versioned by the WCU1 prefix.
export type NativePreviewFrame = {
  sequence: number;
  generation: number;
  configuration: number;
  codec: string;
  keyframe: boolean;
  timestamp: number;
  width: number;
  height: number;
  description?: number[];
  data: Uint8Array;
};

export type NativePreviewCommand =
  | { type: "reset"; epoch: number }
  | { type: "frame"; epoch: number; frame: NativePreviewFrame };

export type NativePreviewReply =
  | { type: "ready" }
  | { type: "ack"; sequence: number; epoch: number; painted: boolean; recovery?: boolean }
  | { type: "error"; message: string };
