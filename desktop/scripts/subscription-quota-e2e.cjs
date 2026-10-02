// Real renderer/main/preload/Go coverage. A disposable trust root and local
// HTTPS proxy serve quota fixtures; no provider traffic leaves the machine.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const https = require('node:https');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, BrowserWindow } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_QUOTA_OUTPUT || path.join(desktop, 'out/subscription-quota-e2e'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-quota-'));
const home = path.join(fixture, 'wuu');
const userHome = path.join(fixture, 'user');
const project = path.join(fixture, 'project');
const buildEnv = { ...process.env };
for (const dir of [home, userHome, project, output]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, {
  WUU_HOME: home, HOME: userHome, CODEX_HOME: path.join(userHome, '.codex'),
  GROK_HOME: path.join(userHome, '.grok'), CLAUDE_CONFIG_DIR: path.join(userHome, '.claude'),
  WUU_ENABLE_BROWSER: '0', WUU_SAFE_MODE: '1', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1',
});
delete process.env.ELECTRON_RENDERER_URL;
const key = path.join(fixture, 'key.pem');
const cert = path.join(fixture, 'cert.pem');
const generated = spawnSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
  '-keyout', key, '-out', cert, '-subj', '/CN=Quota E2E', '-addext',
  'subjectAltName=DNS:api.deepseek.com,DNS:api.kimi.com,DNS:openrouter.ai'], { encoding: 'utf8' });
assert.equal(generated.status, 0, generated.stderr);
process.env.SSL_CERT_FILE = cert;

// macOS's platform verifier ignores SSL_CERT_FILE. Inject a trust-root-only
// startup hook into the test binary, never the source tree or production build.
const trustHook = path.join(fixture, 'trust.go');
fs.writeFileSync(trustHook, `package main
import ("crypto/x509"; "os")
func init() {
  pem, err := os.ReadFile(os.Getenv("SSL_CERT_FILE")); if err != nil { panic(err) }
  pool := x509.NewCertPool(); if !pool.AppendCertsFromPEM(pem) { panic("invalid fixture root") }
  x509.SetFallbackRoots(pool)
}
`);
const overlay = path.join(fixture, 'overlay.json');
fs.writeFileSync(overlay, JSON.stringify({ Replace: { [path.resolve(desktop, '../cmd/wuu/quota_e2e_trust.go')]: trustHook } }));
const core = path.join(fixture, 'wuu-core');
const built = spawnSync('go', ['build', '-overlay', overlay, '-o', core, './cmd/wuu'], {
  cwd: path.resolve(desktop, '..'), encoding: 'utf8', env: buildEnv,
});
assert.equal(built.status, 0, built.stderr);
process.env.WUU_DESKTOP_CORE = core;
process.env.GODEBUG = `${process.env.GODEBUG ? process.env.GODEBUG + ',' : ''}x509usefallbackroots=1`;

const token = (id, email) => `e30.${Buffer.from(JSON.stringify({ email,
  'https://api.openai.com/auth': { chatgpt_account_id: id } })).toString('base64url')}.fixture`;
