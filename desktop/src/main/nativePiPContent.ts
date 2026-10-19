import { MessageChannelMain, WebContentsView, type MessagePortMain, type Rectangle } from "electron";
import type { NativePreviewReply } from "../shared/nativePreview";
import type { ActivitySession } from "../shared/protocol";
import { activityPreviewIsLive, type ActivityPiPContent, type ActivityPiPContentContext } from "./activityPiPContent";
import type { BrowserViewHandle } from "./browserHostWindows";
import { appShellWebPreferences } from "./appShellGuards";
import { CUANativePreview, type CUANativePreviewFrame } from "./cuaFrameStreams";

// Native capture is a headless SCStream provider. This view decodes its bounded
// continuous frame stream; all chrome, controls and window geometry live above
// it in ActivityPiPSurface. It never polls screenshots or receives user input.
export class NativePiPContent implements ActivityPiPContent {
  private view: WebContentsView | undefined;
  private preview: CUANativePreview | undefined;
  private ready: Promise<void> | undefined;
  private port: MessagePortMain | undefined;
  private startupTimer: ReturnType<typeof setTimeout> | undefined;
  private firstFrameTimer: ReturnType<typeof setTimeout> | undefined;
  private inFlight: { frame: CUANativePreviewFrame; epoch: number } | undefined;
  private stopped = false;
  private announced = false;
  private presented = false;
  private viewport: {width: number; height: number} | undefined;
  private visible = false;
  private live = true;
  private lastFrameSequence = 0;
  private captureEpoch = 0;
  private captureUnavailable = false;
  private captureSuspended = false;

  constructor(
    private activity: ActivitySession,
    private readonly context: ActivityPiPContentContext,
    private readonly deps: { helper: string; isPackaged: boolean; preload: string; pageURL: string },
  ) {}

  start(): void {
    if (this.stopped || this.view) return;
    // Capture and renderer readiness are independent; the surface can expose
    // loading or recovery while the first frame is still pending.
    const view = new WebContentsView({ webPreferences: {
      contextIsolation: true, nodeIntegration: false, sandbox: true, backgroundThrottling: false, preload: this.deps.preload,
      ...appShellWebPreferences(this.deps.isPackaged),
    } });
    this.view = view;
    view.setBackgroundColor("#00000000");
    view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    view.webContents.on("will-navigate", (event) => event.preventDefault());
    this.context.window.contentView.addChildView(view as unknown as BrowserViewHandle);
    this.layout(this.context.window.getBounds());
    const { port1, port2 } = new MessageChannelMain();
    this.port = port1;
    let rendererReady = false;
    this.ready = new Promise<void>((resolve, reject) => {
      this.startupTimer = setTimeout(() => reject(new Error("Native preview renderer did not become ready")), 5_000);
      port1.on("message", ({ data }: { data: NativePreviewReply }) => {
        if (this.stopped) return;
        if (data.type === "ready") {
          rendererReady = true;
          clearTimeout(this.startupTimer);
          port1.postMessage({ type: "reset", epoch: this.captureEpoch });
          resolve();
        } else if (data.type === "error") {
          if (rendererReady) this.context.onFailure(data.message);
          else reject(new Error(data.message));
        } else this.acknowledge(data);
      });
      port1.start();
      void view.webContents.loadURL(this.deps.pageURL).then(() => {
        if (!this.stopped) view.webContents.postMessage("wuu-native-preview-port", null, [port2]);
        else port2.close();
      }).catch(reject);
    });
    this.ready.catch((error: unknown) => {
      clearTimeout(this.startupTimer);
      if (!this.stopped) this.context.onFailure(error instanceof Error ? error.message : String(error));
    });
    view.webContents.on("render-process-gone", () => {
      if (!this.stopped) this.context.onFailure("Native preview renderer stopped");
    });
    const preview = new CUANativePreview(
      this.deps.helper, this.activity.thread_id, this.activity.target!.trim(),
      this.activity.process_id, this.activity.window_id,
      (event) => {
        if (this.stopped || event.event === "ready") return;
        if (event.event === "capture_status") {
          this.captureSuspended = event.status === "suspended" && event.reason === "target_hidden";
          if (event.status === "blank" || event.status === "suspended" || event.status === "error") {
            this.invalidateFrames();
            this.captureUnavailable = true;
            this.presented = false;
            this.context.setMounted(false);
          } else if (event.status === "healthy") this.captureUnavailable = false;
          else if (event.status === "stopped") this.invalidateFrames();
          this.watchFirstFrame();
        }
        if (event.event === "gone") this.context.onGone();
        else this.context.onEvent(event);
      },
      (message) => { if (!this.stopped) this.context.onFailure(message); },
      (frame) => { void this.present(frame); },
    );
    this.preview = preview;
    preview.start();
    preview.setVisible(this.visible);
    this.updateActivity(this.activity);
    this.refresh();
  }

