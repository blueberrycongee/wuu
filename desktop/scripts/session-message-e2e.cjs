// Real bundled Electron/main/preload/Go acceptance with isolated persisted
// session-message fixtures. No provider request or peer delivery is simulated.
// Build the application and core first. WUU_DESKTOP_CORE selects the core and
// WUU_SESSION_MESSAGE_OUTPUT selects evidence. Run under xvfb on Linux.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain, clipboard, dialog } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_SESSION_MESSAGE_OUTPUT || path.join(desktop, 'out/session-message-e2e'));
const mainBundle = path.join(desktop, 'out/main/index.js');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-session-message-'));
const home = path.join(fixture, 'home');
fs.mkdirSync(home);
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.HOME = path.join(fixture, 'user-home');
fs.mkdirSync(process.env.HOME);
process.env.USERPROFILE = process.env.HOME;
process.env.CODEX_HOME = path.join(process.env.HOME, '.codex');
process.env.GROK_HOME = path.join(process.env.HOME, '.grok');
process.env.XDG_CONFIG_HOME = path.join(fixture, 'xdg-config');
process.env.XDG_DATA_HOME = path.join(fixture, 'xdg-data');
for (const name of Object.keys(process.env)) {
  if (/(?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|SECRET|PASSWORD)$/.test(name)) delete process.env[name];
}
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = path.resolve(process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'));
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const cwd = path.join(fixture, 'project');
fs.mkdirSync(cwd);
const project = { id: 'peer-project', name: 'Peer message review', path: cwd,
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
const longTitle = 'Review rendering, accessibility and source navigation across every workspace layout · 会话卡验收 '.repeat(4).trim();
const cjkTitle = '验证超长无空格中文会话标题在窄分屏内保持可点击且不溢出'.repeat(12);
const longReply = 'Review complete. Preserve the original draft when opening the related conversation.\n\n'
  + Array.from({ length: 18 }, (_, i) => `Finding ${i + 1}: retain complete copyable text, keyboard access and clear navigation for long conversation titles. 中文、café and 😀 remain intact.`).join('\n\n')
  + '\n\nEND OF COMPLETE PEER REPLY';
const messages = [
  { name: 'UI review', related: 'peer-request', origin: 'host', text: 'Please review the compact conversation card and keep the primary draft intact.' },
  { name: longTitle, related: 'peer-reply', origin: 'plugin', text: longReply },
  { name: 'Conversation unavailable', related: '', origin: 'host', text: 'This historical message has no related conversation target.' },
  { name: '', related: '', origin: 'plugin', text: 'This historical message has no source name or target.' },
  { name: cjkTitle, related: 'peer-cjk', origin: 'host', text: 'Please review.' },
];
// Actual synthetic managed files exercise the shipped protocol and download
// implementation. Only the OS destination picker is supplied by the harness.
const patchText = 'diff --git a/card.txt b/card.txt\n--- a/card.txt\n+++ b/card.txt\n@@ -1 +1 @@\n-old card\n+consistent card\n';
const zip = spawnSync('python3', ['-c', 'import io,zipfile,sys; out=io.BytesIO(); z=zipfile.ZipFile(out,"w"); z.writestr("README.txt","Synthetic acceptance package\\n"); z.close(); sys.stdout.buffer.write(out.getvalue())']);
assert.equal(zip.status, 0, String(zip.stderr));
const files = [
  { name: 'wuu-session-cards.zip', mime: 'application/zip', bytes: zip.stdout },
  { name: 'session-card-polish.patch', mime: 'text/x-diff', bytes: Buffer.from(patchText) },
  { name: '跨会话交付产物需要保留完整长名称以便识别'.repeat(4) + '.patch', mime: 'text/x-diff', bytes: Buffer.from(patchText + '# Long filename fixture\n') },
  { name: 'README', mime: 'application/octet-stream', bytes: Buffer.from('Extensionless artifact with unknown displayed size.\n'), noSize: true },
  { name: 'notes.txt', mime: 'text/plain', bytes: Buffer.from('Review notes without supplied size metadata.\n'), noSize: true },
];
const artifacts = files.map((file, index) => {
  const threadID = index === 0 ? 'peer-files-zip' : index === 1 ? 'peer-files-patch' : 'peer-files-multiple';
  const id = String(index + 1).padStart(32, '0'), sha256 = createHash('sha256').update(file.bytes).digest('hex');
  const root = path.join(home, 'workspaces', 'fixture', 'sessions', threadID, 'artifacts', id);
  fs.mkdirSync(root, { recursive: true });
  fs.writeFileSync(path.join(root, file.name), file.bytes);
  fs.writeFileSync(path.join(root, '.artifact.json'), JSON.stringify({ version: 1, id, thread_id: threadID, plugin_id: 'fixture', name: file.name, sha256, size: file.bytes.length }));
  return { type: 'file', name: file.name, mime_type: file.mime,
    uri: `wuu-artifact://fixture/${threadID}/${id}/${encodeURIComponent(file.name)}?sha256=${sha256}`,
    artifact: { placement: 'turn_end', ref: id, sha256, ...(file.noSize ? {} : { size_bytes: file.bytes.length }) } };
});
// Each output is the latest turn in its own conversation, matching the
// product's latest-turn summary presentation rather than forcing old summaries.
const artifactSessions = [
  { id: 'peer-files-zip', title: 'Archive delivery', messages: [{ name: 'Package build', related: 'peer-request', origin: 'host', text: 'The archive is ready for review.', artifacts: [artifacts[0]] }] },
  { id: 'peer-files-patch', title: 'Patch delivery', messages: [{ name: 'Patch review', related: 'peer-request', origin: 'plugin', text: 'The proposed patch is ready.', artifacts: [artifacts[1]] }] },
  { id: 'peer-files-multiple', title: 'Multiple file delivery', messages: [{ name: 'Delivery review', related: 'peer-reply', origin: 'host', text: 'Review the delivered patch and supporting notes.', artifacts: artifacts.slice(2) }] },
];
let requestedDownload;
const savedDownloads = [];
dialog.showSaveDialog = async (_parent, options) => {
  assert.ok(requestedDownload, 'Only an explicitly tested artifact download may open a picker');
  assert.equal(options.defaultPath, requestedDownload.name);
  const filePath = path.join(fixture, 'saved-' + requestedDownload.name);
  savedDownloads.push({ name: requestedDownload.name, filePath });
  return { canceled: false, filePath };
};
const sessions = [
  ...artifactSessions,
  { id: 'peer-main', title: 'Peer collaboration', messages },
  { id: 'peer-request', title: 'UI review', messages: [{ text: 'Source request context.' }] },
  { id: 'peer-reply', title: longTitle, messages: [{ text: 'Source reply context.' }] },
  { id: 'peer-cjk', title: cjkTitle, messages: [{ text: 'Short source context.' }] },
];
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic-not-a-credential', model: 'fixture' } },
  engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [project],
  active_context: { kind: 'project', project_id: project.id, cwd } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({
  onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 14,
}));
const rpc = [], checks = [], screenshots = [], recordings = [], failures = [];
const boundary = 'Shipped Electron main/preload/renderer and Go history restoration/navigation; synthetic persisted incoming messages and managed artifact files. The save destination picker is supplied by the harness; protocol fetching, preview and file writing are real. No external inference or live peer-to-peer delivery.';
let main, recordingTimer, pendingFrame = Promise.resolve();
const log = text => { console.log(text); fs.appendFileSync(path.join(output, 'run.log'), `${text}\n`); };
const pass = (name, evidence) => { checks.push({ name, evidence }); log(`PASS ${name}`); };
const originalHandle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => originalHandle(channel, async (...args) => {
  const entry = { channel, completed: false };
  if (channel === 'wuu:thread-resume') entry.thread = args[1];
  if (channel === 'wuu:thread-search') entry.query = args[1];
  rpc.push(entry);
  assert.ok(!['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(channel), 'Acceptance must not invoke inference');
  const result = await handler(...args);
  if (channel === 'wuu:thread-search') entry.results = result.results.map(value => ({ id: value.thread.id, title: value.thread.title }));
  entry.completed = true;
  return result;
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function evaluate(fn, ...args) {
  const result = await main.webContents.executeJavaScript(`(async()=>{try{return {value:await (${fn})(${args.map(arg => JSON.stringify(arg)).join(',')})}}catch(error){return {error:String(error.stack||error)}}})()`, true);
  if (result.error) throw new Error(result.error);
  return result.value;
}
async function until(fn, label, ...args) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const result = await evaluate(fn, ...args);
    if (result) return result;
    await delay(25);
  }
  throw new Error(`Timed out: ${label}`);
}
async function hostUntil(fn, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) { if (await fn()) return; await delay(25); }
  throw new Error(`Timed out: ${label}`);
}
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
function key(keyCode, modifiers = []) {
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  // Match the native button activation sequence used by request-lifecycle and
  // fast-mode acceptance. A keyDown alone omits Chromium's character event.
  if (['Return', 'Enter', 'Space'].includes(keyCode) && modifiers.length === 0) {
    main.webContents.sendInputEvent({ type: 'char', keyCode: keyCode === 'Space' ? ' ' : '\r' });
  }
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
}
// Use native CDP mouse input; DOM access locates and validates the real hit area.
async function click(selector, fraction = 0.5) {
  await until(selector => !!document.querySelector(selector), selector, selector);
  await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }), selector);
  await settle();
  const point = await evaluate((selector, fraction) => {
    const element = document.querySelector(selector), box = element.getBoundingClientRect();
    const point = { x: Math.round(box.left + box.width * fraction), y: Math.round(box.top + box.height / 2) };
    if (!element.contains(document.elementFromPoint(point.x, point.y))) throw new Error(`Occluded target: ${selector}`);
    return point;
  }, selector, fraction);
  await main.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
  for (const type of ['mousePressed', 'mouseReleased']) {
    await main.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type, button: 'left', buttons: type === 'mousePressed' ? 1 : 0, clickCount: 1, ...point });
  }
  await frames();
}
// Stable restored message anchors keep selectors independent of surrounding turns.
const message = index => `#user-msg-peer-main-turn-${String(index + 1).padStart(4, '0')}-peer-main-turn-${String(index + 1).padStart(4, '0')}-item-1`;
const card = index => `${message(index)} .session-message-source`;
const artifactCard = index => `.cached-conversation-pane[data-active="true"][data-thread-id="${artifactSessions[index - 5].id}"] [data-wuu-component="turn-artifacts"]`;
const mainInput = '[data-main-conversation-composer] .composer textarea';
const splitInput = id => `.conversation-split-pane[data-thread-id="${id}"] .composer textarea`;
async function input(selector, text) {
  await click(selector);
  await until(selector => document.activeElement === document.querySelector(selector), 'native input focus', selector);
  await evaluate(selector => document.querySelector(selector).select(), selector);
  await main.webContents.insertText(text);
  await until((selector, text) => document.querySelector(selector)?.value === text, 'native draft insertion', selector, text);
}
async function openConversation(id, title) {
  await until(() => {
    const input = document.querySelector('.composer textarea');
    return input && !input.disabled && !input.readOnly;
  }, 'ready conversation shell');
  key('P', [process.platform === 'darwin' ? 'meta' : 'control']);
  await until(() => document.activeElement === document.querySelector('.conversation-search-dialog input'), 'conversation search focus');
  // Reuse the observable request/result barrier from conversation-search-e2e.
  // A visible title may be a retained result while a new query is debouncing;
  // the product correctly refuses that stale result until its request settles.
  const current = await evaluate(() => document.querySelector('.conversation-search-dialog input').value);
  if (current === title) {
    await evaluate(() => document.querySelector('.conversation-search-dialog input').select());
    await main.webContents.insertText('acceptance-query-reset');
    await until(() => document.querySelector('.conversation-search-dialog input')?.value === 'acceptance-query-reset', 'distinct search edit');
    await frames();
  }
  await evaluate(() => document.querySelector('.conversation-search-dialog input').select());
  const offset = rpc.length;
  await main.webContents.insertText(title);
  await until(title => document.querySelector('.conversation-search-dialog input')?.value === title, 'native search query', title);
  await hostUntil(() => rpc.slice(offset).some(entry => entry.channel === 'wuu:thread-search' && entry.query === title && entry.completed), 'completed exact search request');
  const response = rpc.slice(offset).filter(entry => entry.channel === 'wuu:thread-search' && entry.query === title && entry.completed).at(-1);
  assert.ok(response.results.some(result => result.id === id), 'Search must return the intended fixture conversation');
  await until(({ title, count }) => {
    const results = document.querySelector('.conversation-search-results');
    const buttons = [...document.querySelectorAll('.conversation-search-result')];
    return results?.getAttribute('aria-busy') === 'false'
      && document.querySelector('.conversation-search-dialog input')?.value === title
      && buttons.length === count
      && buttons.some(button => button.querySelector('.conversation-search-result-title')?.textContent === title);
  }, 'current search results committed', { title, count: response.results.length });
  await settle();
  // The input's existing ArrowDown/Enter handler is the normal keyboard search
  // flow. Select the exact current row without clicking during a layout change.
  await evaluate(() => document.querySelector('.conversation-search-dialog input').focus());
  const selected = await evaluate(title => [...document.querySelectorAll('.conversation-search-result')].findIndex(button =>
    button.querySelector('.conversation-search-result-title')?.textContent === title), title);
  // Every fixture title is unique. Home is not a search shortcut, so walk from
  // the currently selected row using the same native ArrowDown path as users.
  const currentIndex = await evaluate(() => [...document.querySelectorAll('.conversation-search-result')].findIndex(button => button.getAttribute('aria-selected') === 'true'));
  for (let count = (selected - currentIndex + response.results.length) % response.results.length; count > 0; count--) {
    key('Down');
    await frames();
  }
  await until(title => document.querySelector('.conversation-search-result[aria-selected="true"] .conversation-search-result-title')?.textContent === title, 'exact selected search result', title);
  // Search handles Enter on keyDown and prevents default; native buttons tested
  // below use the complete character sequence instead.
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Enter' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Enter' });
  await until(id => document.querySelector('.cached-conversation-pane[data-active="true"]')?.dataset.threadId === id
    && !document.querySelector('.conversation-search-dialog'), 'active fixture conversation', id);
  await settle();
}

