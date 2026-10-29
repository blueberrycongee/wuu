import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import test from "node:test";
import {
  runExecutionBackendV4, type ExecutionBackendV4, type ExecutionBackendContextV4,
  type ExecutionProcessV4,
} from "./execution-backend.js";

// Protocol failure cases are specified before the adapter: wrong version/token,
// changed session binding, duplicate live IDs, cancellation before physical
// completion, late callbacks, oversized frames and EOF with accepted resources.
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}
function fixture(backend: ExecutionBackendV4) {
  const input = new PassThrough(), output = new PassThrough();
  const reader = createInterface({ input: output });
  const lines = reader[Symbol.asyncIterator]();
  const running = runExecutionBackendV4(backend, { input, output });
  const token = "a".repeat(64);
  return {
    input, running, token,
    send(id: string, method: string, data?: unknown, selectedToken = token) {
      input.write(JSON.stringify({ id, method, data, token: selectedToken }) + "\n");
    },
    async next() {
      const next = await lines.next();
      assert.equal(next.done, false);
      return JSON.parse(next.value!);
    },
    async close() { input.end(); try { await running; } finally { reader.close(); output.end(); } },
  };
}
const binding = { version: 4, root: "/workspace", session: "thread-1", identity: "environment-1" };
const processRecord: ExecutionProcessV4 = {
  id: "process-1", owner_kind: "main_agent", owner_id: "thread-1", root_thread_id: "thread-1",
  lifecycle: "managed", completion_mode: "detached", status: "running", pid: 1, pgid: 1,
  log_path: "backend-owned", command: "build", cwd: "/workspace", started_at: "2026-10-28T00:00:00Z", updated_at: "2026-10-28T00:00:00Z",
};

test("execution backend pins authenticated initialization and does not invent capabilities", { timeout: 5000 }, async () => {
  let initialized = 0;
  const io = fixture({ initialize: async (input, context) => {
    initialized++;
    assert.equal(context.binding.session, input.session);
    assert.ok(Object.isFrozen(input));
  }, methods: { "process/list": () => [] } });
  try {
    io.send("early", "process/list", null);
    assert.match((await io.next()).error, /authentication/);
    io.send("hello", "initialize", binding);
    assert.deepEqual((await io.next()).data, { version: 4, root: "/workspace", capabilities: [] });
    io.send("bad-token", "process/list", null, "b".repeat(64));
    assert.match((await io.next()).error, /authentication/);
    io.send("other-session", "initialize", { ...binding, session: "thread-2" });
    assert.match((await io.next()).error, /identity|binding/);
    io.send("list", "process/list", null);
    assert.deepEqual((await io.next()).data, []);
    io.send("unsupported", "execute", { actor: "thread-1", permission_mode: "standard", call: { name: "read_file", arguments: "{}" } });
    assert.match((await io.next()).error, /unsupported/);
    assert.equal(initialized, 1);
  } finally { await io.close(); }
});

test("cancellation drains its addressed call while independent process controls remain available", { timeout: 5000 }, async () => {
  const entered = deferred(), cleanup = deferred();
  let calls = 0, closed = false;
  const io = fixture({ initialize: () => {}, methods: {
    "process/get": async (input, context) => {
      calls++;
      if (input.id !== "slow") return processRecord;
      entered.resolve();
      await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
      await cleanup.promise;
      context.signal.throwIfAborted();
      return processRecord;
    },
  }, close: () => { closed = true; } });
  try {
    io.send("hello", "initialize", binding); await io.next();
    io.send("slow-call", "process/get", { id: "slow" }); await entered.promise;
    io.send("slow-call", "process/get", { id: "duplicate" });
    assert.match((await io.next()).error, /duplicate/);
    io.send("slow-call", "cancel");
    io.send("fast-call", "process/get", { id: "fast" });
    assert.equal((await io.next()).id, "fast-call", "cancel must not acknowledge before the original handler drains");
    assert.equal(calls, 2);
    cleanup.resolve();
    assert.equal((await io.next()).id, "slow-call");
  } finally { cleanup.resolve(); await io.close(); }
  assert.equal(closed, true);
});

test("cancelled initialization cannot expose a half-initialized or substituted binding", { timeout: 5000 }, async () => {
  const entered = deferred(), release = deferred();
  let calls = 0;
  const io = fixture({ initialize: async () => { entered.resolve(); await release.promise; }, methods: { "process/list": () => { calls++; return []; } } });
  try {
    io.send("hello", "initialize", binding); await entered.promise;
    io.send("hello", "cancel");
    io.send("during", "process/list", null);
    assert.match((await io.next()).error, /initializ/);
    release.resolve();
    assert.ok((await io.next()).error);
    io.send("after", "process/list", null);
    assert.match((await io.next()).error, /initializ/);
    assert.equal(calls, 0);
  } finally { release.resolve(); await io.close(); }
});

