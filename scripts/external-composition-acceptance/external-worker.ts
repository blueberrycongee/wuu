// A deliberately small trusted execution backend. It is not an OS sandbox:
// shell execution explicitly requires unconfined permission. All implementation
// imports are Node APIs and the packed public SDK, never Wuu internal sources.
import { spawn, type ChildProcess } from "node:child_process";
import { randomUUID } from "node:crypto";
import { lstat, mkdir, readFile, readdir, realpath, rename, stat, writeFile } from "node:fs/promises";
import { isAbsolute, relative, resolve } from "node:path";
import { runExecutionBackendV4 } from "@wuu/plugin-sdk";

interface ProcessView {
  id: string; operation_id: string; owner_kind: "main_agent" | "subagent"; owner_id: string; root_thread_id: string;
  lifecycle: "managed"; completion_mode: "detached"; status: "starting" | "running" | "stopping" | "stopped" | "failed";
  pid: number; pgid: number; log_path: string; command: string; cwd: string;
  started_at: string; updated_at: string; stopped_at?: string; exit_code?: number; last_error?: string;
  terminal_cause?: "natural_exit" | "requested_stop";
}
interface ManagedProcess {
  child?: ChildProcess; view: ProcessView; output: Buffer; done: Promise<void>;
  changed: Set<() => void>; stop?: Promise<ProcessView>;
}
const processes = new Map<string, ManagedProcess>();
let root = "", session = "";
let forwardEnv: Readonly<Record<string, string>> = {};
let journal = "", writes = Promise.resolve();

function persist(): Promise<void> {
  const content = JSON.stringify([...processes.values()].map(item => ({ process: snapshot(item), output: item.output.toString("base64") })));
  const write = writes.then(async () => { await writeFile(journal + ".pending", content); await rename(journal + ".pending", journal); });
  writes = write.catch(() => {});
  return write;
}

function owned(id: string): ManagedProcess {
  const process = processes.get(id);
  if (!process || process.view.root_thread_id !== session) throw new Error("process does not belong to this execution session");
  return process;
}
function snapshot(process: ManagedProcess): ProcessView { return { ...process.view }; }
function active(process: ManagedProcess): boolean { return ["starting", "running", "stopping"].includes(process.view.status); }
function wake(process: ManagedProcess): void { for (const notify of process.changed) notify(); }

