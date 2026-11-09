import {
  accessSync,
  chmodSync,
  constants,
  existsSync,
  statSync,
} from "node:fs";
import { createRequire } from "node:module";
import { dirname, isAbsolute, resolve } from "node:path";
import * as pty from "node-pty";
import type {
  RuntimeContext,
  TerminalSessionActionResult,
  TerminalSessionEvent,
  TerminalSessionStartParams,
  TerminalSessionStartResult,
} from "../shared/protocol";

const requireFromMain = createRequire(import.meta.url);

type TerminalSession = {
  id: string;
  ownerWindowID: number;
  ptyProcess: pty.IPty;
  cwd: string;
  shell: string;
  startedAt: number;
};

type TerminalEventEmitter = (
  ownerWindowID: number,
  event: TerminalSessionEvent,
) => void;

export class TerminalSessionManager {
  private nextSessionID = 1;
  private sessions = new Map<string, TerminalSession>();

  constructor(readonly emit: TerminalEventEmitter) {}

  startInContext(
    context: RuntimeContext,
    params: TerminalSessionStartParams = {},
    ownerWindowID: number,
  ): TerminalSessionStartResult {
    const cwd = resolveTerminalCwd(context, params);
    const id = `term-${this.nextSessionID++}`;
    const startedAt = Date.now();
    const shell = terminalShell();
    const cols = normalizeTerminalSize(params.cols, 80, 20, 500);
    const rows = normalizeTerminalSize(params.rows, 24, 6, 200);
    ensureNodePtyHelperExecutable();
    const ptyProcess = pty.spawn(shell.command, shell.args, {
      name: "xterm-256color",
      cols,
      rows,
      cwd,
      env: {
        ...process.env,
        CLICOLOR: "1",
        COLORTERM: "truecolor",
        FORCE_COLOR: "1",
        TERM: "xterm-256color",
      },
    });

    const entry: TerminalSession = {
      id,
      ownerWindowID,
      ptyProcess,
      cwd,
      shell: shell.command,
      startedAt,
    };
    this.sessions.set(id, entry);

    ptyProcess.onData((text) =>
      this.emit(ownerWindowID, { type: "data", id, text }),
    );
    ptyProcess.onExit((event) => {
      this.sessions.delete(id);
      this.emit(ownerWindowID, {
        type: "exit",
        id,
        exit_code: event.exitCode,
        signal: event.signal ?? null,
        duration_ms: Date.now() - startedAt,
        finished_at: new Date().toISOString(),
      });
    });

    return {
      id,
      cwd,
      shell: shell.command,
      started_at: new Date(startedAt).toISOString(),
    };
  }

  write(
    id: string,
    data: string,
    ownerWindowID: number,
  ): TerminalSessionActionResult {
    const session = this.sessions.get(id);
    if (!session || session.ownerWindowID !== ownerWindowID) {
      return { ok: false };
    }
    session.ptyProcess.write(data);
    return { ok: true };
  }

  resize(
    id: string,
    cols: number,
    rows: number,
    ownerWindowID: number,
  ): TerminalSessionActionResult {
    const session = this.sessions.get(id);
    if (!session || session.ownerWindowID !== ownerWindowID) {
      return { ok: false };
    }
    session.ptyProcess.resize(
      normalizeTerminalSize(cols, 80, 20, 500),
      normalizeTerminalSize(rows, 24, 6, 200),
    );
    return { ok: true };
  }

  stop(id: string, ownerWindowID: number): TerminalSessionActionResult {
    const session = this.sessions.get(id);
    if (!session || session.ownerWindowID !== ownerWindowID) {
      return { ok: false };
    }
    this.terminate(session);
    return { ok: true };
  }

  cleanup(): void {
    for (const session of this.sessions.values()) {
      this.terminate(session);
    }
    this.sessions.clear();
  }

  stopForOwner(ownerWindowID: number): void {
    for (const session of [...this.sessions.values()]) {
      if (session.ownerWindowID === ownerWindowID) {
        this.terminate(session);
      }
    }
  }

  private terminate(session: TerminalSession): void {
    this.sessions.delete(session.id);
    try {
      session.ptyProcess.kill();
    } catch (error) {
      this.emit(session.ownerWindowID, {
        type: "error",
        id: session.id,
        message:
          error instanceof Error
            ? error.message
            : "Failed to stop terminal session.",
        finished_at: new Date().toISOString(),
      });
    }
  }
}

// resolveTerminalCwd picks the pty spawn directory: an explicit override
// (params.cwd) takes precedence over the global runtime context's cwd. The
// override is used when the terminal panel is rooted at the active
// thread's own cwd (e.g. a worktree fork) rather than the active project —
// see workspacePanelContext in AppState.ts. The override originates from
// the app server (Thread.cwd) so it is trusted, but is still normalized
// (resolved to an absolute path) before use.
export function resolveTerminalCwd(
  context: RuntimeContext,
  params: TerminalSessionStartParams,
): string {
  return params.cwd ? resolve(params.cwd) : context.cwd;
}

function normalizeTerminalSize(
  value: number | undefined,
  fallback: number,
  min: number,
  max: number,
): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return fallback;
  }
  return Math.max(min, Math.min(max, Math.floor(value)));
}

function terminalShell(): { command: string; args: string[] } {
  if (process.platform === "win32") {
    return { command: process.env.ComSpec || "cmd.exe", args: [] };
  }
  return { command: resolveTerminalShell(), args: ["-l"] };
}

function resolveTerminalShell(): string {
  const candidates = [
    process.env.SHELL,
    "/bin/zsh",
    "/bin/bash",
    "/bin/sh",
    "/usr/bin/zsh",
    "/usr/bin/bash",
    "/usr/bin/sh",
  ];
  for (const candidate of candidates) {
    if (isExecutableFile(candidate)) {
      return candidate;
    }
  }
  return "/bin/sh";
}

function ensureNodePtyHelperExecutable(): void {
  if (process.platform === "win32") {
    return;
  }
  let helperPath: string;
  try {
    const nodePtyMain = requireFromMain.resolve("node-pty");
    helperPath = resolve(
      dirname(nodePtyMain),
      "..",
      "prebuilds",
      `${process.platform}-${process.arch}`,
      "spawn-helper",
    );
    helperPath = helperPath
      .replace("app.asar", "app.asar.unpacked")
      .replace("node_modules.asar", "node_modules.asar.unpacked");
  } catch {
    return;
  }
  try {
    accessSync(helperPath, constants.X_OK);
  } catch {
    if (!existsSync(helperPath)) {
      // Linux builds node-pty from source: there is no prebuilds helper to
      // repair, and pty.node resolves its own build-tree spawn-helper.
      return;
    }
    chmodSync(helperPath, statSync(helperPath).mode | 0o755);
  }
}

function isExecutableFile(path: string | undefined): path is string {
  if (!path || !isAbsolute(path)) {
    return false;
  }
  try {
    if (!statSync(path).isFile()) {
      return false;
    }
    accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}
