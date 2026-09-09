// Remote-control host management for the desktop shell.
//
// The remote host (`wuu remote host`) is a machine-global daemon that serves
// paired phones through the relay. It is a separate child process from the
// per-workdir app-server pool: blocking, single-instance, with its own
// lifecycle. This module owns that child plus the one-shot CLI surface the
// settings panel needs (status, relay config, device revocation).
//
// Electron-free by design: command resolution, spawning, and env are
// injectable so the manager is unit-testable without a running shell.

import { spawn as nodeSpawn } from "node:child_process";
import { resolveWuuCommand, WuuCommand } from "./wuuCommand";

export type RemoteDevice = {
  pub: string;
  fingerprint: string;
  name?: string;
  added_at: string;
};

export type RemoteStatus = {
  fingerprint: string;
  account_server?: string;
  host_name?: string;
  relay_url?: string;
  store: string;
  devices: RemoteDevice[];
};

export type RemoteHostEvent =
  | { kind: "pair-uri"; uri: string }
  | { kind: "paired"; detail: string }
  | { kind: "host-log"; message: string }
  | { kind: "host-exit"; code: number | null };

// The narrow child-process surface the manager needs, so tests can script it.
export type RemoteChildStream = {
  setEncoding(encoding: string): void;
  on(event: "data", listener: (chunk: string) => void): void;
};

export type RemoteChild = {
  stdin?: { end(data: string): void };
  stdout: RemoteChildStream;
  stderr: RemoteChildStream;
  on(event: "exit" | "close", listener: (code: number | null) => void): void;
  on(event: "error", listener: (err: Error) => void): void;
  kill(signal?: NodeJS.Signals): boolean;
};

export type RemoteSpawn = (
  command: string,
  args: string[],
  options: { cwd: string; env: NodeJS.ProcessEnv },
) => RemoteChild;

export type RemoteHostManagerOptions = {
  onEvent?: (event: RemoteHostEvent) => void;
  spawn?: RemoteSpawn;
  resolveCommand?: (workdir: string) => WuuCommand;
  env?: NodeJS.ProcessEnv;
  execTimeoutMs?: number;
  killGraceMs?: number;
  appServerEndpoint?: () => { address: string; token: string } | undefined;
};

export class RemoteHostManager {
  private child: RemoteChild | null = null;
  private childWorkdir: string | null = null;
  private pairUri: string | null = null;
  private relayOverride: string | undefined;
  private pairExpiry: ReturnType<typeof setTimeout> | undefined;
  private hostExitPromise: Promise<void> | null = null;
  private resolveHostExit: (() => void) | null = null;

  constructor(private readonly opts: RemoteHostManagerOptions = {}) {}

  async account(workdir: string, action: string, input: Record<string,string> = {}): Promise<Record<string,unknown>> {
    if (!['status','login','register','logout','revoke','password','recover'].includes(action)) throw new Error('Unknown account operation');
    return JSON.parse(await this.exec(workdir, ['remote','account',action], undefined, JSON.stringify(input)));
  }

  /** Reads the host-side remote configuration (identity, relay, devices). */
  async status(workdir: string): Promise<RemoteStatus> {
    const stdout = await this.exec(workdir, ["remote", "status", "--json"]);
    return JSON.parse(stdout) as RemoteStatus;
  }

  /** Configures (or replaces) the relay the host reports to. */
  async setRelay(workdir: string, relayUrl: string): Promise<void> {
    await this.exec(workdir, ["remote", "init", "--relay", relayUrl]);
  }

  /** Revokes a paired phone. The running host keeps an in-memory device
   *  store, so when one is up it is restarted for the revocation to gate new
   *  handshakes immediately (without reopening a pairing window). */
  async removeDevice(workdir: string, fingerprintOrPub: string): Promise<void> {
    await this.exec(workdir, ["remote", "devices", "remove", fingerprintOrPub]);
    if (this.child) {
      const restartWorkdir = this.childWorkdir ?? workdir;
      await this.stopHost();
      this.startHost(restartWorkdir, { pair: false, relay: this.relayOverride });
    }
  }

