import { app, globalShortcut, Notification } from "electron";
import { execFile, type ChildProcess } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { release } from "node:os";
import { createRequire } from "node:module";
const requireFromMain = createRequire(import.meta.url);
import type { AppContextSettings, AppContextSnapshot, AppContextState } from "../shared/protocol";
import { writeTextFileAtomicSync } from "./atomicFile";
import { wuuHomePath } from "./projects";

const defaults: AppContextSettings = { enabled: false, shortcut: "Control+Alt+Space", include_text: false };
const maxOutputBytes = 24 * 1024 * 1024;

function parseSettings(value: unknown): AppContextSettings {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Invalid app snapshot settings.");
  const record = value as Record<string, unknown>;
  if (typeof record.enabled !== "boolean" || typeof record.include_text !== "boolean" || typeof record.shortcut !== "string") {
    throw new Error("Invalid app snapshot settings.");
  }
  const parts = record.shortcut.trim().split("+");
  const key = parts.pop()?.toUpperCase() ?? "";
  const aliases: Record<string, string> = { commandorcontrol: "Command", cmdorctrl: "Command", command: "Command", cmd: "Command", ctrl: "Control", control: "Control", alt: "Alt", option: "Alt", shift: "Shift", super: "Command" };
  const modifiers = parts.map(part => aliases[part.trim().toLowerCase()]);
  if (record.shortcut.length > 100 || modifiers.some(modifier => !modifier)
      || new Set(modifiers).size !== modifiers.length
      || !modifiers.some(modifier => modifier !== "Shift")
      || !/^(?:[A-Z0-9]|SPACE|F(?:[1-9]|1[0-9]|2[0-4]))$/.test(key)) {
    throw new Error("Use Command, Control, or Option with a key, for example Control+Alt+Space.");
  }
  const shortcut = [...["Command", "Control", "Alt", "Shift"].filter(modifier => modifiers.includes(modifier)), key === "SPACE" ? "Space" : key].join("+");
  return { enabled: record.enabled, shortcut, include_text: record.include_text };
}

function isSnapshot(value: unknown): value is AppContextSnapshot {
  if (!value || typeof value !== "object") return false;
  const item = value as Record<string, unknown>;
  return ["id", "captured_at", "app_name", "bundle_id", "window_title", "image_base64", "available_text"].every(key => typeof item[key] === "string")
    && typeof item.image_base64 === "string" && item.image_base64.length > 0 && item.image_base64.length <= maxOutputBytes
    && /^[A-Za-z0-9+/]+=*$/.test(item.image_base64)
    && typeof item.available_text === "string" && item.available_text.length <= 160_000
    && ["window_id", "width", "height"].every(key => Number.isSafeInteger(item[key]) && (item[key] as number) > 0)
    && ["included", "truncated", "not_requested", "permission_missing", "unavailable"].includes(item.text_status as string);
}

export class AppContextCapture {
  private readonly file = join(wuuHomePath(), "app-snapshot-settings.json");
  private readonly helper = app.isPackaged
    ? join(process.resourcesPath, "app-context", "Wuu App Snapshot.app", "Contents", "MacOS", "wuu-app-context")
    : join(app.getAppPath(), "build", "app-context", "Wuu App Snapshot.app", "Contents", "MacOS", "wuu-app-context");
  private readonly bridge = app.isPackaged
    ? join(process.resourcesPath, "app-context", "capture-target.node")
    : join(app.getAppPath(), "build", "app-context", "capture-target.node");
  private pinTarget?: () => string;
  private owner?: string;
  private settings = { ...defaults };
  private registered?: string;
  private child?: ChildProcess;
  private epoch = 0;
  private phase: AppContextState["phase"] = "idle";
  private error?: string;
  private snapshot?: AppContextSnapshot;
  private targetName?: string;
  private notice?: Notification;

