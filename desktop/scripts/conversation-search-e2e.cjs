// Real bundled Electron/main/preload/Go acceptance fixture for conversation search.
// Run after implementation: npm --prefix desktop run test:e2e:conversation-search
// Or build core/desktop first and run electron scripts/conversation-search-e2e.cjs.
// WUU_DESKTOP_CORE selects a built core; WUU_SEARCH_OUTPUT selects evidence only.
// No inference, mocks, product CSS overrides, credentials, or persistent user
// data are used.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_SEARCH_OUTPUT || path.join(desktop, '../.amp/in/artifacts/conversation-search'));
const mainBundle = path.join(desktop, 'out/main/index.js');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-conversation-search-'));
const home = path.join(fixture, 'home');
fs.mkdirSync(home);
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
// Also isolate credential discovery by built-in engines, not just Wuu settings.
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

const projects = ['Alpha', 'Beta'].map((name, index) => {
  const cwd = path.join(fixture, `project-${index}`);
  fs.mkdirSync(cwd);
  return { id: `search-project-${index}`, name: `Search ${name}`, path: cwd,
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
});
const longTitle = ('History review — ' + 'long searchable conversation title with wrapping and truncation '.repeat(5)).trim();
const oldNeedle = 'cobaltneedle-old-answer';
const lateNeedle = 'sapphire oversized tail';
const archivedNeedle = 'cobaltneedle-archived-answer';
const archivedAt = '2026-01-02T00:00:00Z';
const sessions = [
  { id: 'search-alpha', title: 'Shared planning', project: 0, turns: 3, updated: '2026-01-04T00:00:00Z' },
  { id: 'search-beta', title: 'Shared planning', project: 1, turns: 3, updated: '2026-01-03T00:00:00Z' },
  { id: 'search-alpha-older', title: 'Shared planning', project: 0, turns: 3, updated: '2025-11-03T09:00:00Z' },
  { id: 'search-history', title: longTitle, project: 1, turns: 140, updated: '2026-01-05T00:00:00Z', needle: oldNeedle, matchTurn: 5, lateNeedle },
  { id: 'search-pinned', title: 'Pinned checklist', project: 0, turns: 3, updated: '2026-01-01T00:00:00Z', pinned: '2026-01-02T00:00:00Z' },
  { id: 'search-recent', title: 'Recent notes', project: 0, turns: 3, updated: '2026-01-06T00:00:00Z' },
  { id: 'search-archive', title: 'Archived investigation', project: 0, turns: 4, updated: '2026-01-07T00:00:00Z', archived: archivedAt, needle: archivedNeedle, matchTurn: 2 },
  { id: 'search-process', title: 'Implementation notes', project: 0, turns: 2, updated: '2026-01-02T00:00:00Z', needle: 'opal-process-detail', matchTurn: 2, process: true },
  { id: 'search-user', title: 'Long request', project: 0, turns: 2, updated: '2026-01-02T00:00:00Z', user: true },
];
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
  default_provider: 'fixture',
  providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic-not-a-credential', model: 'fixture' } },
  engines: Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }])),
}));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects,
  active_context: { kind: 'project', project_id: projects[0].id, cwd: projects[0].path } }));
// Explicit 14px reference, rather than depending on a future product default.
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({
  onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 14,
}));