test("code callbacks are parent-bound and accepted process lifetime ends at transport close", { timeout: 5000 }, async () => {
  let codeContext: ExecutionBackendContextV4 | undefined;
  let processLifetime: AbortSignal | undefined;
  let closeSawAborted = false;
  const io = fixture({ capabilities: ["process.start.v1"], initialize: () => {}, methods: {
    "process/start": (input, context) => {
      processLifetime = context.lifetimeSignal;
      return { ...processRecord, operation_id: input.operation_id };
    },
    "process/list": () => [],
    run_code: async (_input, context) => {
      codeContext = context;
      const value = await context.invokeTool({ name: "example.lookup", arguments: "{}" });
      return { Logs: [], Value: value, Error: "", Media: [] };
    },
  }, close: () => { closeSawAborted = processLifetime?.aborted === true; } });
  try {
    io.send("hello", "initialize", binding);
    assert.deepEqual((await io.next()).data.capabilities, ["process.start.v1"]);
    io.send("start", "process/start", { command: "build", actor: "thread-1", permission_mode: "standard", operation_id: "start-1" });
    assert.equal((await io.next()).data.operation_id, "start-1");
    io.send("start", "cancel");
    assert.equal(processLifetime?.aborted, false);
    io.send("code", "run_code", { actor: "thread-1", permission_mode: "standard", timeout_ms: 0, program: { code: "", tools: [] } });
    const callback = await io.next();
    assert.equal(callback.method, "tool"); assert.equal(callback.parent_id, "code");
    io.send(callback.id, "callback", { id: callback.id, data: { content: [{ type: "text", text: "answer" }] } });
    assert.equal((await io.next()).id, "code");
    await assert.rejects(codeContext!.invokeTool({ name: "late", arguments: "{}" }), /closed|ended|abort/i);
  } finally { await io.close(); }
  assert.equal(closeSawAborted, true);
});

test("invalid protocol or process capability fails before provider effects", { timeout: 5000 }, async () => {
  let initialized = false;
  const io = fixture({ initialize: () => { initialized = true; }, methods: {} });
  try {
    io.send("hello", "initialize", { ...binding, version: 3 });
    assert.match((await io.next()).error, /version|protocol/);
    assert.equal(initialized, false);
  } finally { await io.close(); }
  await assert.rejects(runExecutionBackendV4({ capabilities: ["process.start.v1"], initialize: () => {}, methods: {} }, {
    input: { async *[Symbol.asyncIterator]() {} }, output: { write() {} },
  }), /process\/start/);
});

for (const failingOutput of ["reply", "event"] as const) {
  test(`${failingOutput} output failure closes the backend without waiting for input EOF`, { timeout: 5000 }, async () => {
    const input = new PassThrough(), hello = deferred();
    let context: ExecutionBackendContextV4 | undefined, closed = false, writes = 0;
    const broken = new Error("broken execution output");
    const running = runExecutionBackendV4({
      initialize: (_binding, ctx) => { context = ctx; }, methods: {},
      close: () => { assert.equal(context?.lifetimeSignal.aborted, true); closed = true; },
    }, { input, output: { write() {
      writes++;
      if (failingOutput === "reply" || writes > 1) throw broken;
      hello.resolve();
    } } });
    const rejected = assert.rejects(running, error => error === broken);
    input.write(JSON.stringify({ id: "hello", method: "initialize", token: "a".repeat(64), data: binding }) + "\n");
    if (failingOutput === "event") {
      await hello.promise;
      await assert.rejects(context!.emitProcess({ Type: "started", Cause: "", Process: processRecord }), error => error === broken);
    }
    await rejected;
    assert.equal(closed, true);
    assert.equal(input.destroyed, true);
  });
}

test("stateless Code Mode capability is explicit and forwards the enforcement flag", { timeout: 5000 }, async () => {
  await assert.rejects(runExecutionBackendV4({ capabilities: ["code.stateless.v1"], initialize: () => {}, methods: {} }, {}), /requires run_code/);
  let flag: boolean | undefined;
  const io = fixture({ capabilities: ["code.stateless.v1"], initialize: () => {}, methods: {
    run_code(input) { flag = input.disable_state; return { Logs: [], Value: null, Error: "", Media: [] }; },
  } });
  try {
    io.send("hello", "initialize", binding);
    assert.deepEqual((await io.next()).data.capabilities, ["code.stateless.v1"]);
    io.send("code", "run_code", { actor: "thread-1", permission_mode: "standard", timeout_ms: 0, disable_state: true, program: { code: "", tools: [] } });
    assert.equal((await io.next()).id, "code");
    assert.equal(flag, true);
  } finally { await io.close(); }
});