  constructor(private readonly notify: (reveal: boolean, owner?: string) => void, private readonly currentOwner: () => string | undefined) {}

  start(): void {
    try { this.settings = parseSettings(JSON.parse(readFileSync(this.file, "utf8"))); } catch { /* Missing or invalid saved settings use opt-out defaults. */ }
    if (this.available() && this.settings.enabled) {
      try { this.register(this.settings.shortcut); } catch (error) { this.error = (error as Error).message; }
    }
  }

  private available(): boolean { return process.platform === "darwin" && Number.parseInt(release(), 10) >= 23 && existsSync(this.helper) && existsSync(this.bridge); }

  state(): AppContextState {
    if (this.owner && this.owner !== this.currentOwner()) this.cancel();
    return { available: this.available(), settings: { ...this.settings }, shortcut_registered: Boolean(this.registered),
      phase: this.phase, ...(this.error ? { error: this.error } : {}), ...(this.snapshot ? { snapshot: this.snapshot } : {}),
      ...(this.targetName ? { app_name: this.targetName } : {}) };
  }

  update(value: unknown): AppContextState {
    const next = parseSettings(value);
    if (next.enabled && !this.available()) throw new Error("App snapshots require macOS 14 or later and the capture helper. Build or update Wuu on a supported Mac first.");
    const previousKey = this.registered;
    if (next.enabled) this.register(next.shortcut);
    try { writeTextFileAtomicSync(this.file, `${JSON.stringify(next, null, 2)}\n`); }
    catch (error) {
      if (this.registered && this.registered !== previousKey) globalShortcut.unregister(this.registered);
      this.registered = previousKey;
      throw error;
    }
    if (previousKey && previousKey !== (next.enabled ? next.shortcut : undefined)) globalShortcut.unregister(previousKey);
    this.registered = next.enabled ? next.shortcut : undefined;
    this.settings = next;
    // Any settings change revokes an in-flight one-shot capture and its private preview.
    this.cancel();
    return this.state();
  }

  private register(shortcut: string): void {
    if (shortcut === this.registered) return;
    // Load before registration: the shortcut callback itself must only make a
    // synchronous metadata call, never launch a process to discover its target.
    const bridge = requireFromMain(this.bridge) as { pinTarget?: unknown };
    if (typeof bridge.pinTarget !== "function") throw new Error("The capture target bridge is unavailable. Update Wuu and try again.");
    this.pinTarget = bridge.pinTarget as () => string;
    // Never unregister an accelerator owned by another desktop feature.
    if (globalShortcut.isRegistered(shortcut) || !globalShortcut.register(shortcut, () => {
      if (this.child) this.cancel(); else this.capture();
    })) throw new Error("That shortcut is already in use. Choose another shortcut.");
    this.registered = shortcut;
  }

  invalidateOwner(windowID: number): void {
    if (this.owner?.startsWith(`${windowID}:`)) this.cancel();
  }

  cancel(snapshotID?: string): AppContextState {
    if (snapshotID && this.owner !== this.currentOwner()) {
      this.cancel();
      throw new Error("The snapshot window was closed or reloaded. Capture the app again.");
    }
    if (snapshotID && this.snapshot?.id !== snapshotID) throw new Error("This snapshot was replaced or discarded. Capture the app again.");
    this.epoch += 1;
    this.child?.kill();
    this.child = undefined;
    this.notice?.close();
    this.notice = undefined;
    this.snapshot = undefined;
    this.owner = undefined;
    this.targetName = undefined;
    this.error = undefined;
    this.phase = "idle";
    this.notify(false);
    return this.state();
  }

