// Native media and unsaved-form E2E. Synthetic content only; no real devices.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { dialog, nativeImage } = require('electron');

function prepareMediaFixture(home) {
  const file = path.join(home, 'synthetic-av.mp4');
  const generated = spawnSync('ffmpeg', ['-y', '-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000', '-t', '6', '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '26', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-b:a', '64k', '-movflags', '+faststart', file], { encoding: 'utf8', timeout: 30000 });
  assert.equal(generated.status, 0, generated.stderr || String(generated.error));
  const bytes = fs.readFileSync(file);
  return { bytes, sha256: createHash('sha256').update(bytes).digest('hex') };
}
function serveMediaFixture(request, response, url, media) {
  if (url.pathname === '/media.mp4') {
    const range = /^bytes=(\d+)-(\d*)$/.exec(request.headers.range || '');
    const start = range ? Number(range[1]) : 0;
    const end = range?.[2] ? Math.min(Number(range[2]), media.bytes.length - 1) : media.bytes.length - 1;
    if (start > end || start >= media.bytes.length) { response.writeHead(416, { 'Content-Range': `bytes */${media.bytes.length}` }); response.end(); return true; }
    response.writeHead(range ? 206 : 200, { 'Content-Type': 'video/mp4', 'Accept-Ranges': 'bytes', 'Content-Length': end - start + 1,
      ...(range ? { 'Content-Range': `bytes ${start}-${end}/${media.bytes.length}` } : {}) });
    response.end(media.bytes.subarray(start, end + 1)); return true;
  }
  if (!['/media', '/dirty'].includes(url.pathname)) return false;
  response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
  response.end(`<!doctype html><meta charset="utf-8"><title>Synthetic ${url.pathname}</title>
<style>body{font:18px system-ui;margin:28px;background:#edf4fa;color:#17354a}button,input{font:inherit;margin:8px;padding:10px}video{display:block;max-width:95%;background:black}video:fullscreen{max-width:none;width:100%;height:100%;object-fit:contain}</style>
<h1>${url.pathname === '/media' ? 'Local H.264 + AAC playback' : 'Unsaved synthetic form'}</h1>
${url.pathname === '/media' ? `<p>Generated test pattern and tone. Muted playback; physical audio output is not tested.</p>
<video id="video" controls muted playsinline width="640" height="360" src="/media.mp4"></video>
<button id="play">Play video</button><button id="pause">Pause video</button><button id="seek">Seek to 3 seconds</button><button id="fullscreen">Video fullscreen</button><p id="result"></p>
<script>const v=document.querySelector('#video');const result=document.querySelector('#result');
document.querySelector('#play').onclick=()=>v.play().catch(e=>result.textContent=e.message);
document.querySelector('#pause').onclick=()=>v.pause();document.querySelector('#seek').onclick=()=>v.currentTime=3;
document.querySelector('#fullscreen').onclick=()=>v.requestFullscreen().catch(e=>result.textContent=e.message);</script>` : `<p>Typing here arms this fixture's ordinary beforeunload warning.</p><input id="draft" placeholder="Synthetic unsaved draft"><p id="result"></p>
<script>window.__acceptanceDirty=false;document.querySelector('#draft').oninput=()=>{window.__acceptanceDirty=true;document.querySelector('#result').textContent='Unsaved local changes';};window.addEventListener('beforeunload',event=>{if(window.__acceptanceDirty){event.preventDefault();event.returnValue='';}});</script>`}`);
  return true;
}

