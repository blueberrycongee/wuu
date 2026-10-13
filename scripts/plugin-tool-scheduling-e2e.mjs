#!/usr/bin/env node
// Real Go/TypeScript SDK subprocesses over full-duplex JSONL. No inference,
// credentials, desktop, or user's Wuu home. Retain results.json and wire traces.
// Build the TypeScript SDK first; use GO and WUU_E2E_RACE=1 for another toolchain.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createInterface } from "node:readline";

const root = resolve(process.env.WUU_E2E_SOURCE_ROOT ?? join(dirname(fileURLToPath(import.meta.url)), ".."));
const output = resolve(process.env.WUU_E2E_OUTPUT ?? join(root, ".tmp/plugin-tool-scheduling"));
await mkdir(output, { recursive: true });
const goSource = String.raw`package main
import (
 "context"
 "encoding/json"
 "os"
 "sync"
 plugin "github.com/blueberrycongee/wuu/packages/plugin-go"
)
func main() {
 var host plugin.Host
 var mu sync.Mutex
 active, maximum := 0, 0
 events := []string{}
 gate := func(ctx context.Context, h plugin.Host, label string) error {
  mu.Lock(); active++; if active > maximum { maximum = active }; events = append(events, "start:"+label); mu.Unlock()
  defer func(){ mu.Lock(); active--; events = append(events, "end:"+label); mu.Unlock() }()
  var out any
  if err := plugin.CallService(ctx, h, "fixture.gate", "call", map[string]any{"label":label}, &out); err != nil { return err }
  return plugin.CallService(ctx, h, "fixture.trace", "call", map[string]any{"label":label}, &out)
 }
 err := plugin.Serve(context.Background(), plugin.Handler{
  Definition: plugin.Definition{Tools: []plugin.Tool{
   {ID:"safe", Activity:&plugin.ToolActivity{ConcurrencySafe:true}},
   {ID:"default"}, {ID:"readonly", Activity:&plugin.ToolActivity{ReadOnly:true}},
   {ID:"unsafe", Activity:&plugin.ToolActivity{ConcurrencySafe:false}},
  }},
  Initialize: func(ctx context.Context, h plugin.Host, _ plugin.InitializeParams) error {
   host = h
   if os.Getenv("FIXTURE_MODE") == "initialize" { return gate(ctx, h, "initialize") }; return nil
  },
  Activate: func(ctx context.Context) error {
   if os.Getenv("FIXTURE_MODE") == "activate" { return gate(ctx, host, "activate") }; return nil
  },
  ExecuteTool: func(ctx context.Context, h plugin.Host, call plugin.ToolCall)(plugin.ToolResult,error){
   if err := gate(ctx,h,call.CallID); err != nil { return plugin.ToolResult{},err }
   return plugin.TextResult(call.ExecutionID),nil
  },
  InvokeCapability: func(ctx context.Context,h plugin.Host,call plugin.CapabilityCall)(json.RawMessage,error){
   if call.Capability == "model.adapter" {
    mu.Lock(); defer mu.Unlock(); return json.Marshal(map[string]any{"active":active,"maximum":maximum,"events":events})
   }
   if err := gate(ctx,h,"capability"); err != nil { return nil,err }; return json.RawMessage("{}"),nil
  },
  InvokeService: func(ctx context.Context,h plugin.Host,call plugin.ServiceCall)(json.RawMessage,error){
   if err := gate(ctx,h,"service"); err != nil { return nil,err }; return json.RawMessage("{}"),nil
  },
  Shutdown: func(ctx context.Context)error { return gate(ctx,host,"shutdown") },
 })
 if err != nil { panic(err) }
}
`;
const tsSource = `import { runJSONLRuntime } from ${JSON.stringify(pathToFileURL(join(root, "packages/plugin-sdk/dist/index.js")).href)};
let active = 0, maximum = 0;
const events = [];
async function gate(host, label, signal) {
  active++; maximum = Math.max(maximum, active); events.push("start:"+label);
  let abort;
  try {
    const result = host.call("host.service.call", {service:"fixture.gate",method:"call",params:{label}});
    if (signal) {
      signal.throwIfAborted();
      await Promise.race([result, new Promise((_,reject) => {
        abort = () => reject(new Error("execution cancelled"));
        signal.addEventListener("abort", abort, {once:true});
      })]);
    } else await result;
    await host.call("host.service.call", {service:"fixture.trace",method:"call",params:{label}});
  } finally {
    if (abort) signal.removeEventListener("abort", abort);
    active--; events.push("end:"+label);
  }
}
let host;
await runJSONLRuntime({
  concurrentServices: ["fixture.reentrant"],
  async initialize(_params,h) {
    host=h;
    if (process.env.FIXTURE_MODE === "initialize") await gate(h,"initialize");
    return {protocol_version:3,tools:[
      {id:"safe",description:"safe",input_schema:{type:"object"},activity:{concurrency_safe:true}},
      {id:"default",description:"default",input_schema:{type:"object"}},
      {id:"readonly",description:"readonly",input_schema:{type:"object"},activity:{read_only:true}},
      {id:"unsafe",description:"unsafe",input_schema:{type:"object"},activity:{concurrency_safe:false}},
    ]};
  },
  async activate(h) { if (process.env.FIXTURE_MODE === "activate") await gate(h,"activate"); },
  async executeTool(call,h,execution) {
    await gate(h,call.call_id,execution.signal);
    return {result:{content:[{type:"text",text:execution.executionId}]}};
  },
  async invokeCapability(call,h,execution) {
    if (call.capability === "model.adapter") return {output:{active,maximum,events:[...events]}};
    await gate(h,"capability",execution.signal); return {output:{}};
  },
  async invokeService(call,h,execution) {
    if (call.service === "fixture.reentrant") return {execution:execution.executionId};
    await gate(h,"service",execution.signal); return {};
  },
  async shutdown() { await gate(host,"shutdown"); },
},{input:process.stdin,output:process.stdout});
`;
await writeFile(join(output, "fixture.go"), goSource);
await writeFile(join(output, "fixture.mjs"), tsSource);
const binary = join(output, "fixture-go");
const built = spawnSync(process.env.GO ?? "go", ["build", ...(process.env.WUU_E2E_RACE === "1" ? ["-race"] : []), "-o", binary, join(output, "fixture.go")], {cwd:root,encoding:"utf8",env:process.env});
assert.equal(built.status, 0, built.stderr);

