// Exercise a real deployment. Use a disposable server: creates two test accounts.
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import fs from 'node:fs/promises';
import { authProof, probeRelay } from './relay-probe.mjs';

const [phase, address, statePath] = process.argv.slice(2);
if (!['create', 'verify', 'relay', 'revoke'].includes(phase) || !address || !statePath) {
  throw new Error('Usage: node deploy/remote/verify.mjs create|verify|relay|revoke https://server /private/test-state.json');
}
const origin = new URL(address);
assert.ok(!origin.username && !origin.password && !origin.search && !origin.hash && origin.pathname === '/', 'Server must be an origin without credentials, path, query or fragment');
assert.ok(origin.protocol === 'https:' || (origin.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(origin.hostname)), 'Use HTTPS outside loopback');
const server = origin.origin;
const ready = await fetch(server + '/readyz', { redirect: 'error', signal: AbortSignal.timeout(15000) });
assert.equal(ready.status, 200, 'Server or account database is not ready');
assert.equal(await ready.text(), 'ok', 'Readiness endpoint did not reach the relay');
async function request(method, path, token, body, status = 200) {
  const response = await fetch(server + '/v1/account' + path, {
    method, redirect: 'error', signal: AbortSignal.timeout(15000),
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  assert.equal(response.status, status, `${method} ${path}: unexpected status`);
  return response.json();
}
async function enroll(username, password, role, register) {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const pub = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
  const session = await request('POST', register ? '/register' : '/login', '', {
    username, password, role, name: `Deployment test ${role}`, pub: pub.toString('base64url'),
    proof: authProof(privateKey, Buffer.from('wuu/account/enroll/v1:' + username), pub.toString('base64url'), role),
  });
  return { ...session, privateKey: privateKey.export({ format: 'pem', type: 'pkcs8' }) };
}
if (phase === 'create') {
  const suffix = randomBytes(6).toString('hex');
  const password = randomBytes(24).toString('base64url');
  const host = await enroll('test_a_' + suffix, password, 'host', true);
  const phone = await enroll(host.username, password, 'phone', false);
  const other = await enroll('test_b_' + suffix, password, 'host', true);
  await fs.writeFile(statePath, JSON.stringify({ server, host, phone, other }), { mode: 0o600, flag: 'wx' });
  console.log('PASS: registered two isolated accounts and enrolled a second device; private state saved');
} else {
  const state = JSON.parse(await fs.readFile(statePath, 'utf8'));
  assert.equal(state.server, server, 'Test state is not bound to this server; create new disposable test accounts');
  const { host, phone, other } = state;
  const a = await request('GET', '/devices', phone.token);
  const b = await request('GET', '/devices', other.token);
  assert.deepEqual(a.devices.map(d => d.pub).sort(), [host.pub, phone.pub].sort());
  assert.deepEqual(b.devices.map(d => d.pub), [other.pub]);
  await request('DELETE', '/devices/' + encodeURIComponent(host.pub), other.token, undefined, 401);
  assert.equal((await request('GET', '/devices', host.token)).devices.length, 2);
  if (phase === 'relay') {
    await probeRelay(server, state);
    console.log('PASS: WebSocket upgrade, device authentication, bidirectional 256 KiB relay and cross-account denial');
  }
  if (phase === 'revoke') {
    await probeRelay(server, state, () => request('DELETE', '/devices/' + encodeURIComponent(phone.pub), host.token));
    await request('GET', '/devices', phone.token, undefined, 401);
    const reset = await request('POST', '/recover', '', {
      username: host.username, secret: host.recovery, password: randomBytes(24).toString('base64url'),
    });
    assert.ok(reset.recovery && reset.recovery !== host.recovery);
    await request('GET', '/devices', host.token, undefined, 401);
    await request('POST', '/recover', '', {
      username: host.username, secret: host.recovery, password: randomBytes(24).toString('base64url'),
    }, 401);
    assert.equal((await request('GET', '/devices', other.token)).devices.length, 1);
  }
  console.log(`PASS: ${phase === 'revoke' ? 'device revocation, single-use recovery and account isolation' : 'persisted credentials, device directory and cross-account denial'}`);
}
