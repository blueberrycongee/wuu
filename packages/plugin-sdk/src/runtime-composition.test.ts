import { runJSONLRuntime, type RuntimePlugin } from "./index.js";

type Frame = { id: string; method?: string; params?: any; result?: any; error?: { message: string } };
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
function assert(value: unknown, message: string): asserts value {
  if (!value) throw new Error(message);
}

// Real JSONL streams, including nested host calls, keep the regression at the
// transport boundary rather than calling plugin handlers directly.
class Peer {
  readonly frames: Frame[] = [];
  readonly running: Promise<void>;
  onCall?: (frame: Frame) => Promise<unknown>;
  private queue: string[] = [];
  private changed = deferred<void>();
  private ended = false;
  private inputFailure?: Error;
  private sequence = 0;
  private pending = new Map<string, ReturnType<typeof deferred<Frame>>>();

  constructor(plugin: RuntimePlugin) {
    this.running = runJSONLRuntime(plugin, {
      input: this.input(),
      output: { write: (line) => {
        const frame = JSON.parse(line) as Frame;
        this.frames.push(frame);
        if (frame.method) {
          void Promise.resolve().then(() => this.onCall!(frame)).then(
            (result) => this.send({ id: frame.id, result }),
            (error: Error) => this.send({ id: frame.id, error: { message: error.message } }),
          );
        } else {
          this.pending.get(frame.id)?.resolve(frame);
          this.pending.delete(frame.id);
        }
      } },
    });
  }

  send(frame: Frame): void {
    this.queue.push(`${JSON.stringify(frame)}\n`);
    this.changed.resolve();
  }

  request(method: string, params?: unknown): Promise<Frame> {
    const id = `host-${++this.sequence}`;
    const response = deferred<Frame>();
    this.pending.set(id, response);
    this.send({ id, method, params });
    return response.promise;
  }

  async start(): Promise<void> {
    const response = await this.request("initialize", {
      protocol_version: 1, capability_protocol_version: 3,
      plugin_id: "test", plugin_root: ".", project_root: ".", wuu_home: ".",
      supported_host_services: ["host.service.call"],
    });
    assert(!response.error, "initialize failed");
    await this.request("activate");
  }

  async close(error?: Error): Promise<void> {
    this.inputFailure = error;
    this.ended = true;
    this.changed.resolve();
    await this.running;
  }

  private async *input(): AsyncIterable<string> {
    while (true) {
      while (this.queue.length) yield this.queue.shift()!;
      if (this.ended) {
        if (this.inputFailure) throw this.inputFailure;
        return;
      }
      const changed = this.changed;
      await changed.promise;
      if (this.changed === changed) this.changed = deferred<void>();
    }
  }
}

const definition = () => ({ protocol_version: 3 as const });
async function reentrantServices(): Promise<void> {
  const events: string[] = [];
  const aPlugin: RuntimePlugin = {
    concurrentServices: ["a.leaf"],
    initialize: definition,
    async invokeService(params, host) {
      events.push(params.service);
      if (params.service === "a.leaf") {
        assert(host.supports("host.storage.get"), "typed kernel service did not recognize gateway support");
        const stored = await host.call("host.storage.get", { scope: "workspace", key: "report" });
        return { text: stored.value };
      }
      return await host.call("host.service.call", { service: "b.middle", method: "call" });
    },
  };
  const a = new Peer(aPlugin);
  const b = new Peer({
    initialize: definition,
    async invokeService(_params, host) {
      events.push("b.middle");
      return await host.call("host.service.call", { service: "a.leaf", method: "call" });
    },
  });
  const route = async (frame: Frame) => {
    assert(frame.method === "host.service.call", "kernel service bypassed the registry gateway");
    if (frame.params.service === "host.storage.get") return { value: "composed" };
    const target = frame.params.service === "b.middle" ? b : a;
    const response = await target.request("service.invoke", {
      ...frame.params, caller: target === a ? "b" : "a",
    });
    if (response.error) throw new Error(response.error.message);
    return response.result;
  };
  a.onCall = route;
  b.onCall = route;
  await Promise.all([a.start(), b.start()]);
  const response = await a.request("service.invoke", {
    service: "a.outer", method: "call", caller: "kernel", execution_id: "composition",
  });
  assert(response.result?.text === "composed", `nested composition failed: ${JSON.stringify(response)}`);
  assert(events.join(",") === "a.outer,b.middle,a.leaf", "service chain ordering changed");
  for (const frame of [...a.frames, ...b.frames].filter((frame) => frame.method)) {
    assert(frame.params.execution_id === "composition", "nested call lost its execution identity");
  }
  await Promise.all([a.close(), b.close()]);
}

