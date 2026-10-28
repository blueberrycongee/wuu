import assert from "node:assert/strict";
import test from "node:test";
import type { RuntimeHost } from "./index.js";
import { engineGateway, engineServiceDescriptor, type EngineRunResult } from "./engine.js";

test("engine callbacks keep partial receipts and terminal settlement independent of cancelled turn", async () => {
 const controller = new AbortController();
 const calls: {method: string; params: Record<string, unknown>; signal?: AbortSignal}[] = [];
 const host = {call: async (_method: string, input: unknown, options?: {signal?: AbortSignal}) => {
  const call = input as {method: string; params: Record<string, unknown>};
  calls.push({...call,signal:options?.signal});
  if (call.method === "run_loop") return {result:{version:1, content:"partial",new_messages:[],durable_new_messages:[]},failure:{message:"cancelled",origin:"host",category:"cancelled",context_overflow:false}} satisfies EngineRunResult;
  return {};
 }} as RuntimeHost;
 const gateway = engineGateway(host,"bound-resource",{signal:controller.signal});
 const result = await gateway.runLoop([]);
 assert.equal(result.result.content,"partial");
 assert.equal(result.failure?.message,"cancelled");
 await gateway.emit({version:1,type:"content_delta",content:"partial"});
 controller.abort();
 await gateway.settleTurn("native-turn");
 assert.equal(calls[0].params.gateway_id,"bound-resource");
 assert.equal(calls[1].signal,controller.signal);
 assert.notEqual(calls[2].signal,controller.signal);
 assert.equal(calls[2].method,"settle_turn");
 assert.equal(calls[2].params.reference,"native-turn");
 assert.equal(engineServiceDescriptor("external").name,"engine.external");
 assert.equal(engineServiceDescriptor("external").methods.find(x=>x.name==="close")?.cleanup,true);
});