const firstToken = token('account-one', 'personal@example.test');
const secondToken = token('account-two', 'team-with-a-long-account-name@example.test');
let firstStatus = 200;
let firstUsage = 37;
const requests = [];
const future = Math.floor(Date.now() / 1000) + 86400;
let firstReset = future;
function reply(req, res) {
  const host = req.headers.host?.split(':')[0];
  const url = new URL(req.url, `https://${host}`);
  requests.push({ method: req.method, host, path: url.pathname });
  res.setHeader('Content-Type', 'application/json');
  if (req.method !== 'GET') { res.writeHead(405); res.end('{}'); return; }
  if (url.pathname === '/backend-api/wham/usage') {
    if (req.headers.authorization === `Bearer ${secondToken}`) {
      res.end(JSON.stringify({ plan_type: 'plus', rate_limit: {
        primary_window: { limit_window_seconds: 18000, reset_at: future },
      } }));
    } else {
      res.writeHead(firstStatus);
      res.end(JSON.stringify({ plan_type: 'pro', rate_limit: {
        primary_window: { used_percent: firstUsage, limit_window_seconds: 18000, reset_at: firstReset },
        secondary_window: { used_percent: 125, limit_window_seconds: 604800, reset_at: future },
      }, rate_limit_reset_credits: { available_count: 2 } }));
    }
  } else if (host === 'api.deepseek.com' && url.pathname === '/user/balance') {
    res.end(JSON.stringify({ balance_infos: [{ currency: 'USD', total_balance: '12.3400' }, { currency: 'CNY', total_balance: '0' }] }));
  } else if (host === 'openrouter.ai' && url.pathname === '/api/v1/credits') {
    res.end(JSON.stringify({ data: { total_credits: '0.30000000000000001', total_usage: '0.1' } }));
  } else if (host === 'api.kimi.com' && url.pathname === '/coding/v1/usages') {
    res.end(JSON.stringify({ usage: { used: '25', limit: '100', resetTime: new Date(future * 1000).toISOString() },
      limits: [{ window: { duration: 300, timeUnit: 'TIME_UNIT_MINUTE' }, detail: { used: '3', limit: '20' } }] }));
  } else { res.writeHead(404); res.end('{}'); }
}
const upstream = http.createServer(reply);
const secure = https.createServer({ key: fs.readFileSync(key), cert: fs.readFileSync(cert) }, reply);
const proxy = http.createServer((_req, res) => { res.writeHead(502); res.end(); });
proxy.on('connect', (req, socket, head) => {
  if (!['api.deepseek.com:443', 'api.kimi.com:443', 'openrouter.ai:443'].includes(req.url)) {
    socket.end('HTTP/1.1 502 Bad Gateway\r\n\r\n'); return;
  }
  socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');
  if (head.length) socket.unshift(head);
  secure.emit('connection', socket);
});
const listen = server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(50);
  }
  throw new Error(`Timed out: ${fn}`);
}
const click = (win, selector) => evaluate(win, selector => {
  const element = document.querySelector(selector);
  if (!element) throw new Error(`Missing ${selector}`);
  element.click();
}, selector);
const snapshot = win => evaluate(win, () => window.wuu.listEngines({ include_quota: true }));
const findQuota = (result, name) => result.subscription_providers.find(provider => provider.name === name).quota;
const card = name => `[data-testid="subscription-builtin-${name}"]`;
const shots = [];
async function capture(win, name) {
  await evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const bounds = await evaluate(win, () => {
    const page = document.querySelector('.settings-page').getBoundingClientRect();
    const x = Math.max(0, Math.floor(page.x)), y = Math.max(0, Math.floor(page.y));
    return { x, y, width: Math.min(innerWidth - x, Math.ceil(page.width)), height: Math.min(innerHeight - y, Math.ceil(page.height)) };
  });
  const zoom = win.webContents.getZoomFactor();
  const [width, height] = win.getContentSize();
  const x = Math.floor(bounds.x * zoom), y = Math.floor(bounds.y * zoom);
  const crop = { x, y, width: Math.min(width - x, Math.ceil(bounds.width * zoom)), height: Math.min(height - y, Math.ceil(bounds.height * zoom)) };
  const captured = await win.webContents.capturePage(crop);
  assert.deepEqual(captured.getSize(), { width: crop.width, height: crop.height }, 'Captured evidence must match its requested dimensions.');
  fs.writeFileSync(path.join(output, name), captured.toPNG());
  shots.push(name);
}
async function refresh(win) {
  await click(win, '[data-testid="settings-subscriptions"] header button');
  await waitFor(win, () => document.querySelector('[data-testid="settings-subscriptions"]')?.getAttribute('aria-busy') === 'false');
}
let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('Quota E2E timeout'); app.exit(1); }, 240000);
async function run() {
  await listen(upstream);
  await listen(proxy);
  Object.assign(process.env, { HTTPS_PROXY: `http://127.0.0.1:${proxy.address().port}`,
    HTTP_PROXY: `http://127.0.0.1:${proxy.address().port}`, NO_PROXY: '127.0.0.1,localhost' });
  for (const name of ['http_proxy', 'https_proxy', 'all_proxy', 'ALL_PROXY', 'no_proxy']) delete process.env[name];
  const endpoint = `http://127.0.0.1:${upstream.address().port}/backend-api/codex`;
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity']
    .map(id => [id, { enabled: false, binary_path: path.join(fixture, `absent-${id}`) }]));
  const config = { default_provider: 'personal', engines, providers: {
    personal: { type: 'openai-codex', base_url: endpoint, api_key: firstToken, model: 'gpt-fixture' },
    team: { type: 'openai-codex', base_url: endpoint, api_key: secondToken, model: 'gpt-fixture' },
    deepseek: { type: 'openai-compatible', base_url: 'https://api.deepseek.com', api_key: 'fixture-deepseek', model: 'deepseek-chat' },
    router: { type: 'openai-compatible', base_url: 'https://openrouter.ai/api/v1', api_key: 'fixture-router', model: 'fixture' },
    kimi: { type: 'openai-compatible', base_url: 'https://api.kimi.com/coding/v1', api_key: 'fixture-kimi', model: 'kimi-fixture' },
    unknown: { type: 'openai-compatible', base_url: 'https://unsupported.example.test/v1', api_key: 'fixture-unknown', model: 'fixture' },
  } };
  const configPath = path.join(home, 'config.json');
  fs.writeFileSync(configPath, JSON.stringify(config));
  const originalConfig = fs.readFileSync(configPath, 'utf8');
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'repo', name: 'quota-fixture', path: project,
    created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'repo', cwd: project } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  main.setSize(1280, 1000);
  await waitFor(main, () => document.querySelector('.composer textarea'));
  assert.equal(requests.length, 0, 'Ordinary navigation must not request quotas.');
  await click(main, '.sidebar-account-trigger');
  await waitFor(main, () => document.querySelector('[data-settings-page="providers"]'));
  await click(main, '[data-settings-page="providers"]');
  await waitFor(main, () => document.querySelector('.settings-nav-item'));
  await evaluate(main, () => [...document.querySelectorAll('.settings-nav-item')].find(button => button.textContent.trim() === '订阅').click());
  await waitFor(main, () => document.querySelector('[data-testid="settings-subscriptions"]')?.getAttribute('aria-busy') === 'false');
  const fresh = await snapshot(main);
  const first = findQuota(fresh, 'personal'), second = findQuota(fresh, 'team');
  assert.equal(first.status, 'available');
  assert.equal(first.account.label, 'personal@example.test');
  assert.notEqual(first.account.id, second.account.id);
  assert.equal(second.windows[0].used_percent, undefined);
  assert.deepEqual(findQuota(fresh, 'deepseek').balances, [{ currency: 'USD', amount: '12.3400' }, { currency: 'CNY', amount: '0' }]);
  assert.equal(findQuota(fresh, 'router').balances[0].amount, '0.20000000000000001');
  assert.deepEqual(findQuota(fresh, 'kimi').windows.map(window => window.used_percent), [15, 25]);
  assert.equal(findQuota(fresh, 'unknown').status, 'unsupported');
  assert.deepEqual(await evaluate(main, selector => [...document.querySelectorAll(`${selector} meter`)].map(meter => meter.value), card('personal')), [63, 0]);
  assert.equal(await evaluate(main, selector => document.querySelector(`${selector} meter`) === null, card('team')), true);
  assert.equal(await evaluate(main, () => {
    const personal = document.querySelector('[data-testid="subscription-builtin-personal"]');
    const team = document.querySelector('[data-testid="subscription-builtin-team"]');
    return personal.closest('article') === team.closest('article')
      && personal.textContent.includes('personal@example.test')
      && !personal.textContent.includes('team-with-a-long-account-name@example.test')
      && team.textContent.includes('team-with-a-long-account-name@example.test')
      && !team.textContent.includes('personal@example.test');
  }), true, 'A shared service card must retain each account and its independent allowance.');
  assert.equal(await evaluate(main, selector => Boolean(document.querySelector(`${selector} .select-menu`)), card('deepseek')), false);
  const resetTimes = await evaluate(main, selector => [...document.querySelectorAll(`${selector} .settings-subscription-reset time`)].map(time => ({
    datetime: time.dateTime, exact: time.getAttribute('aria-label'), title: time.title,
  })), card('personal'));
  assert.equal(resetTimes.length, 2);
  assert.ok(resetTimes.every(time => time.exact === time.title && time.exact.includes(String(new Date().getFullYear())) && Number.isFinite(Date.parse(time.datetime))));
  await capture(main, '01-fresh-light.png');
  await evaluate(main, selector => document.querySelector(selector).scrollIntoView({ block: 'start' }), card('personal'));
  await capture(main, '01-accounts-light.png');

  const beforeMenuRequests = requests.length;
  await evaluate(main, selector => {
    const trigger = document.querySelector(`${selector} [data-testid="subscription-account-actions"]`);
    trigger.focus();
    trigger.click();
  }, card('personal'));
  await waitFor(main, () => document.activeElement?.getAttribute('role') === 'menu');
  assert.equal(await evaluate(main, () => document.querySelectorAll('[role="menuitem"]').length), 2);
  await capture(main, '01-account-actions-light.png');
  main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
  main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await waitFor(main, () => !document.querySelector('[role="menu"]'));
  assert.equal(await evaluate(main, selector => document.activeElement === document.querySelector(`${selector} [data-testid="subscription-account-actions"]`), card('personal')), true);
  assert.equal(requests.length, beforeMenuRequests, 'Opening and dismissing credential actions must not read quota or start authentication.');

  const layouts = [];
  for (const theme of ['light', 'dark']) for (const size of [14, 20]) for (const width of [1280, 760]) {
    main.setSize(width, 1000);
    await evaluate(main, ({ theme, size }) => {
      document.documentElement.setAttribute('data-theme', theme);
      document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
      document.documentElement.style.setProperty('--appearance-scale', String(size / 14));
    }, { theme, size });
    const problems = await evaluate(main, () => {
      const page = document.querySelector('.settings-page').getBoundingClientRect();
      return [...document.querySelectorAll('.settings-subscription-name, .settings-subscription-usage, .settings-subscription-balance, .settings-subscription-account, .settings-subscription-model-trigger, .settings-subscription-metadata, .settings-subscription-reset, .settings-subscription-plan, .settings-subscription-account-source, .settings-subscription-age')]
        .filter(node => { const rect = node.getBoundingClientRect(); return rect.right > page.right + 1 || rect.left < page.left - 1 || node.scrollWidth > node.clientWidth + 1; })
        .map(node => node.textContent);
    });
    assert.deepEqual(problems, [], `${theme}, ${size}px, ${width}px`);
    layouts.push({ theme, size, width });
    await capture(main, `02-${theme}-${size}-${width}.png`);
  }
  main.setSize(1280, 1000);
  await evaluate(main, () => { document.documentElement.setAttribute('data-theme', 'light'); document.documentElement.style.setProperty('--conversation-message-font-size', '14px'); document.documentElement.style.setProperty('--appearance-scale', '1'); });
  await evaluate(main, selector => document.querySelector(selector).scrollIntoView({ block: 'center' }), card('personal'));
  firstReset = Math.floor(Date.now() / 1000) - 1;
  await refresh(main);
  assert.equal(await evaluate(main, selector => document.querySelector(`${selector} .settings-subscription-window`).querySelector('meter') === null, card('personal')), true);
  await capture(main, '03-reset-expired.png');
  firstReset = future;
  await refresh(main);
  firstStatus = 503;
  await refresh(main);
  const stale = findQuota(await snapshot(main), 'personal');
  assert.equal(stale.status, 'stale');
  assert.equal(stale.windows[0].used_percent, 37);
  assert.equal(await evaluate(main, selector => document.querySelector(`${selector} meter`) === null, card('personal')), true);
  await capture(main, '03-stale.png');
  firstStatus = 200; firstUsage = 52;
  await refresh(main);
  assert.deepEqual(await evaluate(main, selector => [...document.querySelectorAll(`${selector} meter`)].map(meter => meter.value), card('personal')), [48, 0]);
  firstStatus = 401;
  await refresh(main);
  assert.equal(findQuota(await snapshot(main), 'personal').status, 'sign_in');
  assert.equal(await evaluate(main, selector => document.querySelector(`${selector} meter`) === null, card('personal')), true);
  await capture(main, '04-sign-in.png');
  firstStatus = 503;
  assert.equal(findQuota(await snapshot(main), 'personal').status, 'unavailable', 'Rejected credentials must not resurrect cached quota.');
  assert.equal(fs.readFileSync(configPath, 'utf8'), originalConfig, 'Observation must not change the default, model, or credentials.');
  assert.ok(requests.every(request => request.method === 'GET'), 'No inference was submitted.');
  assert.ok(requests.every(request => ['/backend-api/wham/usage', '/user/balance', '/api/v1/credits', '/coding/v1/usages'].includes(request.path)), 'Only quota endpoints were requested.');
  for (const file of fs.readdirSync(path.join(home, 'quota-snapshots'))) {
    const persisted = fs.readFileSync(path.join(home, 'quota-snapshots', file), 'utf8');
    assert.ok(!persisted.includes(firstToken) && !persisted.includes(secondToken) && !persisted.includes('fixture-deepseek'));
  }
  const { build } = await import('vite');
  await build({ configFile: false, root: path.join(desktop, 'dev/subscriptions'), base: './', logLevel: 'warn',
    build: { outDir: path.join(fixture, 'preview') } });
  const preview = new BrowserWindow({ show: false, width: 980, height: 1000,
    webPreferences: { backgroundThrottling: false, offscreen: process.env.WUU_E2E_OFFSCREEN === '1' } });
  const visualCases = [
    { name: 'empty', empty: '1' },
    { name: 'unattributed', unattributed: '1' },
    { name: 'loading', detecting: '1', delay: '30000' },
    { name: 'long', long: '1' },
    { name: 'states', states: '1' },
    { name: 'failures', failures: '1', codex: '1' },
  ];
  for (const theme of ['light', 'dark']) for (const state of visualCases) {
    const viewport = { width: state.name === 'long' ? 440 : 980, height: 1000 };
    preview.setContentSize(viewport.width, viewport.height);
    await preview.loadFile(path.join(fixture, 'preview/index.html'), { query: { ...state, theme, size: '20', lang: 'en' } });
    await waitFor(preview, () => Boolean(document.querySelector('[data-testid="settings-subscriptions"]')));
    if (state.name !== 'loading') await waitFor(preview, () => document.querySelector('[data-testid="settings-subscriptions"]').getAttribute('aria-busy') === 'false');
    if (state.name === 'unattributed') assert.equal(await evaluate(preview, () => Boolean(document.querySelector('[data-testid="subscription-engine-codex"] .settings-subscription-age'))), true, 'An observation without credential attribution keeps its visible age.');
    assert.deepEqual(await evaluate(preview, () => ({ width: innerWidth, height: innerHeight })), viewport, 'Preview must render at the requested viewport.');
    await capture(preview, `05-${state.name}-${theme}.png`);
    assert.deepEqual(await evaluate(preview, () => [...document.querySelectorAll('.settings-subscription-name, .settings-subscription-plan, .settings-subscription-account-name, .settings-subscription-metadata, .settings-subscription-balance')]
      .filter(node => { const r = node.getBoundingClientRect(); return r.right > innerWidth + 1 || r.left < -1 || node.scrollWidth > node.clientWidth + 1; })
      .map(node => node.textContent)), [], `${state.name} must fit the viewport`);
  }
  preview.destroy();
  fs.writeFileSync(path.join(output, 'evidence.json'), JSON.stringify({
    fresh: fresh.subscription_providers.map(provider => ({ name: provider.name, quota: provider.quota })),
    stale, layouts, visualCases: visualCases.map(state => state.name), requests, shots, configUnchanged: true, inferenceRequests: 0,
  }, null, 2));
  console.log("Subscription quota E2E passed; synthetic UI evidence saved.");
}
function cleanup() {
  clearTimeout(timeout);
  upstream.close(); proxy.close(); secure.close();
  proxy.closeAllConnections(); secure.closeAllConnections(); upstream.closeAllConnections();
}
run().then(() => {
  cleanup();
  main.destroy();
  app.quit();
}).catch(async error => {
  console.error(error);
  if (main) await capture(main, 'failure.png');
  cleanup();
  app.exit(1);
});
app.on('will-quit', () => fs.rmSync(fixture, { recursive: true, force: true }));
