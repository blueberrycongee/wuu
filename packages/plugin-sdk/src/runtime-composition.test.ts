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
      if (params.service === "a.leaf") return { text: "composed" };
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
    async invokeService(params, _host, execution) {
      const index = (params.params as { index: number }).index;
      signals[index] = execution.signal;
      started[index]!.resolve();
      await new Promise<void>((resolve) => {
        if (execution.signal.aborted) resolve();
        else execution.signal.addEventListener("abort", () => resolve(), { once: true });
      });
      return { cancelled: execution.signal.aborted };
    },
  } as RuntimePlugin);
  await peer.start();
  const first = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "shared", params: { index: 0 } });
  await started[0]!.promise;
  const second = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "shared", params: { index: 1 } });
  await started[1]!.promise;
  peer.send({ id: "cancel", method: "execution.cancel", params: { execution_id: "shared" } });
  const responses = await Promise.all([first, second]);
  assert(signals.every((signal) => signal.aborted), "cancellation did not reach every handler in the execution");
  assert(responses.every((response) => response.result?.cancelled), "cancelled handlers failed to settle");
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
  const peer = new Peer({
    concurrentServices: ["wait.service"],
    async initialize() {
      initializeStarted.resolve();
      await initializeRelease.promise;
      events.push("initialized");
      return definition();
    },
    async activate() {
      activateStarted.resolve();
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
    shutdown() { events.push("shutdown"); },
  } as RuntimePlugin);
  const initialize = peer.request("initialize", {});
  await initializeStarted.promise;
  const activate = peer.request("activate");
  const service = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test", execution_id: "barrier" });
  initializeRelease.resolve();
  await activateStarted.promise;
  assert(events.join(",") === "initialized", "service bypassed activation barrier");
  activateRelease.resolve();
  await serviceStarted.promise;
  const shutdown = peer.request("shutdown");
  const rejected = peer.request("service.invoke", { service: "wait.service", method: "call", caller: "test" });
  const rejection = await rejected;
  assert(rejection.error?.message === "runtime is shutting down", "shutdown admitted a new service call");
  serviceRelease.resolve();
  await Promise.all([initialize, activate, service, shutdown]);
  assert(events.join(",") === "initialized,activated,service started,service completed,shutdown", `lifecycle ordering changed: ${events}`);
  await peer.close();
}

async function transportClose(failed: boolean): Promise<void> {
  const started = deferred<void>();
  const events: string[] = [];
  const peer = new Peer({
    initialize: definition,
    async invokeService(_params, host, execution) {
      started.resolve();
      try { await host.call("host.service.call", { service: "remote.service", method: "call" }); }
      catch { events.push("pending rejected"); }
      assert(execution.signal.aborted, "EOF failed to cancel the handler");
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
  const failure = failed ? new Error("input stream failed") : undefined;
  let caught: unknown;
  try { await peer.close(failure); } catch (error) { caught = error; }
  assert(caught === failure, "transport did not preserve the input error");
  await response;
  assert(events.join(",") === "pending rejected,new rejected", "EOF left a host call pending");
}

async function serialDispatchAndFailure(): Promise<void> {
  const firstStarted = deferred<void>();
  const firstRelease = deferred<void>();
  const events: string[] = [];
  const peer = new Peer({
    concurrentServices: ["probe.service"],
    initialize: definition,
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
  const second = peer.request("service.invoke", { service: "second.service", method: "call", caller: "test" });
  // The probe response establishes that the preceding frame was received.
  await peer.request("service.invoke", { service: "probe.service", method: "call", caller: "test" });
  assert(events.join(",") === "first.service", "default service dispatch stopped being serial");
  firstRelease.resolve();
  assert((await first).error?.message === "provider failed", "service error was not returned");
  assert((await second).result?.recovered, "one provider failure poisoned later requests");
  assert(events.join(",") === "first.service,second.service", "serial request order changed");
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