async function sharedExecutionCancellation(): Promise<void> {
  const started = [deferred<void>(), deferred<void>()];
  const signals: AbortSignal[] = [];
  const peer = new Peer({
    concurrentServices: ["wait.service"],
    initialize: definition,
    async invokeService(params, host, execution) {
      const index = (params.params as { index: number }).index;
      signals[index] = execution.signal;
      started[index]!.resolve();
      try {
        await host.call("host.session.inspect", { session_id: "child", wait: "terminal" });
        throw new Error("host call unexpectedly completed before cancellation");
      } catch (error) {
        assert(error === execution.signal.reason, "host call did not preserve its execution cancellation");
      }
      const before = peer.frames.length;
      try { await host.call("host.storage.get", { scope: "workspace", key: "cancelled" }); }
      catch (error) { assert(error === execution.signal.reason, "cancelled execution started a new host call"); }
      assert(peer.frames.length === before, "cancelled execution emitted another host call");
      return { cancelled: execution.signal.aborted };
    },
  } as RuntimePlugin);
  peer.onCall = () => new Promise(() => {});
  await peer.start();
  const first = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "shared", params: { index: 0 } });
  await started[0]!.promise;
  const second = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "shared", params: { index: 1 } });
  await started[1]!.promise;
  peer.send({ id: "cancel", method: "execution.cancel", params: { execution_id: "shared" } });
  const responses = await Promise.all([first, second]);
  assert(signals.every((signal) => signal.aborted), "cancellation did not reach every handler in the execution");
  assert(responses.every((response) => response.result?.cancelled), "cancelled handlers failed to settle");
  for (const frame of peer.frames.filter((frame) => frame.method)) peer.send({ id: frame.id, result: {} });
  await peer.request("service.changed", { service: "remote.service" });
  assert(!peer.frames.some((frame) => frame.id === "invalid"), "late cancelled host response was treated as a request");
  await peer.close();
}

async function lifecycleBarriers(): Promise<void> {
  const events: string[] = [];
  const initializeStarted = deferred<void>();
  const initializeRelease = deferred<void>();
  const activateStarted = deferred<void>();
  const activateRelease = deferred<void>();
  const serviceStarted = deferred<void>();
  const serviceRelease = deferred<void>();
  let lifetime!: AbortSignal;
  const peer = new Peer({
    concurrentServices: ["wait.service"],
    concurrentCapabilities: ["probe.capability"],
    async initialize(_params, _host, lifecycle) {
      lifetime = lifecycle.signal;
      initializeStarted.resolve();
      await initializeRelease.promise;
      events.push("initialized");
      return definition();
    },
    async activate(_host, lifecycle) {
      assert(!lifetime.aborted, "initialization ended the background lifetime");
      activateStarted.resolve();
      lifecycle.signal.addEventListener("abort", () => activateRelease.resolve(), { once: true });
      await activateRelease.promise;
      events.push("activated");
    },
    async invokeService() {
      events.push("service started");
      serviceStarted.resolve();
      await serviceRelease.promise;
      events.push("service completed");
      return {};
    },
    invokeCapability(params) { return { output: params.output }; },
    shutdown() {
      assert(lifetime.aborted, "shutdown did not stop the background lifetime");
      events.push("shutdown");
    },
  } as RuntimePlugin);
  const initialize = peer.request("initialize", {});
  await initializeStarted.promise;
  const activate = peer.request("activate");
  const service = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "barrier" });
  initializeRelease.resolve();
  await activateStarted.promise;
  assert(events.join(",") === "initialized", "service bypassed activation barrier");
  const shutdown = peer.request("shutdown");
  const rejected = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test" });
  const rejection = await rejected;
  assert(rejection.error?.message === "runtime is shutting down", "shutdown admitted a new service call");
  assert(lifetime.aborted, "shutdown waited for activation before cancelling its background work");
  await serviceStarted.promise;
  serviceRelease.resolve();
  await Promise.all([initialize, activate, service, shutdown]);
  assert(events.join(",") === "initialized,activated,service started,service completed,shutdown", `lifecycle ordering changed: ${events}`);
  await peer.close();
}

async function transportClose(failed: boolean): Promise<void> {
  const started = deferred<void>();
  const events: string[] = [];
  let lifetime!: AbortSignal;
  const peer = new Peer({
    initialize: definition,
    activate(_host, lifecycle) { lifetime = lifecycle.signal; },
    shutdown() { events.push("shutdown"); },
    async invokeService(params, host, execution) {
      if (params.service === "queued.service") {
        events.push("queued handler ran");
        return {};
      }
      started.resolve();
      try { await host.call("host.service.call", { service: "remote.service", method: "call" }); }
      catch { events.push("pending rejected"); }
      assert(execution.signal.aborted, "EOF failed to cancel the handler");
      assert(lifetime.aborted, "EOF failed to cancel background work");
      try { await host.call("host.service.call", { service: "remote.service", method: "call" }); }
      catch { events.push("new rejected"); }
      return {};
    },
  });
  // A peer disappearing without replying must reject both the outstanding
  // call and attempts to start another call from its cleanup path.
  peer.onCall = () => new Promise(() => {});
  await peer.start();
  const response = peer.request("service.invoke", { service: "local.service", method: "call", caller: "test", execution_id: "closed" });
  await started.promise;
  const queued = peer.request("service.invoke", { service: "queued.service", method: "call", caller: "test", execution_id: "queued-close" });
  const shutdown = peer.request("shutdown");
  const failure = failed ? new Error("input stream failed") : undefined;
  let caught: unknown;
  try { await peer.close(failure); } catch (error) { caught = error; }
  assert(caught === failure, "transport did not preserve the input error");
  await response;
  assert((await queued).error, "transport loss admitted a queued handler");
  assert(!(await shutdown).error, "transport loss skipped lifecycle cleanup");
  assert(events.join(",") === "pending rejected,new rejected,shutdown", `EOF dispatch or cleanup changed: ${events}`);
}

