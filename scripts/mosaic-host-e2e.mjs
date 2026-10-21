#!/usr/bin/env node
// Failure-first stages 1–4, with bounded scheduler/unconnected Calendar checks:
// project/global context leaks, duplicate identities and
// sends, lost main bindings, domain spoofing, notebook regression, task dispatch
// races, misleading completion, and cancellation/restart recovery. The real Go
// app-server and shipped plugins run against one synthetic loopback provider.
// Build the current core and bundles first; WUU_E2E_CORE selects that binary.
// Exercises default logical-memory activation without an isolation verifier.
// The SDK transport fixture only multiplexes clients; the provider is synthetic.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { EventEmitter, once } from "node:events";
import { lstat, mkdir, mkdtemp, readFile, readdir, readlink, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const binary = resolve(root, process.env.WUU_E2E_CORE || "bin/wuu");
const output = resolve(root, process.env.WUU_MOSAIC_E2E_OUTPUT || ".tmp/e2e/mosaic-host");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(output, "run-"));
const home = join(scratch, "state"), fixtureHome = join(scratch, "home");
const personalRoot = join(home, "user-spaces", "local", "personal", "files");
const personalState = join(home, "user-spaces", "local", "personal", "state");
const invocation = join(scratch, "unrelated-project"), otherProject = join(scratch, "other-project");
const codingMarker = "MOSAIC_SYNTHETIC_CODING_NOTEBOOK_9f071";
const legacyKey = "fixture.legacy.personal", legacyID = "mem_" + createHash("sha256").update(legacyKey).digest("hex");
const legacyForgottenID = "mem_" + createHash("sha256").update("fixture.legacy.forgotten").digest("hex");
const legacyStoragePath = join(personalState, "plugin-storage", "user", `sha256-${createHash("sha256").update("plugin:bundled:memory").digest("hex")}.json`);
let legacyRecord;
const projectMarker = "MOSAIC_SYNTHETIC_PROJECT_AGENTS_f52ad";
const globalMarker = "MOSAIC_SYNTHETIC_GLOBAL_AGENTS_7d810";
const projectMemoryMarker = "MOSAIC_SYNTHETIC_PROJECT_MEMORY_25fc1";
const testBinary = join(scratch, "synthetic-sdk-host");
const privateMarkers = [codingMarker, projectMarker, globalMarker, projectMemoryMarker];
const report = { started_at: new Date().toISOString(), binary, scratch, status: "running", cases: [],
  scope: "Production CLI and ordinary SDK logical-memory activation plus Coding compatibility. Synthetic state/provider and multiplexed SDK transport only; no OS isolation, live inference, calendar, device or rendered UI claims",
  synthetic_provider: true, host_isolation_override: false,
  failure_matrix: [
    "Stage 1: no registered project; host scope survives invocation-directory changes, restart and duplicate creation; Coding context must not enter Personal",
    "Stage 2: one main binding, idempotent send, durable readable history, stale fingerprint denied and plugin disable isolated",
    "Built-in memory: disabled optional Memory must not disable Personal records; both plugins must expose one Personal tool set; Coding notebook remains optional",
    "Stage 3: Coding notebook retained; Personal query has no imported Coding facts; caller-supplied domain rejected; generic read cannot reveal Coding memory",
    "Stage 4: background dispatch deduplicates without blocking main chat; no ordinary thread leakage; unknown results are not completed; stop and restart retain task state",
    "Single assistant: waiting_user question must persist in main exactly once; a main resume_task answer must retain the task ID and advance its requirement revision",
    "Continuous conversation: stop_reply must interrupt only the current reply and retain every accepted queued user input and independent background task",
    "Routine changes: conversational manage_routine update must change the existing schedule and binding revision without creating another routine",
    "Stage 6: actual local scheduler admits one reminder, pause cancels its turn and survives restart; no wall-clock recurrence or DST completeness claim",
    "Stage 7: unconnected read-only Calendar reports empty freshness and remains unavailable to Coding; no account authorization or real provider claim",
  ],
};
const providerEvents = new EventEmitter(), providerCalls = [], active = new Set(), held = new Map();
const heldTags = new Set();
const toolPlans = new Map(), planCalls = new Map();
let personal, coding, main, memoryRecord, task, codingThread, duplicateTurnID, attachmentSubmission, firstReplySeq;

function waitFor(events, event, history, predicate, label, timeout = 30_000) {
  const previous = history.find(predicate);
  if (previous !== undefined) return Promise.resolve(previous);
  return new Promise((resolve, reject) => {
    const listener = value => {
      if (!predicate(value)) return;
      clearTimeout(timer); events.off(event, listener); resolve(value);
    };
    const timer = setTimeout(() => {
      events.off(event, listener); reject(new Error(`Timed out: ${label}`));
    }, timeout);
    events.on(event, listener);
  });
}
async function scenario(name, operation, continueOnFailure = false) {
  const item = { name, status: "running" }; report.cases.push(item);
  try { item.result = await operation(); item.status = "passed"; console.log(`PASS ${name}`); }
  catch (error) { item.status = "failed"; item.error = error.stack; if (!continueOnFailure) throw error; console.error(`FAIL ${name}: ${error.message}`); }
  finally { await save(); }
}
async function save() {
  await writeFile(join(scratch, "report.json"), JSON.stringify(report, null, 2));
  await writeFile(join(output, "latest.json"), JSON.stringify({ scratch, report: join(scratch, "report.json"), status: report.status }, null, 2));
  await writeFile(join(scratch, "provider-requests.json"), JSON.stringify(providerCalls, null, 2));
}
async function sourceSnapshot(label) {
  const listed = spawnSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], { cwd: root, encoding: "utf8" });
  assert.equal(listed.status, 0, listed.stderr);
  const manifest = [];
  for (const path of [...new Set(listed.stdout.split("\0").filter(Boolean))].sort()) {
    try {
      const info = await lstat(join(root, path));
      const bytes = info.isSymbolicLink() ? Buffer.from(await readlink(join(root, path))) : await readFile(join(root, path));
      manifest.push({ path, mode: info.mode & 0o777, kind: info.isSymbolicLink() ? "symlink" : "file", sha256: createHash("sha256").update(bytes).digest("hex") });
    } catch (error) { if (error.code !== "ENOENT") throw error; manifest.push({ path, kind: "deleted" }); }
  }
  await writeFile(join(scratch, `source-manifest-${label}.json`), JSON.stringify(manifest, null, 2));
  return { files: manifest.length, sha256: createHash("sha256").update(JSON.stringify(manifest)).digest("hex") };
}
function messageText(message) {
  if (typeof message?.content === "string") return message.content;
  if (Array.isArray(message?.content)) return message.content.filter(part => part.type === "text" && typeof part.text === "string").map(part => part.text).join("\n");
  return JSON.stringify(message?.content ?? "");
}
function tagOf(body) {
  const users = body.messages?.filter(message => message.role === "user") ?? [];
  for (const message of users.reverse()) {
    // Providers may merge adjacent user records after an interrupted reply.
    // Choose the newest explicit fixture-input line, not an older merged tag
    // or a question quoted inside the identity's open-matters JSON.
    const tag = [...messageText(message).matchAll(/(?:^|\n)\s*(?:Goal:\s*|Question:\s*|The user's explicit reminder is due:\s*)?MOSAIC_E2E_CASE:([a-z0-9-]+)/g)].at(-1)?.[1];
    if (tag) return tag;
    if (messageText(message).includes("selected.txt")) return "attachment-only";
  }
  return "unmarked";
}
const agentCalls = tag => providerCalls.filter(call => call.tag === tag && call.kind === "agent");
function answer(response, body, message, finishReason = "stop") {
  const usage = { prompt_tokens: 17, completion_tokens: 7, total_tokens: 24 };
  if (body.stream) {
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    const delta = message.tool_calls ? { role: "assistant", tool_calls: message.tool_calls.map((call, index) => ({ index, ...call })) } : { role: "assistant", content: message.content };
    response.end(`data: ${JSON.stringify({ id: "mosaic-fixture", object: "chat.completion.chunk", model: body.model, choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ id: "mosaic-fixture", object: "chat.completion.chunk", model: body.model, choices: [{ index: 0, delta: {}, finish_reason: finishReason }], usage })}\n\ndata: [DONE]\n\n`);
  } else {
    response.writeHead(200, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ id: "mosaic-fixture", object: "chat.completion", model: body.model,
      choices: [{ index: 0, message: { role: "assistant", ...message }, finish_reason: finishReason }], usage }));
  }
}
const provider = createServer(async (request, response) => {
  try {
    let raw = ""; for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw), tag = tagOf(body), isAgent = Boolean(body.tools?.length);
    const call = { tag, kind: isAgent ? "agent" : "auxiliary", url: request.url, body, at: new Date().toISOString() };
    response.on("close", () => {
      if (response.writableEnded) return;
      call.cancelled = true; providerEvents.emit("cancelled", call);
    });
    providerCalls.push(call); providerEvents.emit("request", call);
    const configured = isAgent ? toolPlans.get(tag) : undefined, count = planCalls.get(tag) ?? 0;
    const plan = Array.isArray(configured) ? configured[count] : count === 0 ? configured : undefined;
    if (isAgent) planCalls.set(tag, count + 1);
    if (plan) {
      // Public plugin tools retain 19 characters of each identifier before a
      // collision-resistant suffix; select the actual advertised name.
      const prefix = plan.plugin ? `plugin_${plan.plugin.slice(0, 19)}_${plan.tool.slice(0, 19)}_` : plan.tool;
      const tool = body.tools?.find(item => plan.plugin ? item.function?.name?.startsWith(prefix) : item.function?.name === prefix);
      if (!tool) throw new Error(`Provider fixture cannot find ${plan.tool} in ${body.tools?.map(item => item.function?.name)}`);
      call.selected_tool = tool.function.name;
      const args = typeof plan.arguments === "function" ? await plan.arguments(body) : plan.arguments;
      answer(response, body, { tool_calls: [{ id: `mosaic-${tag}-${count}`, type: "function", function: { name: tool.function.name, arguments: JSON.stringify(args) } }] }, "tool_calls");
    } else if (isAgent && (tag.startsWith("background-") || heldTags.has(tag))) {
      held.set(tag, () => { if (!response.destroyed) answer(response, body, { content: `MOSAIC_REPLY:${tag}` }); });
      providerEvents.emit("held", { tag });
    } else answer(response, body, { content: `MOSAIC_REPLY:${tag}` });
  } catch (error) {
    report.provider_error = error.stack;
    if (!response.headersSent) response.writeHead(500, { "Content-Type": "application/json" });
    response.end(JSON.stringify({ error: { message: error.message } }));
  }
});

