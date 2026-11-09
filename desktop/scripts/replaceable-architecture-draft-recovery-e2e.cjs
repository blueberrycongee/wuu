// Run through wuu-e2e with a current renderer and an explicitly identified core.
// IPC gates reproduce delayed identity adoption and rejected draft-title persistence.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { pathToFileURL } = require('node:url');
const { app, ipcMain } = require('electron');
if (process.platform !== 'linux' || !process.env.WUU_E2E_SOURCE_COMMIT) throw new Error('Use the isolated Linux runner with source provenance.');
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_REPLACEABLE_UI_OUTPUT || '/evidence/draft-recovery';
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-draft-recovery-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
for (const dir of [output,home,project]) fs.mkdirSync(dir,{recursive:true});
app.setPath('userData',path.join(fixture,'profile'));
Object.assign(process.env,{WUU_HOME:home,WUU_DESKTOP_CORE:path.join(desktop,'build/bin/wuu-core'),WUU_SAFE_MODE:'0',WUU_ENABLE_BROWSER:'0',WUU_ENABLE_CUA_MAC:'0',WUU_ENABLE_CUA_LINUX:'0',WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP:'1'});
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(project,'review.md'),'# Acceptance document\n\nReview this paragraph before changing the implementation.\n');
const report={status:'running',source:process.env.WUU_E2E_SOURCE_COMMIT,checks:[],ipc:[],screenshots:[],limitations:['Linux/Xvfb; no physical macOS acceptance.','Real main/preload/core and renderer; deterministic IPC delay and rename rejection are explicit fault injection.','Core and renderer source identities are recorded separately.']};
const save=()=>fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2));
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(fn,label,timeout=30000){const end=Date.now()+timeout;while(Date.now()<end){const value=await fn();if(value)return value;await delay(25);}throw new Error(`Timed out: ${label}`);}
let win,gateCreation=false,releaseCreation,rejectRename;
app.on('browser-window-created',(_event,created)=>{win ||= created;});
const register=ipcMain.handle.bind(ipcMain);
ipcMain.handle=(channel,handler)=>register(channel,async(event,...args)=>{
  if(channel==='wuu:thread-start' && gateCreation){
    report.ipc.push({channel,phase:'held',args});save();
    await new Promise(resolve=>{releaseCreation=resolve;});
    const result=await handler(event,...args);report.createdThread=result.thread;save();return result;
  }
  if(channel==='wuu:thread-rename' && gateCreation){
    report.ipc.push({channel,phase:'rejected-on-release',args});save();
    await new Promise((_resolve,reject)=>{rejectRename=()=>reject(new Error('Acceptance injected rename rejection'));});
  }
  if(channel==='wuu:turn-start' && gateCreation){report.ipc.push({channel,args});save();}
  return handler(event,...args);
});
const server=http.createServer((req,res)=>{let raw='';req.on('data',c=>{raw+=c;});req.on('end',()=>{
  const input=JSON.parse(raw);if(!input.stream){res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{message:{role:'assistant',content:'Fixture document'},finish_reason:'stop'}]}));return;}
  res.writeHead(200,{'Content-Type':'text/event-stream'});
  for(const [delta,finish_reason] of [[{role:'assistant'},null],[{content:'Open the [review document](review.md) to add a comment.'},null],[{},'stop']])res.write(`data: ${JSON.stringify({id:'fixture',model:'fixture',choices:[{index:0,delta,finish_reason}]})}\n\n`);
  res.end('data: [DONE]\n\n');
});});
const evaluate=(fn,value)=>win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
function key(code,modifiers=[]){for(const type of ['keyDown',...(code==='Enter'?['char']:[]),'keyUp'])win.webContents.sendInputEvent({type,keyCode:type==='char'?'\r':code,modifiers});}
async function frames(){await evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));}
async function click(selector,text,count=1){
  const hover=await evaluate(selector=>{const el=[...document.querySelectorAll(selector)].find(e=>!e.closest('[inert]')&&e.getBoundingClientRect().height);if(!el)return null;el.scrollIntoView({block:'nearest'});const r=el.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2};},selector);
  if(hover){win.focus();win.webContents.focus();win.webContents.sendInputEvent({type:'mouseMove',...hover});await frames();}
  const point=await until(()=>evaluate(({selector,text})=>{for(const el of document.querySelectorAll(selector)){if(el.disabled || el.closest('[inert]') || (text && el.textContent.trim()!==text))continue;el.scrollIntoView({block:'nearest'});const r=el.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2;if(r.width&&r.height&&el.contains(document.elementFromPoint(x,y)))return{x,y};}return null;},{selector,text}),`click ${selector} ${text||''}`);
  win.focus();win.webContents.focus();win.webContents.sendInputEvent({type:'mouseMove',...point});
  for(const type of ['mouseDown','mouseUp'])win.webContents.sendInputEvent({type,button:'left',clickCount:count,...point});
}
async function type(selector,text){await click(selector);key('a',['control']);await frames();await win.webContents.insertText(text);await until(()=>evaluate(({selector,text})=>[...document.querySelectorAll(selector)].some(el=>el.value===text),{selector,text}),'typed value');}
async function capture(name){await frames();const dom=await evaluate(()=>({text:document.body.innerText,active:document.activeElement?.outerHTML,buttons:[...document.querySelectorAll('button')].map(e=>({text:e.textContent,aria:e.getAttribute('aria-label'),class:e.className})),textareas:[...document.querySelectorAll('textarea')].map(e=>({value:e.value,class:e.className,visible:!!e.getBoundingClientRect().height})),rows:[...document.querySelectorAll('.thread-row-main')].map(e=>({text:e.textContent,aria:e.getAttribute('aria-label')}))}));fs.writeFileSync(path.join(output,`${name}.json`),JSON.stringify(dom,null,2));const file=`${name}.png`;fs.writeFileSync(path.join(output,file),(await win.webContents.capturePage()).toPNG());report.screenshots.push(file);save();}
const composer='[data-main-conversation-composer] textarea:not(.composer-response-selection-comment)';
async function addComment(text){
  await click('.file-selection-content p',undefined,2);
  await until(()=>evaluate(()=>Boolean(document.querySelector('.file-selection-action-menu'))),'native document selection menu');
  await click('.file-selection-action-menu button','Comment');
  await type('.file-selection-action-menu textarea',text);
  key('Enter');
  await until(()=>evaluate(text=>[...document.querySelectorAll('.file-selection-comment p')].some(e=>e.textContent===text),text),'comment added');
}
async function assertDraft(label){
  const state=await evaluate(({composer})=>{const el=[...document.querySelectorAll(composer)].find(e=>e.getBoundingClientRect().height>0&&!e.closest('[inert]'));return{prompt:el?.value,focused:document.activeElement===el,comments:[...document.querySelectorAll('.file-selection-comment p')].map(e=>e.textContent)};},{composer});
  assert.equal(state.prompt,'Next question retained 内容',label);assert.deepEqual(state.comments,['Next comment retained 内容'],label);assert.equal(state.focused,true,`${label}: native composer focus`);report.checks.push({label,...state});save();
}
function finish(code){server.close();app.once('will-quit',()=>app.exit(code));app.quit();}
async function run(){
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  fs.writeFileSync(path.join(home,'config.json'),JSON.stringify({default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:`http://127.0.0.1:${server.address().port}/v1`,api_key:'fixture',model:'fixture'}}}));
  fs.writeFileSync(path.join(home,'projects.json'),JSON.stringify({projects:[{id:'acceptance',name:'Draft recovery workspace',path:project,created_at:new Date().toISOString(),updated_at:new Date().toISOString()}],active_context:{kind:'project',project_id:'acceptance',cwd:project}}));
  fs.writeFileSync(path.join(home,'desktop-settings.json'),JSON.stringify({onboarding_version:100,language:'en-US',theme:'light',message_flow_font_size:15}));
  await import(pathToFileURL(path.join(desktop,'out/main/index.js')).href);
  await until(()=>win&&!win.webContents.isLoadingMainFrame()&&evaluate(()=>Boolean(document.querySelector('.composer textarea'))),'default shell');win.setBounds({x:0,y:0,width:1500,height:1000});win.show();win.focus();
  await type('.composer textarea','Show the review document');await click('.composer-send-button');
  await until(()=>evaluate(()=>document.body.innerText.includes('Open the review document')&&!document.querySelector('.composer-stop-button')),'real setup answer');
  await click('.rich-file-link','review document');
  await until(()=>evaluate(()=>Boolean(document.querySelector('.file-selection-content p'))),'real file preview');
  await click('.project-row-new-thread[aria-label="Start a new conversation in Draft recovery workspace"]');
  await until(()=>evaluate(()=>!document.querySelector('.composer-stop-button')),'new draft');
  await click('.conversation-title-rename',undefined,2);await type('.conversation-title-edit','Rejected draft name');key('Enter');
  await addComment('Original comment');await type(composer,'Original question');
  gateCreation=true;await click('[data-main-conversation-composer] .composer-send-button');await until(()=>releaseCreation,'creation delayed');
  await addComment('Next comment retained 内容');await type(composer,'Next question retained 内容');
  await assertDraft('before identity binding');await capture('before-binding');
  releaseCreation();await until(()=>rejectRename,'server identity adopted and rename pending');await frames();
  await assertDraft('after identity binding');await capture('after-binding');
  key('Escape');
  await until(()=>evaluate(()=>Boolean(document.querySelector('.turn[data-turn-status="interrupted"]'))),'native Escape stops pending send');
  rejectRename();await until(()=>evaluate(()=>document.body.innerText.includes('Acceptance injected rename rejection')),'rejection surfaced');await frames();
  await assertDraft('after stopped rename rejection');
  const rows=await evaluate(()=>[...document.querySelectorAll('.thread-row-main')].map(e=>e.getAttribute('aria-label')||e.textContent));
  assert.ok(rows.some(text=>text.includes('Original question')),JSON.stringify(rows));assert.ok(rows.every(text=>!text.includes('Rejected draft name')),JSON.stringify(rows));
  assert.equal(report.ipc.filter(e=>e.channel==='wuu:turn-start').length,0,'Stop prevents the pending turn from being submitted');
  await until(()=>evaluate(()=>{const row=document.querySelector('.thread-row[aria-current="page"] .thread-row-main');return row?.textContent.trim()===document.querySelector('.conversation-title-rename')?.textContent.trim();}),'rendered sidebar title animation settles');
  await capture('rename-reverted');
  const heading=await evaluate(()=>document.querySelector('.conversation-title-rename')?.textContent.trim());
  assert.equal(heading,'Original question','Rename rollback uses the visible user prompt, without serialized file-reference instructions');
  report.checks.push({label:'rejected draft title restored in sidebar',rows});report.status='passed';save();console.log(`PASS draft identity and title rollback: ${output}`);finish(0);
}
run().catch(async error=>{report.status='failed';report.error=String(error.stack);await capture('failure').catch(()=>{});save();console.error(error);finish(1);});
