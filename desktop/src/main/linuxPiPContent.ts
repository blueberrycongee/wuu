import { desktopCapturer, MessageChannelMain, WebContentsView, type MessagePortMain, type Rectangle } from "electron";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
import type { ActivitySession } from "../shared/protocol";
import type { LinuxPreviewCommand, LinuxPreviewReply } from "../shared/linuxPreview";
import { activityPreviewIsLive, type ActivityPiPContent, type ActivityPiPContentContext } from "./activityPiPContent";
import { appShellWebPreferences } from "./appShellGuards";
import type { BrowserViewHandle } from "./browserHostWindows";
import { shutdownChild } from "./childShutdown";

export function resolveLinuxCUAHelper(): string | undefined {
  const resourcesPath = (process as { resourcesPath?: string }).resourcesPath;
  const roots = [process.env.WUU_SOURCE_ROOT, process.cwd(), resolve(process.cwd(), "..")]
    .filter((value): value is string => Boolean(value));
  return [process.env.WUU_CUA_LINUX_HELPER,
    resourcesPath ? join(resourcesPath, "bin", "wuu-cua-linux") : undefined,
    ...roots.map((root) => join(root, "desktop", "build", "bin", "wuu-cua-linux")),
  ].find((candidate): candidate is string => Boolean(candidate && existsSync(candidate)));
}

// A content-only adapter for the shared PiP surface. The helper watches identity
// and visibility; Chromium owns the continuous pixel stream. Neither path sends
// input, restores a target, raises windows, or changes the agent's authority.
export class LinuxPiPContent implements ActivityPiPContent {
  private view: WebContentsView | undefined;
  private port: MessagePortMain | undefined;
  private watcher: ChildProcessWithoutNullStreams | undefined;
  private rendererTimer: ReturnType<typeof setTimeout> | undefined;
  private watcherTimer: ReturnType<typeof setTimeout> | undefined;
  private firstFrameTimer: ReturnType<typeof setTimeout> | undefined;
  private rendererReady = false;
  private targetState: "unknown" | "ready" | "hidden" | "gone" = "unknown";
  private visible = false;
  private live = true;
  private stopped = false;
  private failed = false;
  private capturing = false;
  private presented = false;
  private announced = false;
  private label: string | undefined;
  private epoch = 0;

  constructor(
    private activity: ActivitySession,
    private readonly context: ActivityPiPContentContext,
    private readonly deps: { helper?: string; isPackaged: boolean; preload: string; pageURL: string },
  ) {}

