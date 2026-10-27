import assert from 'node:assert/strict';
import { delegateService, type RuntimeExecutionContext, type RuntimeHost, type ServiceDescriptor } from './index.js';

// Public helper failures: typo overrides, undeclared methods, hidden fallback on
// errors, copied upstream authority, or forwarding through a cached root host.
const descriptor: ServiceDescriptor = {name:'example.store', version:'1.0.0', methods:['read','write'].map(name => ({name,input_schema:`example.store.${name}.input.v1`,output_schema:`example.store.${name}.output.v1`}))};
const calls: unknown[] = [];
const failure = new Error('delegate unavailable');
let fail = false;
const host = {call: async (method: string, params: unknown) => {calls.push({method,params}); if (fail) throw failure; return {saved:true};}} as RuntimeHost;
const execution = {signal:new AbortController().signal} as RuntimeExecutionContext;
const invoke = delegateService(descriptor, {read: (input, actualHost, actualExecution) => {
  assert.equal(actualHost,host); assert.equal(actualExecution,execution);
  if (input.params === 'fail') throw failure;
  return {changed:true};
}});
assert.deepEqual(await invoke({service:'example.store',method:'read',caller:'upstream'},host,execution),{changed:true});
assert.equal(calls.length,0);
assert.deepEqual(await invoke({service:'example.store',method:'write',caller:'upstream',execution_id:'untrusted-upstream-copy',params:{value:1}},host,execution),{saved:true});
assert.deepEqual(calls,[{method:'host.service.call',params:{service:'example.store',method:'write',params:{value:1}}}]);
await assert.rejects(async () => invoke({service:'example.store',method:'read',caller:'upstream',params:'fail'},host,execution),error => error===failure);
assert.equal(calls.length,1,'override failure must not run base');
fail=true;
await assert.rejects(async () => invoke({service:'example.store',method:'write',caller:'upstream'},host,execution),error => error===failure);
assert.equal(calls.length,2,'delegate failure must not retry');
assert.throws(() => delegateService(descriptor,{typo:()=>null}),/declare/);
await assert.rejects(async () => invoke({service:'other.store',method:'write',caller:'upstream'},host,execution),/declare/);
await assert.rejects(async () => invoke({service:'example.store',method:'unknown',caller:'upstream'},host,execution),/declare/);