class Host {
  events = new EventEmitter(); transcript = []; requests = []; stderr = ""; sequence = 0;
  constructor(label, domain, cwd = invocation, owner) { this.label = label; this.domain = domain; this.cwd = cwd; this.owner = owner; }
  async start() {
    const env = { PATH: process.env.PATH, HOME: fixtureHome, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath };
    const args = ["app-server", "--workdir", this.cwd, "--config", join(home, "config.json")];
    if (this.domain) args.push("--user-space", "local", "--product-domain", this.domain);
    if (this.owner) {
      this.child = this.owner.child; this.connections = this.owner.connections;
    } else {
    this.connections = new Map();
    this.child = spawn(this.domain ? testBinary : binary, args, { cwd: this.cwd, env, stdio: ["pipe", "pipe", "pipe"] });
    this.child.stderr.setEncoding("utf8").on("data", value => { this.stderr += value; });
    this.child.on("error", error => { this.stderr += error.stack; this.events.emit("fatal", error); });
    createInterface({ input: this.child.stdout }).on("line", line => {
      try {
        const output = JSON.parse(line), target = this.domain ? this.connections.get(output.client) : this;
        if (!target) throw new Error(`Unknown fixture connection: ${line}`);
        if (output.action) { target.events.emit(output.action); return; }
        const frame = this.domain ? output.frame : output;
        target.transcript.push(frame); target.events.emit("message", frame);
      }
      catch { this.stderr += `\nnon-JSON stdout: ${line}`; }
    });
    }
    active.add(this); this.connections.set(this.label, this);
    if (this.domain) this.child.stdin.write(`${JSON.stringify({ client: this.label, action: "open" })}\n`);
    this.connected = true;
    this.initialized = await this.rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "mosaic-host-e2e" } });
    return this;
  }
  package(name) {
    const item = this.initialized.extension_inventory?.find(item => item.id === name || item.id.endsWith(`:${name}`));
    assert(item, `${this.label}: missing package ${name}; ${this.stderr}`); return item;
  }
  async raw(method, params = {}) {
    const request = { id: `${this.label}-${++this.sequence}`, method, params }; this.requests.push(request);
    assert.equal(this.child.exitCode, null, `${this.label} exited: ${this.stderr}`);
    this.child.stdin.write(`${JSON.stringify(this.domain ? { client: this.label, frame: request } : request)}\n`);
    return await waitFor(this.events, "message", this.transcript, frame => frame.id === request.id, `${this.label} ${method}; stderr=${this.stderr}`, 45_000);
  }
  async rpc(method, params) {
    const frame = await this.raw(method, params); assert(!frame.error, `${this.label} ${method}: ${JSON.stringify(frame.error)}`); return frame.result;
  }
  async client(method, input = {}, name = "personal", overrides = {}) {
    const item = this.package(name);
    return (await this.rpc("plugin/client/request", { id: item.id, fingerprint: item.fingerprint, method, input,
      request_id: `${this.label}-client-${this.sequence + 1}`,
      ...(method === "agents.send" ? { user_input: { request_id: input.request_id, prompt: input.prompt } } : {}), ...overrides })).result;
  }
  async enable(name) {
    const item = this.package(name);
    if (!item.enabled) {
      const control = this.domain ? coding : this, ownerItem = control.package(name);
      const changed = await control.rpc("extension/package/update", { id: ownerItem.id, fingerprint: ownerItem.fingerprint, action: "enable" });
      control.initialized.extension_inventory = changed.extension_inventory;
      if (this.domain) await this.awaitPackage(name, true);
    }
    const active = this.package(name);
    assert.equal(active.runtime_state, "active", `${name}: ${active.last_error ?? JSON.stringify(active)}`);
    return active;
  }
  async awaitPackage(name, enabled) {
    const deadline = Date.now() + 10_000;
    do {
      this.initialized = await this.rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "mosaic-host-e2e" } });
      const item = this.package(name);
      if (item.enabled === enabled && (!enabled || item.runtime_state === "active")) return item;
      await new Promise(resolve => setTimeout(resolve, 100));
    } while (Date.now() < deadline);
    throw new Error(`Owner config change did not reload ${name}: ${JSON.stringify(this.package(name))}`);
  }
  async settled(sent) {
    const turnID = sent.turn_id ?? sent.turn?.id, sessionID = sent.session_id ?? sent.thread_id;
    assert(turnID, `send did not return a turn: ${JSON.stringify(sent)}`);
    const frame = await waitFor(this.events, "message", this.transcript, value =>
      ["turn/completed", "turn/error"].includes(value.method) &&
      (value.params?.turn?.id === turnID || value.params?.turn_id === turnID) && (!sessionID || value.params?.thread_id === sessionID),
    `${this.label} turn ${turnID}`, 45_000);
    assert.notEqual(frame.method, "turn/error", JSON.stringify(frame.params)); return frame;
  }
  async send(tag, requestID = tag) {
    const sent = await this.client("agents.send", { agent_id: main.agent_id, request_id: requestID, prompt: `MOSAIC_E2E_CASE:${tag}` });
    await this.settled(sent); return sent;
  }
  async close(force = false) {
    if (this.owner) { await this.disconnect(); active.delete(this); return; }
    if (this.child.exitCode === null && this.child.signalCode === null) {
      const exited = once(this.child, "exit");
      if (force) this.child.kill("SIGKILL"); else this.child.stdin.end();
      const timer = setTimeout(() => this.child.kill("SIGKILL"), 5000);
      try { await exited; } finally { clearTimeout(timer); }
    }
    active.delete(this); await this.persist();
  }
  async disconnect() {
    if (!this.connected) return;
    assert(this.domain, "Only the synthetic shared SDK fixture has detachable clients");
    if (this.child.exitCode !== null || this.child.signalCode !== null) {
      this.connected = false; await this.persist(); return;
    }
    const closed = once(this.events, "closed");
    this.child.stdin.write(`${JSON.stringify({ client: this.label, action: "close" })}\n`);
    await closed; this.connected = false; await this.persist();
  }
  async reconnect() {
    assert(this.domain && !this.connected);
    this.child.stdin.write(`${JSON.stringify({ client: this.label, action: "open" })}\n`); this.connected = true;
    this.initialized = await this.rpc("initialize", { protocol_version: "wuu-app-server/v0.1", client: { name: "mosaic-host-e2e-reconnected" } });
  }
  async persist() {
    await writeFile(join(scratch, `${this.label}-protocol.json`), JSON.stringify({ requests: this.requests, responses: this.transcript }, null, 2));
    await writeFile(join(scratch, `${this.label}-stderr.log`), this.stderr);
  }
}

async function codingBaseline() {
  coding = await new Host("coding", "").start();
  const initial = coding.package("memory");
  if (initial.enabled) {
    const changed = await coding.rpc("extension/package/update", { id: initial.id, fingerprint: initial.fingerprint, action: "disable" });
    coding.initialized.extension_inventory = changed.extension_inventory;
  }
  await assert.rejects(coding.client("memory.read", {}, "memory"), /inactive|disabled|unavailable|enabled/i);
  await coding.enable("memory");
  const notebook = await coding.client("memory.read", {}, "memory"); assert(JSON.stringify(notebook).includes(codingMarker));
  await assert.rejects(coding.client("agents.ensure_default"), /does not accept|unavailable|Personal/i);
  codingThread = (await coding.rpc("thread/start", { cwd: invocation, engine: "wuu" })).thread.id;
  const sent = await coding.rpc("turn/start", { thread_id: codingThread, prompt: "MOSAIC_E2E_CASE:coding-first" });
  await coding.settled({ thread_id: codingThread, turn_id: sent.turn.id });
  const calls = agentCalls("coding-first"); assert(calls.length);
  const names = calls.flatMap(call => call.body.tools?.map(tool => tool.function?.name) ?? []);
  assert(!names.some(name => /personal_memory|propose_specialist|create_task|complete_task/.test(name)), JSON.stringify(names));
  assert(JSON.stringify(calls).includes(projectMarker), "Coding positive control did not load its project AGENTS");
  const enabled = coding.package("memory");
  const changed = await coding.rpc("extension/package/update", { id: enabled.id, fingerprint: enabled.fingerprint, action: "disable" });
  coding.initialized.extension_inventory = changed.extension_inventory;
  await assert.rejects(coding.client("memory.read", {}, "memory"), /inactive|disabled|unavailable|enabled/i);
  return { thread_id: codingThread, notebook_retained: true, optional_disabled: true };
}

async function downloadAttachment(host, method, scope, record, attachment) {
  let encoded = "", chunks = 0;
  do {
    const chunk = await host.client(method, { ...scope, seq: record.seq, kind: attachment.kind, index: attachment.index, sha256: attachment.sha256, offset: encoded.length });
    assert.equal(chunk.offset, encoded.length); assert.equal(chunk.sha256, attachment.sha256);
    assert(chunk.data.length <= 128 * 1024); assert(chunk.total >= encoded.length + chunk.data.length);
    encoded += chunk.data; chunks++;
    if (encoded.length === chunk.total) break;
    assert(chunk.data.length, "attachment reader made no progress");
  } while (true);
  const bytes = Buffer.from(encoded, "base64");
  assert.equal(createHash("sha256").update(bytes).digest("hex"), attachment.sha256);
  assert.equal(bytes.length, attachment.size);
  return { bytes, chunks };
}

async function waitPersistedReply(sessionID, content, evidenceName) {
  const deadline = Date.now() + 10_000;
  do {
    const persisted = spawnSync(testBinary, ["read-persisted-history", join(personalState, "sessions"), sessionID], { encoding: "utf8" });
    assert.equal(persisted.status, 0, persisted.stderr);
    if (persisted.stdout.includes(content)) {
      await writeFile(join(scratch, evidenceName), persisted.stdout); return;
    }
    await new Promise(resolve => setTimeout(resolve, 50));
  } while (Date.now() < deadline);
  throw new Error("Work did not commit while every client was disconnected");
}

