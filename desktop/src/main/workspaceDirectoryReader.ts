import type { Worker } from "node:worker_threads";
import type { WorkspaceDirectoryListResult } from "../shared/protocol";

export type DirectoryReadRequest = { id: number; root: string; path?: string };
export type DirectoryReadResponse =
  | { id: number; result: WorkspaceDirectoryListResult }
  | { id: number; error: { name: string; message: string; stack?: string; code?: string } };
type PendingRead = {
  request: DirectoryReadRequest;
  owner: number;
  cancelled?: boolean;
  resolve: (result: WorkspaceDirectoryListResult) => void;
  reject: (error: Error) => void;
};
type WorkerSlot = { worker: Worker; pending?: PendingRead; failure?: Error };

const MAX_WORKERS = 2;
const MAX_QUEUED_READS = 64;
const SHUTDOWN_TIMEOUT_MS = 1000;

// Keep the existing synchronous validation/read algorithm off Electron's main
// loop without adding await gaps inside it. This retains its existing external
// filesystem TOCTOU limitations; workers are not a new filesystem sandbox.
export class WorkspaceDirectoryReader {
  private readonly workers = new Set<WorkerSlot>();
  private queue: PendingRead[] = [];
  private nextID = 0;
  private closed = false;
  private closing?: Promise<void>;

  constructor(private readonly createWorker: () => Worker) {}

  list(root: string, path: string | undefined, owner: number): Promise<WorkspaceDirectoryListResult> {
    if (this.closed) return Promise.reject(new Error("directory reader is closed"));
    if (this.queue.length >= MAX_QUEUED_READS) return Promise.reject(new Error("directory read queue is full"));
    return new Promise((resolve, reject) => {
      this.queue.push({ request: { id: ++this.nextID, root, path }, owner, resolve, reject });
      this.dispatch();
    });
  }

  cancelOwner(owner: number): void {
    const cancel = (pending: PendingRead): void => {
      pending.cancelled = true;
      pending.reject(Object.assign(new Error("directory read cancelled"), { name: "AbortError" }));
    };
    this.queue = this.queue.filter(pending => {
      if (pending.owner !== owner) return true;
      cancel(pending);
      return false;
    });
    for (const slot of this.workers) {
      if (slot.pending?.owner === owner) cancel(slot.pending);
    }
    // A running native call cannot be cancelled portably. Retain its slot until
    // it settles, then discard its result; never spawn unbounded replacements.
  }

  close(): Promise<void> {
    if (this.closing) return this.closing;
    this.closed = true;
    const owners = new Set([
      ...this.queue.map(pending => pending.owner),
      ...[...this.workers].flatMap(slot => slot.pending ? [slot.pending.owner] : []),
    ]);
    for (const owner of owners) this.cancelOwner(owner);
    const terminating = Promise.all([...this.workers].map(slot => {
      slot.worker.unref();
      return slot.worker.terminate();
    }));
    // Termination may wait for a native filesystem syscall to return. These
    // read-only threads must not hold up the rest of application shutdown.
    this.closing = new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("directory worker shutdown timed out; native filesystem I/O may still be pending")), SHUTDOWN_TIMEOUT_MS);
      timer.unref();
      terminating.then(() => { clearTimeout(timer); resolve(); }, error => { clearTimeout(timer); reject(error); });
    });
    return this.closing;
  }

  private dispatch(): void {
    while (!this.closed && this.queue.length > 0) {
      let slot = [...this.workers].find(candidate => !candidate.pending && !candidate.failure);
      if (!slot) {
        if (this.workers.size >= MAX_WORKERS) return;
        try {
          slot = this.startWorker();
        } catch (error) {
          this.queue.shift()!.reject(error instanceof Error ? error : new Error(String(error)));
          continue;
        }
      }
      slot.pending = this.queue.shift()!;
      slot.worker.ref();
      try {
        slot.worker.postMessage(slot.pending.request);
      } catch (error) {
        slot.failure = error instanceof Error ? error : new Error(String(error));
        slot.pending.reject(slot.failure);
        // Keep the physical slot counted until exit, including send failures.
        void slot.worker.terminate().catch(error => {
          console.error("Directory worker termination failed", error);
        });
      }
    }
  }

  private startWorker(): WorkerSlot {
    const slot: WorkerSlot = { worker: this.createWorker() };
    this.workers.add(slot);
    slot.worker.on("message", (response: DirectoryReadResponse) => {
      const pending = slot.pending;
      if (!pending || response.id !== pending.request.id) return;
      slot.pending = undefined;
      slot.worker.unref();
      if (!pending.cancelled) {
        if ("error" in response) pending.reject(Object.assign(new Error(response.error.message), response.error));
        else pending.resolve(response.result);
      }
      this.dispatch();
    });
    slot.worker.on("error", error => {
      slot.failure = error;
      slot.pending?.reject(error);
    });
    slot.worker.once("exit", code => {
      this.workers.delete(slot);
      slot.pending?.reject(slot.failure ?? new Error(`directory worker exited (${code})`));
      this.dispatch();
    });
    return slot;
  }
}
