const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../artifacts/channel-continuity");
const baseURL = process.env.WUU_COORDINATOR_PREVIEW_URL || "http://127.0.0.1:5207";
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-state"));
async function waitFor(win, expression) {
  for (let i=0;i<100;i++){if(await win.webContents.executeJavaScript(expression)) return;await new Promise(resolve=>setTimeout(resolve,100));}
  throw new Error(`Timed out: ${expression}`);
}
async function click(win,label){await win.webContents.executeJavaScript(`(() => {const b=[...document.querySelectorAll('button')].find(b=>b.textContent.trim()===${JSON.stringify(label)} || b.getAttribute('aria-label')===${JSON.stringify(label)});if(!b)throw Error('Missing button');b.click();})()`);}
app.whenReady().then(async()=>{
 const win=new BrowserWindow({show:false,width:1100,height:760,webPreferences:{backgroundThrottling:false}});
 for(const theme of ["light","dark"])for(const width of [1100,390]){
  win.setSize(width,760);await win.loadURL(`${baseURL}/dev/room-coordinator/index.html?theme=${theme}`);
  await waitFor(win,`!!document.querySelector('.channel-continuity-launcher')`);
  await click(win,"安排与记忆");await waitFor(win,`document.querySelectorAll('.channel-continuity-item').length===2`);
  await new Promise(resolve=>setTimeout(resolve,300));
  const dimensions=await win.webContents.executeJavaScript(`(()=>{const d=document.querySelector('.channel-continuity-body');return {width:d.clientWidth,scroll:d.scrollWidth,body:document.body.scrollWidth,viewport:innerWidth}})()`);
  assert.ok(dimensions.scroll<=dimensions.width+1,JSON.stringify(dimensions));assert.ok(dimensions.body<=dimensions.viewport+1,JSON.stringify(dimensions));
  fs.writeFileSync(path.join(output,`${theme}-${width}-plans.png`),(await win.webContents.capturePage()).toPNG());
  await click(win,"暂停");await waitFor(win,`[...document.querySelectorAll('.channel-continuity-item')].every(e=>e.textContent.includes('已暂停'))`);
  await click(win,"记忆");await waitFor(win,`!!document.querySelector('.channel-continuity-topic')`);
  await win.webContents.executeJavaScript(`document.querySelector('.channel-continuity-topic').click()`);
  await waitFor(win,`!!document.querySelector('.channel-continuity-memory')`);
  fs.writeFileSync(path.join(output,`${theme}-${width}-memory.png`),(await win.webContents.capturePage()).toPNG());
  await click(win,"编辑");await waitFor(win,`!!document.querySelector('textarea[aria-label="记忆"]')`);
  await click(win,"保存");await waitFor(win,`!!document.querySelector('.channel-continuity-topic')`);
  await win.webContents.executeJavaScript(`document.querySelector('.channel-continuity-topic').click()`);await waitFor(win,`!!document.querySelector('.channel-continuity-memory')`);
  await click(win,"删除记忆");await waitFor(win,`document.querySelector('.channel-continuity-body')?.textContent.includes('还没有记忆')`);
 }
 console.log(`Verified plans, pause, memory editing and deletion in light/dark at 1100/390px: ${output}`);win.destroy();app.quit();
}).catch(error=>{console.error(error);app.exit(1)});
