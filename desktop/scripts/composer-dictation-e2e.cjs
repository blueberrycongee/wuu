const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const evidence = path.resolve('.tmp/composer-dictation-e2e');
fs.mkdirSync(evidence, { recursive: true });
if (!process.versions.electron) {
  const vite = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--config', 'dev/composer-service/vite.config.ts', '--host', '127.0.0.1', '--port', '5194'], { cwd: path.resolve('desktop'), stdio: 'inherit' });
  (async () => {
    try {
      for (let attempt = 0; ; attempt++) { try { if ((await fetch('http://127.0.0.1:5194/dev/composer-service/')).ok) break; } catch {} if (attempt > 100) throw Error('Fixture server did not start'); await new Promise(resolve => setTimeout(resolve, 100)); }
      const child = spawn(require('../node_modules/electron'), ['--no-sandbox', __filename], { stdio: 'inherit' });
      process.exitCode = await new Promise(resolve => child.on('exit', code => resolve(code ?? 1)));
    } finally { vite.kill(); }
  })().catch(error => { console.error(error); process.exitCode = 1; vite.kill(); });
} else {
  const { app, BrowserWindow } = require('electron');
  app.whenReady().then(async () => {
    const win = new BrowserWindow({ width: 1000, height: 720, webPreferences: { sandbox: true } });
    const errors = [];
    win.webContents.on('console-message', (_event, level, message) => { if (level >= 3) errors.push(message); });
    const evaluate = source => win.webContents.executeJavaScript(source, true);
    const waitFor = expression => evaluate(`new Promise((resolve,reject)=>{let n=0;const timer=setInterval(()=>{if(${expression}){clearInterval(timer);resolve()}else if(++n>150){clearInterval(timer);reject(Error(${JSON.stringify(expression)}))}},50)})`);
    const click = selector => evaluate(`document.querySelector(${JSON.stringify(selector)}).click()`);
    const screenshot = async name => fs.writeFileSync(path.join(evidence, name+'.png'), (await win.webContents.capturePage()).toPNG());
    const actionGeometry = () => evaluate(`(() => {
      const visible = element => {
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden';
      };
      const bounds = rect => ({ left: rect.left, top: rect.top, width: rect.width, height: rect.height });
      const frame = document.querySelector('.composer-frame');
      return [...frame.querySelectorAll('.composer-action-button, .composer-plus-button')].filter(visible).map(element => {
        const hit = bounds(element.getBoundingClientRect());
        const pseudo = getComputedStyle(element, '::before');
        const disc = {
          left: hit.left + Number.parseFloat(pseudo.left),
          top: hit.top + Number.parseFloat(pseudo.top),
          width: Number.parseFloat(pseudo.width),
          height: Number.parseFloat(pseudo.height),
        };
        const icon = element.querySelector('svg');
        return { label: element.getAttribute('aria-label') || element.getAttribute('data-wuu-component'), hit, disc, icon: icon ? bounds(icon.getBoundingClientRect()) : null };
      });
    })()`);
    const assertActionGeometry = (label, controls) => {
      assert(controls.length >= 3, `${label}: expected the composer's visible action controls`);
      const hitSizes = new Set(controls.map(control => `${control.hit.width}x${control.hit.height}`));
      const discSizes = new Set(controls.map(control => `${control.disc.width}x${control.disc.height}`));
      assert.equal(hitSizes.size, 1, `${label}: action controls must share one hit-target size`);
      assert.equal(discSizes.size, 1, `${label}: action controls must share one visible disc size`);
      const [representative] = controls;
      assert.ok(representative.disc.width < representative.hit.width, `${label}: the visible disc must sit inside the hit target`);
      for (const control of controls) {
        const hitCenterX = control.hit.left + control.hit.width / 2;
        const hitCenterY = control.hit.top + control.hit.height / 2;
        assert.ok(Math.abs(control.disc.left + control.disc.width / 2 - hitCenterX) <= 0.5 && Math.abs(control.disc.top + control.disc.height / 2 - hitCenterY) <= 0.5, `${label}: ${control.label} disc must be concentric with its hit target`);
        if (control.icon) {
          assert.ok(Math.abs(control.icon.left + control.icon.width / 2 - hitCenterX) <= 0.5 && Math.abs(control.icon.top + control.icon.height / 2 - hitCenterY) <= 0.5, `${label}: ${control.label} glyph must be concentric with its control`);
        }
      }
    };
    try {
      await win.loadURL('http://127.0.0.1:5194/dev/composer-service/?dictation');
      await waitFor(`document.querySelector('[data-wuu-component="composer-dictation"]')`);
      assertActionGeometry('idle', await actionGeometry());
      await screenshot('idle');
      await evaluate(`window.dictationAcceptance.autoFinish=false; document.querySelector('textarea').focus()`);
      win.webContents.insertText('现有草稿');
      await waitFor(`document.querySelector('textarea').value==='现有草稿'`);
      assertActionGeometry('send-enabled', await actionGeometry());
      await click('[data-wuu-component="composer-dictation"]');
      await waitFor(`document.querySelector('[data-phase="listening"]')`);
      await waitFor(`Array.from(document.querySelectorAll('.composer-dictation-wave > span')).filter(e=>Number(e.style.opacity)>.5).length>10`);
      assert.equal(await evaluate(`document.querySelector('textarea').value`), '现有草稿');
      assertActionGeometry('recording', await actionGeometry());
      for (const [name, width, theme, size] of [['light-wide',1000,'light',15],['dark-wide',1000,'dark',15],['light-narrow-large',460,'light',20],['dark-narrow-large',460,'dark',20]]) {
        win.setSize(width,720);
        await evaluate(`document.documentElement.dataset.theme=${JSON.stringify(theme)}; document.documentElement.style.setProperty('--conversation-message-font-size','${size}px'); new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
        assertActionGeometry(name, await actionGeometry());
        await screenshot(name);
        const bounds = await evaluate(`Array.from(document.querySelectorAll('.composer-dictation-recording button, [data-wuu-component="composer-send"]')).map(e=>{const r=e.getBoundingClientRect();return {x:r.x,right:r.right,width:r.width}})`);
        assert(bounds.every(r=>r.x>=0&&r.right<=width&&r.width>0), 'Controls must remain reachable');
      }
      await click('.composer-dictation-stop');
      await waitFor(`window.dictationAcceptance.stopped`);
      assert.equal(await evaluate(`window.composerAcceptance.calls.filter(c=>c.action==='composer.submit').length`), 0);
      await evaluate(`window.dictationAcceptance.finish()`);
      await waitFor(`document.querySelector('textarea').value.includes(window.dictationAcceptance.transcript)`);
      assert.equal(await evaluate(`document.querySelector('textarea').value`), '现有草稿\n'+await evaluate(`window.dictationAcceptance.transcript`));
      await screenshot('transcribed');
      await click('[data-wuu-component="composer-send"]');
      await waitFor(`document.querySelector('textarea').value===''`);
      await click('[data-wuu-component="composer-dictation"]');
      await waitFor(`document.querySelector('[data-phase="listening"]')`);
      await click('[data-wuu-component="composer-send"]');
      await waitFor(`document.querySelector('[data-phase="finalizing"]')`);
      await click('[data-wuu-component="composer-send"]');
      assert.equal(await evaluate(`window.composerAcceptance.calls.filter(c=>c.action==='composer.submit').length`), 1);
      await evaluate(`window.dictationAcceptance.finish(); window.dictationAcceptance.finish()`);
      await waitFor(`window.composerAcceptance.calls.filter(c=>c.action==='composer.submit').length===2`);
      const sent = await evaluate(`window.composerAcceptance.calls.filter(c=>c.action==='composer.submit')`);
      assert.equal(sent[1].input.text, await evaluate(`window.dictationAcceptance.transcript`));
      await waitFor(`document.querySelector('textarea').value===''`);
      await click('[data-wuu-component="composer-dictation"]');
      await waitFor(`document.querySelector('[data-phase="listening"]')`);
      const staleId = await evaluate(`window.dictationAcceptance.active`);
      await click('.composer-dictation-recording button');
      await evaluate(`window.dictationAcceptance.emit({sessionId:${JSON.stringify(staleId)},type:'result',text:'stale',is_final:true})`);
      assert.equal(await evaluate(`document.querySelector('textarea').value`), '');
      await click('[data-wuu-component="composer-dictation"]');
      await waitFor(`document.querySelector('[data-phase="listening"]')`);
      await evaluate(`window.dictationAcceptance.emit({sessionId:window.dictationAcceptance.active,type:'error',code:'microphone_permission_denied'})`);
      await waitFor(`document.querySelector('.composer-dictation-error')`);
      await screenshot('permission-error');
      await click('[data-wuu-component="composer-dictation"]');
      await waitFor(`document.querySelector('[data-phase="listening"]')`);
      await evaluate(`window.composerAcceptance.retire()`);
      await waitFor(`document.querySelectorAll('textarea').length===0 && window.dictationAcceptance.active===''`);
      assert.deepEqual(errors, []);
      fs.writeFileSync(path.join(evidence,'result.json'), JSON.stringify({passed:true, sent, checks:['stop preserves draft and awaits final transcript','direct send awaits final result and sends once','cancel ignores late results','permission denial','owner retirement cancels microphone','action controls share hit-target and concentric visible-disc geometry','light/dark and large/narrow rendering']},null,2));
      console.log('Dictation composer journeys passed'); app.exit(0);
    } catch(error) { console.error(error,errors); await screenshot('failure');fs.writeFileSync(path.join(evidence,'failure.json'),JSON.stringify({error:String(error),errors}));app.exit(1); }
  });
}