const rpc = [];
const checks = [];
const screenshots = [];
const log = message => {
  console.log(message);
  fs.appendFileSync(path.join(output, 'run.log'), `${message}\n`);
};
fs.writeFileSync(path.join(output, 'run.log'), 'Conversation search synthetic E2E\n');
const recordCheck = check => { checks.push(check); log(`PASS ${check.name}`); };
// Record actual handlers without replacing results. Reject inference even if an
// Enter key accidentally escapes the palette into the composer.
const originalHandle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => originalHandle(channel, async (...args) => {
  const entry = { channel, args: args.slice(1), completed: false };
  rpc.push(entry);
  try {
    assert.ok(!['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(channel), 'Fixture must never invoke inference');
    const result = await handler(...args);
    if (channel === 'wuu:thread-search') entry.result = result;
    entry.completed = true;
    return result;
  } catch (error) {
    entry.error = String(error);
    throw error;
  }
});

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = async (win, fn, arg) => {
  const result = await win.webContents.executeJavaScript(`(async () => {
    try { return { value: await (${fn})(${JSON.stringify(arg)}) }; }
    catch (error) { return { error: String(error.stack || error) }; }
  })()`);
  if (result.error) throw new Error(result.error);
  return result.value;
};
// Poll observable state; intervals/deadlines are not assertions about speed.
async function waitFor(win, fn, arg, label = String(fn)) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const result = await evaluate(win, fn, arg);
    if (result) return result;
    await delay(25);
  }
  throw new Error(`Timed out: ${label}`);
}
async function waitHost(fn, label) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    if (fn()) return;
    await delay(25);
  }
  throw new Error(`Timed out: ${label}`);
}
const frames = win => evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
function key(win, keyCode, modifiers = []) {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
}
async function open(win) {
  key(win, 'P', [process.platform === 'darwin' ? 'meta' : 'control']);
  await waitFor(win, () => {
    const dialog = document.querySelector('.conversation-search-dialog');
    return dialog?.querySelector('input') === document.activeElement;
  });
}
async function close(win) {
  key(win, 'Escape');
  await waitFor(win, () => !document.querySelector('.conversation-search-dialog'));
}
async function query(win, text) {
  let current = await evaluate(win, () => {
    const input = document.querySelector('.conversation-search-dialog input');
    input.focus();
    input.select();
    return input.value;
  });
  // A no-op edit need not produce a request. Force a distinct committed value
  // when reopening an empty palette or repeating a query after composition.
  if (current === text) {
    await win.webContents.insertText('synthetic-query-reset');
    await waitFor(win, () => document.querySelector('.conversation-search-dialog input')?.value === 'synthetic-query-reset');
    await frames(win);
    await evaluate(win, () => document.querySelector('.conversation-search-dialog input').select());
    current = 'synthetic-query-reset';
  }
  const offset = rpc.length;
  // Do not queue deletion against an already-empty input: a delayed native
  // Backspace could otherwise remove the last character of insertText below.
  if (current) {
    key(win, 'Backspace');
    await waitFor(win, () => document.querySelector('.conversation-search-dialog input')?.value === '');
  }
  if (text) await win.webContents.insertText(text);
  await waitFor(win, text => document.querySelector('.conversation-search-dialog input')?.value === text, text);
  await waitHost(() => rpc.slice(offset).some(r => r.channel === 'wuu:thread-search' && r.args[0] === text && r.completed), `search response for ${JSON.stringify(text)}`);
  const response = rpc.slice(offset).filter(r => r.channel === 'wuu:thread-search' && r.args[0] === text && r.completed).at(-1).result;
  const expected = response.results.map(r => ({ title: r.thread.title, project: projects.find(p => p.path === r.thread.cwd)?.name }));
  await waitFor(win, expected => {
    const buttons = [...document.querySelectorAll('.conversation-search-result')];
    if (buttons.length !== expected.length) return false;
    if (!buttons.length) return !!document.querySelector('.conversation-search-empty:not([data-loading])');
    return buttons.every((button, i) =>
      button.querySelector('.conversation-search-result-title')?.textContent === expected[i].title &&
      button.querySelector('.conversation-search-result-context')?.textContent.includes(expected[i].project));
  }, expected, `rendered results for ${JSON.stringify(text)}`);
  await frames(win);
  return response.results;
}
async function clickResult(win, title, project) {
  const point = await evaluate(win, async ({ title, project }) => {
    const button = [...document.querySelectorAll('.conversation-search-result')].find(button =>
      button.querySelector('.conversation-search-result-title')?.textContent === title &&
      button.querySelector('.conversation-search-result-context')?.textContent.includes(project));
    if (!button || button.tagName !== 'BUTTON') throw new Error('Missing result button');
    button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
    await Promise.all(button.getAnimations({ subtree: true }).map(a => a.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    const r = button.getBoundingClientRect();
    const point = { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
    if (!button.contains(document.elementFromPoint(point.x, point.y))) throw new Error('Result click target is occluded');
    return point;
  }, { title, project });
  for (const type of ['mousePressed', 'mouseReleased']) {
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type, button: 'left', clickCount: 1, ...point });
  }
}
async function active(win, id) {
  await waitFor(win, id => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    return pane?.getAttribute('data-thread-id') === id && !pane.closest('[inert]') && !document.querySelector('.conversation-search-dialog');
  }, id);
}
const anchor = (session, turn) => {
  const turnID = `${session}-turn-${String(turn).padStart(4, '0')}`;
  return `message-${turnID}-${turnID}-item-2`;
};
async function armFlash(win) {
  await waitFor(win, () => !document.querySelector('.user-message-jump-flash'));
  await evaluate(win, () => {
    window.__searchFlashes = [];
    window.__searchFlashObserver?.disconnect();
    const collect = () => {
      for (const node of document.querySelectorAll('.user-message-jump-flash')) {
        if (!window.__searchFlashes.some(entry => entry.id === node.id)) {
          const animations = node.getAnimations({ subtree: true }).map(animation => ({
            duration: animation.effect.getComputedTiming().duration,
            backgrounds: animation.effect.getKeyframes().map(frame => frame.backgroundColor).filter(Boolean),
          }));
          window.__searchFlashes.push({ id: node.id, tag: node.tagName, text: node.textContent, animations });
        }
      }
    };
    window.__searchFlashObserver = new MutationObserver(collect);
    window.__searchFlashObserver.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class'] });
  });
}
async function matchingArticle(win, session, turn, needle, sourceSelector) {
  await active(win, session);
  const id = anchor(session, turn);
  const evidence = await waitFor(win, ({ id, needle, sourceSelector }) => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    const node = document.getElementById(id);
    const viewport = document.querySelector('.conversation-pane > .scroll-region');
    const flash = window.__searchFlashes.find(entry => entry.id === id);
    if (!node || !pane?.contains(node) || !node.matches('article') || !needle.split(/\s+/).every(word => node.textContent.includes(word)) || !flash || !viewport) return false;
    const r = node.getBoundingClientRect(), v = viewport.getBoundingClientRect();
    if (r.height <= 0 || r.bottom <= v.top || r.top >= v.bottom || r.right <= v.left || r.left >= v.right) return false;
    // An enormous article can intersect the viewport while the matching text
    // remains offscreen. Check the actual needle, including late full-body hits.
    const source = sourceSelector ? node.querySelector(sourceSelector) : node;
    const walker = document.createTreeWalker(source, NodeFilter.SHOW_TEXT);
    let textNode;
    const words = needle.split(/\s+/), matches = [];
    while ((textNode = walker.nextNode())) {
      let offset = 0;
      while (matches.length < words.length) {
        const word = words[matches.length];
        const index = textNode.textContent.indexOf(word, offset);
        if (index < 0) break;
        const range = document.createRange();
        range.setStart(textNode, index); range.setEnd(textNode, index + word.length);
        matches.push(range.getBoundingClientRect());
        offset = index + word.length;
      }
      if (matches.length === words.length) {
        if (matches.some(match => match.height <= 0 || match.top < v.top || match.bottom > v.bottom)) return false;
        const innerViewport = source.closest('.rich-code')?.getBoundingClientRect();
        if (innerViewport && matches.some(match => match.top < innerViewport.top || match.bottom > innerViewport.bottom)) return false;
        return { id, flash, article: r.toJSON(), matches: matches.map(match => match.toJSON()), viewport: v.toJSON(), flashes: window.__searchFlashes };
      }
    }
    return false;
  }, { id, needle, sourceSelector }, `matching assistant article ${id} highlighted and in viewport`);
  assert.ok(evidence.flash.animations.some(animation => animation.duration > 0 &&
    new Set(animation.backgrounds).size > 1), 'Assistant search target must visibly animate its background');
  assert.ok(evidence.flashes.every(flash => flash.id === id), 'Search jump highlighted the wrong message (user/first/latest turn)');
  const turnID = `${session}-turn-${String(turn).padStart(4, '0')}`;
  assert.equal(await evaluate(win, id => !!document.getElementById(id), `user-msg-${turnID}-${turnID}-item-1`), true, 'Existing user message anchors must remain intact');
  const init = await evaluate(win, () => window.wuu.initialize());
  assert.equal(init.workspace_root, projects[sessions.find(s => s.id === session).project].path, 'Search must switch the actual Go workspace');
  recordCheck({ name: `message jump ${session}`, ...evidence });
  await capture(win, `${session}-${needle === lateNeedle ? "late-" : sourceSelector ? "code-" : ""}jump`);
}
async function capture(win, name) {
  await evaluate(win, async () => {
    await document.fonts.ready;
    // Cached inactive panes can pause finite transitions indefinitely.
    await Promise.all(document.getAnimations().filter(a => a.playState === 'running'
      && Number.isFinite(a.effect.getComputedTiming().endTime)
      && !(a.effect.target instanceof Element && a.effect.target.closest('[inert]')))
      .map(a => a.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const file = `${name}.png`;
  fs.writeFileSync(path.join(output, file), (await win.webContents.capturePage()).toPNG());
  screenshots.push(file);
}

let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
app.on('quit', () => fs.rmSync(fixture, { recursive: true, force: true }));
const watchdog = setTimeout(() => {
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: 'Global fixture deadline exceeded', checks, rpc }, null, 2));
  app.exit(1);
}, 360000);

async function run() {
  const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', projects[0].path], {
    env: process.env, input: '{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{}}\n', encoding: 'utf8', timeout: 30000,
  });
  assert.equal(boot.status, 0, boot.stderr || String(boot.error));
  const seed = spawnSync('python3', ['-c', `
import json, sqlite3, sys
from pathlib import Path
home=Path(sys.argv[1]); projects=json.loads((home/'projects.json').read_text())['projects']
db=sqlite3.connect(home/'sessions/sessions.sqlite3')
db.execute('DELETE FROM session_messages'); db.execute('DELETE FROM sessions')
for s in json.loads(sys.argv[2]):
    p=projects[s['project']]
    db.execute('INSERT INTO sessions (id,created_at,updated_at,title,cwd,workspace_id,provider,model,entries,pinned_at,archived_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)', (s['id'],'2026-01-01T00:00:00Z',s['updated'],s['title'],p['path'],p['id'],'fixture','fixture',s['turns']*2,s.get('pinned'),s.get('archived')))
    for t in range(1,s['turns']+1):
        user='Synthetic question '+str(t)
        if s.get('user') and t==2: user=('Long request context. '*90)+'opal-user-detail'
        answer='## Synthetic answer '+str(t)+'\\n\\n'+('Readable long content with **formatting**, without external inference. '*25)
        if t==s.get('matchTurn'):
            answer='The exact historical assistant finding is '+s['needle']+'.\\n\\n'+answer
            if s.get('lateNeedle'): answer += ('\\n\\nMore archived context. '*4000)+'\\n\\n'+'\\n\\n'.join(s['lateNeedle'].split())
        if s.get('process') and t==2: answer += '\\n\\n'+chr(96)*3+'text\\n'+('ordinary code line\\n'*1000)+'Highlight\\n'+chr(96)*3
        for r,(role,text) in enumerate([('user',user),('assistant',answer)]):
            phase='commentary' if s.get('process') and t==2 and role=='assistant' else ''
            db.execute('INSERT INTO session_messages (session_id,seq,role,content,at,phase) VALUES (?,?,?,?,?,?)', (s['id'],(t-1)*2+r+1,role,text,'2026-01-01T00:00:00Z',phase))
    if s.get('process'):
        db.execute('INSERT INTO session_messages (session_id,seq,role,content,at,phase) VALUES (?,?,?,?,?,?)', (s['id'],5,'assistant','Final summary without the process detail.','2026-01-01T00:00:00Z','final_answer'))
db.commit()
`, home, JSON.stringify(sessions)], { encoding: 'utf8' });
  assert.equal(seed.status, 0, seed.stderr);
  await import(pathToFileURL(mainBundle).href);
  await waitHost(() => main, 'actual bundled main window');
  main.webContents.on('console-message', (_event, level, message) => { if (level >= 2) log(`RENDERER ${message}`); });
  await waitFor(main, () => {
    const input = document.querySelector('.composer textarea');
    return window.wuu && input && !input.disabled && !input.readOnly;
  });
  main.show(); main.focus(); main.setSize(1380, 900);
  main.webContents.setBackgroundThrottling(false);
  main.webContents.debugger.attach('1.3');

  await evaluate(main, () => document.querySelector('.composer textarea').focus());
  await open(main);
  const empty = await query(main, '');
  assert.equal(empty.length, 8);
  assert.equal(empty[0].thread.id, 'search-pinned', 'Pinned conversation must precede recent conversations');
  assert.equal(empty[1].thread.id, 'search-recent', 'Unpinned suggestions must be recent first');
  assert.ok(empty.every(r => !r.thread.archived));
  await capture(main, 'empty-initial');
  await close(main);
  assert.equal(await evaluate(main, () => document.activeElement === document.querySelector('.composer textarea')), true, 'Escape restores opener focus');
  recordCheck({ name: 'empty pinned/recent, single column, Escape focus' });

  await open(main);
  const titles = await query(main, 'Shared planning');
  assert.equal(titles.length, 3);
  assert.equal(await evaluate(main, () => [...document.querySelectorAll('.conversation-search-result')].every(button =>
    !button.querySelector('.conversation-search-result-snippet:not(:has(time))') && button.querySelector('.conversation-search-result-context')?.textContent.trim())), true, 'Title results need distinct project context, not repeated snippets');
  const duplicates = await evaluate(main, () => [...document.querySelectorAll('.conversation-search-result time')].map(node => ({ text: node.textContent, datetime: node.dateTime })));
  assert.equal(duplicates.length, 2, 'Only same-project title collisions need a date');
  assert.notEqual(duplicates[0].text, duplicates[1].text);
  await capture(main, 'duplicate-title-results');
  recordCheck({ name: 'same-project duplicate titles have distinct dates', duplicates });
  key(main, 'Down');
  await waitFor(main, () => document.querySelectorAll('.conversation-search-result')[1]?.getAttribute('aria-selected') === 'true');
  key(main, 'Up');
  await waitFor(main, () => document.querySelectorAll('.conversation-search-result')[0]?.getAttribute('aria-selected') === 'true');
  // A native IME is platform-owned. Dispatch composition + key-229 events to
  // exercise the renderer's guard, while all ordinary navigation uses native input.
  const beforeIME = await evaluate(main, () => document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id'));
  await evaluate(main, () => {
    const input = document.querySelector('.conversation-search-dialog input');
    input.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true, data: '中' }));
    for (const key of ['ArrowDown', 'Enter']) input.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key, keyCode: 229, isComposing: true }));
  });
  await frames(main);
  assert.equal(await evaluate(main, () => !!document.querySelector('.conversation-search-dialog') &&
    document.querySelectorAll('.conversation-search-result')[0]?.getAttribute('aria-selected') === 'true'), true, 'IME must not navigate or select');
  assert.equal(await evaluate(main, () => document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id')), beforeIME);
  await evaluate(main, () => document.querySelector('.conversation-search-dialog input').dispatchEvent(new CompositionEvent('compositionend', { bubbles: true, data: '中' })));
  // Settle a fresh query after composition; Enter must still select normally.
  await query(main, 'Shared planning');
  key(main, 'Down');
  await waitFor(main, () => document.querySelectorAll('.conversation-search-result')[1]?.getAttribute('aria-selected') === 'true');
  key(main, 'Enter');
  await active(main, titles[1].thread.id);
  recordCheck({ name: 'ArrowDown/ArrowUp/Enter and composition no accidental selection' });

  await open(main); await query(main, 'History review');
  assert.equal(await evaluate(main, () => !!document.querySelector('.conversation-search-result-snippet')), false);
  key(main, 'Enter'); await active(main, 'search-history');
  await waitFor(main, () => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    return pane?.textContent.includes('Synthetic question 140');
  });
  assert.equal(await evaluate(main, id => !!document.getElementById(id), anchor('search-history', 5)), false, 'Turn 5 must be outside the initial rendered tail');
  // Leave Beta so selecting its assistant body hit exercises project switching.
  await open(main); await query(main, 'Shared planning');
  await clickResult(main, 'Shared planning', 'Search Alpha'); await active(main, 'search-alpha');
  assert.equal((await evaluate(main, () => window.wuu.initialize())).workspace_root, projects[0].path);
  await open(main);
  const bodies = await query(main, 'cobaltneedle');
  assert.equal(bodies.length, 2, 'Body search includes archived conversation');
  for (const result of bodies) {
    const session = sessions.find(s => s.id === result.thread.id);
    assert.equal(result.message_seq, session.matchTurn * 2, 'Backend must address the assistant message, not the user/first/latest turn');
  }
  assert.equal(await evaluate(main, () => [...document.querySelectorAll('.conversation-search-result')].every(button => {
    const snippet = button.querySelector('.conversation-search-result-snippet');
    return snippet?.textContent.includes('cobaltneedle') && [...snippet.querySelectorAll('mark')].some(mark => mark.textContent.toLowerCase().includes('cobaltneedle'));
  })), true, 'Body hits require matching highlighted snippets inline inside result buttons');
  assert.equal(await evaluate(main, () => [...document.querySelectorAll('.conversation-search-result')].find(button =>
    button.querySelector('.conversation-search-result-title')?.textContent === 'Archived investigation')?.querySelector('.conversation-search-result-archived')?.textContent.trim().length > 0), true, 'Archived hit needs a visible label');
  const lateHits = await query(main, lateNeedle);
  assert.equal(lateHits[0].message_seq, 10);
  await armFlash(main); key(main, 'Enter');
  await matchingArticle(main, 'search-history', 5, lateNeedle);
  recordCheck({ name: 'oversized historical body scrolls to a late multi-paragraph match' });
  await open(main); await query(main, oldNeedle); await armFlash(main);
  await clickResult(main, longTitle, 'Search Beta');
  await matchingArticle(main, 'search-history', 5, oldNeedle);

  await open(main); await query(main, archivedNeedle); await armFlash(main);
  await clickResult(main, 'Archived investigation', 'Search Alpha');
  await matchingArticle(main, 'search-archive', 2, archivedNeedle);
  const archives = await evaluate(main, () => window.wuu.listArchivedThreads());
  assert.ok(archives.threads.some(t => t.id === 'search-archive' && t.archived), 'Resume must not unarchive the result');
  const stored = spawnSync('python3', ['-c', "import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute('SELECT archived_at FROM sessions WHERE id=?', ('search-archive',)).fetchone()[0])", path.join(home, 'sessions/sessions.sqlite3')], { encoding: 'utf8' });
  assert.equal(stored.status, 0, stored.stderr);
  assert.equal(stored.stdout.trim(), archivedAt, 'Resume must preserve ArchivedAt exactly');
  recordCheck({ name: 'archived search/resume retains persisted ArchivedAt', archivedAt });

  await open(main); await query(main, 'cobaltneedle');
  await capture(main, 'archived-current-result');
  await close(main);

  await open(main); await query(main, 'opal-process-detail'); await armFlash(main);
  key(main, 'Enter');
  await matchingArticle(main, 'search-process', 2, 'opal-process-detail');
  await open(main); await query(main, 'Highlight'); await armFlash(main);
  key(main, 'Enter');
  await matchingArticle(main, 'search-process', 2, 'Highlight', '.rich-code code');
  recordCheck({ name: 'code body match wins over an identically named toolbar control' });

  await open(main);
  const userHit = await query(main, 'opal-user-detail');
  assert.equal(userHit[0].message_seq, 3);
  await armFlash(main); key(main, 'Enter'); await active(main, 'search-user');
  await waitFor(main, () => {
    const id = 'user-msg-search-user-turn-0002-search-user-turn-0002-item-1';
    const node = document.getElementById(id);
    const viewport = document.querySelector('.conversation-pane > .scroll-region');
    if (!node || !viewport || !node.textContent.includes('opal-user-detail') ||
      node.querySelector('.user-message-long-card.collapsed') || !window.__searchFlashes.some(f => f.id === id)) return false;
    const r = node.getBoundingClientRect(), v = viewport.getBoundingClientRect();
    return r.top < v.bottom && r.bottom > v.top;
  }, undefined, 'long user message expanded at its search target');
  assert.equal(await evaluate(main, () => window.__searchFlashes.some(flash =>
    flash.animations.some(animation => animation.duration > 0 && new Set(animation.backgrounds).size > 1))),
  true, 'User search target must retain its visible background animation');
  recordCheck({ name: 'long user message search reveals collapsed content' });
  await capture(main, 'long-user-jump');

  await open(main);
  assert.deepEqual(await query(main, 'no-synthetic-conversation-matches-9b6f'), []);
  await capture(main, 'no-matches');
  key(main, 'Down'); key(main, 'Enter'); await frames(main);
  assert.equal(await evaluate(main, () => !!document.querySelector('.conversation-search-dialog')), true, 'Empty results must not select a conversation');
  await close(main);
  recordCheck({ name: 'no matches / Enter no-op' });

  // Real persisted settings APIs, not CSS or fixture-only preferences. Font size
  // is applied by the settings UI/preload rather than broadcast by its setter;
  // reload the real bundle so preload reads the saved preference on first paint.
  // Capture all 2 x 2 x 2 combinations, each with empty and inline body hits.
  for (const theme of ['light', 'dark']) for (const size of [14, 20]) for (const width of [1380, 820]) {
    await evaluate(main, async ({ theme, size }) => {
      const t = await window.wuu.setThemePreference(theme);
      const s = await window.wuu.setMessageFlowFontSize(size);
      if (!t.ok || !s.ok) throw new Error('Preference update rejected');
    }, { theme, size });
    const loaded = new Promise(resolve => main.webContents.once('did-finish-load', resolve));
    main.webContents.reload();
    await loaded;
    await waitFor(main, () => window.wuu && document.querySelector('.composer textarea'));
    await waitFor(main, ({ theme, size }) => document.documentElement.dataset.theme === theme &&
      Number.parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--conversation-message-font-size')) === size,
    { theme, size });
    assert.equal(await evaluate(main, () => window.wuu.getMessageFlowFontSize()), size);
    assert.equal(await evaluate(main, () => window.wuu.getThemePreference()), theme);
    main.setSize(width, 900);
    await open(main); await query(main, '');
    for (const [state, text] of [['empty', ''], ['matching', 'cobaltneedle']]) {
      if (text) await query(main, text);
      const geometry = await evaluate(main, () => {
        const dialog = document.querySelector('.conversation-search-dialog');
        const r = dialog.getBoundingClientRect();
        const buttons = [...dialog.querySelectorAll('.conversation-search-result')];
        return { dialog: r.toJSON(), viewport: { width: innerWidth, height: innerHeight },
          rows: buttons.map(button => ({ title: button.querySelector('.conversation-search-result-title').textContent,
            rect: button.getBoundingClientRect().toJSON() })), horizontalOverflow: dialog.scrollWidth > dialog.clientWidth + 1,
          inputFontSize: getComputedStyle(dialog.querySelector('input')).fontSize };
      });
      assert.ok(geometry.dialog.left >= 0 && geometry.dialog.right <= geometry.viewport.width + 1 &&
        geometry.dialog.top >= 0 && geometry.dialog.bottom <= geometry.viewport.height + 1, 'Dialog must fit the viewport');
      assert.equal(geometry.horizontalOverflow, false, 'Long title/content must not overflow horizontally');
      assert.ok(geometry.rows.every(row => row.rect.left >= geometry.dialog.left && row.rect.right <= geometry.dialog.right + 1), 'All results belong to the same bounded column');
      assert.ok(geometry.rows.every((row, i, rows) => i === 0 || row.rect.top >= rows[i - 1].rect.bottom - 1), 'Results must stack vertically, not form multiple columns');
      const name = `${theme}-${size}px-${width}-${state}`;
      recordCheck({ name, geometry });
      await capture(main, name);
    }
    await close(main);
  }
  assert.ok(!rpc.some(r => ['wuu:turn-start', 'wuu:turn-queue', 'wuu:turn-steer'].includes(r.channel)), 'No inference attempts');
  const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ passed: true, recordedAt: new Date().toISOString(),
    versions: process.versions, platform: process.platform, source: spawnSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).stdout.trim(),
    hashes: { harness: hash(__filename), core: hash(process.env.WUU_DESKTOP_CORE), main: hash(mainBundle), preload: hash(path.join(desktop, 'out/preload/index.cjs')) },
    fixture: { sessions, projects: projects.map(p => ({ id: p.id, name: p.name })), noInference: true, safeMode: true },
    checks, screenshots, rpc,
    limitations: ['Composition events exercise the renderer guard, not a native OS IME candidate window.', 'PNG captures and bounded geometry are review evidence, not a complete visual-quality assertion.', '14px is an explicit reference preference, not an assertion about the product default.'],
  }, null, 2));
  log(`PASS ${checks.length} checks; evidence: ${output}`);
  clearTimeout(watchdog);
  app.quit();
}
run().catch(async error => {
  clearTimeout(watchdog);
  log(String(error.stack || error));
  if (main && !main.isDestroyed()) {
    try {
      fs.writeFileSync(path.join(output, 'failure-state.json'), JSON.stringify(await evaluate(main, () => ({
        active: document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id'),
        dialog: document.querySelector('.conversation-search-dialog')?.outerHTML,
        flashes: window.__searchFlashes,
        viewport: (() => {
          const node = document.querySelector('.conversation-pane > .scroll-region');
          return node && { rect: node.getBoundingClientRect().toJSON(), scrollTop: node.scrollTop,
            scrollHeight: node.scrollHeight, clientHeight: node.clientHeight, devicePixelRatio };
        })(),
      })), null, 2));
      fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
    } catch (captureError) { log(`Failure capture: ${captureError}`); }
  }
  fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error.stack || error), checks, screenshots, rpc }, null, 2));
  app.exit(1);
});
