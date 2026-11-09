import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createInterface } from 'node:readline';
import { test } from 'node:test';

// A real provider process must preserve conflict and domain codes while an
// ordinary exception remains unclassified; no stack or incidental fields leak.
test('external service provider preserves structured errors over JSONL', async () => {
  const sdk = new URL('../dist/index.js', import.meta.url).href;
  const child = spawn(process.execPath, ['--input-type=module', '-e', `
    import { runJSONLRuntime, RuntimeHostServiceError } from ${JSON.stringify(sdk)};
    await runJSONLRuntime({
      initialize() { return { protocol_version: 3 }; },
      invokeService({method}) {
        if (method === 'conflict') throw new RuntimeHostServiceError({code:'state_conflict', message:'revision changed'});
        if (method === 'custom') throw Object.assign(new Error('domain unavailable'), {code:'domain_unavailable', internal:'do not serialize'});
        if (method === 'plain') throw new Error('provider failed');
        if (method === 'invalid') throw Object.assign(new Error('invalid code'), {code: {secret:'do not serialize'}});
        return {healthy:true};
      },
    }, {input:process.stdin, output:process.stdout});
  `], { stdio: ['pipe', 'pipe', 'pipe'] });
  const exited = once(child, 'exit');
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const lines = createInterface({ input: child.stdout });
  const responses = lines[Symbol.asyncIterator]();
  const request = async (id, method, params) => {
    child.stdin.write(`${JSON.stringify({id,method,params})}\n`);
    const { value, done } = await responses.next();
    assert.equal(done, false, stderr);
    const result = JSON.parse(value);
    assert.equal(result.id, id);
    return result;
  };
  const watchdog = setTimeout(() => child.kill(), 10000);
  try {
    assert.ok((await request('initialize', 'initialize', { supported_host_services: [] })).result);
    await request('activate', 'activate');
    assert.deepEqual((await request('conflict', 'service.invoke', {service:'state.documents',method:'conflict'})).error, {code:'state_conflict',message:'revision changed'});
    assert.deepEqual((await request('custom', 'service.invoke', {service:'domain',method:'custom'})).error, {code:'domain_unavailable',message:'domain unavailable'});
    assert.deepEqual((await request('plain', 'service.invoke', {service:'domain',method:'plain'})).error, {message:'provider failed'});
    assert.deepEqual((await request('invalid', 'service.invoke', {service:'domain',method:'invalid'})).error, {message:'invalid code'});
    assert.deepEqual((await request('healthy', 'service.invoke', {service:'domain',method:'healthy'})).result, {healthy:true});
    await request('shutdown', 'shutdown');
    child.stdin.end();
    const [code] = await exited;
    assert.equal(code, 0, stderr);
  } finally { clearTimeout(watchdog); child.kill(); lines.close(); }
});
