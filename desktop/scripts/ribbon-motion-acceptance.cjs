// Real X11 pointer motion over shipping native chrome; macOS acceptance is separate.
const assert = require('node:assert/strict');
const path = require('node:path');
const { spawn } = require('node:child_process');
module.exports = async function acceptRibbon(h) {
  const { win, view, output, report, evaluate, until, settle, nativeInput, capture, delay } = h;
  const wc = view.webContents;
  const rail = () => win.contentView.children.find(v => v.webContents?.getURL() === 'wuu-chrome://ui/chrome-overlay.html?surface=ribbon');
  const b = win.getBounds();
  const video = spawn('ffmpeg', ['-hide_banner','-loglevel','error','-f','x11grab','-framerate','60','-video_size',`${b.width}x${b.height}`,'-i',`${process.env.DISPLAY}+${b.x},${b.y}`,'-c:v','libx264','-preset','ultrafast','-pix_fmt','yuv420p','-y',path.join(output,'ribbon-motion.mp4')]);
  await new Promise((resolve,reject) => { video.once('spawn',resolve);video.once('error',reject); });
  report.ribbonMotion = [];
  const { ipcMain } = require('electron');
  report.ribbonEvents = [];
  const logCommand = (_event, message) => {
    report.ribbonEvents.push({at:performance.now(), ...message});
    if(report.ribbonEvents.length>200)report.ribbonEvents.shift();
  };
  ipcMain.on('wuu:chrome-overlay-command',logCommand);
  try {
    for (const [width, zoom, theme] of [[1440,1,'light'],[600,1,'light'],[1280,1.25,'dark']]) {
      win.setContentSize(width,800);win.webContents.setZoomFactor(zoom);
      await evaluate(theme => {document.documentElement.dataset.theme=theme;},theme);await settle();
      const content=win.getContentBounds();
      const move=(x,y)=>nativeInput(['mousemove',String(Math.round(content.x+x*zoom)),String(Math.round(content.y+y*zoom))]);
      await move(width/zoom-60,40);
      if (process.env.WUU_RIBBON_BASELINE === '1' && rail()?.getVisible()) {
        await delay(500); report.initialRailStayedOpen = rail().getVisible();
        // Record the escaped bug, then enter the already shown native surface
        // so the remaining baseline cycles can still be compared.
        await move(80,200); await delay(50); await move(width/zoom-60,40);
      }
      await until(()=>!rail()?.getVisible(),'initial rail dismissed');
      const anchor=await evaluate(()=>{const r=document.querySelector('.sidebar-ribbon [data-wuu-component="website-navigation-item"]').getBoundingClientRect();return{x:r.x+12,y:r.y+r.height/2};});
      await wc.executeJavaScript('document.querySelector("#draft").value="keep this draft";scrollTo(0,240)');
      for(let repeat=0;repeat<3;repeat++) {
        const widths=[], hides=[], surface=rail(), originalBounds=surface.setBounds, originalVisible=view.setVisible;
        surface.setBounds=function(bounds){widths.push({at:performance.now(),width:bounds.width});return originalBounds.call(this,bounds);};
        view.setVisible=function(visible){hides.push(visible);return originalVisible.call(this,visible);};
        const state=()=>wc.executeJavaScript('({width:innerWidth,height:innerHeight,scroll:scrollY,draft:document.querySelector("#draft").value,ticks:window.ticks,visibility:document.visibilityState})');
        try {
          const before=await state();await move(anchor.x,anchor.y);
          await until(()=>surface.getVisible(),'native rail appears');await delay(300);
          const during=await state(), expanded=surface.getBounds();
          const hit=await surface.webContents.executeJavaScript('(()=>{const e=document.querySelector("[data-wuu-component=website-navigation-item]"),r=e.getBoundingClientRect();return {hit:e.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)),iconX:e.firstElementChild.getBoundingClientRect().x,motionDuration:getComputedStyle(document.querySelector(".sidebar-ribbon")).getPropertyValue("--ribbon-motion-duration")}})()');
          assert.ok(hit.hit,'Expanded labels receive native pointer input');
          assert.ok(win.contentView.children.indexOf(surface)>win.contentView.children.indexOf(view),'Rail is above the actual webpage');
          if(repeat===0)await capture(`ribbon-${width}-${theme}-${zoom}`);
          await move(width/zoom-60,40);await until(()=>!surface.getVisible(),'native rail closes completely');
          const after=await state();
          for(const key of ['width','height','scroll','draft']) {assert.equal(during[key],before[key],key+' during hover');assert.equal(after[key],before[key],key+' after hover');}
          assert.equal(hides.filter(v=>!v).length,0,'Hover never hides the webpage');assert.equal(after.visibility,'visible');assert.ok(after.ticks>before.ticks+5,'Page animation remains live');
          const distinct=[...new Set(widths.map(sample=>sample.width))];
          report.ribbonMotion.push({width,zoom,theme,repeat,before,during,after,expanded,widths,distinctWidths:distinct.length,pageHides:hides.filter(v=>!v).length,hit});
          if(process.env.WUU_RIBBON_BASELINE!=='1')assert.ok(distinct.length>=5,'Native reveal has intermediate widths');

          console.log('RIBBON',JSON.stringify({width,zoom,repeat,distinctWidths:distinct.length,pageHides:hides.filter(v=>!v).length}));
        } finally {surface.setBounds=originalBounds;view.setVisible=originalVisible;}
      }
      for(let i=0;i<5;i++){await move(anchor.x,anchor.y);await delay(170);await move(width/zoom-60,40);await wc.executeJavaScript(`document.title="Updating title ${i}"`);await delay(160);}
      await until(()=>!rail().getVisible(),'Rapid reversals settle');assert.ok(view.getVisible());
    }
    // Navigation must keep one painted surface in both directions, including
    // a fresh hover after leaving a non-browser destination.
    report.destinationMotion = [];
    const surface = rail();
    const origin = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    const moveTo = (x, y) => nativeInput(['mousemove', String(Math.round(origin.x + x * zoom)), String(Math.round(origin.y + y * zoom))]);
    const clickDestination = async selector => {
      const point = await surface.webContents.executeJavaScript(`(selector => {
        const r = document.querySelector(selector).getBoundingClientRect();
        return { x: r.x + 12, y: r.y + r.height / 2 };
      })(${JSON.stringify(selector)})`);
      const bounds = surface.getBounds();
      await moveTo(bounds.x / zoom + point.x, bounds.y / zoom + point.y);
      await nativeInput(['click', '1']);
    };
    const website = await evaluate(() => document.querySelector('[data-wuu-component="website-navigation-item"][aria-current="page"]').dataset.ribbonId);
    const websiteSelector = `[data-ribbon-id="${website}"]`;
    for (const selector of ['[data-wuu-component="conversations-section"]', '[data-ribbon-id="catalog"]']) {
      const anchor = await evaluate(() => { const r = document.querySelector('.sidebar-ribbon [data-ribbon-id]').getBoundingClientRect(); return { x: r.x + 12, y: r.y + r.height / 2 }; });
      await moveTo(anchor.x, anchor.y);
      await until(() => surface.getVisible() && surface.getBounds().width >= 240 * zoom, 'browser ribbon expanded');
      await clickDestination(selector);
      await until(() => evaluate(selector => document.querySelector(selector)?.getAttribute('aria-current') === 'page', selector), 'normal destination selected');
      await moveTo(win.getContentSize()[0] / zoom - 60, 40);
      await until(() => !surface.getVisible(), 'normal destination ribbon dismissed');
      const widths = [], visibility = [];
      const originalBounds = surface.setBounds, originalVisible = surface.setVisible;
      surface.setBounds = function(bounds) { widths.push(bounds.width); return originalBounds.call(this, bounds); };
      surface.setVisible = function(visible) { visibility.push(visible); return originalVisible.call(this, visible); };
      try {
        await moveTo(anchor.x, anchor.y);
        await until(() => surface.getVisible() && surface.getBounds().width >= 240 * zoom, 'fresh normal-page hover uses the same animated surface');
        const targetWidth = await evaluate(() => document.querySelector('.sidebar-ribbon').getBoundingClientRect().width);
        await until(() => surface.getBounds().width === Math.ceil(targetWidth * zoom), 'normal-page reveal finishes');
        assert.ok(new Set(widths).size >= 5, 'Normal-page hover has intermediate widths');
        await capture(`normal-ribbon-${report.destinationMotion.length}`);
        widths.length = 0; visibility.length = 0;
        const expandedWidth = surface.getBounds().width;
        for (const target of [websiteSelector, selector, websiteSelector]) {
          await clickDestination(target);
          await until(() => evaluate(target => document.querySelector(target)?.getAttribute('aria-current') === 'page', target), 'destination selected without replacing ribbon');
          await settle();
          assert.equal(surface.getVisible(), true, 'Switch keeps ribbon visible');
          assert.equal(surface.getBounds().width, expandedWidth, 'Switch keeps expanded width');
        }
        assert.ok(visibility.every(Boolean), 'Switch never hides the navigation surface');
        assert.ok(widths.every(width => width === expandedWidth), 'Switch never restarts reveal');
        assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'), 'keep this draft');
        report.destinationMotion.push({ selector, expandedWidth, widths, visibility });
      } finally { surface.setBounds = originalBounds; surface.setVisible = originalVisible; }
      await moveTo(win.getContentSize()[0] / zoom - 60, 40);
      await until(() => !surface.getVisible(), 'switching ribbon dismissed');
    }
    if (!surface.webContents.debugger.isAttached()) surface.webContents.debugger.attach('1.3');
    report.reducedMotion = [];
    for (const source of ['system', 'app']) {
      const media = { features: [{ name: 'prefers-reduced-motion', value: source === 'system' ? 'reduce' : 'no-preference' }] };
      await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', media);
      await surface.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', media);
      await evaluate(source => { document.documentElement.dataset.appearanceMotion = source === 'app' ? 'reduce' : 'system'; }, source);
      const p = await evaluate(() => {const r=document.querySelector('.sidebar-ribbon [data-wuu-component="website-navigation-item"]').getBoundingClientRect();return{x:r.x+12,y:r.y+r.height/2};});
      const origin=win.getContentBounds(), zoom=win.webContents.getZoomFactor();
      report.ribbonEvents = [];
      const firstEvent = 0;
      await nativeInput(['mousemove',String(Math.round(origin.x+p.x*zoom)),String(Math.round(origin.y+p.y*zoom))]);
      await until(()=>surface.getVisible() && surface.getBounds().width>=(process.env.WUU_RIBBON_BASELINE==='1'?190:240)*zoom,'Reduced motion reveals the full native rail');
      await nativeInput(['mousemove',String(origin.x+win.getContentSize()[0]-60),String(origin.y+40)]);
      await until(()=>!surface.getVisible(),'Reduced motion closes the native rail');
      const fractions = report.ribbonEvents.slice(firstEvent).filter(event => event.command.type === 'reveal').map(event => event.command.fraction);
      assert.ok(fractions.every(value => value === 0 || value === 1), 'Reduced motion skips intermediate geometry');
      report.reducedMotion.push({source, fractions});
    }
    surface.webContents.debugger.detach();
  } finally {
    ipcMain.removeListener('wuu:chrome-overlay-command',logCommand);
    report.finalRail = { visible: rail()?.getVisible(), bounds: rail()?.getBounds() };
    if(video.exitCode===null){const stopped=new Promise(resolve=>video.once('exit',resolve));video.kill('SIGINT');await stopped;}
  }
};
