import assert from 'node:assert/strict';
import { randomBytes, sign } from 'node:crypto';

export function authProof(privateKey, nonce, pub, role) {
  const parts = [nonce, Buffer.from(pub, 'base64url'), Buffer.from(role)];
  const message = Buffer.concat([Buffer.from('wuu/relay/auth/v1\0'), ...parts.flatMap(part => {
    const length = Buffer.alloc(4); length.writeUInt32BE(part.length); return [length, part];
  })]);
  return sign(null, message, privateKey).toString('base64url');
}

export async function connectPeer(server, device, role, to, expected = 'auth_ok') {
  assert.ok(device.privateKey, 'Test state lacks device keys; create a new disposable test account');
  const url = new URL('/v1/connect', server);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(url);
  const queue = [];
  let waiting, failure;
  const fail = error => { failure = error; waiting?.reject(error); waiting = undefined; };
  const closed = new Promise(resolve => socket.addEventListener('close', () => {
    fail(new Error('Relay closed the connection')); resolve();
  }));
  socket.addEventListener('error', () => fail(new Error('Relay WebSocket failed')));
  socket.addEventListener('message', event => {
    try {
      const value = JSON.parse(event.data);
      if (waiting) { const pending = waiting; waiting = undefined; pending.resolve(value); }
      else { assert.ok(queue.length < 64, 'Unexpected relay message flood'); queue.push(value); }
    } catch (error) { fail(error); socket.close(); }
  });
  const peer = {
    closed,
    close: () => socket.close(),
    send: value => socket.send(JSON.stringify(value)),
    async next(type) {
      const deadline = Date.now() + 15000;
      while (true) {
        let value = queue.shift();
        if (!value) {
          if (failure) throw failure;
          value = await new Promise((resolve, reject) => {
            const timer = setTimeout(() => { waiting = undefined; reject(new Error(`Timed out waiting for relay ${type}`)); }, Math.max(0, deadline - Date.now()));
            waiting = {
              resolve: value => { clearTimeout(timer); resolve(value); },
              reject: error => { clearTimeout(timer); reject(error); },
            };
          });
        }
        if (value.type === type) return value;
        assert.equal(value.type, 'presence', `Unexpected relay message while waiting for ${type}`);
        assert.ok(Date.now() < deadline, `Timed out waiting for relay ${type}`);
      }
    },
  };
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => done(new Error('WebSocket upgrade timed out')), 15000);
      const opened = () => done();
      const failed = () => done(new Error('WebSocket upgrade failed'));
      function done(error) {
        clearTimeout(timer);
        socket.removeEventListener('open', opened);
        socket.removeEventListener('error', failed);
        socket.removeEventListener('close', failed);
        error ? reject(error) : resolve();
      }
      socket.addEventListener('open', opened);
      socket.addEventListener('error', failed);
      socket.addEventListener('close', failed);
    });
    peer.send({ type: 'hello', proto: 1, role, pub: device.pub, ...(to ? { to } : {}) });
    const challenge = await peer.next('challenge');
    const nonce = Buffer.from(challenge.nonce, 'base64url');
    assert.equal(nonce.length, 32);
    peer.send({ type: 'auth', sig: authProof(device.privateKey, nonce, device.pub, role) });
    const result = await peer.next(expected);
    if (expected === 'auth_ok' && to) assert.equal(result.online, true, 'Test host is not online');
    return peer;
  } catch (error) { peer.close(); throw error; }
}

// The relay is an opaque switchboard. These disposable payloads test forwarding,
// not Agent execution or the end-to-end handshake (covered by native integration tests).
export async function probeRelay(server, { host, phone, other }, revoke) {
  const peers = [];
  async function connect(...args) { const peer = await connectPeer(server, ...args); peers.push(peer); return peer; }
  try {
    const desktop = await connect(host, 'host');
    const mobile = await connect(phone, 'phone', host.pub);
    for (const [sender, receiver, from, to] of [[mobile, desktop, phone.pub, host.pub], [desktop, mobile, host.pub, phone.pub]]) {
      const payload = randomBytes(256 * 1024).toString('base64url');
      sender.send({ type: 'frame', to, payload });
      const received = await receiver.next('frame');
      assert.equal(received.from, from);
      assert.ok(received.payload === payload, 'Relay changed the payload');
    }
    const foreign = await connect(other, 'host');
    foreign.send({ type: 'frame', to: phone.pub, payload: 'probe' });
    assert.equal((await foreign.next('deliver_err')).code, 'unauthorized');
    if (revoke) {
      await revoke();
      let timer;
      try {
        await Promise.race([mobile.closed, new Promise((_, reject) => {
          timer = setTimeout(() => reject(new Error('Revoked phone WebSocket remained open')), 15000);
        })]);
      } finally { clearTimeout(timer); }
      await connect(phone, 'phone', host.pub, 'auth_err');
    }
  } finally { peers.forEach(peer => peer.close()); }
}
