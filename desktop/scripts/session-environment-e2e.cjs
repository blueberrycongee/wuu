// Production renderer acceptance. Run after electron-vite build. No model,
// account or execution provider is contacted; RPC behavior is checked in Go.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow, ipcMain } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_ENVIRONMENT_SUMMARY_OUTPUT || path.join(desktop, 'out/e2e/session-environment');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-environment-summary-'));
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, 'session-environment-e2e-extension.cjs');
let state = 'not_initialized';
let fail = false;
const calls = [];
let finishRead;
let hold = false;
ipcMain.handle('test:execution-environment', async (_event, threadID) => {
  calls.push({ threadID, state, fail });
  const snapshot = { thread_id: threadID, environment: { id: 'synthetic-worker', name: 'personal-linux-development-workspace', backend: 'ssh', root: '/workspace/projects/a-long-project-directory/isolated-worktree', state } };
  if (hold) await new Promise(resolve => { finishRead = resolve; });
  if (fail) throw new Error('synthetic raw diagnostic that must not be rendered');
  return snapshot;
});
const workspaceCalls = [];
const workerRoot = '/workspace/projects/a-long-project-directory/isolated-worktree';
function targetFor(input) {
  assert.ok(input.target?.thread_id, 'Files must carry the source session');
  assert.ok(!input.target.environment_id || input.target.environment_id === 'synthetic-worker');
  return { thread_id: input.target.thread_id, environment_id: 'synthetic-worker', connection_id: 'connection-one' };
}
ipcMain.handle('test:workspace-directory', (_event, input) => {
  workspaceCalls.push({method:'list',...input});
  return { root: workerRoot, path: '', target: targetFor(input), entries: [{name:'same.md',path:'same.md',kind:'file'}], truncated:false };
});
ipcMain.handle('test:workspace-file', (_event, input) => {
  workspaceCalls.push({method:'read',...input});
  return { root: workerRoot, path:'same.md', absolute_path: workerRoot + '/same.md', target: targetFor(input), size_bytes:80,mtime_ms:1,sha256:'worker-sha',binary:false,truncated:false,text:'# Worker workspace\n\nThese are the bytes edited by this conversation.\n' };
});
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
async function waitFor(win, fn, arg) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await evaluate(win, fn, arg)) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function settle(win) {
  await evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(requestAnimationFrame);
    await Promise.all(document.getAnimations().filter(a => a.playState === 'running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
  });
}
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1180, height: 860, show: false, webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false, preload: path.join(__dirname, 'resize-e2e-preload.cjs') } });
  await win.loadFile(path.join(desktop, 'out/renderer/index.html'));
  await waitFor(win, () => Boolean(document.querySelector('.title-actions .icon-button[aria-label]')));
  // Open the existing right workspace panel, then its new-tab picker.
  await evaluate(win, () => {
    const button = [...document.querySelectorAll('.title-actions button')].find(b => /right sidebar|右侧栏/.test(b.getAttribute('aria-label') || ''));
    if (!button) throw new Error('workspace toggle missing');
    button.click();
  });
  await waitFor(win, () => document.querySelector('.workspace-right-panel')?.getAttribute('aria-hidden') === 'false');
  await evaluate(win, () => document.querySelector('.workspace-panel-add').click());
  await waitFor(win, () => Boolean(document.querySelector('[data-wuu-component="session-environment"]')));
  await waitFor(win, () => document.querySelector('.session-environment-name')?.textContent.includes('personal-linux'));
  assert.equal(await evaluate(win, () => document.querySelector('.session-environment-status').open), false);
  await evaluate(win, () => document.querySelector('.session-environment-summary').click());
  await waitFor(win, () => document.querySelector('.session-environment-status').open);
  const samples = [];
  for (const theme of ['light', 'dark']) for (const font of [15, 20]) for (const width of [1180, 640]) {
    win.setContentSize(width, 860);
    await evaluate(win, ({theme,font}) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty('--font-ui', `${font}px`);
    }, {theme,font});
    await settle(win);
    const geometry = await evaluate(win, () => {
      const panel = document.querySelector('.session-environment-status');
      const bounds = panel.getBoundingClientRect();
      const rect = element => { const r = element.getBoundingClientRect(); return { x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height }; };
      return { panel: rect(panel), controls: [...panel.querySelectorAll('summary, button, dd, p')].map(rect), viewport: {width:innerWidth,height:innerHeight}, overflow:panel.scrollWidth > panel.clientWidth };
    });
    assert.equal(geometry.overflow, false, JSON.stringify(geometry));
    assert.ok(geometry.controls.every(r => r.x >= geometry.panel.x - 1 && r.right <= geometry.panel.right + 1), JSON.stringify(geometry));
    fs.writeFileSync(path.join(output, `${theme}-${font}-${width}.png`), (await win.webContents.capturePage()).toPNG());
    samples.push({theme,font,width,geometry});
  }
  win.setContentSize(1180, 860);
  await settle(win);
  // Refresh reads observed state; it must not navigate away or start a turn.
  state = 'disconnected';
  await evaluate(win, () => document.querySelector('.session-environment-state button').click());
  await waitFor(win, () => !document.querySelector('.session-environment-state button').disabled);
  assert.ok(await evaluate(win, () => /未连接|Not connected/.test(document.querySelector('.session-environment-state').textContent)));
  fail = true;
  await evaluate(win, () => document.querySelector('.session-environment-state button').click());
  await waitFor(win, () => Boolean(document.querySelector('.session-environment-status [role="alert"]')));
  assert.equal(await evaluate(win, () => document.querySelector('.session-environment-status').textContent.includes('synthetic raw')), false);
  assert.equal(await evaluate(win, () => document.querySelector('.session-environment-status dd')), null);
  fail = false;
  state = 'ready';
  await evaluate(win, () => document.querySelector('.session-environment-state button').click());
  await waitFor(win, () => /已连接|Connected/.test(document.querySelector('.session-environment-state').textContent));
  // Dismiss while a refresh is in flight; its result must not reopen the panel.
  hold = true;
  await evaluate(win, () => document.querySelector('.session-environment-state button').click());
  await waitFor(win, () => document.querySelector('.session-environment-state button').disabled);
  await evaluate(win, () => [...document.querySelectorAll('.title-actions button')].find(b => /right sidebar|右侧栏/.test(b.getAttribute('aria-label') || '')).click());
  assert.equal(typeof finishRead, 'function', 'refresh reached the observation RPC');
  finishRead();
  await settle(win);
  assert.equal(await evaluate(win, () => document.querySelector('.workspace-right-panel')?.getAttribute('aria-hidden')), 'true');
  hold = false;
  // The Files entry and its retained preview use the session destination.
  await evaluate(win, () => [...document.querySelectorAll('.title-actions button')].find(b => /right sidebar|右侧栏/.test(b.getAttribute('aria-label') || '')).click());
  await waitFor(win, () => document.querySelector('.workspace-right-panel')?.getAttribute('aria-hidden') === 'false');
  await evaluate(win, () => [...document.querySelectorAll('.workspace-tool-menu-item')].find(b => /^(文件|Files)$/.test(b.textContent.trim())).click());
  await waitFor(win, () => document.querySelector('file-tree-container')?.shadowRoot?.querySelector('[data-item-path="same.md"]'));
  await evaluate(win, () => {
    const row = document.querySelector('file-tree-container').shadowRoot.querySelector('[data-item-path="same.md"]');
    (row.querySelector('button') || row).click();
  });
  await waitFor(win, () => document.querySelector('.workspace-file-resource.active')?.textContent.includes('Worker workspace'));
  assert.ok(workspaceCalls.some(call => call.method === 'read' && call.target.environment_id === 'synthetic-worker'));
  for (const theme of ['light','dark']) for (const font of [15,20]) for (const width of [1180,640]) {
    win.setContentSize(width,860);
    await evaluate(win, ({theme,font}) => { document.documentElement.dataset.theme=theme; document.documentElement.style.setProperty('--font-ui',`${font}px`); }, {theme,font});
    await settle(win);
    const geometry = await evaluate(win, () => {
      const preview=document.querySelector('.workspace-file-resource.active'); const r=preview.getBoundingClientRect();
      return {x:r.x,right:r.right,width:r.width,viewport:innerWidth,overflow:preview.scrollWidth>preview.clientWidth};
    });
    assert.ok(geometry.width>0 && geometry.right<=width+1 && !geometry.overflow, JSON.stringify(geometry));
    fs.writeFileSync(path.join(output,`files-${theme}-${font}-${width}.png`),(await win.webContents.capturePage()).toPNG());
    samples.push({surface:'files',theme,font,width,geometry});
  }
  fs.writeFileSync(path.join(output, 'receipt.json'), JSON.stringify({samples,calls,workspaceCalls,verified:['session-bound Files and worker preview','production workspace picker entry','collapsed default','refresh observed state','read failure without host fallback','hidden raw diagnostics','dismiss during pending refresh','two themes','15/20px','1180/640px']}, null, 2));
  console.log('Session environment renderer acceptance passed');
  win.destroy();
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