async function buildSDKHost() {
  const source = join(scratch, "synthetic-sdk-host.go");
  await writeFile(source, `// Acceptance transport fixture only. Uses production logical-memory policy without overrides.
package main
import("bufio";"context";"encoding/json";"flag";"fmt";"io";"os";"sync";"github.com/blueberrycongee/wuu/internal/sdk";"github.com/blueberrycongee/wuu/internal/runtime";"github.com/blueberrycongee/wuu/internal/session";"github.com/blueberrycongee/wuu/internal/pluginsettings")
type output struct{mu sync.Mutex}
func(o *output) emit(v any)error{o.mu.Lock();defer o.mu.Unlock();return json.NewEncoder(os.Stdout).Encode(v)}
type writer struct{o *output;client string}
func(w writer)Write(p []byte)(int,error){err:=w.o.emit(map[string]any{"client":w.client,"frame":json.RawMessage(p)});return len(p),err}
type connection struct{in *io.PipeWriter;done chan struct{}}
// The fixture envelope multiplexes independent, ordinary SDK transports. It
// adds no host API, authorization override or isolation verifier.
func multiplex(r *sdk.Runtime)error{
 out:=&output{};clients:=map[string]connection{}
 defer func(){for _,c:=range clients{c.in.Close()};for _,c:=range clients{<-c.done}}()
 scan:=bufio.NewScanner(os.Stdin);scan.Buffer(make([]byte,65536),32*1024*1024)
 for scan.Scan(){var e struct{Client string;Action string;Frame json.RawMessage};if err:=json.Unmarshal(scan.Bytes(),&e);err!=nil{return err}
 switch e.Action{
 case "open":if _,ok:=clients[e.Client];ok{return fmt.Errorf("duplicate fixture client")};reader,input:=io.Pipe();c:=connection{input,make(chan struct{})};clients[e.Client]=c
 go func(id string,c connection){defer close(c.done);defer reader.Close();if err:=r.Serve(context.Background(),reader,writer{out,id});err!=nil{fmt.Fprintln(os.Stderr,"fixture connection:",err)}}(e.Client,c)
 case "close":c,ok:=clients[e.Client];if !ok{return fmt.Errorf("unknown fixture client")};c.in.Close();<-c.done;delete(clients,e.Client);if err:=out.emit(map[string]any{"client":e.Client,"action":"closed"});err!=nil{return err}
 default:c,ok:=clients[e.Client];if !ok{return fmt.Errorf("unknown fixture client")};if _,err:=c.in.Write(append(e.Frame,'\\n'));err!=nil{return err}
 }};return scan.Err()
}
func run()error{
 if len(os.Args)==4&&os.Args[1]=="probe-storage-transfer"{doc,err:=pluginsettings.ReadState(os.Args[2],"","plugin:bundled:memory",pluginsettings.ScopeUser);if err!=nil{return err};_,visible:=doc.Values[os.Args[3]];_,err=pluginsettings.UpdateState(os.Args[2],"","plugin:bundled:memory",pluginsettings.ScopeUser,func(values map[string]string)error{values[os.Args[3]]="forbidden-old-owner-write";return nil});if err==nil{return fmt.Errorf("old owner write unexpectedly succeeded")};return json.NewEncoder(os.Stdout).Encode(map[string]any{"source_visible":visible,"write_denied":true,"error":err.Error()})}
 if len(os.Args)==4&&os.Args[1]=="read-persisted-history"{head,err:=session.ReadHistoryQuery(context.Background(),os.Args[2],session.HistoryReadQuery{SessionID:os.Args[3],StartSeq:1,Limit:1});if err!=nil{return err};page,err:=session.ReadHistoryQuery(context.Background(),os.Args[2],session.HistoryReadQuery{SessionID:os.Args[3],StartSeq:max(1,head.HeadSeq-49),Limit:50});if err!=nil{return err};return json.NewEncoder(os.Stdout).Encode(page)}
 if len(os.Args)==6&&os.Args[1]=="read-artifact"{data,_,err:=runtime.ReadManagedArtifact(os.Args[2],os.Args[3],os.Args[4],os.Args[5],1048576);if err!=nil{return err};_,err=os.Stdout.Write(data);return err}
 f:=flag.NewFlagSet("synthetic-sdk-host",flag.ContinueOnError)
 cwd:=f.String("workdir","","fixture workdir");cfg:=f.String("config","","fixture config")
 space:=f.String("user-space","","fixture user space");domain:=f.String("product-domain","","fixture domain")
 if len(os.Args)<2||os.Args[1]!="app-server"{return fmt.Errorf("only synthetic app-server is supported")}
 if err:=f.Parse(os.Args[2:]);err!=nil{return err}
 fmt.Fprintln(os.Stderr,"SDK TRANSPORT FIXTURE: production logical-memory policy; no OS isolation attestation")
 r,err:=sdk.New(sdk.Options{WorkDir:*cwd,HomeDir:os.Getenv("HOME"),ConfigPath:*cfg,UserSpaceID:*space,ProductDomain:*domain})
 if err!=nil{return err};defer r.Close();return multiplex(r)
}
func main(){if err:=run();err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}}
`);
  const built = spawnSync("go", ["build", "-buildvcs=false", "-ldflags", "-X github.com/blueberrycongee/wuu/internal/version.Version=v2026.10.19-dev", "-o", testBinary, source], { cwd: root, encoding: "utf8", timeout: 120_000 });
  await writeFile(join(scratch, "synthetic-host-build.log"), `${built.stdout ?? ""}${built.stderr ?? ""}`);
  assert.equal(built.status, 0, built.stderr || String(built.error));
  report.synthetic_host_sha256 = createHash("sha256").update(await readFile(testBinary)).digest("hex");
}

