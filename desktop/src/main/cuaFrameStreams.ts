import { shutdownChild } from "./childShutdown";
import { StringDecoder } from "node:string_decoder";
import type { ActivitySession } from "../shared/protocol";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync, realpathSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

export type CUANativePiPEvent = {
  event: "ready" | "user_close" | "user_input" | "capture_status" | "geometry" | "control" | "gone" | "expand";
  action?: "takeover" | "release" | "stop";
  execution_id?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  status?: "healthy" | "idle" | "blank" | "suspended" | "stopped" | "error";
  message?: string;
};

export type CUANativePreviewFrame = {
  sequence: number;
  width: number;
  height: number;
  data: string;
};

type CUANativePreviewMessage = CUANativePiPEvent | (CUANativePreviewFrame & { event: "frame" });

export const CUA_PIP_FORCE_STOP_TIMEOUT_MS = 2_000;
const MAX_PREVIEW_MESSAGE_BYTES = 4_194_304;

export class CUALineDecoder {
  private buffer = "";
  private readonly decoder = new StringDecoder("utf8");

  push(chunk: Buffer | string): CUANativePreviewMessage[] {
    this.buffer += typeof chunk === "string" ? chunk : this.decoder.write(chunk);
    const lines = this.buffer.split("\n");
    this.buffer = lines.pop() ?? "";
    if (this.buffer.length > MAX_PREVIEW_MESSAGE_BYTES) throw new Error("native preview event exceeds the size limit");
    return lines.filter((line) => line.trim()).map((line) => {
      if (line.length > MAX_PREVIEW_MESSAGE_BYTES) throw new Error("native preview event exceeds the size limit");
      const message = JSON.parse(line) as CUANativePreviewMessage;
      if (!message || typeof message !== "object" || typeof message.event !== "string") {
        throw new Error("invalid native preview event");
      }
      if (message.event === "frame" && (
        !Number.isSafeInteger(message.sequence) || message.sequence <= 0
        || !Number.isInteger(message.width) || message.width <= 0 || message.width > 1280
        || !Number.isInteger(message.height) || message.height <= 0 || message.height > 1280
        || typeof message.data !== "string" || message.data.length === 0
      )) throw new Error("invalid native preview frame");
      return message;
    });
  }
}

export class CUANativePreview {
  private stopped = false;
  private shutdown: Promise<void> | undefined;
  private pendingSequence: number | undefined;
  private child: ChildProcessWithoutNullStreams | undefined;

  constructor(
    private readonly helper: string,
    private readonly threadID: string,
    private readonly target: string,
    private readonly processID: number | undefined,
    private readonly windowID: number | undefined,
    private readonly onEvent: (event: CUANativePiPEvent) => void,
    private readonly onError: (message: string) => void,
    private readonly onFrame: (frame: CUANativePreviewFrame) => void,
    private readonly spawnHelper: typeof spawn = spawn,
  ) {}