async function openMain() {
  await openConversation('peer-main', 'Peer collaboration');
  await until(selector => !!document.querySelector(selector), 'restored incoming cards', card(0));
}
async function openArtifactScene(index) {
  const scene = artifactSessions[index - 5];
  await openConversation(scene.id, scene.title);
  await until(selector => !!document.querySelector(selector), 'latest delivered artifact card', artifactCard(index));
}

async function settle() {
  await evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations().filter(animation => animation.playState === 'running'
      && Number.isFinite(animation.effect.getComputedTiming().endTime)
      && !(animation.effect.target instanceof Element && animation.effect.target.closest('[inert]')))
      .map(animation => animation.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
}
async function capture(name) {
  await settle();
  const file = `${name}.png`;
  fs.writeFileSync(path.join(output, file), (await main.webContents.capturePage()).toPNG());
  screenshots.push(file);
}
async function checkCardGeometry(index, name, title) {
  const geometry = await evaluate(selector => {
    const button = document.querySelector(selector), box = button.getBoundingClientRect();
    const block = button.closest('.user-message-block'), blockBox = block.getBoundingClientRect();
    const pane = button.closest('.conversation-split-pane, .conversation-pane').getBoundingClientRect();
    const children = [...button.children].map(child => ({ tag: child.tagName, box: child.getBoundingClientRect().toJSON(), text: child.textContent }));
    return { viewport: { width: innerWidth, height: innerHeight, devicePixelRatio }, box: box.toJSON(), block: blockBox.toJSON(), pane: pane.toJSON(),
      scrollWidth: button.scrollWidth, clientWidth: button.clientWidth, children,
      name: button.textContent, accessibleName: button.getAttribute('aria-label') };
  }, card(index));
  assert.ok(geometry.box.left >= 0 && geometry.box.right <= geometry.viewport.width + 1, 'Long-title card must fit the window');
  assert.ok(geometry.box.left >= geometry.block.left - 1 && geometry.box.right <= geometry.block.right + 1, 'Card must fit its message');
  assert.ok(geometry.box.left >= geometry.pane.left - 1 && geometry.box.right <= geometry.pane.right + 1, 'Card must not overlap the neighboring pane');
  assert.ok(geometry.scrollWidth <= geometry.clientWidth + 1, 'Long title must not create horizontal card overflow');
  assert.ok(geometry.accessibleName.includes(title), 'Truncation must preserve the complete accessible title');
  for (const child of geometry.children) assert.ok(child.box.left >= geometry.box.left - 1 && child.box.right <= geometry.box.right + 1, 'Card content remains bounded');
  for (let i = 1; i < geometry.children.length; i++) assert.ok(geometry.children[i].box.left >= geometry.children[i - 1].box.right - 1, 'Source text and icons must not overlap');
  await capture(name);
  pass(name, geometry);
}
function startRecording() {
  const directory = path.join(output, 'recording');
  fs.mkdirSync(directory, { recursive: true });
  const started = performance.now();
  let busy = false;
  recordingTimer = setInterval(() => {
    if (busy) return;
    busy = true;
    pendingFrame = (async () => {
      const elapsedMs = Math.round(performance.now() - started);
      const image = await main.webContents.capturePage();
      const file = `recording/frame-${String(recordings.length).padStart(5, '0')}.png`;
      fs.writeFileSync(path.join(output, file), image.toPNG());
      recordings.push({ file, elapsedMs });
    })().finally(() => { busy = false; });
  }, 125);
}
async function stopRecording() {
  clearInterval(recordingTimer);
  await pendingFrame;
  fs.writeFileSync(path.join(output, 'recording.json'), JSON.stringify({ boundary, frames: recordings }, null, 2));
}
async function closeSplit() {
  await click('.conversation-split-close');
  await until(() => !document.querySelector('.conversation-split-pane'), 'split closed');
}
async function setPresentation(theme, size) {
  await evaluate(async (theme, size) => {
    const themeResult = await window.wuu.setThemePreference(theme);
    const sizeResult = await window.wuu.setMessageFlowFontSize(size);
    if (!themeResult.ok || !sizeResult.ok) throw new Error('Preference update rejected');
  }, theme, size);
  const loaded = new Promise(resolve => main.webContents.once('did-finish-load', resolve));
  main.webContents.reload(); await loaded;
  await until((theme, size) => document.documentElement.dataset.theme === theme &&
    Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size')) === size,
  'persisted theme and message font', theme, size);
}
async function captureVisualMatrix() {
  for (const theme of ['light', 'dark']) for (const size of [14, 20]) {
    await setPresentation(theme, size);
    await openMain();
    for (const width of [1380, 820]) {
      await openMain();
      main.setContentSize(width, 960);
      await until(width => innerWidth === width, 'window size', width);
      await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }), card(1));
      await settle();
      await checkCardGeometry(1, `long-title-${theme}-${size}px-${width}`, longTitle);
      for (const index of [5, 6, 7]) {
        await openArtifactScene(index);
        await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }), artifactCard(index));
        await settle();
        const geometry = await evaluate(selector => {
          const card = document.querySelector(selector), bounds = card.getBoundingClientRect();
          const pane = card.closest('.conversation-pane').getBoundingClientRect();
          return { bounds: bounds.toJSON(), pane: pane.toJSON(), clientWidth: card.clientWidth, scrollWidth: card.scrollWidth,
            buttons: [...card.querySelectorAll('button')].map(button => ({ label: button.getAttribute('aria-label'), box: button.getBoundingClientRect().toJSON() })),
            nestedButtons: card.querySelectorAll('button button').length };
        }, artifactCard(index));
        assert.ok(geometry.bounds.left >= geometry.pane.left - 1 && geometry.bounds.right <= geometry.pane.right + 1, 'Artifact card fits its conversation pane');
        assert.ok(geometry.scrollWidth <= geometry.clientWidth + 1, 'Long artifact names must not overflow');
        assert.equal(geometry.nestedButtons, 0, 'Artifact rows retain independent actions');
        assert.equal(geometry.buttons.length, index === 7 ? 3 : 1);
        for (const button of geometry.buttons) assert.ok(button.box.left >= geometry.bounds.left - 1 && button.box.right <= geometry.bounds.right + 1, 'Artifact action fits its card');
        const scene = `${index === 5 ? 'zip' : index === 6 ? 'patch' : 'multiple'}-cards-${theme}-${size}px-${width}`;
        await capture(scene); pass(scene, geometry);
      }
    }
    // A short body must not let an unbroken source title set the grid's
    // intrinsic width. Exercise the actual narrow split, not a component mock.
    await openMain();
    await click(card(4));
    await until(() => !!document.querySelector('.conversation-split-pane[data-thread-id="peer-cjk"]'), 'CJK source split');
    await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }), card(4));
    await settle();
    await checkCardGeometry(4, `short-body-cjk-split-${theme}-${size}px-820`, cjkTitle);
    await closeSplit();
  }
}
app.on('browser-window-created', (_event, win) => { main ||= win; });
app.on('quit', () => fs.rmSync(fixture, { recursive: true, force: true }));
const watchdog = setTimeout(() => { log('Global acceptance deadline exceeded'); app.exit(1); }, 180000);

