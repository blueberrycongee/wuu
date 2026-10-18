// Real bundled process and JSONL protocol; storage and sessions are synthetic.
// This does not connect an account or perform a remote calendar operation.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';

async function run(domain) {
  const runtime = fileURLToPath(new URL('../../../internal/plugin/bundled/personal-calendar/runtime.mjs', import.meta.url));
  const child = spawn(process.execPath, [runtime], { stdio: ['pipe', 'pipe', 'pipe'] });
  let sequence = 0, stderr = ''; const pending = new Map(), frames = [], values = new Map();
  child.stderr.on('data', data => { stderr += data; });
  const lines = createInterface({ input: child.stdout });
  function send(frame) { child.stdin.write(`${JSON.stringify(frame)}\n`); }
  const exit = new Promise((resolve, reject) => { child.on('error', reject); child.on('exit', (code, signal) => { for (const waiter of pending.values()) waiter.reject(new Error(`runtime exited ${code}/${signal}: ${stderr}`)); resolve({ code, signal }); }); });
  lines.on('line', line => {
    const frame = JSON.parse(line); frames.push(frame);
    if (!frame.method) { const waiter = pending.get(frame.id); if (waiter) { pending.delete(frame.id); frame.error ? waiter.reject(new Error(frame.error.message)) : waiter.resolve(frame.result); } return; }
    try {
      assert.equal(frame.method, 'host.service.call'); const { service, params } = frame.params; const key = `${params.scope}:${params.key}`;
      let result;
      if (service === 'host.storage.get') result = { value: values.get(key) ?? null };
      else if (service === 'host.storage.compare-exchange') { const value = values.get(key) ?? null; if (value === params.expected) { values.set(key, params.value); result = { swapped: true, value: params.value }; } else result = { swapped: false, value }; }
      else assert.fail(`unexpected host effect: ${service}`);
      send({ id: frame.id, result });
    } catch (error) { send({ id: frame.id, error: { message: error.message } }); }
  });
  const request = (method, params) => new Promise((resolve, reject) => { const id = `synthetic-${++sequence}`; pending.set(id, { resolve, reject }); send({ id, method, params }); });
  try {
    const definition = await request('initialize', { protocol_version: 1, capability_protocol_version: 3, lifecycle_version: 1, plugin_id: 'personal-calendar', plugin_root: '.', project_root: '', wuu_home: '/synthetic', data_root: '/synthetic/personal', user_space_id: 'protocol-user', product_domain: domain, supported_host_services: ['host.service.call'] });
    await request('activate');
    if (domain === 'personal') {
      assert.ok(definition.provided_services.some(item => item.name === 'calendar.personal'));
      const snapshot = await request('service.invoke', { service: 'calendar.personal', method: 'refresh', caller: 'kernel', execution_id: 'synthetic-read', params: { calendar_id: 'primary', time_min: '2026-10-18T00:00:00Z', time_max: '2026-10-19T00:00:00Z', timezone: 'UTC' } });
      assert.equal(snapshot.state, 'unconnected'); assert.equal(snapshot.error_code, 'authorization_not_configured'); assert.equal(snapshot.freshness, 'empty'); assert.deepEqual(snapshot.events, []);
    } else { assert.deepEqual(definition.tools, []); assert.deepEqual(definition.provided_services, []); assert.equal(frames.filter(frame => frame.method).length, 0); }
    await request('shutdown'); child.stdin.end(); const stopped = await exit; assert.equal(stopped.code, 0, stderr);
  } finally { lines.close(); if (child.exitCode === null) child.kill(); }
}

test('bundled runtime speaks scoped JSONL and reports the real unauthenticated state', { timeout: 10_000 }, async () => { await run('personal'); await run('coding'); });
