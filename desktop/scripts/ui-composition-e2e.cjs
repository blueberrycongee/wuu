// Build desktop first. WUU_UI_COMPOSITION_CORE selects an independently built core.
// Real Electron/preload/Go, public plugin dev/package APIs, synthetic local model.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { createHash } = require('node:crypto');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const repository = path.dirname(desktop);
const output = path.resolve(process.env.WUU_UI_COMPOSITION_OUTPUT || path.join(repository, '.tmp/e2e/ui-composition'));
fs.mkdirSync(output, { recursive: true });
const run = fs.mkdtempSync(path.join(output, 'run-'));
const home = path.join(run, 'state');
const workspace = path.join(run, 'workspace');
const source = path.join(run, 'deep-ui');
for (const dir of [home, workspace]) fs.mkdirSync(dir);
fs.writeFileSync(path.join(workspace, 'evidence.txt'), 'UI composition tool evidence\n');
fs.cpSync(path.join(repository, 'examples/plugins/deep-ui'), source, { recursive: true });
// Directory dev sources run their declared build; this plain JS example only needs syntax validation.
fs.writeFileSync(path.join(source, 'package.json'), JSON.stringify({ name: 'ui-composition-fixture', private: true, type: 'module', scripts: { build: 'node --check desktop.js' } }));
// Keep the shipped UI; add lifetime observations and one native PTY per activation
// to verify asynchronous resource cleanup through the public application service.
const entry = path.join(source, 'desktop.js');
fs.writeFileSync(entry, fs.readFileSync(entry, 'utf8').replace('  const React = api.react;', `  const React = api.react;
  api.registerLocale({ id: 'toolbar-labels', locale: 'en', entries: { 'composition.tool': 'Composition tool {number}' } });
  for (const [id, target] of [['composer', 'wuu.default-composer/composer.above'], ['navigation', 'wuu.default-navigation/sidebar.footer']]) {
    api.registerUIContribution({ id: 'public-' + id, target, render({ application }) {
      const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
      return React.createElement('span', { hidden: true, ['data-public-' + id]: JSON.stringify(snapshot[id]) });
    } });
  }
  const toolbarRegistrations = [];
  for (const number of [1, 2, 3]) toolbarRegistrations.push(api.registerUIContribution({ id: 'toolbar-' + number,
    title: 'Composition tool ' + number, target: 'wuu.default-composer/composer.toolbar', order: number,
    render({ translate }) {
      const label = translate('composition.tool', { number });
      return React.createElement('button', { type: 'button', className: number < 3 ? 'composer-tool-button' : undefined,
        style: number < 3 ? undefined : { whiteSpace: 'normal', overflowWrap: 'anywhere', textAlign: 'left' },
        'aria-label': label, title: label, 'data-composition-tool': number,
        onClick: event => {
          console.log('ui-composition:tool-' + number);
          if (number === 3 && event.shiftKey) {
            toolbarRegistrations[1].dispose();
            toolbarRegistrations[2].dispose();
          }
        } }, number < 3 ? String(number) : label);
    }
  }));
  api.registerUIContribution({ id: 'foreign-sidebar', target: 'wuu.default-navigation/sidebar.primary',
    render: ({ context }) => context.viewTypeId ? React.createElement('div', { 'data-foreign-sidebar': true }, 'Wrong owner') : null });
  for (const region of ['message', 'process']) api.registerUIContribution({
    id: 'public-' + region, target: 'wuu.default-conversation/conversation.' + region, mode: 'wrap',
    render({ context, children }) {
      if (!Object.isFrozen(context.snapshot)) throw new Error('Leaf snapshot is mutable');
      return React.createElement(React.Fragment, null, children, React.createElement('span', {
        hidden: true, ['data-public-' + region]: JSON.stringify({ threadId: context.threadId, turnId: context.turnId, snapshot: context.snapshot }) }));
    }
  });
  let terminalStarted = false;
  api.signal.addEventListener('abort', () => console.log('ui-composition:abort'), { once: true });
  api.registerCleanup(() => console.log('ui-composition:provider-cleanup'));`).replace('    render({ children }) {', `    render({ children }) {
      React.useEffect(() => {
        if (terminalStarted) return;
        terminalStarted = true;
        void api.application.terminals.start({ cwd: ${JSON.stringify(workspace)}, cols: 80, rows: 24 })
          .then(() => console.log('ui-composition:terminal-ready'), error => { if (!api.signal.aborted) console.error(error); });
      }, []);
      React.useEffect(() => () => console.log('ui-composition:view-cleanup'), []);`));
