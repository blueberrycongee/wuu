import { watch, type FSWatcher } from "node:fs";
import { realpath, stat } from "node:fs/promises";
import { basename, dirname, isAbsolute, relative, resolve, sep } from "node:path";
import type { WorkspaceFileChange } from "../shared/protocol";

const COALESCE_MS = 40;
const FALLBACK_INTERVAL_MS = 2000;
const unavailableWatchCodes = new Set(["ENOSYS", "ENOTSUP", "EINVAL", "ENOSPC", "EMFILE", "ENFILE", "EPERM", "EACCES", "ENOENT", "ENOTDIR"]);
const directories = new Map<string, DirectoryObserver>();
const targets = new Map<string, TargetObserver>();
type DirectorySignal = (force: boolean, error?: Error) => void;
type Binding = { identity: string; names: Set<string | null> };

function missing(error: unknown): boolean {
  return ["ENOENT", "ENOTDIR"].includes((error as NodeJS.ErrnoException).code ?? "");
}
function inside(path: string, root: string): boolean {
  const rel = relative(root, path);
  return rel === "" || (rel !== ".." && !rel.startsWith(`..${sep}`) && !isAbsolute(rel));
}

/** One OS handle per real directory, shared across targets, plugins and windows. */
class DirectoryObserver {
  private watcher?: FSWatcher;
  private timer?: ReturnType<typeof setInterval>;
  private readonly listeners = new Map<DirectorySignal, Set<string | null>>();

  constructor(readonly path: string, public identity: string) { this.start(); }

  private start(): void {
    try {
      const watcher = watch(this.path, { persistent: false }, (_event, filename) => {
        if (this.watcher !== watcher) return;
        const name = filename?.toString();
        for (const [signal, names] of this.listeners) {
          // Some platforms report the watched directory's own name on deletion.
          if (!name || name === basename(this.path) || names.has(null) || names.has(name)) signal(true);
        }
      });
      this.watcher = watcher;
      watcher.on("error", error => { if (this.watcher === watcher) this.fallback(error); });
    } catch (error) { this.fallback(error as Error, true); }
  }

  private fallback(error: Error, throwOnFailure = false): void {
    this.watcher?.close();
    this.watcher = undefined;
    if (!unavailableWatchCodes.has((error as NodeJS.ErrnoException).code ?? "")) {
      for (const signal of [...this.listeners.keys()]) signal(false, error);
      if (throwOnFailure) throw error;
      return;
    }
    if (this.timer) return;
    console.warn("Native workspace file observation unavailable; using shared metadata polling", (error as NodeJS.ErrnoException).code);
    this.timer = setInterval(() => {
      for (const signal of this.listeners.keys()) signal(false);
    }, FALLBACK_INTERVAL_MS);
    this.timer.unref();
  }

  rebind(identity: string): void {
    if (identity === this.identity) return;
    this.watcher?.close();
    this.watcher = undefined;
    clearInterval(this.timer); this.timer = undefined;
    this.identity = identity;
    this.start();
    for (const signal of this.listeners.keys()) signal(true);
  }

  add(names: Set<string | null>, signal: DirectorySignal): () => void {
    this.listeners.set(signal, names);
    return () => {
      this.listeners.delete(signal);
      if (this.listeners.size) return;
      this.watcher?.close();
      this.watcher = undefined;
      clearInterval(this.timer);
      if (directories.get(this.path) === this) directories.delete(this.path);
    };
  }
}

/** Recompute only after an event. Include logical and real ancestors so replacing
 * symlinks, files or directories does not leave an observer on a detached inode. */
async function inspect(root: string, path: string, previous?: { root?: string; path?: string }) {
  const bindings = new Map<string, Binding>();
  const rootReal = await realpath(root).catch(error => { if (missing(error)) return undefined; throw error; });
  const pathReal = await realpath(path).catch(error => { if (missing(error)) return undefined; throw error; });
  if (pathReal && (!rootReal || !inside(pathReal, rootReal))) throw new Error("file is outside the current workspace");
  const current = pathReal ? await stat(pathReal).catch(error => { if (missing(error)) return undefined; throw error; }) : undefined;
  if (current && !current.isFile() && !current.isDirectory()) throw new Error("selected path is not a file or directory");

  async function add(directory: string, name: string | null, withinWorkspace: boolean): Promise<boolean> {
    const real = await realpath(directory).catch(error => { if (missing(error)) return undefined; throw error; });
    if (!real) return false;
    if (withinWorkspace && (!rootReal || !inside(real, rootReal))) throw new Error("directory is outside the current workspace");
    const info = await stat(real).catch(error => { if (missing(error)) return undefined; throw error; });
    if (!info?.isDirectory()) return false;
    const binding = bindings.get(real) ?? { identity: `${info.dev}:${info.ino}`, names: new Set<string | null>() };
    binding.names.add(name);
    bindings.set(real, binding);
    return true;
  }

  // Retain resolved locations while absent: a dangling symlink does not reveal
  // where its target will be recreated, and no event need touch the symlink itself.
  const resolvedRoot = rootReal ?? previous?.root;
  const resolvedPath = pathReal ?? (rootReal === previous?.root ? previous?.path : undefined);
  for (const base of new Set([root, ...(resolvedRoot ? [resolvedRoot] : [])])) {
    let child = base;
    while (!(await add(dirname(child), basename(child), false)) && dirname(child) !== child) child = dirname(child);
  }
  for (const [base, target] of [[root, path], ...(rootReal && resolvedPath ? [[rootReal, resolvedPath]] : [])]) {
    let directory = base;
    const parts = relative(base, target).split(sep).filter(Boolean);
    for (const part of parts) {
      if (!(await add(directory, part, true))) break;
      directory = resolve(directory, part);
    }
  }
  if (current?.isDirectory()) await add(path, null, true);
  return { bindings, resolved: { root: resolvedRoot, path: resolvedPath }, exists: Boolean(current), signature: JSON.stringify([rootReal, pathReal,
    current?.dev, current?.ino, current?.mtimeMs, current?.ctimeMs, current?.size, current?.nlink]) };
}

