// Exercise a real deployment. Use a disposable server: creates two test accounts.
import assert from 'node:assert/strict';
import { generateKeyPairSync, randomBytes, sign } from 'node:crypto';
import fs from 'node:fs/promises';

const [phase, server, statePath] = process.argv.slice(2);
if (!['create', 'verify', 'revoke'].includes(phase) || !server || !statePath) {
  throw new Error('Usage: node deploy/remote/verify.mjs create|verify|revoke https://server /private/test-state.json');
}
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
  const parts = [Buffer.from('wuu/account/enroll/v1:' + username), pub, Buffer.from(role)];
  const message = Buffer.concat([Buffer.from('wuu/relay/auth/v1\0'), ...parts.flatMap(part => {
    const length = Buffer.alloc(4); length.writeUInt32BE(part.length); return [length, part];
  })]);
  return request('POST', register ? '/register' : '/login', '', {
    username, password, role, name: `Deployment test ${role}`, pub: pub.toString('base64url'),
    proof: sign(null, message, privateKey).toString('base64url'),
  });
}
if (phase === 'create') {
  const suffix = randomBytes(6).toString('hex');
  const password = randomBytes(24).toString('base64url');
  const host = await enroll('test_a_' + suffix, password, 'host', true);
  const phone = await enroll(host.username, password, 'phone', false);
  const other = await enroll('test_b_' + suffix, password, 'phone', true);
  await fs.writeFile(statePath, JSON.stringify({ host, phone, other }), { mode: 0o600, flag: 'wx' });
  console.log('PASS: registered two isolated accounts and enrolled a second device; private state saved');
} else {
  const { host, phone, other } = JSON.parse(await fs.readFile(statePath, 'utf8'));
  const a = await request('GET', '/devices', phone.token);
  const b = await request('GET', '/devices', other.token);
  assert.deepEqual(a.devices.map(d => d.pub).sort(), [host.pub, phone.pub].sort());
  assert.deepEqual(b.devices.map(d => d.pub), [other.pub]);
  await request('DELETE', '/devices/' + encodeURIComponent(host.pub), other.token, undefined, 401);
  assert.equal((await request('GET', '/devices', host.token)).devices.length, 2);
  if (phase === 'revoke') {
    await request('DELETE', '/devices/' + encodeURIComponent(phone.pub), host.token);
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
