#!/usr/bin/env node
// Synthetic frame producer for the live-preview E2E. It exercises the real
// subprocess/binary AVC/renderer path, not ScreenCaptureKit or macOS permissions.
// Like the native helper, one host process serves every preview session.
const http = require('node:http');
const fs = require('node:fs');
const readline = require('node:readline');
const endpoint = process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_URL;
if (!endpoint || process.argv[2] !== '--native-preview-host') process.exit(2);
const sessions = new Map();
const record = (session, entry) => fs.appendFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG,
  JSON.stringify({ target: session.target, session: session.id, ...entry }) + '\n');
const send = (session, event) => {
  const { data, ...metadata } = event;
  const payload = Buffer.from(data || []);
  record(session, { direction: 'out', event: { ...metadata, bytes: payload.length } });
  const header = Buffer.from(JSON.stringify({ ...metadata, session: session.id })), prefix = Buffer.alloc(12);
  prefix.write('WCU1'); prefix.writeUInt32BE(header.length, 4); prefix.writeUInt32BE(payload.length, 8);
  process.stdout.write(Buffer.concat([prefix, header, payload]));
};
function flush(session) {
  if (!session.visible || !session.live || session.inFlight || !session.pending) return;
  const frame = session.pending; session.pending = undefined; session.inFlight = ++session.sequence;
  send(session, { ...frame, event: 'frame', sequence: session.sequence });
}
function close(session) {
  if (!sessions.has(session.id)) return;
  sessions.delete(session.id);
  session.request.destroy();
  send(session, { event: 'closed' });
}
function open(id, target) {
  const session = { id, target, visible: false, live: true, pending: undefined, inFlight: undefined, sequence: 0 };
  sessions.set(id, session);
  session.request = http.get(`${endpoint}/source?target=${encodeURIComponent(target)}`, response => {
    readline.createInterface({ input: response }).on('line', line => {
      const event = JSON.parse(line);
      if (event.event === 'frame') { session.pending = event; flush(session); }
      else if (event.event === 'gone') { send(session, event); close(session); }
      else send(session, event);
    });
  });
  session.request.on('error', error => {
    if (!sessions.has(id)) return;
    send(session, { event: 'failed', message: error.message });
    close(session);
  });
}
readline.createInterface({ input: process.stdin }).on('line', line => {
  const command = JSON.parse(line);
  if (command.type === 'open') { open(command.session, command.target); }
  const session = sessions.get(command.session);
  if (!session) return;
  record(session, { direction: 'in', command });
  if (command.type === 'close') { close(session); return; }
  if (command.type === 'visible') session.visible = command.visible === true;
  if (command.type === 'live') session.live = command.live === true;
  if (command.type === 'ack' && command.sequence === session.inFlight) session.inFlight = undefined;
  flush(session);
}).on('close', () => {
  for (const session of [...sessions.values()]) close(session);
  process.exit(0);
});
