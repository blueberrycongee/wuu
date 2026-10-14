import { spawn } from "node:child_process";
import { open } from "node:fs/promises";
import { homedir } from "node:os";
import { delimiter, extname, isAbsolute, join } from "node:path";
import {
  acquireFileLock, releaseFileLock, FILE_LOCK_SERVICE,
  type RuntimeHost, type RuntimePlugin, type ToolContentPart, type ToolExecuteResult,
} from "@wuu/plugin-sdk";

const CLI_VERSION = "0.3.2";
const DEFAULT_TIMEOUT_S = 60;
const MAX_TIMEOUT_S = 300;
const OUTPUT_BYTES = 64 * 1024;
const IMAGE_BYTES = 8 * 1024 * 1024;
const IMAGE_TYPES: Record<string, string> = { ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp" };

let lifecycleHost: RuntimeHost;
let lifecycleSignal: AbortSignal;

const plugin: RuntimePlugin = {
  initialize(_params, host, lifecycle) {
    lifecycleHost = host;
    lifecycleSignal = lifecycle.signal;
    return {
      required_services: [{ name: FILE_LOCK_SERVICE, major_version: 1, required: true }],
      tools: [{
        id: "phone_exec",
        description: "Operate a phone with Phone Harness. Python helpers are pre-imported; print results and call screenshot() to attach the latest image. Each call has fresh Python variables. Read the phone-harness skill first. Choose the intended phone explicitly; do not substitute a cloud phone for the user's personal phone. Observe after input to verify the outcome. Interrupted input may already have taken effect; never replay it automatically.",
        input_schema: {
          type: "object",
          properties: {
            code: { type: "string", description: "Python using ensure_device(), screen(), screenshot(), open_app(), tap_text(), tap(), swipe(), scroll(), type_text(), and other Phone Harness helpers. Print values to return them." },
            target: { type: "string", enum: ["ios", "android", "cloud"], description: "ios: personal iPhone via Mac mirroring. android: a specific ADB device. cloud: an existing Phone Harness Cloud session." },
            serial: { type: "string", description: "Required for android: exact ADB device serial from adb devices -l." },
            session: { type: "string", description: "Required for cloud: existing session ID from phone-harness cloud ls. Does not rent or start a phone." },
            timeout_s: { type: "integer", minimum: 1, maximum: MAX_TIMEOUT_S, default: DEFAULT_TIMEOUT_S, description: "Script execution budget in seconds, excluding the wait for another Wuu phone call." },
          },
          required: ["code", "target"], additionalProperties: false,
        },
        activity: { read_only: false, concurrency_safe: false },
        display: { kind: "phone", text: "Using phone" },
      }],
    };
  },
  async executeTool(call, _host, execution): Promise<ToolExecuteResult> {
    if (call.tool_id !== "phone_exec") throw new Error(`unknown phone tool ${JSON.stringify(call.tool_id)}`);
    const input = call.arguments;
    if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("phone_exec requires an argument object");
    const args = input as Record<string, unknown>;
    for (const key of Object.keys(args)) {
      if (!["code", "target", "serial", "session", "timeout_s"].includes(key)) throw new Error(`unknown phone_exec field ${key}`);
    }
    if (typeof args.code !== "string" || !args.code.trim()) throw new Error("code must be a non-empty Python script");
    if (Buffer.byteLength(args.code) > OUTPUT_BYTES) throw new Error(`code exceeds ${OUTPUT_BYTES} bytes; split the task into shorter calls`);
    if (typeof args.target !== "string" || !["ios", "android", "cloud"].includes(args.target)) throw new Error("target must be ios, android, or cloud");
    for (const key of ["serial", "session"]) {
      if (args[key] !== undefined && (typeof args[key] !== "string" || !(args[key] as string).trim())) throw new Error(`${key} must be a non-empty string`);
    }
    if (args.target === "android" && !args.serial) throw new Error("android requires an explicit serial from adb devices -l");
    if (args.target === "cloud" && !args.session) throw new Error("cloud requires an existing session ID from phone-harness cloud ls");
    if (args.target !== "android" && args.serial !== undefined) throw new Error("serial is only valid for android");
    if (args.target !== "cloud" && args.session !== undefined) throw new Error("session is only valid for cloud");
    if (args.target === "ios" && process.platform !== "darwin") throw new Error("Personal iPhone control requires macOS and iPhone Mirroring");
    const timeout = args.timeout_s ?? DEFAULT_TIMEOUT_S;
    if (typeof timeout !== "number" || !Number.isInteger(timeout) || timeout < 1 || timeout > MAX_TIMEOUT_S) throw new Error(`timeout_s must be an integer from 1 to ${MAX_TIMEOUT_S}`);

    const signal = AbortSignal.any([execution.signal, lifecycleSignal]);
    signal.throwIfAborted();
    // A user-wide lock also serializes other workspaces and retained plugin generations.
    const lock = await acquireFileLock(lifecycleHost, { root: "wuu_home", path: "locks/phone-harness.lock" }, execution.executionId);
    try {
      signal.throwIfAborted();
      const env: NodeJS.ProcessEnv = { ...process.env };
      env.PATH = [env.PATH, join(homedir(), ".local", "bin")].filter(Boolean).join(delimiter);
      env.PHONE_HARNESS_TELEMETRY = "0";
      env.PHONE_HARNESS_NO_UPDATE_CHECK = "1";
      env.PYTHONUNBUFFERED = "1";
      env.PH_CLIENT = "wuu";
      // Empty values keep a workspace .env from selecting a different phone.
      env.ANDROID_SERIAL = args.target === "android" ? args.serial as string : "";
      env.PHONE_HARNESS_SESSION = args.target === "cloud" ? args.session as string : "";
      if (args.target === "cloud") delete env.PHONE_HARNESS_PLATFORM;
      else env.PHONE_HARNESS_PLATFORM = args.target as string;
      // The extra pipe closes even if the plugin is killed. Its reader stops the
      // detached process group, which would otherwise outlive the host's group.
      // Helpers load first; the cloud guard rejects workspace platform overrides
      // before executing user code, so a cloud request cannot drive a local phone.
      const code = `import os as _wuu_os, signal as _wuu_signal, threading as _wuu_threading
_wuu_os.set_blocking(3, True)
def _wuu_watch_host():
    try:
        _wuu_os.read(3, 1)
    finally:
        _wuu_os.killpg(_wuu_os.getpgrp(), _wuu_signal.SIGKILL)
_wuu_threading.Thread(target=_wuu_watch_host, daemon=True).start()
from importlib.metadata import version as _wuu_version
if _wuu_version("phone-harness") != "${CLI_VERSION}":
    raise RuntimeError("This Wuu plugin requires phone-harness ${CLI_VERSION}; install that version before controlling a phone")
${args.target === "cloud" ? 'if "PHONE_HARNESS_PLATFORM" in _wuu_os.environ:\n    raise RuntimeError("Remove PHONE_HARNESS_PLATFORM from the Phone Harness workspace .env to use a cloud session")\n' : ""}exec(compile(${JSON.stringify(args.code)}, "<phone_exec>", "exec"), globals())
`;
      const started = Date.now();
      const output = await runScript(code, call.cwd, env, timeout, signal);
      const content: ToolContentPart[] = [];
      let screenshot: string | undefined;
      let imageError: string | undefined;
      // screenshot() prints an absolute path on its own line, including paths with spaces.
      // Read only the newest image from this call; older printed files are not observations.
      for (const line of output.stdout.split(/\r?\n/).reverse()) {
        const path = line.trim();
        const mime = IMAGE_TYPES[extname(path).toLowerCase()];
        if (!mime || !isAbsolute(path)) continue;
        try {
          const file = await open(path, "r");
          try {
            const info = await file.stat();
            if (!info.isFile() || info.mtimeMs < started) continue;
            screenshot = path;
            if (info.size > IMAGE_BYTES) {
              imageError = `Screenshot exceeds ${IMAGE_BYTES} bytes; resize it before returning it.`;
            } else {
              const data = Buffer.alloc(info.size + 1);
              let length = 0;
              while (length < data.length) {
                const { bytesRead } = await file.read(data, length, data.length - length, null);
                if (!bytesRead) break;
                length += bytesRead;
              }
              if (length > info.size) imageError = "Screenshot changed while being read; capture it again.";
              else content.push({ type: "image", data: data.subarray(0, length).toString("base64"), mime_type: mime });
            }
          } finally {
            await file.close();
          }
          break;
        } catch (error) {
          imageError = `Could not attach screenshot: ${error instanceof Error ? error.message : String(error)}`;
          break;
        }
      }
      const result = { ...output, target: args.target, serial: args.serial, session: args.session, screenshot, image_error: imageError };
      content.unshift({ type: "text", text: JSON.stringify(result) });
      return { result: { content, structured_content: result, is_error: output.exit_code !== 0 || Boolean(output.error) || Boolean(imageError) } };
    } finally {
      await releaseFileLock(lifecycleHost, lock.lock_id);
    }
  },
};

interface ScriptOutput {
  exit_code: number | null;
  stdout: string;
  stderr: string;
  stdout_truncated: boolean;
  stderr_truncated: boolean;
  error?: string;
}

// Keep the pipes draining after the retained tail is full so verbose scripts cannot
// block the phone process. Killing its process group also stops active adb children.
function runScript(code: string, cwd: string, env: NodeJS.ProcessEnv, timeout: number, signal: AbortSignal): Promise<ScriptOutput> {
  signal.throwIfAborted();
  return new Promise((resolve) => {
    const child = spawn(env.WUU_PHONE_HARNESS || "phone-harness", [], { cwd, env, detached: true, stdio: ["pipe", "pipe", "pipe", "pipe"] });
    const tails = { stdout: Buffer.alloc(0), stderr: Buffer.alloc(0) };
    const totals = { stdout: 0, stderr: 0 };
    let error: string | undefined;
    let stopped = false;
    const stop = (reason: string) => {
      if (stopped) return;
      stopped = true;
      error = reason + " Input may already have taken effect. Observe the phone before continuing; do not replay this script automatically.";
      if (child.pid) {
        try { process.kill(-child.pid, "SIGKILL"); }
        catch (failure) {
          if ((failure as NodeJS.ErrnoException).code !== "ESRCH") {
            error += ` Process cleanup failed: ${String(failure)}`;
            child.kill("SIGKILL");
          }
        }
      }
    };
    const abort = () => stop("Phone execution cancelled.");
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) abort();
    const timer = setTimeout(() => stop(`Phone execution timed out after ${timeout}s.`), timeout * 1000);
    for (const stream of ["stdout", "stderr"] as const) {
      child[stream].on("data", (chunk: Buffer) => {
        totals[stream] += chunk.length;
        tails[stream] = Buffer.concat([tails[stream], chunk]).subarray(-OUTPUT_BYTES);
      });
    }
    child.on("error", (failure: NodeJS.ErrnoException) => {
      error = failure.code === "ENOENT"
        ? `Phone Harness was not found. Install with: uv tool install --python 3.12 phone-harness==${CLI_VERSION}. If installed elsewhere, set WUU_PHONE_HARNESS to its absolute executable path before starting Wuu.`
        : `Could not run Phone Harness: ${failure.message}`;
    });
    child.stdin.on("error", (failure: NodeJS.ErrnoException) => {
      if (failure.code !== "EPIPE") stop(`Could not send the phone script: ${failure.message}`);
    });
    child.on("close", (exit_code, exitSignal) => {
      clearTimeout(timer);
      signal.removeEventListener("abort", abort);
      resolve({ exit_code, stdout: tails.stdout.toString("utf8"), stderr: tails.stderr.toString("utf8"), stdout_truncated: totals.stdout > OUTPUT_BYTES, stderr_truncated: totals.stderr > OUTPUT_BYTES, error: error ?? (exitSignal ? `Phone Harness exited on ${exitSignal}; input may have taken effect.` : undefined) });
    });
    child.stdin.end(code);
  });
}

export default plugin;
