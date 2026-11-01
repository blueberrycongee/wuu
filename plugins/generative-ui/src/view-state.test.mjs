import assert from 'node:assert/strict';
import { test } from 'node:test';
import { build } from 'esbuild';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

// The client capability may only access bounded view snapshots for the owner.
// Failures: forged/non-owner callback, unrelated key, oversized or malformed
// value, extra input, unsupported method, or accidentally using another host.
test('owner-scoped client stores only generative view state', async t => {
  const dir = await mkdtemp(join(tmpdir(), 'wuu-genui-state-test-'));
  t.after(() => rm(dir, {recursive:true, force:true}));
  await build({entryPoints:[fileURLToPath(new URL('./index.ts', import.meta.url))], outfile:join(dir,'plugin.mjs'), bundle:true, platform:'node', format:'esm'});
  const plugin = (await import(pathToFileURL(join(dir,'plugin.mjs')).href)).default;
  assert(plugin.initialize().capabilities.some(x => x.id === 'plugin.client.request'));
  const values = new Map(), calls = [];
  let owner = true;
  const host = {async call(method, params) {
    calls.push({method,params});
    if (method === 'host.service.call') {
      assert.equal(params.service, 'host.personal');
      assert.equal(params.params.operation, 'owner.check');
      return owner ? {ok:true,value:{}} : {ok:false,error:{code:'forbidden'}};
    }
    assert.equal(params.scope, 'user');
    if (method === 'host.storage.get') return {value:values.get(params.key) ?? null};
    if (method === 'host.storage.set') { values.set(params.key,params.value); return {}; }
    throw new Error(`unexpected host method ${method}`);
  }};
  const signal = new AbortController().signal;
  const client = (method,input,capability='plugin.client.request') => plugin.invokeCapability({capability,input:{method,input}},host,{signal});
  const key = `ui-v2-${'a'.repeat(64)}`, value = JSON.stringify({version:2,values:{people:4}});
  assert.deepEqual((await client('view-state.get',{key})).output.result,{value:null});
  assert.deepEqual((await client('view-state.set',{key,value})).output.result,{});
  assert.deepEqual((await client('view-state.get',{key})).output.result,{value});
  const sourceKey = `ui-v3-${"b".repeat(64)}`, sourceValue = JSON.stringify({version:3,values:{people:4},components:{}});
  await client("view-state.set",{key:sourceKey,value:sourceValue});
  assert.deepEqual((await client("view-state.get",{key:sourceKey})).output.result,{value:sourceValue});
  owner = false;
  const before = calls.filter(call => call.method.startsWith('host.storage.')).length;
  await assert.rejects(() => client('view-state.get',{key}));
  await assert.rejects(() => client('view-state.set',{key,value}));
  assert.equal(calls.filter(call => call.method.startsWith('host.storage.')).length,before);
  owner = true;
  for (const input of [{key:'../other'}, {key:`ui-v2-${'A'.repeat(64)}`}, {key,extra:true}]) await assert.rejects(() => client('view-state.get',input));
  for (const input of [{key,value:'not json'}, {key,value:' '.repeat(16449)}, {key,value,extra:true}]) await assert.rejects(() => client('view-state.set',input));
  await assert.rejects(() => client('view-state.delete',{key}));
  await assert.rejects(() => client('view-state.get',{key},'other.capability'));
});
