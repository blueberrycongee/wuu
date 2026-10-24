export interface TerminalCallOptionsV1 { readonly signal?: AbortSignal }
export interface TerminalStartV1 { readonly cwd: string; readonly cols?: number; readonly rows?: number }
export interface TerminalSessionV1 { readonly id: string; readonly cwd: string; readonly shell: string; readonly started_at: string }
export type TerminalEventV1 =
  | { readonly type: "data"; readonly id: string; readonly text: string }
  | { readonly type: "exit"; readonly id: string; readonly exit_code: number | null; readonly signal: string | number | null; readonly duration_ms: number; readonly finished_at: string }
  | { readonly type: "error"; readonly id: string; readonly message: string; readonly finished_at: string };
export interface TerminalManagedProcessV1 {
  readonly id: string; readonly owner_kind: string; readonly owner_id: string; readonly lifecycle: string;
  readonly status: "starting" | "running" | "stopping" | "stopped" | "failed";
  readonly pid: number; readonly tty?: boolean; readonly command: string; readonly cwd: string;
  readonly started_at: string; readonly updated_at: string; readonly stopped_at?: string; readonly exit_code?: number;
  readonly last_error?: string; readonly input_available?: boolean;
  readonly trigger?: { readonly status: "waiting" | "received" | "handled" | "cancelled" | "stopped"; readonly on_exit: boolean; readonly recheck_minutes?: number; readonly next_recheck_at?: string };
}
export interface TerminalManagedReadV1 { readonly thread_id: string; readonly process_id: string; readonly offset_bytes?: number; readonly max_bytes?: number; readonly wait_ms?: number }
export interface TerminalManagedOutputV1 { readonly process: TerminalManagedProcessV1; readonly output: string; readonly truncated: boolean; readonly start_offset: number; readonly end_offset: number; readonly total_bytes: number; readonly timed_out: boolean }

/** Created PTYs belong to this UI scope. Managed processes are borrowed and survive its disposal. */
export interface ApplicationTerminalsV1 {
  readonly capabilities: { readonly createTerminal: boolean };
  start(input: TerminalStartV1, options?: TerminalCallOptionsV1): Promise<TerminalSessionV1>;
  write(id: string, data: string, options?: TerminalCallOptionsV1): Promise<void>;
  resize(id: string, cols: number, rows: number, options?: TerminalCallOptionsV1): Promise<void>;
  /** Idempotent cleanup of an owned PTY remains available after scope expiry. */
  stop(id: string): Promise<void>;
  onEvent(listener: (event: TerminalEventV1) => void): () => void;
  listManaged(threadId: string, options?: TerminalCallOptionsV1): Promise<{ readonly processes: readonly TerminalManagedProcessV1[] }>;
  readManaged(input: TerminalManagedReadV1, options?: TerminalCallOptionsV1): Promise<TerminalManagedOutputV1>;
  writeManaged(threadId: string, processId: string, input: string, options?: TerminalCallOptionsV1): Promise<{ readonly process: TerminalManagedProcessV1; readonly bytes_written: number }>;
  resizeManaged(threadId: string, processId: string, cols: number, rows: number, options?: TerminalCallOptionsV1): Promise<{ readonly process: TerminalManagedProcessV1 }>;
  stopManaged(threadId: string, processId: string, options?: TerminalCallOptionsV1): Promise<{ readonly process: TerminalManagedProcessV1 }>;
}

/** Retain late start results for cleanup; racing away their resource ID would leak the PTY. */
export function scopeApplicationTerminals(source: () => ApplicationTerminalsV1, signal: AbortSignal, assertActive: () => void): ApplicationTerminalsV1 {
  const owned = new Map<string, ApplicationTerminalsV1>();
  const stopping = new Map<string, Promise<void>>();
  const subscriptions = new Set<() => void>();
  const check = () => { assertActive(); signal.throwIfAborted(); };
  const owner = (id: string) => {
    const provider = owned.get(id); if (!provider) throw new Error("Terminal is not owned by this scope"); return provider;
  };
  const stop = async (id: string): Promise<void> => {
    const existing = stopping.get(id); if (existing) return existing;
    const provider = owned.get(id);
    if (!provider) { check(); throw new Error("Terminal is not owned by this scope"); }
    const result = Promise.resolve().then(() => provider.stop(id)); stopping.set(id, result); return result;
  };
  signal.addEventListener("abort", () => {
    for (const unsubscribe of subscriptions) unsubscribe();
    for (const id of owned.keys()) void stop(id).catch(error => console.error("Terminal scope cleanup failed", error));
  }, {once:true});
  async function invoke<T>(operation: (signal: AbortSignal) => Promise<T>, options?: TerminalCallOptionsV1, cleanup?: (value: T) => Promise<void>): Promise<T> {
    check(); options?.signal?.throwIfAborted();
    const lifetime = new AbortController();
    const abort = () => lifetime.abort(signal.aborted ? signal.reason : options?.signal?.reason);
    signal.addEventListener("abort", abort, {once:true}); options?.signal?.addEventListener("abort", abort, {once:true});
    let rejectAbort!: () => void;
    const cancelled = new Promise<never>((_resolve,reject) => { rejectAbort = () => reject(lifetime.signal.reason ?? new DOMException("Terminal operation cancelled", "AbortError")); lifetime.signal.addEventListener("abort",rejectAbort,{once:true}); });
    try {
      const pending = operation(lifetime.signal).then(async value => {
        try { check(); lifetime.signal.throwIfAborted(); }
        catch (error) { await cleanup?.(value); throw error; }
        return value;
      });
      return await Promise.race([pending,cancelled]);
    } finally {
      signal.removeEventListener("abort",abort); options?.signal?.removeEventListener("abort",abort); lifetime.signal.removeEventListener("abort",rejectAbort);
    }
  }
  return Object.freeze({
    get capabilities() { check(); return source().capabilities; },
    start: (input: TerminalStartV1, options?: TerminalCallOptionsV1) => invoke(async signal => {
      const provider = source(); const result = await provider.start(input,{signal}); owned.set(result.id,provider); return result;
    },options,result => stop(result.id)),
    stop,
    write: (id: string,data: string,options?: TerminalCallOptionsV1) => invoke(signal => owner(id).write(id,data,{signal}),options),
    resize: (id: string,cols: number,rows: number,options?: TerminalCallOptionsV1) => invoke(signal => owner(id).resize(id,cols,rows,{signal}),options),
    onEvent: (listener: (event: TerminalEventV1) => void) => {
      check(); const unsubscribe = source().onEvent(event => { if (!signal.aborted) { assertActive(); listener(event); } });
      let stopped = false; const dispose = () => { if (stopped) return; stopped = true; subscriptions.delete(dispose); unsubscribe(); };
      subscriptions.add(dispose); if (signal.aborted) dispose(); return dispose;
    },
    listManaged: (id: string,options?: TerminalCallOptionsV1) => invoke(signal => source().listManaged(id,{signal}),options),
    readManaged: (input: TerminalManagedReadV1,options?: TerminalCallOptionsV1) => invoke(signal => source().readManaged(input,{signal}),options),
    writeManaged: (thread: string,id: string,input: string,options?: TerminalCallOptionsV1) => invoke(signal => source().writeManaged(thread,id,input,{signal}),options),
    resizeManaged: (thread: string,id: string,cols: number,rows: number,options?: TerminalCallOptionsV1) => invoke(signal => source().resizeManaged(thread,id,cols,rows,{signal}),options),
    stopManaged: (thread: string,id: string,options?: TerminalCallOptionsV1) => invoke(signal => source().stopManaged(thread,id,{signal}),options),
  });
}
