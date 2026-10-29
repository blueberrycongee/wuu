// Render the actual panel with synthetic in-memory data. No product preload,
// account configuration, credentials, host execution or network access is used.
const { app, BrowserWindow, session } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const assert = require("node:assert/strict");
const profile = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-personal-review-ui-"));
const output = path.resolve(process.env.WUU_REVIEW_UI_OUTPUT || path.join(__dirname,"../out/personal-review-e2e"));
const target = path.join(__dirname,"../out/personal-review-fixture/index.html");
app.setPath("userData",profile);
app.setPath("sessionData",path.join(profile,"session"));
app.commandLine.appendSwitch("disable-background-networking");
fs.mkdirSync(output,{recursive:true});
const report={scope:"Actual Personal review component, synthetic in-memory service, isolated Electron profile, no production gate connection",cases:[],screenshots:[],errors:[]};
let window;
async function waitFor(selector){
  await window.webContents.executeJavaScript(`new Promise((resolve,reject)=>{const selector=${JSON.stringify(selector)};if(document.querySelector(selector))return resolve();const observer=new MutationObserver(()=>{if(document.querySelector(selector)){clearTimeout(timer);observer.disconnect();resolve()}});const timer=setTimeout(()=>{observer.disconnect();reject(new Error('missing '+selector))},5000);observer.observe(document,{subtree:true,childList:true,attributes:true})})`);
}
async function capture(name){
  await window.webContents.executeJavaScript('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');
  const geometry=await window.webContents.executeJavaScript(`({width:innerWidth,scroll:document.documentElement.scrollWidth,buttons:[...document.querySelectorAll('button')].map(node=>{const r=node.getBoundingClientRect();return {text:node.textContent,left:r.left,right:r.right,width:r.width,disabled:node.disabled}})})`);
  assert(geometry.scroll<=geometry.width+1,`${name}: page overflow`);
  for(const button of geometry.buttons)assert(button.left>=-1&&button.right<=geometry.width+1,`${name}: clipped action ${button.text}`);
  const screenshot=await window.webContents.capturePage();const file=`${name}.png`;fs.writeFileSync(path.join(output,file),screenshot.toPNG());report.screenshots.push({file,geometry});
}
app.whenReady().then(async()=>{
  session.defaultSession.webRequest.onBeforeRequest((details,callback)=>callback({cancel:!details.url.startsWith("file:")&&!details.url.startsWith("data:")&&!details.url.startsWith("devtools:")}));
  window=new BrowserWindow({width:1100,height:950,show:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
  window.webContents.on("console-message",event=>{if(event.level==="error")report.errors.push(event.message)});
  for(const [name,width,height,query] of [["light-wide",1100,950,{}],["dark-wide",1100,950,{theme:"dark"}],["dark-narrow-large-long",430,1100,{theme:"dark",font:"20",scenario:"long"}],["unavailable",800,900,{scenario:"unavailable"}],["empty",800,900,{scenario:"empty"}],["expired",800,950,{scenario:"expired"}]]){
    window.setSize(width,height);await window.loadFile(target,{query});await waitFor('[data-personal-action-review]');await window.webContents.executeJavaScript('new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');await capture(name);
    if(await window.webContents.executeJavaScript(`Boolean(document.querySelector('[data-review-choice]'))`)){
      await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice]').scrollIntoView({block:'center'})`);await capture(`${name}-actions`);
    }
  }
  window.setSize(1000,1050);await window.loadFile(target);await waitFor('[data-review-choice="approve_once"]');
  await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice="approve_once"]').click()`);await waitFor('[data-review-state="approved"]');
  assert.equal(await window.webContents.executeJavaScript(`document.querySelector('[data-review-state="approved"] [data-review-choice="approve_once"]')===null`),true);report.cases.push("Approve once displays saved approval without claiming execution");await capture("approved");
  await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice="revoke"]').click()`);await waitFor('[data-review-state="revoked"]');report.cases.push("Approval can be withdrawn before execution");
  await window.loadFile(target,{query:{scenario:"error"}});await waitFor('[data-review-choice="approve_once"]');await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice="approve_once"]').click()`);await waitFor('[role="alert"]');
  assert(await window.webContents.executeJavaScript(`document.querySelector('[data-review-state="awaiting_confirmation"]')!==null`));report.cases.push("Save failure does not optimistically approve");await capture("save-failure");
  window.setSize(430,1000);await window.loadFile(target,{query:{theme:"dark",font:"20",scenario:"long"}});await waitFor('[data-review-choice="approve_once"]');
  await window.webContents.executeJavaScript(`const details=document.querySelector('.personal-review-parameters');details.open=true;details.scrollIntoView({block:'start'});details.querySelector('summary').focus()`);
  window.webContents.focus();window.webContents.sendInputEvent({type:"keyDown",keyCode:"Tab"});window.webContents.sendInputEvent({type:"keyUp",keyCode:"Tab"});
  await window.webContents.executeJavaScript('new Promise(resolve=>requestAnimationFrame(resolve))');
  const focused=await window.webContents.executeJavaScript(`({choice:document.activeElement.dataset.reviewChoice,visible:document.activeElement.matches(':focus-visible'),modality:document.documentElement.dataset.focusModality})`);assert.equal(focused.choice,"approve_once");assert.equal(focused.visible,true);assert.equal(focused.modality,"keyboard");
  await capture("narrow-parameters-focus");report.cases.push("Long parameters and targets remain readable with keyboard focus at 20px");
  window.setSize(850,1000);await window.loadFile(target,{query:{scenario:"numbers"}});await waitFor('[data-review-choice="approve_once"]');
  await window.webContents.executeJavaScript(`const details=document.querySelector('.personal-review-parameters');details.open=true;details.scrollIntoView({block:'center'})`);
  assert.equal(await window.webContents.executeJavaScript(`document.querySelector('.personal-review-parameters pre').textContent`),'{"fraction":0.123456789012345678901,"integer":9007199254740993,"overflow":1e400}');
  await capture("lossless-numbers");report.cases.push("Canonical parameters preserve large integers, fractions and exponent literals without numeric re-encoding");
  await window.loadFile(target,{query:{scenario:"unknown-schema"}});await waitFor('[role="alert"]');
  assert.equal(await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice]')`),null);
  await capture("unknown-schema");report.cases.push("Unknown owner snapshot schema exposes no approval controls");
  await window.loadFile(target,{query:{scenario:"blocked"}});await waitFor('[data-review-state="blocked"] [data-review-choice="revoke"]');
  await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice="revoke"]').scrollIntoView({block:'center'})`);await capture("blocked-withdrawal");
  await window.webContents.executeJavaScript(`document.querySelector('[data-review-choice="revoke"]').click()`);await waitFor('[data-review-state="revoked"]');
  report.cases.push("Owner can withdraw a blocked action before review recovers");
  assert.deepEqual(report.errors,[]);report.status="passed";
}).catch(error=>{report.status="failed";report.error=error.stack;process.exitCode=1}).finally(()=>{
  fs.writeFileSync(path.join(output,"report.json"),JSON.stringify(report,null,2));window?.destroy();app.quit();
});
app.on("will-quit",()=>{fs.rmSync(profile,{recursive:true,force:true})});
