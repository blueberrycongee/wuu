// Real browser -> encrypted relay -> Go Agent -> host filesystem. Only the
// model is deterministic; no renderer, bridge, tool or session API is mocked.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { slowLink } from './slow-link.mjs';

const clientDir = fileURLToPath(new URL('..', import.meta.url));
const repoDir = path.resolve(clientDir, '../..');
const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'wuu-web-e2e-'));
const workspace = path.join(temp, 'workspace'), state = path.join(temp, 'state');
const processes = [];
let browser, provider, throttled, page;
const downloadBps = Number(process.env.WUU_E2E_DOWNLOAD_BPS || 0);
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await check()) return; await pause(50); }
  throw new Error(`Timed out: ${label}`);
}
function start(command, args, options = {}) {
  const child = spawn(command, args, { cwd: repoDir, ...options, stdio: ['pipe', 'pipe', 'pipe'] });
  const run = { child, output: '', exited: false };
  child.stdout.on('data', data => { run.output += data; });
  child.stderr.on('data', data => { run.output += data; });
  child.on('exit', () => { run.exited = true; });
  child.on('error', error => { run.output += error.message; run.exited = true; });
  processes.push(run); return run;
}
async function listen(server) {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return server.address().port;
}
async function freePort() {
  const server = http.createServer(); const port = await listen(server);
  await new Promise(resolve => server.close(resolve)); return port;
}
let releaseCompletion, completionRequested = false;
const completionGate = new Promise(resolve => { releaseCompletion = resolve; });
let interruptRequested = false;
const stopPrompt = 'Wait for a desktop stop';
const desktopPrompt = 'Continue from the desktop';
const desktopFinal = 'The desktop continued the same shared conversation.';
const finalText = 'Completed the browser task. Created src/browser-task.ts on the paired computer.';
const fileText = 'export const remoteResult = "written on the paired computer";\n'
  + (downloadBps ? Array.from({ length: 15000 }, (_, i) => `// Generated source line ${i}: keep remote history complete across reconnection.\n`).join('') : '');