// The stock information panel is feature-disabled. This external layout probe
// exercises its declared slot with actual application state, without changing
// the product flag or reaching into the renderer's private registry.
fs.writeFileSync(entry, fs.readFileSync(entry, 'utf8').replace('  const React = api.react;', `  const React = api.react;
  api.registerUIContribution({ id: 'inspector-probe', target: 'wuu.default-shell/application.after', render({ application, slots }) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
    const threadId = snapshot.conversation.threadId;
    const presentation = application.composerPresentation;
    const status = React.useSyncExternalStore(listener => presentation.subscribeStatus(threadId, listener),
      () => presentation.getStatusSnapshot(threadId), () => presentation.getStatusSnapshot(threadId));
    const items = status.todoUpdate?.todos;
    if (!items?.length) return null;
    const inspector = { contractVersion: 1, session: { id: threadId, status: 'running' },
      todo: { items, total: items.length, completed: items.filter(item => item.status === 'completed').length } };
    return React.createElement('aside', { 'data-inspector-probe': true, className: 'environment-panel',
      style: { position: 'fixed', top: 70, right: 16, width: 300, maxHeight: '60vh', overflow: 'auto', zIndex: 100 } },
      React.createElement('div', { className: 'plugin-inspector-sections' },
        React.createElement(slots.Slot, { name: 'wuu.default-shell/inspector.sections', context: { snapshot: inspector } })));
  } });`));
