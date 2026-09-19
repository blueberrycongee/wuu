import type { App, BrowserWindow, RenderProcessGoneDetails, WebContents } from "electron";

const RELOAD_COOLDOWN_MS = 5_000;
const MAX_AUTOMATIC_RELOADS = 3;
const STABLE_RENDERER_MS = 60_000;
const LOAD_TIMEOUT_MS = 30_000;
const unavailableRenderers = new WeakSet<WebContents>();

const RELOADABLE_RENDERER_GONE_REASONS = new Set([
  "crashed",
  "oom",
  "abnormal-exit",
  "launch-failed",
]);

type RecoveryOptions = {
  app: Pick<App, "on" | "removeListener">;
  load: () => Promise<void>;
  stopTerminals: (ownerID: number) => void;
  prompt: () => Promise<"reload" | "close">;
};

// Keep the BrowserWindow and its registry/bootstrap identity. Only the renderer
// is replaced; the app-server and other windows continue running.
export function installRendererRecovery(window: BrowserWindow, options: RecoveryOptions): void {
  const contents = window.webContents;
  const ownerID = contents.id;
  let disposed = false;
  let attempts = 0;
  let lastAttemptAt: number | undefined;
  let generation = 0;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let stableTimer: ReturnType<typeof setTimeout> | undefined;
  let loadTimer: ReturnType<typeof setTimeout> | undefined;

  function active(): boolean {
    return !disposed && !window.isDestroyed() && !contents.isDestroyed();
  }

  function cancelTimers(): void {
    clearTimeout(retryTimer);
    clearTimeout(stableTimer);
    clearTimeout(loadTimer);
    retryTimer = stableTimer = loadTimer = undefined;
  }

  function invalidate(): void {
    generation++;
    cancelTimers();
    unavailableRenderers.add(contents);
    // Renderer refs to PTYs cannot survive a crash; React cleanup cannot run.
    options.stopTerminals(ownerID);
  }

  function retry(): void {
    if (!active()) return;
    attempts++;
    lastAttemptAt = Date.now();
    const attempt = ++generation;
    const failed = (error: unknown) => {
      if (!active() || generation !== attempt) return;
      console.error("[renderer] recovery load failed", error);
      invalidate();
      scheduleRecovery();
    };
    loadTimer = setTimeout(() => failed(new Error("Renderer load timed out")), LOAD_TIMEOUT_MS);
    try {
      // Load the known app entry, not a failed/empty Chromium navigation entry.
      void options.load().catch(failed);
    } catch (error) {
      failed(error);
    }
  }

  function scheduleRecovery(): void {
    if (!active()) return;
    if (attempts < MAX_AUTOMATIC_RELOADS) {
      const delay = lastAttemptAt === undefined ? 0 : Math.max(0, RELOAD_COOLDOWN_MS - (Date.now() - lastAttemptAt));
      retryTimer = setTimeout(retry, delay);
      return;
    }
    const promptGeneration = generation;
    void options.prompt().then((choice) => {
      if (!active() || generation !== promptGeneration) return;
      if (choice === "close") {
        window.close();
      } else {
        attempts = 0;
        retry();
      }
    }).catch((error: unknown) => {
      console.error("[renderer] recovery dialog failed", error);
      if (active() && generation === promptGeneration) window.close();
    });
  }

  function processGone(_event: unknown, details: RenderProcessGoneDetails): void {
    if (!active()) return;
    invalidate();
    if (!RELOADABLE_RENDERER_GONE_REASONS.has(details.reason)) return;
    console.error(`[renderer] process gone (${details.reason}, exit ${details.exitCode}); recovering`);
    scheduleRecovery();
  }

  function domReady(): void {
    if (active()) unavailableRenderers.delete(contents);
  }

  function loaded(): void {
    if (!active()) return;
    generation++;
    cancelTimers();
    // A page that loads then immediately crashes must not reset the budget.
    stableTimer = setTimeout(() => {
      attempts = 0;
      lastAttemptAt = undefined;
    }, STABLE_RENDERER_MS);
  }

  function dispose(): void {
    disposed = true;
    generation++;
    cancelTimers();
    unavailableRenderers.add(contents);
    contents.removeListener("render-process-gone", processGone);
    contents.removeListener("dom-ready", domReady);
    contents.removeListener("did-finish-load", loaded);
    contents.removeListener("destroyed", dispose);
    window.removeListener("closed", dispose);
    options.app.removeListener("before-quit", dispose);
  }

  contents.on("render-process-gone", processGone);
  contents.on("dom-ready", domReady);
  contents.on("did-finish-load", loaded);
  contents.once("destroyed", dispose);
  window.once("closed", dispose);
  options.app.on("before-quit", dispose);
}

export function sendToWindow(window: BrowserWindow, channel: string, payload: unknown): void {
  if (window.isDestroyed()) return;
  const contents = window.webContents;
  if (contents.isDestroyed() || contents.isCrashed() || unavailableRenderers.has(contents)) return;
  try {
    const frame = contents.mainFrame;
    // Electron catches and logs native _send failures internally, so an outer
    // catch alone cannot suppress sends to an already disposed frame.
    if (frame.isDestroyed() || frame.detached) return;
    frame.send(channel, payload);
  } catch (error) {
    if (!(error instanceof Error && error.message.includes("Render frame was disposed"))) throw error;
  }
}
