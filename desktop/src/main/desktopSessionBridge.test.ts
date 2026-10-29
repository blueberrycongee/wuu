import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext, ServerEvent } from "../shared/protocol";
import { AppServerClientPool } from "./appServerClients";
import { DesktopSessionBridge } from "./desktopSessionBridge";
import type { DesktopSessionRoutes, SessionRoute } from "./sessionRoutes";

vi.mock("electron", () => ({ app: { getAppPath: () => process.cwd() } }));

const sourceContext: RuntimeContext = { kind: "user", cwd: "/fixture/personal", user_space_id: "owner", product_domain: "personal" };
const targetContext: RuntimeContext = { kind: "project", cwd: "/fixture/project", project_id: "registered-project" };
const sourceIdentity = { version: 1, host_id: "source-generation", public_key: "source-public-key" };
const targetIdentity = { version: 1, host_id: "target-generation", public_key: "target-public-key" };
type Frame = { id?: string; method?: string; params?: any; result?: any; error?: { message: string } };
class Host extends EventEmitter {
  readonly stdin = new PassThrough(); readonly stdout = new PassThrough(); readonly stderr = new PassThrough(); readonly admission = new PassThrough();
  readonly stdio = [this.stdin, this.stdout, this.stderr, this.admission];
  killed = false;
  readonly requests: Frame[] = [];
  readonly replies = new Map<string, (value: Frame) => void>();
  constructor(readonly handle: (frame: Frame) => unknown | Promise<unknown>) {
    super();
    this.on("exit", () => { this.killed = true; });
    this.stdin.on("data", (data) => {
      for (const line of String(data).trim().split("\n")) {
        const frame: Frame = JSON.parse(line);
        if (!frame.method) { this.replies.get(frame.id!)?.(frame); continue; }
        this.requests.push(frame);
        void Promise.resolve().then(() => this.handle(frame)).then(result => {
          if (!this.killed) this.stdout.write(JSON.stringify({ id: frame.id, result }) + "\n");
          if (frame.method === "shutdown") this.emit("exit", 0, null);
        }, error => this.stdout.write(JSON.stringify({ id: frame.id, error: { message: String(error) } }) + "\n"));
      }
    });
  }
  kill() { this.killed = true; this.emit("exit", 0, null); return true; }
  reverse(params: unknown): Promise<Frame> {
    const id = `ticket-${this.replies.size}`;
    return new Promise(resolve => { this.replies.set(id, resolve); this.stdout.write(JSON.stringify({ id, method: "session/desktop/dispatch", params }) + "\n"); });
  }
}
const pools: AppServerClientPool[] = [];
afterEach(async () => { await Promise.all(pools.splice(0).map(pool => pool.shutdown())); vi.unstubAllEnvs(); });

