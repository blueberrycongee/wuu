import type { ActivitySession } from "../shared/protocol";
import type { BrowserHostCoordinator } from "./browserHostWindows";
import { CUANativePreview, resolveCUAFrameHelper } from "./cuaFrameStreams";
import type { RemotePreviewHandlers, RemotePreviewSource } from "./remoteActivityPreview";

export function createRemotePreviewSource(
  activity: ActivitySession,
  handlers: RemotePreviewHandlers,
  browser: BrowserHostCoordinator,
): RemotePreviewSource {
  if (activity.kind === "browser") {
    let unsubscribe: (() => void) | undefined;
    let stopped = false;
    return {
      start: () => {
        const release = browser.subscribePreview(activity, handlers.frame, message => handlers.state("error", message));
        // A shared screencast can replay a cached frame synchronously. If its
        // recipient is backlogged, teardown happens before subscribe returns.
        if (stopped) release();
        else unsubscribe = release;
      },
      stop: () => { stopped = true; unsubscribe?.(); unsubscribe = undefined; },
      acknowledgeFrame: () => {},
      requestKeyframe: () => {}, // Every JPEG is independently decodable.
    };
  }
  const helper = resolveCUAFrameHelper();
  if (!helper) throw new Error("Live computer preview requires the macOS capture helper");
  // A mobile session shares the existing native host, but owns its encoder and
  // ACKs. A slow or disconnected phone must never stall the desktop PiP.
  const preview = new CUANativePreview(
    helper, activity.thread_id, activity.target!, activity.process_id, activity.window_id,
    event => {
      if (event.event === "gone") handlers.state("ended");
      else if (event.event === "capture_status") {
        if (event.status === "error") handlers.state("error", event.message);
        else if (event.status === "blank" || event.status === "suspended" || event.status === "stopped") {
          handlers.state("paused", event.reason === "target_hidden" ? "The target app is hidden" : event.message);
        }
      }
    },
    message => handlers.state("error", message), handlers.frame, undefined,
    { max_dimension: 960, frame_rate: 12, bitrate: 700_000 },
  );
  return {
    start: () => {
      preview.start();
      // Visibility here expresses viewer demand, not a desktop window. Capture
      // never restores hidden/minimized apps or alters desktop selection.
      preview.setVisible(true);
      preview.updateActivity(activity, false);
      preview.setLive(true);
    },
    stop: () => preview.stop(),
    acknowledgeFrame: sequence => preview.acknowledgeFrame(sequence),
    requestKeyframe: () => preview.requestKeyframe(),
  };
}