class TargetObserver {
  readonly listeners = new Set<(change: WorkspaceFileChange) => void>();
  readonly ready: Promise<void>;
  private readonly bindings = new Map<string, { key: string; release: () => void }>();
  private timer?: ReturnType<typeof setTimeout>;
  private stopped = false;
  private running = true;
  private dirty = false;
  private force = false;
  private signature?: string;
  private resolved?: { root?: string; path?: string };

  constructor(readonly root: string, readonly path: string, private readonly key: string) {
    this.ready = this.refresh(true).finally(() => {
      this.running = false;
      if (this.dirty) this.schedule(false);
    });
  }

  private schedule = (force: boolean, error?: Error): void => {
    if (this.stopped) return;
    if (error) { this.fail(error); return; }
    this.dirty = true;
    this.force ||= force;
    if (this.running || this.timer) return;
    // A fixed window coalesces bursts without starving continuous writers.
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.running = true;
      this.dirty = false;
      void this.refresh(false).catch(error => this.fail(error)).finally(() => {
        this.running = false;
        if (this.dirty) this.schedule(false);
      });
    }, COALESCE_MS);
    this.timer.unref();
  };

  private async refresh(initial: boolean): Promise<void> {
    const force = this.force; this.force = false;
    const state = await inspect(this.root, this.path, this.resolved);
    if (this.stopped) return;
    this.resolved = state.resolved;
    if (initial && !state.exists) throw new Error("selected path does not exist");
    for (const [path, binding] of state.bindings) {
      const key = JSON.stringify([binding.identity, [...binding.names].sort()]);
      const previous = this.bindings.get(path);
      if (previous?.key === key) continue;
      let observer = directories.get(path);
      if (!observer) {
        observer = new DirectoryObserver(path, binding.identity);
        directories.set(path, observer);
      } else observer.rebind(binding.identity);
      // Each binding has its own callback identity; acquiring before releasing
      // avoids closing a shared handle during an inode/route update.
      const release = observer.add(binding.names, (force, error) => this.schedule(force, error));
      previous?.release();
      this.bindings.set(path, { key, release });
    }
    for (const [path, binding] of this.bindings) {
      if (!state.bindings.has(path)) { binding.release(); this.bindings.delete(path); }
    }
    const changed = this.signature !== undefined && (force || state.signature !== this.signature);
    this.signature = state.signature;
    if (changed) for (const listener of [...this.listeners]) listener({ type: "changed" });
    // Close the read-before-subscribe race, then ordinary events drive refreshes.
    if (initial) await this.refresh(false);
  }

  private fail(error: Error): void {
    if (this.stopped) return;
    const listeners = [...this.listeners];
    this.stop();
    for (const listener of listeners) listener({ type: "error", message: error.message });
  }

  remove(listener: (change: WorkspaceFileChange) => void): void {
    this.listeners.delete(listener);
    if (!this.listeners.size) this.stop();
  }

  private stop(): void {
    this.stopped = true;
    clearTimeout(this.timer);
    for (const binding of this.bindings.values()) binding.release();
    this.bindings.clear();
    this.listeners.clear();
    if (targets.get(this.key) === this) targets.delete(this.key);
  }
}

export async function observeWorkspaceFile(root: string, path: string, listener: (change: WorkspaceFileChange) => void): Promise<() => void> {
  const absolute = resolve(root, path);
  if (!inside(absolute, root)) throw new Error("file is outside the current workspace");
  const key = JSON.stringify([root, absolute]);
  let target = targets.get(key);
  if (!target) { target = new TargetObserver(root, absolute, key); targets.set(key, target); }
  const deliver = (change: WorkspaceFileChange) => listener(change);
  target.listeners.add(deliver);
  try { await target.ready; }
  catch (error) { target.remove(deliver); throw error; }
  return () => target.remove(deliver);
}
