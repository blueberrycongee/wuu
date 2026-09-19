const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const baseline = process.argv.includes("--baseline");
const precise = process.argv.includes("--precise");
const appearance = process.argv.includes("--appearance");
const correction = process.argv.includes("--correction");
const anchor = process.argv.includes("--anchor");
const preview = process.argv.includes("--preview");
const output = path.resolve(__dirname, "../../.tmp/collaboration-layout", `${baseline ? "before" : "after"}${preview ? '-preview' : anchor ? '-anchor' : correction ? '-correction' : precise ? '-precise' : appearance ? '-appearance' : ''}`);
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "profile"));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1500, height: 900, webPreferences: { backgroundThrottling: false } });
  if (correction || appearance || preview || precise) {
    // Keep fixture input independent of the user's active DEV window and mouse.
    win.webContents.debugger.attach('1.3');
    win.webContents.on('did-finish-load', () => {
      void win.webContents.debugger.sendCommand('Emulation.setFocusEmulationEnabled', { enabled: true });
    });
  }
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const js = expression => win.webContents.executeJavaScript(expression);
  async function waitFor(expression) {
    for (let i = 0; i < 150; i++) {
      if (await js(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error(`Timed out: ${expression}\n${errors.join("\n")}`);
  }
  const geometry = () => js(`(() => {
    const rect = s => { const n = document.querySelector(s); if (!n) return null; const r = n.getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height,right:r.right,bottom:r.bottom}; };
    const avatar = document.querySelector('.channel-response-status-avatar');
    const r = avatar?.getBoundingClientRect();
    const spinner=document.querySelector('.managed-agent-work-pill .managed-session-spinner');
    return {window: {w:innerWidth,h:innerHeight,dpr:devicePixelRatio}, chat:rect('.channel-room-main'), panel:rect('.managed-session-panel'),
      handle:rect('.channel-inspector-resizer'), activity:rect('.channel-activity-region'), avatar:rect('.channel-response-status-avatar'),
      launcher:rect('.managed-agent-work-pill'), composer:rect('.composer-frame'), footer:rect('.channel-conversation-footer'),
      hitAvatar: r ? !!document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)?.closest('.channel-activity-inspect') : false,
      animations:avatar?.getAnimations().map(a=>({state:a.playState,time:a.currentTime})),
      launcherText:document.querySelector('.managed-agent-work-pill')?.textContent,
      launcherAnimations:spinner?.getAnimations().map(a=>({state:a.playState,time:a.currentTime})),
      draft:document.querySelector('textarea')?.value,
      sameNodes:document.querySelector('textarea')===window.savedComposer && document.querySelector('[role="log"]')===window.savedStream,
      overflow:document.documentElement.scrollWidth>innerWidth};
  })()`);
  const settle = () => waitFor(`!document.querySelector('.channel-conversation').getAnimations().some(a=>a.playState==='running')`);
  const shot = async name => {
    await js(`document.fonts.ready.then(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))`);
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  };
  async function click(selector) {
    // Fonts and ResizeObserver-driven footer layout must settle before hit testing.
    await js(`document.fonts.ready.then(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))`);
    const {x,y} = await js(`(()=>{const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}})()`);
    win.webContents.sendInputEvent({type:'mouseDown',x,y,button:'left',clickCount:1});
    win.webContents.sendInputEvent({type:'mouseUp',x,y,button:'left',clickCount:1});
  }
  const checkChat = g => {
    assert(!g.overflow, 'No horizontal page overflow');
    assert(g.sameNodes && g.draft === '保留输入草稿 / keep this draft', 'Panel interactions must preserve chat and draft');
    assert(g.hitAvatar, 'The running avatar must remain visible and clickable');
    assert(g.animations?.some(a => a.state === 'running'), 'Running animation must not be hidden or stopped');
    assert(g.avatar.right <= g.launcher.x || g.launcher.right <= g.avatar.x || g.avatar.bottom <= g.launcher.y || g.launcher.bottom <= g.avatar.y, 'Launcher and animated avatar must not overlap');
    assert(g.avatar.bottom < g.composer.y && g.launcher.bottom <= g.composer.y, 'Status row must clear the input');
    assert(g.composer.bottom <= g.window.h && g.composer.x >= g.chat.x && g.composer.right <= g.chat.right, 'Input must stay inside the chat');
  };
  async function mouseDrag(x, y, delta) {
    win.webContents.sendInputEvent({type:'mouseMove',x,y});
    win.webContents.sendInputEvent({type:'mouseDown',x,y,button:'left',clickCount:1});
    if (!baseline) await waitFor(`document.querySelector('.channel-inspector-resizer')?.dataset.resizing === 'true'`);
    win.webContents.sendInputEvent({type:'mouseMove',x:x+delta,y,button:'left'});
    // Wait for Chromium to process the real input before releasing the pointer.
    await js(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
    win.webContents.sendInputEvent({type:'mouseUp',x:x+delta,y,button:'left',clickCount:1});
    if (!baseline) await waitFor(`!document.querySelector('.channel-inspector-resizer')?.dataset.resizing`);
    await settle();
  }
  async function drag(delta) {
    const g = await geometry();
    const x = Math.round(g.handle.x+3), y = 200;
    assert(await js(`!!document.elementFromPoint(${x},${y})?.closest('.channel-inspector-resizer')`), 'Resize handle must win hit testing');
    const bounds = await js(`(()=>{const s=document.querySelector('.channel-inspector-resizer');return {min:+s.getAttribute('aria-valuemin'),max:+s.getAttribute('aria-valuemax')}})()`);
    await mouseDrag(x,y,delta);
    const result = await geometry();
    assert.equal(result.panel.w, Math.max(bounds.min,Math.min(bounds.max,g.panel.w-delta)));
    assert(result.chat.w >= 400, 'Dragging must preserve chat clearance');
    checkChat(result);
    return result;
  }
  const report = [];
  if (preview) {
    const card = '.conversation-status-preview-card';
    async function moveTo(selector) {
      const p = await js(`(()=>{const n=document.querySelector(${JSON.stringify(selector)});n.scrollIntoView({block:'nearest'});const r=n.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`);
      const zoom = win.webContents.getZoomFactor();
      const point = {x:Math.round(p.x*zoom),y:Math.round(p.y*zoom)};
      win.webContents.sendInputEvent({type:'mouseMove',...point});
      return point;
    }
    for (const theme of ['light','dark']) for (const font of [14,18]) for (const width of [1500,480]) for (const zoom of [1,1.25]) for (const state of ['running','idle','empty']) {
      win.setContentSize(width,width===480?600:900);
      await win.loadURL(`http://127.0.0.1:5217/dev/channel-session-layout/index.html?theme=${theme}&font=${font}${state==='running'?'':`&${state}`}`);
      win.webContents.setZoomFactor(zoom);
      await waitFor(`!!document.querySelector('.managed-agent-work-pill') && Math.abs(innerWidth-${width/zoom})<2`);
      if (state === 'empty') await js(`window.layoutFixture.setEmpty(true)`);
      await moveTo('.managed-agent-work-pill');
      await waitFor(`!!document.querySelector('${card}')`);
      await moveTo(card);
      // Exercise the real pointer bridge beyond the exit grace period.
      await new Promise(resolve => setTimeout(resolve,250));
      const measured = await js(`(()=>{const n=document.querySelector('${card}'),r=n?.getBoundingClientRect(),c=document.querySelector('.composer-frame').getBoundingClientRect();return {open:!!n,left:r?.left,right:r?.right,top:r?.top,bottom:r?.bottom,inputTop:c.top,width:innerWidth,rows:[...document.querySelectorAll('.managed-session-preview-title')].map(x=>x.textContent),runningRows:document.querySelectorAll('.managed-session-preview-list .managed-session-spinner').length,nativeTitle:!!document.querySelector('.managed-agent-work-pill').closest('[title]')||!!n?.querySelector('[title]'),spinner:!!document.querySelector('.managed-agent-work-pill .managed-session-spinner'),overflow:document.documentElement.scrollWidth>innerWidth}})()`);
      assert(measured.open && measured.left>=0 && measured.right<=measured.width && measured.top>=0 && measured.bottom<=measured.inputTop && !measured.nativeTitle && !measured.spinner && !measured.overflow,JSON.stringify({theme,font,width,zoom,state,measured}));
      assert.equal(measured.rows.length,state==='empty'?0:5);
      assert.equal(measured.runningRows,state==='running'?3:0);
      if(state!=='empty') assert(measured.rows[0].startsWith(state==='running'?'会话 3：':'会话 65：'));
      await shot(`${theme}-${font}-${width}-${zoom}-${state}-preview`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Escape'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'Escape'});
      await waitFor(`!document.querySelector('${card}')`);
      win.webContents.sendInputEvent({type:'mouseMove',x:1,y:1});
      await js(`document.querySelector('.managed-agent-work-pill').focus()`);
      await waitFor(`!!document.querySelector('${card}')`);
      const p=await moveTo(state==='empty'?'.managed-agent-work-pill':'.managed-session-preview-all');
      win.webContents.sendInputEvent({type:'mouseDown',...p,button:'left',clickCount:1});
      win.webContents.sendInputEvent({type:'mouseUp',...p,button:'left',clickCount:1});
      await waitFor(`!!document.querySelector('.managed-session-panel') && !document.querySelector('${card}')`);
      report.push({theme,font,width,zoom,state,measured});
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({report,errors},null,2));
    assert.equal(errors.length,0);console.log('PASS: 48 hover/focus preview cases, pointer bridge, Escape, all-sessions action and viewport clearance');win.destroy();app.quit();return;
  }
  if(anchor) {
    for(const theme of ['light','dark'])for(const idle of [false,true])for(const width of [1500,480])for(const zoom of [1,1.25]) {
      win.setContentSize(width,900);win.webContents.setZoomFactor(zoom);
      await win.loadURL(`http://127.0.0.1:5217/dev/channel-session-layout/index.html?theme=${theme}${idle?'&idle':''}`);
      win.webContents.setZoomFactor(zoom);
      await waitFor(`!!document.querySelector('.managed-agent-work-pill')`);
      await waitFor(`Math.abs(innerWidth-${width/zoom})<2 && ![...document.querySelectorAll('.channel-activity-slot')].some(n=>n.getAnimations().some(a=>a.playState==='running'))`);
      if(width===480)await js(`document.querySelector('[aria-label="Toggle fixture sidebar"]').click()`);
      for(const multiline of [false,true])for(const history of [false,true]) {
        await js(`(()=>{const n=document.querySelector('textarea');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(n,${JSON.stringify(multiline ? 'line one\nline two\nline three\nline four' : 'line one')});n.dispatchEvent(new Event('input',{bubbles:true}));})()`);
        await js(`new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))`);
        await js(`(()=>{const n=document.querySelector('[role="log"]');n.scrollTop=${history?'0':'n.scrollHeight'};n.dispatchEvent(new Event('scroll'));})()`);
        await waitFor(`${history?'!!':'!'}document.querySelector('.jump-to-latest-cluster-inline > .jump-to-latest-pill:not(.managed-agent-work-pill)')`);
        const measured=await js(`(()=>{const g=document.querySelector('.jump-to-latest-cluster-inline'),buttons=[...g.querySelectorAll('button')].map(n=>n.getBoundingClientRect()),c=document.querySelector('.composer-frame').getBoundingClientRect(),a=document.querySelector('.channel-response-status-avatar')?.getBoundingClientRect(),activity=document.querySelector('.channel-activity-inspect')??document.querySelector('.channel-activity-region'),r=activity.getBoundingClientRect();return {gap:c.top-Math.max(...buttons.map(b=>b.bottom)),center:(buttons[0].left+buttons.at(-1).right-c.left-c.right)/2,centerlineError:Math.max(...buttons.map(b=>Math.abs((b.top+b.bottom-r.top-r.bottom)/2))),animatedCenterlineError:a?Math.max(...buttons.map(b=>Math.abs((b.top+b.bottom-a.top-a.bottom)/2))):null,overlap:!!a&&buttons.some(b=>a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top),overflow:document.documentElement.scrollWidth>innerWidth,composerBottom:c.bottom,viewport:innerHeight}})()`);
        assert(measured.gap>0 && measured.centerlineError<1 && (measured.animatedCenterlineError===null||measured.animatedCenterlineError<2) && Math.abs(measured.center)<2 && !measured.overlap && !measured.overflow && measured.composerBottom<=measured.viewport,JSON.stringify({theme,idle,width,zoom,multiline,history,measured}));
        report.push({theme,idle,width,zoom,multiline,history,measured});
        if(zoom===1.25&&multiline)await shot(`${theme}-${idle?'idle':'running'}-${width}-${history?'history':'bottom'}`);
      }
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({report,errors},null,2));assert.equal(errors.length,0);console.log(`PASS: ${report.length} actual input-anchor states`);win.destroy();app.quit();return;
  }
  if (correction) {
    const move = async selector => {
      const p=await js(`(()=>{const r=document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();return {x:Math.round(r.x+r.width/2),y:Math.round(Math.max(90,r.y+10))}})()`);
      win.webContents.sendInputEvent({type:'mouseMove',...p});
      try { await waitFor(`document.querySelector(${JSON.stringify(selector)}).matches(':hover')`); }
      catch(error) { console.error(await js(`JSON.stringify({point:${JSON.stringify(p)},hit:document.elementFromPoint(${p.x},${p.y})?.outerHTML,rect:document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect().toJSON()})`));throw error; }
      await js(`new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))`);
      await waitFor(`!document.querySelector(${JSON.stringify(selector)}).getAnimations({subtree:true}).some(a=>a.playState==='running')`);
      return p;
    };
    const divider = selector => js(`(()=>{const n=document.querySelector(${JSON.stringify(selector)}),s=getComputedStyle(n),p=getComputedStyle(n,'::before');return {hitWidth:s.width,lineWidth:p.width,background:p.backgroundColor,shadow:p.boxShadow,transform:p.transform}})()`);
    const search = () => js(`(()=>{const n=document.querySelector('.managed-session-search'),i=n.querySelector('input');return [n,i].map(x=>{const s=getComputedStyle(x);return {outline:s.outlineWidth,shadow:s.boxShadow,background:s.backgroundColor,focusVisible:x.matches(':focus-visible')}})})()`);
    for(const theme of ['light','dark'])for(const font of [14,18]) {
      win.setContentSize(1500,1000);
      await win.loadURL(`http://127.0.0.1:5217/dev/channel-session-layout/index.html?bubbles&theme=${theme}&font=${font}`);
      await waitFor(`!!document.querySelector('.channel-message.agent .composer-file-attachment')`);
      await js(`window.savedComposer=document.querySelector('textarea');window.savedStream=document.querySelector('[role="log"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(savedComposer,'保留输入草稿 / keep this draft');savedComposer.dispatchEvent(new Event('input',{bubbles:true}));savedStream.scrollTop=savedStream.scrollHeight;savedStream.dispatchEvent(new Event('scroll'))`);
      await waitFor(`document.querySelectorAll('.jump-to-latest-cluster-inline > .jump-to-latest-pill:not(.managed-agent-work-pill)').length===0`);
      await shot(`${theme}-${font}-centered-at-bottom`);
      checkChat(await geometry());
      assert(await js(`!document.querySelector('.managed-agent-work-pill .managed-session-spinner')`));
      await move('.managed-agent-work-pill');
      await waitFor(`!!document.querySelector('.conversation-status-preview-card')`);
      await shot(`${theme}-${font}-running-hover-detail`);
      await js(`document.querySelector('.managed-agent-work-pill').focus()`);
      await waitFor(`!!document.querySelector('.conversation-status-preview-card')`);
      await shot(`${theme}-${font}-running-focus-detail`);
      await js(`window.savedStream.scrollTop=0;window.savedStream.dispatchEvent(new Event('scroll'))`);
      await waitFor(`!!document.querySelector('.jump-to-latest-cluster-inline > .jump-to-latest-pill:not(.managed-agent-work-pill)')`);
      const group=await js(`(()=>{const n=document.querySelector('.jump-to-latest-cluster-inline'),r=n.getBoundingClientRect(),c=document.querySelector('.composer-frame').getBoundingClientRect();const buttons=[...n.querySelectorAll('button')].map(b=>b.getBoundingClientRect());return {centerDelta:(buttons[0].left+buttons.at(-1).right)/2-(c.left+c.right)/2,gap:buttons[1].left-buttons[0].right,bottom:r.bottom,activityTop:document.querySelector('.channel-activity-region').getBoundingClientRect().top}})()`);
      assert(Math.abs(group.centerDelta)<2 && group.gap>=0);
      await shot(`${theme}-${font}-centered-reading-history`);
      await click('.jump-to-latest-cluster-inline > .jump-to-latest-pill:not(.managed-agent-work-pill)');
      await waitFor(`!document.querySelector('.jump-to-latest-cluster-inline > .jump-to-latest-pill:not(.managed-agent-work-pill)')`);
      await js(`document.querySelectorAll('.channel-message.agent')[document.querySelectorAll('.channel-message.agent').length-1].scrollIntoView({block:'start'})`);
      await shot(`${theme}-${font}-long-agent-attachments`);
      const bubble=await js(`(()=>{const n=[...document.querySelectorAll('.channel-message.agent .channel-message-bubble')].at(-1),r=n.getBoundingClientRect(),s=getComputedStyle(n);return {background:s.backgroundColor,padding:s.padding,radius:s.borderRadius,paragraphs:n.querySelectorAll('p').length,clipped:n.scrollWidth>n.clientWidth,text:n.textContent,attachments:n.parentElement.querySelectorAll('.composer-attachments').length}})()`);
      assert(parseFloat(bubble.padding)>0 && parseFloat(bubble.radius)>0 && bubble.background!=='rgba(0, 0, 0, 0)' && !bubble.clipped);
      assert(bubble.paragraphs>2 && bubble.text.includes('最后一段') && bubble.attachments===1);
      // Rich-code actions and author mention are the existing hover controls.
      const code=await js(`(()=>{const n=[...document.querySelectorAll('.channel-message.agent .rich-code-block')].at(-1);n.scrollIntoView({block:'center'});return !!n.querySelector('button')})()`);
      assert(code);
      await move('.channel-message.agent:last-child .rich-code-block');
      await shot(`${theme}-${font}-code-hover`);
      await click('.managed-agent-work-pill');await waitFor(`!!document.querySelector('.managed-session-panel')`);await settle();
      await move('.sidebar-resizer');const reference=await divider('.sidebar-resizer');await shot(`${theme}-${font}-existing-divider-hover`);
      const p=await move('.channel-inspector-resizer');const target=await divider('.channel-inspector-resizer');
      assert.deepEqual(target,reference,'Same existing sidebar hit zone and hover line, not a new visual design');
      await shot(`${theme}-${font}-collaboration-divider-hover`);
      win.webContents.sendInputEvent({type:'mouseDown',...p,button:'left',clickCount:1});
      await waitFor(`!!document.querySelector('.channel-inspector-resizer').dataset.resizing`);
      win.webContents.sendInputEvent({type:'mouseMove',x:p.x-60,y:p.y,button:'left'});
      await shot(`${theme}-${font}-divider-drag`);
      win.webContents.sendInputEvent({type:'mouseUp',x:p.x-60,y:p.y,button:'left',clickCount:1});
      await click('.managed-session-search input');const pointer=await search();
      assert(pointer.every(s=>parseFloat(s.outline)===0&&s.shadow==='none'));
      await shot(`${theme}-${font}-search-pointer`);
      await waitFor(`document.hasFocus()`);
      await js(`document.querySelector('.managed-session-panel header button').focus()`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Tab'});win.webContents.sendInputEvent({type:'keyUp',keyCode:'Tab'});
      await waitFor(`document.activeElement===document.querySelector('.managed-session-search input') && document.activeElement.matches(':focus-visible')`);
      const keyboard=await search();assert(keyboard.every(s=>parseFloat(s.outline)===0&&s.shadow==='none'));
      assert.notEqual(keyboard[0].background,'rgba(0, 0, 0, 0)');
      await shot(`${theme}-${font}-search-keyboard`);
      report.push({theme,font,bubble,reference,target,pointer,keyboard,group});
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({report,errors},null,2));
    assert.equal(errors.length,0);console.log('PASS: bubble content, attachments, hover, existing divider comparison, pointer and keyboard focus');win.destroy();app.quit();return;
  }
  if (appearance) {
    const palette = reference => js(`(() => {
      function styles(node) {if(!node)return null;const s=getComputedStyle(node);return Object.fromEntries(['backgroundColor','color','borderRadius','padding','boxShadow','fontSize','lineHeight'].map(k=>[k,s[k]]));}
      function message(selector){const n=[...document.querySelectorAll(selector)].at(-1);return {surface:styles(n),link:styles(n.querySelector('.rich-link')),inlineCode:styles(n.querySelector(':not(pre)>code')),codeBlock:styles(n.querySelector('.rich-code-block'))};}
      return {user:message(${JSON.stringify(reference ? '.user-message' : '.channel-message.own .channel-message-bubble')}),agent:message(${JSON.stringify(reference ? '.agent-block' : '.channel-message.agent .channel-message-bubble')})};
    })()`);
    for(const theme of ['light','dark'])for(const font of [14,18]) {
      win.setContentSize(1500,900);
      await win.loadURL(`http://127.0.0.1:5217/dev/channel-session-layout/index.html?session&theme=${theme}&font=${font}`);
      await waitFor(`!!document.querySelector('.agent-block .rich-code-block') && !!document.querySelector('.user-message .rich-code-block')`);
      const session=await palette(true);
      await shot(`${theme}-${font}-session-reference`);
      await win.loadURL(`http://127.0.0.1:5217/${baseline ? '.tmp/session-layout-baseline/' : ''}dev/channel-session-layout/index.html?theme=${theme}&font=${font}&running=1`);
      await waitFor(`!!document.querySelector('.channel-message.agent .rich-code-block')`);
      const collaboration=await palette(false);
      if(!baseline) {
        // DEV retains Collaboration's own padding and rich-content cards;
        // the shared contract is the user bubble's palette and corner shape.
        for (const property of ['backgroundColor','color','borderRadius']) {
          assert.equal(collaboration.user.surface[property],session.user.surface[property],`Human bubble ${property} follows Session`);
        }
        assert(parseFloat(collaboration.user.surface.padding)>0);
        assert.notEqual(collaboration.agent.surface.backgroundColor,'rgba(0, 0, 0, 0)','Collaboration agent retains a bubble');
        assert(parseFloat(collaboration.agent.surface.padding)>0 && parseFloat(collaboration.agent.surface.borderRadius)>0);
      }
      await shot(`${theme}-${font}-messages`);
      await click('.managed-agent-work-pill');
      await waitFor(`!!document.querySelector('.managed-session-panel')`);
      await settle();
      // Reach search via keyboard rather than relying on autoFocus alone.
      await js(`document.querySelector('.managed-session-panel header button').focus()`);
      await waitFor(`document.hasFocus()`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Tab'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'Tab'});
      await waitFor(`document.activeElement === document.querySelector('.managed-session-search input') && document.activeElement.matches(':focus-visible')`);
      const focus=await js(`(()=>{const n=document.querySelector('.managed-session-search'),input=n.querySelector('input'),s=getComputedStyle(n);return {keyboard:input.matches(':focus-visible'),style:s.outlineStyle,width:parseFloat(s.outlineWidth),shadow:s.boxShadow,background:s.backgroundColor}})()`);
      if(!baseline) {
        assert(focus.keyboard && focus.width===0 && focus.shadow==='none' && focus.background!=='rgba(0, 0, 0, 0)','Keyboard focus uses a surface cue, not a frame');
        assert(await js(`![...document.querySelectorAll('.managed-session-result')].some(n=>n.textContent.includes('/fixture'))`));
      }
      await shot(`${theme}-${font}-list-keyboard-focus`);
      await js(`const input=document.querySelector('.managed-session-search input');Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input,'layout-session-64');input.dispatchEvent(new Event('input',{bubbles:true}));`);
      await waitFor(`document.querySelectorAll('.managed-session-result').length===1`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Tab'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'Tab'});
      await waitFor(`document.activeElement === document.querySelector('.managed-session-result')`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'Enter'});
      win.webContents.sendInputEvent({type:'char',keyCode:'\r'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'Enter'});
      await waitFor(`window.selectedFixtureSession === 'layout-session-64'`);
      report.push({theme,font,session,collaboration,focus});
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({baseline,appearance,report,errors},null,2));
    assert.equal(errors.length,0);console.log('PASS: Session human surface, Collaboration agent bubble, unframed keyboard focus and exact keyboard selection');
    win.destroy();app.quit();return;
  }
  if (precise) {
    for (const theme of ['light','dark']) {
      win.setContentSize(2000,1423);
      await win.loadURL(`http://127.0.0.1:5217/${baseline ? '.tmp/session-layout-baseline/' : ''}dev/channel-session-layout/index.html?running=1&theme=${theme}`);
      await waitFor(`!!document.querySelector('.managed-agent-work-pill') && !!document.querySelector('.channel-response-status-avatar')`);
      await js(`window.savedComposer=document.querySelector('textarea');window.savedStream=document.querySelector('[role="log"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(savedComposer,'保留输入草稿 / keep this draft');savedComposer.dispatchEvent(new Event('input',{bubbles:true}));`);
      await click('.managed-agent-work-pill');
      await waitFor(`!!document.querySelector('.managed-session-panel')`);
      await settle();
      const beforeDrag=await geometry();
      checkChat(beforeDrag);
      assert(beforeDrag.launcherText.includes('1'));
      if(baseline)assert(beforeDrag.launcherAnimations.some(a=>a.state==='running'));
      else assert(!beforeDrag.launcherAnimations);
      let afterDrag;
      if(baseline) {
        await mouseDrag(Math.round(beforeDrag.panel.x+1),200,-80);
        afterDrag=await geometry();
        assert.equal(afterDrag.panel.w,beforeDrag.panel.w);
      } else {
        await js(`document.querySelector('.channel-inspector-resizer').dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))`);
        await settle();
        afterDrag=await drag(-80);
        assert(Math.abs(afterDrag.handle.x+afterDrag.handle.w/2-afterDrag.panel.x)<1,'The drag target centers on the actual chat/panel boundary');
      }
      const samples = await js(`new Promise(resolve=>{
        const samples=[], start=performance.now();
        const ball=document.querySelector('.channel-response-status-avatar'), pill=document.querySelector('.managed-agent-work-pill'), spinner=pill.querySelector('.managed-session-spinner'), input=document.querySelector('.composer-frame');
        function sample(now){
          const a=ball.getBoundingClientRect(),b=pill.getBoundingClientRect(),c=input.getBoundingClientRect();
          samples.push({elapsed:now-start,overlap:a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top,
            hit:!!document.elementFromPoint(a.x+a.width/2,a.y+a.height/2)?.closest('.channel-activity-inspect'),
            clearsInput:a.bottom<c.top&&b.bottom<=c.top,ballTime:ball.getAnimations()[0]?.currentTime,pillTime:spinner?.getAnimations()[0]?.currentTime});
          if(now-start>=2600)resolve(samples);else requestAnimationFrame(sample);
        }requestAnimationFrame(sample);
      })`);
      assert(samples.length>30 && samples.every(s=>!s.overlap&&s.hit&&s.clearsInput));
      assert(samples.at(-1).ballTime-samples[0].ballTime>=2400);
      if(baseline)assert(samples.at(-1).pillTime>samples[0].pillTime);
      await shot(`${theme}-running-1-boundary`);
      report.push({theme,beforeDrag,afterDrag,samples});
    }
    fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({baseline,precise,report,errors},null,2));
    assert.equal(errors.length,0); console.log(`PASS: precise boundary + running 1 pill; full animation cycle, both themes; ${report.map(r=>r.samples.length).join('/')} frames`);
    win.destroy();app.quit();return;
  }
  for (const theme of ["light", "dark"]) for (const font of [14, 18]) {
    win.setSize(1500, 900);
    await win.loadURL(`http://127.0.0.1:5217/${baseline ? '.tmp/session-layout-baseline/' : ''}dev/channel-session-layout/index.html?theme=${theme}&font=${font}`);
    await waitFor(`!!document.querySelector('.managed-agent-work-pill') && !!document.querySelector('.channel-response-status-avatar')`);
    await js(`window.savedComposer = document.querySelector('textarea'); window.savedStream = document.querySelector('[role="log"]'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(savedComposer,'保留输入草稿 / keep this draft'); savedComposer.dispatchEvent(new Event('input',{bubbles:true}));`);
    await shot(`${theme}-${font}-closed`);
    const closed = await geometry();
    checkChat(closed);
    if (theme === 'light' && font === 14) {
      const firstTime = closed.animations[0].time;
      for (let frame=0;frame<12;frame++) {
        await shot(`motion-${String(frame).padStart(2,'0')}`);
        await js(`new Promise(resolve=>{const start=performance.now();function tick(now){if(now-start>=160)resolve();else requestAnimationFrame(tick)}requestAnimationFrame(tick)})`);
      }
      assert((await geometry()).animations[0].time>firstTime,'The breathing animation must actually advance');
    }
    await click('.managed-agent-work-pill');
    await waitFor(`!!document.querySelector('.managed-session-panel')`);
    await settle();
    if (!baseline) {
      await js(`document.querySelector('.channel-inspector-resizer').dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))`);
      await settle();
    }
    const open = await geometry();
    checkChat(open);
    await shot(`${theme}-${font}-open`);
    let dragging;
    if (baseline) {
      await mouseDrag(Math.round(open.panel.x+2),200,-80);
      dragging = await geometry();
      assert.equal(dragging.panel.w,open.panel.w,'Baseline: dragging the panel edge has no effect');
      assert.equal(open.handle,null);
    } else {
      const larger = await drag(-80);
      await shot(`${theme}-${font}-dragged`);
      const maximum = await drag(-600);
      const minimum = await drag(700);
      await js(`document.querySelector('.channel-inspector-resizer').focus()`);
      win.webContents.sendInputEvent({type:'keyDown',keyCode:'LEFT'});
      win.webContents.sendInputEvent({type:'keyUp',keyCode:'LEFT'});
      await waitFor(`+document.querySelector('.channel-inspector-resizer').getAttribute('aria-valuenow') === ${minimum.panel.w+16}`);
      await settle();
      await js(`document.querySelector('.channel-inspector-resizer').dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))`);
      await settle();
      await drag(-80);
      await js(`document.querySelector('.managed-session-panel header button').click()`);
      await waitFor(`!document.querySelector('.managed-session-panel')`);
      await settle();
      checkChat(await geometry());
      assert(await js(`document.activeElement === document.querySelector('.managed-agent-work-pill')`), 'Closing should return focus to the entry');
      await click('.managed-agent-work-pill');
      await waitFor(`!!document.querySelector('.managed-session-panel')`);
      await settle();
      assert.equal((await geometry()).panel.w,larger.panel.w,'Preferred width survives reopening');
      await js(`document.querySelector('.managed-session-result').click()`);
      assert(await js(`!!window.selectedFixtureSession`),'Session navigation remains connected');
      // Real wheel input scrolls history, not the divider.
      const scroll = await js(`(()=>{const n=document.querySelector('.managed-session-results');const r=n.getBoundingClientRect();n.scrollTop=0;return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+50),max:n.scrollHeight-n.clientHeight}})()`);
      assert(scroll.max>0);
      win.webContents.sendInputEvent({type:'mouseWheel',x:scroll.x,y:scroll.y,deltaX:0,deltaY:-260});
      await waitFor(`document.querySelector('.managed-session-results').scrollTop>0`);
      assert.equal((await geometry()).panel.w,larger.panel.w);
      // A window becoming too narrow must cancel an in-flight drag and restore the cursor.
      const g = await geometry(), x = Math.round(g.handle.x+3), y = 200;
      win.webContents.sendInputEvent({type:'mouseDown',x,y,button:'left',clickCount:1});
      await waitFor(`document.querySelector('.channel-inspector-resizer').dataset.resizing === 'true'`);
      win.setSize(1080,760);
      await waitFor(`document.querySelector('.channel-inspector-resizer').hidden && document.body.style.cursor !== 'col-resize'`);
      win.webContents.sendInputEvent({type:'mouseUp',x,y,button:'left',clickCount:1});
      win.setSize(1280,900);
      await waitFor(`!document.querySelector('.channel-inspector-resizer').hidden`);
      await settle();
      const constrained = await drag(-500);
      win.setSize(1500,900);
      await waitFor(`innerWidth === 1500`);
      await settle();
      await shot(`${theme}-${font}-max-width`);
      dragging = {larger,maximum,minimum,constrained};
    }
    // Narrow windows retain the existing full-width inspector/back-to-chat behavior.
    win.setSize(1080,760);
    await waitFor(`document.querySelector('.channel-conversation').dataset.inspectorOverlay === 'true'`);
    await settle();
    const narrowOpen = await geometry();
    assert.equal(narrowOpen.panel.w,narrowOpen.chat.w);
    assert(narrowOpen.handle === null || narrowOpen.handle.w===0);
    await shot(`${theme}-${font}-narrow-open`);
    await js(`document.querySelector('.managed-session-panel header button').click()`);
    await waitFor(`!document.querySelector('.managed-session-panel')`);
    await settle();
    const narrowClosed = await geometry();
    checkChat(narrowClosed);
    await shot(`${theme}-${font}-narrow-closed`);
    await js(`document.querySelector('[aria-label="Toggle fixture sidebar"]').click()`);
    win.setSize(480,760);
    await waitFor(`innerWidth === 480 && document.querySelector('.channel-room-main').getBoundingClientRect().x === 0`);
    await settle();
    const compact = await geometry();
    checkChat(compact);
    await shot(`${theme}-${font}-compact`);
    await js(`window.layoutFixture.setEmpty(true)`);
    await waitFor(`!document.querySelector('.managed-agent-work-pill')`);
    assert(await js(`document.querySelector('textarea') === window.savedComposer && !!document.querySelector('.channel-response-status-avatar')`),'Empty sessions must not remove activity or input');
    report.push({theme,font,closed,open,dragging,narrowOpen,narrowClosed,compact});
  }
  if (!baseline) {
    win.setSize(1500,900);
    await win.loadURL('http://127.0.0.1:5217/dev/channel-session-layout/index.html?idle');
    await waitFor(`!!document.querySelector('.managed-agent-work-pill')`);
    assert(await js(`!document.querySelector('.managed-session-spinner') && !document.querySelector('.channel-response-status-avatar')`),'Idle fixture must not invent running activity');
    await click('.managed-agent-work-pill');
    await waitFor(`!!document.querySelector('.managed-session-panel')`);
    await settle();
    assert.equal((await geometry()).panel.w,600,'User width persists across a fresh mount');
    await shot('idle-reloaded');
  }
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ baseline, report, errors }, null, 2));
  console.log(JSON.stringify({baseline, sample:report[0], errors}, null, 2));
  assert.equal(errors.length, 0);
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