async function runBrowserMediaAcceptance(h, fixture) {
  const { win, report, output, started, save, testNative, openSite, pageClick, navigateAddress, readSite, nativeView, until, evaluate, fill, address, capture, xinput, delay } = h;
  // A synchronous OS dialog suspends Electron's main JS. A bounded helper
  // waits for the real X11 dialog, screenshots it, and selects its actual button.
  // The production dialog result is never replaced or automatically granted.
  function observeNativeDecision(method, title, messageFragment, choice, label) {
    const original = dialog[method]; const observations = [];
    dialog[method] = function(...args) {
      const options = args.at(-1);
      if (options.title !== title || !options.message.includes(fixture.origin) || !options.message.includes(messageFragment)) return original.apply(this, args);
      assert.equal(options.buttons.length, 2, 'Native helper expects two decision buttons');
      assert.equal(options.defaultId, 0, 'Native helper starts on the safe default');
      assert.equal(options.cancelId, 0, 'Escape preserves the safe decision');
      const imageFile = `native-dialog-${Date.now()}-${label}.png`;
      const resultFile = path.join(output, imageFile + '.json');
      const record = { title, message: options.message, buttons: options.buttons, requestedChoice: choice, imageFile, resultFile };
      observations.push(record);
      const child = spawn('python3', ['-c', `
import json, os, subprocess, sys, time
config=json.loads(sys.argv[1]); result={}
try:
 ids=subprocess.run(['xdotool','search','--sync','--onlyvisible','--name',config['title']],capture_output=True,text=True,timeout=8,check=True).stdout.split()
 window=ids[-1]
 subprocess.run(['xdotool','windowactivate','--sync',window],timeout=3,check=True)
 subprocess.run(['ffmpeg','-y','-hide_banner','-loglevel','error','-f','x11grab','-video_size','1600x1000','-i',os.environ['DISPLAY'],'-frames:v','1',config['image']],capture_output=True,timeout=6,check=True)
 # Frame decoration offsets made root-coordinate clicks miss the buttons.
 # Both real dialogs focus button 0; Tab selects button 1 without guessing pixels.
 keys=['Escape'] if config['choice']==0 else ['Tab','Return']
 subprocess.run(['xdotool','key','--clearmodifiers',*keys],timeout=3,check=True)
 result['input']={'window':window,'keys':keys}
 deadline=time.monotonic()+4
 while time.monotonic()<deadline:
  visible=subprocess.run(['xdotool','search','--onlyvisible','--name',config['title']],capture_output=True,text=True,timeout=2).stdout.split()
  if window not in visible: break
  time.sleep(0.1)
 else: raise TimeoutError('Native dialog remained visible after the real input; recovered with safe Escape')
 result['status']='completed'
except Exception as error:
 result={'status':'failed','error':str(error)}
 subprocess.run(['xdotool','key','Escape'],timeout=3)
finally:
 open(config['result'],'w').write(json.dumps(result))
`, JSON.stringify({ title: title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), choice, image: path.join(output, imageFile), result: resultFile })], { stdio: 'ignore' });
      record.helperPID = child.pid;
      const result = original.apply(this, args);
      if (result && typeof result.then === 'function') return result.then(value => { record.response = value.response; return value; });
      record.response = result; return result;
    };
    return { observations, restore: () => { dialog[method] = original; }, async evidence() {
      for (const record of observations) {
        await until(() => fs.existsSync(record.resultFile), 'native dialog helper finished', 15000);
        const result = JSON.parse(fs.readFileSync(record.resultFile));
        assert.equal(result.status, 'completed', result.error);
        assert.equal(record.response, choice, 'Actual native dialog returned the selected button');
        const png = fs.readFileSync(path.join(output, record.imageFile));
        report.screenshots.push({ file: record.imageFile, source: 'x11-real-native-dialog', atMs: Date.now() - started,
          sha256: createHash('sha256').update(png).digest('hex'), image: nativeImage.createFromBuffer(png).getSize(), decision: choice });
        record.input = result; delete record.resultFile;
      }
      save(); return observations;
    } };
  }

  await testNative('local H264 AAC video decodes plays pauses seeks and enters fullscreen', async () => {
    const wc = await openSite('Fixture Alpha', '/media');
    const state = () => wc.executeJavaScript(`(() => {const v=document.querySelector('#video');return {time:v.currentTime,paused:v.paused,ready:v.readyState,duration:v.duration,error:v.error?.message||null,frames:v.getVideoPlaybackQuality().totalVideoFrames,width:v.videoWidth,height:v.videoHeight,fullscreen:document.fullscreenElement===v,muted:v.muted};})()`);
    await until(async () => (await state()).ready >= 2, 'MP4 metadata and decoded frame available');
    await pageClick(wc, '#play', 'play-local-h264-aac');
    await until(async () => { const value=await state();return value.time>0.8&&value.frames>8&&!value.paused; }, 'video clock and decoded frames advance');
    await capture('native-mp4-playing'); const playing = await state();
    await pageClick(wc, '#pause', 'pause-local-video');
    await until(async () => (await state()).paused, 'video pauses');
    const paused = await state(); await delay(250); assert.ok(Math.abs((await state()).time - paused.time) < 0.08);
    await pageClick(wc, '#seek', 'seek-local-video');
    await until(async () => Math.abs((await state()).time - 3) < 0.15, 'video seeks to requested time');
    const permission = observeNativeDecision('showMessageBox', 'Website permission', 'fullscreen', 1, 'allow-fixture-fullscreen');
    const wasWindowFullscreen = win.isFullScreen();
    let fullscreen;
    try {
      await pageClick(wc, '#fullscreen', 'enter-video-fullscreen');
      await until(async () => (await state()).fullscreen, 'native video fullscreen entered');
      await until(() => {
        const bounds = nativeView('/media').getBounds(), [width, height] = win.getContentSize();
        return bounds.x === 0 && bounds.y === 0 && bounds.width === width && bounds.height === height && wc.isFocused();
      }, 'Fullscreen native video covers the whole window and keeps keyboard focus');
      fullscreen = { ...await state(), windowFullscreen: win.isFullScreen(), nativeBounds: nativeView('/media').getBounds(), focused: wc.isFocused() };
      await capture('native-video-fullscreen');
      await permission.evidence(); xinput(['key', '--clearmodifiers', 'Escape']);
      await until(async () => !(await state()).fullscreen && win.isFullScreen() === wasWindowFullscreen, 'video fullscreen exited');
      await until(async () => {
        const host = await evaluate(() => {
          const rect = document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-host').getBoundingClientRect();
          return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
        });
        const bounds = nativeView('/media').getBounds(), zoom = win.webContents.getZoomFactor();
        return wc.isFocused() && ['x', 'y', 'width', 'height'].every(key => Math.abs(bounds[key] - Math.round(host[key] * zoom)) <= 2);
      }, 'Escape restores the normal native host bounds and keyboard focus');
      await capture('native-video-fullscreen-exited');
    } finally { permission.restore(); xinput(['key', 'Escape']); }
    return { source: { sha256: fixture.media.sha256, codecs: ['H.264', 'AAC'] }, playing, paused, fullscreen, audioOutput: 'unverified; muted synthetic playback' };
  });

  await testNative('native unsaved-form Stay preserves input and Leave closes only its tab', async () => {
    const original = await openSite('Fixture Alpha', '/alpha');
    const before = (await readSite()).tabs.length;
    await pageClick(original, '#draft', 'new-unsaved-fixture-tab'); xinput(['key', '--clearmodifiers', 'ctrl+t']);
    await until(async () => (await readSite()).tabs.length === before + 1, 'dirty-form test tab created');
    await until(() => evaluate(() => document.activeElement?.matches('.web-workspace-stage:not([hidden]) .web-workspace-start-search input')), 'New blank tab receives native shortcut focus');
    await navigateAddress('/dirty', 'dirty-form');
    const wc = nativeView('/dirty').webContents;
    try {
      await pageClick(wc, '#draft', 'type-unsaved-native-form'); await wc.insertText('Keep this unsaved fixture draft');
      const stayClose = observeNativeDecision('showMessageBoxSync', 'Leave this website?', 'unsaved changes', 0, 'stay-on-close');
      try {
        xinput(['key', '--clearmodifiers', 'ctrl+w']);
        await until(() => stayClose.observations.length === 1 && stayClose.observations[0].response === 0, 'Stay answered real close warning');
        await stayClose.evidence();
        assert.ok(!wc.isDestroyed()); assert.equal((await readSite()).tabs.length, before + 1);
        assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'), 'Keep this unsaved fixture draft');
      } finally { stayClose.restore(); }
      const stayNavigation = observeNativeDecision('showMessageBoxSync', 'Leave this website?', 'unsaved changes', 0, 'stay-on-navigation');
      try {
        await fill(address, fixture.origin + '/alpha'); xinput(['key', '--clearmodifiers', 'Return']);
        await until(() => stayNavigation.observations.length === 1 && stayNavigation.observations[0].response === 0, 'Stay answered real navigation warning');
        await stayNavigation.evidence();
        assert.equal(wc.getURL(), fixture.origin + '/dirty');
        assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'), 'Keep this unsaved fixture draft');
        await capture('unsaved-draft-survives-navigation-cancel');
      } finally { stayNavigation.restore(); }
      const leave = observeNativeDecision('showMessageBoxSync', 'Leave this website?', 'unsaved changes', 1, 'leave-close');
      try {
        await pageClick(wc, '#draft', 'close-confirmed-fixture'); xinput(['key', '--clearmodifiers', 'ctrl+w']);
        await until(() => wc.isDestroyed(), 'Leave closes confirmed dirty tab');
        await leave.evidence(); assert.equal(leave.observations.length, 1);
        assert.ok(!win.isDestroyed()); assert.equal((await readSite()).tabs.length, before);
      } finally { leave.restore(); }
      return { stayPreservedDocumentAndInput: true, cancelledNavigationPreservedURL: true, confirmedTabDestroyed: true, mainWindowSurvived: true };
    } finally {
      if (!wc.isDestroyed()) await wc.executeJavaScript('window.__acceptanceDirty=false');
    }
  });
}
module.exports = { prepareMediaFixture, serveMediaFixture, runBrowserMediaAcceptance };