async function run() {
  log(boundary);
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', cwd], {
    env: process.env, input: '{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{}}\n', encoding: 'utf8', timeout: 30000,
  });
  assert.equal(boot.status, 0, boot.stderr || String(boot.error));
  const seed = spawnSync('python3', ['-c', `
import json, sqlite3, sys
from pathlib import Path
home=Path(sys.argv[1]); p=json.loads((home/'projects.json').read_text())['projects'][0]
db=sqlite3.connect(home/'sessions/sessions.sqlite3')
db.execute('DELETE FROM session_messages'); db.execute('DELETE FROM sessions')
for s in json.loads(sys.argv[2]):
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries) VALUES (?,?,?,?,?,?,?,?,?)', (s['id'],'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',s['title'],p['path'],p['id'],'fixture','fixture',len(s['messages'])*2))
    seq=0
    for i,m in enumerate(s['messages']):
        seq+=1
        peer=bool(m.get('origin'))
        db.execute('INSERT INTO session_messages (session_id,seq,role,content,display_content,origin,presentation_kind,related_session_id,read_only,name,at) VALUES (?,?,?,?,?,?,?,?,?,?,?)', (s['id'],seq,'user',('Delivery context\\n\\n'+m['text']) if peer else m['text'],m['text'] if peer else '',m.get('origin',''),'session_message' if peer else '',m.get('related',''),int(peer),m.get('name',''),'2026-01-01T00:00:00Z'))
        if m.get('artifacts'):
            call='deliver-'+str(i)
            seq+=1
            db.execute('INSERT INTO session_messages (session_id,seq,role,tool_calls_json,at) VALUES (?,?,?,?,?)', (s['id'],seq,'assistant',json.dumps([{'id':call,'name':'present_artifact','arguments':'{}'}]),'2026-01-01T00:00:00Z'))
            seq+=1
            db.execute('INSERT INTO session_messages (session_id,seq,role,content,tool_call_id,name,tool_result_json,at) VALUES (?,?,?,?,?,?,?,?)', (s['id'],seq,'tool','Delivered files.',call,'present_artifact',json.dumps({'content':m['artifacts']}),'2026-01-01T00:00:00Z'))
        seq+=1
        db.execute('INSERT INTO session_messages (session_id,seq,role,content,at) VALUES (?,?,?,?,?)', (s['id'],seq,'assistant','Acknowledged.','2026-01-01T00:00:00Z'))
    db.execute('UPDATE sessions SET entries=? WHERE id=?', (seq,s['id']))
db.commit()
`, home, JSON.stringify(sessions)], { encoding: 'utf8' });
  assert.equal(seed.status, 0, seed.stderr);
  await import(pathToFileURL(mainBundle).href);
  await hostUntil(() => main, 'bundled main window');
  main.webContents.on('console-message', ({ level, message }) => { if (level >= 2) log(`RENDERER ${message}`); });
  main.show(); main.focus(); main.setContentSize(1380, 960);
  main.webContents.setBackgroundThrottling(false);
  main.webContents.debugger.attach('1.3');
  await openMain();
  const restored = await evaluate(() => window.wuu.resumeThread('peer-main'));
  assert.deepEqual(restored.thread.turns.flatMap(turn => turn.items).filter(item => item.presentation_kind === 'session_message')
    .map(item => ({ text: item.text, origin: item.origin, target: item.related_session_id || '' })),
  messages.map(item => ({ text: item.text, origin: item.origin, target: item.related })));
  pass('incoming request/reply and missing-target metadata restored through Go');

  // Save the default product presentation before the interaction checks so a
  // later failure still leaves useful, exact-build artifact-card evidence.
  for (const index of [5, 6, 7]) {
    await openArtifactScene(index);
    await evaluate(() => document.querySelector('.cached-conversation-pane[data-active="true"] .session-message-source').scrollIntoView({ block: 'start', behavior: 'instant' }));
    await capture(`${index === 5 ? 'zip' : index === 6 ? 'patch' : 'multiple'}-cards-light-14px-initial`);
  }
  // Independent visual evidence must survive a later behavioral assertion.
  await captureVisualMatrix();
  await setPresentation('light', 14);
  main.setContentSize(1380, 960);
  await until(() => innerWidth === 1380, 'restored interaction viewport');
  await openMain();

  await input(mainInput, 'Keep my primary draft');
  await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }), card(0));
  await capture('request-card-light');
  startRecording();
  await click(card(0), 0.93);
  await until(() => document.querySelectorAll('.conversation-split-pane').length === 2, 'source conversation split');
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, splitInput('peer-main')), 'Keep my primary draft');
  await input(splitInput('peer-request'), 'Keep my source draft');
  await capture('request-open-split');
  await click(card(0), 0.12);
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, splitInput('peer-request')), 'Keep my source draft');
  await closeSplit();
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, mainInput), 'Keep my primary draft');
  pass('whole-card native clicks and repeated opening preserve both split drafts');

  await evaluate(selector => { document.querySelector(selector).scrollIntoView({ block: 'center', behavior: 'instant' }); document.querySelector(selector).focus(); }, card(0));
  key('Tab');
  await until(selector => document.activeElement !== document.querySelector(selector), 'native Tab leaves source card', card(0));
  key('Tab', ['shift']);
  await until(selector => {
    const button = document.querySelector(selector);
    return document.activeElement === button && button.matches(':focus-visible');
  }, 'native Shift+Tab exposes source focus ring', card(0));
  await capture('request-keyboard-focus');
  key('Return');
  await until(() => !!document.querySelector('.conversation-split-pane[data-thread-id="peer-request"]'), 'Enter opens related session');
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, splitInput('peer-request')), 'Keep my source draft');
  await closeSplit();
  await evaluate(selector => document.querySelector(selector).focus(), card(0));
  key('Space');
  await until(() => !!document.querySelector('.conversation-split-pane[data-thread-id="peer-request"]'), 'Space opens related session');
  await closeSplit();
  pass('native Enter/Space open the source and recover its draft');

  assert.equal(await evaluate(selector => document.querySelector(selector)?.getAttribute('aria-expanded'), `${message(1)} .user-message-expand-toggle`), 'false');
  clipboard.clear();
  const copySelector = `${message(1)} .message-copy-button`;
  const describeText = text => ({ text, utf16Length: text.length, utf8Length: Buffer.byteLength(text),
    sha256: createHash('sha256').update(text).digest('hex') });
  const copyEvidence = { expectedSource: 'longReply, verified unchanged in the real Go resume result',
    expected: describeText(longReply), samples: [] };
  const sampleClipboard = async label => {
    const text = await clipboard.readText();
    let firstDifference = 0;
    while (firstDifference < text.length && firstDifference < longReply.length && text[firstDifference] === longReply[firstDifference]) firstDifference++;
    copyEvidence.samples.push({ label, elapsedMs: Math.round(performance.now() - copyStarted),
      actual: describeText(text), firstDifference: text === longReply ? null : firstDifference,
      formats: (await clipboard.read()).flatMap(item => item.types),
      selectionText: process.platform === 'linux' ? await clipboard.selection.readText() : undefined,
      windowFocused: main.isFocused(), renderer: await evaluate(selector => ({
        documentFocused: document.hasFocus(), activeElement: document.activeElement?.outerHTML,
        control: document.querySelector(selector)?.outerHTML,
      }), copySelector) });
    fs.writeFileSync(path.join(output, 'clipboard-evidence.json'), JSON.stringify(copyEvidence, null, 2));
  };
  const copyStarted = performance.now();
  await evaluate(selector => document.querySelector(selector).focus(), copySelector);
  await sampleClipboard('before native copy click');
  await click(copySelector);
  await sampleClipboard('immediately after native copy click');
  await delay(100);
  await sampleClipboard('100ms after native copy click');
  try {
    // Electron 44 clipboard reads are asynchronous. Compare the resolved native
    // string, never the Promise, with the exact restored source text.
    await hostUntil(async () => await clipboard.readText() === longReply, 'complete reply copied to native clipboard');
    await sampleClipboard('exact native clipboard match');
    pass('collapsed copy retains the complete reply in the native clipboard');
  } catch (error) {
    await sampleClipboard('exact native clipboard check failed');
    copyEvidence.rendererRead = await evaluate(async () => {
      const result = await Promise.race([
        Promise.resolve().then(() => navigator.clipboard.readText()).then(text => ({ text }), error => ({ error: String(error) })),
        new Promise(resolve => setTimeout(() => resolve({ error: 'Renderer clipboard read timed out after 1000ms' }), 1000)),
      ]);
      return { ...result, documentFocused: document.hasFocus() };
    });
    fs.writeFileSync(path.join(output, 'clipboard-evidence.json'), JSON.stringify(copyEvidence, null, 2));
    failures.push({ name: 'complete reply copied to native clipboard', error: String(error.stack || error), evidence: 'clipboard-evidence.json' });
    log(`FAIL complete reply copied to native clipboard; see clipboard-evidence.json`);
    // Keep the exact assertion as a final red gate while unrelated expansion,
    // navigation and file checks continue. Never substitute a mocked clipboard.
  }
  assert.equal(await evaluate(() => !!document.querySelector('.conversation-split-pane')), false);
  await click(`${message(1)} .user-message-expand-toggle`);
  await until(selector => document.querySelector(selector)?.getAttribute('aria-expanded') === 'true', 'full reply expanded', `${message(1)} .user-message-expand-toggle`);
  assert.equal(await evaluate(selector => document.querySelector(selector)?.textContent, `${message(1)} .user-message-raw-query`), longReply);
  await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'start', behavior: 'instant' }), card(1));
  await capture('reply-expanded-light');
  assert.equal(await evaluate(() => !!document.querySelector('.conversation-split-pane')), false);
  await click(`${message(1)} .user-message-expand-toggle`);
  pass('expansion retains complete reply; expansion and copy do not navigate');
  await click(card(1));
  await until(() => !!document.querySelector('.conversation-split-pane[data-thread-id="peer-reply"]'), 'reply source split');
  assert.equal(await evaluate(selector => document.querySelector(selector)?.value, splitInput('peer-main')), 'Keep my primary draft');
  await capture('reply-open-split');
  await closeSplit();
  pass('reply card navigates to its own source and retains the primary draft');

  for (const index of [2, 3]) {
    assert.equal(await evaluate(selector => document.querySelector(selector)?.disabled, card(index)), true);
    const requestsBefore = rpc.filter(entry => entry.channel === 'wuu:thread-resume').length;
    await click(card(index));
    await frames();
    assert.equal(await evaluate(() => !!document.querySelector('.conversation-split-pane')), false);
    assert.equal(rpc.filter(entry => entry.channel === 'wuu:thread-resume').length, requestsBefore);
    await capture(`unavailable-source-${index}`);
  }
  pass('missing target and empty source are visibly disabled and cannot navigate');

  for (const [messageIndex, fileIndex] of [[5, 0], [6, 1]]) {
    await openArtifactScene(messageIndex);
    await click(`${artifactCard(messageIndex)} .turn-edit-summary-overview`);
    await until(() => !!document.querySelector('.artifact-preview-panel'), 'managed artifact preview');
    if (fileIndex === 1) {
      await until(text => document.querySelector('.artifact-preview-text')?.textContent === text, 'complete patch preview', patchText);
    } else {
      assert.equal(await evaluate(() => !!document.querySelector('.artifact-preview-empty')), true, 'Unsupported ZIP retains the honest preview fallback');
    }
    await capture(fileIndex ? 'patch-preview' : 'zip-preview');
    requestedDownload = files[fileIndex];
    const savedPath = path.join(fixture, 'saved-' + requestedDownload.name);
    await click('.artifact-preview-actions button[aria-label^="Download"]');
    await hostUntil(() => fs.existsSync(savedPath), 'saved original artifact bytes');
    assert.deepEqual(fs.readFileSync(savedPath), files[fileIndex].bytes, 'Download must preserve original artifact bytes');
    requestedDownload = undefined;
    await click('.artifact-preview-actions button[aria-label="Close"]');
    await until(() => !document.querySelector('.artifact-preview-panel'), 'artifact preview closed');
  }
  pass('ZIP fallback and patch preview preserve complete downloads through managed protocol',
    files.slice(0, 2).map(file => ({ name: file.name, size: file.bytes.length, sha256: createHash('sha256').update(file.bytes).digest('hex') })));
  await stopRecording();

  assert.ok(!rpc.some(entry => ['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(entry.channel)), 'No inference attempted');
  const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed: failures.length === 0, failures, boundary, recordedAt: new Date().toISOString(),
    source: spawnSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).stdout.trim(), versions: process.versions,
    hashes: { harness: hash(__filename), core: hash(process.env.WUU_DESKTOP_CORE), main: hash(mainBundle), preload: hash(path.join(desktop, 'out/preload/index.cjs')) },
    checks, screenshots, recordings, rpc, savedDownloads: savedDownloads.map(({ name }) => ({ name })),
    limitations: ['Messages are seeded fixtures, not live cross-agent delivery.', 'The native save picker is replaced with a disposable test destination; downloaded bytes and the production save implementation are verified.', 'Linux Electron screenshots do not validate macOS-specific rendering.', 'Geometry checks and screenshots require human visual review.'],
  }, null, 2));
  assert.equal(failures.length, 0, failures.map(failure => failure.name).join('; '));
  log(`PASS ${checks.length} checks; ${screenshots.length} screenshots; ${recordings.length} recorded frames`);
  clearTimeout(watchdog);
  app.quit();
}
run().catch(async error => {
  clearTimeout(watchdog);
  await stopRecording().catch(() => {});
  log(String(error.stack || error));
  if (main && !main.isDestroyed()) {
    try {
      fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
      fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify(await evaluate(() => ({
        cards: [...document.querySelectorAll('.session-message-source')].map(node => node.outerHTML),
        activeElement: document.activeElement?.outerHTML,
        search: document.querySelector('.conversation-search-dialog')?.outerHTML,
        focusVisible: document.activeElement?.matches(':focus-visible'),
        panes: [...document.querySelectorAll('.conversation-split-pane, .cached-conversation-pane')].map(node => ({ id: node.dataset.threadId, active: node.dataset.active })),
        inputs: [...document.querySelectorAll('.composer textarea')].map(node => ({ text: node.value, rect: node.getBoundingClientRect().toJSON() })),
      })), null, 2));
    } catch (captureError) { log(String(captureError)); }
  }
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), boundary, checks, failures, screenshots, rpc }, null, 2));
  app.exit(1);
});
