// Real React library/address controls -> Electron IPC -> shipping persistence.
// Covers targeted deletion, cross-window suggestions, bookmark identity, failed
// saves retaining the draft, and a fresh store reading the persisted result.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_LIBRARY_EDIT_OUTPUT || path.join(root, 'out/e2e/browser-library-edit'));
const electron = process.versions.electron ? require('electron') : process.env.WUU_LIBRARY_EDIT_ELECTRON || require('electron');
if (typeof electron === 'string') {
  fs.mkdirSync(output, { recursive: true });
  fs.rmSync(path.join(output, 'report.json'), { force: true });
  fs.rmSync(path.join(output, 'failure.txt'), { force: true });
  const build = require('esbuild').buildSync;
  build({ entryPoints: [path.join(root, 'src/main/webWorkspaceLibrary.ts')], outfile: path.join(output, 'library.cjs'), bundle: true, platform: 'node', format: 'cjs' });
  build({ stdin: { contents: `import {createRoot} from 'react-dom/client';import {useRef,useState} from 'react';import {BrowserLibrary} from './src/renderer/BrowserLibrary';import {WebWorkspaceAddress} from './src/renderer/WebWorkspaceAddress';import './src/renderer/styles.css';function Fixture(){const [value,setValue]=useState('');const input=useRef(null);return <div style={{padding:24}}><WebWorkspaceAddress api={window.wuu.webWorkspace} pageKey="fixture" active inputRef={input} value={value} disabled={false} invalid={false} onChange={setValue} onSubmit={()=>true} onEscape={()=>{}}/><BrowserLibrary api={window.wuu.webWorkspace} currentURL="https://example.test/keep" currentTitle="Original bookmark" onOpen={()=>{}}/></div>}createRoot(document.getElementById('root')).render(<Fixture/>);`, resolveDir: root, loader: 'tsx' }, outfile: path.join(output, 'renderer.js'), bundle: true, platform: 'browser', format: 'iife', jsx: 'automatic', define: { 'import.meta.env.DEV': 'false', 'import.meta.env.MODE': '"production"' }, loader: { '.woff2': 'file', '.woff': 'file', '.png': 'file', '.svg': 'dataurl' } });
  fs.writeFileSync(path.join(output, 'index.html'), '<!doctype html><meta charset="UTF-8"><link rel="stylesheet" href="renderer.css"><div id="root"></div><script src="renderer.js"></script>');
  fs.writeFileSync(path.join(output, 'preload.cjs'), `const{contextBridge,ipcRenderer}=require('electron');contextBridge.exposeInMainWorld('wuu',{webWorkspace:{capabilities:{nativeOverlays:false},library:{getState:()=>ipcRenderer.invoke('library:get'),setBookmark:(...args)=>ipcRenderer.invoke('library:bookmark',...args),removeHistory:id=>ipcRenderer.invoke('library:remove',id),clearHistory:()=>ipcRenderer.invoke('library:clear'),onState:handler=>{const listener=(_event,state)=>handler(state);ipcRenderer.on('library:state',listener);return()=>ipcRenderer.removeListener('library:state',listener)}}}});`);
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-library-edit-'));
  try {
    const result = spawnSync(electron, [...(process.platform === 'linux' ? ['--no-sandbox'] : []), __filename], { encoding: 'utf8', env: { ...process.env, WUU_LIBRARY_EDIT_OUTPUT: output, WUU_LIBRARY_EDIT_FIXTURE: fixture } });
    fs.writeFileSync(path.join(output, 'run.log'), result.stdout + result.stderr);
    process.stdout.write(result.stdout); process.stderr.write(result.stderr);
    assert.equal(result.status, 0, 'browser library edit E2E must pass');
    assert.equal(JSON.parse(fs.readFileSync(path.join(output, 'report.json'), 'utf8')).status, 'passed');
  } finally { fs.rmSync(fixture, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }); }
} else {
  const { app, BrowserWindow, ipcMain } = electron;
  const fixture = process.env.WUU_LIBRARY_EDIT_FIXTURE;
  app.on('window-all-closed', () => {});
  app.setPath('userData', path.join(fixture, 'profile'));
  const timeout = setTimeout(() => app.exit(1), 30000);
  (async () => {
    await app.whenReady();
    const { WebWorkspaceLibrary } = require(path.join(output, 'library.cjs'));
    const file = path.join(fixture, 'library.json');
    const library = new WebWorkspaceLibrary(file);
    const removedURL = 'https://example.test/remove', keepURL = 'https://example.test/keep';
    library.recordVisit({ url: removedURL, title: 'Remove only this history' });
    library.recordVisit({ url: keepURL, title: 'Preserved history' });
    library.setBookmark(keepURL, 'Original bookmark', true);
    const original = library.getState().bookmarks[0];
    const windows = [0, 1].map(() => new BrowserWindow({ width: 1000, height: 800, show: false, webPreferences: { preload: path.join(output, 'preload.cjs'), contextIsolation: true, sandbox: true } }));
    const broadcast = state => { for (const window of windows) window.webContents.send('library:state', state); return state; };
    ipcMain.handle('library:get', () => library.getState());
    ipcMain.handle('library:bookmark', (_event, ...args) => broadcast(library.setBookmark(...args)));
    ipcMain.handle('library:remove', (_event, id) => broadcast(library.removeHistory(id)));
    ipcMain.handle('library:clear', () => broadcast(library.clearHistory()));
    for (const window of windows) await window.loadFile(path.join(output, 'index.html'));
    const [main, other] = windows.map(window => window.webContents);
    const evaluate = (wc, fn, ...args) => wc.executeJavaScript(`(${fn})(...${JSON.stringify(args)})`, true);
    const wait = async (wc, fn, ...args) => { for (let attempt = 0; attempt < 200; attempt++) { if (await evaluate(wc, fn, ...args)) return; await new Promise(resolve => setTimeout(resolve, 25)); } throw new Error('UI condition timed out: ' + fn); };
    const click = (wc, selector) => evaluate(wc, selector => document.querySelector(selector).click(), selector);
    const fill = (wc, selector, value) => evaluate(wc, (selector, value) => { const input = document.querySelector(selector); input.focus(); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); }, selector, value);
    await wait(main, () => document.querySelector('[data-browser-library-trigger]'));
    await fill(other, '.workspace-browser-url-input', 'example');
    await wait(other, () => document.querySelectorAll('[data-address-suggestion-url]').length === 2);
    await click(main, '[data-browser-library-trigger]');
    await wait(main, () => document.querySelector('[data-library-remove-history]'));
    const removedID = library.getState().history.find(entry => entry.url === removedURL).id;
    await click(main, `[data-library-remove-history="${removedID}"]`);
    await wait(main, () => document.querySelectorAll('[data-library-entry]').length === 1);
    await wait(other, url => !document.querySelector(`[data-address-suggestion-url="${url}"]`), removedURL);
    assert.deepEqual(library.getState().history.map(entry => entry.url), [keepURL]);
    await click(main, '[data-library-section="bookmarks"]');
    await click(main, '[data-library-edit-bookmark]');
    await fill(main, '[data-library-bookmark-title]', 'Daily reading');
    fs.renameSync(file, file + '.saved'); fs.mkdirSync(file);
    await click(main, '[data-library-save-bookmark]');
    await wait(main, () => document.querySelector('[role=alert]') && !document.querySelector('[data-library-save-bookmark]').disabled);
    assert.equal(await evaluate(main, () => document.querySelector('[data-library-bookmark-title]').value), 'Daily reading');
    assert.equal(library.getState().bookmarks[0].title, original.title);
    fs.rmdirSync(file); fs.renameSync(file + '.saved', file);
    await click(main, '[data-library-save-bookmark]');
    await wait(main, () => !document.querySelector('[data-library-bookmark-title]'));
    assert.deepEqual(library.getState().bookmarks[0], { ...original, title: 'Daily reading' });
    await wait(other, () => document.querySelector('[data-address-suggestion-source="bookmark"]')?.textContent.includes('Daily reading'));
    await click(main, '[data-library-edit-bookmark]');
    await fill(main, '[data-library-bookmark-title]', '');
    await click(main, '[data-library-save-bookmark]');
    await wait(main, () => !document.querySelector('[data-library-bookmark-title]'));
    const restarted = new WebWorkspaceLibrary(file).getState();
    assert.equal(restarted.history.length, 1); assert.equal(restarted.history[0].url, keepURL);
    assert.equal(restarted.bookmarks.length, 1); assert.equal(restarted.bookmarks[0].id, original.id); assert.equal(restarted.bookmarks[0].title, keepURL);
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ status: 'passed', platform: process.platform, electron: process.versions.electron, checks: ['targeted history deletion', 'other history retained', 'cross-window address suggestions updated', 'bookmark rename preserves identity', 'failed save preserves editor draft and original bookmark', 'empty title follows URL fallback', 'fresh store preserves deletion and bookmark'] }, null, 2));
    console.log('PASS: library deletion, rename, draft recovery, cross-window suggestions, persistence');
    clearTimeout(timeout); windows.forEach(window => window.destroy()); app.exit(0);
  })().catch(error => { console.error(error); fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack)); app.exit(1); });
}
