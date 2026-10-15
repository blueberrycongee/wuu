import { shutdownChild } from "./childShutdown";
import { StringDecoder } from "node:string_decoder";
import type { ActivitySession } from "../shared/protocol";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync, realpathSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

export type CUANativePiPEvent = {
  event: "ready" | "user_close" | "user_input" | "capture_status" | "geometry" | "control" | "gone" | "expand" | "promote" | "shown" | "completion_expired";
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

// Host-mode lines name their session; `closed` and `failed` end one session.
type CUANativePreviewMessage = (
  | CUANativePiPEvent
  | (CUANativePreviewFrame & { event: "frame" })
  | { event: "closed" }
  | { event: "failed"; message?: string }
) & { session?: string };

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

type PreviewSessionHandlers = {
  message(message: CUANativePreviewMessage): void;
  hostExited(message: string): void;
};

// One native helper streams every live preview. replayd groups capture
// clients by executable path, so separate helper processes of one executable
// interrupt each other's streams. The host exits once its last session closes.
class CUANativePreviewHost {
  private child: ChildProcessWithoutNullStreams | undefined;
  private readonly sessions = new Map<string, PreviewSessionHandlers>();
  private stderr = "";

  constructor(private readonly helper: string, private readonly spawnHelper: typeof spawn) {}

  open(session: string, command: Record<string, unknown>, handlers: PreviewSessionHandlers): void {
    this.sessions.set(session, handlers);
    this.ensureChild();
    this.send({ ...command, session, type: "open" });
  }

  send(command: { session: string; type: string } & Record<string, unknown>): void {
    const child = this.child;
    if (!child || !child.stdin.writable || child.stdin.destroyed || child.stdin.writableEnded) return;
    child.stdin.write(`${JSON.stringify(command)}\n`, (error) => {
      if (error) this.fail(child, error.message);
    });
  }

  // Forget a session. Once none remain, closing stdin lets the helper exit.
  release(session: string): void {
    this.sessions.delete(session);
    const child = this.child;
    if (this.sessions.size > 0 || !child) return;
    this.child = undefined;
    void shutdownChild(child, () => {
      if (!child.stdin.destroyed && !child.stdin.writableEnded) child.stdin.end();
      else child.kill("SIGTERM");
    }, CUA_PIP_FORCE_STOP_TIMEOUT_MS).catch((error: Error) => {
      process.stderr.write(`[cua-preview] ${error.message}\n`);
    });
  }

  private ensureChild(): void {
    if (this.child) return;
    // Dev-only trace of the helper lifecycle to the Electron process stderr.
    // Enable with WUU_CUA_DEBUG=1. Screen pixels are never logged.
    const debug = process.env.WUU_CUA_DEBUG === "1";
    const trace = (message: string) => {
      if (debug) process.stderr.write(`[cua-pip] ${message}\n`);
    };
    const decoder = new CUALineDecoder();
    const child = this.spawnHelper(this.helper, ["--native-preview-host", String(process.pid)], { stdio: ["pipe", "pipe", "pipe"] });
    this.child = child;
    this.stderr = "";
    trace(`spawn helperPid=${child.pid}`);
    child.stdout.on("data", (chunk: Buffer) => {
      if (this.child !== child) return;
      try {
        for (const message of decoder.push(chunk)) {
          if (this.child !== child) break;
          if (message.event !== "frame") trace(`event ${JSON.stringify(message)}`);
          const session = (message as { session?: unknown }).session;
          if (typeof session === "string") this.sessions.get(session)?.message(message);
        }
      } catch (error) {
        this.fail(child, error instanceof Error ? error.message : String(error));
      }
    });
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk: string) => {
      this.stderr = (this.stderr + chunk).slice(-4096);
      trace(`stderr ${chunk.trim()}`);
    });
    child.stdin.on("error", (error) => this.fail(child, error.message));
    child.on("error", (error) => this.fail(child, error.message));
    child.on("exit", (code, signal) => {
      if (this.child !== child) return;
      trace(`exit code=${code} signal=${signal}`);
      this.child = undefined;
      this.notifyExit(this.stderr.trim() || `native PiP exited with code ${code}, signal ${signal}`);
    });
  }

  // A broken pipe or protocol error ends the shared helper; every session
  // reports it once and may start again on a fresh helper.
  private fail(child: ChildProcessWithoutNullStreams, message: string): void {
    if (this.child !== child) return;
    this.child = undefined;
    void shutdownChild(child, () => { child.kill("SIGTERM"); }, CUA_PIP_FORCE_STOP_TIMEOUT_MS)
      .catch((failure: Error) => { process.stderr.write(`[cua-preview] ${failure.message}\n`); });
    this.notifyExit(message);
  }

  private notifyExit(message: string): void {
    const sessions = [...this.sessions.values()];
    this.sessions.clear();
    for (const session of sessions) session.hostExited(message);
  }
}