async function serialDispatchAndFailure(): Promise<void> {
  const firstStarted = deferred<void>();
  const firstRelease = deferred<void>();
  const events: string[] = [];
  const peer = new Peer({
    concurrentServices: ["probe.service"],
    concurrentCapabilities: ["probe.capability"],
    initialize: definition,
    executeTool() {
      events.push("cancelled.tool");
      return { result: { content: [] } };
    },
    invokeCapability(params) {
      if (params.capability !== "probe.capability") events.push("cancelled.capability");
      return { output: { capability: params.capability } };
    },
    shutdown() { events.push("shutdown"); },
    async invokeService(params) {
      if (params.service === "probe.service") return {};
      events.push(params.service);
      if (params.service === "first.service") {
        firstStarted.resolve();
        await firstRelease.promise;
        throw new Error("provider failed");
      }
      return { recovered: true };
    },
  });
  await peer.start();
  const first = peer.request("service.invoke", { service: "first.service", method: "call", caller: "test" });
  await firstStarted.promise;
  const cancelled = [
    peer.request("tool.execute", { tool_id: "effect", tool: "effect", call_id: "queued", cwd: ".", arguments: {}, execution_id: "queued-tool" }),
    peer.request("capability.invoke", { capability: "agent.request.transform", input: {}, execution_id: "queued-capability" }),
    peer.request("service.invoke", { service: "cancelled.service", method: "call", caller: "test", execution_id: "queued-service" }),
  ];
  for (const executionId of ["queued-tool", "queued-capability", "queued-service"]) {
    peer.send({ id: `cancel-${executionId}`, method: "execution.cancel", params: { execution_id: executionId } });
  }
  const second = peer.request("service.invoke", { service: "second.service", method: "call", caller: "test" });
  // The probe response establishes that every cancellation frame was received.
  await peer.request("service.invoke", { service: "probe.service", method: "call", caller: "test" });
  const probe = await peer.request("capability.invoke", { capability: "probe.capability", input: {}, output: {} });
  assert(probe.result?.output?.capability === "probe.capability", "concurrent capability could not bypass blocked work");
  assert(events.join(",") === "first.service", "default service dispatch stopped being serial");
  firstRelease.resolve();
  assert((await first).error?.message === "provider failed", "service error was not returned");
  assert((await second).result?.recovered, "one provider failure poisoned later requests");
  assert((await Promise.all(cancelled)).every((response) => response.error), "cancelled queued requests did not return errors");
  assert(events.join(",") === "first.service,second.service", `cancelled queued handler ran: ${events}`);
  peer.send({ id: "late-cancel", method: "execution.cancel", params: { execution_id: "queued-service" } });
  assert((await peer.request("service.invoke", { service: "fresh.service", method: "call", caller: "test", execution_id: "fresh" })).result?.recovered,
    "late cancellation prevented fresh work");
  await peer.request("shutdown");
  assert(events.join(",") === "first.service,second.service,fresh.service,shutdown", "cancellation prevented lifecycle cleanup");
  await peer.close();
}

async function closeUnscopedExecution(): Promise<void> {
  const started = deferred<void>();
  const peer = new Peer({
    initialize: definition,
    async invokeService(_params, _host, execution) {
      started.resolve();
      await new Promise<void>((resolve) => {
        if (execution.signal.aborted) resolve();
        else execution.signal.addEventListener("abort", () => resolve(), { once: true });
      });
      return { cancelled: true };
    },
  });
  await peer.start();
  const response = peer.request("service.invoke", { service: "local.service", method: "call", caller: "test" });
  await started.promise;
  await peer.close();
  assert((await response).result?.cancelled, "EOF left an unscoped handler running");
}

// The watchdog only fails a hung regression; all successful ordering is driven
// by protocol frames and handler gates, with no elapsed-time assertions.
const watchdog = setTimeout(() => { throw new Error("JSONL composition deadlocked"); }, 10000);
try {
  await reentrantServices();
  await sharedExecutionCancellation();
  await lifecycleBarriers();
  await transportClose(false);
  await transportClose(true);
  await serialDispatchAndFailure();
  await closeUnscopedExecution();
} finally {
  clearTimeout(watchdog);
}
