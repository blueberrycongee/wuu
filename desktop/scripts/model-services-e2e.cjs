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
const { app } = require('electron');

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
async function openModelRemoval(win, model) {
  await evaluate(win, id => document.querySelector(`[data-testid="settings-provider-model-actions"][data-model="${id}"]`).click(), model);
  await waitFor(win, () => document.querySelector('[role="menuitem"][data-tone="danger"]'));
  await click(win, '[role="menuitem"][data-tone="danger"]');
  await waitFor(win, () => document.querySelector('.model-remove-dialog'));
}
async function openSettingsPage(win, page) {
  await click(win, `.settings-nav [data-wuu-page="${page}"]`);
  await waitFor(win, target => document.querySelector('.settings-page')?.dataset.wuuPage === target, page);
}
async function openProviderCatalog(win) {
  await click(win, '[data-testid="settings-connection-add"]');
  await waitFor(win, () => document.querySelector('.model-browse-dialog'));
}
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
  const renderedFontSize = await evaluate(win, () => {
    const label = [...document.querySelectorAll('.settings-page .model-service-card-title, .settings-page .settings-row-label-title')]
      .find(node => node.getBoundingClientRect().width > 0 && node.getBoundingClientRect().height > 0);
    if (!label) throw new Error('Layout matrix requires a visible connection or settings row label.');
    return parseFloat(getComputedStyle(label).fontSize);
  });
  assert.equal(renderedFontSize, size, 'The layout matrix must use the requested UI font size.');
}
// A settings column can itself overflow the viewport. Check both boundaries
// so narrow-window regressions cannot pass merely because the parent grew.
const layoutProblems = win => evaluate(win, () => {
  const page = document.querySelector('.settings-page')?.getBoundingClientRect();
  const problems = [];
  if (!page) return ['settings page missing'];
  if (page.left < -1 || page.right > innerWidth + 1) problems.push(`settings page outside viewport: ${page.left}..${page.right}/${innerWidth}`);
  for (const node of document.querySelectorAll('.settings-page button, .settings-page .model-service-card-title, .settings-page h1')) {
    const rect = node.getBoundingClientRect();
    if (rect.width === 0) continue;
    const label = node.textContent.trim().slice(0, 40);
    if (rect.left < page.left - 1 || rect.right > page.right + 1) problems.push(`outside settings column: ${label}`);
    if (rect.left < -1 || rect.right > innerWidth + 1) problems.push(`outside viewport: ${label} (${rect.left}..${rect.right})`);
  }
  for (const row of document.querySelectorAll('.connection-summary-row')) {
    const icon = row.querySelector('.connection-summary-icon')?.getBoundingClientRect();
    const copy = row.querySelector('.connection-summary-copy')?.getBoundingClientRect();
    const chevron = row.querySelector(':scope > .settings-disclosure-chevron, :scope > .icon')?.getBoundingClientRect();
    const rect = row.getBoundingClientRect();
    if (!icon || !copy || !chevron) { problems.push('connection row content missing'); continue; }
    if (icon.right > copy.left + 1 || copy.right > chevron.left + 1) problems.push('connection icon, text or chevron overlaps');
    if (Math.abs(icon.top + icon.height / 2 - chevron.top - chevron.height / 2) > 1) problems.push('connection icon and chevron centers misaligned');
    if (copy.width <= 0 || rect.height < icon.height) problems.push('connection row content clipped');
    if (icon.height > copy.height + 1) problems.push('connection icon exceeds its two-line identity');
    const quota = row.querySelector(':scope > .connection-usage-summary')?.getBoundingClientRect();
    if (quota && (quota.right > chevron.left + 1 || quota.left < copy.left - 1)) problems.push('connection quota outside its reading column');
    if (quota && quota.top < copy.bottom - 1 && quota.left < copy.right - 1) problems.push('connection quota overlaps identity');
    const viewport = row.closest('.settings-scroll').getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    if (y >= Math.max(0, viewport.top) && y < Math.min(innerHeight, viewport.bottom)) {
      const hit = document.elementFromPoint(x, y);
      if (!hit || !row.contains(hit)) problems.push('connection row click target obscured');
    }
  }
  const actions = document.querySelectorAll('.settings-page-actions button');
  if (!actions.length) problems.push('settings page actions missing');
  for (const node of actions) {
    const rect = node.getBoundingClientRect();
    const label = node.getAttribute('aria-label') || node.textContent.trim();
    if (rect.width <= 0 || rect.height <= 0 || rect.top < 0 || rect.bottom > innerHeight || rect.left < -1 || rect.right > innerWidth + 1) {
      problems.push(`header action not visible: ${label}`);
      continue;
    }
    const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
    if (!hit || !node.contains(hit)) problems.push(`header action obscured: ${label}`);
  }
  return problems;
});