class Peer {
  frames = []; waiters = []; stderr = ""; sequence = 0;
  constructor(sdk, mode = "normal") {
    const entry = ["subagent", "peers"].includes(mode) ? join(root, "internal/plugin/bundled", mode, "runtime.mjs") : join(output, "fixture.mjs");
    this.child = spawn(sdk === "go" ? binary : process.execPath, sdk === "go" ? [] : [entry], {
      cwd:root, env:{PATH:process.env.PATH,FIXTURE_MODE:mode}, stdio:["pipe","pipe","pipe"],
    });
    this.closed = new Promise(resolve => this.child.on("close", (code,signal) => {
      this.exit = {code,signal};
      for (const waiter of this.waiters.splice(0)) waiter.reject(new Error(`runtime exited: ${code}/${signal}: ${this.stderr}`));
      resolve(this.exit);
    }));
    this.child.stderr.on("data", chunk => this.stderr += chunk);
    this.child.stdin.on("error", () => {});
    createInterface({input:this.child.stdout}).on("line", line => {
      const frame = JSON.parse(line); this.frames.push(frame);
      if (frame.method === "host.service.call" && frame.params.service === "fixture.trace") this.reply(frame);
      this.onFrame?.(frame);
      for (const waiter of [...this.waiters]) if (waiter.predicate(frame)) {
        this.waiters.splice(this.waiters.indexOf(waiter),1); waiter.resolve(frame);
      }
    });
  }
  send(frame) { this.child.stdin.write(JSON.stringify(frame)+"\n"); }
  wait(predicate) {
    const found = this.frames.find(predicate); if (found) return Promise.resolve(found);
    if (this.exit) return Promise.reject(new Error(`already exited: ${JSON.stringify(this.exit)}`));
    const result = new Promise((resolve,reject) => this.waiters.push({predicate,resolve,reject}));
    // Requests may be intentionally queued while a gate is held. Keep exit
    // failures handled until the scenario reaches that response's assertion.
    void result.catch(() => {});
    return result;
  }
  request(method,params,id=`request-${++this.sequence}`) {
    this.send({id,method,params}); return this.wait(frame => !frame.method && frame.id === id);
  }
  tool(label,tool="safe",execution_id=label,args={}) {
    return this.request("tool.execute", {tool_id:tool,tool,call_id:label,execution_id,arguments:args,cwd:output,session_id:"parent",turn_id:"parent-turn"},label);
  }
  gate(label) { return this.wait(frame => frame.method && frame.params?.service === "fixture.gate" && frame.params.params.label === label); }
  reply(frame,result={}) { this.send({id:frame.id,result}); }
  cancel(execution_id) { this.send({id:`cancel-${++this.sequence}`,method:"execution.cancel",params:{execution_id}}); }
  probe() { return this.request("capability.invoke", {capability:"model.adapter",input:{},output:{},execution_id:`probe-${++this.sequence}`}).then(frame => frame.result.output); }
  async start() {
    const response = await this.request("initialize", {protocol_version:1,capability_protocol_version:3,plugin_id:"fixture",wuu_home:output,project_root:output,supported_host_services:["host.service.call"]});
    assert.ok(!response.error,JSON.stringify(response));
    assert.ok(!(await this.request("activate")).error);
  }
  async stop() {
    const stopped = this.request("shutdown"); this.reply(await this.gate("shutdown"));
    assert.ok(!(await stopped).error); this.child.stdin.end();
    assert.deepEqual(await this.closed,{code:0,signal:null});
  }
}
const results = {sourceRoot:root,race:process.env.WUU_E2E_RACE === "1",cases:[]};
async function scenario(sdk,name,body,mode) {
  if (process.env.WUU_E2E_CASE && !new RegExp(process.env.WUU_E2E_CASE).test(`${sdk}/${name}`)) return;
  const peer = new Peer(sdk,mode);
  const watchdog = setTimeout(() => peer.child.kill("SIGKILL"),15000);
  try {
    await body(peer);
    results.cases.push({sdk,name,passed:true,frames:peer.frames,stderr:peer.stderr});
    console.log(`PASS ${sdk}/${name}`);
  } catch(error) {
    results.cases.push({sdk,name,passed:false,error:String(error.stack ?? error),frames:peer.frames,stderr:peer.stderr});
    console.error(`FAIL ${sdk}/${name}: ${error}`);
  } finally {
    clearTimeout(watchdog);
    if (!peer.exit) { peer.child.kill("SIGKILL"); await peer.closed; }
    await writeFile(join(output,"results.json"),JSON.stringify(results,null,2));
  }
}
for (const sdk of ["go","ts"]) {
  await scenario(sdk,"bounded safe tools, queued cancellation, execution isolation",async p => {
    await p.start();
    const calls = Array.from({length:12},(_,i) => p.tool(`pool-${i}`));
    const first = await Promise.all(Array.from({length:8},(_,i) => p.gate(`pool-${i}`)));
    const snapshot = await p.probe(); assert.equal(snapshot.active,8); assert.equal(snapshot.maximum,8);
    p.cancel("pool-10"); await p.probe();
    first.forEach(frame => p.reply(frame));
    for (const i of [8,9,11]) p.reply(await p.gate(`pool-${i}`));
    const responses = await Promise.all(calls);
    assert.ok(responses[10].error); assert.ok(responses.every((r,i) => i === 10 || !r.error));
    assert.ok(!p.frames.some(f => f.method && f.params?.params?.label === "pool-10"));
    const trace = p.frames.filter(f => f.method && f.params.service === "fixture.trace");
    assert.equal(trace.length,11);
    assert.ok(trace.every(f => f.params.execution_id === f.params.params.label));
    assert.equal((await p.probe()).maximum,8);
    await p.stop();
  });
  for (const shared of [false,true]) for (const exclusive of ["default","readonly","unsafe","unknown","service","capability"]) {
    await scenario(sdk,`FIFO ${exclusive} barrier with ${shared ? "shared" : "independent"} execution IDs`,async p => {
      await p.start();
      const first = p.tool("first","safe",shared ? "shared" : "first"); const firstGate = await p.gate("first");
      const middle = exclusive === "service" ? p.request("service.invoke",{service:"fixture.ordinary",method:"call",execution_id:shared ? "shared" : "middle"})
        : exclusive === "capability" ? p.request("capability.invoke",{capability:"ordinary",input:{},execution_id:shared ? "shared" : "middle"})
        : p.tool("middle",exclusive,shared ? "shared" : "middle");
      const label = ["service","capability"].includes(exclusive) ? exclusive : "middle";
      const last = p.tool("last","safe",shared ? "shared" : "last");
      const before = await p.probe(); assert.deepEqual(before.events,["start:first"]);
      p.reply(firstGate); await first;
      const gate = await p.gate(label);
      assert.deepEqual((await p.probe()).events,["start:first","end:first",`start:${label}`]);
      p.reply(gate); await middle;
      p.reply(await p.gate("last")); assert.ok(!(await last).error);
      assert.equal((await p.probe()).maximum,1); await p.stop();
    });
  }
  await scenario(sdk,"shared execution cancellation and late-ID reuse",async p => {
    await p.start();
    const calls = [p.tool("shared-a","safe","shared"),p.tool("shared-b","safe","shared"),p.tool("independent")];
    const gates = await Promise.all([p.gate("shared-a"),p.gate("shared-b"),p.gate("independent")]);
    p.cancel("shared"); await p.probe();
    const cancelled = await Promise.all(calls.slice(0,2)); assert.ok(cancelled.every(r => r.error));
    assert.equal((await p.probe()).active,1);
    gates.forEach(frame => p.reply(frame)); assert.ok(!(await calls[2]).error);
    p.cancel("shared"); await p.probe();
    const fresh = p.tool("fresh","safe","shared"); p.reply(await p.gate("fresh")); assert.ok(!(await fresh).error);
    await p.stop();
  });
  for (const barrier of ["initialize","activate"]) {
    await scenario(sdk,`${barrier} publishes before tools and drains before shutdown`,async p => {
      const init = p.request("initialize", {protocol_version:1,plugin_id:"fixture",supported_host_services:["host.service.call"]});
      if (barrier === "activate") await init;
      const activation = p.request("activate");
      const before = await p.gate(barrier);
      const first = p.tool("after-a"); const second = p.tool("after-b");
      p.reply(before); await Promise.all([init,activation]);
      const gates = await Promise.all([p.gate("after-a"),p.gate("after-b")]);
      const closing = p.request("shutdown");
      gates.forEach(frame => p.reply(frame)); await Promise.all([first,second]);
      const cleanup = await p.gate("shutdown");
      assert.ok(p.frames.findIndex(f => f.id === "after-a" && !f.method) < p.frames.indexOf(cleanup));
      assert.ok(p.frames.findIndex(f => f.id === "after-b" && !f.method) < p.frames.indexOf(cleanup));
      p.reply(cleanup); await closing; p.child.stdin.end(); assert.equal((await p.closed).code,0);
    },barrier);
  }
  if (sdk === "ts") await scenario(sdk,"reentrant service bypasses a saturated tool pool",async p => {
    await p.start();
    const calls = Array.from({length:8},(_,i) => p.tool(`outer-${i}`,"safe","chain"));
    const gates = await Promise.all(calls.map((_,i) => p.gate(`outer-${i}`)));
    const callback = await p.request("service.invoke",{service:"fixture.reentrant",method:"call",execution_id:"chain"});
    assert.equal(callback.result.execution,"chain");
    gates.forEach(f => p.reply(f)); await Promise.all(calls); await p.stop();
  });
}

