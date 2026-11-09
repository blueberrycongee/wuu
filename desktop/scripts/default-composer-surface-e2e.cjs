const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const assert = require('node:assert/strict');
const evidence = path.resolve('.tmp/default-composer-surface-e2e');
fs.mkdirSync(evidence, { recursive: true });
if (!process.versions.electron) {
  const vite = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--config', 'dev/composer-service/vite.config.ts', '--host', '127.0.0.1', '--port', '5193'], { cwd: path.resolve('desktop'), stdio: 'inherit' });
  (async () => {
    try {
      for (let attempt = 0; ; attempt++) { try { if ((await fetch('http://127.0.0.1:5193/dev/composer-service/')).ok) break; } catch {} if (attempt > 100) throw Error('Fixture server did not start'); await new Promise(resolve => setTimeout(resolve, 100)); }
      const child = spawn(require('../node_modules/electron'), ['--no-sandbox', __filename], { stdio: 'inherit' });
      process.exitCode = await new Promise(resolve => child.on('exit', code => resolve(code ?? 1)));
    } finally { vite.kill(); }
  })().catch(error => { console.error(error); process.exitCode = 1; vite.kill(); });
} else {
  const { app, BrowserWindow } = require('electron');
  app.whenReady().then(async () => {
    const win = new BrowserWindow({ width: 1100, height: 800, webPreferences: { sandbox: true } });
    const errors = [];
    win.webContents.on('console-message', (_event, level, message) => { if (level >= 3) errors.push(message); });
    const evaluate = source => win.webContents.executeJavaScript(source, true);
    try {
      await win.loadURL('http://127.0.0.1:5193/dev/composer-service/');
      await evaluate(`new Promise((resolve,reject)=>{let n=0;const timer=setInterval(()=>{if(document.querySelector('textarea')){clearInterval(timer);resolve()}else if(++n>150){clearInterval(timer);reject(Error('Composer did not render'))}},100)})`);
      await evaluate(`document.querySelector('textarea').focus()`);
      win.webContents.insertText('Selected service sends to its captured draft');
      await evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
      await evaluate(`document.querySelector('.composer-send-button').click()`);
      const submitted = await evaluate(`window.composerAcceptance.calls.filter(call=>call.action==='composer.submit')`);
      assert.equal(submitted.length, 1); assert.equal(submitted[0].input.ownerTabId, 'draft-one'); assert.match(submitted[0].input.text, /Selected service/);
      await evaluate(`document.querySelector('[data-public-tool]').click()`);
      assert.equal(await evaluate(`window.composerAcceptance.calls.filter(call=>call.action==='fixture-tool').length`), 1);
      await evaluate(`document.querySelector('textarea').focus()`);
      win.webContents.insertText('A longer draft checks wrapping while the selected composer keeps its toolbar and send control reachable. '.repeat(4));
      for (const [name, width, theme, size] of [['light-wide',1100,'light',15],['dark-narrow',500,'dark',15],['light-large',500,'light',20],['dark-large',1100,'dark',20]]) {
        win.setSize(width, 800);
        await evaluate(`document.documentElement.dataset.theme=${JSON.stringify(theme)}; document.documentElement.style.setProperty("--conversation-message-font-size",${JSON.stringify(size+"px")}); new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
        fs.writeFileSync(path.join(evidence, name+'.png'), (await win.webContents.capturePage()).toPNG());
      }
      await evaluate(`document.querySelector('.codex-runtime-trigger').click(); new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
      await evaluate(`Promise.all(document.getAnimations().filter(animation=>animation.effect?.getComputedTiming().iterations!==Infinity).map(animation=>animation.finished.catch(()=>{})))`);
      fs.writeFileSync(path.join(evidence, 'runtime-menu.png'), (await win.webContents.capturePage()).toPNG());
      await evaluate(`window.composerAcceptance.retire()`);
      await evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
      assert.equal(await evaluate(`document.querySelectorAll('textarea').length`), 0);
      assert.deepEqual(errors, []);
      fs.writeFileSync(path.join(evidence,'result.json'), JSON.stringify({ passed: true, submitted, screenshots: ['light-wide.png','dark-narrow.png','light-large.png','dark-large.png','runtime-menu.png'] }, null, 2));
      console.log('Selected composer source submission and retirement passed'); app.exit(0);
    } catch (error) { console.error(error, errors); fs.writeFileSync(path.join(evidence,'failure.json'), JSON.stringify({ error: String(error), errors })); app.exit(1); }
  });
}
