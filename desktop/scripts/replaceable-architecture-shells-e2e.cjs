// Actual Electron/preload/Go plus independently packed public-SDK shells.
// Only run through the isolated Linux wuu-e2e entry point.
// Requires xdotool, built desktop/out + build/bin/wuu-core, and the installable
// packages produced by scripts/replaceable-architecture-author.mjs. Supply their
// container path with WUU_REPLACEABLE_PACKAGES; the default is beside this file.
// WUU_REPLACEABLE_SHELL_SCENARIO=cold-research runs the independent cold path;
// omit it for the complete default -> research -> default replacement journey.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
assert.equal(process.platform, 'linux', 'Use isolated wuu-e2e');
assert.ok(process.env.WUU_E2E_SOURCE_COMMIT);
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_REPLACEABLE_UI_OUTPUT || '/evidence/architecture-shells';
const coldResearch = process.env.WUU_REPLACEABLE_SHELL_SCENARIO?.startsWith('cold-') ?? false;
const keyboardSettings = process.env.WUU_REPLACEABLE_SHELL_SCENARIO === 'cold-keyboard';
const packages = process.env.WUU_REPLACEABLE_PACKAGES || path.join(__dirname, 'replaceable-architecture-packages');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-architecture-shells-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: path.join(desktop, 'build/bin/wuu-core'), WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0', WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;
const report = { status: 'running', source: process.env.WUU_E2E_SOURCE_COMMIT, scenario:coldResearch ? process.env.WUU_REPLACEABLE_SHELL_SCENARIO : 'hot-shell-replacement', fixture, checks: [], wire: [], screenshots: [], limitations: ['Real Go with local scripted HTTP inference; no real accounts or paid model.', 'Linux/Xvfb input and rendering; not macOS native acceptance.'] };
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2));
// Observe actual public IPC requests and their real core responses; never
// manufacture registration/render ACKs or replace a product handler result.
const registerHandler = ipcMain.handle.bind(ipcMain);
ipcMain.handle = (channel, handler) => registerHandler(channel, !['wuu:plugin-desktop-activation','wuu:plugin-runtime-request','wuu:extension-catalog-refresh'].includes(channel) ? handler : async (event,...args) => {
  const item={channel,params:args[0],sender:event.sender.id};
  try { const result=await handler(event,...args); item.result=result; return result; }
  catch(error) { item.error=String(error.message); throw error; }
  finally { fs.appendFileSync(path.join(output,'public-ipc.jsonl'),JSON.stringify(item)+'\n'); }
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
// Let the real product close its core processes before the runner collects files.
function finish(code) { app.once('will-quit',()=>app.exit(code)); app.quit(); }
let win, holdResponse;
app.on('browser-window-created', (_event, created) => { win ||= created; });
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    const input = JSON.parse(body);
    report.wire.push({ path: req.url, body: input }); save();
    if (!input.stream) { res.writeHead(200, {'Content-Type':'application/json'}); res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'Architecture fixture'},finish_reason:'stop'}]})); return; }
    res.writeHead(200, {'Content-Type':'text/event-stream'});
    const frame = (delta, finish_reason = null) => `data: ${JSON.stringify({id:'acceptance',model:'fixture',choices:[{index:0,delta,finish_reason}]})}\n\n`;
    res.write(frame({role:'assistant'}));
    if (body.includes('__HOLD__')) { holdResponse = res; res.on('close', () => { report.holdClosed = true; save(); }); return; }
    res.write(frame({content:'Independent real core answer: evidence survives a shell replacement.'}));
    res.write(frame({}, 'stop')); res.end('data: [DONE]\n\n');
  });
});
async function until(check, label, timeout = 45000) {
  const deadline = Date.now()+timeout;
  while (Date.now()<deadline) { const result = await check(); if (result) return result; await delay(25); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
function contents(parent = win.contentView) { return parent.children.flatMap(view => view.getVisible() ? [...(view.webContents ? [view.webContents] : []), ...contents(view)] : []); }
function key(target, keyCode, modifiers=[]) {
  target.sendInputEvent({type:'keyDown',keyCode,modifiers});
  if(keyCode==='Enter' || keyCode==='Space') target.sendInputEvent({type:'char',keyCode:keyCode==='Enter' ? '\r' : ' ',modifiers});
  target.sendInputEvent({type:'keyUp',keyCode,modifiers});
}
async function click(selector, text) {
  return until(async () => {
    for (const target of [win.webContents,...contents()].reverse()) {
      const point = await target.executeJavaScript(`(({selector,text}) => {
        for (const e of document.querySelectorAll(selector)) {
          if (e.disabled || (text && e.textContent.trim() !== text) || e.closest('[inert]')) continue;
          e.scrollIntoView({block:'nearest'});
          const r=e.getBoundingClientRect(), x=r.x+r.width/2, y=r.y+r.height/2;
          if(r.width && r.height && e.contains(document.elementFromPoint(x,y))) return {x,y};
        } return null;
      })(${JSON.stringify({selector,text})})`);
      if (!point) continue;
      report.checks.push({input:'click',selector,text,contents:target.id,point}); save();
      win.focus(); target.focus();
      target.sendInputEvent({type:'mouseMove',...point});
      target.sendInputEvent({type:'mouseDown',button:'left',clickCount:1,...point});
      target.sendInputEvent({type:'mouseUp',button:'left',clickCount:1,...point});
      return target;
    } return null;
  }, `click ${selector} ${text || ''}`);
}
async function type(selector, value) {
  const target = await click(selector);
  await until(()=>evaluate(selector=>document.activeElement===document.querySelector(selector),selector),'native input focus');
  key(target,'a',['control']);
  await until(()=>evaluate(selector=>{
    const input=document.querySelector(selector);
    return input.selectionStart===null || (input.selectionStart===0 && input.selectionEnd===input.value.length);
  },selector),'native select all');
  await evaluate(async ()=>{ await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))); });
  await target.insertText(value);
  await until(() => evaluate(({selector,value}) => document.querySelector(selector)?.value === value, {selector,value}), 'native typing retained');
}
async function focusByTab(selector) {
  for(let step=0;step<80;step++) {
    if(await evaluate(selector=>document.activeElement?.matches(selector),selector)) return;
    key(win.webContents,'Tab');
    await evaluate(async()=>{await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));});
  }
  throw new Error(`${selector} is not reachable through native Tab navigation`);
}
async function toggleSettings() {
  if(!keyboardSettings) return click('button[aria-label="Settings"]');
  await focusByTab('button[aria-label="Settings"]');
  key(win.webContents,'Enter');report.checks.push({input:'keyboard',control:'Settings',route:'Tab then Enter'});save();
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  report.lastDOM = await evaluate(() => {
    const root=getComputedStyle(document.documentElement), shell=document.querySelector('.research-shell'), topbar=document.querySelector('.research-topbar');
    const caption=document.querySelector('.linux-window-controls');
    return {text:document.body.innerText,active:document.activeElement?.outerHTML,
      appearance:{theme:document.documentElement.dataset.theme,tokens:Object.fromEntries(['--wuu-color-canvas','--wuu-color-text','--wuu-color-border-subtle','--wuu-font-size-ui','--font-body','--window-controls-inset-right'].map(name=>[name,root.getPropertyValue(name)])),shell:shell ? {fontSize:getComputedStyle(shell).fontSize,background:getComputedStyle(shell).backgroundColor,color:getComputedStyle(shell).color} : null,topbarBorder:topbar ? getComputedStyle(topbar).borderBottomColor : null},
      caption:caption?.getBoundingClientRect().toJSON(),
      buttons:[...document.querySelectorAll('button')].map(e=>({label:e.getAttribute('aria-label'),text:e.textContent,expanded:e.getAttribute('aria-expanded'),disabled:e.disabled,rect:e.getBoundingClientRect().toJSON()}))};
  });
  fs.writeFileSync(path.join(output,`${name}-dom.json`),JSON.stringify(report.lastDOM,null,2));
  for (const [index,target] of [win.webContents,...contents()].entries()) {
    const file=`${name}-${index}.png`; fs.writeFileSync(path.join(output,file),(await target.capturePage()).toPNG()); report.screenshots.push(file);
  } save();
}
function boot(method, params) {
  const result=spawnSync(process.env.WUU_DESKTOP_CORE,['app-server','--workdir',project],{env:process.env,input:JSON.stringify({id:1,method,params})+'\n',encoding:'utf8',timeout:45000});
  fs.appendFileSync(path.join(output,'bootstrap.log'), `${method}\n${result.stdout}\n${result.stderr}\n`);
  assert.equal(result.status,0,result.stderr || result.error?.message);
  const response=result.stdout.split('\n').filter(Boolean).map(line=>JSON.parse(line)).find(item=>item.id===1);
  assert.ok(response && !response.error,JSON.stringify(response)); return response.result;
}
async function inventory(name) {
  const value=await evaluate(() => window.wuu.initialize());
  fs.writeFileSync(path.join(output,`${name}.json`),JSON.stringify(value,null,2)); return value;
}
async function selectShell(provider, selector) {
  const file=path.join(home,'config.json'); const cfg=JSON.parse(fs.readFileSync(file,'utf8'));
  cfg.composition.services=[{service:'ui.shell',major:1,provider}]; fs.writeFileSync(file,JSON.stringify(cfg));
  await evaluate(() => window.wuu.refreshExtensionCatalog());
  const state=await inventory(provider);
  await until(() => evaluate(selector=>Boolean(document.querySelector(selector)),selector),`render ${provider}`);
  assert.ok(state.application_shell.plugin_id.includes(provider),JSON.stringify(state.application_shell));
  report.checks.push({check:'selected shell renders',provider,binding:state.application_shell}); save();
}
async function run() {
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  fs.writeFileSync(path.join(home,'config.json'),JSON.stringify({default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:`http://127.0.0.1:${server.address().port}/v1`,api_key:'fixture-only',model:'fixture'}},composition:{version:2,services:[{service:'ui.shell',major:1,provider:'wuu.default-shell'}]}}));
  fs.writeFileSync(path.join(home,'projects.json'),JSON.stringify({projects:[{id:'acceptance',name:'Independent research workspace with a long title',path:project,created_at:new Date().toISOString(),updated_at:new Date().toISOString()}],active_context:{kind:'project',project_id:'acceptance',cwd:project}}));
  fs.writeFileSync(path.join(home,'desktop-settings.json'),JSON.stringify({onboarding_version:100,language:'en-US',theme:'light',message_flow_font_size:15}));
  for(const id of ['research-workbench','research-evidence']) {
    const installed=boot('plugin/package/install',{path:path.join(packages,id)});
    const record=installed.extension_inventory.find(item=>item.kind==='plugin' && item.provenance.plugin_id===id);
    assert.ok(record,JSON.stringify(installed));
    assert.equal(record.approval_state,'granted','install grants trust without a second approval');
    assert.equal(record.enabled,true,'install enables the selected package');
    report[id]=record;
  }
  if(coldResearch) {
    const file=path.join(home,'config.json'), cfg=JSON.parse(fs.readFileSync(file,'utf8'));
    cfg.composition.services=[{service:'ui.shell',major:1,provider:'research-workbench'}];fs.writeFileSync(file,JSON.stringify(cfg));
  }
  await import(pathToFileURL(path.join(desktop,'out/main/index.js')).href);
  await until(()=>win && !win.webContents.isLoadingMainFrame() && evaluate(selector=>Boolean(document.querySelector(selector)),coldResearch ? '.research-shell' : '.composer textarea'),'initial shell connected');
  win.setBounds({x:0,y:0,width:1280,height:850});win.show();win.focus();
  const original=await inventory('initial-shell');
  assert.ok(original.application_shell.plugin_id.includes(coldResearch ? 'research-workbench' : 'wuu.default-shell'));
  if(coldResearch) {
    await click('button','New research');
    await until(()=>evaluate(()=>Boolean(document.querySelector('.research-nav button[aria-current="page"]'))),'new research selected before typing');
  }
  const initialComposer=coldResearch ? '.research-composer textarea' : '.composer textarea';
  await type(initialComposer,'Please provide independent architecture evidence.');
  if(coldResearch) await click('button','Send'); else await click('.composer-send-button');
  await until(()=>evaluate(()=>document.body.innerText.includes('Independent real core answer: evidence survives a shell replacement.')),'default real inference answer');
  await until(()=>evaluate(()=>!document.querySelector('.composer-stop-button') && ![...document.querySelectorAll('.research-composer button')].some(e=>e.textContent==='Stop')),'first turn completes');
  const listed=await evaluate(cwd=>window.wuu.listThreads(cwd),project);
  assert.equal(listed.threads.length,1,JSON.stringify(listed)); const threadID=listed.threads[0].id;report.threadID=threadID;
  // Answer-ready can precede durable turn completion and usage publication.
  const before=await until(async()=>{
    const snapshot=await evaluate(id=>window.wuu.resumeThread(id),threadID);
    return snapshot.thread.turns.length===1 && snapshot.thread.turns[0].status==='completed' ? snapshot : null;
  },'first turn durably completes before shell replacement');
  fs.writeFileSync(path.join(output,'thread-before.json'),JSON.stringify(before,null,2));
  await capture(coldResearch ? 'research-cold-completed' : 'default-completed');
  if(!coldResearch) {
    const repeated=await evaluate(id=>window.wuu.resumeThread(id),threadID);
    fs.writeFileSync(path.join(output,'thread-before-repeat.json'),JSON.stringify(repeated,null,2));
    assert.deepEqual(repeated.thread.turns,before.thread.turns,'completed turn is stable before shell replacement');
  }
  await type(initialComposer,'Retained draft across shell replacement 内容');
  // No screenshot or idle interval between the last keystroke and replacement.
  if(!coldResearch) {
    report.hotSwitchStartedAt=Date.now();
    await selectShell('research-workbench','.research-shell');
  }
  await until(()=>evaluate(()=>document.querySelector('textarea')?.value==='Retained draft across shell replacement 内容'),'draft survives shell replacement');
  await until(()=>evaluate(()=>document.querySelector('[role=log]')?.textContent.includes('Independent real core answer: evidence survives a shell replacement.')),'research shell displays same durable history');
  const after=await evaluate(id=>window.wuu.resumeThread(id),threadID);
  fs.writeFileSync(path.join(output,'thread-after.json'),JSON.stringify(after,null,2));
  assert.deepEqual(after.thread.turns,before.thread.turns);
  await click('button','Rename'); await type('.research-rename input','Independent research thread with a deliberately long title'); await click('button','Save');
  await until(()=>evaluate(()=>document.querySelector('.research-threadbar h2')?.textContent==='Independent research thread with a deliberately long title'),'rename');
  await type('.research-composer textarea','__HOLD__ running cancellation fixture'); await click('button','Send');
  await until(()=>holdResponse && evaluate(()=>[...document.querySelectorAll('button')].some(e=>e.textContent==='Stop')),'running second turn');
  assert.ok(await evaluate(()=>[...document.querySelectorAll('button')].find(e=>e.textContent==='Archive')?.disabled));
  await capture('research-running');await click('button','Stop');
  await until(()=>report.holdClosed && evaluate(()=>![...document.querySelectorAll('button')].some(e=>e.textContent==='Stop')),'cancellation reaches real HTTP stream');
  for(const theme of ['light','dark']) for(const size of [15,20]) for(const width of [1280,720]) {
    await toggleSettings();
    await until(()=>evaluate(()=>Boolean(document.querySelector('.research-settings'))),'settings open');
    await click('.research-settings select');
    const selection=spawnSync('xdotool',['key','--clearmodifiers','Home','Down',...(theme==='dark' ? ['Down'] : []),'Return'],{encoding:'utf8',timeout:5000});
    assert.equal(selection.status,0,selection.stderr || selection.error?.message);
    await until(()=>evaluate(theme=>document.querySelector('.research-settings select')?.value===theme,theme),'native theme selection');
    await type('.research-settings input',String(size));
    await until(()=>evaluate(({theme,size})=>document.documentElement.dataset.theme===theme && parseFloat(getComputedStyle(document.querySelector('.research-shell')).fontSize)===size,{theme,size}),'appearance applied to rendered shell');
    await toggleSettings();
    // Finish the native click before resizing moves its target.
    await until(()=>evaluate(()=>!document.querySelector('.research-settings') && Boolean(document.querySelector('.research-composer textarea'))),'settings close before resize');
    win.setBounds({x:0,y:0,width,height:850});
    await capture(`research-${theme}-${size}-${width}`);
    const geometry=await evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,composer:document.querySelector('.research-composer').getBoundingClientRect().toJSON()}));
    assert.ok(geometry.scroll<=geometry.width+1,JSON.stringify(geometry));
  }
  // Compact layouts intentionally hide this pane until the user opens it.
  if(await evaluate(() => document.querySelector('button[aria-label="Toggle evidence"]')?.getAttribute('aria-expanded') === 'false')) {
    await click('button[aria-label="Toggle evidence"]');
  }
  await click('button','Capture latest answer');
  await until(()=>evaluate(()=>document.querySelector('[aria-label="Evidence clips"]')?.textContent.includes('1 captured excerpts')),'nested slot evidence count');
  assert.equal(await evaluate(() => document.querySelector('[aria-label="Evidence clips"] blockquote')?.textContent), 'Independent real core answer: evidence survives a shell replacement.');
  await type('[aria-label="Evidence clips"] textarea','Working note from native keyboard.');
  await capture('research-evidence');
  await click('button','Remove clip');
  await until(() => evaluate(() => !document.querySelector('[aria-label="Evidence clips"] blockquote') && !document.querySelector('[aria-label="Evidence clips"]')?.textContent.includes('captured excerpts')), 'removing a clip updates nested details');
  assert.equal(await evaluate(() => document.querySelector('[aria-label="Evidence clips"] textarea').value),'Working note from native keyboard.');
  await click('button','Capture latest answer');
  await until(() => evaluate(() => document.querySelector('[aria-label="Evidence clips"]')?.textContent.includes('1 captured excerpts')), 'nested details update after recapture');
  const evidence=report['research-evidence'];
  await evaluate(record=>window.wuu.updateExtensionPackage({id:record.id,fingerprint:record.fingerprint,action:'disable'}),evidence);
  await until(()=>evaluate(()=>Boolean(document.querySelector('.research-shell')) && !document.querySelector('[aria-label="Evidence clips"]')),'selected shell is ready after nested unload');
  await evaluate(record=>window.wuu.updateExtensionPackage({id:record.id,fingerprint:record.fingerprint,action:'enable'}),evidence);
  await until(()=>evaluate(()=>Boolean(document.querySelector('[aria-label="Evidence clips"]'))),'nested reload');
  assert.equal(await evaluate(()=>document.querySelector('[aria-label="Evidence clips"] textarea').value),'');
  if(await evaluate(() => document.querySelector('button[aria-label="Toggle evidence"]')?.getAttribute('aria-expanded') === 'false')) await click('button[aria-label="Toggle evidence"]');
  await capture('research-reloaded');
  if(coldResearch) {
    report.status='passed';report.limitations.push('Cold research shell only; hot replacement remains a separate required red scenario.');save();console.log(`PASS cold research product acceptance: ${output}`);server.close();finish(0);return;
  }
  if(await evaluate(() => document.querySelector('button[aria-label="Toggle evidence"]')?.getAttribute('aria-expanded') === 'true')) await click('button[aria-label="Toggle evidence"]');
  await type('.research-composer textarea','Retained research draft returning to default 内容');
  await selectShell('wuu.default-shell','.composer');
  await until(()=>evaluate(()=>document.body.innerText.includes('Independent real core answer: evidence survives a shell replacement.')),'default shell restores same conversation');
  await until(()=>evaluate(()=>document.querySelector('.composer textarea')?.value==='Retained research draft returning to default 内容'),'research draft survives return to default shell');
  await capture('default-restored');
  await inventory('final-default');report.status='passed';save();console.log(`PASS real two-shell product acceptance: ${output}`);server.close();finish(0);
}
run().catch(async error=>{report.status='failed';report.error=String(error.stack);await capture('failure').catch(()=>{});save();console.error(error);server.close();finish(1);});
