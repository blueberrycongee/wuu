import type { AppServerNotification, AppServerResponse } from "../shared/protocol";
import type { AppServerClient, AppServerClientEvent } from "./appServerClients";
import { AppServerResponseError } from "./appServerErrors";
import {
  SessionOutcomeUnknownError, type DesktopSessionConnection, type DesktopSessionRoutes,
  type RemoteSessionRoute, type SessionRPC, type SessionReverseRequest, type SessionReverseResult,
} from "./sessionRoutes";

export type PooledAppServerClient = Pick<AppServerClient,
  "workdir" | "workspaceId" | "userBinding" | "start" | "bindRequest" | "bindCleanup" | "request" | "respond" | "reject" |
  "dispose" | "touch" | "lastUsed" | "isBusy" | "ownerLocalSessionLocator" | "federationLocator" | "knownThreadCwds" | "runningThreadIDsList" | "runningThreadCwds">;

const SAFE_REVERSE_METHODS = new Set([
  "approval/request", "user-input/request", "item/commandExecution/requestApproval",
  "item/fileChange/requestApproval", "item/tool/requestUserInput",
]);
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

type UserBinding = Readonly<{ userSpaceId: string; productDomain: string; compositionProfile?: string }>;
class UserHostBindingError extends AppServerResponseError {}

export class RemoteAppServerClient implements PooledAppServerClient {
  ownerLocalSessionLocator(): undefined { return undefined; }
  federationLocator(): undefined { return undefined; }
  readonly userBinding?: UserBinding;
  private verifiedUserRPC?: SessionRPC;
  private readonly rejectedUserRPCs = new WeakSet<SessionRPC>();
  private connection?: Promise<DesktopSessionConnection>;
  private connected?: DesktopSessionConnection;
  private disposed = false;
  private lastUsedAt = Date.now();
  private pending = 0;
  private running = new Set<string>();
  private knownThreads = new Set<string>();
  private reverse = new Map<string, (result: SessionReverseResult) => void>();
  private nextReverseID = 1;

  constructor(
    readonly workdir: string,
    readonly workspaceId: string,
    private readonly route: RemoteSessionRoute,
    private readonly connect: DesktopSessionRoutes["connect"],
    private readonly emit: (client: PooledAppServerClient, event: AppServerClientEvent) => void,
    private readonly onStateChange: () => void,
    private readonly isSelected: () => boolean,
    userBinding?: UserBinding,
  ) {
    this.userBinding = userBinding && Object.freeze({ ...userBinding });
    this.route = Object.freeze({ ...route, connector: Object.freeze({ ...route.connector }) });
  }

