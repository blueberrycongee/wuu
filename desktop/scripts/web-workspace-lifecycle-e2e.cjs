// Real Electron views and fixed local pages; no user profile or macOS desktop.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { app, BrowserWindow, ipcMain, webContents } = require('electron');
const { buildSync } = require('esbuild');
assert.equal(process.platform, 'linux');
app.disableHardwareAcceleration();
const output = path.resolve(process.env.WUU_LIFECYCLE_OUTPUT || 'out/e2e/web-workspace-lifecycle');
fs.mkdirSync(output, { recursive: true });
process.on('uncaughtException',error=>{console.error(error);fs.writeFileSync(path.join(output,'failure.txt'),String(error.stack));process.exit(1)});
app.setPath('userData', path.join(output, 'profile'));
let diagnosticState = () => ({ phase: 'starting' });
const diagnostic = (type, details = {}) => {
  const entry = { at: new Date().toISOString(), type, ...details };
  console.log('LIFECYCLE_TRACE', JSON.stringify(entry));
  fs.appendFileSync(path.join(output, 'lifecycle-trace.jsonl'), JSON.stringify(entry) + '\n');
};
app.on('web-contents-created', (_event, contents) => {
  const id = contents.id;
  const close = contents.close.bind(contents);
  contents.close = options => {
    diagnostic('close-call', { id, url: contents.isDestroyed() ? null : contents.getURL(), options });
    try { const result = close(options); diagnostic('close-return', { id }); return result; }
    catch (error) { diagnostic('close-error', { id, error: String(error) }); throw error; }
  };
  for (const event of ['close', 'destroyed', 'will-prevent-unload', 'unresponsive', 'responsive', 'render-process-gone']) {
    contents.on(event, () => diagnostic(event, { id }));
  }
});
const diagnosticTimer = setInterval(() => diagnostic('watchdog-progress', diagnosticState()), 15000);
diagnosticTimer.unref();
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, label) { for (let i = 0; i < 300; i++) { if (await fn()) return; await delay(20); } throw new Error('Timeout: ' + label); }
(async () => {
  await app.whenReady();
  // Bundle the shipping host, with real Electron webContents and session history.
  const bundle = path.resolve('out/main/lifecycle-test-host.cjs');
  buildSync({ entryPoints: ['src/main/webWorkspaceHost.ts'], outfile: bundle, bundle: true, platform: 'node', external: ['electron'], format: 'cjs' });
  const { WebWorkspaceHost } = require(bundle);
  const downloadBundle = path.resolve('out/main/lifecycle-downloads.cjs');
  buildSync({ entryPoints: ['src/main/webWorkspaceDownloads.ts'], outfile: downloadBundle, bundle: true, platform: 'node', external: ['electron'], format: 'cjs' });
  const { WebWorkspaceDownloads } = require(downloadBundle);
  const downloads = new WebWorkspaceDownloads(() => {});
  const server = http.createServer((req, res) => {
    if (req.url === '/download.bin') {
      res.writeHead(200, {'Content-Type':'application/octet-stream','Content-Length':10000000});
      res.write(Buffer.alloc(1024));
      const timer=setInterval(() => res.write(Buffer.alloc(1024)),100); res.on('close',()=>clearInterval(timer)); return;
    }
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end(`<title>${req.url}</title><style>body{height:3000px;background:#badced}</style><input id="draft"><button id="click">Action</button>${req.url === '/frame' ? '<iframe src="/editor"></iframe>' : ''}<script>window.identity=Math.random();${req.url === '/storage' ? "sessionStorage.setItem('draft','preserved')" : ''}</script>`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const win = new BrowserWindow({ width: 900, height: 700, webPreferences: { sandbox: true } });
  await win.loadURL('data:text/html,' + encodeURIComponent('<title>Lifecycle fixture</title><input id="address">'));
  const changes = [], host = new WebWorkspaceHost((_owner, state) => changes.push(state), () => {}, {downloads});
  diagnosticState = () => ({ ownerDestroyed: win.isDestroyed(), discarding: host.discarding,
    pages: [...host.pages.values()].map(page => ({ id: page.contents.id, tabId: page.tab.id,
      destroyed: page.contents.isDestroyed(), url: page.tab.url, discarding: page.discarding,
      pendingClose: Boolean(page.pendingClose), active: page.group.activeTabId === page.tab.id,
      discarded: Boolean(page.tab.discarded), protected: page.discardProtected,
      loading: !page.contents.isDestroyed() && page.contents.isLoading() })) });
  ipcMain.on('wuu:web-workspace-page-edited', event => host.protectEditedPage(event.sender));
  host.initializeTabs(win, []);
  const group = 'ribbon-browser';
  host.reportBounds(win, group, { x: 0, y: 0, width: 900, height: 650 });
  const create = async name => {
    const state = host.createTab(win, group, origin + '/' + name), id = state.activeTabId;
    let view;
    await until(() => { view = win.contentView.children.find(view => view.webContents?.getURL() === origin + '/' + name); return view && !view.webContents.isLoading(); }, name);
    return { id, view, wc: view.webContents, contentsID: view.webContents.id };
  };
  console.log('LIFECYCLE creating fixtures');
  const cold = await create('cold');
  await cold.wc.loadURL(origin + '/second');
  await cold.wc.executeJavaScript('scrollTo(0,540);void 0');
  const pinned = await create('pinned'); host.setTabPinned(win, group, pinned.id, true);
  const edited = await create('edited');
  edited.wc.focus(); await edited.wc.executeJavaScript('document.querySelector("#draft").focus()');
  await edited.wc.insertText('unsaved draft');
  const storage = await create('storage');
  const frame = await create('frame');
  const veto = await create('veto');
  await veto.wc.executeJavaScript('window.onbeforeunload=()=>false;void 0', true);
  console.log('LIFECYCLE media');
  const media = await create('media');
  await media.wc.executeJavaScript(`(async()=>{const c=document.createElement('canvas'),v=document.createElement('video');v.muted=true;c.width=64;c.height=64;v.srcObject=c.captureStream(10);document.body.append(v);window.mediaTimer=setInterval(()=>{c.getContext('2d').fillStyle=Math.random()>.5?'red':'blue';c.getContext('2d').fillRect(0,0,64,64)},50);await v.play()})()`,true);
  console.log('LIFECYCLE opener');
  const opener = await create('opener');
  await opener.wc.executeJavaScript(`window.child=window.open('${origin}/child');void 0`,true);
  let child;
  await until(()=>{child=win.contentView.children.find(v=>v.webContents?.getURL()===origin+'/child');return child&&!child.webContents.isLoading()},'opener-connected child');
  const active = await create('active');
  console.log('LIFECYCLE download');
  let download;
  active.wc.session.once('will-download',(_event,item)=>{download=item;downloads.start(win,group,item);item.setSavePath(path.join(output,'download.bin'));});
  active.wc.downloadURL(origin+'/download.bin');
  await until(()=>download && downloads.list(win).some(item=>!item.done),'pending download');
  assert.equal(await host.discardInactiveTabs(performance.now()+31*60*1000),0,'Pending downloads protect their owning group');
  download.cancel();
  await until(()=>downloads.list(win).every(item=>item.done),'download cancellation');
  const first = await host.discardInactiveTabs();
  assert.equal(cold.wc.isDestroyed(), false, 'Recently viewed tabs are retained');
  console.log('LIFECYCLE discard');
  const before = app.getAppMetrics();
  const result = await host.discardInactiveTabs(performance.now() + 31 * 60 * 1000);
  assert.ok(cold.wc.isDestroyed(), 'An idle ordinary tab releases its native contents');
  for (const page of [pinned, edited, storage, frame, veto, media, opener, active]) assert.equal(page.wc.isDestroyed(), false, 'Protected page retained: ' + page.id);
  assert.equal(child.webContents.isDestroyed(),false,'Opener-linked child retained');
  const dormant = changes.at(-1).tabs.find(tab => tab.id === cold.id);
  assert.equal(dormant.discarded, true);
  assert.equal(dormant.canGoBack, true);
  host.selectTab(win, group, cold.id);
  let restored;
  await until(() => { restored = win.contentView.children.find(view => view.webContents?.getURL() === origin + '/second'); return restored && !restored.webContents.isLoading(); }, 'restored page');
  assert.notEqual(restored.webContents.id, cold.contentsID, 'Restoration creates a fresh renderer');
  assert.equal(restored.webContents.navigationHistory.canGoBack(), true);
  await until(async () => (await restored.webContents.executeJavaScript('scrollY')) === 540, 'restored scroll');
  restored.webContents.navigationHistory.goBack();
  await until(() => restored.webContents.getURL() === origin + '/cold' && !restored.webContents.isLoading(), 'restored back navigation');
  assert.equal(await edited.wc.executeJavaScript('document.querySelector("#draft").value'), 'unsaved draft');
  const cycles=[];
  host.setTabPinned(win,group,active.id,true);
  for(let repeat=0;repeat<3;repeat++) {
    const old=restored.webContents, oldID=old.id;
    await old.executeJavaScript('scrollTo(0,540);void 0');
    host.selectTab(win,group,active.id);
    const count=await host.discardInactiveTabs(performance.now()+31*60*1000);
    assert.ok(old.isDestroyed());
    host.selectTab(win,group,cold.id);
    await until(()=>{restored=win.contentView.children.find(v=>v.webContents?.getURL()===origin+'/cold');return restored&&!restored.webContents.isLoading()},'repeated restore');
    await until(async()=>await restored.webContents.executeJavaScript('scrollY')===540,'repeated scroll restore');
    assert.equal(restored.webContents.navigationHistory.canGoForward(),true);
    cycles.push({repeat,count,oldID,newID:restored.webContents.id,views:win.contentView.children.length});
  }
  // Selection and explicit close can arrive while Chromium is still running
  // beforeunload. Exercise real native close, not a simulated destroyed event.
  host.setTabPinned(win, group, cold.id, true);
  // Chromium 152 gives beforeunload 500 ms before forced continuation.
  // Keep this real handler below that deadline; trigger the competing action
  // synchronously after native close starts, not through a long polling delay.
  const beginSlowDiscard = async (name, vetoClose = false, onStarted = () => {}) => {
    const target = await create(name);
    await target.wc.executeJavaScript(`window.onbeforeunload=()=>{const start=performance.now();while(performance.now()-start<150){};${vetoClose ? 'return false;' : ''}};void 0`, true);
    const guard = await create(name + '-guard');
    host.setTabPinned(win, group, guard.id, true);
    const publicId = host.browserSource(() => win).list().find(tab => tab.url === origin + '/' + name).tab_id;
    let closeStarted = false, actionResult, actionError;
    const close = target.wc.close.bind(target.wc);
    target.wc.close = options => {
      close(options);
      if (closeStarted) return;
      closeStarted = true;
      try {
        assert.ok([...host.pages.values()].some(page => page.tab.id === target.id && page.discarding), 'Competing action overlaps an in-flight native discard');
        assert.equal(target.wc.isDestroyed(), false);
        actionResult = onStarted(target, publicId);
        actionResult?.catch?.(() => {});
      } catch (error) { actionError = error; }
    };
    const pending = host.discardInactiveTabs(performance.now() + 31 * 60 * 1000);
    await until(() => closeStarted, 'native idle close started: ' + name);
    if (actionError) throw actionError;
    return { target, pending, publicId, actionResult };
  };
  const source = host.browserSource(() => win);
  let focusedBeforeDiscardCompletion;
  const selectedRace = await beginSlowDiscard('selected-during-discard', false, (target, publicId) => {
    assert.ok(!source.list().some(tab => tab.url === origin + '/selected-during-discard'), 'Closing contents cannot be offered to an Agent');
    assert.throws(() => source.attach(publicId, () => {}, () => {}, () => false), /no longer available/, 'An Agent cannot borrow contents already closing');
    host.selectTab(win, group, target.id);
    host.command(win, group, 'focus');
    focusedBeforeDiscardCompletion = target.wc.isFocused();
  });
  await selectedRace.pending;
  let selectedRestored;
  await until(() => { selectedRestored = win.contentView.children.find(view => view.webContents?.getURL() === origin + '/selected-during-discard'); return selectedRestored && !selectedRestored.webContents.isLoading(); }, 'selected tab restored after idle close');
  assert.ok(selectedRace.target.wc.isDestroyed());
  assert.ok(selectedRestored.getVisible(), 'Reselected tab has a visible native surface without another click');
  // Observe native focus ownership without assuming that destruction should
  // focus a replacement over newer address-bar or overlay interaction.
  const focusedAfterRestore = selectedRestored.webContents.isFocused();
  const shellFocusedAfterRestore = win.webContents.isFocused();
  assert.equal(changes.at(-1).activeTabId, selectedRace.target.id);
  assert.ok(source.list().some(tab => tab.tab_id === selectedRace.publicId), 'Restored page keeps its public Agent tab identity');
  host.setTabPinned(win, group, selectedRace.target.id, true);

  const closedRace = await beginSlowDiscard('closed-during-discard', false, target => host.closeTab(win, group, target.id));
  const explicitClose = closedRace.actionResult;
  await Promise.all([closedRace.pending, explicitClose]);
  assert.ok(!changes.at(-1).tabs.some(tab => tab.id === closedRace.target.id), 'Explicit close during idle cleanup removes the tab');

  const vetoRace = await beginSlowDiscard('veto-during-discard', true, target => host.closeTab(win, group, target.id));
  const vetoExplicitClose = vetoRace.actionResult;
  await Promise.all([vetoRace.pending.then(() => diagnostic('veto-discard-settled')), vetoExplicitClose.then(() => diagnostic('veto-explicit-close-settled'))]);
  diagnostic('veto-both-settled', { destroyed: vetoRace.target.wc.isDestroyed() });
  await delay(0);
  diagnostic('veto-native-callback-drained', { destroyed: vetoRace.target.wc.isDestroyed() });
  assert.equal(vetoRace.target.wc.isDestroyed(), false, 'Vetoed idle cleanup retains the live page');
  assert.ok(changes.at(-1).tabs.some(tab => tab.id === vetoRace.target.id), 'Vetoed concurrent close keeps its tab');
  diagnostic('veto-cleanup-start');
  await vetoRace.target.wc.executeJavaScript('window.onbeforeunload=null;void 0');
  diagnostic('veto-cleanup-finished');
  const closeFocus = [];
  for (const newerShellFocus of [false, true]) {
    const remaining = await create('keyboard-remaining-' + newerShellFocus);
    await remaining.wc.executeJavaScript('document.querySelector("#draft").focus()');
    const closing = await create('keyboard-closing-' + newerShellFocus);
    await closing.wc.executeJavaScript('window.onbeforeunload=()=>{const start=performance.now();while(performance.now()-start<150){}};void 0', true);
    await win.webContents.executeJavaScript('document.querySelector("#address").focus();document.querySelector("#address").value="new-address"');
    win.focus();
    closing.wc.focus();
    await until(() => closing.wc.isFocused(), 'keyboard close starts in the webpage');
    let overlapped = false;
    const close = closing.wc.close.bind(closing.wc);
    closing.wc.close = options => {
      close(options);
      if (newerShellFocus && !overlapped) {
        assert.equal(closing.wc.isDestroyed(), false, 'New address focus overlaps native close');
        overlapped = true;
        win.webContents.focus();
      }
    };
    closing.wc.sendInputEvent({ type: 'keyDown', keyCode: 'W', modifiers: ['control'] });
    await until(() => !changes.at(-1).tabs.some(tab => tab.id === closing.id), 'keyboard tab closes');
    await delay(0);
    const expected = newerShellFocus ? win.webContents : remaining.wc;
    assert.equal(webContents.getFocusedWebContents()?.id, expected.id, 'Close restores unowned focus without replacing a newer focus owner');
    await expected.insertText('-AFTER');
    const value = await expected.executeJavaScript(`document.querySelector(${JSON.stringify(newerShellFocus ? '#address' : '#draft')}).value`);
    assert.equal(value, newerShellFocus ? 'new-address-AFTER' : '-AFTER');
    closeFocus.push({ newerShellFocus, overlapped, focusedId: expected.id, value });
  }
  const races = { selectedRestored: true, explicitClose: true, vetoedCloseSettled: true, agentClosingPageHidden: true, closeFocus,
    focusDiagnostics: { focusedBeforeDiscardCompletion, focusedAfterRestore, shellFocusedAfterRestore } };
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ status: 'passed', sourceCommit: process.env.GITHUB_SHA ?? null, runtime: { platform: process.platform, electron: process.versions.electron, chrome: process.versions.chrome, hardwareAcceleration: false }, first, result, races, before, after: app.getAppMetrics(), restoredHistory: restored.webContents.navigationHistory.getAllEntries().map(entry => ({url:entry.url,title:entry.title})), protectedCount: 9, cycles }, null, 2));
  await veto.wc.executeJavaScript('window.onbeforeunload=null;void 0');
  host.destroyAll(); win.destroy(); server.close(); app.quit();
})().catch(error => { console.error(error); fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack)); process.exit(1); });
setTimeout(() => {
  const snapshot = diagnosticState();
  diagnostic('watchdog-timeout', snapshot);
  fs.writeFileSync(path.join(output, 'timeout-state.json'), JSON.stringify(snapshot, null, 2));
  fs.writeFileSync(path.join(output, 'failure.txt'), 'Lifecycle fixture exceeded 90 seconds; see lifecycle-trace.jsonl and timeout-state.json.');
  process.exit(1);
}, 90000).unref();
