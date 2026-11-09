import assert from 'node:assert/strict';
import { toolService, type RuntimeHost } from './index.js';
const calls: unknown[] = [];
const failure = new Error('selected provider unsupported');
let fail = false;
const host = {call: async (method: string, params: unknown) => {
 calls.push({method,params}); if (fail) throw failure;
 return {found:true,name:'current_public_name',direct_only:true};
}} as RuntimeHost;
const service = toolService(host);
assert.deepEqual(await service.resolveOwn({tool_id:'review'}), {found:true,name:'current_public_name',direct_only:true});
assert.deepEqual(calls,[{method:'host.service.call',params:{service:'agent.tools',method:'resolve-own',params:{tool_id:'review'}}}]);
fail=true;
await assert.rejects(service.resolveOwn({tool_id:'review'}),error=>error===failure);
assert.equal(calls.length,2); // No fallback or retry under another provider.
