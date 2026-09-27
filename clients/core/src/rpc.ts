// App-server protocol client, mirrored from internal/exec's line-JSON client:
// requests carry a string id, responses echo it, notifications have a method
// but no id, and server requests (reverse direction, e.g. approvals) carry
// both — the client must answer those.
//
// Transport-neutral: lines arrive via feed() and leave via the injected
// writeLine, so the same client runs over a sealed remote channel today and
// any future transport tomorrow.

export interface ResponseError {
  code?: string;
  message: string;
}

/** One line of app-server JSON in envelope form. */
export interface ProtocolEnvelope {
  id?: string | number;
  method?: string;
  params?: unknown;
  /** Execution workspace for hosts that multiplex several Go services. */
  workdir?: string;
  result?: unknown;
  error?: ResponseError;
}

export interface ServerRequest {
  id: string | number;
  method: string;
  params?: unknown;
}

export interface ServerRequestResult {
  result?: unknown;
  error?: ResponseError;
}

export type NotificationHandler = (method: string, params: unknown, workdir?: string) => void;
export type ServerRequestHandler = (req: ServerRequest) => Promise<ServerRequestResult> | ServerRequestResult;

export interface ProtocolClientOptions {
  onNotification?: NotificationHandler;
  /** Answers reverse requests from the app-server (approvals and friends).
   *  Without a handler the client fails closed, like non-interactive exec. */
  onServerRequest?: ServerRequestHandler;
  /** Distinguishes request ids across successive connections. */
  idPrefix?: string;
}

interface Pending {
  method: string;
  resolve: (v: unknown) => void;
  reject: (err: Error) => void;
  timer?: ReturnType<typeof setTimeout>;
  onResult?: (result: unknown) => void;
}

export class ProtocolClient {
  private nextId = 0;
  private readonly pending = new Map<string, Pending>();
  private closed: Error | null = null;

  constructor(
    private readonly writeLine: (env: ProtocolEnvelope) => void,
    private readonly opts: ProtocolClientOptions = {},
  ) {}

  /** Sends one request and resolves with its result. Timed-out requests are
   *  removed so a late response cannot retain or settle stale UI work.
   *  onResult must synchronously install a successful response; it runs before
   *  the next notification is fed. A thrown error rejects this call. */
  call<T = unknown>(method: string, params?: unknown, timeoutMs?: number, workdir?: string, onResult?: (result: T) => void): Promise<T> {
    if (method.trim() === "") return Promise.reject(new Error("method is required"));
    if (this.closed) return Promise.reject(this.closed);
    if (timeoutMs !== undefined && timeoutMs <= 0) {
      return Promise.reject(new Error(`rpc timeout: ${method}`));
    }
    this.nextId++;
    const id = `${this.opts.idPrefix ?? "rc"}-${this.nextId}`;
    return new Promise<T>((resolve, reject) => {
      const pending: Pending = { method, resolve: resolve as (v: unknown) => void, reject, onResult: onResult as Pending["onResult"] };
      this.pending.set(id, pending);
      if (timeoutMs !== undefined) {
        pending.timer = setTimeout(() => {
          if (!this.pending.delete(id)) return;
          reject(new Error(`rpc timeout: ${method}`));
        }, timeoutMs);
      }
      const env: ProtocolEnvelope = { id, method };
      if (params !== undefined) env.params = params;
      if (workdir) env.workdir = workdir;
      try {
        this.writeLine(env);
      } catch (err) {
        this.pending.delete(id);
        clearTimeout(pending.timer);
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }

  /** Routes one downlink line. Classification mirrors the reference client:
   *  method+id → server request, method only → notification, else response. */
  feed(env: ProtocolEnvelope): void {
    const method = typeof env.method === "string" ? env.method.trim() : "";
    if (method !== "" && env.id !== undefined && env.id !== null) {
      this.handleServerRequest({ id: env.id, method, params: env.params });
      return;
    }
    if (method !== "") {
      this.opts.onNotification?.(method, env.params, env.workdir);
      return;
    }
    if (env.id === undefined || env.id === null) return;
    const pending = this.pending.get(String(env.id));
    if (!pending) return;
    this.pending.delete(String(env.id));
    clearTimeout(pending.timer);
    if (env.error) {
      pending.reject(new Error(env.error.message));
    } else {
      try {
        pending.onResult?.(env.result);
        pending.resolve(env.result);
      } catch (err) {
        pending.reject(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }

  private handleServerRequest(req: ServerRequest): void {
    const respond = (res: ServerRequestResult) => {
      const env: ProtocolEnvelope = { id: req.id };
      if (res.error) env.error = res.error;
      else if (res.result !== undefined) env.result = res.result;
      try {
        this.writeLine(env);
      } catch {
        // Connection died; the server will retry or fail the request.
      }
    };
    const handler = this.opts.onServerRequest;
    if (!handler) {
      respond({ error: { code: "unhandled", message: `client cannot handle app-server request ${JSON.stringify(req.method)}` } });
      return;
    }
    Promise.resolve()
      .then(() => handler(req))
      .then(respond)
      .catch((err: unknown) => {
        respond({ error: { code: "handler_error", message: err instanceof Error ? err.message : String(err) } });
      });
  }

  /** Rejects all pending calls; further calls fail immediately. Used when a
   *  reconnect lands on a fresh app-server connection (resumed=false). */
  close(reason?: string): void {
    if (this.closed) return;
    this.closed = new Error(reason ?? "app-server protocol closed");
    for (const [, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.reject(new Error(`app-server protocol closed before ${pending.method} response`));
    }
    this.pending.clear();
  }

  isClosed(): boolean {
    return this.closed !== null;
  }
}
