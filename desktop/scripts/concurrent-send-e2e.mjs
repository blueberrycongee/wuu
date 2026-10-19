// Real renderer/preload/main/Go admission race with a local streaming provider.
// Build the desktop and desktop/build/bin/wuu-core, then run:
//   cd desktop && npx electron scripts/concurrent-send-e2e.mjs
// IPC gates force the competing turn to win; requests still reach the real core.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { createHash } from 'node:crypto';
import { fileURLToPath, pathToFileURL } from 'node:url';

// The wire adapter may merge interrupted and current user messages. Match the
// current fixture message, never an earlier marker or an arbitrary substring.
function backgroundForRequest(text, registered) {
  const current = typeof text === 'string' ? text.split('\n').filter(line => line.length > 0).at(-1) : undefined;
  return registered.has(current) ? current : undefined;
}
function hasPromptLine(text, prompt) {
  return typeof text === 'string' && text.split('\n').includes(prompt);
}
if (process.argv.includes('--self-test')) {
  const previous = 'Competing background turn interrupted';
  const current = 'Competing background turn current';
  const registered = new Set([previous, current]);
  assert.equal(backgroundForRequest(current, registered), current);
  assert.equal(backgroundForRequest(`${previous}\n${current}`, registered), current);
  assert.equal(backgroundForRequest(`${previous}\nordinary new input`, registered), undefined);
  assert.equal(backgroundForRequest(`${current} suffix`, registered), undefined);
  assert.equal(backgroundForRequest(`prefix ${current}`, registered), undefined);
  assert.equal(hasPromptLine(`${previous}\nheld message\n${current}`, 'held message'), true);
  assert.equal(hasPromptLine('Competing background turn held message', 'held message'), false);
  console.log('Concurrent-send fixture contracts passed');
  process.exit(0);
}

