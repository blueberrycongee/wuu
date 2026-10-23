// Real product/main/preload/core, disposable data, and current navigation routes.
// Run only through wuu-e2e --core; no private renderer state or CSS mutation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
if (process.platform !== 'linux' || !process.env.WUU_E2E_SOURCE_COMMIT) {
  throw new Error('Use the isolated Linux wuu-e2e runner and pass WUU_E2E_SOURCE_COMMIT.');
}
const menuSmoke = process.env.WUU_REPLACEABLE_NAVIGATION_SCENARIO === 'menu-smoke';
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_REPLACEABLE_UI_OUTPUT || path.join(desktop, 'out/e2e/replaceable-architecture-navigation'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-architecture-navigation-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'synthetic', model: 'fixture' } } }));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'acceptance', name: 'Independent architecture acceptance with a deliberately long workspace title', path: project, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }], active_context: { kind: 'project', project_id: 'acceptance', cwd: project } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 15 }));
const report = { status: 'running', scenario: menuSmoke ? 'menu-smoke' : 'full-navigation-matrix', commit: process.env.WUU_E2E_SOURCE_COMMIT, platform: process.platform, versions: process.versions, cases: [], screenshots: [], limitations: ['Default product navigation baseline; full independent shell and nested plugin require separate acceptance.', 'Synthetic configuration with no inference, no real accounts.', 'Linux Electron input; not physical macOS input or compositor acceptance.', 'Shell and native overlay captures are separate surfaces, not a composed desktop screenshot.'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
const trace = value => fs.appendFileSync(path.join(output, 'chrome-events.jsonl'), JSON.stringify({at:Date.now(),...value})+'\n');
app.on('web-contents-created', (_event, contents) => {
  const send=contents.send.bind(contents);
  contents.send=(channel,...args)=>{
    if(channel==='wuu:chrome-overlay-event') trace({kind:'chrome-event',contents:contents.id,args});
    return send(channel,...args);
  };
  contents.on('console-message', (_event, details) => trace({kind:'console',contents:contents.id,url:contents.getURL(),message:details.message,level:details.level}));
  contents.on('did-fail-load', (_event, code, description, url) => trace({kind:'load-failed',contents:contents.id,code,description,url}));
  contents.on('render-process-gone', (_event, details) => trace({kind:'renderer-gone',contents:contents.id,details}));
});
ipcMain.on('wuu:chrome-overlay-painted', (event, token, generation) => trace({kind:'painted',contents:event.sender.id,token,generation}));
const registerHandler = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => registerHandler(channel, !channel.startsWith('wuu:chrome-overlay') && channel !== 'wuu:plugin-desktop-activation' ? handler : async (event,...args) => {
  try { const result=await handler(event,...args); trace({kind:'ipc',channel,args,result,sender:event.sender.id}); return result; }
  catch(error) { trace({kind:'ipc-error',channel,error:String(error),sender:event.sender.id}); throw error; }
});
let win;
app.on('browser-window-created', (_event, created) => {
  win ||= created;
  for(const event of ['focus','blur','show','hide','resize','resized']) created.on(event,()=>trace({kind:'window',event,bounds:created.getBounds(),focused:created.isFocused()}));
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
// Let the real product close its core processes before the runner collects files.
function finish(code) { app.once('will-quit',()=>app.exit(code)); app.quit(); }
async function until(check, label, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const value = await check(); if (value) return value; await delay(25); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
function visibleContents(parent = win.contentView) { return parent.children.flatMap(view => !view.getVisible() ? [] : [...(view.webContents ? [view.webContents] : []), ...visibleContents(view)]); }
function key(contents, keyCode, modifiers = []) { contents.sendInputEvent({ type: 'keyDown', keyCode, modifiers }); contents.sendInputEvent({ type: 'keyUp', keyCode, modifiers }); }
async function click(selector) {
  return until(async () => {
    for (const contents of [win.webContents, ...visibleContents()].reverse()) {
      const point = await contents.executeJavaScript(`(selector => {
        for (const element of document.querySelectorAll(selector)) {
          if (element.disabled || element.closest('[inert]')) continue;
          element.scrollIntoView({block:'nearest'});
          for (const target of [element, ...element.querySelectorAll('svg,span')]) {
            const r = target.getBoundingClientRect(), x = r.x+r.width/2, y = r.y+r.height/2;
            if (r.width && r.height && element.contains(document.elementFromPoint(x,y))) return {x,y};
          }
        }
        return null;
      })(${JSON.stringify(selector)})`);
      if (!point) continue;
      report.lastInput = { selector, contents: contents.id, point };
      win.focus(); contents.focus();
      contents.sendInputEvent({ type: 'mouseMove', ...point });
      contents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point });
      contents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point });
      return contents;
    }
    return null;
  }, `click ${selector}`);
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  report.surfaces = win.contentView.children.map(view => ({visible:view.getVisible(),bounds:view.getBounds(),url:view.webContents?.getURL()}));
  report.overlayDOM = await Promise.all(win.contentView.children.filter(view=>view.webContents).map(async view=>({url:view.webContents.getURL(),visible:view.getVisible(),dom:await view.webContents.executeJavaScript('({text:document.body.innerText,visibility:document.visibilityState,ready:document.readyState})')})));
  report.inputTrace = await evaluate(()=>window.__acceptanceMenuTrace);
  report.finalAccount = await evaluate(()=>document.querySelector('button[aria-label="Account menu"]')?.outerHTML);
  report.focus = {window:win.isFocused(),contents:win.webContents.isFocused()};
  const surfaces = [win.webContents, ...visibleContents()];
  for (const [index, contents] of surfaces.entries()) {
    const file = `${name}-${index}.png`;
    const screenshot = await contents.capturePage();
    fs.writeFileSync(path.join(output, file), screenshot.toPNG());
    report.screenshots.push({ file, url: contents.getURL(), width: screenshot.getSize().width });
  }
  save();
}
async function checkViewport() {
  const geometry = await evaluate(() => ({ viewport: { width: innerWidth, height: innerHeight }, pageWidth: document.documentElement.scrollWidth,
    composer: (() => { const e=document.querySelector('textarea'); const r=e?.getBoundingClientRect(); return r ? {x:r.x,y:r.y,width:r.width,height:r.height} : null; })(),
    focus: { tag: document.activeElement?.tagName, label: document.activeElement?.getAttribute('aria-label') },
  }));
  assert.ok(geometry.pageWidth <= geometry.viewport.width + 1, 'no horizontal page overflow');
  return geometry;
}
async function run() {
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  const rendererURL = pathToFileURL(path.join(desktop, 'out/renderer/index.html')).href;
  await until(() => win && !win.webContents.isLoadingMainFrame() && win.webContents.getURL() === rendererURL, 'shipping renderer');
  await until(() => evaluate(() => Boolean(document.querySelector('button[aria-label="Account menu"]:not(:disabled)') && document.querySelector('textarea'))), 'initialized composer and account route');
  win.show(); win.focus();
  for (const theme of menuSmoke ? ['light'] : ['light', 'dark']) for (const size of menuSmoke ? [15] : [15, 20]) for (const width of menuSmoke ? [1280] : [1280, 720]) {
    const name = `${theme}-${size}-${width}`;
    const current = { name, checks: [] };
    report.cases.push(current);
    await evaluate(async ({theme,size}) => { await window.wuu.setThemePreference(theme); await window.wuu.setMessageFlowFontSize(size); }, {theme,size});
    win.setBounds({x:0,y:0,width,height:850});
    if (report.cases.length > 1) win.webContents.reload();
    await until(() => !win.webContents.isLoadingMainFrame() && evaluate(() => Boolean(document.querySelector('textarea') && document.querySelector('button[aria-label="Account menu"]:not(:disabled)'))), 'initialized appearance');
    await click('textarea');
    key(win.webContents, 'a', ['control']); key(win.webContents, 'Backspace');
    await until(() => evaluate(() => document.querySelector('textarea').value === ''), 'empty composer');
    current.emptyGeometry = await checkViewport();
    await capture(`${name}-empty`);
    const draft = Array.from({length:18}, (_, index) => `Line ${index+1}: retained draft 内容 with a long sentence and keyboard focus.`).join('\n');
    await win.webContents.insertText(draft);
    await until(() => evaluate(text => document.querySelector('textarea')?.value === text, draft), 'long draft');
    current.longGeometry = await checkViewport();
    await capture(`${name}-long`);
    await evaluate(()=>{
      const trigger=document.querySelector('button[aria-label="Account menu"]');
      window.__acceptanceMenuTrace=[];
      window.__acceptanceMenuUnsubscribe?.();
      window.__acceptanceMenuUnsubscribe=window.wuu.chromeOverlay.onEvent(value=>window.__acceptanceMenuTrace.push({at:Date.now(),event:'public chrome event',value}));
      for(const event of ['pointerdown','pointerup','click','focusin','focusout']) document.addEventListener(event,e=>window.__acceptanceMenuTrace.push({at:Date.now(),event,target:e.target?.outerHTML?.slice(0,180)}),{capture:true});
      new MutationObserver(()=>window.__acceptanceMenuTrace.push({at:Date.now(),event:'trigger mutation',connected:trigger.isConnected,expanded:trigger.getAttribute('aria-expanded')})).observe(trigger,{attributes:true});
    });
    await click('button[aria-label="Account menu"]');
    report.afterAccountClick = await evaluate(() => ({focused:document.hasFocus(),trigger:document.querySelector('button[aria-label="Account menu"]')?.outerHTML}));
    save();
    const menu = await until(async () => {
      for (const contents of [win.webContents,...visibleContents()].reverse()) if (await contents.executeJavaScript("(() => { const e=document.querySelector('[role=menuitem][data-settings-page=providers]'); const r=e?.getBoundingClientRect(); return Boolean(r?.width && e.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2))); })()")) return contents;
      return null;
    }, 'native Settings menu item');
    await capture(`${name}-menu`);
    key(menu, 'Escape');
    await until(async () => await evaluate(() => document.activeElement?.getAttribute('aria-label') === 'Account menu') && !visibleContents().some(contents => contents.getURL().includes('surface=account')), 'Escape returns focus to Account menu');
    current.checks.push('Escape returns focus to the trigger');
    await click('button[aria-label="Account menu"]');
    await until(() => visibleContents().some(contents => contents.getURL().includes('surface=account')), 'account reopened and presented');
    await click('textarea');
    await until(async () => await evaluate(() => document.querySelector('button[aria-label="Account menu"]')?.getAttribute('aria-expanded') === 'false' && document.activeElement === document.querySelector('textarea')) && !visibleContents().some(contents => contents.getURL().includes('surface=account')), 'outside click closes presented menu and leaves focus in composer');
    assert.equal(await evaluate(() => document.querySelector('textarea').value), draft);
    current.checks.push('Presented menu dismisses on outside pointer input without taking composer focus or changing draft');
    await click('button[aria-label="Account menu"]');
    await click('[role=menuitem][data-settings-page=providers]');
    await until(() => evaluate(() => Boolean(document.querySelector('.settings-embedded'))), 'Settings page via current user path');
    current.settingsGeometry = await checkViewport();
    await capture(`${name}-settings`);
    await click('[data-wuu-component="conversations-section"]');
    await until(() => evaluate(text => !document.querySelector('.settings-embedded') && document.querySelector('textarea')?.value === text, draft), 'long draft restored after Settings');
    current.checks.push('Settings navigation preserves the exact long draft');
    await click('textarea'); key(win.webContents, 'Tab');
    assert.ok(await evaluate(() => document.activeElement !== document.body), 'keyboard traversal has a focus target');
    await capture(`${name}-keyboard-focus`);
    current.status = 'passed'; save();
  }
  report.status = 'passed'; save();
  console.log(`PASS: current default navigation in ${report.cases.length} appearances. Evidence ${output}`);
  finish(0);
}
process.on('uncaughtException', error => { report.status='failed'; report.error=String(error.stack); save(); console.error(error); finish(1); });
run().catch(async error => { report.status='failed'; report.error=String(error.stack); await capture('failure').catch(()=>{}); save(); console.error(error); finish(1); });