await scenario("ts","bundled subagent foreground overlap and stale-result protection",async p => {
  let stored = null, sequence = 0;
  const sessions = [], pending = [];
  p.onFrame = frame => {
    if (frame.method !== "host.service.call") return;
    const {service,params} = frame.params;
    switch(service) {
      case "host.storage.get": p.reply(frame,{value:stored}); break;
      case "host.storage.keys": p.reply(frame,{keys:[]}); break;
      case "host.storage.set": stored = params.value; p.reply(frame); break;
      case "host.session.create": {
        const session_id = `child-${++sequence}`;
        sessions.push({session_id,parent_session_id:"parent",name:params.name,state:"running"});
        p.reply(frame,{session_id}); break;
      }
      case "host.session.send": p.reply(frame,{state:"running",turn_id:params.request_id,steered:false}); break;
      case "host.session.list": p.reply(frame,{sessions}); break;
      case "host.session.inspect": pending.push(frame); break;
      default: throw new Error(`unexpected service ${service}`);
    }
  };
  await p.start();
  const first = p.tool("spawn-a","spawn_agent","spawn-a",{name:"one",description:"one",prompt:"synthetic",run_in_background:false});
  await p.wait(f => f.method && f.params.service === "host.session.inspect" && f.params.params.session_id === "child-1");
  const second = p.tool("spawn-b","spawn_agent","spawn-b",{name:"two",description:"two",prompt:"synthetic",run_in_background:false});
  await p.wait(f => f.method && f.params.service === "host.session.inspect" && f.params.params.session_id === "child-2");
  const updated = await p.tool("followup","send_message","followup",{target:"child-1",message:"new task"});
  assert.ok(!updated.error,JSON.stringify(updated));
  const expected = JSON.parse(stored).records.find(r => r.session_id === "child-1").request_id;
  for (const frame of pending) p.reply(frame,{session:{session_id:frame.params.params.session_id,state:"completed"},turn:{request_id:frame.params.params.request_id,turn_id:"old-turn",state:"completed",final_output:"done"}});
  assert.ok((await Promise.all([first,second])).every(r => !r.error));
  const actual = JSON.parse(stored).records.find(r => r.session_id === "child-1");
  assert.equal(actual.request_id,expected); assert.equal(actual.state,"running"); assert.ok(!actual.suppress_completion);
  assert.ok(!(await p.request("shutdown")).error); p.child.stdin.end(); assert.equal((await p.closed).code,0);
},"subagent");
await scenario("ts","bundled peer queries overlap without execution leakage",async p => {
  p.onFrame = frame => {
    if (frame.method === "host.service.call" && frame.params.service === "host.storage.get") p.reply(frame,{value:null});
  };
  await p.start();
  const first = p.tool("peers-a","list_peers");
  const a = await p.wait(f => f.method && f.params.service === "host.session.list" && f.params.execution_id === "peers-a");
  const second = p.tool("peers-b","list_peers");
  const b = await p.wait(f => f.method && f.params.service === "host.session.list" && f.params.execution_id === "peers-b");
  p.reply(b,{sessions:[{session_id:"parent"},{session_id:"second"}]});
  assert.equal(JSON.parse((await second).result.result.content[0].text).peers[0].session_id,"second");
  p.reply(a,{sessions:[{session_id:"parent"},{session_id:"first"}]});
  assert.equal(JSON.parse((await first).result.result.content[0].text).peers[0].session_id,"first");
  assert.ok(!(await p.request("shutdown")).error); p.child.stdin.end(); assert.equal((await p.closed).code,0);
},"peers");
if (results.cases.some(c => !c.passed)) process.exitCode = 1;
