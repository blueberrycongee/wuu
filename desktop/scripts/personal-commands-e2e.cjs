// Production Twin composer and Personal Go host; loopback model and temporary data.
// Covers skill identity dispatch, domain isolation, keyboard/IME, attachments,
// replies, literal slash text, settings, discovery failure, and draft restoration.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawn, execFileSync } = require('node:child_process');
const readline = require('node:readline');
const { app, BrowserWindow, ipcMain } = require('electron');
const output = path.resolve(__dirname, '../out/personal-commands-e2e');
fs.mkdirSync(output, {recursive:true});
const scratch = fs.mkdtempSync(path.join(output,'run-'));
app.setPath('userData',path.join(scratch,'electron'));
const report = {scope:'Production Electron composer + Personal app-server, synthetic loopback provider; no live account or paid inference',cases:[],requests:[]};
let win, core, provider, personal, catalogFault=false, serial=0;
const waiting=new Map();
const modelRequests=[];
function rpc(method,params={}) {
 const id=++serial;
 return new Promise((resolve,reject)=>{waiting.set(id,{resolve,reject});core.stdin.write(JSON.stringify({id,method,params})+'\n');});
}
const COMPOSER_INPUT='[data-wuu-component="composer-input"]';
const evaluate=fn=>win.webContents.executeJavaScript(`(()=>{const COMPOSER_INPUT=${JSON.stringify(COMPOSER_INPUT)};const composerText=node=>node.tagName==='TEXTAREA'?node.value:node.textContent;return (${fn})()})()`,true);
async function until(fn) {
 const deadline=Date.now()+20000;
 while(Date.now()<deadline){if(await evaluate(fn))return;await new Promise(resolve=>setTimeout(resolve,30));}
 throw new Error(`Timed out: ${fn}`);
}
async function fill(value) {
 win.focus();win.webContents.focus();
 await win.webContents.executeJavaScript(`(()=>{const input=(document.querySelector('.mosaic-replies')||document.querySelector('.mosaic-main')).querySelector('${COMPOSER_INPUT}');input.focus();
 if(input.tagName==='TEXTAREA'){Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(input,${JSON.stringify(value)});input.dispatchEvent(new Event('input',{bubbles:true}));}
 else{document.execCommand('selectAll');for(const [index,segment] of ${JSON.stringify(value)}.split('\\n').entries()){if(index)document.execCommand('insertLineBreak');document.execCommand('insertText',false,segment);}}})()`,true);
 await evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
}
async function appendText(value) {
 win.focus();win.webContents.focus();
 await win.webContents.executeJavaScript(`(()=>{const input=(document.querySelector('.mosaic-replies')||document.querySelector('.mosaic-main')).querySelector('${COMPOSER_INPUT}');input.focus();const selection=getSelection();selection.selectAllChildren(input);selection.collapseToEnd();for(const [index,segment] of ${JSON.stringify(value)}.split('\\n').entries()){if(index)document.execCommand('insertLineBreak');document.execCommand('insertText',false,segment);}})()`,true);
 await evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
}
async function key(keyCode,modifiers=[]) {win.webContents.sendInputEvent({type:'keyDown',keyCode,modifiers});win.webContents.sendInputEvent({type:'keyUp',keyCode,modifiers});await evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));}
async function capture(name){await evaluate(()=>Promise.all((document.querySelector('.slash-command-menu')?.getAnimations({subtree:true})||[]).map(animation=>animation.finished.catch(()=>{}))));await evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));fs.writeFileSync(path.join(output,name+'.png'),(await win.webContents.capturePage()).toPNG());}
async function initialize(){
 provider=http.createServer(async(req,res)=>{
  let bytes='';for await(const chunk of req)bytes+=chunk;
  const body=JSON.parse(bytes);modelRequests.push(body);
  const message={role:'assistant',content:'Fixture completed.'};
  const chunk=(delta,finish_reason)=>({id:'fixture',object:'chat.completion.chunk',model:body.model,choices:[{index:0,delta,finish_reason}]});
  if(body.stream){res.writeHead(200,{'Content-Type':'text/event-stream'});res.end(`data: ${JSON.stringify(chunk(message,null))}\n\ndata: ${JSON.stringify(chunk({},'stop'))}\n\ndata: [DONE]\n\n`);}
  else {res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({id:'fixture',model:body.model,choices:[{index:0,message,finish_reason:'stop'}]}));}
 });
 await new Promise(resolve=>provider.listen(0,'127.0.0.1',resolve));
 const state=path.join(scratch,'state'), files=path.join(state,'user-spaces/local/personal/files');
 const skill=path.join(files,'.agents/skills/fixture-note');fs.mkdirSync(skill,{recursive:true});
 fs.writeFileSync(path.join(skill,'SKILL.md'),'---\nname: fixture-note\ndescription: 合成笔记技能\nuser-invocable: true\n---\nPERSONAL_SKILL_DISPATCH_SENTINEL\nArguments: $ARGUMENTS\n');
 const codingSkill=path.join(state,'skills/coding-secret');fs.mkdirSync(codingSkill,{recursive:true});fs.writeFileSync(path.join(codingSkill,'SKILL.md'),'---\nname: coding-secret\ndescription: Must stay outside Personal\n---\nCODING_PRIVATE_SENTINEL');
 const config=path.join(scratch,'config.json');
 fs.writeFileSync(config,JSON.stringify({composition:{version:5,profile:'personal'},default_provider:'fixture',providers:{fixture:{type:'openai-compatible',base_url:`http://127.0.0.1:${provider.address().port}`,api_key:'synthetic-unused',model:'fixture',models:{fixture:{context_window:131072}}}},agent:{max_steps:2},skills:{enabled:true}}));
 core=spawn(process.env.WUU_TEST_CORE||path.join(__dirname,'../build/bin/wuu-core'),['app-server','--desktop-owner-pipe','--user-space','local','--product-domain','personal','--composition-profile','personal','--workdir',files,'--config',config],{stdio:['pipe','pipe','pipe','pipe'],env:{...process.env,WUU_HOME:state,WUU_NODE_EXECUTABLE:execFileSync('which',['node'],{encoding:'utf8'}).trim()}});
 core.stderr.on('data',chunk=>fs.appendFileSync(path.join(output,'host.log'),chunk));
 core.on('exit',code=>{for(const pending of waiting.values())pending.reject(new Error(`host exited ${code}`));waiting.clear();});
 readline.createInterface({input:core.stdout}).on('line',line=>{let frame;try{frame=JSON.parse(line);}catch{return;}const pending=waiting.get(frame.id);if(!pending)return;waiting.delete(frame.id);frame.error?pending.reject(new Error(JSON.stringify(frame.error))):pending.resolve(frame.result);});
 core.stdio[3].end(JSON.stringify({version:1,user_space_id:'local',product_domain:'personal'}));
 const initialized=await rpc('initialize');personal=initialized.extension_inventory.find(item=>item.provenance?.plugin_id==='personal');assert(personal?.enabled);
 ipcMain.handle('fixture:scope',async(_event,method,params)=>{
  if(method==='personal-source')return personal;
  if(method==='catalog-fault'){catalogFault=params;return;}
  if(method==='initialize-scope')return initialized.extension_inventory;
  if(method==='plugin/client/request'){
   const {composition_profile,workspace_id,workspace_root,...wire}=params;
   if(wire.method==='agents.skills'&&catalogFault)throw new Error('Synthetic skill catalog outage');
   report.requests.push({method:wire.method,input:wire.method==='agents.send'?wire.input:undefined});
   const result=await rpc(method,wire);
   if(wire.method==='agents.skills')report.catalog=result;
   if(wire.method==='agents.send'){
    const end=Date.now()+15000;
    while(Date.now()<end){const status=await rpc(method,{...wire,request_id:`inspect-${++serial}`,method:'agents.open',input:{agent_id:wire.input.agent_id},user_input:undefined});if(['completed','failed','interrupted'].includes(status.result?.session?.state))break;await new Promise(resolve=>setTimeout(resolve,30));}
   }
   return result;
  }
  return rpc(method,params);
 });
}
async function open(query={}){
 await win.loadFile(path.resolve(__dirname,'../out/personal-fixture/index.html'),{query:{commands:'1',...query}});
 await until(()=>window.mosaicFixture?.application.getSnapshot().phase==='ready'&&document.querySelector('[data-wuu-component="plugin-navigation-item"][aria-label="twin"]'));
 await evaluate(()=>window.mosaicFixture.openPersonal());
 await until(()=>{const input=document.querySelector('.mosaic-main '+COMPOSER_INPUT);return input&&(input.tagName==='TEXTAREA'?input.disabled===false:input.isContentEditable);});
}
app.whenReady().then(async()=>{
 await initialize();
 win=new BrowserWindow({width:1100,height:850,show:true,webPreferences:{contextIsolation:true,preload:path.join(__dirname,'personal-scoped-e2e-preload.cjs')}});
 win.webContents.on('console-message',(_e,level,message)=>{if(level>=2)fs.appendFileSync(path.join(output,'renderer.log'),message+'\n');});
 await open();await fill('/');
 await until(()=>document.querySelector('[data-command-name="fixture-note"]'));
 assert.equal(await evaluate(()=>!!document.querySelector('[data-command-name="coding-secret"]')),false);
 await capture('wide-light-menu');
 await fill('/fixture');await until(()=>document.querySelectorAll('.slash-command-item').length===1);
 await evaluate(()=>{const input=document.querySelector('.mosaic-main '+COMPOSER_INPUT);input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:229,bubbles:true,isComposing:true}));});
 assert.equal(await evaluate(()=>composerText(document.querySelector('.mosaic-main '+COMPOSER_INPUT))),'/fixture');
 assert.equal(report.requests.filter(r=>r.method==='agents.send').length,0);
 await evaluate(()=>document.querySelector('.mosaic-main '+COMPOSER_INPUT).dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));
 await key('ArrowDown');await key('Tab');
 await until(()=>{const input=document.querySelector('.mosaic-main '+COMPOSER_INPUT);const token=input?.querySelector('[data-skill-token]')?.dataset.skillToken;return token?token.startsWith('/skill '):composerText(input).startsWith('/skill ');});
 const selected=await evaluate(()=>{const input=document.querySelector('.mosaic-main '+COMPOSER_INPUT);return input.querySelector('[data-skill-token]')?.dataset.skillToken??composerText(input);});
 assert.equal(JSON.parse(selected.split('\n')[0].slice(7)).name,'fixture-note');
 await appendText('中文验证');
 await evaluate(()=>{const input=document.querySelector('.mosaic-main input[type=file]');const transfer=new DataTransfer();transfer.items.add(new File(['attached fixture context'],'context.txt',{type:'text/plain'}));input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));});
 await until(()=>document.querySelector('.mosaic-main').textContent.includes('context.txt'));
 await until(()=>document.querySelector('.mosaic-main .composer-send-button:not(:disabled)'));await evaluate(()=>document.querySelector('.mosaic-main .composer-send-button').click());
 await until(()=>document.querySelector('.mosaic-main').textContent.includes('Fixture completed.'));
 const sent=modelRequests.find(body=>JSON.stringify(body.messages).includes('PERSONAL_SKILL_DISPATCH_SENTINEL'));
 assert(sent,'selected skill never reached model');assert(JSON.stringify(sent.messages).includes('中文验证'));assert(!JSON.stringify(sent.messages).includes('CODING_PRIVATE_SENTINEL'));
 report.cases.push('Owned skill identity resolves into real model input; Coding skill excluded; keyboard/IME and attached file retained');
 await evaluate(()=>document.querySelector('.mosaic-main [data-action="reply-message"]').click());await until(()=>document.querySelector('.mosaic-replies '+COMPOSER_INPUT));
 await fill('/fixture');await until(()=>document.querySelector('[data-command-name="fixture-note"]'));await key('Enter');await key('Enter');
 await until(()=>document.querySelector('.mosaic-replies').textContent.includes('Fixture completed.'));
 assert(report.requests.filter(r=>r.method==='agents.send').at(-1).input.relation?.reply_to);
 report.cases.push('Reply composer invokes the skill and preserves its reply relation');
 await evaluate(()=>document.querySelector('[data-action="close-replies"]').click());await until(()=>!document.querySelector('.mosaic-replies'));
 for(const value of ['/not-a-command hello','/tmp/example.txt','//comment','/settings\nordinary multiline','/handoff\nordinary multiline']){
  await fill(value);await key('Escape');await until(()=>document.querySelector('.mosaic-main .composer-send-button:not(:disabled)'));await evaluate(()=>document.querySelector('.mosaic-main .composer-send-button').click());
  await until(()=>composerText(document.querySelector('.mosaic-main '+COMPOSER_INPUT))==='');
  assert.equal(report.requests.filter(r=>r.method==='agents.send').at(-1).input.prompt,value);
 }
 report.cases.push('Unknown commands, paths, escaped slashes and multiline text remain literal');
 await fill('/fixture');await key('Escape');assert.equal(await evaluate(()=>!!document.querySelector('.slash-command-menu')),false);
 await fill('draft retained across reload');await new Promise(resolve=>{win.webContents.once('did-finish-load',resolve);win.reload();});await until(()=>{const input=document.querySelector('.mosaic-main '+COMPOSER_INPUT);return input&&composerText(input)==='draft retained across reload'&&(input.tagName==='TEXTAREA'?input.disabled===false:input.isContentEditable);});
 await fill('/settings');await until(()=>document.querySelector('[data-command-name="settings"]'));const sendsBefore=report.requests.filter(r=>r.method==='agents.send').length;await key('Enter');await until(()=>document.querySelector('[data-personal-preferences]'));
 assert.equal(report.requests.filter(r=>r.method==='agents.send').length,sendsBefore);report.cases.push('Settings opens its existing panel without a model message; draft survives reload');
 for(const [theme,width,font] of [['dark',620,'20'],['light',1100,'15']]){win.setSize(width,850);await open({theme,font});await fill('/');await until(()=>document.querySelector('[data-command-name="fixture-note"]'));const box=await evaluate(()=>{const r=document.querySelector('.slash-command-menu').getBoundingClientRect();return {left:r.left,right:r.right,width:innerWidth};});assert(box.left>=0&&box.right<=box.width,JSON.stringify(box));await capture(`${theme}-${width}-menu`);}
 catalogFault=true;await open();await fill('/fixture');await until(()=>document.querySelector('.slash-command-menu [role=alert]'));await capture('catalog-failure');await key('Enter');assert.equal(await evaluate(()=>composerText(document.querySelector('.mosaic-main '+COMPOSER_INPUT))),'/fixture');catalogFault=false;await evaluate(()=>document.querySelector('.slash-command-menu [role=alert] button').click());await until(()=>document.querySelector('[data-command-name="fixture-note"]'));
 report.cases.push('Catalog failure preserves draft and supports retry; wide/light and narrow/dark/large text menu fits viewport');
 report.status='passed';
}).catch(error=>{report.status='failed';report.error=error.stack;console.error(error);}).finally(async()=>{
 if(win&&!win.isDestroyed()&&report.status==='failed'){report.dom=await evaluate(()=>({active:document.activeElement?.tagName,inputs:[...document.querySelectorAll('textarea,[data-wuu-component=composer-input]')].map(n=>n.outerHTML),menus:[...document.querySelectorAll('[role=listbox]')].map(n=>n.outerHTML)})).catch(()=>null);await capture('failure').catch(()=>{});}
 fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2));
 fs.writeFileSync(path.join(output,'model-requests.json'),JSON.stringify(modelRequests,null,2));
 console.log(JSON.stringify({status:report.status,cases:report.cases,error:report.error,report:path.join(output,'report.json')}));core?.kill();provider?.close();app.exit(report.status==='passed'?0:1);
});