const core = path.resolve(process.env.WUU_UI_COMPOSITION_CORE || path.join(desktop, 'build/bin/wuu-core'));
const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
fs.writeFileSync(path.join(run, 'identity.json'), JSON.stringify({ core, core_sha256: hash(core),
  desktop: ['main', 'preload', 'renderer'].flatMap(directory => fs.readdirSync(path.join(desktop, 'out', directory), { recursive: true })
    .map(file => path.join(directory, file))).filter(file => fs.statSync(path.join(desktop, 'out', file)).isFile())
    .map(file => ({ path: file, sha256: hash(path.join(desktop, 'out', file)) })),
}, null, 2));
app.setPath('userData', path.join(run, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = core;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
const requests = [];
const pendingResponses = [];
const terminalCalls = [];
const registerHandler = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => registerHandler(channel, async (event, ...args) => {
  const result = await handler(event, ...args);
  if (channel === 'wuu:terminal-start') terminalCalls.push({ phase, action: 'start', id: result.id });
  if (channel === 'wuu:terminal-stop') terminalCalls.push({ phase, action: 'stop', id: args[0] });
  return result;
});
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', part => { body += part; });
  req.on('end', () => {
    const payload = JSON.parse(body || '{}');
    requests.push({ path: req.url, payload });
    if (req.url.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] }));
    }
    const content = 'UI composition verified: the original message remains readable.';
    if (!payload.stream) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content }, finish_reason: 'stop' }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } }));
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    if (payload.tools?.some(tool => tool.function?.name === 'read_file') && !payload.messages.some(message => message.role === 'tool')) {
      res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', tool_calls: [{ index: 0, id: 'read-evidence', type: 'function', function: { name: 'read_file', arguments: JSON.stringify({ path: 'evidence.txt' }) } }] }, finish_reason: null }] })}\n\n`);
      return res.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }] })}\n\ndata: [DONE]\n\n`);
    }
    const todo = payload.tools?.find(tool => tool.function?.name.startsWith('plugin_todo_update_todo_'));
    if (todo && !payload.messages.some(message => message.tool_calls?.some(call => call.function?.name === todo.function.name))) {
      const args = { todos: [{ content: 'Retain UI migration evidence', status: 'in_progress' }, { content: 'Verify inspector cleanup', status: 'pending' }] };
      res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', tool_calls: [{ index: 0, id: 'todo-evidence', type: 'function', function: { name: todo.function.name, arguments: JSON.stringify(args) } }] }, finish_reason: null }] })}\n\n`);
      return res.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }] })}\n\ndata: [DONE]\n\n`);
    }
    res.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: 'assistant', content }, finish_reason: null }] })}\n\n`);
    pendingResponses.push(() => res.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`));
  });
});
let win;
let phase = 'publish';
const logs = [];
const mainErrors = [];
const originalError = console.error;
console.error = (...args) => {
  const message = args.map(String).join(' ');
  // The dev host mirrors every renderer console level through console.error;
  // renderer severity is captured independently by the WebContents listener.
  if (!message.startsWith('[renderer] ')) mainErrors.push({ phase, message });
  originalError(...args);
};
const created = new Promise(resolve => app.once('browser-window-created', (_event, window) => {
  win = window;
  win.webContents.on('console-message', (_event, ...args) => logs.push(args));
  resolve();
}));
const waitForTerminal = count => new Promise(resolve => {
  const check = () => {
    if (logs.filter(row => row[1] === 'ui-composition:terminal-ready').length >= count) {
      win.webContents.removeListener('console-message', check);
      resolve();
    }
  };
  win.webContents.on('console-message', check);
  check();
});
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
const until = (fn, arg) => win.webContents.executeJavaScript(`new Promise((resolve, reject) => {
  const test = (${fn});
  const argument = ${JSON.stringify(arg)};
  const observer = new MutationObserver(check);
  const timer = setTimeout(() => { observer.disconnect(); reject(new Error('UI condition timed out')); }, 30000);
  function check() { if (test(argument)) { observer.disconnect(); clearTimeout(timer); resolve(); } }
  observer.observe(document, { subtree: true, childList: true, attributes: true });
  check();
})`);
const capture = async name => {
  await evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    // Native hidden windows can suspend compositor time. Finish finite visual
    // transitions for stable captures; leave ambient infinite animations alone.
    for (const animation of document.getAnimations()) {
      if (Number.isFinite(animation.effect?.getComputedTiming().endTime)) animation.finish();
    }
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const geometry = await evaluate(() => ({ width: innerWidth, height: innerHeight,
    reducedMotion: matchMedia('(prefers-reduced-motion: reduce)').matches,
    bodyFontSize: getComputedStyle(document.body).fontSize,
    sidebarFontSize: document.querySelector('.plugin-automation-sidebar') ? getComputedStyle(document.querySelector('.plugin-automation-sidebar')).fontSize : undefined }));
  fs.writeFileSync(path.join(run, `${name}.json`), JSON.stringify(geometry, null, 2));
  fs.writeFileSync(path.join(run, `${name}.png`), (await win.webContents.capturePage()).toPNG());
};
async function captureToolContribution(name) {
  await until(() => document.querySelector('.activity-timeline .plugin-todo-list')?.textContent.includes('Retain UI migration evidence'));
  await evaluate(() => {
    const list = document.querySelector('.activity-timeline .plugin-todo-list');
    const disclosures = [];
    for (let parent = list.parentElement; parent; parent = parent.parentElement) {
      if (parent.tagName === 'DETAILS') disclosures.push(parent);
    }
    for (const disclosure of disclosures.reverse()) {
      if (!disclosure.open) disclosure.querySelector(':scope > summary').click();
    }
  });
  await until(() => {
    const list = document.querySelector('.activity-timeline .plugin-todo-list');
    if (!list) return false;
    for (let parent = list.parentElement; parent; parent = parent.parentElement) {
      if (parent.tagName === 'DETAILS' && !parent.open) return false;
    }
    return true;
  });
  await evaluate(() => {
    // The test-only Inspector probe overlays the conversation. Capture the
    // actual tool leaf without that unrelated fixture covering its text.
    document.querySelector('[data-inspector-probe]').style.visibility = 'hidden';
    document.querySelector('.activity-timeline .plugin-todo-list').scrollIntoView({ block: 'center' });
  });
  await capture(name);
  const geometry = await evaluate(() => {
    const list = document.querySelector('.activity-timeline .plugin-todo-list');
    const rect = list.getBoundingClientRect();
    const unobscured = [...list.querySelectorAll('li')].every(item => {
      const bounds = item.getBoundingClientRect();
      return [bounds.top + 1, bounds.bottom - 1].every(y => item.contains(document.elementFromPoint(bounds.left + bounds.width / 2, y)));
    });
    return { width: rect.width, height: rect.height, top: rect.top, bottom: rect.bottom, unobscured,
      visible: list.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }), viewportHeight: innerHeight };
  });
  fs.writeFileSync(path.join(run, `${name}-tool.json`), JSON.stringify(geometry, null, 2));
  assert.ok(geometry.unobscured && geometry.visible && geometry.width > 0 && geometry.height > 0 && geometry.top >= 0 && geometry.bottom <= geometry.viewportHeight, 'tool contribution is visibly expanded inside the viewport');
  await evaluate(() => { document.querySelector('[data-inspector-probe]').style.visibility = ''; });
}
const save = result => {
  fs.writeFileSync(path.join(run, 'result.json'), JSON.stringify({ phase, ...result }, null, 2));
  fs.writeFileSync(path.join(run, 'console.json'), JSON.stringify(logs, null, 2));
  fs.writeFileSync(path.join(run, 'main-errors.json'), JSON.stringify(mainErrors, null, 2));
  fs.writeFileSync(path.join(run, 'terminal-calls.json'), JSON.stringify(terminalCalls, null, 2));
  fs.writeFileSync(path.join(run, 'model-requests.json'), JSON.stringify(requests, null, 2));
};
const watchdog = setTimeout(() => { save({ passed: false, error: 'watchdog timeout' }); app.exit(1); }, 150000);
async function verifyToolbarGeometry() {
  const failures = await evaluate(() => {
    const errors = [];
    const controls = [...document.querySelectorAll('[data-composition-tool], .composer-plugin-tools-trigger, .composer-bar-right button')];
    for (const control of controls) {
      const rect = control.getBoundingClientRect();
      const owner = control.closest('.composer-plugin-tools-menu') ?? control.closest('.composer-bar');
      const bounds = owner.getBoundingClientRect();
      if (rect.left < bounds.left - 1 || rect.right > bounds.right + 1 || rect.top < bounds.top - 1 || rect.bottom > bounds.bottom + 1)
        errors.push(control.getAttribute('aria-label') + ' outside its visible region');
      if (control.scrollWidth > control.clientWidth + 1 || control.scrollHeight > control.clientHeight + 1)
        errors.push(control.getAttribute('aria-label') + ' clips its contents');
    }
    const menu = document.querySelector('.composer-plugin-tools-menu');
    if (menu && menu.scrollWidth > menu.clientWidth + 1) errors.push('overflow menu clips horizontally');
    const inline = controls.filter(control => control.closest('.composer-plugin-toolbar'));
    for (let index = 1; index < inline.length; index++) {
      if (inline[index - 1].getBoundingClientRect().right > inline[index].getBoundingClientRect().left)
        errors.push('inline tools overlap');
    }
    return errors;
  });
  assert.deepEqual(failures, [], 'toolbar controls remain readable and contained');
}
async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture',
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'synthetic-only', model: 'fixture' } },
    engines: { default_engine: 'wuu' },
    skills: { enabled: false }, ptc: { enabled: false },
  }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'UI composition', path: workspace, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: workspace } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  const published = spawnSync(core, ['plugin', 'dev', '--watch=false', source], { cwd: workspace, env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.writeFileSync(path.join(run, 'dev.log'), `${published.stdout}\n${published.stderr}`);
  assert.equal(published.status, 0, 'public plugin dev publication');
  const developer = path.join(run, 'developer-loop');
  fs.mkdirSync(developer);
  for (const name of ['src', 'plugin.json', 'package.json', 'tsconfig.json']) {
    fs.cpSync(path.join(repository, 'examples/plugins/developer-loop', name), path.join(developer, name), { recursive: true });
  }
  const packed = spawnSync('npm', ['pack', '--json', '--pack-destination', run], {
    cwd: path.join(repository, 'packages/plugin-sdk'), encoding: 'utf8', timeout: 180000,
  });
  fs.writeFileSync(path.join(run, 'sdk-pack.log'), `${packed.stdout}\n${packed.stderr}`);
  assert.equal(packed.status, 0, 'pack actual public SDK');
  const tarball = path.join(run, JSON.parse(packed.stdout)[0].filename);
  const developerPackage = JSON.parse(fs.readFileSync(path.join(developer, 'package.json'), 'utf8'));
  developerPackage.devDependencies['@wuu/plugin-sdk'] = `file:${tarball}`;
  fs.writeFileSync(path.join(developer, 'package.json'), JSON.stringify(developerPackage, null, 2));
  for (const args of [['install', '--no-audit', '--no-fund'], ['run', 'build']]) {
    const built = spawnSync('npm', args, { cwd: developer, encoding: 'utf8', timeout: 180000 });
    fs.appendFileSync(path.join(run, 'developer-build.log'), `${args.join(' ')}\n${built.stdout}\n${built.stderr}\n`);
    assert.equal(built.status, 0, 'build unchanged developer-loop against the packed public SDK');
  }
  const archive = path.join(run, 'developer-loop.zip');
  const packaged = spawnSync(core, ['plugin', 'pack', '--output', archive, developer], { cwd: workspace, env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.writeFileSync(path.join(run, 'developer-pack.log'), `${packaged.stdout}\n${packaged.stderr}`);
  assert.equal(packaged.status, 0, 'package developer-loop through the public CLI');
  const installed = spawnSync(core, ['plugin', 'install', '--json', archive], { cwd: workspace, env: process.env, encoding: 'utf8', timeout: 30000 });
  fs.writeFileSync(path.join(run, 'developer-install.log'), `${installed.stdout}\n${installed.stderr}`);
  assert.equal(installed.status, 0, 'install actual developer-loop package');
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await created;
  // Use the product's reduced-motion setting for stable isolated captures;
  // Chromium pseudo-element disclosure transitions can lag capturePage.
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  assert.ok(await evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches));
  phase = 'composer';
  win.setContentSize(1280, 860);
  await until(() => document.querySelector('[data-main-conversation-composer] textarea:not(:disabled)'));
  const inventory = (await evaluate(() => window.wuu.initialize())).extension_inventory;
  const plugin = inventory.find(item => item.provenance.plugin_id === 'deep-ui-example');
  assert.ok(plugin, 'published example appears in actual inventory');
  const developerPlugin = inventory.find(item => item.provenance.plugin_id === 'developer-loop-example');
  assert.equal(developerPlugin?.runtime_state, 'active', 'actual developer-loop runtime activated');
  await until(() => document.querySelector('[data-developer-loop-presenter="composer"]') && document.querySelector('[data-developer-loop-presenter="navigation"]'));
  fs.writeFileSync(path.join(run, 'inventory.json'), JSON.stringify(inventory, null, 2));
  await until(() => document.querySelectorAll('[data-composition-tool]').length === 2);
  await until(() => document.querySelector('[data-public-composer]') && document.querySelector('[data-public-navigation]'));
  const publicSnapshots = await evaluate(() => Object.fromEntries(['composer', 'navigation'].map(id => [id, JSON.parse(document.querySelector('[data-public-' + id + ']').getAttribute('data-public-' + id))])));
  assert.equal(publicSnapshots.composer.contractVersion, 1);
  assert.ok(Array.isArray(publicSnapshots.navigation.nodes));

  await evaluate(() => document.querySelector('.composer-plugin-tools-trigger').click());
  await until(() => document.querySelector('[data-composition-tool="3"]'));
  assert.equal(await evaluate(() => document.querySelector('[data-composition-tool="3"]').textContent), 'Composition tool 3', 'generic contributions retain registered translations');
  await verifyToolbarGeometry();
  await capture('toolbar-overflow-light-wide');
  await evaluate(() => document.querySelector('[data-composition-tool="3"]').click());
  await until(() => !document.querySelector('.composer-plugin-tools-menu'));
  assert.ok(logs.some(row => row[1] === 'ui-composition:tool-3'), 'overflow action reached the extension');
  await evaluate(() => {
    const input = document.querySelector('[data-main-conversation-composer] textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Check the explicit UI wrapper.');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await until(() => !document.querySelector('[data-main-conversation-composer] .composer-send-button')?.disabled);
  await evaluate(() => document.querySelector('[data-main-conversation-composer] textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })));
  phase = 'wrapped-turn';
  await until(() => document.querySelector('.deep-ui-timeline') && document.body.textContent.includes('UI composition verified'));
  assert.ok(requests.some(request => request.payload.messages?.some(message => message.role === 'tool' && JSON.stringify(message.content).includes('UI composition tool evidence'))), 'real read_file result reached the model');
  await until(() => [...document.querySelectorAll('[data-public-message]')].some(node => {
    const item = JSON.parse(node.dataset.publicMessage).snapshot;
    return item.status === 'streaming' && item.text?.includes('UI composition verified');
  }));
  const leafPayloads = await evaluate(() => ({
    messages: [...document.querySelectorAll('[data-public-message]')].map(node => JSON.parse(node.dataset.publicMessage)),
    processes: [...document.querySelectorAll('[data-public-process]')].map(node => JSON.parse(node.dataset.publicProcess)),
  }));
  assert.ok(leafPayloads.messages.some(item => item.threadId && item.turnId && item.snapshot.kind === 'assistant-message'), 'actual message supplies address and semantic payload');
  assert.ok(leafPayloads.processes.some(item => item.snapshot.items.some(entry => entry.toolName === 'read_file')), 'actual process supplies selected tools');
  assert.ok(leafPayloads.processes.every(item => item.snapshot.active === false), 'earlier tool group is inactive while the answer streams');
  fs.writeFileSync(path.join(run, 'leaf-payloads.json'), JSON.stringify(leafPayloads, null, 2));
  await capture('wrapped-light-wide');
  await captureToolContribution('tool-contribution-light-wide');
  phase = 'inspector';
  await until(() => document.querySelector('[data-inspector-probe] .plugin-todo-list')?.textContent.includes('Retain UI migration evidence'));
  await capture('inspector-probe-light-wide');
  await evaluate(async () => { await window.wuu.setThemePreference('dark'); document.documentElement.style.setProperty('--conversation-message-font-size', '20px'); });
  win.setContentSize(760, 860);
  await evaluate(() => document.documentElement.style.setProperty('--wuu-font-size-ui', '20px'));
  await capture('inspector-probe-dark-narrow-large');
  const todoPlugin = inventory.find(item => item.provenance.plugin_id === 'todo');
  assert.ok(todoPlugin, 'bundled TODO appears in actual inventory');
  phase = 'disable';
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'disable' }), todoPlugin);
  await until(() => !document.querySelector('[data-inspector-probe] .plugin-todo-list') && !document.querySelector('.activity-timeline .plugin-todo-list'));
  assert.ok(await evaluate(() => document.querySelector('.activity-timeline')?.textContent.length > 0), 'disabling TODO preserves native tool output');
  assert.ok(await evaluate(() => !!document.querySelector('[data-inspector-probe]')), 'disabling TODO preserves the external layout');
  phase = 'reenable';
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'enable' }), todoPlugin);
  await until(() => document.querySelector('[data-inspector-probe] .plugin-todo-list')?.textContent.includes('Retain UI migration evidence'));
  await captureToolContribution('tool-contribution-dark-narrow-large');
  for (const complete of pendingResponses.splice(0)) complete();
  await until(() => !document.querySelector('[data-inspector-probe]'));
  await capture('wrapped-dark-narrow-large');
  await evaluate(() => document.querySelector('.composer-plugin-tools-trigger').click());
  await until(() => document.querySelector('[data-composition-tool="3"]'));
  await verifyToolbarGeometry();
  await capture('toolbar-overflow-dark-narrow-large');
  phase = 'toolbar-removal';
  await evaluate(() => document.querySelector('[data-composition-tool="3"]').dispatchEvent(new MouseEvent('click', { bubbles: true, shiftKey: true })));
  await until(() => document.querySelector('.composer-plugin-toolbar > [data-composition-tool="1"]')
    && !document.querySelector('.composer-plugin-tools-trigger'));
  await verifyToolbarGeometry();
  await capture('toolbar-after-contribution-removal');
  await until(() => !document.querySelector('.composer-plugin-tools-menu'));
  phase = 'automation-sidebar';
  await evaluate(() => [...document.querySelectorAll('[data-ribbon-id]')].find(node => node.dataset.ribbonId.includes('automation-client')).click());
  await until(() => document.querySelector('.plugin-automation-sidebar-workspace')?.textContent.includes('UI composition'));
  assert.equal(await evaluate(() => !!document.querySelector('[data-foreign-sidebar]')), false, 'contextual layout excludes other plugin content');
  await evaluate(() => document.querySelector('.plugin-automation-create').click());
  await until(() => document.querySelector('.plugin-automation-form textarea'));
  await evaluate(() => {
    const form = document.querySelector('.plugin-automation-form');
    for (const [field, value, proto] of [
      [form.querySelector('input'), 'Review composition lifecycle and retained evidence', HTMLInputElement.prototype],
      [form.querySelector('textarea'), 'Inspect the retained composition evidence.', HTMLTextAreaElement.prototype],
    ]) {
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(field, value);
      field.dispatchEvent(new Event('input', { bubbles: true }));
    }
  });
  await evaluate(() => document.querySelector('.plugin-automation-form').requestSubmit());
  await until(() => document.querySelector('.plugin-automation-sidebar-item')?.textContent.includes('Review composition lifecycle'));
  await evaluate(() => document.querySelector('.plugin-automation-detail-close').click());
  await until(() => !document.querySelector('.plugin-automation-detail'));
  await evaluate(() => document.querySelector('.plugin-automation-sidebar-item').click());
  await until(() => document.querySelector('.plugin-automation-form input')?.value === 'Review composition lifecycle and retained evidence');
  await capture('automation-sidebar-dark-narrow-large');
  win.setContentSize(1280, 860);
  await evaluate(() => {
    document.documentElement.style.setProperty('--wuu-font-size-ui', '15px');
    document.documentElement.style.setProperty('--conversation-message-font-size', '15px');
    return window.wuu.setThemePreference('light');
  });
  await capture('automation-sidebar-light-wide');
  const automationPlugin = inventory.find(item => item.provenance.plugin_id === 'automation-client');
  phase = 'disable';
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'disable' }), automationPlugin);
  await until(() => !document.querySelector('.plugin-automation-sidebar'));
  await evaluate(() => document.querySelector('[data-ribbon-id="conversations"]').click());
  await until(() => document.querySelector('.deep-ui-timeline'));
  await waitForTerminal(1);
  phase = 'disable';
  const before = logs.length;
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'disable' }), plugin);
  await until(() => !document.querySelector('.deep-ui-timeline'));
  assert.equal(await evaluate(() => document.querySelectorAll('[data-composition-tool]').length), 0, 'disable removes toolbar contributions');
  assert.equal(await evaluate(() => document.querySelectorAll('[data-public-composer], [data-public-navigation]').length), 0, 'disable removes composer and navigation contributions');

  assert.ok(await evaluate(() => document.body.textContent.includes('UI composition verified')), 'disable preserves original message');
  await capture('disabled-content-preserved');
  const retired = logs.slice(before).flat().filter(value => typeof value === 'string' && value.startsWith('ui-composition:'));
  assert.ok(retired.includes('ui-composition:abort'), 'generation abort observed');
  assert.ok(retired.includes('ui-composition:view-cleanup'), 'mounted wrapper cleaned');
  assert.ok(retired.indexOf('ui-composition:provider-cleanup') > retired.indexOf('ui-composition:view-cleanup'), 'provider cleanup follows view cleanup');
  // Quit with a mounted extension: the renderer must finish cleanup and ACKs
  // before native shutdown closes the backend, without racing late requests.
  phase = 'reenable';
  const terminalsBeforeEnable = logs.filter(row => row[1] === 'ui-composition:terminal-ready').length;
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'enable' }), plugin);
  await until(() => document.querySelector('.deep-ui-timeline') && document.querySelectorAll('[data-composition-tool]').length === 2);
  await waitForTerminal(terminalsBeforeEnable + 1);
  phase = 'developer-loop';
  const openDeveloperCounter = () => evaluate(() => {
    const tab = [...document.querySelectorAll('[data-wuu-component="workspace-tool-tab"]')].find(element => element.textContent.includes('Acceptance Counter'));
    const navigation = [...document.querySelectorAll('button')].find(element => element.textContent.trim() === 'Developer Loop' || element.getAttribute('aria-label') === 'Developer Loop');
    const target = tab ?? navigation;
    if (!target) throw new Error('Developer Loop public view is not discoverable');
    target.click();
  });
  await openDeveloperCounter();
  phase = 'developer-loop-counter';
  await until(() => [...document.querySelectorAll('[data-counter-button]')].some(node => node.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }) && !node.disabled));
  await evaluate(() => [...document.querySelectorAll('[data-counter-button]')].find(node => node.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true })).click());
  await until(() => [...document.querySelectorAll('[data-counter-value]')].some(node => node.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }) && node.textContent === '1'));
  await capture('developer-loop-counter');
  phase = 'disable';
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'disable' }), developerPlugin);
  await until(() => !document.querySelector('[data-developer-loop-presenter="composer"]') && !document.querySelector('[data-developer-loop-presenter="navigation"]') && !document.querySelector('[data-counter-button]'));
  phase = 'reenable';
  await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, fingerprint: record.fingerprint, action: 'enable' }), developerPlugin);
  await until(() => document.querySelector('[data-developer-loop-presenter="composer"]') && document.querySelector('[data-developer-loop-presenter="navigation"]'));
  await openDeveloperCounter();
  phase = 'developer-loop-restored';
  await until(() => [...document.querySelectorAll('[data-counter-value]')].some(node => node.checkVisibility({ checkVisibilityCSS: true, checkOpacity: true }) && node.textContent === '1'));
  await evaluate(() => document.querySelector('[data-ribbon-id="conversations"]').click());
  await until(() => document.querySelector('.deep-ui-timeline'));
  phase = 'shutdown';
  const quitting = logs.length;
  app.once('will-quit', event => {
    try {
      const cleanup = logs.slice(quitting).map(row => row[1]);
      assert.ok(cleanup.includes('ui-composition:view-cleanup'), 'quit unmounted the live extension');
      assert.ok(cleanup.indexOf('ui-composition:provider-cleanup') > cleanup.indexOf('ui-composition:view-cleanup'), 'quit drained consumers before providers');
      assert.deepEqual(mainErrors.filter(entry => entry.phase === 'shutdown'), [], 'main process completed shutdown without errors');
      assert.deepEqual(mainErrors.filter(entry => !['disable', 'reenable'].includes(entry.phase)
        || !/plugin graph generation is no longer active|renderer graph is no longer available/.test(entry.message)), [], 'only obsolete graph requests may be rejected during replacement');
      assert.deepEqual(logs.filter(row => row[0] >= 3), [], 'renderer completed without errors');
      const startedTerminals = terminalCalls.filter(call => call.action === 'start');
      assert.ok(startedTerminals.length >= 2, 'native terminal created before and after disable');
      assert.ok(startedTerminals.every(start => terminalCalls.some(call => call.action === 'stop' && call.id === start.id)), 'every native terminal stop completed before quit');
      save({ passed: true, core, run, checks: ['public dev publication and Go inventory', 'unchanged developer-loop source built against packed SDK, installed without separate approval, public composer/navigation contributions and counter storage restored after disable/enable', 'generic composer toolbar preserves translation, first-two layout, overflow interaction and unload', 'automation sidebar uses shared view context, excludes foreign contributions, opens stored tasks and disappears on disable', 'real composer, read_file and TODO tools and synthetic model message', 'TODO tool UI and inspector use generic slots and survive disable/reenable with native tool output retained', 'explicit wrappers render in the selected conversation with immutable message and process payloads from the actual leaves', 'light wide/default and dark narrow/large captures', 'disable preserves content, aborts scope and drains view before provider', 'reenable and application quit drain live renderer resources and native terminals before backend shutdown'], limits: ['synthetic model', 'reduced-motion captures; tool text checked for clipping and occlusion', 'stock environment panel is feature-disabled; an external layout probe renders its declared Inspector slot using real application state', 'does not cover product Agent composition binding or full workbench migration', 'requests crossing a graph replacement can be rejected; main-errors.json preserves those responses'] });
      console.log(`PASS UI composition and shutdown; evidence ${run}`);
    } catch (error) {
      event.preventDefault();
      save({ passed: false, error: error.stack });
      originalError(error);
      app.exit(1);
    } finally { clearTimeout(watchdog); server.close(); }
  });
  app.quit();
}
main().catch(async error => { save({ passed: false, error: error.stack }); if (win) { await capture('failure').catch(() => {}); fs.writeFileSync(path.join(run, 'failure.html'), await evaluate(() => document.body.innerHTML).catch(() => '')); } console.error(error); clearTimeout(watchdog); server.close(); app.exit(1); });
