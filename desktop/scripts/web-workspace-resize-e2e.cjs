// Run the shipping app in the isolated wuu-e2e Xvfb runner, never the host desktop.
// WUU_RESIZE_REPEATS=3; WUU_RESIZE_OUTPUT=/evidence/<label> keeps comparable runs.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawn, spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { performance } = require('node:perf_hooks');
const { app, ipcMain, WebContentsView, contentTracing, screen, webContents } = require('electron');
assert.equal(process.platform, 'linux', 'Automated acceptance requires the isolated Linux display');
assert.ok(process.env.DISPLAY, 'Xvfb display required');
// This VM has no GPU passthrough. Keep both variants on the same software path.
app.disableHardwareAcceleration();
const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_RESIZE_OUTPUT || path.join(desktop, 'out/e2e/web-workspace-resize'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-resize-'));
const home = path.join(fixture, 'data'), project = path.join(fixture, 'project');
for (const dir of [output, home, project]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_ENABLE_BROWSER = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
fs.writeFileSync(path.join(project, 'README.md'), '# Resize fixture\n');
fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: 'http://127.0.0.1:1/v1', api_key: 'fixture', model: 'fixture' } }, engines: Object.fromEntries(['codex','claude','cursor','devin','dsh','grok','hermes','pi','opencode','antigravity'].map(id => [id, { enabled: false }])) }));
fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'resize-project', name: 'Resize fixture', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'resize-project', cwd: project } }));
fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light', message_flow_font_size: 15 }));
const html = kind => `<!doctype html><meta charset="utf-8"><title>Resize ${kind}</title><style>html{background:#dcecf5;color:#18394d;font:18px system-ui}body{margin:0}header{padding:20px;background:#c1dbe9;position:sticky;top:0}input,button{font:inherit;padding:8px}main{padding:24px;display:grid;grid-template-columns:repeat(auto-fit,minmax(min(240px,100%),1fr));gap:16px}article{background:#fff;padding:16px;border:1px solid #8cacbf}#motion{width:70px;height:25px;background:#ee8047;transform:translateX(0)}#edge{position:fixed;inset:0;border:4px solid #128368;pointer-events:none;box-sizing:border-box}</style><header><input id="draft" aria-label="Draft"><button id="click">Clicks: 0</button><div id="motion"></div></header><main>${Array.from({length:kind==='static'?1:160},(_,i)=>`<article><h2>Card ${i}</h2><p>Fixed local text reflows continuously as the viewport changes. No network assets.</p></article>`).join('')}</main><div id="edge"></div><script>window.clicks=0;document.querySelector('#click').onclick=()=>document.querySelector('#click').textContent='Clicks: '+(++window.clicks);window.ticks=0;${kind==='animated'?`function tick(t){window.ticks++;document.querySelector('#motion').style.transform='translateX('+Math.round((Math.sin(t/220)+1)*Math.max(0,innerWidth-140)/2)+'px)';requestAnimationFrame(tick)}requestAnimationFrame(tick);`:''}</script>`;
const report = { status:'running', versions:process.versions, platform:process.platform, architecture:process.arch, os:os.release(), cpus:os.cpus().map(c=>c.model), memory:os.totalmem(), buildMode:'electron-vite production build; shipping main/preload/renderer; Go with normal public composition', trajectory:'programmatic setBounds at fixed 16ms targets; horizontal, vertical, four anchored diagonal returns; 1440x900 to 440x560 (main window has no app minimum; zero-size content is outside the timed usable range); not a native mouse drag', repeats:Number(process.env.WUU_RESIZE_REPEATS || 3), cases:[], errors:[], artifacts:[], fixtureHashes:Object.fromEntries(['static','scroll','animated'].map(k=>[k,createHash('sha256').update(html(k)).digest('hex')])) };
const save=()=>fs.writeFileSync(path.join(output,'report.json'),JSON.stringify(report,null,2));
process.on('uncaughtException', error => { report.status = 'failed'; report.error = String(error.stack); save(); console.error(error); app.exit(1); });
let win, server, current;
const delay=ms=>new Promise(r=>setTimeout(r,ms));
const originalHandle=ipcMain.handle.bind(ipcMain);
ipcMain.handle=(channel,handler)=>originalHandle(channel,async(...args)=>{
  if (['wuu:turn-start','wuu:turn-queue','wuu:turn-steer'].includes(channel)) throw new Error('Resize fixture forbids inference');
  const start=performance.now();
  try{return await handler(...args);} finally {if(current){current.ipc[channel]=(current.ipc[channel]||0)+1;if(channel==='wuu:web-workspace-bounds')current.boundsIPC.push({at:start,bounds:args[2],duration:performance.now()-start});}}
});
for (const method of ['setBounds','setVisible']) {
  const original=WebContentsView.prototype[method];
  WebContentsView.prototype[method]=function(value){const start=performance.now();const result=original.call(this,value);if(current)current.viewCalls.push({at:start,id:this.webContents.id,method,value,duration:performance.now()-start});return result;};
}
const reactProbe=`window.__resizeCommits=0;window.__REACT_DEVTOOLS_GLOBAL_HOOK__={supportsFiber:true,inject:()=>{window.__resizeHookInjected=true;return 1},onCommitFiberRoot:()=>{window.__resizeCommits++},onCommitFiberUnmount:()=>{},onPostCommitFiberRoot:()=>{}};`;
app.on('browser-window-created',(_e,w)=>{
  if(win)return;win=w;
  w.webContents.debugger.attach('1.3');
  w.webContents.debugger.sendCommand('Page.addScriptToEvaluateOnNewDocument',{source:reactProbe}).catch(e=>report.errors.push(String(e)));
  w.on('resize',()=>{if(current)current.nativeEvents.push({at:performance.now(),bounds:w.getContentBounds()});});
  w.webContents.on('render-process-gone',(_e,d)=>report.errors.push(d));
});
async function evaluate(fn,arg,wc=win.webContents){return wc.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);}
async function until(fn,label,timeout=20000){const end=Date.now()+timeout;while(Date.now()<end){if(await fn())return;await delay(40);}throw new Error('Timeout: '+label);}
async function settle(){await evaluate(async()=>{await document.fonts.ready;await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));});await delay(250);}
async function click(selector){const p=await evaluate(s=>{const e=document.querySelector(s);if(!e)throw new Error(s);const r=e.getBoundingClientRect();return{x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)};},selector);win.webContents.sendInputEvent({type:'mouseMove',...p});win.webContents.sendInputEvent({type:'mouseDown',button:'left',clickCount:1,...p});win.webContents.sendInputEvent({type:'mouseUp',button:'left',clickCount:1,...p});await settle();if(await evaluate(()=>document.querySelector('.sidebar-ribbon')?.dataset.expanded==='true')){win.webContents.sendInputEvent({type:'mouseMove',x:300,y:90});await settle();}}
async function nativeInput(args){await new Promise((resolve,reject)=>{const child=spawn('xdotool',args);child.once('error',reject);child.once('exit',code=>code===0?resolve():reject(new Error('xdotool '+code)));});}
async function capture(name){const b=win.getBounds();const result=spawnSync('ffmpeg',['-hide_banner','-loglevel','error','-f','x11grab','-video_size',`${b.width}x${b.height}`,'-i',`${process.env.DISPLAY}+${b.x},${b.y}`,'-frames:v','1','-y',path.join(output,name+'.png')],{timeout:15000});assert.equal(result.status,0,String(result.stderr));report.artifacts.push(name+'.png');}
const probe=()=>{
  window.__resizeProbe={frames:[],longTasks:[],resizes:[],commits:window.__resizeHookInjected===true?window.__resizeCommits:null};
  let last;const tick=t=>{const p=window.__resizeProbe;if(!p)return;if(last!==undefined)p.frames.push(t-last);last=t;requestAnimationFrame(tick)};requestAnimationFrame(tick);
  window.__resizeLongTask=new PerformanceObserver(list=>{window.__resizeProbe?.longTasks.push(...list.getEntries().map(e=>({start:e.startTime,duration:e.duration})))});window.__resizeLongTask.observe({type:'longtask'});
  window.__resizeListener=()=>window.__resizeProbe?.resizes.push({at:performance.now(),width:innerWidth,height:innerHeight});window.addEventListener('resize',window.__resizeListener);
};
const stopProbe=()=>{window.__resizeLongTask.disconnect();window.removeEventListener('resize',window.__resizeListener);const p=window.__resizeProbe;p.commits=p.commits===null?null:window.__resizeCommits-p.commits;window.__resizeProbe=null;return p;};
const quantiles=values=>{const v=values.filter(Number.isFinite).sort((a,b)=>a-b);return{count:v.length,p50:v[Math.floor(v.length*.5)]||0,p95:v[Math.floor(v.length*.95)]||0,p99:v[Math.floor(v.length*.99)]||0,max:v.at(-1)||0,over25:v.filter(x=>x>25).length,over50:v.filter(x=>x>50).length};};
async function metrics(wc){return Object.fromEntries((await wc.debugger.sendCommand('Performance.getMetrics')).metrics.map(m=>[m.name,m.value]));}
async function trajectory(){
  const [nativeMinW,nativeMinH]=win.getMinimumSize();const minW=Math.max(440,nativeMinW),minH=Math.max(560,nativeMinH);const points=[];const segments=[[1440,900,minW,900,0,0],[minW,900,1440,900,0,0],[1440,900,1440,minH,0,0],[1440,minH,1440,900,0,0]];
  for(const [ax,ay] of [[0,0],[1,0],[0,1],[1,1]]){segments.push([1440,900,minW,minH,ax,ay],[minW,minH,1440,900,ax,ay]);}
  for(const [w1,h1,w2,h2,ax,ay] of segments)for(let i=1;i<=24;i++){const f=i/24,w=Math.round(w1+(w2-w1)*f),h=Math.round(h1+(h2-h1)*f);points.push({x:30+(ax?1440-w:0),y:30+(ay?900-h:0),width:w,height:h});}
  const start=performance.now();for(let i=0;i<points.length;i++){const due=start+i*16;await delay(Math.max(0,due-performance.now()));const at=performance.now();win.setBounds(points[i],false);current.commands.push({at,due,bounds:points[i]});}current.motionEnd=performance.now();
}
async function setSidebars(left,right){await click('[data-wuu-component="conversations-section"]');await evaluate(({left,right})=>{const shell=document.querySelector('.app-shell');if(shell.classList.contains('sidebar-collapsed')===left)document.querySelector('.section-sidebar-toggle').click();if(shell.classList.contains('right-panel-open')!==right)document.querySelector('.shell-right-sidebar-toggle').click();},{left,right});await settle();}
async function geometry(view){const dom=await evaluate(()=>{const el=document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-host');const r=el?.getBoundingClientRect();return r?{x:Math.round(r.x),y:Math.round(r.y),width:Math.round(r.width),height:Math.round(r.height)}:null;});const native=view?.getBounds();if(view){assert.equal(view.getVisible(),true);for(const k of ['x','y','width','height'])assert.ok(Math.abs(native[k]-dom[k])<=1,`Final ${k} matches: ${JSON.stringify({native,dom})}`);}return{dom,native};}
async function interactions(view,label){const wc=view.webContents;await wc.executeJavaScript('scrollTo(0,0)');const p=await wc.executeJavaScript(`(()=>{const r=document.querySelector('#draft').getBoundingClientRect();return{x:r.x+20,y:r.y+r.height/2}})()`);const outer=win.getContentBounds(),inner=view.getBounds();await nativeInput(['mousemove',String(Math.round(outer.x+inner.x+p.x)),String(Math.round(outer.y+inner.y+p.y)),'click','1']);await wc.insertText(label);assert.equal(await wc.executeJavaScript('document.activeElement.id'),'draft');assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'),label);await nativeInput(['key','Tab','key','space']);await until(()=>wc.executeJavaScript('window.clicks>0'),'native click');await nativeInput(['mousemove',String(outer.x+inner.x+inner.width-40),String(outer.y+inner.y+inner.height-60),'click','--repeat','4','5']);await delay(150);return{focus:wc.isFocused(),draft:await wc.executeJavaScript('document.querySelector("#draft").value'),clicks:await wc.executeJavaScript('window.clicks'),scrollY:await wc.executeJavaScript('scrollY'),ticks:await wc.executeJavaScript('window.ticks')};}
async function run(){
  server=http.createServer((req,res)=>{const kind=req.url.slice(1);res.writeHead(200,{'Content-Type':'text/html','Cache-Control':'no-store'});res.end(html(kind));});await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin=`http://127.0.0.1:${server.address().port}`;
  await import(pathToFileURL(path.join(desktop,'out/main/index.js')).href);
  await until(()=>win&&!win.webContents.isLoadingMainFrame()&&win.webContents.getURL().startsWith('file:'),'shipping app');
  await until(()=>evaluate(()=>!!document.querySelector('.sidebar-ribbon')),'ribbon');
  // The first file navigation can outrun the async CDP registration. Reload
  // only in the separate profiler run so React sees the hook before importing.
  if (process.env.WUU_RESIZE_TRACE === '1' && !await evaluate(() => window.__resizeHookInjected === true)) {
    await win.webContents.debugger.sendCommand('Page.enable');
    await win.webContents.debugger.sendCommand('Page.addScriptToEvaluateOnNewDocument', { source: reactProbe });
    await new Promise(resolve => { win.webContents.once('did-finish-load', resolve); win.webContents.reload(); });
    await until(()=>evaluate(()=>!!document.querySelector('.sidebar-ribbon')),'reloaded profiler ribbon');
  }
  win.setBounds({x:30,y:30,width:1440,height:900});win.show();win.focus();
  await win.webContents.debugger.sendCommand('Performance.enable');
  report.reactProbe=await evaluate(()=>({injected:window.__resizeHookInjected===true,totalCommits:window.__resizeHookInjected===true?window.__resizeCommits:null}));report.display=screen.getPrimaryDisplay();report.gpu=app.getGPUFeatureStatus();report.minimum=win.getMinimumSize();
  report.buildHashes=Object.fromEntries(['out/main/index.js','out/preload/index.cjs',...fs.readdirSync(path.join(desktop,'out/renderer/assets')).filter(x=>x.endsWith('.js')&&x.startsWith('index-')).map(x=>'out/renderer/assets/'+x)].map(f=>[f,createHash('sha256').update(fs.readFileSync(path.join(desktop,f))).digest('hex')]));
  report.sourceHashes=Object.fromEntries(['src/renderer/useComposerHeight.ts','src/renderer/WebWorkspace.tsx','src/main/webWorkspaceHost.ts'].map(f=>[f,createHash('sha256').update(fs.readFileSync(path.join(desktop,f))).digest('hex')]));
  // Fixture setup only, before any measured motion. Use the active product's
  // native tab model, while retaining the older branch path for A/B runs.
  const fixtureTabs = await evaluate(async origin => {
    const api = window.wuu.webWorkspace, tabs = {};
    if (api.initializeTabs) {
      await api.initializeTabs([]);
      for (const kind of ['static', 'scroll', 'animated']) {
        const state = await api.createTab('ribbon-browser', origin + '/' + kind);
        tabs[kind] = { id: state.activeTabId, group: 'ribbon-browser' };
      }
    } else {
      const websites = ['static', 'scroll', 'animated'].map(kind => ({ id: 'website:resize-' + kind, title: 'Resize ' + kind, url: origin + '/' + kind }));
      localStorage.setItem('wuu.navigation-ribbon.v1', JSON.stringify({ websites, order: [] }));
      window.dispatchEvent(new Event('wuu-navigation-ribbon-change'));
      for (const kind of ['static', 'scroll', 'animated']) tabs[kind] = { id: 'website:resize-' + kind, group: 'website:resize-' + kind };
    }
    return tabs;
  }, origin);
  await until(() => evaluate(tabs => Object.values(tabs).every(tab => document.querySelector(`[data-ribbon-id="${tab.id}"]`)), fixtureTabs), 'resize fixture tabs');
  await settle();
  if (process.env.WUU_TOOLBAR_ACCEPTANCE === '1' || process.env.WUU_RIBBON_MOTION === '1') {
    await click(`.sidebar-ribbon-main button[data-ribbon-id="${fixtureTabs.animated.id}"]`);
    const views = parent => parent.children.flatMap(view => [view, ...views(view)]);
    let view;
    await until(() => { view = views(win.contentView).find(view => view.webContents?.getURL() === origin + '/animated' && view.getVisible()); return view && !view.webContents.isLoading(); }, 'animated toolbar fixture');
    await require('./web-workspace-toolbar-acceptance.cjs')({ win, view, output, report, evaluate, until, settle, click, nativeInput, capture, delay });
    report.status = 'passed'; save(); server.close(); app.quit(); setTimeout(() => app.exit(0), 1000).unref(); return;
  }
  for(let repeat=0;repeat<(process.env.WUU_RESIZE_ACCEPTANCE==='1'?0:report.repeats);repeat++)for(const [left,right] of [[false,false],[true,false],[false,true],[true,true]])for(const kind of ['none','static','scroll','animated']){
    if(process.env.WUU_RESIZE_KINDS&&!process.env.WUU_RESIZE_KINDS.split(',').includes(kind))continue;
    if(process.env.WUU_RESIZE_FOCUS==='1'&&(left||right))continue;
    const label=`${repeat}-${left?'left':'no-left'}-${right?'right':'no-right'}-${kind}`;
    win.setBounds({x:30,y:30,width:1440,height:900});await setSidebars(left,right);
    let view,wc;
    if(kind!=='none'){await click(`.sidebar-ribbon-main button[data-ribbon-id="${fixtureTabs[kind].id}"]`);await until(()=>{view=win.contentView.children.find(v=>v.webContents?.getURL()===origin+'/'+kind&&v.getVisible());return view&&!view.webContents.isLoading();},'visible '+kind);wc=view.webContents;if(!wc.debugger.isAttached()){wc.debugger.attach('1.3');await wc.debugger.sendCommand('Performance.enable');}await wc.executeJavaScript('document.querySelector("#draft").value="";window.clicks=0');}
    await settle();await geometry(view);
    const traced=repeat===0&&left===right&&process.env.WUU_RESIZE_TRACE==='1';
    if(traced)await contentTracing.startRecording({included_categories:['devtools.timeline','v8','blink.user_timing','cc','viz','toplevel','disabled-by-default-devtools.timeline','disabled-by-default-devtools.timeline.stack','disabled-by-default-v8.cpu_profiler'],record_mode:'record-until-full',trace_buffer_size_in_kb:131072});
    const before=await metrics(win.webContents),pageBefore=wc?await metrics(wc):null;
    await evaluate(probe);if(wc)await evaluate(probe,undefined,wc);
    current={label,repeat,left,right,kind,ipc:{},boundsIPC:[],nativeEvents:[],viewCalls:[],commands:[],cpu:[],start:performance.now(),hostPID:win.webContents.getOSProcessId(),pagePID:wc?.getOSProcessId()};
    const timer=setInterval(()=>current?.cpu.push({at:performance.now(),processes:app.getAppMetrics().map(p=>({pid:p.pid,type:p.type,cpu:p.cpu.percentCPUUsage,memory:p.memory}))}),250);
    await trajectory();await delay(200);clearInterval(timer);
    const sample=current;current=null;
    sample.host=await evaluate(stopProbe);if(wc)sample.page=await evaluate(stopProbe,undefined,wc);
    sample.host.framesSummary=quantiles(sample.host.frames);if(sample.page)sample.page.framesSummary=quantiles(sample.page.frames);
    sample.commandLateness=quantiles(sample.commands.map(c=>c.at-c.due));
    const after=await metrics(win.webContents),pageAfter=wc?await metrics(wc):null;sample.hostMetrics=Object.fromEntries(Object.keys(after).map(k=>[k,after[k]-before[k]]));if(wc)sample.pageMetrics=Object.fromEntries(Object.keys(pageAfter).map(k=>[k,pageAfter[k]-pageBefore[k]]));
    if(traced){await contentTracing.stopRecording(path.join(output,label+'.trace.json'));report.artifacts.push(label+'.trace.json');}
    report.cases.push(sample);save();
    await settle();sample.geometry=await geometry(view);
    if(wc){sample.interactions=await interactions(view,label);if(kind!=='static')assert.ok(sample.interactions.scrollY>0,'Native wheel scrolls');if(kind==='animated')assert.ok(sample.interactions.ticks>30,'Animation remains live');if(repeat===0&&left===right)await capture(label);}
    save();console.log('CASE',label,JSON.stringify({host:sample.host.framesSummary,page:sample.page?.framesSummary,layout:sample.hostMetrics.LayoutDuration,ipc:sample.boundsIPC.length,view:sample.viewCalls.length,commits:sample.host.commits}));
  }
  if(process.env.WUU_RESIZE_ACCEPTANCE==='1')await require('./web-workspace-resize-acceptance.cjs')({win,output,report,evaluate,until,settle,click,setSidebars,nativeInput,geometry,capture,delay,origin,fixtureTabs});
  if(process.env.WUU_RESIZE_FOCUS==='1')await setSidebars(true,true);
  await click('[data-wuu-component="conversations-section"]');await settle();report.recoveredSidebars=await evaluate(()=>({left:!document.querySelector('.app-shell').classList.contains('sidebar-collapsed'),right:document.querySelector('.app-shell').classList.contains('right-panel-open')}));assert.deepEqual(report.recoveredSidebars,{left:true,right:true});
  report.idle=[];for(let i=0;i<5;i++){await delay(500);report.idle.push(app.getAppMetrics().map(p=>({pid:p.pid,type:p.type,cpu:p.cpu.percentCPUUsage,memory:p.memory})));}
  report.status='passed';save();server.close();for(const wc of webContents.getAllWebContents())if(wc.debugger.isAttached())wc.debugger.detach();app.quit();setTimeout(()=>app.exit(0),1000).unref();
}
const watchdog=setTimeout(()=>{report.error='watchdog';save();app.exit(1);},process.env.WUU_TOOLBAR_ACCEPTANCE === '1' ? 90000 : 540000);watchdog.unref();
run().catch(async e=>{report.status='failed';report.error=String(e.stack||e);save();console.error(e);try{if(win)await capture('failure');}catch{}server?.close();for(const wc of webContents.getAllWebContents())if(wc.debugger.isAttached())wc.debugger.detach();process.exitCode=1;app.once('will-quit',()=>app.exit(1));app.quit();setTimeout(()=>app.exit(1),1000).unref();});