async function fixture(options: { route?: (context: RuntimeContext) => SessionRoute; dispatch?: (frame: Frame, host: Host) => unknown; exportSource?: Record<string, unknown>; contexts?: RuntimeContext[]; bindTarget?: (identity: any) => void; list?: (result: any) => any } = {}) {
  vi.stubEnv("WUU_DESKTOP_CORE", "fixture-core");
  const hosts = new Map<string, Host[]>(); const events: ServerEvent[] = [];
  let ticket: any; let bridge: DesktopSessionBridge; const boundExports: any[] = [];
  const routes: DesktopSessionRoutes = { select: options.route ?? (() => ({ kind: "local" })), connect: vi.fn(async () => { throw new Error("Must not connect a remote host"); }) };
  const pool = new AppServerClientPool(() => sourceContext, () => sourceContext.cwd, event => {
    events.push(event);
    if (event.kind === "server-request") void bridge.handleServerRequest(event);
  }, (_cmd, _args, spawnOptions) => {
    const isSource = spawnOptions.cwd === sourceContext.cwd;
    const generation = hosts.get(spawnOptions.cwd)?.length ?? 0;
    const identity = isSource ? sourceIdentity : { ...targetIdentity, host_id: generation ? `replacement-${generation}` : spawnOptions.cwd === targetContext.cwd ? targetIdentity.host_id : `host-${spawnOptions.cwd}` };
    const host = new Host(frame => {
      if (frame.method === "initialize") return { desktop_session_transport: identity, ...(isSource ? { user_space_id: "owner", product_domain: "personal" } : {}) };
      if (frame.method === "session/desktop/export") {
        if (!frame.params.target) return ticket;
        options.bindTarget?.(frame.params.target);
        const bound = { ...ticket, payload: JSON.stringify({ ...JSON.parse(ticket.payload), target: frame.params.target }) + "  \n" };
        boundExports.push(bound);
        return bound;
      }
      if (frame.method === "session/desktop/list") {
        const result = { sessions: [{ session_id: "existing", title: "Existing Coding session", locator: { version: 1, execution_host: `desktop:${identity.host_id}`, session_id: "existing" } }] };
        return options.list ? options.list(result) : result;
      }
      if (frame.method === "session/desktop/dispatch") {
        if (options.dispatch) return options.dispatch(frame, host);
        const parsed = JSON.parse(frame.params.export.payload);
        return { version: 1, operation_id: parsed.request.operation_id, locator: parsed.request.locator, status: "completed", result: { accepted: true, turn_id: "turn-1" } };
      }
      return {};
    });
    hosts.set(spawnOptions.cwd, [...(hosts.get(spawnOptions.cwd) ?? []), host]);
    return host as unknown as ChildProcessWithoutNullStreams;
  }, () => ({ capabilities: { reverse_rpc: { methods: ["session/desktop/dispatch"] } } }), undefined, routes);
  pools.push(pool);
  bridge = new DesktopSessionBridge(pool, () => options.contexts ?? [targetContext]);
  await pool.requestInContext(sourceContext, "fixture/ready");
  const source = hosts.get(sourceContext.cwd)![0];
  const exportRequest = (operation: string, params: Record<string, unknown>, executionHost = "desktop-project:registered-project") => {
    const request = { version: 1, operation, operation_id: "intent-1", locator: { version: 1, execution_host: executionHost, ...(operation !== "list" ? { session_id: "existing" } : {}) }, params };
    ticket = { version: 1, payload: JSON.stringify({ version: 1, source: { ...sourceIdentity, user_space_id: "owner", product_domain: "personal", plugin_id: "fixture.personal", ...options.exportSource }, request, ...(operation === "send" ? { message: { prompt: "原话\n  exact spacing", files: [{ filename: "raw.txt", data: "AAEC/w==", sha256: "digest", media_type: "application/octet-stream" }] } } : {}) }), signature: "opaque-signature" };
    return ticket;
  };
  return { pool, bridge, hosts, source, routes, events, exportRequest, boundExports, send: () => source.reverse({ version: 1, ticket_id: "immutable-ticket", source_host: sourceIdentity.host_id }) };
}

