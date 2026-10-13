import { WebContentsView, type Rectangle } from "electron";
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
  private ready: Promise<unknown> | undefined;
  private stopped = false;
  private announced = false;
  private presented = false;
  private viewport: {width: number; height: number} | undefined;
  private visible = false;
  private live = true;
  private lastFrameSequence = 0;
  private captureEpoch = 0;
  private captureUnavailable = false;

  constructor(
    private activity: ActivitySession,
    private readonly context: ActivityPiPContentContext,
    private readonly deps: { helper: string; isPackaged: boolean },
  ) {}

  start(): void {
    if (this.stopped || this.view) return;
    const view = new WebContentsView({ webPreferences: {
      contextIsolation: true, nodeIntegration: false, sandbox: true,
      ...appShellWebPreferences(this.deps.isPackaged),
    } });
    this.view = view;
    view.setBackgroundColor("#00000000");
    view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    view.webContents.on("will-navigate", (event) => event.preventDefault());
    this.context.window.contentView.addChildView(view as unknown as BrowserViewHandle);
    this.layout(this.context.window.getBounds());
    this.ready = view.webContents.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(nativePreviewHTML)}`);
    this.ready.catch((error: unknown) => {
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
          if (event.status === "blank" || event.status === "suspended" || event.status === "error") {
            this.invalidateFrames();
            this.captureUnavailable = true;
            this.presented = false;
            this.context.setMounted(false);
          } else if (event.status === "healthy") this.captureUnavailable = false;
          else if (event.status === "stopped") this.invalidateFrames();
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
  }

  setLive(live: boolean): void {
    if (this.live !== live) this.invalidateFrames();
    this.live = live;
    this.preview?.setLive(live && activityPreviewIsLive(this.activity));
  }

  updateActivity(activity: ActivitySession): void {
    if (activity.execution_id !== this.activity.execution_id || activity.controller !== this.activity.controller
      || (activity.state === "stopped" && this.activity.state !== "stopped")) this.invalidateFrames();
    this.activity = activity;
    this.preview?.updateActivity(activity);
    this.preview?.setLive(this.live && activityPreviewIsLive(activity));
  }

  private invalidateFrames(): void {
    const epoch = ++this.captureEpoch;
    const view = this.view;
    void this.ready?.then(() => {
      if (!this.stopped && this.view === view && view && !view.webContents.isDestroyed()) {
        return view.webContents.executeJavaScript(`window.wuuNativeEpoch(${epoch})`);
      }
    }).catch(() => undefined);
  }

  refresh(): void {
    this.context.setLabel(this.activity.target ?? "");
    this.context.setMounted(this.presented && !this.captureUnavailable);
  }

  layout(bounds: Rectangle): void {
    this.view?.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
  }

  stop(onStopped: () => void): void {
    this.stopped = true;
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
    const view = this.view;
    if (!preview || !view || this.stopped || frame.sequence <= this.lastFrameSequence) return;
    this.lastFrameSequence = frame.sequence;
    const epoch = this.captureEpoch;
    const accepts = (): boolean => !this.stopped && this.view === view && this.visible
      && this.live && activityPreviewIsLive(this.activity)
      && !this.captureUnavailable && this.captureEpoch === epoch;
    try {
      await this.ready;
      if (!accepts()) return;
      const painted = await view.webContents.executeJavaScript(`window.wuuNativeFrame(${JSON.stringify(frame)},${epoch})`);
      if (!painted || !accepts()) return;
      if (this.viewport?.width !== frame.width || this.viewport.height !== frame.height) {
        this.viewport = { width: frame.width, height: frame.height };
        this.context.setViewport(this.viewport);
      }
      if (!this.presented) {
        this.presented = true;
        this.context.setMounted(true);
      }
      if (!this.announced) {
        this.announced = true;
        this.context.onEvent({ event: "ready" });
      }
    } catch (error) {
      if (!this.stopped) this.context.onFailure(error instanceof Error ? error.message : String(error));
    } finally {
      // Only one frame can cross native stdout/main/renderer at a time. A frame
      // is acknowledged after decode/draw (or intentional discard), never on receipt.
      if (!this.stopped && this.preview === preview) preview.acknowledgeFrame(frame.sequence);
    }
  }
}

const nativePreviewHTML = `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; script-src 'unsafe-inline'; style-src 'unsafe-inline'">
<style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:transparent}canvas{width:100%;height:100%;object-fit:scale-down;display:block;pointer-events:none}</style>
</head><body><canvas></canvas><script>
const canvas=document.querySelector('canvas'),context=canvas.getContext('2d',{alpha:false});
let sequence=0,activeEpoch=0;
window.wuuNativeEpoch=function(epoch){activeEpoch=Math.max(activeEpoch,epoch);};
window.wuuNativeFrame=async function(frame,epoch){
  if(epoch!==activeEpoch||frame.sequence<=sequence)return false;
  sequence=frame.sequence;
  const image=new Image();
  image.src='data:image/jpeg;base64,'+frame.data;
  await image.decode();
  if(epoch!==activeEpoch||sequence!==frame.sequence)return false;
  if(canvas.width!==frame.width||canvas.height!==frame.height){canvas.width=frame.width;canvas.height=frame.height;}
  context.drawImage(image,0,0,frame.width,frame.height);
  return true;
};
</script></body></html>`;
