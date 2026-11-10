// Visual check for the flat runtime picker: vite dev page + Electron capture.
//   node scripts/runtime-picker-visual-e2e.cjs
// Screenshots land in .tmp/runtime-picker-visual/ for review.
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const output = path.resolve('.tmp/runtime-picker-visual');
fs.mkdirSync(output, { recursive: true });
const PORT = 5195;
if (!process.versions.electron) {
  const vite = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--config', 'dev/runtime-picker/vite.config.ts', '--host', '127.0.0.1', '--port', String(PORT)], { cwd: __dirname + '/..', stdio: 'inherit' });
  (async () => {
    try {
      for (let attempt = 0; ; attempt++) {
        try { if ((await fetch(`http://127.0.0.1:${PORT}/dev/runtime-picker/`)).ok) break; } catch {}
        if (attempt > 100) throw Error('Fixture server did not start');
        await new Promise(resolve => setTimeout(resolve, 100));
      }
      const child = spawn(require('electron'), ['--no-sandbox', __filename], { stdio: 'inherit' });
      process.exitCode = await new Promise(resolve => child.on('exit', code => resolve(code ?? 1)));
    } finally { vite.kill(); }
  })().catch(error => { console.error(error); process.exitCode = 1; vite.kill(); });
} else {
  const { app, BrowserWindow } = require('electron');
  app.whenReady().then(async () => {
    const win = new BrowserWindow({ width: 1180, height: 780, webPreferences: { sandbox: true }, show: true });
    const errors = [];
    win.webContents.on('console-message', (_event, level, message) => { if (level >= 3) errors.push(message); });
    const evaluate = source => win.webContents.executeJavaScript(source, true);
    const settle = () => evaluate(`Promise.all(document.getAnimations().filter(animation=>animation.effect?.getComputedTiming().iterations!==Infinity).map(animation=>animation.finished.catch(()=>{})))`);
    const load = async (scene, extra = '') => {
      await win.loadURL(`http://127.0.0.1:${PORT}/dev/runtime-picker/?scene=${scene}${extra}`);
      await evaluate(`new Promise((resolve,reject)=>{let n=0;const timer=setInterval(()=>{if(document.querySelector('.runtime-flat-panel')){clearInterval(timer);resolve()}else if(++n>150){clearInterval(timer);reject(Error('panel did not render for ${scene}'))}},100)})`);
      await settle();
      await evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))`);
    };
    const capture = async name => { fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG()); };
    try {
      await load('wuu'); await capture('wuu-composer-light');
      await load('engine'); await capture('engine-codex-light');
      await load('rail'); await capture('wuu-rail-light');
      await load('rail', '&engine=codex'); await capture('engine-rail-light');
      await load('locked'); await capture('engine-locked-light');
      await load('noeffort'); await capture('wuu-noeffort-light');
      await load('wuu', '&theme=dark'); await capture('wuu-composer-dark');
      win.setContentSize(560, 780);
      await load('wuu'); await capture('wuu-composer-narrow');
      await evaluate(`document.querySelector('.codex-model-menu input[type="search"]').focus(); document.querySelector('.codex-model-menu input[type="search"]').value='';`);
      // Narrow + rail: the engine column must keep docking the rail edge.
      await load('rail'); await capture('wuu-rail-narrow');
      const report = { screenshots: fs.readdirSync(output).filter(name => name.endsWith('.png')), errors };
      fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify(report, null, 2));
      console.log('captures:', report.screenshots.join(', '));
      console.log('errors:', errors.length ? errors : 'none');
      app.exit(0);
    } catch (error) { console.error(error, errors); fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: String(error), errors })); app.exit(1); }
  });
}
