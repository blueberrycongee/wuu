import type { LinuxPreviewCommand, LinuxPreviewReply } from "./shared/linuxPreview";

const video = document.querySelector("video")!;
const canvas = document.querySelector("canvas")!;
let port: MessagePort | undefined;
let stream: MediaStream | undefined;
let epoch = 0;
let frameCallback: number | undefined;

function reply(message: LinuxPreviewReply) { port?.postMessage(message); }

function suspend(clear: boolean): void {
  if (frameCallback !== undefined) video.cancelVideoFrameCallback(frameCallback);
  frameCallback = undefined;
  // Keep the last real frame for the shared completion/dismissal animation.
  if (!clear && stream && video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    canvas.getContext("2d")!.drawImage(video, 0, 0);
  } else if (clear) canvas.getContext("2d")!.clearRect(0, 0, canvas.width, canvas.height);
  for (const track of stream?.getTracks() ?? []) track.stop();
  stream = undefined;
  video.pause();
  video.srcObject = null;
  video.hidden = true;
  canvas.hidden = false;
}

async function receive(command: LinuxPreviewCommand): Promise<void> {
  if (command.epoch <= epoch) return;
  epoch = command.epoch;
  suspend(command.type === "suspend" && command.clear);
  if (command.type !== "capture") return;
  const current = epoch;
  try {
    // Electron's exact-source constraint starts a continuous X11 window stream.
    // It does not use a portal picker, capture the desktop, or activate a window.
    const captured = await navigator.mediaDevices.getUserMedia({
      audio: false,
      video: { mandatory: {
        chromeMediaSource: "desktop", chromeMediaSourceId: command.sourceID,
        maxWidth: 1280, maxHeight: 1280, maxFrameRate: 15,
      } } as MediaTrackConstraints,
    });
    if (epoch !== current) { captured.getTracks().forEach((track) => track.stop()); return; }
    stream = captured;
    const track = captured.getVideoTracks()[0];
    if (!track) throw new Error("Linux preview did not provide a video track");
    track.addEventListener("ended", () => {
      if (epoch !== current || stream !== captured) return;
      suspend(true);
      reply({ type: "ended", epoch: current });
    }, { once: true });
    video.srcObject = captured;
    video.hidden = false;
    canvas.hidden = true;
    let width = 0, height = 0;
    const painted = () => {
      if (epoch !== current || stream !== captured) return;
      if (video.videoWidth !== width || video.videoHeight !== height) {
        width = video.videoWidth; height = video.videoHeight;
        if (width > 0 && height > 0) reply({ type: "frame", epoch: current, width, height });
      }
      frameCallback = video.requestVideoFrameCallback(painted);
    };
    frameCallback = video.requestVideoFrameCallback(painted);
    await video.play();
  } catch (error) {
    if (epoch !== current) return;
    suspend(true);
    reply({ type: "error", epoch: current, message: error instanceof Error ? error.message : String(error) });
  }
}

window.addEventListener("message", (event: MessageEvent) => {
  if (event.source !== window || event.data !== "wuu-linux-preview-port" || port || !event.ports[0]) return;
  port = event.ports[0];
  port.onmessage = (event: MessageEvent<LinuxPreviewCommand>) => { void receive(event.data); };
  port.start();
  reply({ type: "ready" });
});
window.addEventListener("pagehide", () => { epoch++; suspend(true); port?.close(); });
