// Real Electron/main/preload/core acceptance with disposable data and native PTYs.
// Build once before running. No fake RPC result, inference, or installed profile.
// WUU_HANDOFF_MAIN=/.../out/main/index.js WUU_DESKTOP_CORE=/.../wuu-core
// WUU_HANDOFF_COMMIT=<frozen source> WUU_HANDOFF_MODE=start|stop|web|quit electron this-file
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {createHash} = require('node:crypto');
const {pathToFileURL} = require('node:url');
const {app, BrowserWindow, ipcMain} = require('electron');
const desktop = path.resolve(__dirname, '..');
const main = path.resolve(process.env.WUU_HANDOFF_MAIN || path.join(desktop, 'out/main/index.js'));
const core = path.resolve(process.env.WUU_DESKTOP_CORE || path.join(desktop, 'build/bin/wuu-core'));
const mode = process.env.WUU_HANDOFF_MODE || 'start';
assert.ok(['start','stop','web','quit'].includes(mode));
assert.ok(fs.existsSync(main), 'Build the selected main/preload/renderer first');
assert.ok(fs.existsSync(core), 'Build or explicitly select WUU_DESKTOP_CORE first');
assert.ok(process.env.WUU_HANDOFF_COMMIT, 'Frozen source identity required');
const parent = process.env.WUU_HANDOFF_OUTPUT || path.join(os.tmpdir(), 'wuu-terminal-handoff-evidence');
fs.mkdirSync(parent, {recursive:true});
const output = fs.mkdtempSync(path.join(parent, `${mode}-`));
const home = path.join(output,'home'), userHome = path.join(output,'user-home');
for (const dir of [home,userHome]) fs.mkdirSync(dir);
const projects = ['A','B'].map((name,i) => {
  const cwd = path.join(output, `workspace-${name}`); fs.mkdirSync(cwd);
  return {id:`project-${i}`,name:`Handoff ${name}`,path:cwd,created_at:'2026-01-01T00:00:00Z',updated_at:'2026-01-01T00:00:00Z'};
});
app.setPath('userData',path.join(output,'profile'));
Object.assign(process.env,{WUU_HOME:home,HOME:userHome,CODEX_HOME:path.join(userHome,'.codex'),GROK_HOME:path.join(userHome,'.grok'),WUU_DESKTOP_CORE:core,WUU_SAFE_MODE:'0',WUU_ENABLE_BROWSER:'1',WUU_ENABLE_CUA_MAC:'0',WUU_ENABLE_CUA_LINUX:'0',WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP:'1'});
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(home,'config.json'),JSON.stringify({default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:'http://127.0.0.1:1/v1',api_key:'synthetic',model:'no-inference'}},engines:Object.fromEntries(['codex','claude','cursor','devin','dsh','grok','hermes','pi','opencode','antigravity'].map(id=>[id,{enabled:false}]))}));
fs.writeFileSync(path.join(home,'projects.json'),JSON.stringify({projects,active_context:{kind:'project',project_id:projects[0].id,cwd:projects[0].path}}));
fs.writeFileSync(path.join(home,'desktop-settings.json'),JSON.stringify({onboarding_version:100,language:'en',theme:'light'}));
const hash=file=>createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const builtRoot=path.dirname(path.dirname(main));
const builtHashes={};
function hashBuild(dir){for(const entry of fs.readdirSync(dir,{withFileTypes:true})){const file=path.join(dir,entry.name);if(entry.isDirectory())hashBuild(file);else if(entry.isFile())builtHashes[path.relative(builtRoot,file)]=hash(file);}}
for(const part of ['main','preload','renderer'])hashBuild(path.join(builtRoot,part));
const report={status:'running',mode,source:process.env.WUU_HANDOFF_COMMIT,output,platform:process.platform,versions:process.versions,hashes:{core:hash(core),main:hash(main),built:builtHashes,harness:hash(__filename)},events:[],screenshots:[],limitations:['Real Electron/main/preload/core and native PTYs; controlled IPC hold, not a field root-cause reproduction.','Native Chromium input on isolated profile; not physical macOS/compositor acceptance when run on Linux.','No paid inference or synthetic queue acknowledgement. Web resource handoff intentionally remains blocking.']};
const save=()=>fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2));
const record=(phase,fields={})=>report.events.push({at_ms:+performance.now().toFixed(3),phase,...fields});
function deferred(){let release;const promise=new Promise(resolve=>{release=resolve;});return {promise,release,released:false};}
const gate=deferred();
const release=()=>{if(!gate.released){gate.released=true;record('gate_released');gate.release();}};
let oldID, gateEntered=false, projectSelected=false, win, shutdownRequested=false, targetInitialized=false;
const starts=[],stops=[],acks=[],stopResults=[];
const activeReports=new Map();let pendingReports=0;
const handle=ipcMain.handle.bind(ipcMain);
ipcMain.handle=(channel,listener)=>handle(channel,async (...args)=>{
  const start=performance.now();
  if(channel==='wuu:plugin-desktop-activation')pendingReports++;
  if(channel==='wuu:project-select') {projectSelected=true;record('project_select');}
  if(channel==='wuu:turn-start'||channel==='wuu:turn-queue') throw new Error('Acceptance must never submit inference');
  if(channel==='wuu:terminal-stop') {
    const id=args[1]; record('terminal_stop_enter',{id});
    if(mode==='stop'&&id===oldID&&!gate.released){gateEntered=true;record('gate_entered',{channel,id});await gate.promise;}
    const result=await listener(...args);stops.push(id);stopResults.push({id,ok:result.ok});record('terminal_stop_done',{id,ok:result.ok});assert.equal(result.ok,true,'Stopping an owned native PTY must succeed');assert.ok(starts.some(entry=>entry.id===id),'Cleanup attempted a foreign PTY');return result;
  }
  const result=await listener(...args);
  if(channel==='wuu:plugin-desktop-activation'){pendingReports--;const p=args[1];const key=[p.id,p.graph_generation,p.activation].join(':');if(p.phase==='registration'&&p.status==='active')activeReports.set(p.id,key);if(p.phase==='cleanup'&&activeReports.get(p.id)===key)activeReports.delete(p.id);record('activation_report_done',{stage:p.phase,status:p.status,remaining:activeReports.size});}
  if(channel==='wuu:terminal-start') {
    const id=result.id;starts.push({id,afterSelection:projectSelected,workspace:projects.findIndex(project=>project.path===result.cwd)});oldID ||= id;record('terminal_start_native',{id,afterSelection:projectSelected});
    if((mode==='start'||mode==='quit')&&id===oldID&&!gate.released){gateEntered=true;record('gate_entered',{channel,id});await gate.promise;}
    record('terminal_start_reply',{id});
  }
  if(channel==='wuu:web-workspace-tabs-initialize'&&mode==='web'&&!gateEntered&&!projectSelected){gateEntered=true;record('gate_entered',{channel});await gate.promise;}
  if(channel==='wuu:initialize'&&projectSelected&&result.workspace_root===projects[1].path)targetInitialized=true;
  if(['wuu:initialize','wuu:project-select','wuu:plugin-desktop-activation','wuu:web-workspace-tabs-initialize'].includes(channel)) record('ipc_done',{channel,duration_ms:+(performance.now()-start).toFixed(3)});
  return result;
});
ipcMain.on('wuu:renderer-shutdown-complete',(event,error)=>{if(win&&event.sender===win.webContents){if(mode==='quit'&&!gate.released){desiredExit=1;report.status='failed';report.error='Renderer shutdown ACK escaped while retired PTY was still held';record('early_shutdown_ack');save();}acks.push({error:error===undefined?null:String(error)});record('renderer_shutdown_ack',{error:error===undefined?null:String(error)});}});
app.on('web-contents-created',(_event,contents)=>{
  const send=contents.send.bind(contents);
  contents.send=(channel,...args)=>{if(channel==='wuu:renderer-shutdown'){record('renderer_shutdown_sent');shutdownRequested=true;}return send(channel,...args);};
});
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check,label,timeout=15000){const deadline=performance.now()+timeout;while(performance.now()<deadline){const value=await check();if(value)return value;await delay(25);}throw new Error(`Timed out: ${label}`);}
const evaluate=(fn,arg)=>win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
const frames=()=>evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
async function click(selector,text){
  const point=await until(()=>evaluate(({selector,text})=>{
    const node=[...document.querySelectorAll(selector)].find(node=>(!text||node.textContent.includes(text)||node.getAttribute('aria-label')?.includes(text))&&!node.disabled&&node.getBoundingClientRect().width);
    if(!node)return null;node.scrollIntoView({block:'nearest',behavior:'instant'});const rect=node.getBoundingClientRect();return {x:Math.round(rect.x+rect.width/2),y:Math.round(rect.y+rect.height/2)};
  },{selector,text}),`find ${selector} ${text||''}`);
  await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent',{type:'mouseMoved',...point});
  await until(()=>evaluate(({selector,text,point})=>[...document.querySelectorAll(selector)].some(node=>(!text||node.textContent.includes(text)||node.getAttribute('aria-label')?.includes(text))&&node.contains(document.elementFromPoint(point.x,point.y))),{selector,text,point}),`hit-test ${selector}`);
  await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...point});
  await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...point});
  record('native_click',{selector,text});
}
const ready=index=>evaluate(index=>{
  const title=[...document.querySelectorAll('[data-wuu-component="conversation-titlebar"]')].find(node=>node.getBoundingClientRect().height)?.textContent;
  const input=[...document.querySelectorAll('.composer textarea')].find(node=>node.getBoundingClientRect().height&&!node.closest('[inert]'));
  return !!(title?.includes(`Handoff ${index?'B':'A'}`)&&input&&!input.disabled&&!input.readOnly&&!document.querySelector('.view-switch-loading-conversation,.view-switch-loading,[data-wuu-presentation-frame]'));
},index);
async function snapshot(name){await frames();const file=`${name}.png`;fs.writeFileSync(path.join(output,file),(await win.webContents.capturePage()).toPNG());report.screenshots.push(file);save();}
async function sidebarProbe(){await evaluate(()=>{
  const node=document.querySelector('.sidebar');if(!node)throw new Error('Sidebar is missing');
  window.__terminalHandoffProbe={node,disconnections:0,samples:0};
  const observer=new MutationObserver(records=>{const p=window.__terminalHandoffProbe;p.samples++;if(!p.node.isConnected||records.some(record=>[...record.removedNodes].some(removed=>removed===p.node||removed.contains?.(p.node))))p.disconnections++;});observer.observe(document.body,{subtree:true,childList:true});
  window.__terminalHandoffProbe.observer=observer;
});}
async function checkSidebar(){const result=await evaluate(()=>{const p=window.__terminalHandoffProbe;return {same:p.node===document.querySelector('.sidebar'),connected:p.node.isConnected,disconnections:p.disconnections,samples:p.samples};});report.sidebar=result;assert.equal(result.same,true);assert.equal(result.connected,true);assert.equal(result.disconnections,0);save();}
let desiredExit=0;
const watchdog=setTimeout(()=>{release();record('watchdog');save();app.exit(2);},90000);
app.once('will-quit',()=>{
  try{
    assert.equal(report.status,'awaiting-normal-quit','Unexpected native exit before acceptance completed');
    if(report.status==='awaiting-normal-quit'){
      assert.ok(shutdownRequested,'Product renderer shutdown was not requested');assert.equal(acks.length,1);assert.equal(acks[0].error,null);assert.ok(stopResults.every(result=>result.ok===true&&starts.some(entry=>entry.id===result.id)),'Every native stop must succeed and address a created PTY');for(const entry of starts)assert.ok(stops.includes(entry.id),`PTY ${entry.id} was not drained through the renderer owner`);
      report.status='passed';record('normal_quit_completed');save();
    }
  }catch(error){desiredExit=1;report.status='failed';report.error ||= String(error);save();}
  clearTimeout(watchdog);console.log('HANDOFF_REPORT',JSON.stringify({output,status:report.status,mode}));app.exit(desiredExit);
});
(async()=>{
  record('launch');await import(pathToFileURL(main).href);
  win=await until(()=>BrowserWindow.getAllWindows().find(w=>w.webContents.getURL().endsWith('/renderer/index.html')),'product window');
  win.setBounds({x:0,y:0,width:1400,height:950});win.show();win.focus();win.webContents.debugger.attach('1.3');
  await until(()=>ready(0),'source draft editable');await frames();
  {
    await click('[data-wuu-tool="terminal"]');await until(()=>oldID,'real source PTY created');
    if(mode==='stop')await until(()=>evaluate(()=>Boolean(document.querySelector('.xterm-helper-textarea'))),'terminal mounted');
  }
  if(mode==='web')await until(()=>gateEntered,'source web initialize held');
  await sidebarProbe();await snapshot('source');
  const start=performance.now();report.navigationProbeStartMs=+start.toFixed(3);report.targetEditableTiming='Harness time immediately before finding/scrolling/hovering and dispatching the native project-new-draft click, through the first editable target DOM observation; includes probe IPC and polling, excludes setup, typing and screenshots. Not physical input-to-paint latency.';await click('.project-row-new-thread','Handoff B');await until(()=>projectSelected,'target selection IPC');
  if(mode==='stop')await until(()=>gateEntered,'retired PTY stop held');
  if(mode==='web'){
    await until(()=>targetInitialized&&stops.includes(oldID),'target graph initialized and source resources retired before web barrier');
    // Hold past externally observed retirement and monitor the entire interval.
    // A barrier-removal negative control fails here; do not reduce this to a
    // single pre-release snapshot, which could pass during normal activation.
    const heldUntil=performance.now()+750;while(performance.now()<heldUntil){assert.equal(await ready(1),false,'Successor escaped the old web ownership barrier');await delay(25);}
    assert.equal(gate.released,false);report.webBlockedBeforeRelease=true;await snapshot('web-held');release();
  }else assert.equal(gateEntered,true);
  await until(()=>ready(1),'target editable while terminal IPC held');report.targetEditableObservedMs=+performance.now().toFixed(3);report.targetEditableMs=+(report.targetEditableObservedMs-start).toFixed(3);
  if(mode!=='web')assert.equal(gate.released,false,'Target must become editable before old PTY join settles');
  await checkSidebar();await click('.composer textarea');await win.webContents.insertText('target remains editable');
  await until(()=>evaluate(()=>[...document.querySelectorAll('.composer textarea')].some(node=>node.value==='target remains editable'&&!node.disabled)),'native draft edit');
  await snapshot('target-editable');
  if(mode==='web'){
    report.status='awaiting-normal-quit';record('web_control_passed');app.quit();return;
  }
  const beforeTargetTerminal=starts.length;
  await click('[data-wuu-tool="terminal"]');
  const next=await until(()=>starts.slice(beforeTargetTerminal).find(entry=>entry.id!==oldID),'new target PTY opened after editable successor');report.ids={old:oldID,next:next.id};assert.equal(next.workspace,1,'Successor PTY must belong to target workspace');
  assert.equal(stops.includes(next.id),false,'Old cleanup must not target successor PTY');
  const live=await evaluate(id=>window.wuu.writeTerminalSession(id,'printf HANDOFF_LIVE\\n\r'),next.id);assert.equal(live.ok,true);
  if(mode==='quit'){
    report.status='awaiting-normal-quit';record('normal_quit_requested');app.quit();
    await until(()=>shutdownRequested&&stops.includes(next.id)&&activeReports.size===0&&pendingReports===0,'last writer and all registered plugins retired during normal quit');
    await delay(750);
    assert.equal(acks.length,0,'Native renderer ACK must await retired PTY join');assert.equal(gate.released,false);report.quitWaitedForOldPTY=true;release();return;
  }
  release();await until(()=>stops.includes(oldID),'old PTY cleanup completed');assert.equal(stops.filter(id=>id===oldID).length,1,'Retired PTY cleanup must be idempotent');
  assert.equal(stops.includes(next.id),false,'Late old cleanup stopped new PTY');
  assert.equal((await evaluate(id=>window.wuu.writeTerminalSession(id,'printf HANDOFF_SURVIVED\\n\r'),next.id)).ok,true);
  report.status='awaiting-normal-quit';record('normal_quit_requested');app.quit();
})().catch(async error=>{
  desiredExit=1;report.status='failed';report.error=String(error);record('failed');console.error(error);
  try{if(win&&!win.isDestroyed()){report.dom=await evaluate(()=>({text:document.body.innerText.slice(0,12000),input:document.querySelector('.composer textarea')?.outerHTML}));await snapshot('failure');}}catch{}
  save();release();app.quit();
});