  async requestPermission(kind: unknown): Promise<AppContextState> {
    if (kind !== "screen" && kind !== "text") throw new Error("Unknown app snapshot permission.");
    if (!this.available()) throw new Error("App snapshots require macOS 14 or later and the capture helper.");
    this.cancel();
    await new Promise<void>((resolve, reject) => {
      execFile(this.helper, [kind === "screen" ? "--request-screen-permission" : "--request-text-permission"],
        { timeout: 10_000, maxBuffer: 16_384, encoding: "utf8", env: this.helperEnvironment() }, error => error ? reject(new Error("Could not open the macOS permission request. Check Privacy & Security in System Settings.")) : resolve());
    });
    return this.state();
  }

  private helperEnvironment(): NodeJS.ProcessEnv {
    // The capture process never receives model credentials or inherited tool settings.
    return { HOME: app.getPath("home"), PATH: "/usr/bin:/bin:/usr/sbin:/sbin", LANG: "en_US.UTF-8" };
  }

  private capture(): void {
    if (!this.settings.enabled || !this.available()) return;
    this.cancel();
    const epoch = this.epoch;
    const owner = this.currentOwner();
    this.owner = owner;
    if (!owner) {
      this.phase = "error";
      this.error = "Open Wuu's main window before using the app snapshot shortcut.";
      if (Notification.isSupported()) {
        this.notice = new Notification({ title: "App snapshot unavailable", body: this.error, silent: true });
        this.notice.show();
      }
      return;
    }
    let target: string;
    try {
      target = this.pinTarget!();
      if (typeof target !== "string" || target.length > 2048) throw new Error("The app capture target could not be identified.");
    } catch (error) {
      this.phase = "error";
      this.error = (error as Error).message;
      this.notify(true, owner);
      return;
    }
    this.phase = "capturing";
    this.notify(false, owner);
    const args = ["--capture", "--exclude-pid", String(process.pid), "--target", target, ...(this.settings.include_text ? ["--include-text"] : [])];
    let progress = "";
    this.child = execFile(this.helper, args, { timeout: 12_000, maxBuffer: maxOutputBytes, encoding: "utf8", env: this.helperEnvironment() }, (error, stdout) => {
      if (epoch !== this.epoch || owner !== this.currentOwner()) return;
      this.child = undefined;
      this.notice?.close();
      this.notice = undefined;
      let result: { ok?: boolean; message?: string; snapshot?: unknown } | undefined;
      try { result = JSON.parse(stdout.trim().split("\n").at(-1) ?? ""); } catch { /* The process may have timed out or exited before producing a result. */ }
      if (!error && result?.ok && isSnapshot(result.snapshot)) {
        this.snapshot = result.snapshot;
        this.phase = "ready";
      } else {
        this.phase = "error";
        this.error = typeof result?.message === "string" ? result.message : "App capture did not finish. Keep the app window visible, check permissions, and try again.";
      }
      // Only the terminal result may reveal Wuu. The helper pins and revalidates
      // the exact shortcut-pinned target before this point; no last-app inference is used.
      this.notify(true, owner);
    });
    this.child.stdout?.on("data", (chunk: Buffer | string) => {
      if (epoch !== this.epoch || owner !== this.currentOwner() || this.targetName) return;
      progress += chunk.toString();
      const newline = progress.indexOf("\n");
      if (newline < 0) { if (progress.length > 4096) progress = ""; return; }
      if (newline > 4096) { progress = ""; return; }
      try {
        const event = JSON.parse(progress.slice(0, newline)) as { event?: string; app_name?: string };
        if (event.event !== "pinned" || typeof event.app_name !== "string") return;
        this.targetName = event.app_name;
        this.notify(false, owner);
        if (Notification.isSupported()) {
          this.notice = new Notification({ title: "Capturing app snapshot", body: `${event.app_name}. Press ${this.settings.shortcut} again to cancel.`, silent: true });
          this.notice.on("click", () => this.cancel());
          this.notice.show();
        }
      } catch { /* Progress is optional; the bounded terminal response is authoritative. */ }
    });
  }

  dispose(): void {
    this.cancel();
    if (this.registered) globalShortcut.unregister(this.registered);
    this.registered = undefined;
  }
}
