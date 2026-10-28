import assert from "node:assert/strict";
import test from "node:test";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { watch } from "node:fs";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";
import { delegateExecutionBackendV4, type ExecutionBackendContextV4 } from "./execution-backend.js";

// Real peer processes protect the delegation contract: explicit peer selection,
// one pinned world, actual cancellation/cleanup, reverse callbacks, events,
// unsupported capabilities and missing peers without replay or local fallback.
const sdk = new URL("./execution-backend.js", import.meta.url).href;
const binding = Object.freeze({ version: 4 as const, root: "/workspace", session: "delegate-thread", identity: "delegate-world" });
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}
const program = `
import {runExecutionBackendV4} from ${JSON.stringify(sdk)};
let calls = 0;
await runExecutionBackendV4({
  initialize(binding) { if(binding.session !== 'delegate-thread') throw new Error('wrong session'); },
  methods: {
    async execute(input, context) {
      calls++;
      if(input.call.arguments === 'wait') {
        await context.emitProcess({Type:'updated',Cause:'',Process:{id:'waiting',root_thread_id:context.binding.session}});
        await new Promise(resolve => context.signal.addEventListener('abort', resolve, {once:true}));
        throw new Error('peer cancelled original call');
      }
      return {structured_content:{calls, root:context.binding.root, session:context.binding.session, argument:input.call.arguments}};
    },
    async run_code(input, context) {
      const result = await context.invokeTool({name:'outside.lookup',arguments:input.program.code});
      return {Logs:[],Value:result,Error:'',Media:[]};
    },
    'process/list'() { return []; },
  },
  close() {},
});
`;

function context(signal = new AbortController().signal, onEvent = (_event: unknown) => {}): ExecutionBackendContextV4 {
  return {
    requestId: "outer-admitted-request", binding, signal, lifetimeSignal: new AbortController().signal,
    async emitProcess(event) { onEvent(event); },
    async invokeTool(call) { return { structured_content: { through: "outer-admitted-request", call } }; },
  };
}

test("explicit backend delegation preserves world, callbacks and addressed cancellation", { timeout: 10000 }, async () => {
  const entered = deferred();
  const events: unknown[] = [];
  const peer = delegateExecutionBackendV4({ command: [process.execPath, "--input-type=module", "-e", program] });
  const initialization = context(undefined, event => { events.push(event); entered.resolve(); });
  try {
    await peer.initialize(binding, initialization);
    const result = await peer.methods.execute!({ actor: "delegate-thread", permission_mode: "unconfined", call: { name: "read_file", arguments: "original" } }, context());
    assert.deepEqual(result.structured_content, { calls: 1, root: binding.root, session: binding.session, argument: "original" });
    await assert.rejects(Promise.resolve(peer.methods.execute!({ actor: "delegate-thread", permission_mode: "unconfined", call: { name: "read_file", arguments: "wrong-world" } }, {
      ...context(), binding: { ...binding, root: "/another-world" },
    })), /binding|identity/);
    const code = await peer.methods.run_code!({ actor: "delegate-thread", permission_mode: "unconfined", timeout_ms: 0, program: { code: "query", tools: [] } }, context());
    assert.deepEqual(code.Value, { structured_content: { through: "outer-admitted-request", call: { name: "outside.lookup", arguments: "query" } } });
    const cancellation = new AbortController();
    const waiting = peer.methods.execute!({ actor: "delegate-thread", permission_mode: "unconfined", call: { name: "read_file", arguments: "wait" } }, context(cancellation.signal));
    await entered.promise;
    cancellation.abort();
    await assert.rejects(Promise.resolve(waiting), /peer cancelled original call/);
    assert.equal(events.length, 1);
    await assert.rejects(Promise.resolve(peer.initialize({ ...binding, session: "another" }, initialization)), /binding|identity/);
  } finally { await peer.close!(); }
  await assert.rejects(Promise.resolve(peer.methods["process/list"]!(null, context())), /closed/);
});

test("terminal attached-peer failure closes the physical peer without waiting for an outer EOF", { timeout: 5000 }, async () => {
  const input = new PassThrough();
  let closed = false;
  const peer = delegateExecutionBackendV4({ transport: {
    input,
    output: { write(line) {
      const request = JSON.parse(line);
      if (request.method === "initialize") input.write(JSON.stringify({ id: request.id, data: { version: 4, root: binding.root, capabilities: [] } }) + "\n");
      else input.write("{malformed\n");
    } },
    async close() { closed = true; input.end(); },
  } });
  try {
    await peer.initialize(binding, context());
    await assert.rejects(Promise.resolve(peer.methods["process/list"]!(null, context())));
    assert.equal(closed, true, "terminal failure must close the chosen peer immediately, not wait for the caller's EOF");
  } finally { await peer.close!(); }
});

test("missing explicit peers and missing advertised capabilities never select a substitute", { timeout: 10000 }, async () => {
  const missing = delegateExecutionBackendV4({ command: ["/does-not-exist-wuu-execution-backend"] });
  await assert.rejects(Promise.resolve(missing.initialize(binding, context())), /ENOENT|spawn|unavailable/);
  await assert.rejects(Promise.resolve(missing.initialize(binding, context())), /ENOENT|spawn|unavailable|closed/);
  await Promise.resolve(missing.close!()).catch(() => {});
  const incapable = delegateExecutionBackendV4({ command: [process.execPath, "--input-type=module", "-e", program], capabilities: ["process.start.v1"] });
  try { await assert.rejects(Promise.resolve(incapable.initialize(binding, context())), /capability.*process.start.v1|process.start.v1.*capability/); }
  finally { await incapable.close!(); }
});

