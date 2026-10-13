#!/usr/bin/env node
// Synthetic frame producer for the live-preview E2E. It exercises the real
// subprocess/JSONL/renderer path, not ScreenCaptureKit or macOS permissions.
const http = require('node:http');
const fs = require('node:fs');
const readline = require('node:readline');
const target = process.argv[4];
const endpoint = process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_URL;
if (!endpoint || process.argv[2] !== '--native-preview') process.exit(2);
let visible = false, live = true, pending, inFlight, sequence = 0;
const record = (entry) => fs.appendFileSync(process.env.WUU_ACTIVITY_PREVIEW_FIXTURE_LOG, JSON.stringify({ target, ...entry }) + '\n');
const send = (event) => { record({ direction: 'out', event }); process.stdout.write(JSON.stringify(event) + '\n'); };
function flush() {
  if (!visible || !live || inFlight || !pending) return;
  const frame = pending; pending = undefined; inFlight = ++sequence;
  send({ ...frame, event: 'frame', sequence });
}
const request = http.get(`${endpoint}/source?target=${encodeURIComponent(target)}`, response => {
  readline.createInterface({ input: response }).on('line', line => {
    const event = JSON.parse(line);
    if (event.event === 'frame') { pending = event; flush(); }
    else send(event);
  });
});
request.on('error', error => { process.stderr.write(error.message); process.exit(1); });
readline.createInterface({ input: process.stdin }).on('line', line => {
  const command = JSON.parse(line);
  record({ direction: 'in', command });
  if (command.type === 'close') { request.destroy(); process.exit(0); }
  if (command.type === 'visible') visible = command.visible === true;
  if (command.type === 'live') live = command.live === true;
  if (command.type === 'ack' && command.sequence === inFlight) inFlight = undefined;
  flush();
}).on('close', () => { request.destroy(); process.exit(0); });
