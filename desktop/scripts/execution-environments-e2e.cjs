// Renderer/bridge acceptance with a synthetic backend; Go integration tests own
// real configuration persistence and program execution.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktopRoot = path.resolve(__dirname, "..");
const evidence = path.join(desktopRoot, "out", "e2e", "execution-environments");
fs.mkdirSync(evidence, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(evidence, "profile-")));
process.env.WUU_RESIZE_E2E_CWD = path.resolve(desktopRoot, "..");
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true);
async function waitFor(win, fn) {
  const end = Date.now() + 15000;
  while (Date.now() < end) {
    if (await evaluate(win, fn)) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out: ${fn}`);
}
async function settle(win) {
  await evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(requestAnimationFrame);
    await Promise.all(document.getAnimations().filter(a => a.playState === "running" && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
  });
}
// The language switch stays on General.
async function openSettingsPage(win, label, ready) {
  await win.webContents.executeJavaScript(`[...document.querySelectorAll('.settings-nav-item')].find(button => ${label}.test(button.textContent.trim())).click()`);
  const end = Date.now() + 15000;
  while (!(await win.webContents.executeJavaScript(`Boolean(document.querySelector(${JSON.stringify(ready)}))`))) {
    if (Date.now() > end) throw new Error(`Timed out opening ${label}`);
    await new Promise(resolve => setTimeout(resolve, 40));
  }
}
async function openRuntime(win) {
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-trigger")));
  await evaluate(win, () => document.querySelector(".sidebar-account-trigger").click());
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await evaluate(win, () => document.querySelector('[data-settings-page="providers"]').click());
  await waitFor(win, () => Boolean(document.querySelector('.settings-nav-item')));
  await openSettingsPage(win, "/^(Built-in agent|内置 Agent)$/", '[data-testid="settings-execution-environments"]');
}
app.whenReady().then(async () => {
 const win = new BrowserWindow({width:1180,height:860,show:false,webPreferences:{contextIsolation:true,nodeIntegration:false,sandbox:false,backgroundThrottling:false,preload:path.join(__dirname,"resize-e2e-preload.cjs")}});
 await win.loadFile(path.join(desktopRoot,"out","renderer","index.html"));
 await openRuntime(win);
 await openSettingsPage(win,"/^(General|常规)$/",'[data-testid="settings-general"]');
 await evaluate(win,()=>[...document.querySelectorAll('[data-testid="settings-general"] button')].find(b=>b.textContent.trim()==="English").click());
 await waitFor(win,()=>document.documentElement.lang==="en-US");
 await openSettingsPage(win,"/^(Built-in agent|内置 Agent)$/",'[data-testid="settings-execution-environments"]');
 await evaluate(win,()=>[...document.querySelectorAll('[data-testid="settings-execution-environments"] button')].find(b=>b.textContent.trim()==="Add environment").click());
 await waitFor(win,()=>Boolean(document.querySelector('[aria-label="Name"]')));
 async function fill(label,value) {
  await win.webContents.executeJavaScript(`(() => { const input=document.querySelector('[data-testid="settings-execution-environments"] input[aria-label="${label}"]'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,${JSON.stringify(value)}); input.dispatchEvent(new Event('input',{bubbles:true})); })()`);
 }
 await fill("Name","isolated-tools");
 await fill("Image","wuu-execution:local");
 await fill("CPU cores","2");
 await fill("Memory limit","2048");
 const samples=[];
 for(const theme of ["light","dark"]) for(const font of [14,20]) for(const width of [1180,640]) {
  win.setContentSize(width,860);
  await win.webContents.executeJavaScript(`document.documentElement.dataset.theme='${theme}';document.documentElement.style.setProperty('--font-ui','${font}px')`);
  await evaluate(win,()=>document.querySelector('[aria-label="Name"]').scrollIntoView({block:'center'}));
  await settle(win);
  const geometry=await evaluate(win,()=>[...document.querySelectorAll('[data-testid="settings-execution-environments"] input, [data-testid="settings-execution-environments"] button')].map(el=>{const r=el.getBoundingClientRect();return {label:el.getAttribute('aria-label')||el.textContent,x:r.x,right:r.right,width:r.width,y:r.y,bottom:r.bottom};}).filter(r=>r.y>=0&&r.bottom<=innerHeight));
  assert.ok(geometry.every(r=>r.x>=0&&r.right<=width&&r.width>0),JSON.stringify({theme,font,width,geometry}));
  fs.writeFileSync(path.join(evidence,`${theme}-${font}-${width}.png`),(await win.webContents.capturePage()).toPNG());
  samples.push({theme,font,width,geometry});
 }
 await evaluate(win,()=>[...document.querySelectorAll('[data-testid="settings-execution-environments"] button')].find(b=>b.textContent.trim()==='Save').click());
 await waitFor(win,()=>!document.querySelector('[data-testid="settings-execution-environments"] input[aria-label="Name"]'));
 // The default environment is chosen with the other new-conversation defaults on General.
 await openSettingsPage(win,"/^(General|常规)$/",'[aria-label="Default for new conversations"]');
 await evaluate(win,()=>document.querySelector('[aria-label="Default for new conversations"]').click());
 await waitFor(win,()=>Boolean(document.querySelector('[role="menuitemradio"][data-value="isolated-tools"]')));
 await evaluate(win,()=>document.querySelector('[role="menuitemradio"][data-value="isolated-tools"]').click());
 await waitFor(win,()=>document.querySelector('[aria-label="Default for new conversations"]').textContent.includes('isolated-tools'));
 const settings=await evaluate(win,async()=>(await window.wuu.initialize()).general_settings.execution_environments);
 assert.equal(settings.default,'isolated-tools');assert.equal(settings.profiles['isolated-tools'].memory_mb,2048);
 await evaluate(win,()=>document.querySelector('[aria-label="Default for new conversations"]').focus());
 win.webContents.sendInputEvent({type:'keyDown',keyCode:'Space'});win.webContents.sendInputEvent({type:'keyUp',keyCode:'Space'});
 await waitFor(win,()=>Boolean(document.querySelector('[role="menu"]')));
 await settle(win);
 fs.writeFileSync(path.join(evidence,'keyboard-menu.png'),(await win.webContents.capturePage()).toPNG());
 win.webContents.sendInputEvent({type:'keyDown',keyCode:'Escape'});win.webContents.sendInputEvent({type:'keyUp',keyCode:'Escape'});
 await openSettingsPage(win,"/^(Built-in agent|内置 Agent)$/",'[data-testid="settings-execution-environments"]');
 await evaluate(win,()=>document.querySelector('[data-testid="settings-execution-environments"] button[aria-label="Remove isolated-tools"]').click());
 await openSettingsPage(win,"/^(General|常规)$/",'[aria-label="Default for new conversations"]');
 await waitFor(win,()=>document.querySelector('[aria-label="Default for new conversations"]').textContent.includes('Local'));
 fs.writeFileSync(path.join(evidence,'receipt.json'),JSON.stringify({settings,samples,verified:['create profile','save numeric limits','select default','keyboard menu','delete active profile','light and dark','14px and 20px','wide and narrow']},null,2));
 console.log('Execution environment settings acceptance passed');win.destroy();app.quit();
}).catch(error=>{console.error(error);app.exit(1)});
