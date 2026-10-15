import type { NativePreviewCommand, NativePreviewFrame, NativePreviewReply } from "./shared/nativePreview";

const canvas = document.querySelector("canvas")!;
const context = canvas.getContext("2d", { alpha: false })!;
let port: MessagePort | undefined;
let decoder: VideoDecoder | undefined;
let configuration = 0, generation = 0, epoch = 0, sequence = 0, failures = 0;
let pending: { frame: NativePreviewFrame; epoch: number; timer: ReturnType<typeof setTimeout> } | undefined;

function reply(message: NativePreviewReply) { port?.postMessage(message); }
function closeDecoder() {
  if (decoder && decoder.state !== "closed") decoder.close();
  decoder = undefined;
  configuration = 0;
}
function finish(painted: boolean, recovery = false) {
  const current = pending;
  if (!current) return;
  pending = undefined;
  clearTimeout(current.timer);
  reply({ type: "ack", sequence: current.frame.sequence, epoch: current.epoch, painted, recovery });
}
function recover(message: string) {
  closeDecoder();
  if (++failures >= 3) {
    finish(false);
    reply({ type: "error", message });
  } else finish(false, true);
}
function receive(command: NativePreviewCommand) {
  if (command.type === "reset") {
    if (command.epoch <= epoch) return;
    epoch = command.epoch;
    closeDecoder();
    finish(false);
    return;
  }
  const frame = command.frame;
  if (command.epoch !== epoch || frame.sequence <= sequence) {
    reply({ type: "ack", sequence: frame.sequence, epoch: command.epoch, painted: false });
    return;
  }
  sequence = frame.sequence;
  // Capture recovery may retire an unacknowledged frame and start a new stream.
  // Discard its decoder before accepting the replacement or requesting an IDR.
  if (pending) { closeDecoder(); finish(false); }
  pending = { frame, epoch, timer: setTimeout(() => recover("Native preview video decode timed out"), 2_000) };
  try {
    if (!decoder || configuration !== frame.configuration || generation !== frame.generation) {
      closeDecoder();
      if (!frame.keyframe || !frame.description) { finish(false, true); return; }
      const next = new VideoDecoder({
        output(video) {
          try {
            const current = pending;
            if (decoder !== next || !current || current.epoch !== epoch || video.timestamp !== current.frame.timestamp) return;
            if (canvas.width !== video.displayWidth || canvas.height !== video.displayHeight) {
              canvas.width = video.displayWidth;
              canvas.height = video.displayHeight;
            }
            context.drawImage(video, 0, 0, canvas.width, canvas.height);
            failures = 0;
            finish(true);
          } catch (error) { recover(error instanceof Error ? error.message : String(error)); }
          finally { video.close(); }
        },
        error(error) { if (decoder === next) recover(error.message); },
      });
      decoder = next;
      // Let Chromium select software decoding when hardware is unavailable.
      next.configure({ codec: frame.codec, codedWidth: frame.width, codedHeight: frame.height,
        description: new Uint8Array(frame.description), optimizeForLatency: true });
      configuration = frame.configuration;
      generation = frame.generation;
    }
    decoder.decode(new EncodedVideoChunk({ type: frame.keyframe ? "key" : "delta", timestamp: frame.timestamp, data: frame.data }));
    // Do not flush here: WebCodecs requires a keyframe after every flush.
  } catch (error) { recover(error instanceof Error ? error.message : String(error)); }
}

window.addEventListener("message", (event: MessageEvent) => {
  if (event.source !== window || event.data !== "wuu-native-preview-port" || port || !event.ports[0]) return;
  port = event.ports[0];
  if (typeof VideoDecoder === "undefined") { reply({ type: "error", message: "Native preview video decoding is unavailable" }); return; }
  port.onmessage = (event: MessageEvent<NativePreviewCommand>) => receive(event.data);
  port.start();
  reply({ type: "ready" });
});
window.addEventListener("pagehide", () => { closeDecoder(); finish(false); port?.close(); });