  start(): void {
    if (this.child || this.stopped) return;
    // Dev-only trace of the whole helper lifecycle to the Electron process
    // stderr (the `wuu-dev` terminal). Enable with WUU_CUA_DEBUG=1. This never
    // touches the PiP content or any product UI.
    const debug = process.env.WUU_CUA_DEBUG === "1";
    const trace = (message: string) => {
      if (debug) process.stderr.write(`[cua-pip:${this.target}] ${message}\n`);
    };
    const decoder = new CUALineDecoder();
    const child = this.spawnHelper(this.helper, [
      "--native-preview",
      this.threadID,
      this.target,
      String(this.processID ?? 0),
      String(this.windowID ?? 0),
      String(process.pid),
    ], { stdio: ["pipe", "pipe", "pipe"] });
    this.child = child;
    trace(`spawn helperPid=${child.pid} processID=${this.processID ?? 0} windowID=${this.windowID ?? 0}`);
    child.stdout.on("data", (chunk: Buffer) => {
      if (this.child !== child) return;
      try {
        for (const event of decoder.push(chunk)) {
          if (this.child !== child) break;
          if (event.event === "frame") {
            this.pendingSequence = event.sequence;
            // Never serialize screen pixels into debug logs.
            trace(`frame sequence=${event.sequence} size=${event.width}x${event.height}`);
            this.onFrame(event);
          } else {
            trace(`event ${JSON.stringify(event)}`);
            this.onEvent(event);
          }
        }
      } catch (error) {
        if (this.child !== child) return;
        this.stop();
        this.onError(error instanceof Error ? error.message : String(error));
      }
    });
    let stderr = "";
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk: string) => {
      stderr = (stderr + chunk).slice(-4096);
      trace(`stderr ${chunk.trim()}`);
    });
    child.stdin.on("error", (error) => {
      trace(`stdin error ${error.message}`);
      this.handleInputFailure(child, error);
    });
    child.on("error", (error) => {
      if (this.child !== child) return;
      trace(`error ${error.message}`);
      this.stop();
      this.onError(error.message);
    });
    child.on("exit", (code, signal) => {
      if (this.child !== child) return;
      this.child = undefined;
      this.pendingSequence = undefined;
      this.stopped = true;
      trace(`exit code=${code} signal=${signal}`);
      if (code !== 0) this.onError(stderr.trim() || `native PiP exited with code ${code}, signal ${signal}`);
      else this.onEvent({ event: "gone" });
    });
  }

  setVisible(visible: boolean): void {
    this.send({ type: "visible", visible });
  }

  setLive(live: boolean): void {
    this.send({ type: "live", live });
  }

  updateActivity(activity: ActivitySession): void {
    this.send({ type: "activity", state: activity.state, controller: activity.controller, error: activity.error ?? "" });
  }

  acknowledgeFrame(sequence: number): void {
    if (this.pendingSequence !== sequence) return;
    this.pendingSequence = undefined;
    this.send({ type: "ack", sequence });
  }

  stop(onStopped?: () => void): void {
    this.stopped = true;
    if (this.shutdown) {
      void this.shutdown.then(() => onStopped?.());
      return;
    }
    const child = this.child;
    this.child = undefined;
    this.pendingSequence = undefined;
    if (!child) {
      onStopped?.();
      return;
    }
    // Native staging and state restoration use public AX APIs. Leave enough
    // time for a graceful close to put a hidden/minimized target back before a
    // genuinely stuck helper is terminated.
    this.shutdown = shutdownChild(child, () => {
      if (!child.stdin.destroyed && !child.stdin.writableEnded) {
        child.stdin.end(`${JSON.stringify({ type: "close" })}\n`);
      } else {
        child.kill("SIGTERM");
      }
    }, CUA_PIP_FORCE_STOP_TIMEOUT_MS)
      .catch((error: Error) => {
        // Retirement is terminal. Report teardown failures to diagnostics,
        // never to a sink that may already own a newer preview source.
        process.stderr.write(`[cua-preview] ${error.message}\n`);
      });
    void this.shutdown.then(() => onStopped?.());
  }

  isLive(): boolean { return this.child !== undefined; }

  private send(command: object): void {
    const child = this.child;
    if (!child || !child.stdin.writable || child.stdin.destroyed || child.stdin.writableEnded) return;
    child.stdin.write(`${JSON.stringify(command)}\n`, (error) => {
      if (error) this.handleInputFailure(child, error);
    });
  }

  private handleInputFailure(child: ChildProcessWithoutNullStreams, error: Error): void {
    // A pipe can close between the writable-state check above and the actual
    // write. Clear this exact helper before reporting the failure so the
    // coordinator can replace it without sending another command to it.
    if (this.child !== child) return;
    this.child = undefined;
    this.pendingSequence = undefined;
    this.stopped = true;
    this.shutdown = shutdownChild(child, () => { child.kill("SIGTERM"); }, CUA_PIP_FORCE_STOP_TIMEOUT_MS)
      .catch((failure: Error) => { process.stderr.write(`[cua-preview] ${failure.message}\n`); });
    this.onError(error.message);
  }
}

export function resolveCUAFrameHelper(): string | undefined {
  if (process.platform !== "darwin") return undefined;
  const resourcesPath = (process as { resourcesPath?: string }).resourcesPath;
  const roots = [process.env.WUU_SOURCE_ROOT, process.cwd(), resolve(process.cwd(), "..")]
    .filter((value): value is string => Boolean(value));
  const mcpHelper = process.env.WUU_CUA_MAC_HELPER;
  return cuaFrameHelperCandidates(process.env, resourcesPath, roots)
    .find((candidate) => existsSync(candidate) && (!mcpHelper || !isSameExecutableFile(candidate, mcpHelper)));
}

export function isSameExecutableFile(first: string, second: string): boolean {
  if (resolve(first) === resolve(second)) return true;
  try {
    if (realpathSync(first) === realpathSync(second)) return true;
    const firstStat = statSync(first);
    const secondStat = statSync(second);
    return firstStat.dev === secondStat.dev && firstStat.ino === secondStat.ino;
  } catch {
    return false;
  }
}

export function cuaFrameHelperCandidates(
  env: NodeJS.ProcessEnv,
  resourcesPath: string | undefined,
  roots: string[],
): string[] {
  const mcpHelper = env.WUU_CUA_MAC_HELPER;
  const candidates = [
    env.WUU_CUA_MAC_PIP_HELPER,
    mcpHelper ? join(dirname(mcpHelper), "wuu-cua-mac-pip") : undefined,
    resourcesPath ? join(resourcesPath, "bin", "wuu-cua-mac-pip") : undefined,
    ...roots.map((root) => join(root, "desktop", "build", "bin", "wuu-cua-mac-pip")),
  ].filter((value): value is string => Boolean(value));
  const mcpPath = mcpHelper ? resolve(mcpHelper) : undefined;
  return [...new Set(candidates)].filter((candidate) => resolve(candidate) !== mcpPath);
}