  setVisible(visible: boolean): void {
    if (this.visible !== visible) this.invalidateFrames();
    this.visible = visible;
    this.preview?.setVisible(visible);
    this.watchFirstFrame();
  }

  setLive(live: boolean): void {
    if (this.live !== live) this.invalidateFrames();
    this.live = live;
    this.preview?.setLive(live && activityPreviewIsLive(this.activity));
    this.watchFirstFrame();
  }

  updateActivity(activity: ActivitySession, restoreTarget = false): void {
    if (activity.execution_id !== this.activity.execution_id || activity.controller !== this.activity.controller
      || (activity.state === "stopped" && this.activity.state !== "stopped")) this.invalidateFrames();
    this.activity = activity;
    this.preview?.updateActivity(activity, restoreTarget);
    this.preview?.setLive(this.live && activityPreviewIsLive(activity));
    this.watchFirstFrame();
  }

  private watchFirstFrame(): void {
    if (this.stopped || this.presented || this.captureSuspended || !this.visible || !this.live || !activityPreviewIsLive(this.activity)) {
      clearTimeout(this.firstFrameTimer);
      this.firstFrameTimer = undefined;
      return;
    }
    if (this.firstFrameTimer) return;
    this.firstFrameTimer = setTimeout(() => {
      this.firstFrameTimer = undefined;
      this.context.onFailure("Native preview did not deliver its first frame");
    }, 10_000);
  }

  private invalidateFrames(): void {
    this.captureEpoch++;
    this.port?.postMessage({ type: "reset", epoch: this.captureEpoch });
    this.preview?.requestKeyframe();
  }

  refresh(): void {
    this.context.setLabel(this.activity.target ?? "");
    this.context.setMounted(this.presented && !this.captureUnavailable);
  }

  layout(bounds: Rectangle): void {
    this.view?.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
  }

  async snapshot(): Promise<string | undefined> {
    const view = this.view;
    if (!view || !this.presented || view.webContents.isDestroyed()) return undefined;
    const src = await view.webContents.executeJavaScript("document.querySelector('canvas').toDataURL('image/jpeg',0.86)") as string;
    return src || undefined;
  }

  stop(onStopped: () => void): void {
    this.stopped = true;
    clearTimeout(this.startupTimer);
    clearTimeout(this.firstFrameTimer);
    this.firstFrameTimer = undefined;
    this.port?.close();
    this.port = undefined;
    this.inFlight = undefined;
    this.context.setMounted(false);
    const view = this.view;
    this.view = undefined;
    if (view) {
      if (!this.context.window.isDestroyed()) this.context.window.contentView.removeChildView(view as unknown as BrowserViewHandle);
      if (!view.webContents.isDestroyed()) view.webContents.close();
    }
    const preview = this.preview;
    this.preview = undefined;
    if (preview) preview.stop(onStopped);
    else onStopped();
  }

  private async present(frame: CUANativePreviewFrame): Promise<void> {
    const preview = this.preview;
    if (!preview || this.stopped) return;
    const epoch = this.captureEpoch;
    if (frame.sequence <= this.lastFrameSequence) { preview.acknowledgeFrame(frame.sequence); return; }
    this.lastFrameSequence = frame.sequence;
    try {
      await this.ready;
      if (!this.accepts(epoch)) { preview.acknowledgeFrame(frame.sequence); return; }
      this.inFlight = { frame, epoch };
      // Structured clone carries bytes directly. Pixels never enter JavaScript
      // source strings or the application's general-purpose preload bridge.
      this.port!.postMessage({ type: "frame", frame, epoch });
    } catch (error) {
      preview.acknowledgeFrame(frame.sequence);
      if (!this.stopped) this.context.onFailure(error instanceof Error ? error.message : String(error));
    }
  }

  private accepts(epoch: number): boolean {
    return !this.stopped && this.visible && this.live && activityPreviewIsLive(this.activity)
      && !this.captureUnavailable && this.captureEpoch === epoch;
  }

  private acknowledge(reply: Extract<NativePreviewReply, { type: "ack" }>): void {
    const current = this.inFlight;
    if (!current || reply.sequence !== current.frame.sequence || reply.epoch !== current.epoch) return;
    this.inFlight = undefined;
    if (reply.recovery && this.accepts(reply.epoch)) this.preview?.requestKeyframe();
    // ACK follows actual decode/draw or deliberate discard, not IPC receipt.
    this.preview?.acknowledgeFrame(reply.sequence);
    if (!reply.painted || !this.accepts(reply.epoch)) return;
    const frame = current.frame;
    if (this.viewport?.width !== frame.width || this.viewport.height !== frame.height) {
      this.viewport = { width: frame.width, height: frame.height };
      this.context.setViewport(this.viewport);
    }
    if (!this.presented) {
      this.presented = true;
      this.watchFirstFrame();
      this.context.setMounted(true);
    }
    if (!this.announced) {
      this.announced = true;
      this.context.onEvent({ event: "ready" });
    }
  }
}