const { default: electron } = await import('electron');
const { app, ipcMain } = electron;
const desktop = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.resolve(process.env.WUU_CONCURRENT_SEND_OUTPUT || path.join(desktop, 'out/e2e/concurrent-send'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-concurrent-send-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_CONCURRENT_SEND_CORE || path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;

const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};
const rpc = [], requests = [], checks = [];
let collision, queueFailure = false;
const blockers = new Map();
const backgrounds = new Set();
const server = http.createServer((req, res) => {
  let raw = '';
  req.on('data', chunk => { raw += chunk; });
  req.on('end', () => {
    if (req.method === 'GET') return res.end(JSON.stringify({ data: [{ id: 'fixture' }] }));
    const body = JSON.parse(raw);
    const messages = body.messages || [];
    const user = messages.findLast(message => message.role === 'user'
      && !String(message.content).trimStart().startsWith('<system-reminder>'));
    const text = typeof user?.content === 'string' ? user.content : JSON.stringify(user?.content);
    if (!body.stream) return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Concurrent send' } }] }));
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = (delta, finish_reason = null) => res.write(`data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
    const finish = () => {
      chunk({ role: 'assistant', content: `Received: ${text}` });
      chunk({}, 'stop');
      res.end('data: [DONE]\n\n');
    };
    if (body.tools?.length) {
      const background = backgroundForRequest(text, backgrounds);
      requests.push({ text, messages, background });
      if (background) {
        chunk({ role: 'assistant', content: 'Background work is running.' });
        blockers.set(background, finish);
        return;
      }
    }
    if (!body.tools?.length) {
      chunk({ role: 'assistant', content: 'Concurrent send' });
      chunk({}, 'stop');
      res.end('data: [DONE]\n\n');
    } else {
      finish();
    }
  });
});

// Keep the busy response in flight until a second send or Stop has been issued.
const register = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => register(channel, async (...args) => {
  if (channel === 'wuu:turn-start') {
    rpc.push({ method: 'turn/start', thread: args[1], prompt: args[2], client: args[9] });
    const active = collision;
    if (active && args[2] === active.prompt) {
      collision = undefined;
      active.input = args.slice(1);
      if (active.timeout) throw new Error('app-server request timed out: turn/start');
      const competing = [...args];
      competing[2] = active.background;
      competing[3] = []; competing[4] = []; competing[6] = undefined; competing[7] = undefined;
      competing[9] = `background-${active.prompt}`;
      await handler(...competing);
      try {
        await handler(...args);
        throw new Error('Expected a real busy admission rejection');
      } catch (error) {
        assert.match(error.message, /already has a running turn/);
        active.rejection = error.message;
        rpc.push({ method: 'turn/start rejected', prompt: args[2], error: error.message });
        await active.release.promise;
        throw error;
      }
    }
  }
  if (channel === 'wuu:turn-queue') {
    rpc.push({ method: 'turn/queue', thread: args[1], prompt: args[2], client: args[4], hold: args[10], images: args[3], files: args[5] });
    if (queueFailure) throw new Error('fixture queue persistence failed');
  }
  return handler(...args);
});

let main;
app.on('browser-window-created', (_event, win) => {
  main ||= win;
  win.webContents.on('console-message', (event) => {
    if (event.level === 'error' || event.level === 'warning') console.error(`[renderer] ${event.message}`);
  });
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, arg) => main.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function until(check, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    if (await check()) return;
    await delay(30);
  }
  throw new Error(`Timed out: ${label}`);
}
async function send(text, attachment = false) {
  await evaluate(({ text, attachment }) => {
    const input = document.querySelector('.composer textarea');
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    if (attachment) {
      const data = new DataTransfer();
      data.items.add(new File(['ATTACHMENT_SURVIVES_ADMISSION_RACE'], 'context.txt', { type: 'text/plain' }));
      input.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true }));
    }
  }, { text, attachment });
  await until(() => evaluate(() => Boolean(document.querySelector('.composer-stop-button, .composer-send-button:not(:disabled)'))), 'composer accepts input');
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Return' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Return' });
  await until(() => rpc.some(row => row.method === 'turn/start' && row.prompt === text)
    || evaluate(() => document.querySelector('.composer textarea')?.value === ''), `submitted ${text}`);
}
const idle = () => until(() => evaluate(() => Boolean(document.querySelector('.composer-send-button[data-wuu-state="send"]'))), 'idle composer');
const snapshot = id => evaluate(id => window.wuu.resumeThread(id), id);
function arm(prompt, options = {}) {
  collision = { prompt, background: `Competing background turn ${prompt}`, release: deferred(), ...options };
  backgrounds.add(collision.background);
  return collision;
}
async function completed(id, prompt) {
  await until(async () => {
    const { thread } = await snapshot(id);
    return thread.turns.some(turn => turn.status === 'completed' && turn.items.some(item => item.type === 'user_message' && item.text === prompt));
  }, `completed ${prompt}`);
}
function save(status, error) {
  fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify({ status, error, checks, rpc, requests,
    coreSHA256: createHash('sha256').update(fs.readFileSync(process.env.WUU_DESKTOP_CORE)).digest('hex'),
  }, null, 2));
}
const watchdog = setTimeout(() => { save('failed', 'watchdog expired'); app.exit(1); }, 150000);

await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', engines,
  providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
}));
const stamp = '2026-01-01T00:00:00Z';
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'Concurrent send', path: project, created_at: stamp, updated_at: stamp }],
  active_context: { kind: 'project', project_id: 'fixture', cwd: project },
}));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);

// Electron waits for ESM evaluation before emitting ready. Finish setup before
// starting the interactive journey, which needs the production window.
async function run() {
  await until(() => main && evaluate(() => Boolean(document.querySelector('.composer textarea'))), 'composer ready');
  main.webContents.setBackgroundThrottling(false);
  await send('Initial message');
  await until(async () => (await evaluate(() => window.wuu.listThreads())).threads.length > 0, 'initial thread created');
  const { threads } = await evaluate(() => window.wuu.listThreads());
  const id = threads[0].id;
  await completed(id, 'Initial message');
  await idle();

  const first = arm('Retain this first message');
  await send(first.prompt, true);
  await until(() => first.rejection, 'first busy rejection');
  await send('Retain this second message');
  assert.equal(rpc.filter(row => row.method === 'turn/queue').length, 0, 'later input waits for the first admission');
  first.release.resolve();
  await until(() => rpc.filter(row => row.method === 'turn/queue').length === 2, 'both messages queued');
  const queued = rpc.filter(row => row.method === 'turn/queue');
  assert.deepEqual(queued.map(row => row.prompt), [first.prompt, 'Retain this second message']);
  assert.equal(queued[0].client, first.input[8], 'fallback retains the original client ID');
  assert.deepEqual(queued[0].files, first.input[3], 'fallback retains encoded attachments');
  assert.equal(queued[0].files.length, 1);
  await until(() => blockers.has(first.background), 'background provider blocked');
  const retained = await snapshot(id);
  assert.equal(retained.pending_user_messages.length, 2, 'core snapshot includes both accepted messages');
  assert.equal(await evaluate(() => Boolean(document.querySelector('[data-wuu-component="composer-pending"]'))), true);
  assert.equal(await evaluate(() => Boolean(document.querySelector('[data-wuu-component="notice"][data-wuu-variant="error"]'))), false);
  blockers.get(first.background)();
  await completed(id, 'Retain this second message');
  const delivered = await snapshot(id);
  for (const prompt of [first.prompt, 'Retain this second message']) {
    assert.equal(delivered.thread.turns.flatMap(turn => turn.items).filter(item => item.type === 'user_message' && item.text === prompt).length, 1);
  }
  const attachmentRequest = requests.find(row => row.text?.startsWith(`${first.prompt}\n`));
  const attachmentPath = attachmentRequest.text.match(/"context\.txt"[^\n]*: (.+)/)[1];
  assert.equal(fs.readFileSync(attachmentPath, 'utf8'), 'ATTACHMENT_SURVIVES_ADMISSION_RACE');
  checks.push('Real busy rejection queues once, preserves attachment and identity, and drains in send order');
  await idle();

  const stopped = arm('Keep this message after Stop');
  await send(stopped.prompt);
  await until(() => stopped.rejection, 'stop scenario busy rejection');
  await evaluate(() => document.querySelector('.composer-stop-button, .composer-send-button[data-wuu-state="submitting"], .composer-send-button[data-wuu-state="stop"]').click());
  stopped.release.resolve();
  await until(() => rpc.some(row => row.method === 'turn/queue' && row.prompt === stopped.prompt), 'stopped send held');
  assert.equal(rpc.find(row => row.method === 'turn/queue' && row.prompt === stopped.prompt).hold, true);
  await until(async () => (await snapshot(id)).held_user_messages?.some(message => message.prompt === stopped.prompt), 'stopped message persisted');
  assert.equal(requests.filter(row => hasPromptLine(row.text, stopped.prompt)).length, 0);
  checks.push('Stop during admission retains the message as held without running it');
  await idle();

  await until(async () => (await snapshot(id)).thread.turns.some(turn => turn.status === 'interrupted'
    && turn.items.some(item => item.type === 'user_message' && item.text === stopped.background)),
  'Stop settles the competing turn as interrupted before the next send');
  const interrupted = await snapshot(id);
  assert.ok(interrupted.held_user_messages?.some(message => message.id === stopped.input[8]
    && message.prompt === stopped.prompt), 'Stop preserves the original client identity');

  queueFailure = true;
  const failed = arm('Restore this rejected queue draft');
  await send(failed.prompt);
  await until(() => failed.rejection, 'queue failure busy rejection');
  failed.release.resolve();
  await until(() => evaluate(text => document.querySelector('.composer textarea')?.value === text, failed.prompt), 'queue failure restores draft');
  queueFailure = false;
  await until(() => blockers.has(failed.background), 'failure scenario blocker');
  blockers.get(failed.background)();
  await completed(id, failed.background);
  const afterFailure = await snapshot(id);
  assert.ok(afterFailure.held_user_messages?.some(message => message.id === stopped.input[8]
    && message.prompt === stopped.prompt), 'A later send cannot silently consume held input');
  assert.equal(afterFailure.thread.turns.some(turn => turn.items.some(item =>
    item.type === 'user_message' && item.text === stopped.prompt)), false,
  'Held input has not become an executed user turn');
  for (const prompt of [stopped.prompt, failed.prompt]) {
    assert.equal(requests.some(row => hasPromptLine(row.text, prompt)), false,
      'Neither held input nor a failed queue draft reaches inference inside merged wire content');
  }
  checks.push('Queue persistence failure restores the original draft');
  await idle();

  const before = rpc.filter(row => row.method === 'turn/queue').length;
  arm('Do not replay ambiguous timeout', { timeout: true });
  await send('Do not replay ambiguous timeout');
  await until(() => evaluate(() => document.querySelector('.composer textarea')?.value === 'Do not replay ambiguous timeout'), 'timeout draft restored');
  assert.equal(rpc.filter(row => row.method === 'turn/queue').length, before);
  checks.push('Ambiguous timeout is not automatically resubmitted');
  save('passed');
  console.log(JSON.stringify({ status: 'passed', checks, output }, null, 2));
  clearTimeout(watchdog);
  server.close();
  app.exit(0);
}
void run().catch(async (error) => {
  if (main && !main.isDestroyed()) {
    fs.writeFileSync(path.join(output, 'failure-ui.json'), JSON.stringify(await evaluate(() => ({
      url: location.href, text: document.body.innerText, buttons: [...document.querySelectorAll('button')].map(button => ({ text: button.textContent, label: button.getAttribute('aria-label') })),
    })), null, 2));
  }
  save('failed', error.stack);
  console.error(error);
  clearTimeout(watchdog);
  app.exit(1);
});