  private ensureConnection(): Promise<DesktopSessionConnection> {
    if (this.disposed) return Promise.reject(new Error("Remote desktop connection is disposed"));
    if (!this.connection) {
      this.connection = Promise.resolve().then(() => this.connect({ ...this.route, connector: { ...this.route.connector } }, {
        onNotification: (method, params, workdir) => {
          if (!this.isCurrent() || !this.userHostVerified() || (workdir && workdir !== this.route.workdir)) return;
          const message = { method, params };
          this.observeNotification(message);
          this.emit(this, { kind: "notification", message });
          this.onStateChange();
        },
        onServerRequest: request => this.reverseRequest(request),
        onAttach: event => {
          if (!event.resumed) {
            this.verifiedUserRPC = undefined;
            this.clearReverse();
            this.running.clear();
            if (this.isCurrent()) this.emit(this, { kind: "connection-reset" });
            this.onStateChange();
          }
        },
        onDetach: () => {
          this.clearReverse();
          // A resumed attach retains the same ProtocolClient and pending calls.
          // It must not resend a possibly admitted turn or change bound owners.
          if (this.isCurrent()) this.emit(this, { kind: "connection-reset" });
        },
        onState: state => {
          if (!this.isCurrent() || !this.userHostVerified()) return;
          this.running = new Set(state.running.filter(turn => turn.workdir ? turn.workdir === this.route.workdir : !!turn.thread_id && this.knownThreads.has(turn.thread_id))
            .flatMap(turn => turn.thread_id ? [turn.thread_id] : []));
          this.onStateChange();
        },
      })).then(connection => {
        if (this.disposed) { void connection.stop(); throw new Error("Remote desktop connection is disposed"); }
        this.connected = connection;
        connection.start();
        return connection;
      });
    }
    return this.connection;
  }
  private userHostVerified(rpc = this.connected?.rpc()): boolean {
    return !this.userBinding || !!rpc && this.verifiedUserRPC === rpc && !rpc.isClosed() && !this.rejectedUserRPCs.has(rpc);
  }
  private assertUserScope(params: unknown): void {
    if (!this.userBinding || !record(params)) return;
    const expected = { user_space_id: this.userBinding.userSpaceId, product_domain: this.userBinding.productDomain, composition_profile: this.userBinding.compositionProfile };
    for (const [key, value] of Object.entries(expected)) {
      if (params[key] !== undefined && params[key] !== value) throw new UserHostBindingError("user_host_binding_mismatch: The request does not match the captured user host");
    }
  }
  private isCurrent(): boolean { return !this.disposed && this.isSelected(); }
  start(): void {
    this.touch();
    void this.ensureConnection().catch(() => {
      if (this.isCurrent()) this.emit(this, { kind: "server-error", message: "The selected remote desktop connection is unavailable" });
    });
  }
  private async binding(): Promise<{ connection: DesktopSessionConnection; rpc: SessionRPC }> {
    const connection = this.connected ?? await this.ensureConnection();
    const captured = connection.rpc();
    await connection.waitAttached(10_000);
    const rpc = captured ?? connection.rpc();
    if (!rpc || connection.rpc() !== rpc || rpc.isClosed() || !this.isCurrent()) throw new Error("The selected remote desktop connection is unavailable");
    return { connection, rpc };
  }
  bindRequest(): PooledAppServerClient["request"] {
    const binding = this.binding();
    // Attachment acquisition can outlive its consumer; prevent an unobserved
    // rejected binding without changing what the bound caller receives.
    void binding.catch(() => {});
    return async <T>(method: string, params?: unknown, onResponse?: (response: AppServerResponse) => void): Promise<T> => {
      return this.requestWithBinding<T>(binding, method, params, onResponse);
    };
  }
  bindCleanup(): (method: string, params?: unknown) => () => Promise<void> {
    const binding=this.binding();void binding.catch(()=>{});
    return (method,params)=>{
      const frozen=params===undefined?undefined:JSON.parse(JSON.stringify(params));
      return async()=>{
        const {connection,rpc}=await binding;
        if(this.disposed || connection.rpc()!==rpc || rpc.isClosed())throw new Error("The owning cleanup connection generation is no longer available");
        if (!this.userHostVerified(rpc)) throw new UserHostBindingError("user_host_initialization_required: The owning user host has not been verified");
        this.assertUserScope(frozen);
        // No selector re-resolution, result observation or readable response.
        // Only the fixed operation cleanup captured before retirement is sent.
        await rpc.call(method,frozen,5000,this.route.workdir);
      };
    };
  }
  async request<T>(method: string, params?: unknown, onResponse?: (response: AppServerResponse) => void): Promise<T> {
    return this.requestWithBinding<T>(this.binding(), method, params, onResponse);
  }
  private async requestWithBinding<T>(binding: ReturnType<RemoteAppServerClient["binding"]>, method: string, params?: unknown, onResponse?: (response: AppServerResponse) => void): Promise<T> {
    this.pending++;
    try {
      const { connection, rpc } = await binding;
      if (connection.rpc() !== rpc || rpc.isClosed() || !this.isCurrent()) throw new Error("The owning remote connection generation is no longer available");
      return await this.call<T>(connection, rpc, method, params, onResponse);
    } finally { this.pending--; this.onStateChange(); }
  }
  private async call<T>(connection: DesktopSessionConnection, rpc: SessionRPC, method: string, params?: unknown, onResponse?: (response: AppServerResponse) => void): Promise<T> {
    if (method === "shutdown") throw new AppServerResponseError("Desktop connections cannot shut down a remote host");
    this.touch();
    this.assertUserScope(params);
    if (this.userBinding) {
      if (this.rejectedUserRPCs.has(rpc)) throw new UserHostBindingError("user_host_binding_mismatch: This connection generation failed user host verification");
      if (method !== "initialize" && !this.userHostVerified(rpc)) throw new UserHostBindingError("user_host_initialization_required: Initialize the selected user host before sending requests");
      if (method === "initialize") params = { ...(record(params) ? params : {}), user_space_id: this.userBinding.userSpaceId,
        product_domain: this.userBinding.productDomain, ...(this.userBinding.compositionProfile ? { composition_profile: this.userBinding.compositionProfile } : {}) };
    }
    if (record(params)) params = { ...params,
      ...(params.cwd === this.workdir ? { cwd: this.route.workdir } : {}),
      ...(params.workspace_root === this.workdir ? { workspace_root: this.route.workdir } : {}),
    };
    // Do not advertise the local browser/CUA bridge to a remote execution host.
    if (method === "initialize" && record(params)) params = { ...params, capabilities: { reverse_rpc: { methods: [...SAFE_REVERSE_METHODS] } } };
    try {
      return await rpc.call<T>(method, params, 30_000, this.route.workdir, result => {
        if (connection.rpc() !== rpc || !this.isCurrent()) throw new SessionOutcomeUnknownError("the selected route or connection generation changed");
        const response: unknown = result;
        if (this.userBinding && this.rejectedUserRPCs.has(rpc)) throw new UserHostBindingError("user_host_binding_mismatch: This connection generation failed user host verification");
        if (this.userBinding && method === "initialize") {
          if (!record(response) || response.user_space_id !== this.userBinding.userSpaceId || response.product_domain !== this.userBinding.productDomain) {
            this.verifiedUserRPC = undefined; this.rejectedUserRPCs.add(rpc); this.clearReverse(); this.running.clear();
            throw new UserHostBindingError("user_host_binding_mismatch: The initialized host does not match the captured user space and product domain");
          }
          this.verifiedUserRPC = rpc;
        } else if (!this.userHostVerified(rpc)) {
          throw new SessionOutcomeUnknownError("the user host binding is no longer verified");
        }
        if (method === "initialize" && record(response) && response.workspace_root === this.route.workdir) response.workspace_root = this.workdir;
        this.observeResponse(method, params, result);
        this.onStateChange();
        onResponse?.({ id: "remote-response", result });
      });
    } catch (error) {
      if (record(error) && error.kind === "app-server-response" && typeof error.message === "string") throw new AppServerResponseError(error.message, typeof error.code === "string" ? error.code : undefined);
      if (error instanceof SessionOutcomeUnknownError || error instanceof UserHostBindingError) throw error;
      throw new SessionOutcomeUnknownError("the connection ended before a response was confirmed");
    }
  }
  private reverseRequest(request: SessionReverseRequest): Promise<SessionReverseResult> {
    if (!this.isCurrent() || !this.userHostVerified() || !SAFE_REVERSE_METHODS.has(request.method)) {
      return Promise.resolve({ error: { code: "permission_denied", message: "Remote pairing does not grant local desktop control" } });
    }
    const id = `remote-reverse-${this.nextReverseID++}`;
    return new Promise(resolve => {
      this.reverse.set(id, resolve);
      this.emit(this, { kind: "server-request", message: { id, method: request.method, params: request.params } });
    });
  }
  respond(id: string, result: unknown): void { this.answer(id, { result }); }
  reject(id: string, message: string): void { this.answer(id, { error: { code: "rejected", message } }); }
  private answer(id: string, result: SessionReverseResult): void {
    const reply = this.reverse.get(id);
    if (!reply) throw new Error("Remote reverse request generation is no longer available");
    this.reverse.delete(id);
    reply(this.isCurrent() && this.userHostVerified() ? result : { error: { code: "route_changed", message: "Desktop route changed" } });
  }
  private clearReverse(): void {
    for (const reply of this.reverse.values()) reply({ error: { code: "disconnected", message: "Desktop connection closed" } });
    this.reverse.clear();
  }
  async dispose(): Promise<void> {
    this.disposed = true; this.verifiedUserRPC = undefined; this.clearReverse(); this.running.clear();
    const connection = await this.connection?.catch(() => undefined);
    await connection?.stop();
  }
  touch(): void { this.lastUsedAt = Date.now(); }
  lastUsed(): number { return this.lastUsedAt; }
  isBusy(): boolean { return this.pending > 0 || this.running.size > 0 || this.reverse.size > 0; }
  knownThreadCwds(): string[] { return [this.workdir]; }
  runningThreadIDsList(): string[] { return [...this.running]; }
  runningThreadCwds(): string[] { return this.isBusy() ? [this.workdir] : []; }
  private observeNotification(message: AppServerNotification): void {
    const params = record(message.params) ? message.params : undefined;
    const id = params?.thread_id;
    if (typeof id === "string") {
      this.knownThreads.add(id);
      if (message.method === "turn/started") this.running.add(id);
      if (message.method === "turn/completed" || message.method === "turn/error") this.running.delete(id);
    }
    if (message.method.startsWith("thread/")) this.observeThread(params?.thread);
  }
  private observeResponse(method: string, params: unknown, result: unknown): void {
    if (!record(result)) return;
    if (method === "thread/list" && Array.isArray(result.threads)) result.threads.forEach(thread => this.observeThread(thread));
    else if (method.startsWith("thread/")) this.observeThread(result.thread);
    else if (method === "turn/start" && record(params) && typeof params.thread_id === "string" && record(result.turn) && result.turn.status === "in_progress") this.running.add(params.thread_id);
  }
  private observeThread(thread: unknown): void {
    if (!record(thread) || typeof thread.id !== "string") return;
    this.knownThreads.add(thread.id);
    if (thread.status === "in_progress") this.running.add(thread.id); else this.running.delete(thread.id);
  }
}