try {
  await fs.mkdir(workspace); await fs.mkdir(state);
  await fs.writeFile(path.join(workspace, '.gitignore'), '.wuu-state/\n');
  const git = (...args) => execFileSync('git', ['-C', workspace, ...args], { stdio: 'pipe' });
  git('init', '-b', 'main');
  git('config', 'user.name', 'Web Test'); git('config', 'user.email', 'web@example.invalid');
  git('config', 'commit.gpgSign', 'false'); git('config', 'core.hooksPath', '/dev/null');
  git('add', '.gitignore');
  git('-c', 'user.name=Web Test', '-c', 'user.email=web@example.invalid', '-c', 'commit.gpgSign=false', 'commit', '-m', 'Initial');
  const binary = process.env.WUU_E2E_BINARY || path.join(temp, 'wuu');
  if (!process.env.WUU_E2E_BINARY) execFileSync('go', ['build', '-o', binary, './cmd/wuu'], { cwd: repoDir, stdio: 'inherit' });
  provider = http.createServer(async (req, res) => {
    try {
      let raw = ''; for await (const data of req) raw += data;
      if (req.method === 'GET') { res.end(JSON.stringify({ object: 'list', data: [{ id: 'web-fixture', object: 'model' }] })); return; }
      const body = JSON.parse(raw);
      const desktopTurn = body.messages?.some(message => message.role === 'user' && JSON.stringify(message.content).includes(desktopPrompt));
      const historyFixture = body.messages?.some(message => message.role === 'user' && JSON.stringify(message.content).includes('Remote history fixture'));
      const writeTool = !historyFixture && body.tools?.some(tool => tool.function?.name === 'write_file');
      if (writeTool && body.messages?.filter(message => message.role === 'user').at(-1)?.content === stopPrompt) {
        interruptRequested = true;
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.write('data: ' + JSON.stringify({ id: 'held', object: 'chat.completion.chunk', choices: [{ index: 0, delta: { role: 'assistant', content: 'Working until stopped' }, finish_reason: null }] }) + '\n\n');
        await new Promise(resolve => res.once('close', resolve));
        return;
      }
      const hasToolReply = body.messages?.some(message => message.role === 'tool');
      const message = writeTool && !hasToolReply
        ? { role: 'assistant', tool_calls: [{ id: 'write-web-fixture', type: 'function', function: { name: 'write_file', arguments: JSON.stringify({ path: 'src/browser-task.ts', content: fileText }) } }] }
        : { role: 'assistant', content: writeTool ? (desktopTurn ? desktopFinal : finalText) : 'Browser coding task' };
      if (writeTool && hasToolReply) { completionRequested = true; await completionGate; }
      const finish_reason = message.tool_calls ? 'tool_calls' : 'stop';
      if (body.stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        const deltas = [];
        if (downloadBps && message.tool_calls) {
          const tool = message.tool_calls[0];
          for (let offset = 0; offset < tool.function.arguments.length; offset += 8192) {
            deltas.push({ tool_calls: [{ index: 0, ...(offset === 0 ? { id: tool.id, type: tool.type } : {}), function: { ...(offset === 0 ? { name: tool.function.name } : {}), arguments: tool.function.arguments.slice(offset, offset + 8192) } }] });
          }
        } else deltas.push(message);
        for (const choice of [...deltas.map(delta => ({ index: 0, delta, finish_reason: null })), { index: 0, delta: {}, finish_reason }]) {
          res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [choice] })}\n\n`);
        }
        res.end('data: [DONE]\n\n');
      } else {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ id: 'fixture', choices: [{ index: 0, message, finish_reason }] }));
      }
    } catch (error) { res.writeHead(500); res.end(String(error)); }
  });
  const webHost = process.env.WUU_E2E_WEB_HOST || "127.0.0.1";
  const modelPort = await listen(provider), relayPort = await freePort();
  if (downloadBps) throttled = await slowLink(relayPort, downloadBps);
  const webPort = throttled?.port ?? relayPort;
  await fs.writeFile(path.join(state, 'config.json'), JSON.stringify({ default_provider: 'web-test', providers: { 'web-test': { type: 'openai-compatible', base_url: `http://127.0.0.1:${modelPort}/v1`, api_key: 'local-test', model: 'web-fixture' } }, agent: { permission_mode: 'standard' } }));
  const env = { ...process.env, WUU_HOME: state };
  execFileSync(process.execPath, [path.join(repoDir, 'desktop/scripts/build-web.cjs')], { cwd: repoDir, stdio: 'pipe' });
  const relay = start(binary, ['relay', '--addr', `${webHost}:${relayPort}`, '--state', path.join(temp, 'relay.json'), '--web-root', path.join(repoDir, 'desktop/build/mobile-web')], { env });
  await until(() => relay.output.includes('listening'), 'relay startup');
  const sharedRunner = path.join(temp, 'shared-host.mjs');
  execFileSync(path.join(repoDir, 'desktop/node_modules/.bin/esbuild'), [
    path.join(repoDir, 'desktop/test-fixtures/sharedRemoteHost.ts'), '--bundle', '--platform=node', '--format=esm',
    `--alias:electron=${path.join(repoDir, 'desktop/test-fixtures/electronForRemoteHost.ts')}`, `--outfile=${sharedRunner}`,
  ], { stdio: 'pipe' });
  const desktop = start(process.execPath, [sharedRunner], { env: { ...env, WUU_DESKTOP_CORE: binary, WUU_TEST_WORKSPACE: workspace } });
  const records = () => desktop.output.split('\n').flatMap(line => { try { return [JSON.parse(line)]; } catch { return []; } });
  await until(() => records().some(row => row.bridge), 'desktop service pool');
  const endpoint = records().find(row => row.bridge).bridge;
  env.WUU_DESKTOP_APP_SERVER_ADDR = endpoint.address;
  env.WUU_DESKTOP_APP_SERVER_TOKEN = endpoint.token;
  let requestID = 0;
  const desktopCall = async (method, params) => {
    const id = ++requestID;
    desktop.child.stdin.write(JSON.stringify({ id, method, params }) + '\n');
    await until(() => records().some(row => row.desktopResponse?.id === id), `desktop ${method}`);
    const response = records().find(row => row.desktopResponse?.id === id).desktopResponse;
    assert.equal(response.error, undefined);
    return response.result;
  };
  const host = start(binary, ['remote', 'host', '--workdir', workspace, '--relay', `ws://${webHost}:${relayPort}/v1/connect`, '--pair'], { env });
  await until(() => host.output.includes('wuu://pair?'), 'host pairing');
  const pairingURL = new URL(host.output.match(/wuu:\/\/pair\?[^\s]+/)[0]);
  pairingURL.searchParams.set('r', `ws://${webHost}:${webPort}/v1/connect`);
  const uri = pairingURL.href;
  browser = await chromium.launch({ channel: process.env.WUU_BROWSER_CHANNEL || undefined });
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, locale: 'zh-CN' });
  if (downloadBps) context.setDefaultTimeout(90000);
  page = await context.newPage(); const pageErrors = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.addInitScript(() => {
    const Native = window.WebSocket;
    window.WebSocket = class extends Native { constructor(...args) { super(...args); window.testSocket = this; } };
    window.testDocumentID = Math.random();
  });
  await page.goto(`http://${webHost}:${webPort}/#${new URLSearchParams({ pair: uri })}`);
  await page.locator('.app-shell').waitFor();
  assert.equal(new URL(page.url()).hash, '');
  const documentID = await page.evaluate(() => window.testDocumentID);
  const input = page.locator('textarea:visible').first();
  await input.fill('Create src/browser-task.ts with the requested test export.');
  await page.getByRole('button', { name: '发送', exact: true }).filter({ visible: true }).click();
  await until(() => completionRequested, 'Agent tool execution');
  const started = records().find(row => row.desktopEvent?.message?.method === 'turn/started');
  assert(started, 'desktop received the phone turn without polling');
  const threadID = started.desktopEvent.message.params.thread_id;
  await desktopCall('thread/resume', { session_id: threadID });

  assert.equal(await fs.readFile(path.join(workspace, 'src/browser-task.ts'), 'utf8'), fileText);
  await input.fill('Draft that must survive restoration');
  await context.setOffline(true); await page.evaluate(() => window.testSocket.close());
  await page.getByRole('status').filter({ hasText: '连接已断开' }).waitFor();
  assert.equal(await input.isEditable(), true, 'offline drafts remain editable');
  releaseCompletion();
  // The host emits this after completing the turn while no browser is attached.
  await until(() => host.output.includes('push agent_done'), 'host completion while detached');
  const desktopSnapshot = await desktopCall('thread/resume', { session_id: threadID });
  assert(JSON.stringify(desktopSnapshot).includes(finalText), 'already-open desktop conversation sees phone completion');
  await context.setOffline(false);
  await page.locator('.web-connection-status').waitFor({ state: 'hidden', timeout: 45000 });
  await page.getByText(finalText, { exact: true }).filter({ visible: true }).waitFor();
  assert.equal(await page.evaluate(() => window.testDocumentID), documentID);
  assert.equal(await input.inputValue(), 'Draft that must survive restoration');
  host.child.kill('SIGTERM');
  await until(() => host.exited, 'host shutdown');
  await page.getByRole('status').filter({ hasText: '连接已断开' }).waitFor();
  const restarted = start(binary, ['remote', 'host', '--workdir', workspace, '--relay', `ws://${webHost}:${relayPort}/v1/connect`], { env });
  await until(() => restarted.output.includes('connected to relay'), 'host restart');
  await page.locator('.web-connection-status').waitFor({ state: 'hidden', timeout: 45000 });
  await page.getByText(finalText, { exact: true }).filter({ visible: true }).waitFor();
  assert.equal(await page.getByText(finalText, { exact: true }).filter({ visible: true }).count(), 1);
  assert.equal(await input.inputValue(), 'Draft that must survive restoration');
  assert.equal(await page.evaluate(() => window.testDocumentID), documentID);
  await desktopCall('turn/start', { thread_id: threadID, prompt: desktopPrompt });
  await page.getByText(desktopFinal, { exact: true }).filter({ visible: true }).waitFor();
  assert.equal(await page.evaluate(() => window.testDocumentID), documentID);
  await page.evaluate(({ threadID, stopPrompt }) => window.wuu.startTurn(threadID, stopPrompt), { threadID, stopPrompt });
  await until(() => interruptRequested, 'phone task awaiting desktop stop');
  restarted.child.kill('SIGTERM');
  await until(() => restarted.exited, 'remote transport stops while task runs');
  const held = await desktopCall('thread/resume', { session_id: threadID });
  assert.equal(held.thread.turns.at(-1).status, 'in_progress', 'closing remote access must not stop the shared task');
  const reattachedHost = start(binary, ['remote', 'host', '--workdir', workspace, '--relay', `ws://${webHost}:${relayPort}/v1/connect`], { env });
  await until(() => reattachedHost.output.includes('connected to relay'), 'remote transport resumes');
  await page.locator('.web-connection-status').waitFor({ state: 'hidden', timeout: 45000 });
  await desktopCall('turn/interrupt', { thread_id: threadID });
  await until(async () => {
    const snapshot = await page.evaluate(threadID => window.wuu.resumeThread(threadID), threadID);
    return snapshot.thread.turns.at(-1)?.status === 'interrupted';
  }, 'phone receives desktop interruption');
  const changes = await page.evaluate(() => window.wuu.listGitChanges());
  assert(changes.files.some(file => file.path === 'src/browser-task.ts' && file.additions === fileText.trimEnd().split('\n').length));
  const diff = await page.evaluate(() => window.wuu.readGitFileDiff('src/browser-task.ts'));
  if (downloadBps) {
    assert.equal(diff.truncated, true);
    assert(diff.modified_text.length > 0 && fileText.startsWith(diff.modified_text));
  } else assert.equal(diff.modified_text, fileText);
  if (!downloadBps) {
  await page.locator('.compact-conversation-actions [aria-haspopup="menu"]').tap();
  await page.getByRole('menuitem', { name: '打开右侧栏', exact: true }).tap();
  await page.getByRole('button', { name: '文件', exact: true }).click();
  await page.getByRole('treeitem', { name: 'src', exact: true }).filter({ visible: true }).click();
  await page.getByRole('treeitem', { name: 'browser-task.ts', exact: true }).filter({ visible: true }).click();
  await page.locator('.monaco-editor').filter({ visible: true }).waitFor();
  await page.getByRole('button', { name: '返回', exact: true }).click();
  assert.equal(await page.locator('.workspace-right-panel').getAttribute('data-wuu-view'), 'files');
  await page.getByRole('button', { name: '返回', exact: true }).click();
  await page.getByRole('button', { name: '返回', exact: true }).click();
  // Exercise both browser resize models. This simulates viewport geometry,
  // not an OS keyboard; focus and blank-message-area taps are real browser input.
  for (const visualOnly of [false, true]) {
    await page.setViewportSize({ width: 390, height: 844 });
    const draft = await input.inputValue();
    const originalInput = await input.elementHandle();
    await input.tap();
    assert.equal(await input.evaluate(node => node === document.activeElement), true);
    const resizeKeyboard = async (height) => {
      if (visualOnly) {
        await page.evaluate(height => {
          Object.defineProperty(window.visualViewport, 'height', { configurable: true, value: height });
          window.visualViewport.dispatchEvent(new Event('resize'));
        }, height);
      } else {
        await page.setViewportSize({ width: 390, height });
      }
      await until(async () => {
        const rect = await page.locator('.app-shell').boundingBox();
        return rect && rect.y >= 0 && Math.abs(rect.y + rect.height - height) <= 1;
      }, `workbench follows ${visualOnly ? 'visual' : 'layout'} viewport at ${height}`);
    };
    await resizeKeyboard(420);
    await page.locator('.scroll-region').filter({ visible: true }).first().tap({ position: { x: 2, y: 2 } });
    assert.equal(await input.evaluate(node => node === document.activeElement), false);
    await resizeKeyboard(844);
    assert.equal(await input.inputValue(), draft);
    assert.equal(await input.evaluate((node, original) => node === original, originalInput), true);
    await originalInput.dispose();
    if (visualOnly) {
      await page.evaluate(() => {
        delete window.visualViewport.height;
        window.visualViewport.dispatchEvent(new Event('resize'));
      });
    }
  }
  for (const [width, height] of [[320, 740], [390, 844], [430, 932], [390, 360]]) {
    await page.setViewportSize({ width, height });
    await until(async () => {
      const rect = await page.getByRole('button', { name: '发送', exact: true }).filter({ visible: true }).boundingBox();
      return rect && rect.x >= 0 && rect.y >= 0 && rect.x + rect.width <= width && rect.y + rect.height <= height;
    }, `composer reachable at ${width}x${height}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), width);
    await page.locator('.compact-conversation-actions [aria-haspopup="menu"]').tap();
    await page.getByRole('menuitem', { name: '展开左侧栏', exact: true }).tap();
    const closeDrawer = page.locator('.compact-session-switcher-close');
    await closeDrawer.waitFor({ state: 'visible' });
    for (const target of await page.locator('.sidebar :is(button.sidebar-mode-option, .sidebar-notifications-button)').all()) {
      await target.tap({ trial: true });
      const box = await target.boundingBox();
      assert(box && box.width >= 44 && box.height >= 44, `sidebar touch target at ${width}x${height}: ${await target.getAttribute('class')} ${JSON.stringify(box)}`);
      assert(box.x >= 0 && box.x + box.width <= width && box.y + box.height <= height, 'sidebar controls fit the phone');
    }
    if (height === 360) {
      assert.equal(await page.evaluate(() => window.dispatchEvent(new Event('wuu:native-back', {cancelable:true}))), false);
    } else await closeDrawer.tap();
    await closeDrawer.waitFor({ state: 'hidden' });
    assert.equal(await page.locator('.app-shell').count(), 1, 'back closes the drawer without disconnecting the computer');
  }
  await page.setViewportSize({ width: 1280, height: 900 });
  await until(async () => {
    const brand = await page.locator('.sidebar-brand').boundingBox();
    const workbench = await page.locator('.app-shell').boundingBox();
    return brand && workbench && brand.x >= 0 && brand.y - workbench.y < 30;
  }, 'web sidebar starts near the top without native window chrome');
  }
  if (process.env.WUU_E2E_REMOTE_HISTORY === '1') {
    await page.evaluate(async () => {
      await window.wuu.createCheckoutGitBranch('web-verified');
      await window.wuu.commitGitChanges({message:'Verify remote workspace changes'});
    });
    assert(git('branch', '--show-current').toString().trim() === 'web-verified', 'Web switches the desktop Git branch');
    assert(git('log', '-1', '--format=%s').toString().trim() === 'Verify remote workspace changes', 'Web commits through the shared desktop Git service');
    const fixture = await desktopCall('thread/start', {});
    const historyID = fixture.thread.id;
    const image = await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 512;
      const ctx = canvas.getContext('2d'), pixels = ctx.createImageData(512, 512);
      let seed = 42;
      for (let i = 0; i < pixels.data.length; i += 4) {
        for (let c = 0; c < 3; c++) { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; pixels.data[i+c] = seed >>> 24; }
        pixels.data[i+3] = 255;
      }
      ctx.putImageData(pixels, 0, 0);
      return {media_type:'image/png', data:canvas.toDataURL('image/png').split(',')[1]};
    });
    for (let i = 0; i < 23; i++) {
      const turn = await desktopCall('turn/start', {thread_id:historyID, prompt:`Remote history fixture ${i}`, ...(i === 22 ? {images:[image]} : {})});
      await until(() => records().some(row => row.desktopEvent?.message?.method === 'turn/completed' && row.desktopEvent.message.params.thread_id === historyID && row.desktopEvent.message.params.turn?.id === turn.turn.id), `history fixture turn ${i}`);
    }
    const full = await desktopCall('thread/resume', {session_id:historyID, response_only:true});
    const compact = await page.evaluate(id => window.wuu.resumeThread(id), historyID);
    assert(compact.thread.turns.length < full.thread.turns.length, 'initial history is paged');
    assert(compact.thread.history_cursor, 'older history has a cursor');
    const fullImage = full.thread.turns.flatMap(turn => turn.items).flatMap(item => item.images ?? [])[0];
    const deferred = compact.thread.turns.flatMap(turn => turn.items).flatMap(item => item.images ?? [])[0];
    assert(fullImage.data.length > 16 * 1024 && deferred.data === '' && deferred.remote_ref, 'image bytes stay on desktop');
    const thumbnail = await page.evaluate(ref => window.wuu.readRemoteAttachmentPreview(ref), deferred.remote_ref);
    assert(thumbnail.startsWith('data:image/jpeg;base64,') && thumbnail.length < fullImage.data.length, 'encrypted thumbnail is smaller than the original');
    assert(await page.evaluate(async ({ref, expected}) => (await window.wuu.readRemoteAttachment(ref)) === expected, {ref:deferred.remote_ref, expected:fullImage.data}), 'encrypted image chunks reassemble exactly');
    const olderIDs = await page.evaluate(async ({id, cursor}) => {
      const ids = [];
      while (cursor) {
        let page;
        const off = window.wuu.onServerEvent(event => { if (event.kind === 'notification' && event.message.method === 'thread/historyLoaded') page = event.message.params; });
        try { await window.wuu.loadEarlierThreadHistory(id, cursor); } finally { off(); }
        if (!page) throw new Error('missing history page event');
        ids.unshift(...page.turns.map(turn => turn.id)); cursor = page.history_cursor;
      }
      return ids;
    }, {id:historyID,cursor:compact.thread.history_cursor});
    assert.deepEqual([...olderIDs,...compact.thread.turns.map(turn => turn.id)], full.thread.turns.map(turn => turn.id));
    console.log('PASS: paged history and encrypted on-demand image round trip', {fullBytes:JSON.stringify(full).length, firstPageBytes:JSON.stringify(compact).length});
  }
  assert.deepEqual(pageErrors, []);
  console.log(downloadBps
    ? 'PASS: slow TCP link, pairing, shared execution, large tool history, offline completion, snapshot/draft restoration, host restart, desktop interruption and Git RPCs'
    : 'PASS: shared desktop/phone execution, bidirectional live messages, pairing, host tool execution, offline completion, snapshot and draft restoration, host restart, Git review, file navigation and phone viewports',
    { downloadBps, generatedFileBytes: Buffer.byteLength(fileText) });
} catch (error) {
  if (page && !page.isClosed()) {
    const evidence = path.join(os.tmpdir(), 'wuu-web-e2e-failure.png');
    await page.screenshot({path:evidence}).catch(() => {});
    console.error('UI failure screenshot:', evidence);
    console.error('Connection status:', await page.locator('.web-connection-status').allTextContents().catch(() => []));
  }
  for (const run of processes) console.error(run.output.slice(-3000).replace(/"token":"[^"]+"/g, '"token":"[redacted]"').replace(/wuu:\/\/pair\?[^\s]+/g, '[pairing URI]'));
  throw error;
} finally {
  releaseCompletion();
  await browser?.close();
  throttled?.close();
  for (const run of processes) if (!run.exited) run.child.kill('SIGTERM');
  for (const run of processes) {
    try { await until(() => run.exited, 'process shutdown', 5000); } catch { run.child.kill('SIGKILL'); }
  }
  provider?.closeAllConnections(); provider?.close();
  await fs.rm(temp, { recursive: true, force: true });
}
