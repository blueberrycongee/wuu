// Real Electron/main/preload/Go coverage for the model services settings page.
// Build the core and desktop first; WUU_MODEL_SERVICES_CORE overrides the core
// binary. Keys and config live in a disposable WUU_HOME and HOME, and a local
// HTTP provider answers the turn, so no real account, key, or inference is
// used. Screenshots and evidence.json are written to artifacts/model-services-e2e/.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(desktop, '../artifacts/model-services-e2e');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-model-services-'));
const home = path.join(fixture, 'wuu-home');
const userHome = path.join(fixture, 'user-home');
const project = path.join(fixture, 'project');
for (const dir of [home, userHome, project]) fs.mkdirSync(dir);
fs.rmSync(output, { recursive: true, force: true });
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
// Subscription logins are discovered from HOME; keep the user's out of view.
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE = process.env.WUU_MODEL_SERVICES_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';

const requests = [];
// Gate the real RPC at the main/preload boundary, without timing-based delays.
let releaseCatalog;
const catalogGate = new Promise(resolve => { releaseCatalog = resolve; });
const handle = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, listener) => handle(channel, channel === 'wuu:config-model-catalog-providers'
  ? async (...args) => { await catalogGate; return listener(...args); }
  : listener);
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    requests.push({ path: req.url, authorization: req.headers.authorization, body: JSON.parse(body) });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = delta => `data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`;
    res.write(chunk({ role: 'assistant', content: 'Model services fixture answer.' }));
    res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 4, total_tokens: 14 } })}\n\ndata: [DONE]\n\n`);
  });
});

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(50);
  }
  throw new Error(`Timed out: ${fn} ${arg === undefined ? '' : JSON.stringify(arg)}`);
}
const settle = win => evaluate(win, async () => {
  await document.fonts.ready;
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  await Promise.all(document.getAnimations().filter(a => a.playState === 'running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
});
const shots = [];
async function capture(win, name) {
  await settle(win);
  fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG());
  shots.push(name);
}
const click = (win, selector) => evaluate(win, target => {
  const element = document.querySelector(target);
  if (!element) throw new Error(`missing ${target}`);
  element.click();
}, selector);
const type = (win, selector, value) => evaluate(win, ({ target, text }) => {
  const input = document.querySelector(target);
  if (!input) throw new Error(`missing ${target}`);
  Object.getOwnPropertyDescriptor(input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, 'value').set.call(input, text);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}, { target: selector, text: value });
const readConfig = () => JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8'));
// Every credential file the core may write, read as text.
function storedSecrets() {
  const texts = [];
  const walk = dir => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name !== 'config.json' && fs.statSync(full).size < 1_000_000) texts.push(fs.readFileSync(full, 'utf8'));
    }
  };
  walk(home);
  return texts.join('\n');
}
async function setAppearance(win, { theme, size, width }) {
  win.setSize(width, 900);
  await evaluate(win, ({ theme, size }) => {
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.style.setProperty('--appearance-scale', String(size / 14));
    document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
  }, { theme, size });
  assert.equal(await evaluate(win, () => parseFloat(getComputedStyle(document.querySelector('.model-service-card-title')).fontSize)), size, 'The layout matrix must use the requested UI font size.');
}
// Nothing on the page may clip its text or overflow the settings column.
const layoutProblems = win => evaluate(win, () => {
  const page = document.querySelector('.settings-page')?.getBoundingClientRect();
  const problems = [];
  for (const node of document.querySelectorAll('.settings-page button, .settings-page .model-service-card-title, .settings-page h1')) {
    const rect = node.getBoundingClientRect();
    if (rect.width === 0) continue;
    if (page && rect.right > page.right + 1) problems.push(`overflow: ${node.textContent.trim().slice(0, 40)}`);
  }
  return problems;
});

let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 240000);

async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: endpoint, api_key: 'fixture-only', model: 'fixture' } },
  }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
    projects: [{ id: 'repo', name: 'model-services-fixture', path: project, created_at: stamp, updated_at: stamp }],
    active_context: { kind: 'project', project_id: 'repo', cwd: project },
  }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  main.setSize(1280, 900);
  await waitFor(main, () => document.querySelector('.composer textarea'));

  await click(main, '.sidebar-account-trigger');
  await waitFor(main, () => document.querySelector('[data-settings-page="providers"]'));
  await click(main, '[data-settings-page="providers"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-tiles"] [role="status"]'));
  assert.equal(await evaluate(main, () => document.querySelectorAll('[data-testid="settings-provider-tiles"] button.model-service-tile').length), 0, 'A pending directory never exposes a partial set of services.');
  await capture(main, '00-directory-loading.png');
  releaseCatalog();
  await waitFor(main, () => document.querySelector('[data-testid="settings-default-model"]') && document.querySelector('[data-catalog="deepseek"]'));
  assert.match(await evaluate(main, () => document.querySelector('[data-testid="settings-default-model"]').textContent), /fixture/);
  await capture(main, '01-overview.png');

  // Changing authentication methods resets model/endpoint/key fields so a
  // saved connection cannot accidentally mix two credential sources.
  for (const [service, method] of [['openai', 'codex-cli'], ['xai', 'xai-oauth'], ['xai', 'grok-cli']]) {
    await click(main, '[data-catalog="' + service + '"]');
    await waitFor(main, () => document.querySelector('[data-testid="settings-provider-connect-key"]'));
    await type(main, '[data-testid="settings-provider-connect-key"]', 'unused-api-key');
    await click(main, '[data-connection-method="' + method + '"]');
    await waitFor(main, () => !document.querySelector('[data-testid="settings-provider-connect-key"]'));
    assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect"]').disabled), false);
    await capture(main, '01-' + method + '.png');
    for (const theme of ['light', 'dark']) {
      await setAppearance(main, { theme, size: 20, width: 760 });
      await capture(main, '01-' + method + '-' + theme + '-large-narrow.png');
    }
    await setAppearance(main, { theme: 'light', size: 14, width: 1280 });
    await click(main, '[data-connection-method="api-key"]');
    await waitFor(main, () => document.querySelector('[data-testid="settings-provider-connect-key"]'));
    assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect-key"]').value), '');
    if (method === 'xai-oauth') {
      // Never open a real browser sign-in from this isolated fixture.
      await click(main, '.model-connect-dialog .environment-dialog-header .icon-button');
      continue;
    }
    await click(main, '[data-connection-method="' + method + '"]');
    await click(main, '[data-testid="settings-provider-connect"]');
    await waitFor(main, () => document.querySelector('.model-service-hero'));
    const providerType = method === 'codex-cli' ? 'openai-codex' : 'grok-build';
    const saved = readConfig();
    assert.equal(saved.default_provider, 'fixture');
    assert.equal(saved.providers[providerType].type, providerType);
    assert.equal(saved.providers[providerType].api_key, undefined);
    if (method === 'codex-cli') assert.equal(saved.providers[providerType].reuse_codex_credentials, true);
    await capture(main, '01-' + method + '-needs-login.png');
    await click(main, '.model-service-hero-actions [aria-haspopup="menu"]');
    await waitFor(main, () => [...document.querySelectorAll('[role="menuitem"]')].some(item => item.textContent.includes('删除服务')));
    await evaluate(main, () => [...document.querySelectorAll('[role="menuitem"]')].find(item => item.textContent.includes('删除服务')).click());
    await waitFor(main, () => document.querySelector('[data-testid="settings-provider-remove-confirm"]'));
    await click(main, '[data-testid="settings-provider-remove-confirm"]');
    await waitFor(main, () => document.querySelector('[data-testid="settings-default-model"]'));
    assert.equal(readConfig().providers[providerType], undefined);
  }

  // The grouped catalog searches the complete directory, preserves the
  // custom endpoint action on no results, and survives close/reopen without
  // creating or choosing a service.
  const configBeforeBrowsing = readConfig();
  await click(main, '[data-testid="settings-provider-browse"]');
  await waitFor(main, () => Boolean(document.querySelector('.model-browse-dialog')));
  await capture(main, '01b-grouped-catalog.png');
  assert.ok(await evaluate(main, () => Boolean(document.querySelector('.model-browse-dialog [data-catalog-group="api"]'))));
  assert.ok(await evaluate(main, () => Boolean(document.querySelector('.model-browse-dialog [data-catalog-group="relay"]'))));
  await type(main, '.model-browse-dialog input[type="search"]', '  DEEPSEEK  ');
  await waitFor(main, () => document.querySelectorAll('.model-browse-dialog [data-catalog]').length === 1);
  assert.equal(await evaluate(main, () => document.querySelector('.model-browse-dialog [data-catalog]').dataset.catalog), 'deepseek');
  await type(main, '.model-browse-dialog input[type="search"]', 'no-such-service-e2e');
  await waitFor(main, () => document.querySelectorAll('.model-browse-dialog [data-catalog]').length === 0);
  assert.ok(await evaluate(main, () => Boolean(document.querySelector('.model-browse-dialog .environment-dialog-footer button'))));
  await capture(main, '01c-empty-catalog-search.png');
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await waitFor(main, () => !document.querySelector('[role="dialog"]'));
  await click(main, '[data-testid="settings-provider-browse"]');
  await waitFor(main, () => document.querySelector('.model-browse-dialog input[type="search"]')?.value === '');
  await click(main, '.model-browse-dialog [data-catalog="deepseek"]');
  await waitFor(main, () => Boolean(document.querySelector('.model-connect-dialog')));
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('.model-browse-dialog'))), false);
  await click(main, '.model-connect-dialog .environment-dialog-header .icon-button');
  await waitFor(main, () => !document.querySelector('[role="dialog"]'));
  assert.deepEqual(readConfig(), configBeforeBrowsing, 'Browsing and dismissing leave saved services and the default unchanged.');

  // A catalog provider: its endpoint and a suggested model come from the
  // catalog, so the key is the only thing to type. The working default stays.
  await click(main, '[data-catalog="deepseek"]');
  await waitFor(main, () => document.querySelector('[role="dialog"] [data-testid="settings-provider-connect-key"]'));
  assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect-default"]').getAttribute('aria-checked')), 'false');
  assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect"]').disabled), true);
  await type(main, '[data-testid="settings-provider-connect-key"]', 'sk-e2e-deepseek');
  await capture(main, '02-connect-deepseek.png');
  await click(main, '[data-testid="settings-provider-connect"]');
  await waitFor(main, () => document.querySelector('.model-service-hero h1')?.textContent === 'DeepSeek');
  let config = readConfig();
  assert.equal(config.default_provider, 'fixture', 'Connecting a second service keeps the default.');
  assert.equal(config.providers.deepseek.base_url, 'https://api.deepseek.com');
  assert.equal(config.providers.deepseek.type, 'openai-compatible');
  assert.ok(!JSON.stringify(config).includes('sk-e2e-deepseek'), 'The key is not written into config.json.');
  assert.ok(storedSecrets().includes('sk-e2e-deepseek'), 'The key is kept in the local credential store.');
  const catalogModels = await evaluate(main, () => [...document.querySelectorAll('[data-testid="settings-provider-model"]')].map(button => ({ id: button.dataset.model, selected: button.getAttribute('aria-pressed') === 'true' })));
  assert.ok(catalogModels.length >= 2, 'The catalog supplies the service model choices.');
  assert.equal(catalogModels.filter(model => model.selected).map(model => model.id).join(), config.providers.deepseek.model);
  await capture(main, '03-detail-deepseek.png');

  // Choosing another model for a service that is not the default saves it
  // for that service only.
  const nextModel = catalogModels.find(model => !model.selected).id;
  await click(main, `[data-testid="settings-provider-model"][data-model="${nextModel}"]`);
  await waitFor(main, id => document.querySelector(`[data-testid="settings-provider-model"][data-model="${id}"]`)?.getAttribute('aria-pressed') === 'true', nextModel);
  config = readConfig();
  assert.equal(config.providers.deepseek.model, nextModel);
  assert.equal(config.default_provider, 'fixture');

  // Hiding a choice persists so catalog refreshes cannot bring it back.
  const hidden = catalogModels.find(model => model.id !== nextModel).id;
  await evaluate(main, id => document.querySelector(`[data-model="${id}"]`).closest('.model-choice').querySelector('.model-choice-hide').click(), hidden);
  await waitFor(main, id => !document.querySelector(`[data-testid="settings-provider-model"][data-model="${id}"]`) && document.querySelector('.model-choice-hidden'), hidden);
  assert.equal(readConfig().providers.deepseek.models[hidden].disabled, true);
  await evaluate(main, () => document.querySelector('.model-choice-hidden').setAttribute('open', ''));
  await capture(main, '04-detail-hidden-model.png');

  // A custom endpoint made the default: the next conversation uses it.
  await click(main, '[data-testid="settings-provider-back"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-custom"]'));
  await click(main, '[data-testid="settings-provider-custom"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-connect-base-url"]'));
  await type(main, '[data-testid="settings-provider-connect-base-url"]', endpoint);
  await type(main, '[data-testid="settings-provider-connect-key"]', 'sk-e2e-gateway');
  await type(main, '[data-testid="settings-provider-connect-model"]', 'fixture-b');
  await type(main, '[data-testid="settings-provider-connect-name"]', 'gateway');
  await click(main, '[data-testid="settings-provider-connect-default"]');
  await capture(main, '05-connect-custom.png');
  await click(main, '[data-testid="settings-provider-connect"]');
  await waitFor(main, () => document.querySelector('.model-service-hero .model-service-badge'));
  config = readConfig();
  assert.equal(config.default_provider, 'gateway');
  assert.equal(config.providers.gateway.model, 'fixture-b');

  await click(main, '[data-testid="settings-provider-back"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-default-model"]')?.textContent.includes('fixture-b'));
  const cards = await evaluate(main, () => [...document.querySelectorAll('[data-testid="settings-provider-card"]')].map(card => ({ name: card.dataset.provider, label: card.getAttribute('aria-label'), isDefault: Boolean(card.querySelector('.model-service-badge')) })));
  assert.deepEqual(cards.filter(card => card.isDefault).map(card => card.name), ['gateway']);
  await capture(main, '06-overview-after.png');

  // Every theme, text size, and width keeps controls inside the column.
  const layouts = [];
  for (const theme of ['light', 'dark']) for (const size of [14, 20]) for (const width of [1280, 760]) {
    await setAppearance(main, { theme, size, width });
    await settle(main);
    const problems = await layoutProblems(main);
    assert.deepEqual(problems, [], `${theme} ${size}px ${width}px`);
    layouts.push({ theme, size, width });
    if ((theme === 'dark' && size === 14 && width === 1280) || (size === 20 && width === 760)) {
      await capture(main, `07-overview-${theme}-${size}-${width}.png`);
    }
  }
  await setAppearance(main, { theme: 'light', size: 14.5, width: 1280 });

  // Removal asks first and deletes the saved key with the service.
  await click(main, '[data-provider="deepseek"]');
  await waitFor(main, () => document.querySelector('.model-service-hero h1')?.textContent === 'DeepSeek');
  await click(main, '.model-service-hero-actions [aria-haspopup="menu"]');
  await waitFor(main, () => [...document.querySelectorAll('[role="menuitem"]')].some(item => item.textContent.includes('删除服务')));
  await evaluate(main, () => [...document.querySelectorAll('[role="menuitem"]')].find(item => item.textContent.includes('删除服务')).click());
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-remove-confirm"]'));
  await capture(main, '08-remove-confirm.png');
  await click(main, '[data-testid="settings-provider-remove-confirm"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-default-model"]') && !document.querySelector('[data-provider="deepseek"]'));
  assert.equal(readConfig().providers.deepseek, undefined);
  assert.ok(!storedSecrets().includes('sk-e2e-deepseek'), 'Removing a service removes its saved key.');

  // Back in the app, a new conversation starts on the new default.
  await click(main, '.settings-back-button');
  await waitFor(main, () => document.querySelector('.composer textarea'));
  await type(main, '.composer textarea', 'Which model answers this?');
  await click(main, '.composer-send-button');
  await waitFor(main, () => [...document.querySelectorAll('[data-thread-id] .session-flow')].some(flow => flow.textContent.includes('Model services fixture answer.')), undefined, 60000);
  const turn = requests.find(request => JSON.stringify(request.body.messages ?? request.body.input ?? '').includes('Which model answers this?'));
  assert.ok(turn, 'The turn reached the provider.');
  assert.equal(turn.body.model, 'fixture-b', 'The new default model answered.');
  assert.equal(turn.authorization, 'Bearer sk-e2e-gateway', 'The request used the key saved in settings.');
  await capture(main, '09-conversation-on-new-default.png');

  fs.writeFileSync(path.join(output, 'evidence.json'), `${JSON.stringify({
    fixture,
    finalConfig: { default_provider: readConfig().default_provider, providers: Object.keys(readConfig().providers) },
    deepseek: { models: catalogModels, chosen: nextModel, hidden },
    cards,
    layouts,
    turn: { path: turn.path, model: turn.body.model, authorization: 'Bearer sk-e2e-gateway' },
    shots,
  }, null, 2)}\n`);
  console.log(`model services E2E passed; artifacts in ${output}`);
}

run().then(() => {
  clearTimeout(timeout);
  server.close();
  app.quit();
}).catch(async error => {
  console.error(error, 'FIXTURE', fixture);
  if (main) fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  server.close();
  app.exit(1);
});
