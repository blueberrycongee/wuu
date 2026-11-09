import type { ApplicationTerminalsV1, TerminalCallOptionsV1, TerminalStartV1, TerminalEventV1, TerminalManagedReadV1 } from "../../../../packages/plugin-sdk/src/ui-terminals";
import type { WuuDesktopApi } from "../../shared/protocol";

type TerminalHost = Pick<WuuDesktopApi, "startTerminalSession" | "writeTerminalSession" | "resizeTerminalSession" | "stopTerminalSession" | "onTerminalEvent" | "listManagedProcesses" | "readManagedProcess" | "writeManagedProcess" | "resizeManagedProcess" | "stopManagedProcess" | "unsupportedMethods">;
function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => { if (!item || typeof item !== "object") return; Object.values(item).forEach(freeze); Object.freeze(item); };
  freeze(copy); return copy;
}

/** Owns native PTYs, never the agent processes inspected through managed controls. */
export class ApplicationTerminals implements ApplicationTerminalsV1 {
  private disposed = false;
  private readonly resources = new Map<string, Promise<void> | undefined>();
  private readonly starts = new Set<Promise<unknown>>();
  private readonly subscriptions = new Set<() => void>();
  readonly capabilities: ApplicationTerminalsV1["capabilities"];
  constructor(private readonly api: TerminalHost) {
    this.capabilities = Object.freeze({createTerminal:!api.unsupportedMethods?.includes("startTerminalSession")});
  }
  private check(options?: TerminalCallOptionsV1): void {
    if (this.disposed || options?.signal?.aborted) throw new DOMException("Terminal operation cancelled", "AbortError");
  }
  private async run<T>(operation: () => Promise<T>, options?: TerminalCallOptionsV1): Promise<T> {
    this.check(options); const value = await operation(); this.check(options); return immutable(value);
  }
  async start(input: TerminalStartV1, options?: TerminalCallOptionsV1) {
    this.check(options); if (!this.capabilities.createTerminal) throw new Error("Terminal creation unavailable");
    const pending = this.api.startTerminalSession({...input}); this.starts.add(pending);
    try {
      const result = await pending; this.resources.set(result.id,undefined);
      try { this.check(options); } catch (error) { await this.stop(result.id); throw error; }
      return immutable(result);
    } finally { this.starts.delete(pending); }
  }
  async stop(id: string): Promise<void> {
    if (!this.resources.has(id)) throw new Error("Terminal is not owned by this controller");
    let pending = this.resources.get(id);
    if (!pending) {
      pending = Promise.resolve().then(async () => { await this.api.stopTerminalSession(id); }).catch(error => {
        // Retain ownership so a later explicit cleanup can retry a failed stop.
        this.resources.set(id, undefined);
        throw error;
      });
      this.resources.set(id, pending);
    }
    return pending;
  }
  async dispose(): Promise<void> {
    this.disposed = true; for (const stop of this.subscriptions) stop();
    await Promise.allSettled([...this.starts]);
    await Promise.all([...this.resources.keys()].map(id => this.stop(id)));
  }
  async write(id: string,data: string,options?: TerminalCallOptionsV1): Promise<void> { await this.run(() => this.api.writeTerminalSession(id,data),options); }
  async resize(id: string,cols: number,rows: number,options?: TerminalCallOptionsV1): Promise<void> { await this.run(() => this.api.resizeTerminalSession(id,cols,rows),options); }
  onEvent(listener: (event: TerminalEventV1) => void): () => void {
    this.check(); let stopped = false;
    const unsubscribe = this.api.onTerminalEvent(event => { if (!this.disposed && !stopped) listener(immutable(event)); });
    const stop = () => { if (stopped) return; stopped = true; this.subscriptions.delete(stop); unsubscribe(); };
    this.subscriptions.add(stop); return stop;
  }
  listManaged(thread: string,options?: TerminalCallOptionsV1) { return this.run(() => this.api.listManagedProcesses(thread),options); }
  readManaged(input: TerminalManagedReadV1,options?: TerminalCallOptionsV1) { return this.run(() => this.api.readManagedProcess({...input}),options); }
  writeManaged(thread: string,id: string,input: string,options?: TerminalCallOptionsV1) { return this.run(() => this.api.writeManagedProcess(thread,id,input),options); }
  resizeManaged(thread: string,id: string,cols: number,rows: number,options?: TerminalCallOptionsV1) { return this.run(() => this.api.resizeManagedProcess(thread,id,cols,rows),options); }
  stopManaged(thread: string,id: string,options?: TerminalCallOptionsV1) { return this.run(() => this.api.stopManagedProcess(thread,id),options); }
}
