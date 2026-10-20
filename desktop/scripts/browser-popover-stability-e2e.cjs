// Native boundary failure cases: resize loses text focus/selection; suggestion
// relayout steals address focus; repeated open/close reloads or hides the page.
// Full-shell toolbar interactions live in browser-daily-use-e2e.cjs.
const assert = require('node:assert/strict'), fs = require('node:fs'), os = require('node:os'), path = require('node:path');
const root = path.resolve(__dirname,'..');
const output = path.resolve(process.env.WUU_POPOVER_STABILITY_OUTPUT || path.join(root,'out/e2e/browser-popover-stability'));
const electron = process.versions.electron ? require('electron') : process.env.WUU_E2E_ELECTRON || require('electron');
if(typeof electron==='string'){
  fs.mkdirSync(output,{recursive:true});
  require('esbuild').buildSync({entryPoints:[path.join(root,'src/main/nativeBrowserPopover.ts')],outfile:path.join(output,'host.cjs'),bundle:true,platform:'node',external:['electron']});
  const fixture=fs.mkdtempSync(path.join(os.tmpdir(),'wuu-popover-'));
  try{const result=require('node:child_process').spawnSync(electron,[...(process.platform==='linux'?['--no-sandbox']:[]),__filename],{encoding:'utf8',env:{...process.env,WUU_POPOVER_FIXTURE:fixture}});fs.writeFileSync(path.join(output,'run.log'),result.stdout+result.stderr);process.stdout.write(result.stdout);process.stderr.write(result.stderr);assert.equal(result.status,0)}finally{fs.rmSync(fixture,{recursive:true,force:true})}
}else{
  const {app,BrowserWindow,WebContentsView,View,Menu}=electron;
  app.setPath('userData',process.env.WUU_POPOVER_FIXTURE);
  const timeout=setTimeout(()=>{console.error('Timed out');app.exit(1)},30000);
  (async()=>{
    await app.whenReady();Menu.setApplicationMenu(Menu.buildFromTemplate([{role:'editMenu'}]));
    const {NativeBrowserPopover}=require(path.join(output,'host.cjs'));
    const win=new BrowserWindow({width:1000,height:800,show:false,webPreferences:{sandbox:true}}),host=new NativeBrowserPopover(win);
    win.webContents.setWindowOpenHandler(details=>host.open(details)||{action:'deny'});
    await win.loadURL('data:text/html,<style>body{font:18px system-ui;padding:20px}</style><input id="address" value="example.test"><button id="history">History</button><button>Downloads</button>');
    const page=new WebContentsView({webPreferences:{sandbox:true}});win.contentView.addChildView(page);page.setBounds({x:0,y:160,width:1000,height:610});
    await page.webContents.loadURL('data:text/html,<style>body{background:%23e3efe8;font:28px system-ui;padding:40px}</style><h1>Website stays visible</h1><input placeholder="Page input"><script>window.identity=Math.random()</script>');
    const identity=await page.webContents.executeJavaScript('identity');win.show();win.focus();
    const wait=async fn=>{for(let i=0;i<120;i++){if(await fn())return;await new Promise(r=>setTimeout(r,25))}throw Error('Condition timed out: '+fn)};
    const run=(wc,code)=>wc.executeJavaScript(code,true);
    await run(win.webContents,`window.pop=window.open('about:blank','wuu-browser-popover');pop.document.write('<style>body{font:18px system-ui;background:white;padding:24px}input{font:inherit}</style><h2>History</h2><input value="Example"><button>Close</button>')`);
    const view=win.contentView.children.flatMap(child=>child.children).find(child=>child instanceof WebContentsView),pop=view.webContents;
    const key=(wc,keyCode,modifiers=[])=>{wc.sendInputEvent({type:'keyDown',keyCode,modifiers});wc.sendInputEvent({type:'keyUp',keyCode,modifiers})};
    const checks=[],diagnostics=[];
    for(let cycle=0;cycle<8;cycle++){
      const token='history-'+cycle,bounds={x:300,y:80,width:480,height:300,focus:true};
      host.report(token,bounds);await run(pop,`document.querySelector('input').focus();document.querySelector('input').select()`);await wait(()=>pop.isFocused());
      for(let i=0;i<16;i++)host.report(token,{...bounds,height:300+(i%2)});
      const focused=pop.isFocused(),selection=await run(pop,'document.activeElement.selectionEnd-document.activeElement.selectionStart');
      diagnostics.push({cycle,focused,selection});
      assert.equal(focused,true,'history relayout preserves native focus');assert.equal(selection,7,'history relayout preserves selection');
      host.report(token,null);assert.equal(win.webContents.isFocused(),true,'close restores shell focus');
      host.report('downloads-'+cycle,{...bounds,height:200});host.report('downloads-'+cycle,null);
      assert.equal(page.getVisible(),true);assert.equal(await run(page.webContents,'identity'),identity);
    }
    checks.push('8 history/download reopen cycles preserve page document and visibility','128 native bounds changes preserve input focus and selection');
    win.webContents.focus();await run(win.webContents,'document.querySelector("#address").focus();document.querySelector("#address").setSelectionRange(12,12)');
    const bounds={x:24,y:70,width:480,height:200,focus:false};
    host.report('address',bounds);for(let i=0;i<16;i++)host.report('address',{...bounds,height:200+(i%2)});
    assert.equal(win.webContents.isFocused(),true,'address suggestion relayout retains shell focus');
    assert.equal(win.contentView.children.find(child=>child.children.includes(view)).getVisible(),true,'address suggestions remain visible while shell keeps focus');
    await win.webContents.insertText('/typing');assert.equal(await run(win.webContents,'document.querySelector("#address").value'),'example.test/typing');
    checks.push('address suggestions preserve native typing focus');
    key(win.webContents,'A',[process.platform==='darwin'?'meta':'control']);await run(win.webContents,'new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');const shellSyntheticSelectAll=await run(win.webContents,'document.activeElement.selectionStart===0&&document.activeElement.selectionEnd===19');
    host.report('address',null);host.report('edit',{...bounds,focus:true});await run(pop,'document.querySelector("input").focus();document.querySelector("input").setSelectionRange(7,7)');
    key(pop,'A',[process.platform==='darwin'?'meta':'control']);
    await run(pop,'new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');const popupSyntheticSelectAll=await run(pop,'document.activeElement.selectionStart===0&&document.activeElement.selectionEnd===7');diagnostics.push({shellSyntheticSelectAll,popupSyntheticSelectAll});pop.selectAll();await wait(()=>run(pop,'document.activeElement.selectionStart===0&&document.activeElement.selectionEnd===7'));
    checks.push('native select-all command reaches popover input');
    const sibling=new View();sibling.setVisible(false);win.contentView.addChildView(sibling);
    host.report('edit',{...bounds,focus:true});
    assert.equal(win.contentView.children.at(-1).children.includes(view),true,'popover rises above newly attached sibling');
    assert.equal(pop.isFocused(),true,'necessary raise retains native focus');
    win.contentView.removeChildView(sibling);
    checks.push('new sibling attachment still restores popover stacking');
    fs.writeFileSync(path.join(output,'popover-content.png'),(await pop.capturePage()).toPNG());
    fs.writeFileSync(path.join(output,'website-content.png'),(await page.webContents.capturePage()).toPNG());
    fs.writeFileSync(path.join(output,'report.json'),JSON.stringify({status:'passed',electron:process.versions.electron,platform:process.platform,checks,diagnostics},null,2));
    console.log('PASS',checks);clearTimeout(timeout);page.webContents.close();win.destroy();app.exit(0);
  })().catch(error=>{console.error(error);fs.writeFileSync(path.join(output,'failure.txt'),String(error.stack));app.exit(1)});
}
