// Actual App settings navigation and shipped Personal entry; synthetic transport.
const {app,BrowserWindow}=require('electron');
const fs=require('node:fs'), path=require('node:path'), assert=require('node:assert/strict');
const output=path.resolve(process.env.WUU_PERSONAL_SETTINGS_OUTPUT || path.join(__dirname,'../out/personal-settings-e2e'));fs.mkdirSync(output,{recursive:true});
app.setPath('userData',fs.mkdtempSync(path.join(output,'profile-')));
let win;
const watchdog=setTimeout(()=>{fs.writeFileSync(path.join(output,"timeout.txt"),"Fixture exceeded 45 seconds");app.exit(2);},45000);
app.whenReady().then(async()=>{
  win=new BrowserWindow({width:1280,height:1000,show:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
  win.webContents.session.webRequest.onBeforeRequest((details,callback)=>callback({cancel:!details.url.startsWith('file:')&&!details.url.startsWith('data:')}));
  const evaluate=fn=>win.webContents.executeJavaScript(`(${fn})()`,true);
  const until=async fn=>{const end=Date.now()+10000;while(Date.now()<end){if(await win.webContents.executeJavaScript(`Boolean((${fn})())`,true))return;await new Promise(r=>setTimeout(r,30));}throw new Error(`Timed out: ${fn}`);};
  const capture=async name=>{await new Promise(r=>setTimeout(r,300));await evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));fs.writeFileSync(path.join(output,name+'.png'),(await win.webContents.capturePage()).toPNG());};
  await win.loadFile(path.resolve(__dirname,'../out/personal-fixture/index.html'));
  await until(()=>document.querySelector('[data-settings-entry="settings"]'));
  await evaluate(()=>document.querySelector('[data-settings-entry="settings"]').click());
  await until(()=>document.querySelector('[data-wuu-page="plugin-view:personal:personal-preferences"]'));
  await evaluate(()=>document.querySelector('[data-wuu-page="plugin-view:personal:personal-preferences"]').click());
  await until(()=>document.querySelector('.personal-review')?.textContent.includes('动作审批当前不可用'));
  assert.equal(await evaluate(()=>Boolean(document.querySelector('[data-review-choice]'))),false);
  await evaluate(()=>document.querySelector('[data-personal-preferences]').scrollIntoView({block:'start'}));
  await capture('settings-light-wide');
  await until(()=>document.querySelector('[data-personal-preferences] select')?.options.length === 2);
  await evaluate(()=>{const select=document.querySelector('[data-personal-preferences] select');select.value=Array.from(select.options).find(option=>option.textContent.includes('model-two')).value;select.dispatchEvent(new Event('change',{bubbles:true}));});
  await until(()=>document.querySelector('.mosaic-model-select')?.disabled === false && window.__modelFixture.selection.model === 'model-two');
  assert.deepEqual(await evaluate(()=>window.__modelFixture.calls),[{provider:'synthetic-two',model:'model-two'}]);
  await capture('settings-model-selected');
  assert.equal(await evaluate(()=>Boolean(document.querySelector('[data-personal-connections], [data-personal-connectors]'))),false);
  await evaluate(()=>document.querySelector('.personal-review').scrollIntoView({block:'center'}));await capture('settings-review-light');
  await evaluate(()=>{Array.from(document.querySelectorAll('.mosaic-fixture-label button')).find(el=>el.textContent==='明 / 暗').click();document.documentElement.style.setProperty('--conversation-message-font-size','20px');});
  win.setContentSize(680,900);
  await evaluate(()=>document.querySelector('[data-personal-preferences]').scrollIntoView({block:'start'}));
  await capture('settings-model-dark-large-narrow');
  await evaluate(()=>document.querySelector('.personal-review').scrollIntoView({block:'center'}));
  await capture('settings-dark-large-narrow');
  const geometry=await evaluate(()=>{const el=document.querySelector('[data-personal-preferences]');return{width:el.clientWidth,scrollWidth:el.scrollWidth,text:el.textContent,reviewButtons:el.querySelectorAll('[data-review-choice]').length};});
  assert.ok(geometry.scrollWidth<=geometry.width+1,JSON.stringify(geometry));
  fs.writeFileSync(path.join(output,'report.json'),JSON.stringify({status:'passed',scope:'Actual shipped settings UI with synthetic transport',route:'Sidebar gear → Settings → Plugins → twin',cases:['model selection automatically sends provider and model','review unavailable with no approval buttons','light wide','dark 20px narrow'],geometry},null,2));
  clearTimeout(watchdog);app.exit(0);
}).catch(async error=>{
  fs.writeFileSync(path.join(output,'error.txt'),String(error.stack||error));
  if(win&&!win.isDestroyed())try{fs.writeFileSync(path.join(output,'failure.png'),(await win.webContents.capturePage()).toPNG());}catch(captureError){fs.writeFileSync(path.join(output,'capture-error.txt'),String(captureError));}
  app.exit(1);
});