try {
  report.source_commit = spawnSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).stdout.trim();
  report.source_changes = spawnSync("git", ["status", "--short"], { cwd: root, encoding: "utf8" }).stdout.trim();
  report.source_tree_start = await sourceSnapshot("start");
  report.harness_sha256 = createHash("sha256").update(await readFile(fileURLToPath(import.meta.url))).digest("hex");
  report.command = { node: process.execPath, harness: fileURLToPath(import.meta.url), core: binary };
  report.core_sha256 = createHash("sha256").update(await readFile(binary)).digest("hex");
  report.runtime_sha256 = {};
  for (const plugin of await readdir(join(root, "internal/plugin/bundled"))) {
    try { report.runtime_sha256[plugin] = createHash("sha256").update(await readFile(join(root, `internal/plugin/bundled/${plugin}/runtime.mjs`))).digest("hex"); }
    catch (error) { if (error.code !== "ENOENT" && error.code !== "ENOTDIR") throw error; }
  }
  for (const path of [home, fixtureHome, invocation, otherProject, join(home, "memory"), join(fixtureHome, ".wuu"), join(invocation, ".wuu", "memory")]) await mkdir(path, { recursive: true });
  await writeFile(join(invocation, "AGENTS.md"), `# Coding instructions\n${projectMarker}\n`);
  await writeFile(join(otherProject, "AGENTS.md"), `# Another Coding project\n${projectMarker}\n`);
  await writeFile(join(fixtureHome, "AGENTS.md"), `${globalMarker}\n`);
  await writeFile(join(fixtureHome, ".wuu", "AGENTS.md"), `${globalMarker}\n`);
  await writeFile(join(home, "AGENTS.md"), `${globalMarker}\n`);
  await writeFile(join(home, "memory", "MEMORY.md"), `# Existing Coding notebook\n- ${codingMarker}\n`);
  await writeFile(join(invocation, ".wuu", "memory", "MEMORY.md"), `${projectMemoryMarker}\n`);
  await new Promise(resolve => provider.listen(0, "127.0.0.1", resolve));
  // The integrated single-conversation suite keeps every earlier turn. Leave
  // enough synthetic context for the full chain; compaction is tested elsewhere.
  await writeFile(join(home, "config.json"), JSON.stringify({
    default_provider: "fixture", providers: { fixture: { type: "openai-compatible", base_url: `http://127.0.0.1:${provider.address().port}`, api_key: "synthetic-unused", model: "mosaic-fixture", models: { "mosaic-fixture": { context_window: 131072 } } } },
    agent: { permission_mode: "standard", tool_loading: "flat", max_steps: 8 }, skills: { enabled: false }, ptc: { enabled: false },
  }));
  // Seed persisted v1 bytes, not a live Memory provider. The first production
  // Personal activation must adopt records even while the source is disabled.
  const legacyTime = new Date(Date.now() - 60000).toISOString();
  const legacySource = { kind: "message", ref: "synthetic-legacy-source", occurred_at: legacyTime };
  legacyRecord = { id: legacyID, key: legacyKey, revision: 4, product_domain: "personal", user_space_id: "local", status: "active",
    content: "Synthetic preserved Personal preference", evidence: "explicit", sources: [legacySource], source_at: legacyTime, effective_at: legacyTime,
    contributor_agent_id: "legacy-personal-main", updated_by_agent_id: "legacy-personal-main", created_at: legacyTime, updated_at: legacyTime,
    contributions: [{ revision: 4, agent_id: "legacy-personal-main", executor_session_id: "legacy-session", operation: "correct", source_at: legacyTime, effective_at: legacyTime, written_at: legacyTime, sources: [legacySource] }], superseded_sources: [] };
  const forgotten = { id: legacyForgottenID, revision: 2, product_domain: "personal", user_space_id: "local", status: "forgotten", updated_at: legacyTime,
    blocked_sources: [createHash("sha256").update(JSON.stringify(["message", "synthetic-legacy-forgotten-source"])).digest("hex")] };
  await mkdir(join(personalState, "plugin-storage", "user"), { recursive: true });
  await writeFile(legacyStoragePath, JSON.stringify({ schema_version: 1, plugin_id: "plugin:bundled:memory", values: {
    [`personal.records.v1.${legacyID}`]: JSON.stringify(legacyRecord), [`personal.records.v1.${legacyForgottenID}`]: JSON.stringify(forgotten),
  } }), { mode: 0o600 });
  await scenario("Production CLI activates logical-memory Personal without an isolation override", async () => {
    const started = spawnSync(binary, ["app-server", "--workdir", invocation, "--config", join(home, "config.json"), "--user-space", "local", "--product-domain", "personal"],
      { cwd: invocation, env: { PATH: process.env.PATH, HOME: fixtureHome, WUU_HOME: home, WUU_NODE_EXECUTABLE: process.execPath }, encoding: "utf8", timeout: 30_000,
        input: JSON.stringify({ id: "activation", method: "initialize", params: { protocol_version: "wuu-app-server/v0.1", client: { name: "logical-activation-e2e" } } }) + "\n" });
    assert.equal(started.status, 0, started.stderr);
    const initialized = started.stdout.trim().split("\n").map(line => JSON.parse(line)).find(frame => frame.id === "activation");
    assert(initialized?.result, started.stdout + started.stderr);
    assert.equal(initialized.result.extension_inventory.find(item => item.id.endsWith(":memory") || item.id === "memory").enabled, false, "legacy migration must not require enabling the source");
    assert.deepEqual(initialized.result.user_host_isolation, { memory: "logical", host: "unverified" });
    assert.equal(initialized.result.user_space_id, "local"); assert.equal(initialized.result.product_domain, "personal");
    return { exit_code: started.status, capabilities: initialized.result.user_host_isolation };
  });
  await scenario("Coding keeps its notebook and has no Personal client or model tools", codingBaseline);
  await buildSDKHost();
  await scenario("Personal initializes without registered projects and advertises its plugin", async () => {
    personal = await new Host("personal-first", "personal").start();
    const item = personal.package("personal"); assert(item.enabled); assert.equal(item.runtime_state, "active", item.last_error);
    for (const method of ["workspace/list", "thread/list", "thread/start", "turn/start", "config/get", "extension/package/update", "storage/list"]) {
      await assert.rejects(personal.rpc(method), /user host client method is unavailable/);
    }
    return { id: item.id, fingerprint: item.fingerprint };
  });
  await scenario("Personal starts with optional Memory disabled", async () => {
    const item = personal.package("memory"); assert.equal(item.enabled, false);
    return { optional_memory_enabled: item.enabled };
  });
  await scenario("Concurrent default requests bind one persistent main session", async () => {
    const results = await Promise.all([personal.client("agents.ensure_default"), personal.client("agents.ensure_default")]);
    main = results[0].agent; assert(main.main_session_id); assert.equal(main.main_session_id, results[1].agent.main_session_id);
    assert.equal(main.kind, "main"); assert.equal((await personal.client("agents.list")).agents.length, 1);
    const opened = await personal.client("agents.open", { agent_id: main.agent_id }); assert.equal(opened.agent.main_session_id, main.main_session_id);
    return { agent_id: main.agent_id, main_session_id: main.main_session_id, opened };
  });
  await scenario("Real Personal turn excludes Coding instructions and notebook", async () => {
    const sent = await personal.send("personal-first");
    const calls = agentCalls("personal-first"); assert(calls.length > 0);
    for (const marker of privateMarkers) assert(!JSON.stringify(calls).includes(marker), `Personal provider received ${marker}`);
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    firstReplySeq = history.records.find(record => record.content?.includes("MOSAIC_REPLY:personal-first"))?.seq;
    assert(firstReplySeq);
    const turn = personal.transcript.find(frame => frame.method === "turn/completed" && frame.params?.turn?.id === sent.turn_id)?.params.turn;
    const user = turn?.items.find(item => item.type === "user_message" && item.text === "MOSAIC_E2E_CASE:personal-first");
    assert(user, "missing visible user message"); assert.equal(user.origin, "user"); assert.notEqual(user.read_only, true);
    return { provider_calls: calls.length, history_records: history.records.length };
  });
  await scenario("Personal history hides durable model context but preserves identical exact user text", async () => {
    const communication = main.communication, marker = "MOSAIC_IDENTITY_CONTEXT_271fe";
    main = (await personal.client("agents.update", { agent_id: main.agent_id, expected_revision: main.revision, communication: marker })).agent;
    await personal.send("history-projection");
    assert(agentCalls("history-projection").some(call => JSON.stringify(call.body.messages).includes(marker)), "identity context must still reach the provider");
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    assert(!history.records.some(record => record.content?.includes(marker)), "internal identity context leaked into conversation history");
    const hiddenSearch = await personal.client("agents.search", { agent_id: main.agent_id, query: marker });
    assert.equal(hiddenSearch.matches?.length ?? 0, 0);
    const persisted = spawnSync(testBinary, ["read-persisted-history", join(personalState, "sessions"), main.main_session_id], { encoding: "utf8" });
    assert.equal(persisted.status, 0, persisted.stderr);
    const audit = JSON.parse(persisted.stdout).Records;
    assert(audit.some(record => record.content?.includes(marker) && record.hidden === true && record.read_only === true && record.origin === "plugin"), "projection must not remove durable model context");
    await writeFile(join(scratch, "history-provenance-audit.json"), persisted.stdout);

    const prompt = `  MOSAIC_E2E_CASE:history-user-text\n${marker}\n  `;
    const sent = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "history-user-text", prompt });
    await personal.settled(sent);
    const found = await personal.client("agents.search", { agent_id: main.agent_id, query: marker });
    assert.equal(found.matches.length, 1); assert.equal(found.matches[0].role, "user"); assert.equal(found.matches[0].excerpt, prompt);
    const original = await personal.client("agents.history", { agent_id: main.agent_id, start_seq: found.matches[0].seq, end_seq: found.matches[0].seq });
    assert.equal(original.records.length, 1); assert.equal(original.records[0].content, prompt); assert.equal(original.records[0].seq, found.matches[0].seq);
    main = (await personal.client("agents.update", { agent_id: main.agent_id, expected_revision: main.revision, communication })).agent;
    return { durable_context_retained: true, provider_context_retained: true, exact_user_seq: found.matches[0].seq };
  });
  await scenario("Personal chat rejects missing or mismatched live client input proof", async () => {
    const input = { agent_id: main.agent_id, request_id: "unproved-intent", prompt: "MOSAIC_E2E_CASE:unproved-chat" };
    await assert.rejects(personal.client("agents.send", input, "personal", { user_input: undefined }), /user.input|provenance|proof|client|authorization/i);
    await assert.rejects(personal.client("agents.send", input, "personal", { user_input: { request_id: input.request_id, prompt: "mismatched text" } }), /user.input|provenance|proof|match|client|authorization/i);
    assert.equal(agentCalls("unproved-chat").length, 0);
    return { missing_proof_rejected: true, mismatched_prompt_rejected: true };
  });
  await scenario("Duplicate chat intent executes and persists only once", async () => {
    const first = await personal.send("duplicate", "stable-chat-intent");
    duplicateTurnID = first.turn_id;
    const second = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "stable-chat-intent", prompt: "MOSAIC_E2E_CASE:duplicate" });
    assert.equal(second.turn_id, first.turn_id);
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    assert.equal(history.records.filter(record => record.role === "user" && record.content?.includes("MOSAIC_E2E_CASE:duplicate")).length, 1);
    assert.equal(agentCalls("duplicate").length, 1); return { turn_id: first.turn_id };
  });
  await scenario("Attachment proof rejects changed bytes, omitted selections and plugin-supplied paths", async () => {
    const bytes = Buffer.from("MOSAIC_SELECTED_BYTES_".repeat(8000));
    const file = { sha256: createHash("sha256").update(bytes).digest("hex"), filename: "selected.txt", media_type: "text/plain", data: bytes.toString("base64") };
    const { data, ...ref } = file;
    const input = { agent_id: main.agent_id, request_id: "attachment-only-intent", prompt: "", attachments: [ref] };
    const proof = { request_id: input.request_id, prompt: input.prompt, attachments: [file] };
    const before = providerCalls.length;
    // Each malformed submission is a distinct intent: otherwise the durable
    // idempotency fence rejects changed fingerprints before proof validation.
    const rejected = (suffix, candidate, userInput) => {
      const request_id = `attachment-rejected-${suffix}`;
      return personal.client("agents.send", { ...candidate, request_id }, "personal", userInput ? { user_input: { ...userInput, request_id } } : {});
    };
    await assert.rejects(rejected("missing", input), /attachment|user.input|submission/i);
    await assert.rejects(rejected("bytes", input, { ...proof, attachments: [{ ...file, data: Buffer.from("different bytes").toString("base64") }] }), /digest|bytes/i);
    await assert.rejects(rejected("renamed", { ...input, attachments: [{ ...ref, filename: "renamed.txt" }] }, proof), /match|selection/i);
    await assert.rejects(rejected("path", { ...input, attachments: [{ ...ref, path: "/untrusted" }] }, proof), /path|unknown|reference/i);
    await assert.rejects(rejected("omitted", { ...input, prompt: "text", attachments: [] }, { ...proof, prompt: "text" }), /match|selection/i);
    assert.equal(providerCalls.length, before);
    attachmentSubmission = { input, proof, bytes, file };
    return { rejected_cases: 5, zero_inference: true };
  });
  await scenario("Attachment-only user input persists bounded metadata and digest-verified chunks once", async () => {
    const { input, proof, bytes, file } = attachmentSubmission;
    const sent = await personal.client("agents.send", input, "personal", { user_input: proof });
    const completed = await personal.settled(sent);
    const submitted = completed.params.turn.items.find(item => item.type === "user_message");
    assert.equal(submitted?.origin, "user"); assert.notEqual(submitted.read_only, true);
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    const records = history.records.filter(record => record.role === "user" && record.attachments?.some(item => item.sha256 === file.sha256));
    assert.equal(records.length, 1); const record = records[0], attachment = record.attachments[0];
    assert.equal(record.content, ""); assert.equal(attachment.name, file.filename); assert.equal(attachment.kind, "file");
    assert(!JSON.stringify(record).includes(file.data)); assert(!JSON.stringify(record).includes(personalRoot));
    const downloaded = await downloadAttachment(personal, "agents.attachments.read", { agent_id: main.agent_id }, record, attachment);
    assert.deepEqual(downloaded.bytes, bytes); assert(downloaded.chunks > 1);
    const before = providerCalls.length;
    const replay = await personal.client("agents.send", input, "personal", { user_input: proof }); assert.equal(replay.turn_id, sent.turn_id);
    assert.equal(providerCalls.length, before);
    Object.assign(attachmentSubmission, { sent, record, attachment });
    return { turn_id: sent.turn_id, seq: record.seq, bytes: bytes.length, chunks: downloaded.chunks, sha256: file.sha256 };
  });
  await scenario("Text plus image and file requires exact ordered proof and preserves readable history", async () => {
    const png = { filename: "pixel.png", media_type: "image/png", data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aF9kAAAAASUVORK5CYII=" };
    png.sha256 = createHash("sha256").update(Buffer.from(png.data, "base64")).digest("hex");
    const files = [png, attachmentSubmission.file];
    const input = { agent_id: main.agent_id, request_id: "text-attachments-intent", prompt: "  MOSAIC_E2E_CASE:text-attachments  ", attachments: files.map(({ data, ...ref }) => ref) };
    const user_input = { request_id: input.request_id, prompt: input.prompt, attachments: files };
    await assert.rejects(personal.client("agents.send", input, "personal", { user_input: { ...user_input, prompt: input.prompt.trim() } }), /match|submission/i);
    await assert.rejects(personal.client("agents.send", input, "personal", { user_input: { ...user_input, attachments: [...files].reverse() } }), /match|selection/i);
    let selectedPath;
    toolPlans.set("text-attachments", { tool: "read_file", arguments: body => {
      const message = body.messages.filter(message => message.role === "user").at(-1);
      const content = typeof message.content === "string" ? message.content : message.content.filter(part => part.type === "text").map(part => part.text).join("\n");
      selectedPath = content.match(/"selected\.txt"[^\n]*:\s+([^\n]+)/)?.[1];
      assert(selectedPath?.startsWith(`${personalRoot}/.input-files/`));
      return { path: selectedPath };
    } });
    await personal.settled(await personal.client("agents.send", input, "personal", { user_input }));
    const result = agentCalls("text-attachments").at(-1).body.messages.filter(message => message.role === "tool").at(-1);
    assert(messageText(result).includes("MOSAIC_SELECTED_BYTES_"), "the model could not read the selected working copy");
    await writeFile(selectedPath, "CHANGED_WORKING_COPY_AFTER_USER_SELECTION");
    const record = (await personal.client("agents.history", { agent_id: main.agent_id })).records.find(record => record.role === "user" && record.content?.includes("MOSAIC_E2E_CASE:text-attachments"));
    assert.equal(record.content, input.prompt);
    assert.equal(record.attachments.length, 2); assert.deepEqual(record.attachments.map(item => item.kind), ["image", "file"]);
    for (const attachment of record.attachments) await downloadAttachment(personal, "agents.attachments.read", { agent_id: main.agent_id }, record, attachment);
    return { seq: record.seq, attachments: record.attachments };
  });
  await scenario("One SDK owner keeps work alive across concurrent and disconnected client transports", async () => {
    const peer = await new Host("personal-peer", "personal", invocation, personal).start();
    assert.equal((await peer.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    const input = { agent_id: main.agent_id, request_id: "multi-client-intent", prompt: "MOSAIC_E2E_CASE:multi-client" };
    const [one, two] = await Promise.all([personal.client("agents.send", input), peer.client("agents.send", input)]);
    assert.equal(one.turn_id, two.turn_id); await peer.settled(two); assert.equal(agentCalls("multi-client").length, 1);
    const detached = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "disconnect-work", title: "Continue without a device", goal: "MOSAIC_E2E_CASE:background-disconnected" })).task;
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "background-disconnected", "detached work admission");
    await personal.disconnect(); await peer.close();
    assert.equal(personal.child.exitCode, null); assert(!agentCalls("background-disconnected")[0].cancelled);
    held.get("background-disconnected")();
    await waitPersistedReply(detached.execution.session_id, "MOSAIC_REPLY:background-disconnected", "headless-persisted-history.json");
    await personal.reconnect();
    let history; const deadline = Date.now() + 10_000;
    do {
      history = await personal.client("tasks.history", { task_id: detached.task_id });
      if (history.records.some(record => record.content?.includes("MOSAIC_REPLY:background-disconnected"))) break;
      await new Promise(resolve => setTimeout(resolve, 50));
    } while (Date.now() < deadline);
    assert(history.records.some(record => record.content?.includes("MOSAIC_REPLY:background-disconnected")));
    assert.equal((await personal.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    assert.equal(agentCalls("background-disconnected").length, 1); assert(!agentCalls("background-disconnected")[0].cancelled);
    return { shared_turn_id: one.turn_id, headless_task_id: detached.task_id, owner_pid: personal.child.pid };
  });
  await scenario("Completion-time notification opt-out suppresses attention while visible replies persist", async () => {
    let settings = (await personal.client("settings.get")).settings;
    const original = settings;
    settings = (await personal.client("settings.update", { expected_revision: settings.revision, notifications: true, notification_batch_seconds: 0, quiet_hours: { ...settings.quiet_hours, enabled: false } })).settings;
    const positive = await personal.send("attention-positive");
    const initial = await waitFor(personal.events, "message", personal.transcript, frame => frame.method === "user/attention" && frame.params?.turn_id === positive.turn_id, "immediate owner-authorized attention");
    assert.equal(initial.params.user_location.agent_id, main.agent_id);
    assert.equal(initial.params.user_location.user_space_id, "local"); assert.equal(initial.params.user_location.product_domain, "personal");
    assert(initial.params.user_location.message_seq > 0);
    for (const key of ["content", "text", "prompt", "error", "thread_id", "session_id"]) assert(!(key in initial.params));
    assert(!("session_id" in initial.params.user_location));
    heldTags.add("attention-opt-out");
    const sent = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "attention-opt-out", prompt: "MOSAIC_E2E_CASE:attention-opt-out" });
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "attention-opt-out" && call.kind === "agent", "held response before notification preference change");
    settings = (await personal.client("settings.update", { expected_revision: settings.revision, notifications: false, notification_batch_seconds: 0, quiet_hours: settings.quiet_hours })).settings;
    held.get("attention-opt-out")(); await personal.settled(sent);
    // Denial has no public acknowledgement. Keep the preference disabled beyond
    // the host policy callback deadline, then use a positive event as a barrier.
    await new Promise(resolve => setTimeout(resolve, 6000));
    assert(!personal.transcript.some(frame => frame.method === "user/attention" && frame.params?.turn_id === sent.turn_id));
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    assert(history.records.some(record => record.content === "MOSAIC_REPLY:attention-opt-out"));
    settings = (await personal.client("settings.update", { expected_revision: settings.revision, notifications: true, notification_batch_seconds: 0, quiet_hours: settings.quiet_hours })).settings;
    const enabled = await personal.send("attention-restored");
    await waitFor(personal.events, "message", personal.transcript, frame => frame.method === "user/attention" && frame.params?.turn_id === enabled.turn_id, "attention after explicit re-enable");
    assert(!personal.transcript.some(frame => frame.method === "user/attention" && frame.params?.turn_id === sent.turn_id));
    await personal.client("settings.update", { expected_revision: settings.revision, notifications: original.notifications, notification_batch_seconds: original.notification_batch_seconds ?? 0, quiet_hours: original.quiet_hours });
    return { allowed_turn: positive.turn_id, suppressed_turn: sent.turn_id, visible_reply_retained: true, immediate_policy_only: true, external_push_not_tested: true };
  });
  await scenario("Working notes persist under Personal state without global storage", async () => {
    toolPlans.set("notes-write", { tool: "notes", arguments: { action: "write", path: "checkpoint.md", content: "MOSAIC_PERSONAL_SCOPED_NOTE", revision: "" } });
    await personal.send("notes-write");
    const name = `${createHash("sha256").update(main.main_session_id).digest("hex")}.json`;
    const path = join(personalState, "working-notes", name);
    assert((await readFile(path, "utf8")).includes("MOSAIC_PERSONAL_SCOPED_NOTE"));
    await assert.rejects(readFile(join(home, "working-notes", name)), { code: "ENOENT" });
    return { path };
  });
  await scenario("Process restart and project switch preserve main binding and history", async () => {
    await personal.close(); personal = await new Host("personal-restarted", "personal", otherProject).start();
    const restored = (await personal.client("agents.ensure_default")).agent; assert.equal(restored.main_session_id, main.main_session_id);
    const history = await personal.client("agents.history", { agent_id: restored.agent_id, start_seq: firstReplySeq, end_seq: firstReplySeq });
    assert(history.records.some(record => record.content?.includes("MOSAIC_REPLY:personal-first")));
    const replay = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "stable-chat-intent", prompt: "MOSAIC_E2E_CASE:duplicate" });
    assert.equal(replay.turn_id, duplicateTurnID); assert.equal(agentCalls("duplicate").length, 1);
    const { input, proof, record, attachment, bytes, sent } = attachmentSubmission;
    const attachmentRetry = await personal.client("agents.send", input, "personal", { user_input: proof }); assert.equal(attachmentRetry.turn_id, sent.turn_id);
    const reread = await downloadAttachment(personal, "agents.attachments.read", { agent_id: main.agent_id }, record, attachment); assert.deepEqual(reread.bytes, bytes);
    main = restored; await personal.send("after-restart");
    toolPlans.set("notes-after-restart", { tool: "notes", arguments: { action: "read", path: "checkpoint.md" } });
    await personal.send("notes-after-restart");
    const toolResult = agentCalls("notes-after-restart").at(-1).body.messages.filter(message => message.role === "tool").at(-1);
    assert(messageText(toolResult).includes("MOSAIC_PERSONAL_SCOPED_NOTE"));
    return { main_session_id: main.main_session_id, working_notes_restored: true };
  });
  await scenario("Personal memory imports no Coding notebook and rejects caller-selected domain", async () => {
    const query = await personal.client("memory.query", { agent_id: main.agent_id });
    assert(!JSON.stringify(query).includes(codingMarker));
    await assert.rejects(personal.client("memory.query", { agent_id: main.agent_id, product_domain: "coding" }), /domain|scope|unexpected/i);
    return query;
  });
  await scenario("Disabled legacy Memory records and forgotten-source barriers migrate before Personal activation", async () => {
    assert.equal(personal.package("memory").enabled, false);
    const restored = (await personal.client("memory.read", { agent_id: main.agent_id, id: legacyID })).record;
    assert.equal(restored.revision, legacyRecord.revision); assert.equal(restored.content, legacyRecord.content);
    assert.deepEqual(restored.sources, legacyRecord.sources); assert.deepEqual(restored.contributions, legacyRecord.contributions);
    const tombstone = (await personal.client("memory.read", { agent_id: main.agent_id, id: legacyForgottenID })).record;
    assert.equal(tombstone.status, "forgotten"); assert.equal(tombstone.revision, 2);
    toolPlans.set("memory-forgotten-legacy", { plugin: "personal", tool: "personal_memory_create", arguments: {
      key: "fixture.legacy.resurrection", expected_revision: 0, content: "Must not return", evidence: "explicit",
      sources: [{ kind: "message", ref: "synthetic-legacy-forgotten-source", occurred_at: legacyTime }],
    } });
    await personal.send("memory-forgotten-legacy");
    assert(agentCalls("memory-forgotten-legacy").at(-1).body.messages.some(message => message.role === "tool" && messageText(message).includes("forgotten_source")));
    const probe = spawnSync(testBinary, ["probe-storage-transfer", personalState, `personal.records.v1.${legacyID}`], { encoding: "utf8" });
    assert.equal(probe.status, 0, probe.stderr); const fenced = JSON.parse(probe.stdout);
    assert.equal(fenced.source_visible, false); assert.equal(fenced.write_denied, true);
    await writeFile(join(scratch, "legacy-owner-fence.json"), JSON.stringify(fenced, null, 2));
    return { preserved_record: legacyID, revision: restored.revision, preserved_tombstone: legacyForgottenID, source_disabled: true, old_owner_fenced: true };
  });
  await scenario("Real memory tool writes preserve trusted author and source attribution", async () => {
    toolPlans.set("memory-create", { plugin: "personal", tool: "personal_memory_create", arguments: {
      key: "fixture.preference.drink", expected_revision: 0, content: "The synthetic user prefers mint tea",
      evidence: "explicit", sources: [{ kind: "message", ref: "mosaic-fixture-user-message-1", occurred_at: "2026-10-18T00:00:00Z" }],
    } });
    await personal.send("memory-create");
    const query = await personal.client("memory.query", { agent_id: main.agent_id, query: "mint tea" });
    memoryRecord = query.records.find(record => record.key === "fixture.preference.drink");
    assert(memoryRecord, "real tool did not persist a queryable record");
    assert.equal(memoryRecord.contributor_agent_id, main.agent_id);
    assert.equal(memoryRecord.product_domain, "personal"); assert.equal(memoryRecord.user_space_id, "local");
    assert(memoryRecord.sources.some(source => source.ref === "mosaic-fixture-user-message-1")); return memoryRecord;
  });
  await scenario("Personal-only worker candidates require main evidence review and acceptance", async () => {
    await personal.send("memory-evidence-source");
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    const source = history.records.find(record => record.role === "user" && record.content === "MOSAIC_E2E_CASE:memory-evidence-source");
    assert(source);
    const ref = `personal-message:${encodeURIComponent(main.main_session_id)}:${source.seq}`;
    toolPlans.set("memory-worker-candidate", { plugin: "personal", tool: "personal_memory_create", arguments: {
      key: "fixture.worker.candidate", expected_revision: 0, content: "Synthetic worker review candidate", evidence: "inferred",
      sources: [{ kind: "message", ref, occurred_at: "2026-01-01T00:00:00Z" }],
    } });
    const worker = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "memory-candidate-worker", title: "Review memory evidence", goal: "MOSAIC_E2E_CASE:memory-worker-candidate" })).task;
    await personal.settled({ session_id: worker.execution.session_id, turn_id: worker.execution.turn_id });
    const records = (await personal.client("memory.query", { agent_id: main.agent_id, include_inactive: true })).records;
    const candidate = records.find(record => record.key === "fixture.worker.candidate");
    assert(candidate); assert.equal(candidate.status, "candidate"); assert.equal(candidate.sources[0].occurred_at, new Date(source.at).toISOString());
    assert(!(await personal.client("memory.query", { agent_id: main.agent_id, query: "Synthetic worker review candidate" })).records.some(record => record.id === candidate.id));
    toolPlans.set("memory-main-review", [
      { plugin: "personal", tool: "personal_memory_evidence", arguments: { id: candidate.id } },
      { plugin: "personal", tool: "personal_memory_accept", arguments: { id: candidate.id, expected_revision: candidate.revision } },
    ]);
    await personal.send("memory-main-review");
    const review = agentCalls("memory-main-review").at(-1).body.messages.filter(message => message.role === "tool");
    assert(review.some(message => messageText(message).includes("MOSAIC_E2E_CASE:memory-evidence-source")));
    const accepted = (await personal.client("memory.read", { agent_id: main.agent_id, id: candidate.id })).record;
    assert.equal(accepted.status, "active"); assert.equal(accepted.evidence, "inferred"); assert.deepEqual(accepted.sources, candidate.sources);
    await personal.client("tasks.stop", { task_id: worker.task_id, expected_revision: (await personal.client("tasks.get", { task_id: worker.task_id })).task.revision });
    return { candidate_id: candidate.id, worker_session: worker.execution.session_id, main_reviewed: true, source_timestamp_preserved: true };
  });
  await scenario("Enabling optional Memory adds no duplicate Personal tools or records provider", async () => {
    assert.equal(personal.package("memory").enabled, false);
    await personal.enable("memory");
    await personal.send("memory-both-enabled");
    const names = agentCalls("memory-both-enabled")[0].body.tools.map(item => item.function.name);
    assert.equal(names.filter(name => name.startsWith("plugin_personal_personal_memory_que")).length, 1);
    assert(!names.some(name => name.startsWith("plugin_memory_")));
    const query = await personal.client("memory.query", { agent_id: main.agent_id, query: "mint tea" });
    assert.equal(query.records.filter(record => record.id === memoryRecord.id).length, 1);
    const notebook = await coding.client("memory.read", {}, "memory");
    assert(JSON.stringify(notebook).includes(codingMarker));
    return { one_personal_memory_tool_set: true, coding_notebook_retained: true };
  });
  await scenario("The public product exposes only its durable main conversation", async () => {
    const listed = await personal.client("agents.list");
    assert.deepEqual(listed.agents.map(agent => agent.agent_id), [main.agent_id]);
    assert.equal(listed.agents[0].main_session_id, main.main_session_id);
    await assert.rejects(personal.client("agents.open", { agent_id: "unknown-assistant" }), /main|agent|conversation|found/i);
    const names = agentCalls("personal-first").flatMap(call => call.body.tools?.map(item => item.function?.name) ?? []);
    assert(!names.some(name => name.includes("propose_specialist")));
    return { agent_id: main.agent_id, main_session_id: main.main_session_id };
  });
  await scenario("Attachment references cannot select another agent, arbitrary session or changed digest", async () => {
    const { record, attachment } = attachmentSubmission;
    const input = { agent_id: main.agent_id, seq: record.seq, kind: attachment.kind, index: attachment.index, sha256: attachment.sha256 };
    await assert.rejects(personal.client("agents.attachments.read", { ...input, agent_id: "unknown-assistant" }), /agent|main|conversation|removed|changed|source/i);
    await assert.rejects(personal.client("agents.attachments.read", { ...input, session_id: main.main_session_id }), /scope|session|path/i);
    await assert.rejects(personal.client("agents.attachments.read", { ...input, sha256: "0".repeat(64) }), /removed|changed/i);
    await assert.rejects(personal.client("agents.attachments.read", { ...input, offset: attachmentSubmission.file.data.length + 1 }), /offset|size/i);
    await assert.rejects(coding.client("agents.attachments.read", input), /unavailable|Personal|does not accept/i);
    return { rejected_cases: 5 };
  });
  await scenario("Shared Personal correction conflicts are explicit and forgetting removes common recall", async () => {
    const shared = await personal.client("memory.query", { agent_id: main.agent_id, query: "mint tea" });
    assert(shared.records.some(record => record.id === memoryRecord.id));
    const corrected = await personal.client("memory.correct", { agent_id: main.agent_id, id: memoryRecord.id,
      expected_revision: memoryRecord.revision, request_id: "explicit-drink-correction", content: "The synthetic user now prefers green tea" });
    assert.equal(corrected.record.id, memoryRecord.id); assert.equal(corrected.record.updated_by_agent_id, main.agent_id);
    await assert.rejects(personal.client("memory.correct", { agent_id: main.agent_id, id: memoryRecord.id,
      expected_revision: memoryRecord.revision, request_id: "stale-drink-correction", content: "Stale mint tea claim" }), /revision|conflict/i);
    const latest = await personal.client("memory.read", { agent_id: main.agent_id, id: memoryRecord.id });
    assert.equal(latest.record.content, "The synthetic user now prefers green tea");
    const forgotten = await personal.client("memory.forget", { agent_id: main.agent_id, id: memoryRecord.id, expected_revision: latest.record.revision });
    assert.equal(forgotten.record.status, "forgotten"); assert.equal(forgotten.record.content, undefined);
    const after = await personal.client("memory.query", { agent_id: main.agent_id, query: "tea" });
    assert(!after.records.some(record => record.id === memoryRecord.id));
    return { id: memoryRecord.id, corrected_revision: latest.record.revision, forgotten_revision: forgotten.record.revision };
  });
  await scenario("Two independent tasks retain separate revisions and targeted cancellation", async () => {
    const a = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "parallel-a", title: "Review design", goal: "MOSAIC_E2E_CASE:background-parallel-a" })).task;
    const b = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "parallel-b", title: "Find discussion", goal: "MOSAIC_E2E_CASE:background-parallel-b" })).task;
    assert.notEqual(a.task_id, b.task_id); assert.notEqual(a.execution.session_id, b.execution.session_id);
    for (const tag of ["background-parallel-a", "background-parallel-b"]) await waitFor(providerEvents, "request", providerCalls, call => call.tag === tag && call.kind === "agent", tag);
    await personal.send("chat-with-two-background-tasks");
    const currentA = (await personal.client("tasks.get", { task_id: a.task_id })).task;
    const revised = (await personal.client("tasks.update", { task_id: a.task_id, expected_revision: currentA.revision, goal: "MOSAIC_E2E_CASE:background-parallel-a-revised" })).task;
    assert.equal(revised.task_id, a.task_id); assert.equal(revised.requirements_revision, 2);
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "background-parallel-a-revised", "revised first task");
    const currentB = (await personal.client("tasks.get", { task_id: b.task_id })).task;
    assert.equal(currentB.requirements_revision, 1); assert.equal(currentB.cancel_requested, false);
    const stopped = (await personal.client("tasks.stop", { task_id: b.task_id, expected_revision: currentB.revision })).task;
    assert.equal(stopped.task_id, b.task_id); assert.equal(stopped.cancel_requested, true);
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "background-parallel-b" && call.cancelled, "targeted cancel");
    const survivor = (await personal.client("tasks.get", { task_id: a.task_id })).task;
    assert.equal(survivor.cancel_requested, false); assert.equal(survivor.requirements_revision, 2);
    assert(!agentCalls("background-parallel-a-revised")[0].cancelled);
    await personal.send("main-after-targeted-cancel");
    await personal.client("tasks.stop", { task_id: survivor.task_id, expected_revision: survivor.revision });
    return { revised_task: revised.task_id, stopped_task: stopped.task_id, sibling_continued: true };
  });
  await scenario("Generic read cannot reveal the Coding notebook to Personal", async () => {
    toolPlans.set("cross-domain-read", { tool: "read_file", arguments: { path: join(home, "memory", "MEMORY.md") } });
    await personal.send("cross-domain-read");
    const calls = agentCalls("cross-domain-read"), final = calls.at(-1);
    assert(calls.length >= 2, "read attempt must return a tool result to the provider");
    const toolResult = final.body.messages.filter(message => message.role === "tool").at(-1);
    assert(toolResult, "missing read tool result"); assert.match(messageText(toolResult), /denied|protected|not allowed|permission|outside/i);
    assert(!JSON.stringify(calls).includes(codingMarker)); return { tool_result: toolResult };
  });
  await scenario("Personal artifact imports an immutable snapshot retrievable only with its scoped root", async () => {
    const path = join(personalRoot, "synthetic-deliverable.txt"), content = "MOSAIC_SCOPED_ARTIFACT_BYTES";
    await writeFile(path, content);
    toolPlans.set("artifact-publish", { tool: "present_artifact", arguments: { path } });
    const start = personal.transcript.length;
    await personal.send("artifact-publish");
    const part = personal.transcript.slice(start).flatMap(frame => frame.params?.tool_result_detail?.content ?? []).find(part => part.uri?.startsWith("wuu-artifact:"));
    const uri = part?.uri; assert(uri, "present_artifact did not publish a managed URI");
    const parsed = new URL(uri), stateDir = join(personalState, "workspaces", parsed.hostname);
    await writeFile(path, "CHANGED_SOURCE_AFTER_PUBLISH");
    const retrieved = spawnSync(testBinary, ["read-artifact", personalState, stateDir, main.main_session_id, uri], { encoding: "utf8" });
    assert.equal(retrieved.status, 0, retrieved.stderr); assert.equal(retrieved.stdout, content);
    const wrongRoot = spawnSync(testBinary, ["read-artifact", home, stateDir, main.main_session_id, uri], { encoding: "utf8" });
    assert.notEqual(wrongRoot.status, 0); assert.match(wrongRoot.stderr, /outside.*managed workspace/);
    return { uri, state_dir: stateDir, retrieved_sha256: createHash("sha256").update(retrieved.stdout).digest("hex"), coding_root_rejected: true };
  });
  await scenario("Task artifact history reads stay bound to the owned execution across restart", async () => {
    const path = join(personalRoot, "task-output.txt"), bytes = Buffer.from("MOSAIC_TASK_ARTIFACT_BYTES"); await writeFile(path, bytes);
    toolPlans.set("task-attachment", { tool: "present_artifact", arguments: { path } });
    const owned = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "task-attachment-intent", title: "Task attachment", goal: "MOSAIC_E2E_CASE:task-attachment" })).task;
    await personal.settled({ session_id: owned.execution.session_id, turn_id: owned.execution.turn_id });
    const history = await personal.client("tasks.history", { task_id: owned.task_id });
    const record = history.records.find(record => record.attachments?.some(item => item.kind === "artifact")); assert(record, "task output metadata missing");
    const attachment = record.attachments.find(item => item.kind === "artifact");
    const scope = { task_id: owned.task_id, requirements_revision: 1 };
    assert.deepEqual((await downloadAttachment(personal, "tasks.attachments.read", scope, record, attachment)).bytes, bytes);
    await assert.rejects(personal.client("tasks.attachments.read", { ...scope, seq: record.seq, ...attachment, requirements_revision: 999 }), /unknown_task_requirements_revision/);
    await assert.rejects(personal.client("tasks.attachments.read", { ...scope, seq: record.seq, ...attachment, session_id: main.main_session_id }), /session_id|selector|scope/i);
    await assert.rejects(personal.client("agents.attachments.read", { agent_id: main.agent_id, seq: record.seq, ...attachment }), /changed|removed/i);
    await writeFile(path, "MODIFIED_AFTER_TASK_PUBLICATION");
    await personal.close(); personal = await new Host("personal-task-artifact-restarted", "personal").start();
    assert.deepEqual((await downloadAttachment(personal, "tasks.attachments.read", scope, record, attachment)).bytes, bytes);
    return { task_id: owned.task_id, seq: record.seq, attachment, restart_retained_immutable_bytes: true };
  });
  await scenario("Background task deduplicates while the main conversation continues", async () => {
    const input = { agent_id: main.agent_id, request_id: "background-intent", title: "Synthetic long lookup", goal: "MOSAIC_E2E_CASE:background-one" };
    task = (await personal.client("tasks.create", input)).task;
    const duplicate = (await personal.client("tasks.create", input)).task;
    assert.equal(task.task_id, duplicate.task_id); assert.equal(task.execution.session_id, duplicate.execution.session_id);
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "background-one", "background provider admission");
    await personal.send("chat-during-background");
    await assert.rejects(personal.rpc("thread/list"), /user host client method is unavailable/);
    return { task_id: task.task_id, execution: task.execution };
  });
  await scenario("Stop reply preserves accepted queued user inputs and independent background work", async () => {
    heldTags.add("main-stop-current");
    const first = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "main-stop-current", prompt: "MOSAIC_E2E_CASE:main-stop-current" });
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "main-stop-current" && call.kind === "agent", "current reply is held");
    const pending = [];
    for (const tag of ["main-stop-queued-one", "main-stop-queued-two"]) {
      const input = { agent_id: main.agent_id, request_id: tag, prompt: `MOSAIC_E2E_CASE:${tag}` };
      const sent = await personal.client("agents.send", input);
      assert.equal(sent.state, "queued"); assert(sent.queue_id); pending.push({ tag, input, queue_id: sent.queue_id });
    }
    const cancellation = await personal.client("agents.stop_reply", { agent_id: main.agent_id, turn_id: first.turn_id });
    assert.equal(cancellation.cancelled, true);
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "main-stop-current" && call.cancelled, "current main reply cancellation reaches provider");
    for (const queued of pending) await waitFor(personal.events, "message", personal.transcript, frame => frame.method === "turn/completed" && frame.params?.thread_id === main.main_session_id && frame.params?.content === `MOSAIC_REPLY:${queued.tag}`, `accepted queued input ${queued.tag} completes after stop`);
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    for (const queued of pending) {
      assert.equal(history.records.filter(record => record.role === "user" && record.content === queued.input.prompt).length, 1);
      assert.equal(history.records.filter(record => record.role === "assistant" && record.content === `MOSAIC_REPLY:${queued.tag}`).length, 1);
      await personal.client("agents.send", queued.input);
      assert.equal(agentCalls(queued.tag).length, 1, "queued intent retry reran its completed reply");
    }
    heldTags.add("main-stop-newer");
    const newer = await personal.client("agents.send", { agent_id: main.agent_id, request_id: "main-stop-newer", prompt: "MOSAIC_E2E_CASE:main-stop-newer" });
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "main-stop-newer" && call.kind === "agent", "newer reply starts before stale stop retry");
    const stale = await personal.client("agents.stop_reply", { agent_id: main.agent_id, turn_id: first.turn_id });
    assert.equal(stale.cancelled, false); assert(!agentCalls("main-stop-newer")[0].cancelled);
    const open = await personal.client("agents.open", { agent_id: main.agent_id });
    assert.equal(open.turn.turn_id, newer.turn_id); assert.equal(open.turn.state, "running");
    await personal.client("agents.stop_reply", { agent_id: main.agent_id, turn_id: newer.turn_id });
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "main-stop-newer" && call.cancelled, "targeted newer reply cleanup");
    const background = (await personal.client("tasks.get", { task_id: task.task_id })).task;
    assert.equal(background.cancel_requested, false); assert.equal(background.state, "running"); assert(!agentCalls("background-one")[0].cancelled);
    return { stopped_turn_id: first.turn_id, newer_turn_survived_stale_retry: newer.turn_id, preserved_queue_ids: pending.map(item => item.queue_id), background_task_id: background.task_id, main_session_id: main.main_session_id };
  });
  await scenario("Stop survives late provider completion and process restart", async () => {
    task = (await personal.client("tasks.stop", { task_id: task.task_id, expected_revision: task.revision })).task;
    assert(task.cancel_requested); assert(["stopped", "waiting_external"].includes(task.state));
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "background-one" && call.cancelled, "task cancellation reaches provider");
    task = (await personal.client("tasks.reconcile", { task_id: task.task_id })).task;
    assert.equal(task.state, "stopped"); held.get("background-one")?.();
    await personal.close(); personal = await new Host("personal-task-restarted", "personal").start();
    const restored = (await personal.client("tasks.get", { task_id: task.task_id })).task;
    assert.equal(restored.state, "stopped"); assert.equal(restored.execution.request_id, task.execution.request_id);
    const replay = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: restored.request_id, title: restored.title, goal: restored.goal })).task;
    assert.equal(replay.task_id, restored.task_id); assert.equal(replay.state, "stopped"); assert.equal(replay.execution.session_id, restored.execution.session_id);
    assert.equal((await personal.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    return restored;
  });
  await scenario("A final answer without persisted task evidence never claims completion", async () => {
    let unknown = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "unknown-result", title: "Unverified result", goal: "MOSAIC_E2E_CASE:unknown-result" })).task;
    await personal.settled({ session_id: unknown.execution.session_id, turn_id: unknown.execution.turn_id });
    unknown = (await personal.client("tasks.reconcile", { task_id: unknown.task_id })).task;
    assert.notEqual(unknown.state, "completed"); assert.equal(unknown.reconciliation_required, true); return unknown;
  });
  await scenario("Fabricated and failed tool-result references cannot verify task completion", async () => {
    const rejected = [];
    for (const kind of ["fabricated", "failed"]) {
      const tag = `invalid-evidence-${kind}`;
      const complete = { plugin: "personal", tool: "complete_task", arguments: body => {
        const taskID = JSON.stringify(body.messages).match(/Work on Personal task (task-[a-z0-9]+)/)?.[1]; assert(taskID);
        const failed = body.messages.find(message => message.role === "tool" && message.name === "read_file");
        return { task_id: taskID, requirements_revision: 1, summary: "This must never be presented as completed", evidence: [{ tool_call_id: kind === "failed" ? failed.tool_call_id : "fabricated-tool-call-id", description: "Untrusted result claim" }] };
      } };
      toolPlans.set(tag, kind === "failed" ? [{ tool: "read_file", arguments: { path: join(personalRoot, "does-not-exist.txt") } }, complete] : [complete]);
      let invalid = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: tag, title: `Synthetic ${kind} evidence`, goal: `MOSAIC_E2E_CASE:${tag}` })).task;
      await personal.settled({ session_id: invalid.execution.session_id, turn_id: invalid.execution.turn_id });
      invalid = (await personal.client("tasks.reconcile", { task_id: invalid.task_id })).task;
      assert.notEqual(invalid.state, "completed"); assert.equal(invalid.reconciliation_required, true); assert(!invalid.result); assert(!invalid.delivery);
      rejected.push({ kind, task_id: invalid.task_id, state: invalid.state, error: invalid.error });
    }
    return rejected;
  });
  await scenario("Verified task completion waits for a visible main reply before marking delivery", async () => {
    heldTags.add("delivery-success"); heldTags.add("verified-result");
    toolPlans.set("verified-result", [
      { tool: "notes", arguments: { action: "write", path: "result.md", content: "SYNTHETIC_RESULT_EVIDENCE", revision: "" } },
      { plugin: "personal", tool: "complete_task", arguments: async body => {
        const taskID = JSON.stringify(body.messages).match(/Work on Personal task (task-[a-z0-9]+)/)?.[1]; assert(taskID);
        const result = body.messages.find(message => message.role === "tool" && message.name === "notes"); assert(result?.tool_call_id);
        return { task_id: taskID, requirements_revision: 1, summary: "MOSAIC_E2E_CASE:delivery-success", evidence: [{ tool_call_id: result.tool_call_id, description: "Saved synthetic result checkpoint" }] };
      } },
    ]);
    let completed = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "verified-result-intent", title: "Synthetic verified result", goal: "MOSAIC_E2E_CASE:verified-result" })).task;
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "verified-result" && call.body.messages.some(message => message.role === "tool" && message.name?.includes("complete_task")), "task completion intent acknowledgement");
    const pending = (await personal.client("tasks.get", { task_id: completed.task_id })).task;
    assert.notEqual(pending.state, "completed", "uncommitted completion intent claimed success before executor ended");
    held.get("verified-result")();
    await personal.settled({ session_id: completed.execution.session_id, turn_id: completed.execution.turn_id });
    assert(!report.provider_error, report.provider_error);
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "delivery-success" && call.kind === "agent", "main result delivery admission");
    completed = (await personal.client("tasks.reconcile", { task_id: completed.task_id })).task;
    assert.equal(completed.state, "completed"); assert.equal(completed.delivery.status, "submitted");
    held.get("delivery-success")();
    await waitFor(personal.events, "message", personal.transcript, frame => frame.method === "turn/completed" && frame.params?.thread_id === main.main_session_id && frame.params?.content === "MOSAIC_REPLY:delivery-success", "visible main delivery response");
    completed = (await personal.client("tasks.reconcile", { task_id: completed.task_id })).task;
    assert.equal(completed.delivery.status, "delivered");
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    assert.equal(history.records.filter(record => record.role === "assistant" && record.content === "MOSAIC_REPLY:delivery-success").length, 1);
    await personal.client("tasks.reconcile", { task_id: completed.task_id });
    assert.equal(agentCalls("delivery-success").length, 1);
    return { task_id: completed.task_id, delivery: completed.delivery, result: completed.result };
  }, true);
  await scenario("Executor question is durably delivered in main and its main answer resumes the same task", async () => {
    heldTags.add("question-main"); heldTags.add("question-resumed");
    toolPlans.set("question-execution", { plugin: "personal", tool: "report_task", arguments: body => {
      const taskID = JSON.stringify(body.messages).match(/Work on Personal task (task-[a-z0-9]+)/)?.[1]; assert(taskID);
      return { task_id: taskID, requirements_revision: 1, state: "waiting_user", progress: "MOSAIC_E2E_CASE:question-main Which Sunday should I plan for?" };
    } });
    let waiting = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "question-task-intent", title: "Choose the Sunday", goal: "MOSAIC_E2E_CASE:question-execution Plan the requested Sunday activity after the user chooses the date" })).task;
    await personal.settled({ session_id: waiting.execution.session_id, turn_id: waiting.execution.turn_id });
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "question-main" && call.kind === "agent", "question reaches unique main conversation");
    waiting = (await personal.client("tasks.reconcile", { task_id: waiting.task_id })).task;
    assert.equal(waiting.state, "waiting_user"); assert.equal(waiting.delivery.kind, "question"); assert.equal(waiting.delivery.status, "submitted"); assert.equal(waiting.delivery.session_id, main.main_session_id);
    const original = { task_id: waiting.task_id, execution: waiting.execution, requirements_revision: waiting.requirements_revision, question_request_id: waiting.delivery.request_id };
    held.get("question-main")();
    await waitFor(personal.events, "message", personal.transcript, frame => frame.method === "turn/completed" && frame.params?.thread_id === main.main_session_id && frame.params?.content === "MOSAIC_REPLY:question-main", "main asks its task question visibly");
    waiting = (await personal.client("tasks.reconcile", { task_id: waiting.task_id })).task;
    assert.equal(waiting.delivery.status, "delivered");
    const history = await personal.client("agents.history", { agent_id: main.agent_id });
    assert.equal(history.records.filter(record => record.role === "assistant" && record.content === "MOSAIC_REPLY:question-main").length, 1);
    await personal.client("tasks.reconcile", { task_id: waiting.task_id }); assert.equal(agentCalls("question-main").length, 1);
    toolPlans.set("question-answer", { plugin: "personal", tool: "resume_task", arguments: async () => {
      const current = (await personal.client("tasks.get", { task_id: original.task_id })).task;
      return { task_id: current.task_id, expected_revision: current.revision, answer: "Sunday October 25", goal: "MOSAIC_E2E_CASE:question-resumed Plan the same requested Sunday activity for October 25, as the user answered" };
    } });
    await personal.send("question-answer");
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "question-resumed" && call.kind === "agent", "same task resumes after main answer");
    waiting = (await personal.client("tasks.get", { task_id: original.task_id })).task;
    assert.equal(waiting.task_id, original.task_id); assert.equal(waiting.requirements_revision, original.requirements_revision + 1); assert.equal(waiting.state, "running"); assert.equal(waiting.question, undefined);
    assert.notEqual(waiting.execution.session_id, original.execution.session_id); assert(waiting.previous_executions.some(execution => execution.request_id === original.execution.request_id));
    assert.equal((await personal.client("tasks.list")).tasks.filter(item => item.request_id === "question-task-intent").length, 1);
    assert.equal((await personal.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    const resumed = waiting.execution;
    await personal.client("tasks.stop", { task_id: waiting.task_id, expected_revision: waiting.revision });
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "question-resumed" && call.cancelled, "resumed task cleanup");
    return { task_id: waiting.task_id, main_session_id: main.main_session_id, question_delivery: original.question_request_id, original_execution: original.execution, resumed_execution: resumed };
  });
  await scenario("Conversational routine update keeps the same responsibility and scheduler binding", async () => {
    await personal.enable("automation");
    const originalTime = new Date(Date.now() + 3_600_000).toISOString(), updatedTime = new Date(Date.now() + 7_200_000).toISOString();
    const before = (await personal.client("routines.create", { agent_id: main.agent_id, request_id: "editable-routine-intent", title: "Future reminder", prompt: "MOSAIC_E2E_CASE:routine-before-edit", kind: "reminder", run_at: originalTime, timezone: "UTC" })).routine;
    assert.equal(before.binding.status, "active", before.last_error);
    toolPlans.set("routine-edit-main", { plugin: "personal", tool: "manage_routine", arguments: async () => {
      const current = (await personal.client("routines.list")).routines.find(item => item.routine_id === before.routine_id); assert(current);
      return { action: "update", routine_id: current.routine_id, expected_revision: current.revision, run_at: updatedTime, prompt: "MOSAIC_E2E_CASE:routine-after-edit" };
    } });
    await personal.send("routine-edit-main");
    const routines = (await personal.client("routines.list")).routines;
    const after = routines.find(item => item.routine_id === before.routine_id); assert(after);
    assert.equal(routines.filter(item => item.request_id === "editable-routine-intent").length, 1);
    assert.equal(after.binding.binding_id, before.binding.binding_id); assert(after.binding.revision > before.binding.revision); assert.equal(after.config_revision, before.config_revision + 1);
    assert.equal(after.run_at, updatedTime); assert.equal(after.binding.next_run_at, updatedTime); assert.equal(after.binding.status, "active");
    await personal.client("routines.pause", { routine_id: after.routine_id, expected_revision: after.revision });
    return { routine_id: after.routine_id, binding_id: after.binding.binding_id, original_binding_revision: before.binding.revision, updated_binding_revision: after.binding.revision, original_time: originalTime, updated_time: updatedTime };
  });
  await scenario("Real Routine scheduler deduplicates, pauses an active reminder and retains pause after restart", async () => {
    await personal.enable("automation"); heldTags.add("routine-reminder");
    const input = { agent_id: main.agent_id, request_id: "one-routine-intent", title: "Synthetic reminder", prompt: "MOSAIC_E2E_CASE:routine-reminder", kind: "reminder", run_at: new Date(Date.now() - 1000).toISOString(), timezone: "UTC", missed_policy: "run_once" };
    let routine = (await personal.client("routines.create", input)).routine;
    assert.equal(routine.binding.status, "active", routine.last_error);
    const duplicate = (await personal.client("routines.create", input)).routine; assert.equal(duplicate.routine_id, routine.routine_id);
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "routine-reminder" && call.kind === "agent", "actual scheduled reminder admission", 45_000);
    routine = (await personal.client("routines.list")).routines.find(item => item.routine_id === routine.routine_id);
    assert.equal(routine.status, "active", "running reminder cannot already claim completed delivery");
    routine = (await personal.client("routines.pause", { routine_id: routine.routine_id, expected_revision: routine.revision })).routine;
    assert(["paused", "stopping"].includes(routine.status));
    if (routine.status === "stopping") assert.equal(routine.cancellation_pending, true);
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "routine-reminder" && call.cancelled, "pausing routine cancels its active main reminder");
    await personal.close(); personal = await new Host("personal-routine-restarted", "personal").start();
    let restored; const deadline = Date.now() + 25_000;
    do {
      restored = (await personal.client("routines.list")).routines.find(item => item.routine_id === routine.routine_id);
      if (restored.status !== "stopping") break;
      await new Promise(resolve => setTimeout(resolve, 250));
    } while (Date.now() < deadline);
    assert.equal(restored.status, "paused"); assert.equal(restored.binding.binding_id, routine.binding.binding_id);
    assert.equal((await personal.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    await personal.send("after-routine-restart"); assert.equal(agentCalls("routine-reminder").length, 1);
    return restored;
  });
  await scenario("Routine scheduler admits and completes new work while every client is disconnected", async () => {
    heldTags.add("offline-routine");
    const routine = (await personal.client("routines.create", { agent_id: main.agent_id, request_id: "offline-routine-intent", title: "Synthetic offline reminder", prompt: "MOSAIC_E2E_CASE:offline-routine", kind: "reminder", run_at: new Date(Date.now() + 2000).toISOString(), timezone: "UTC", missed_policy: "run_once" })).routine;
    assert.equal(routine.binding.status, "active");
    await personal.disconnect(); const ownerPID = personal.child.pid;
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "offline-routine" && call.kind === "agent", "offline scheduler admission", 45_000);
    assert.equal(personal.child.exitCode, null); assert.equal(personal.connected, false);
    assert.equal(agentCalls("offline-routine").length, 1); assert(!agentCalls("offline-routine")[0].cancelled);
    held.get("offline-routine")();
    await waitPersistedReply(main.main_session_id, "MOSAIC_REPLY:offline-routine", "offline-routine-persisted-history.json");
    await personal.reconnect();
    // A running reminder defers15s; a15s tick may precede retry_at by a few
    // milliseconds. Allow the following tick plus transport margin.
    let restored; const deadline = Date.now() + 45_000;
    do {
      restored = (await personal.client("routines.list")).routines.find(item => item.routine_id === routine.routine_id);
      if (restored.status === "completed") break;
      await new Promise(resolve => setTimeout(resolve, 250));
    } while (Date.now() < deadline);
    assert.equal(restored.status, "completed"); assert.equal(agentCalls("offline-routine").length, 1);
    assert.equal((await personal.client("agents.ensure_default")).agent.main_session_id, main.main_session_id);
    return { routine_id: routine.routine_id, owner_pid: ownerPID, persisted_before_reconnect: true };
  });
  await scenario("Unconnected Calendar reports empty read-only context without implying availability", async () => {
    const status = await personal.client("calendar.status", { agent_id: main.agent_id });
    assert.equal(status.state, "unconnected"); assert.equal(status.read_only, true); assert.equal(status.freshness, "empty");
    const input = { agent_id: main.agent_id, calendar_id: "primary", time_min: "2026-10-18T00:00:00Z", time_max: "2026-10-19T00:00:00Z", timezone: "UTC" };
    const refreshed = await personal.client("calendar.refresh", input);
    assert.equal(refreshed.state, "unconnected"); assert.equal(refreshed.freshness, "empty"); assert.equal(refreshed.read_only, true); assert.deepEqual(refreshed.events, []);
    const agenda = await personal.client("calendar.agenda", input); assert.equal(agenda.state, "unconnected"); assert.equal(agenda.source, undefined);
    await assert.rejects(coding.client("calendar.status", {}, "personal-calendar"), /unavailable|does not accept|outside|capability/i);
    return { status, refreshed, no_external_calendar_account: true };
  });
  await scenario("Stale generation is rejected and disabling Personal preserves Coding", async () => {
    const item = personal.package("personal");
    await assert.rejects(personal.client("agents.list", {}, "personal", { fingerprint: "stale-generation" }), /fingerprint|changed|stale|generation.*active/i);
    const activeTask = (await personal.client("tasks.create", { agent_id: main.agent_id, request_id: "disable-running-task", title: "Stop on disable", goal: "MOSAIC_E2E_CASE:background-disable" })).task;
    await waitFor(providerEvents, "request", providerCalls, call => call.tag === "background-disable", "owner task provider admission before disable");
    await assert.rejects(personal.rpc("extension/package/update", { id: item.id, fingerprint: item.fingerprint, action: "disable" }), /user host client method is unavailable/);
    const ownerItem = coding.package("personal");
    const changed = await coding.rpc("extension/package/update", { id: ownerItem.id, fingerprint: ownerItem.fingerprint, action: "disable" });
    coding.initialized.extension_inventory = changed.extension_inventory;
    await waitFor(providerEvents, "cancelled", providerCalls, call => call.tag === "background-disable" && call.cancelled, "disabling Personal must cancel its active owner session", 8_000);
    await assert.rejects(personal.client("agents.list"), /inactive|disabled|unavailable|not approved and enabled/i);
    const before = providerCalls.length;
    await assert.rejects(personal.rpc("turn/start", { thread_id: main.main_session_id, prompt: "MOSAIC_E2E_CASE:disabled-owner-bypass" }), /user host client method is unavailable/);
    assert.equal(providerCalls.length, before, "disabled owner admitted generic model inference");
    const notebook = await coding.client("memory.read", {}, "memory"); assert(JSON.stringify(notebook).includes(codingMarker));
    const sent = await coding.rpc("turn/start", { thread_id: codingThread, prompt: "MOSAIC_E2E_CASE:coding-after-disable" });
    await coding.settled({ thread_id: codingThread, turn_id: sent.turn.id });
    return { cancelled_execution_session_id: activeTask.execution.session_id };
  });
  for (const call of providerCalls.filter(call => !call.tag.startsWith("coding-"))) {
    for (const marker of privateMarkers) assert(!JSON.stringify(call).includes(marker), `${call.tag} leaked ${marker}`);
  }
  assert(!report.provider_error, report.provider_error);
  report.status = report.cases.some(item => item.status === "failed") ? "failed" : "passed";
  if (report.status === "failed") process.exitCode = 1;
} catch (error) { report.status = "failed"; report.error = error.stack; console.error(error.stack); process.exitCode = 1; }
finally {
  // Detach peers before closing the SDK owner they share, including failed runs.
  for (const host of [...active].sort((a, b) => Number(Boolean(b.owner)) - Number(Boolean(a.owner)))) await host.close().catch(error => { report.cleanup_error = String(error); host.child.kill("SIGKILL"); });
  provider.closeAllConnections(); await new Promise(resolve => provider.close(resolve));
  report.source_tree_end = await sourceSnapshot("end");
  report.source_tree_stable = report.source_tree_start?.sha256 === report.source_tree_end.sha256;
  report.finished_at = new Date().toISOString(); await save(); console.log(`Evidence: ${scratch}`);
}