test("a real broken stdout pipe drains backend cleanup while stdin remains open", { timeout: 10000 }, async () => {
  const directory = await mkdtemp(join(tmpdir(), "wuu-execution-pipe-"));
  const marker = join(directory, "closed");
  const child = spawn(process.execPath, ["--input-type=module", "-e", `
    import {runExecutionBackendV4} from ${JSON.stringify(sdk)};
    import {writeFile} from 'node:fs/promises';
    try { await runExecutionBackendV4({initialize(){},methods:{},close(){return writeFile(${JSON.stringify(marker)},'closed')}}); }
    catch(error) { process.stderr.write('handled:'+error.message+'\\n'); }
  `], { stdio: ["pipe", "pipe", "pipe"] });
  let diagnostics = "";
  child.stderr.on("data", data => { diagnostics += data; });
  const exited = once(child, "close");
  try {
    const pipeClosed = once(child.stdout, "close");
    child.stdout.destroy(); await pipeClosed;
    child.stdin.write(JSON.stringify({ id: "hello", method: "initialize", token: "a".repeat(64), data: binding }) + "\n");
    const [code] = await exited;
    assert.equal(code, 0, diagnostics);
    assert.equal(await readFile(marker, "utf8"), "closed");
    assert.match(diagnostics, /handled:/);
  } finally { child.kill(); child.stdin.destroy(); await exited; await rm(directory, { recursive: true, force: true }); }
});


test("spawned peers receive enough default grace to finish cleanup beyond three seconds", { timeout: 15000 }, async () => {
  const directory = await mkdtemp(join(tmpdir(), "wuu-execution-cleanup-"));
  const marker = join(directory, "closed");
  const watcher = watch(directory);
  const entered = new Promise<void>(resolve => watcher.on("change", (_event, name) => {
    if (name === "closing") resolve();
  }));
  const peer = delegateExecutionBackendV4({ command: [process.execPath, "--input-type=module", "-e", `
    import {runExecutionBackendV4} from ${JSON.stringify(sdk)};
    import {watch} from 'node:fs';
    import {writeFile} from 'node:fs/promises';
    await runExecutionBackendV4({initialize(){},methods:{},async close(){
      const gate = watch(${JSON.stringify(directory)});
      const released = new Promise(resolve => gate.on('change', (_event, name) => {
        if(name === 'release') { gate.close(); resolve(); }
      }));
      await writeFile(${JSON.stringify(join(directory, "closing"))}, 'closing');
      await released;
      await writeFile(${JSON.stringify(marker)}, 'closed');
    }});
  `] });
  try {
    await peer.initialize(binding, context());
    const closing = Promise.resolve(peer.close!());
    void closing.catch(() => {});
    await entered;
    // Hold real cleanup past the old three-second kill deadline, then release
    // it explicitly rather than making the peer guess when the test is ready.
    await delay(3200);
    await writeFile(join(directory, "release"), "release");
    await closing;
    assert.equal(await readFile(marker, "utf8"), "closed");
  } finally {
    watcher.close();
    await Promise.resolve(peer.close!()).catch(() => {});
    await rm(directory, { recursive: true, force: true });
  }
});

test("an explicit shutdown budget terminates a stuck peer without claiming its resources stopped", { timeout: 5000 }, async () => {
  const peer = delegateExecutionBackendV4({
    command: [process.execPath, "--input-type=module", "-e", `
      import {runExecutionBackendV4} from ${JSON.stringify(sdk)};
      await runExecutionBackendV4({initialize(){},methods:{},close(){
        return new Promise(() => setInterval(() => {}, 1000));
      }});
    `],
    shutdown_grace_ms: 100,
  });
  try {
    await peer.initialize(binding, context());
    await assert.rejects(Promise.resolve(peer.close!()), /timed out; resource outcome may be unknown/);
    await assert.rejects(Promise.resolve(peer.close!()), /timed out; resource outcome may be unknown/);
  } finally { await Promise.resolve(peer.close!()).catch(() => {}); }
});

test("shutdown budgets reject invalid timer ranges and cannot configure an attached transport", () => {
  for (const shutdown_grace_ms of [0, -1, 0.5, NaN, Infinity, 2_147_483_648, null, "100"]) {
    // @ts-expect-error JavaScript callers must also supply an integer budget.
    assert.throws(() => delegateExecutionBackendV4({ command: [process.execPath], shutdown_grace_ms }), /shutdown_grace_ms/);
  }
  for (const shutdown_grace_ms of [1, 90_000, 2_147_483_647]) {
    assert.doesNotThrow(() => delegateExecutionBackendV4({ command: [process.execPath], shutdown_grace_ms }));
  }
  const input = new PassThrough();
  try {
    // @ts-expect-error Attached transports own their shutdown budget.
    assert.throws(() => delegateExecutionBackendV4({
      transport: { input, output: { write() {} }, async close() {} },
      shutdown_grace_ms: 100,
    }), /shutdown_grace_ms.*command/);
  } finally { input.destroy(); }
});
