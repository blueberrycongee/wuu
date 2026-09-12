// Real browser IndexedDB and UI; only account HTTP responses are controlled.
// WUU_HISTORY_TEST_URL can reuse an existing Vite development server.
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { createServer } from 'vite';

const dev = process.env.WUU_HISTORY_TEST_URL ? undefined : await createServer({
  root: fileURLToPath(new URL('..', import.meta.url)),
  server: { host: '127.0.0.1', port: 0, strictPort: false },
});
let browser;
try {
  await dev?.listen();
  const origin = process.env.WUU_HISTORY_TEST_URL || dev.resolvedUrls.local[0].replace(/\/$/, '');
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.route(origin + '/history-fixture', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>History cache test</title>' }));
  let generation = 'one', revision = '1', text = 'First answer', deleted = false, offline = false, enabled = true;
  let hold = false, held, release;
  let deniedHost = '', revoked = false;
  const requests = [];
  await page.route('**/v1/account/devices', route => {
    requests.push('/v1/account/devices');
    return route.fulfill({ status: revoked ? 401 : 200, json: revoked ? { error: 'device revoked' } : {
      username: 'account', auth_method: 'password', devices: [{ pub: 'other-host', role: 'host', name: 'Other computer', account: 'account', online: false }],
    } });
  });
  await page.route('**/v1/account/history**', async route => {
    const url = new URL(route.request().url());
    requests.push(url.pathname + url.search);
    if (offline) return route.abort('internetdisconnected');
    if (revoked || deniedHost && url.searchParams.get('host') === deniedHost) return route.fulfill({ status: 401, json: { error: 'device revoked' } });
    if (hold) { hold = false; held(); await new Promise(resolve => { release = resolve; }); }
    const settings = { host: url.searchParams.get('host') || 'host', enabled, generation };
    if (url.pathname.endsWith('/settings')) {
      enabled = route.request().postDataJSON().enabled; generation = 'new-generation';
      return route.fulfill({ json: { ...settings, enabled, generation } });
    }
    if (url.pathname.endsWith('/thread')) return route.fulfill({ json: { revision, thread: { id: 'thread', title: 'Task', updated_at: '2026-09-11T12:00:00Z', status: 'idle', messages: [{ id: 'answer', turn_id: 'turn', role: 'assistant', text }] } } });
    const unchanged = url.searchParams.get('after') === revision && url.searchParams.get('generation') === generation;
    return route.fulfill({ json: { ...settings, entries: unchanged || !enabled ? [] : [{ id: 'thread', title: 'Task', updated_at: '2026-09-11T12:00:00Z', revision, digest: text, deleted }], cursor: revision, more: false } });
  });
  const open = async (username = 'account') => {
    await page.goto(origin + '/history-fixture');
    await page.evaluate(async ({ origin, username }) => {
      const module = await import('/src/lib/conversationCache.ts');
      window.historyModule = module;
      window.historyClient = new module.ConversationHistory({ server: origin, token: 'test-only-token', username, pub: 'phone', device_seed: '' }, 'host', state => { window.copy = state; });
      await window.historyClient.load();
    }, { origin, username });
  };
  const cachedText = () => page.evaluate(() => window.historyClient.state.bodies.thread?.thread.messages[0].text);
  await open();
  await page.evaluate(() => window.historyClient.sync());
  assert.equal(await cachedText(), text);
  offline = true;
  await open(); // New JS context; content comes exclusively from IndexedDB.
  assert.equal(await cachedText(), text);
  assert.equal(await page.evaluate(() => window.historyClient.sync().then(() => false, () => true)), true);
  assert.equal(await cachedText(), text);
  offline = false; revision = '2'; text = 'Completed while phone was away';
  await page.evaluate(() => window.historyClient.sync());
  assert.equal(await cachedText(), text);
  assert(requests.some(path => path.includes('after=1')));
  const bodyRequests = requests.filter(path => path.includes('/thread?')).length;
  await page.evaluate(() => window.historyClient.sync());
  assert.equal(requests.filter(path => path.includes('/thread?')).length, bodyRequests, 'unchanged text must not download again');
  await open('different-account');
  assert.equal(await cachedText(), undefined, 'another account must not see cached text');
  await open();
  revision = '3'; deleted = true;
  await page.evaluate(() => window.historyClient.sync());
  assert.equal(await cachedText(), undefined);
  assert.equal(await page.evaluate(() => Object.keys(window.historyClient.state.entries).length), 0);
  deleted = false; revision = '4';
  await page.evaluate(() => window.historyClient.sync());
  await page.evaluate(() => window.historyClient.setEnabled(false));
  await open();
  assert.equal(await cachedText(), undefined, 'disable must delete durable text');
  assert.equal(await page.evaluate(() => window.historyClient.state.settings.enabled), false);
  enabled = true; generation = 'enabled-again'; revision = '1';
  const pendingRequest = new Promise(resolve => { held = resolve; }); hold = true;
  await page.evaluate(() => { window.pendingSync = window.historyClient.sync().catch(() => {}); });
  await pendingRequest;
  await page.evaluate(() => window.historyModule.clearConversationCaches());
  release();
  await page.evaluate(() => window.pendingSync);
  await open();
  assert.equal(await cachedText(), undefined, 'late response repopulated logged-out cache');
  await page.evaluate(async () => { await window.historyClient.sync(); await window.historyClient.read('thread'); window.historyClient.close(); });
  const delayedUIRequest = new Promise(resolve => { held = resolve; }); hold = true;
  await page.evaluate(async origin => {
    localStorage.setItem('wuu.account.v1', JSON.stringify({ server: origin, token: 'test-only-token', username: 'account', pub: 'phone', device_seed: '' }));
    const { default: refresh } = await import('/@react-refresh');
    refresh.injectIntoGlobalHook(window);
    window.$RefreshReg$ = () => {};
    window.$RefreshSig$ = () => type => type;
    window.__vite_plugin_react_preamble_installed__ = true;
    const { default: React } = await import('/node_modules/.vite/deps/react.js');
    const { default: ReactDOM } = await import('/node_modules/.vite/deps/react-dom_client.js');
    const { default: History } = await import('/src/ConversationWorkspace.tsx');
    const root = document.createElement('div'); document.body.append(root);
    window.uiRoot = ReactDOM.createRoot(root);
    window.uiRoot.render(React.createElement(History, { credentials: { host_pub: 'host', host_name: 'Test computer' }, back: () => {} }));
  }, origin);
  await delayedUIRequest;
  await page.getByRole('article', { name: '对话内容' }).getByText(text, { exact: true }).waitFor();
  assert.equal(await page.getByRole('button', { name: '连接电脑', exact: true }).count(), 1);
  release();
  await page.evaluate(() => window.uiRoot.unmount());
  const prepareAccount = async () => {
    deniedHost = ''; revoked = false;
    await open();
    await page.evaluate(async origin => {
      const session = { server: origin, token: 'test-only-token', username: 'account', pub: 'phone', device_seed: 'A'.repeat(43) };
      window.testAccount = session;
      localStorage.setItem('wuu.account.v1', JSON.stringify(session));
      localStorage.setItem('wuu.web.credentials', JSON.stringify({ v: 1, account_username: session.username, device_seed: session.device_seed,
        host_pub: 'host', host_name: 'Test computer', relay_url: origin.replace(/^http/, 'ws') + '/v1/connect' }));
      localStorage.setItem('wuu.web.language', 'zh-CN');
      for (const host of ['host', 'other-host']) {
        const client = new window.historyModule.ConversationHistory(session, host, () => {});
        await client.sync(); client.close();
      }
    }, origin);
  };
  const persistedBodies = host => page.evaluate(async ({ host, origin }) => {
    const { ConversationHistory } = await import('/src/lib/conversationCache.ts');
    const client = new ConversationHistory({ server: origin, username: 'account', pub: 'phone', token: 'test-only-token', device_seed: '' }, host, () => {});
    await client.load(); client.close(); return Object.keys(client.state.bodies).length;
  }, { host, origin });
  const openRevokedHistory = async () => {
    await Promise.all([
      page.waitForResponse(response => response.url().includes('/v1/account/history?') && response.status() === 401),
      page.goto(origin),
    ]);
    await page.waitForFunction(() => !document.querySelector('.conversation-workspace'));
  };
  // Exercise the real account entry: its account panel is inactive in history.
  await prepareAccount(); deniedHost = 'host';
  await openRevokedHistory();
  assert.equal(await page.evaluate(() => !!localStorage.getItem('wuu.account.v1')), true, 'host removal signed out a valid phone');
  assert.equal(await persistedBodies('host'), 0, 'removed host retained its cache');
  assert.equal(await persistedBodies('other-host'), 1, 'host removal cleared unrelated history');
  await prepareAccount(); revoked = true;
  await openRevokedHistory();
  assert.equal(await page.evaluate(() => localStorage.getItem('wuu.account.v1')), null, 'revoked login retained');
  assert.equal(await page.evaluate(() => localStorage.getItem('wuu.web.credentials')), null, 'revoked connection retained');
  assert.equal(await persistedBodies('host'), 0);
  assert.equal(await persistedBodies('other-host'), 0, 'revoked phone retained another computer cache');
  console.log('PASS: durable offline recovery, delta refresh, account isolation, deletion, disable, logout race, cache-first UI, host removal, account-wide revocation cleanup');
} finally { await browser?.close(); await dev?.close(); }