const previewHosts = new WeakMap<typeof spawn, Map<string, CUANativePreviewHost>>();
let previewSessionCounter = 0;

function previewHost(helper: string, spawnHelper: typeof spawn): CUANativePreviewHost {
  let hosts = previewHosts.get(spawnHelper);
  if (!hosts) {
    hosts = new Map();
    previewHosts.set(spawnHelper, hosts);
  }
  let host = hosts.get(helper);
  if (!host) {
    host = new CUANativePreviewHost(helper, spawnHelper);
    hosts.set(helper, host);
  }
  return host;
}

export class CUANativePreview {
  private stopped = false;
  private session: string | undefined;
  private pendingSequence: number | undefined;
  private failed = false;
  private onClosed: Array<() => void> = [];
  private readonly host: CUANativePreviewHost;

  constructor(
    helper: string,
    private readonly threadID: string,
    private readonly target: string,
    private readonly processID: number | undefined,
    private readonly windowID: number | undefined,
    private readonly onEvent: (event: CUANativePiPEvent) => void,
    private readonly onError: (message: string) => void,
    private readonly onFrame: (frame: CUANativePreviewFrame) => void,
    spawnHelper: typeof spawn = spawn,
  ) {
    this.host = previewHost(helper, spawnHelper);
  }

  start(): void {
    if (this.session || this.stopped) return;
    const session = `${this.threadID}:${++previewSessionCounter}`;
    this.session = session;
    this.host.open(session, {
      activity_id: this.threadID,
      target: this.target,
      process_id: this.processID ?? 0,
      window_id: this.windowID ?? 0,
    }, {
      message: (message) => this.handleMessage(session, message),
      hostExited: (message) => this.handleClosed(session, message),
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
    this.pendingSequence = undefined;
    const session = this.session;
    if (!session) {
      onStopped?.();
      return;
    }
    if (onStopped) this.onClosed.push(onStopped);
    if (this.onClosed.length > (onStopped ? 1 : 0)) return;
    this.host.send({ session, type: "close" });
    // A session stuck in a wedged capture call must not hold teardown hostage.
    const timer = setTimeout(() => this.handleClosed(session), CUA_PIP_FORCE_STOP_TIMEOUT_MS);
    timer.unref?.();
    this.onClosed.push(() => clearTimeout(timer));
  }

  isLive(): boolean { return this.session !== undefined && !this.stopped; }

  private send(command: { type: string } & Record<string, unknown>): void {
    const session = this.session;
    if (!session || this.stopped) return;
    this.host.send({ ...command, session });
  }

  private handleMessage(session: string, message: CUANativePreviewMessage): void {
    if (this.session !== session) return;
    if (message.event === "closed") {
      this.handleClosed(session);
      return;
    }
    if (this.stopped) return;
    if (message.event === "failed") {
      this.failed = true;
      this.onError(message.message ?? "native preview failed");
      return;
    }
    if (message.event === "frame") {
      this.pendingSequence = message.sequence;
      this.onFrame(message);
      return;
    }
    this.onEvent(message);
  }

  private handleClosed(session: string, hostFailure?: string): void {
    if (this.session !== session) return;
    this.session = undefined;
    this.pendingSequence = undefined;
    this.host.release(session);
    const wasStopped = this.stopped;
    this.stopped = true;
    if (!wasStopped) {
      if (hostFailure !== undefined) this.onError(hostFailure);
      else if (!this.failed) this.onEvent({ event: "gone" });
    }
    for (const callback of this.onClosed.splice(0)) callback();
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