  /** Spawns the long-lived `wuu remote host` daemon. With pair=true a
   *  pairing window opens and the pairing URI is surfaced via onEvent (and
   *  currentPairUri) as soon as the daemon prints it. */
  startHost(workdir: string, options: { pair?: boolean; relay?: string } = {}): void {
    if (this.child) {
      return;
    }
    const command = this.commandFor(workdir);
    const args = [...command.args, "remote", "host", "--workdir", workdir];
    this.relayOverride = options.relay;
    if (options.relay) args.push("--relay", options.relay);
    if (options.pair) {
      args.push("--pair");
    }
    const endpoint = this.opts.appServerEndpoint?.();
    if (this.opts.appServerEndpoint && !endpoint) throw new Error("Shared desktop app-server is unavailable");
    const child = this.spawnFn()(command.command, args, {
      cwd: command.cwd,
      env: {
        ...this.env(),
        ...(endpoint ? {
          WUU_DESKTOP_APP_SERVER_ADDR: endpoint.address,
          WUU_DESKTOP_APP_SERVER_TOKEN: endpoint.token,
        } : {}),
      },
    });
    this.child = child;
    this.childWorkdir = workdir;
    clearTimeout(this.pairExpiry);
    this.pairUri = null;
    this.hostExitPromise = new Promise((resolve) => {
      this.resolveHostExit = resolve;
    });

    let buffer = "";
    child.stdout.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      if (this.child !== child) {
        return;
      }
      buffer += chunk;
      for (;;) {
        const index = buffer.indexOf("\n");
        if (index < 0) {
          return;
        }
        const line = buffer.slice(0, index).trim();
        buffer = buffer.slice(index + 1);
        if (line) {
          this.handleHostLine(line);
        }
      }
    });
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => {
      if (this.child !== child) {
        return;
      }
      const message = chunk.trim();
      if (message) {
        this.emit({ kind: "host-log", message });
      }
    });
    child.on("error", (err) => {
      this.finalizeHostChild(
        child,
        null,
        `remote host process error: ${err.message}`,
      );
    });
    child.on("exit", (code) => {
      this.finalizeHostChild(child, code);
    });
    child.on("close", (code) => {
      this.finalizeHostChild(child, code);
    });
  }

  /** Terminates the daemon (SIGTERM, escalating to SIGKILL after a grace
   *  period) and resolves once it has exited. */
  async stopHost(): Promise<void> {
    const child = this.child;
    if (!child) {
      return;
    }
    const exited = this.hostExitPromise;
    if (!exited) {
      this.finalizeHostChild(
        child,
        null,
        "remote host lifecycle was not initialized",
      );
      return;
    }

    const forceKillTimer = setTimeout(() => {
      this.signalHostChild(child, "SIGKILL", "remote host force-stop failed");
    }, this.opts.killGraceMs ?? 3000);

    try {
      this.signalHostChild(child, "SIGTERM", "remote host stop failed");
      await exited;
    } finally {
      clearTimeout(forceKillTimer);
    }
  }

  isRunning(): boolean {
    return this.child !== null;
  }

  runningWorkdir(): string | null {
    return this.childWorkdir;
  }

  /** The open pairing window's URI, or null when none is open. */
  currentPairUri(): string | null {
    return this.pairUri;
  }

  private finalizeHostChild(
    child: RemoteChild,
    code: number | null,
    errorMessage = "",
  ): void {
    if (this.child !== child) {
      return;
    }

    const resolveExit = this.resolveHostExit;
    this.child = null;
    this.childWorkdir = null;
    clearTimeout(this.pairExpiry);
    this.pairUri = null;
    this.hostExitPromise = null;
    this.resolveHostExit = null;

    // Resolve stopHost before notifying observers. A renderer callback can
    // immediately start a replacement host; late events from this child are
    // identity-guarded and cannot clear or mutate that replacement.
    resolveExit?.();
    if (errorMessage) {
      this.emit({ kind: "host-log", message: errorMessage });
    }
    this.emit({ kind: "host-exit", code });
  }

  private signalHostChild(
    child: RemoteChild,
    signal: NodeJS.Signals,
    failureContext: string,
  ): void {
    try {
      if (child.kill(signal)) {
        return;
      }
      this.finalizeHostChild(
        child,
        null,
        `${failureContext}: signal was not sent`,
      );
    } catch (error) {
      this.finalizeHostChild(
        child,
        null,
        remoteHostProcessError(failureContext, error),
      );
    }
  }

  private handleHostLine(line: string): void {
    // The daemon prints the pairing URI on its own line; the exact scheme
    // prefix keeps this parse stable across surrounding human text.
    if (line.startsWith("wuu://pair?")) {
      clearTimeout(this.pairExpiry);
      this.pairExpiry = setTimeout(() => {
        this.pairUri = null;
        this.emit({ kind: "host-log", message: "Pairing window expired" });
      }, 10 * 60 * 1000);
      this.pairUri = line;
      this.emit({ kind: "pair-uri", uri: line });
      return;
    }
    if (line.startsWith("paired: ")) {
      // pair-once (the default) closes the window after the first phone.
      clearTimeout(this.pairExpiry);
      this.pairUri = null;
      this.emit({ kind: "paired", detail: line.slice("paired: ".length) });
      return;
    }
    this.emit({ kind: "host-log", message: line });
  }

  /** One-shot CLI call; resolves with stdout, rejects with stderr on failure. */
  private exec(workdir: string, args: string[], timeoutMs = this.opts.execTimeoutMs ?? 15_000, input?: string): Promise<string> {
    const command = this.commandFor(workdir);
    return new Promise((resolve, reject) => {
      const child = this.spawnFn()(command.command, [...command.args, ...args], {
        cwd: command.cwd,
        env: this.env(),
      });
      if (input !== undefined) {
        if (!child.stdin) { child.kill(); reject(new Error("Account command stdin unavailable")); return; }
        child.stdin.end(input);
      }
      let stdout = "";
      let stderr = "";
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk) => {
        stdout += chunk;
      });
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk) => {
        stderr += chunk;
      });
      const timer = setTimeout(() => {
        child.kill("SIGKILL");
        reject(new Error(`wuu ${args.join(" ")} timed out`));
      }, timeoutMs);
      child.on("error", (err) => {
        clearTimeout(timer);
        reject(err);
      });
      child.on("exit", (code) => {
        clearTimeout(timer);
        if (code === 0) {
          resolve(stdout);
        } else {
          reject(new Error(stderr.trim() || `wuu ${args.join(" ")} exited with code ${code}`));
        }
      });
    });
  }

  private emit(event: RemoteHostEvent): void {
    this.opts.onEvent?.(event);
  }

  private commandFor(workdir: string): WuuCommand {
    if (this.opts.resolveCommand) {
      return this.opts.resolveCommand(workdir);
    }
    return resolveWuuCommand(
      this.env(),
      workdir,
      this.env().WUU_SOURCE_ROOT,
      (process as { resourcesPath?: string }).resourcesPath,
    );
  }

  private env(): NodeJS.ProcessEnv {
    return this.opts.env ?? process.env;
  }

  private spawnFn(): RemoteSpawn {
    return this.opts.spawn ?? (nodeSpawn as unknown as RemoteSpawn);
  }
}

function remoteHostProcessError(context: string, error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `${context}: ${message}`;
}