let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 240000);

async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
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
  await waitFor(main, () => document.querySelector('[data-provider="fixture"][data-default]') && document.querySelector('[data-testid="settings-connection-add"]'));
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('[data-catalog]'))), false, 'Inactive catalog is hidden until explicitly opened.');
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('[data-testid="settings-engine-codex-status"]'))), false, 'Uninstalled inactive engines are hidden.');
  await capture(main, '01-overview.png');

  // The grouped catalog searches the complete directory, preserves the
  // custom endpoint action on no results, and survives close/reopen without
  // creating or choosing a service.
  const configBeforeBrowsing = readConfig();
  await openProviderCatalog(main);
  await waitFor(main, () => Boolean(document.querySelector('.model-browse-dialog')));
  await capture(main, '01b-grouped-catalog.png');
  await type(main, '.model-browse-dialog input[type="search"]', '  DEEPSEEK  ');
  await waitFor(main, () => document.querySelectorAll('.model-browse-dialog [data-catalog]').length === 1);
  assert.equal(await evaluate(main, () => document.querySelector('.model-browse-dialog [data-catalog]').dataset.catalog), 'deepseek');
  await type(main, '.model-browse-dialog input[type="search"]', 'no-such-service-e2e');
  await waitFor(main, () => document.querySelectorAll('.model-browse-dialog [data-catalog], .model-browse-dialog [data-subscription]').length === 0);
  assert.ok(await evaluate(main, () => Boolean(document.querySelector('[data-testid="settings-provider-custom"]'))));
  await click(main, '[data-testid="settings-connection-back"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-connection-add"]'));
  await openProviderCatalog(main);
  await type(main, '.model-browse-dialog input[type="search"]', 'deepseek');
  await click(main, '.model-browse-dialog [data-catalog="deepseek"]');
  await waitFor(main, () => Boolean(document.querySelector('.model-connect-dialog')));
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('[role="dialog"]'))), false, 'Connection navigation stays in the page.');
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('[data-testid="settings-provider-card"]'))), false, 'Configuration replaces the overview.');
  await click(main, '.model-connect-dialog [data-testid="settings-connection-back"]');
  await waitFor(main, () => document.querySelector('.model-browse-dialog input[type="search"]')?.value === 'deepseek');
  assert.equal(await evaluate(main, () => document.querySelectorAll('.model-browse-dialog [data-catalog]').length), 1, 'Back preserves catalog filtering.');
  await click(main, '[data-testid="settings-connection-back"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-connection-add"]'));
  assert.deepEqual(readConfig(), configBeforeBrowsing, 'Browsing and dismissing leave saved services and the default unchanged.');

  // A catalog provider: its endpoint and a suggested model come from the
  // catalog, so the key is the only thing to type. The working default stays.
  await openProviderCatalog(main);
  await click(main, '[data-catalog="deepseek"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-connect-key"]'));
  assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect-default"]').getAttribute('aria-checked')), 'false');
  assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-connect"]').disabled), true);
  await type(main, '[data-testid="settings-provider-connect-key"]', 'sk-e2e-deepseek');
  await capture(main, '02-connect-deepseek.png');
  await click(main, '[data-testid="settings-provider-connect"]');
  await waitFor(main, () => document.querySelector('.connection-page-title')?.textContent === 'DeepSeek');
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

  // Removing a choice persists so catalog refreshes cannot bring it back.
  const hidden = catalogModels.find(model => model.id !== nextModel).id;
  await openModelRemoval(main, hidden);
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-remove-model-confirm"]'));
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await waitFor(main, () => !document.querySelector('.model-remove-dialog'));
  assert.ok(await evaluate(main, () => Boolean(document.querySelector('[data-testid="settings-provider-back"]'))), 'Dismissing a confirmation keeps the connection details open.');
  await openModelRemoval(main, hidden);
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-remove-model-confirm"]'));
  await click(main, '[data-testid="settings-provider-remove-model-confirm"]');
  await waitFor(main, id => !document.querySelector(`[data-testid="settings-provider-model"][data-model="${id}"]`) && document.querySelector('.model-choice-hidden'), hidden);
  assert.equal(readConfig().providers.deepseek.models[hidden].disabled, true);
  await evaluate(main, () => document.querySelector('.model-choice-hidden').setAttribute('open', ''));
  await capture(main, '04-detail-hidden-model.png');

  // A custom endpoint made the default: the next conversation uses it.
  await click(main, '[data-testid="settings-provider-back"]');
  await waitFor(main, () => !document.querySelector('[data-testid="settings-provider-back"]'));
  await openProviderCatalog(main);
  await click(main, '[data-testid="settings-provider-custom"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-connect-base-url"]'));
  await type(main, '[data-testid="settings-provider-connect-base-url"]', endpoint);
  await type(main, '[data-testid="settings-provider-connect-key"]', 'sk-e2e-gateway');
  await type(main, '[data-testid="settings-provider-connect-model"]', 'fixture-b');
  await type(main, '[data-testid="settings-provider-connect-name"]', 'gateway');
  await click(main, '[data-testid="settings-provider-connect-default"]');
  await capture(main, '05-connect-custom.png');
  await click(main, '[data-testid="settings-provider-connect"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-back"]')
    && document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-b"][aria-pressed="true"]')
    && !document.querySelector('[data-testid="settings-provider-connect"]'));
  // The detail appears after save completes; verify its persisted result before
  // the next edit so an unfinished connection cannot satisfy the transition.
  const savedDeadline = Date.now() + 30000;
  while (Date.now() < savedDeadline) {
    config = readConfig();
    if (config.default_provider === 'gateway' && config.providers.gateway?.model === 'fixture-b') break;
    await delay(50);
  }
  assert.equal(config.default_provider, 'gateway');
  assert.equal(config.providers.gateway.model, 'fixture-b');

  // Add a custom model, explicitly replace the selected model, and reload.
  await click(main, '[data-testid="settings-provider-add-model-open"]');
  await type(main, '[data-testid="settings-provider-add-model"]', 'fixture-extra');
  await click(main, '.model-choice-add button[type="submit"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-extra"]'));
  await openModelRemoval(main, 'fixture-b');
  assert.equal(await evaluate(main, () => document.querySelector('[data-testid="settings-provider-remove-model-confirm"]').disabled), true);
  await click(main, '[data-testid="settings-provider-replacement-model"]');
  await click(main, '[data-value="fixture-extra"]');
  await click(main, '[data-testid="settings-provider-remove-model-confirm"]');
  await waitFor(main, () => !document.querySelector('.model-remove-dialog') && !document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-b"]'));
  assert.equal(readConfig().providers.gateway.model, 'fixture-extra');
  assert.equal(readConfig().providers.gateway.models['fixture-b'].disabled, true);
  await capture(main, '05b-selected-model-removed.png');
  main.reload();
  await waitFor(main, () => document.querySelector('.composer textarea'));
  await click(main, '.sidebar-account-trigger');
  await waitFor(main, () => document.querySelector('[data-settings-page="providers"]'));
  await click(main, '[data-settings-page="providers"]');
  await waitFor(main, () => document.querySelector('[data-provider="gateway"]'));
  await click(main, '[data-provider="gateway"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-extra"]'));
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-b"]'))), false);
  await evaluate(main, () => document.querySelector('.model-choice-hidden').setAttribute('open', ''));
  await click(main, '[aria-label="恢复 fixture-b"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-b"]'));
  await click(main, '[data-testid="settings-provider-model"][data-model="fixture-b"]');
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-model"][data-model="fixture-b"]').getAttribute('aria-pressed') === 'true');
  assert.ok(!readConfig().providers.gateway.models['fixture-b'].disabled);

  await click(main, '[data-testid="settings-provider-back"]');
  await waitFor(main, () => document.querySelector('[data-provider="gateway"][data-default]'));
  const cards = await evaluate(main, () => [...document.querySelectorAll('[data-testid="settings-provider-card"]')].map(card => ({ name: card.dataset.provider, label: card.getAttribute('aria-label'), isDefault: card.hasAttribute('data-default') })));
  assert.deepEqual(cards.filter(card => card.isDefault).map(card => card.name), ['gateway']);
  await capture(main, '06-overview-after.png');

  // General gathers how new conversations start; it shows the new default.
  await openSettingsPage(main, 'general');
  await waitFor(main, () => document.querySelector('[data-testid="settings-default-model-select"]')?.textContent.includes('fixture-b'));
  await capture(main, '06-general-new-conversations.png');
  await openSettingsPage(main, 'providers');
  await waitFor(main, () => document.querySelector('[data-provider="gateway"]'));

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
  // Detail entry from a scrolled overview must expose Back on its first paint.
  await evaluate(main, () => {
    const scroll = document.querySelector('.settings-scroll');
    scroll.scrollTop = scroll.scrollHeight;
    document.querySelector('[data-provider="gateway"]').click();
  });
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-back"]'));
  const entry = await evaluate(main, () => {
    const back = document.querySelector('[data-testid="settings-provider-back"]');
    const page = back.closest('.connection-page');
    const scroll = page.closest('.settings-scroll');
    const viewport = scroll.getBoundingClientRect();
    const rect = back.getBoundingClientRect();
    const heading = page.querySelector('.connection-page-title');
    return {
      scrollTop: scroll.scrollTop,
      backVisible: rect.top >= viewport.top && rect.bottom <= viewport.bottom,
      headingFocused: document.activeElement === heading,
    };
  });
  assert.deepEqual(entry, { scrollTop: 0, backVisible: true, headingFocused: true }, 'Detail entry reveals Back without delayed scroll correction.');
  // Keep detail controls reachable at large text sizes in narrow windows.
  for (const theme of ['light', 'dark']) for (const width of [1280, 760]) {
    await setAppearance(main, { theme, size: 20, width });
    await settle(main);
    const overflow = await evaluate(main, () => {
      const panel = document.querySelector('[data-testid="settings-provider-back"]').closest('.connection-page');
      const rect = panel.getBoundingClientRect();
      const viewport = panel.closest('.settings-scroll').getBoundingClientRect();
      const controls = [];
      for (const node of panel.querySelectorAll('button, input, select, summary')) {
        const bounds = node.getBoundingClientRect();
        if (!bounds.width || !bounds.height) continue;
        const label = node.getAttribute('aria-label') || node.textContent.trim().slice(0, 40) || node.tagName;
        if (bounds.left < rect.left - 1 || bounds.right > rect.right + 1) controls.push(`outside detail: ${label}`);
        const x = bounds.left + bounds.width / 2;
        const y = bounds.top + bounds.height / 2;
        if (x >= Math.max(0, viewport.left) && x < Math.min(innerWidth, viewport.right)
          && y >= Math.max(0, viewport.top) && y < Math.min(innerHeight, viewport.bottom)) {
          const hit = document.elementFromPoint(x, y);
          if (!hit || !node.contains(hit)) controls.push(`obscured: ${label}`);
        }
      }
      for (const text of panel.querySelectorAll('.model-choice-name, .model-choice-id')) {
        const bounds = text.getBoundingClientRect();
        if (!bounds.width || !bounds.height) continue;
        const button = text.closest('.model-choice-main');
        if (!button) continue;
        const content = button.getBoundingClientRect();
        if (text.scrollWidth > text.clientWidth + 1 || text.scrollHeight > text.clientHeight + 1) controls.push(`truncated model text: ${text.textContent}`);
        if (bounds.left < content.left - 1 || bounds.right > content.right + 1 || bounds.top < content.top - 1 || bounds.bottom > content.bottom + 1) controls.push(`model text outside control: ${text.textContent}`);
      }
      for (const label of panel.querySelectorAll('.model-choice-label')) {
        const tick = label.querySelector('.model-choice-selected');
        const name = label.querySelector('.model-choice-name');
        if (!tick || !name) continue;
        const bounds = label.getBoundingClientRect(), mark = tick.getBoundingClientRect(), text = name.getBoundingClientRect();
        if (mark.right > bounds.right + 1 || mark.left < text.right - 1) controls.push('selected model mark overlaps or escapes its label');
      }
      return { horizontal: panel.scrollWidth > panel.clientWidth + 1, outside: rect.left < -1 || rect.right > innerWidth + 1, controls };
    });
    assert.deepEqual(overflow, { horizontal: false, outside: false, controls: [] }, `detail ${theme} ${width}px`);
    await capture(main, `07-detail-${theme}-20-${width}.png`);
  }
  await click(main, '[data-testid="settings-provider-back"]');
  await waitFor(main, () => !document.querySelector('[data-testid="settings-provider-back"]'));
  await setAppearance(main, { theme: 'light', size: 14.5, width: 1280 });

  // Removal asks first and deletes the saved key with the service.
  await click(main, '[data-provider="deepseek"]');
  await waitFor(main, () => document.querySelector('.connection-page-title')?.textContent === 'DeepSeek');
  await click(main, '.model-service-hero-actions [aria-haspopup="menu"]');
  await waitFor(main, () => [...document.querySelectorAll('[role="menuitem"]')].some(item => item.textContent.includes('删除服务')));
  await evaluate(main, () => [...document.querySelectorAll('[role="menuitem"]')].find(item => item.textContent.includes('删除服务')).click());
  await waitFor(main, () => document.querySelector('[data-testid="settings-provider-remove-confirm"]'));
  await capture(main, '08-remove-confirm.png');
  await click(main, '[data-testid="settings-provider-remove-confirm"]');
  await waitFor(main, () => document.querySelector('[data-provider="gateway"]') && !document.querySelector('[data-provider="deepseek"]'));
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