  start(): void {
    if (this.stopped || this.view) return;
    if (!process.env.DISPLAY || process.env.WAYLAND_DISPLAY || process.env.XDG_SESSION_TYPE === "wayland") {
      this.fail("Linux window previews require an X11 desktop session; native Wayland is not supported");
      return;
    }
    if (!this.deps.helper) { this.fail("The Linux Computer Use helper is unavailable"); return; }
    const windowID = this.activity.window_id, processID = this.activity.process_id;
    const identity = /^linux:(\d+):(\d+):(\d+)$/.exec(this.activity.target?.trim() ?? "");
    if (!Number.isSafeInteger(windowID) || !windowID || windowID < 1 || windowID > 0xffffffff
      || !Number.isSafeInteger(processID) || !processID || processID < 1
      || !identity || Number(identity[1]) !== processID || Number(identity[2]) !== windowID) {
      this.fail("Linux preview requires a resolved window and process identity");
      return;
    }
    const view = new WebContentsView({ webPreferences: {
      contextIsolation: true, nodeIntegration: false, sandbox: true, backgroundThrottling: false,
      partition: `wuu-linux-preview-${randomUUID()}`, preload: this.deps.preload,
      ...appShellWebPreferences(this.deps.isPackaged),
    } });
    this.view = view;
    view.setBackgroundColor("#00000000");
    view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    view.webContents.on("will-navigate", (event) => event.preventDefault());
    // This private view has no camera, microphone, input or general browser
    // permissions. Electron 44.1 reports desktop capture as media with no types;
    // remove that branch when the minimum Electron uses display-capture.
    view.webContents.session.setPermissionCheckHandler(() => false);
    view.webContents.session.setPermissionRequestHandler((contents, permission, callback, details) => {
      const types = "mediaTypes" in details ? details.mediaTypes : undefined;
      const capture = (permission === "display-capture" && !types?.includes("audio"))
        || (permission === "media" && types?.length === 0);
      callback(contents === view.webContents && !this.stopped && this.capturing && this.targetState === "ready"
        && details.isMainFrame && capture);
    });
    this.context.window.contentView.addChildView(view as unknown as BrowserViewHandle);
    this.layout(this.context.window.getBounds());
    const { port1, port2 } = new MessageChannelMain();
    this.port = port1;
    this.rendererTimer = setTimeout(() => this.fail("Linux preview renderer did not become ready"), 5_000);
    port1.on("message", ({ data }: { data: LinuxPreviewReply }) => this.receive(data));
    port1.start();
    void view.webContents.loadURL(this.deps.pageURL).then(() => {
      if (!this.stopped) view.webContents.postMessage("wuu-linux-preview-port", null, [port2]);
      else port2.close();
    }).catch((error: Error) => this.fail(error.message));
    view.webContents.on("render-process-gone", () => this.fail("Linux preview renderer stopped"));

    // Watching the exact XID checks _NET_WM_PID, process start time and WM_CLASS,
    // then follows X11 lifecycle events. An XID reused by another app is gone.
    const watcher = spawn(this.deps.helper, ["--watch-window", String(windowID), String(processID), identity[3]!], { stdio: ["pipe", "pipe", "pipe"] });
    this.watcher = watcher;
    this.watcherTimer = setTimeout(() => this.fail("Linux preview target did not become ready"), 5_000);
    let buffer = "", stderr = "";
    watcher.stdout.setEncoding("utf8");
    watcher.stdout.on("data", (chunk: string) => {
      if (this.stopped || this.failed) return;
      buffer += chunk;
      if (buffer.length > 65_536) { this.fail("Linux preview target event exceeded the size limit"); return; }
      let end: number;
      while ((end = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
        try { this.targetChanged(JSON.parse(line)); }
        catch { this.fail("Linux preview target returned an invalid event"); return; }
      }
    });
    watcher.stderr.setEncoding("utf8");
    watcher.stderr.on("data", (chunk: string) => { stderr = (stderr + chunk).slice(-4096); });
    watcher.on("error", (error) => this.fail(error.message));
    watcher.stdin.on("error", (error) => this.fail(error.message));
    watcher.on("exit", () => {
      if (!this.stopped && this.targetState !== "gone") this.fail(stderr.trim() || "Linux preview target watcher stopped");
    });
    this.refresh();
  }

  setVisible(visible: boolean): void { this.visible = visible; this.syncCapture(); }
  setLive(live: boolean): void { this.live = live; this.syncCapture(); }

  updateActivity(activity: ActivitySession): void {
    const newExecution = activity.id !== this.activity.id || activity.execution_id !== this.activity.execution_id;
    this.activity = activity;
    if (newExecution) this.suspend(true);
    this.syncCapture();
    this.refresh();
  }

  refresh(): void {
    this.context.setLabel(this.label ?? this.activity.target ?? "");
    this.context.setMounted(this.presented && this.targetState === "ready");
  }

  layout(bounds: Rectangle): void {
    this.view?.setBounds({ x: 0, y: 0, width: bounds.width, height: bounds.height });
  }

  async snapshot(): Promise<string | undefined> {
    const view = this.view;
    if (!view || !this.presented || view.webContents.isDestroyed()) return undefined;
    return view.webContents.executeJavaScript(`(() => {
      const video = document.querySelector('video'), saved = document.querySelector('canvas');
      if (video.hidden || video.readyState < 2) return saved.toDataURL('image/jpeg', 0.86);
      const canvas = document.createElement('canvas');
      canvas.width = video.videoWidth; canvas.height = video.videoHeight;
      canvas.getContext('2d').drawImage(video, 0, 0);
      return canvas.toDataURL('image/jpeg', 0.86);
    })()`) as Promise<string>;
  }

  stop(onStopped: () => void): void {
    this.stopped = true;
    this.suspend(true);
    clearTimeout(this.rendererTimer);
    clearTimeout(this.watcherTimer);
    this.port?.close(); this.port = undefined;
    const view = this.view; this.view = undefined;
    if (view) {
      // Drop closures held by Electron's session registry after the view dies.
      if (!view.webContents.isDestroyed()) {
        view.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
      }
      if (!this.context.window.isDestroyed()) this.context.window.contentView.removeChildView(view as unknown as BrowserViewHandle);
      if (!view.webContents.isDestroyed()) view.webContents.close();
    }
    const watcher = this.watcher; this.watcher = undefined;
    if (!watcher) { onStopped(); return; }
    void shutdownChild(watcher, () => watcher.stdin.end(), 2_000)
      .catch((error: Error) => process.stderr.write(`[linux-preview] ${error.message}\n`)).finally(onStopped);
  }

  private targetChanged(message: { status?: string }): void {
    if (this.stopped || this.failed || this.targetState === "gone") return;
    if (message.status !== "ready" && message.status !== "hidden" && message.status !== "gone") {
      throw new Error("invalid target state");
    }
    clearTimeout(this.watcherTimer);
    if (this.targetState === message.status) return;
    this.targetState = message.status;
    if (message.status === "gone") {
      this.suspend(true);
      this.context.onGone();
      return;
    }
    if (message.status === "hidden") {
      this.suspend(true);
      this.context.onEvent({ event: "capture_status", status: "suspended", reason: "target_hidden" });
    } else {
      this.context.onEvent({ event: "capture_status", status: "healthy" });
      this.syncCapture();
    }
  }

  private syncCapture(): void {
    const wanted = !this.stopped && !this.failed && this.rendererReady && this.targetState === "ready"
      && this.visible && this.live && activityPreviewIsLive(this.activity);
    if (wanted === this.capturing) return;
    if (!wanted) { this.suspend(false); return; }
    this.capturing = true;
    const epoch = ++this.epoch;
    this.firstFrameTimer = setTimeout(() => this.fail("Linux preview did not deliver its first frame"), 10_000);
    void desktopCapturer.getSources({ types: ["window"], thumbnailSize: { width: 0, height: 0 }, fetchWindowIcons: false })
      .then((sources) => {
        if (!this.accepts(epoch)) return;
        const source = sources.find((candidate) => {
          const match = /^window:(\d+):[01]$/.exec(candidate.id);
          return match && Number(match[1]) === this.activity.window_id;
        });
        if (!source) { this.fail("The selected Linux window is not available for capture"); return; }
        this.label = source.name;
        this.context.setLabel(this.label);
        this.send({ type: "capture", epoch, sourceID: source.id });
      }).catch((error: Error) => { if (this.accepts(epoch)) this.fail(error.message); });
  }

  private suspend(clear: boolean): void {
    this.capturing = false;
    clearTimeout(this.firstFrameTimer);
    this.firstFrameTimer = undefined;
    this.send({ type: "suspend", epoch: ++this.epoch, clear });
    if (clear) { this.presented = false; this.context.setMounted(false); }
  }

  private receive(message: LinuxPreviewReply): void {
    if (this.stopped || this.failed) return;
    if (message.type === "ready") {
      this.rendererReady = true;
      clearTimeout(this.rendererTimer);
      this.syncCapture();
      return;
    }
    if (!this.accepts(message.epoch)) return;
    if (message.type === "error" || message.type === "ended") {
      this.fail(message.type === "error" ? message.message : "Linux preview capture ended");
      return;
    }
    if (message.type === "frame" && Number.isInteger(message.width) && Number.isInteger(message.height)
      && message.width > 0 && message.height > 0 && message.width <= 1280 && message.height <= 1280) {
      clearTimeout(this.firstFrameTimer);
      this.firstFrameTimer = undefined;
      this.context.setViewport({ width: message.width, height: message.height });
      this.presented = true;
      this.context.setMounted(true);
      if (!this.announced) { this.announced = true; this.context.onEvent({ event: "ready" }); }
    }
  }

  private accepts(epoch: number): boolean {
    return !this.stopped && !this.failed && this.capturing && this.targetState === "ready" && this.epoch === epoch;
  }

  private send(command: LinuxPreviewCommand): void { this.port?.postMessage(command); }

  private fail(message: string): void {
    if (this.stopped || this.failed || this.targetState === "gone") return;
    this.failed = true;
    this.suspend(true);
    this.context.onFailure(message);
  }
}