async function workspacePath(input: string, creating = false): Promise<string> {
  const target = resolve(root, input || ".");
  let checked: string;
  try { checked = await realpath(target); }
  catch (error) {
    if (!creating || (error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    // A dangling link is not a new file: following it could create a target
    // outside this root. This fixture is trusted, not a race-proof OS sandbox.
    const existing = await lstat(target).then(() => true, error => {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") return false;
      throw error;
    });
    if (existing) throw new Error("cannot write through an unresolved filesystem entry");
    checked = resolve(await realpath(resolve(target, "..")), target.split(/[\\/]/).at(-1)!);
  }
  const child = relative(root, checked);
  if (isAbsolute(child) || child === ".." || child.startsWith("../")) throw new Error("path is outside the execution workspace");
  return checked;
}
async function stop(process: ManagedProcess): Promise<ProcessView> {
  if (process.stop) return await process.stop;
  process.stop = (async () => {
    if (!active(process)) return snapshot(process);
    process.view.status = "stopping"; process.view.terminal_cause = "requested_stop";
    const signal = (name: NodeJS.Signals) => {
      try { globalThis.process.kill(-process.view.pgid, name); }
      catch (error) { if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error; }
    };
    signal("SIGTERM");
    const timer = setTimeout(() => signal("SIGKILL"), 1000);
    try { await process.done; } finally { clearTimeout(timer); }
    return snapshot(process);
  })();
  return await process.stop;
}
function childEnvironment(): Record<string, string> {
  const env: Record<string, string> = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (value !== undefined && !/KEY|TOKEN|PASSWORD|SECRET|^WUU_/i.test(key)) env[key] = value;
  }
  return { ...env, ...forwardEnv };
}

await runExecutionBackendV4({
  capabilities: ["process.start.v1"],
  async initialize(binding) {
    if (binding.require_existing_workspace) await stat(binding.root);
    else await mkdir(binding.root, { recursive: true });
    root = await realpath(binding.root); session = binding.session; forwardEnv = binding.environment ?? {};
    const state = resolve(root, ".acceptance-execution", session); await mkdir(state, { recursive: true });
    journal = resolve(state, "processes.json");
    let saved: { process: ProcessView; output: string }[] = [];
    try { saved = JSON.parse(await readFile(journal, "utf8")); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
    for (const entry of saved) {
      if (entry.process.root_thread_id !== session) throw new Error("saved process belongs to another session");
      // Records remain reconcilable, but a PID from an earlier transport is
      // never treated as authority to signal a possibly reused OS process.
      if (["starting", "running", "stopping"].includes(entry.process.status)) {
        entry.process.status = "failed"; entry.process.last_error = "previous backend lost; process outcome requires reconciliation";
      }
      processes.set(entry.process.id, { view: entry.process, output: Buffer.from(entry.output, "base64"), done: Promise.resolve(), changed: new Set() });
    }
  },
  methods: {
    async execute(request, context) {
      context.signal.throwIfAborted();
      const args = JSON.parse(request.call.arguments || "{}");
      let value: string;
      switch (request.call.name) {
        case "read_file": {
          const bytes = await readFile(await workspacePath(args.path), { signal: context.signal });
          if (bytes.length > 1024 * 1024) throw new Error("fixture read limit exceeded");
          value = bytes.toString("utf8"); break;
        }
        case "write_file": {
          if (request.permission_mode === "read_only") throw new Error("read-only execution cannot write");
          if (typeof args.content !== "string" || Buffer.byteLength(args.content) > 1024 * 1024) throw new Error("fixture write limit exceeded");
          await writeFile(await workspacePath(args.path, true), args.content, { signal: context.signal });
          value = "external worker wrote file"; break;
        }
        case "list_files": value = (await readdir(await workspacePath(args.path ?? "."))).join("\n"); break;
        default: throw new Error(`external fixture does not implement tool ${request.call.name}`);
      }
      return { content: [{ type: "text", text: value }] };
    },
    async "process/start"(request, context) {
      if (request.permission_mode !== "unconfined") throw new Error("trusted fixture shell requires explicit unconfined permission");
      context.signal.throwIfAborted();
      if (!request.actor || !request.operation_id || typeof request.command !== "string" || !request.command) throw new Error("actor, operation_id and command required");
      const cwd = await workspacePath(request.cwd ?? ".");
      context.signal.throwIfAborted();
      const child = spawn("/bin/sh", ["-c", request.command], { cwd, detached: true, env: childEnvironment(), stdio: ["pipe", "pipe", "pipe"] });
      const now = new Date().toISOString();
      let finished!: () => void;
      const managed: ManagedProcess = {
        child, output: Buffer.alloc(0), changed: new Set(), done: new Promise(resolve => { finished = resolve; }),
        view: { id: `external-${randomUUID()}`, operation_id: request.operation_id, owner_kind: request.actor === session ? "main_agent" : "subagent", owner_id: request.actor,
          root_thread_id: session, lifecycle: "managed", completion_mode: "detached", status: "starting", pid: child.pid ?? 0,
          pgid: child.pid ?? 0, log_path: "", command: request.command, cwd, started_at: now, updated_at: now },
      };
      processes.set(managed.view.id, managed);
      const append = (bytes: Buffer) => {
        // Bounded acceptance workloads; refuse runaway output instead of hiding
        // truncation behind a fake successful byte cursor.
        if (managed.output.length + bytes.length > 2 * 1024 * 1024) { void stop(managed); return; }
        managed.output = Buffer.concat([managed.output, bytes]); wake(managed);
      };
      child.stdout!.on("data", append); child.stderr!.on("data", append);
      child.once("error", error => { managed.view.last_error = error.message; });
      child.once("close", code => {
        managed.view.exit_code = code ?? 128;
        managed.view.status = managed.view.last_error ? "failed" : "stopped";
        managed.view.stopped_at = managed.view.updated_at = new Date().toISOString();
        managed.view.terminal_cause ??= "natural_exit";
        void (async () => {
          try {
            await persist();
            if (!context.lifetimeSignal.aborted) await context.emitProcess({ Type: managed.view.status === "failed" ? "failed" : "stopped", Cause: managed.view.terminal_cause ?? "", Process: snapshot(managed) });
          } catch (error) {
            if (!context.lifetimeSignal.aborted) { managed.view.last_error = String(error); console.error(error); process.exitCode = 1; }
          } finally { wake(managed); finished(); }
        })();
      });
      await new Promise<void>((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
      managed.view.status = "running";
      if (context.signal.aborted) { await stop(managed); context.signal.throwIfAborted(); }
      await persist();
      if (context.signal.aborted) { await stop(managed); context.signal.throwIfAborted(); }
      await context.emitProcess({ Type: "started", Cause: "", Process: snapshot(managed) });
      return snapshot(managed);
    },
    "process/get": request => snapshot(owned(request.id)),
    "process/list": () => [...processes.values()].map(snapshot),
    "process/stop": request => stop(owned(request.id)),
    async "process/read"(request, context) {
      const managed = owned(request.id), options = request.options ?? {};
      const offset = options.OffsetBytes ?? 0, maxBytes = options.MaxBytes || 65536;
      if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(maxBytes) || maxBytes < 1 || maxBytes > 1048576) throw new Error("invalid output cursor or bound");
      const waitMS = Math.min(30000, Math.max(0, (options.Wait ?? 0) / 1_000_000));
      const began = Date.now();
      if (managed.output.length <= offset && active(managed) && waitMS > 0) await new Promise<void>((resolve, reject) => {
        const finish = () => { clearTimeout(timer); managed.changed.delete(finish); context.signal.removeEventListener("abort", abort); resolve(); };
        const abort = () => { clearTimeout(timer); managed.changed.delete(finish); reject(context.signal.reason); };
        const timer = setTimeout(finish, waitMS);
        managed.changed.add(finish); context.signal.addEventListener("abort", abort, { once: true });
        if (context.signal.aborted) abort();
      });
      context.signal.throwIfAborted();
      const end = Math.min(managed.output.length, offset + maxBytes);
      return { Process: snapshot(managed), Output: managed.output.subarray(offset, end).toString("utf8"), Truncated: false,
        StartOffset: Math.min(offset, end), EndOffset: end, TotalBytes: managed.output.length,
        TimedOut: Date.now() - began >= waitMS && end === offset, Duration: (Date.now() - began) * 1_000_000 };
    },
    "process/input_available": request => active(owned(request.id)),
    "process/completion_pending": request => !active(owned(request.id)),
    "process/completion_delivered": request => snapshot(owned(request.id)),
    "process/cancel_notifications": request => snapshot(owned(request.id)),
    "process/completion_mode": request => snapshot(owned(request.id)),
    async "process/cleanup"() { return { Cleaned: await Promise.all([...processes.values()].map(stop)) }; },
  },
  async close() { await Promise.all([...processes.values()].map(stop)); if (journal) await persist(); await writes; },
});