describe("local desktop session bridge", () => {
  it("discovers existing Coding sessions and forwards the unchanged signed user material to its exact host", async () => {
    const f = await fixture();
    f.exportRequest("list", {}, "desktop");
    expect((await f.send()).result.result.sessions[0].locator).toEqual({ version: 1, execution_host: "desktop-project:registered-project", session_id: "existing" });
    const exported = f.exportRequest("send", { session_id: "existing", request_id: "intent-1", input: { prompt: "agent instruction" } });
    expect((await f.send()).result.status).toBe("completed");
    const calls = f.hosts.get(targetContext.cwd)![0].requests.filter(frame => frame.method === "session/desktop/dispatch");
    expect(calls).toHaveLength(1);
    expect(calls[0].params).toEqual({ export: f.boundExports[0], trusted_source: sourceIdentity });
    expect(JSON.parse(calls[0].params.export.payload)).toEqual({ ...JSON.parse(exported.payload), target: targetIdentity });
  });

  it.each([{ host_id: "forged" }, { public_key: "forged" }, { user_space_id: "another-owner" }, { product_domain: "coding" }])("rejects export identity substitution %j before touching a target", async exportSource => {
    const f = await fixture({ exportSource }); f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    expect((await f.send()).error?.message).toMatch(/source|identity|scope/i);
    expect(f.hosts.has(targetContext.cwd)).toBe(false);
  });

  it("rejects copied event metadata rather than accepting it as owner authentication", async () => {
    const f = await fixture();
    const fake = { kind: "server-request", workdir: sourceContext.cwd, message: { id: "forged", method: "session/desktop/dispatch", params: { version: 1, ticket_id: "ticket", source_host: sourceIdentity.host_id } } } as ServerEvent;
    await expect(f.bridge.handleServerRequest(fake)).rejects.toThrow(/owning|source/i);
    expect(f.hosts.has(targetContext.cwd)).toBe(false);
  });

  it("rejects a discovery row that associates one session's metadata with a different target session", async () => {
    const f = await fixture({ list: result => ({ sessions: [{ ...result.sessions[0], session_id: "different-session" }] }) });
    f.exportRequest("list", {}, "desktop");
    expect((await f.send()).error?.message).toMatch(/session.*locator|session.*identity/i);
  });

  it("keeps bridge receipts main-owned even if a renderer guesses the real request ID", async () => {
    const f = await fixture(); f.exportRequest("list", {}, "desktop");
    const pending = f.send();
    const event = f.events.find(event => event.kind === "server-request")!;
    if (event.kind !== "server-request") throw new Error("missing request");
    expect(() => f.pool.respondToServerRequest(event.message.id, { forged: true })).toThrow(/main.process|reserved/i);
    expect(() => f.pool.rejectServerRequest(event.message.id, "forged cancellation")).toThrow(/main.process|reserved/i);
    expect((await pending).result.status).toBe("completed");
  });

  it.each(["session/desktop/export", "session/desktop/list", "session/desktop/dispatch", "session/desktop/future"])('denies reserved method %s through ordinary request gateways', async method => {
    const f = await fixture();
    await expect(f.pool.requestInContext(sourceContext, method, { trusted_source: sourceIdentity })).rejects.toThrow(/reserved|main.process/i);
    expect(() => f.pool.requestForWorkdir(sourceContext.cwd, method)).toThrow(/reserved|main.process/i);
    expect(() => f.pool.requestForSession(sourceContext, "existing", method)).toThrow(/reserved|main.process/i);
    expect(f.source.requests.some(frame => frame.method === method)).toBe(false);
  });

  it("never connects a registered remote target or silently lists it as local", async () => {
    const remote: SessionRoute = { kind: "remote", execution_host: "remote", workdir: "/remote", connector: { kind: "module", path: "/unused" } };
    const f = await fixture({ route: context => context.kind === "project" ? remote : { kind: "local" } });
    f.exportRequest("list", {}, "desktop");
    const reply = await f.send();
    expect(reply.error?.message).toMatch(/local|remote/i);
    expect(f.routes.connect).not.toHaveBeenCalled();
  });

  it("lists beyond the pool cap and sends to the first and last logical project without unbounded residency", async () => {
    const contexts: RuntimeContext[] = Array.from({ length: 6 }, (_, i) => ({ kind: "project", project_id: `project-${i}`, cwd: `/fixture/project-${i}` }));
    const f = await fixture({ contexts }); f.exportRequest("list", {}, "desktop");
    const reply = await f.send();
    expect(reply.error).toBeUndefined();
    expect(reply.result.result.sessions).toHaveLength(6);
    for (const index of [0, 5]) {
      const locator = reply.result.result.sessions[index].locator;
      f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } }, locator.execution_host);
      expect((await f.send()).result.status).toBe("completed");
      expect([...f.hosts.values()].flat().filter(host => !host.killed)).toHaveLength(4);
    }
  });

  it("retains only an acquired operation's exact owner while other projects rotate through the pool", async () => {
    const f = await fixture();
    const target = f.pool.bindLocalContextHost(targetContext);
    const release = target.retain();
    await target.initialized;
    for (let i = 0; i < 6; i++) await f.pool.requestInContext({ kind: "project", project_id: `p-${i}`, cwd: `/fixture/rotate-${i}` }, "fixture/ready");
    expect(() => target.check()).not.toThrow();
    expect([...f.hosts.values()].flat().filter(host => !host.killed)).toHaveLength(4);
    release(); release();
    await f.pool.requestInContext({ kind: "project", project_id: "last", cwd: "/fixture/last" }, "fixture/ready");
    expect(() => target.check()).toThrow(/generation|available/);
  });

  it("does not treat a resident context's descriptive locator as the logical target's session identity", async () => {
    const f = await fixture({ contexts: [{ ...targetContext, session_locator: { version: 1, execution_host: "local", session_id: "previously-viewed" } }] });
    f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    expect((await f.send()).result.status).toBe("completed");
  });

  it.each(["inspect", "send", "cancel"])("routes %s to the existing non-default local session owner before signing", async operation => {
    const f = await fixture();
    f.exportRequest("list", {}, "desktop"); await f.send();
    const worktree: RuntimeContext = { kind: "no_project", cwd: "/fixture/worktree-owner" };
    await f.pool.requestInContext(worktree, "fixture/ready");
    const owner = f.hosts.get(worktree.cwd)![0];
    owner.stdout.write(JSON.stringify({ method: "turn/started", params: { thread_id: "existing", turn_id: "running-turn" } }) + "\n");
    f.exportRequest(operation, { session_id: "existing", ...(operation === "send" ? { input: { prompt: "agent" } } : {}), ...(operation === "cancel" ? { turn_id: "running-turn" } : {}) });
    expect((await f.send()).result.status).toBe("completed");
    expect(JSON.parse(f.boundExports[0].payload).target.host_id).toBe(`host-${worktree.cwd}`);
    expect(owner.requests.filter(frame => frame.method === "session/desktop/dispatch")).toHaveLength(1);
    expect(f.hosts.get(targetContext.cwd)![0].requests.some(frame => frame.method === "session/desktop/dispatch")).toBe(false);
  });

  it("does not fall back to the default project when the signed non-default owner disappears", async () => {
    const worktree: RuntimeContext = { kind: "no_project", cwd: "/fixture/worktree-owner" };
    let f: Awaited<ReturnType<typeof fixture>>;
    f = await fixture({ bindTarget: () => f.hosts.get(worktree.cwd)![0].emit("exit", 1, null) });
    f.exportRequest("list", {}, "desktop"); await f.send();
    await f.pool.requestInContext(worktree, "fixture/ready");
    const owner = f.hosts.get(worktree.cwd)![0];
    owner.stdout.write(JSON.stringify({ method: "turn/started", params: { thread_id: "existing", turn_id: "running-turn" } }) + "\n");
    f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    expect((await f.send()).error?.message).toMatch(/generation|available/i);
    expect(f.hosts.get(worktree.cwd)).toHaveLength(1);
    expect([...f.hosts.values()].flat().some(host => host.requests.some(frame => frame.method === "session/desktop/dispatch"))).toBe(false);
  });

  it("rejects a session-owner change during signing instead of dispatching or redirecting the bound ticket", async () => {
    const first: RuntimeContext = { kind: "no_project", cwd: "/fixture/first-owner" };
    const replacement: RuntimeContext = { kind: "no_project", cwd: "/fixture/new-owner" };
    let f: Awaited<ReturnType<typeof fixture>>;
    const announce = (context: RuntimeContext) => f.hosts.get(context.cwd)![0].stdout.write(JSON.stringify({ method: "turn/started", params: { thread_id: "existing", turn_id: "running-turn" } }) + "\n");
    f = await fixture({ bindTarget: () => { announce(replacement); } });
    await f.pool.requestInContext(first, "fixture/ready");
    await f.pool.requestInContext(replacement, "fixture/ready");
    announce(first);
    f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    expect((await f.send()).error?.message).toMatch(/owner|generation|changed/i);
    expect([...f.hosts.values()].flat().some(host => host.requests.some(frame => frame.method === "session/desktop/dispatch"))).toBe(false);
  });

  it("fences target replacement after the source binds the resolved generation and does not replay", async () => {
    let f: Awaited<ReturnType<typeof fixture>>;
    f = await fixture({ bindTarget: () => f.hosts.get(targetContext.cwd)![0].emit("exit", 1, null) });
    f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    expect((await f.send()).error?.message).toMatch(/host|generation|available/i);
    expect(f.hosts.get(targetContext.cwd)).toHaveLength(1);
    expect(f.hosts.get(targetContext.cwd)![0].requests.some(frame => frame.method === "session/desktop/dispatch")).toBe(false);
  });

  it("reports an interrupted dispatch as unknown without retrying", async () => {
    const f = await fixture({ dispatch: (_frame, host) => { host.emit("exit", 1, null); return {}; } });
    f.exportRequest("send", { session_id: "existing", input: { prompt: "agent" } });
    const reply = await f.send();
    expect(reply.result).toMatchObject({ status: "unknown", operation_id: "intent-1", error: { code: "session_outcome_unknown" } });
    expect(f.hosts.get(targetContext.cwd)).toHaveLength(1);
    expect(f.hosts.get(targetContext.cwd)![0].requests.filter(frame => frame.method === "session/desktop/dispatch")).toHaveLength(1);
  });

  it("rejects broad cancellation and preserves exact turn cancellation", async () => {
    const f = await fixture(); f.exportRequest("cancel", { session_id: "existing" });
    expect((await f.send()).error?.message).toMatch(/exact turn|queue/i);
    f.exportRequest("cancel", { session_id: "existing", turn_id: "turn-1" });
    expect((await f.send()).result.status).toBe("completed");
    const call = f.hosts.get(targetContext.cwd)![0].requests.find(frame => frame.method === "session/desktop/dispatch")!;
    expect(JSON.parse(call.params.export.payload).request.params).toEqual({ session_id: "existing", turn_id: "turn-1" });
  });
});
