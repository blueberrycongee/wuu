// End-to-end Linux X11 input and live PiP, using only a disposable local app.
import assert from 'node:assert/strict';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdirSync, writeFileSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { cpus, release, totalmem } from 'node:os';
import { monitorEventLoopDelay, performance } from 'node:perf_hooks';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { app, BrowserWindow, WebContentsView, screen, nativeImage } from 'electron';
import { createObservationPiPFactory } from '../src/main/browserPiPWindow';
import { ObservationCoordinator } from '../src/main/cuaActivityWindows';
import type { BrowserHostCoordinator } from '../src/main/browserHostWindows';
import type { WindowRegistry } from '../src/main/windowRegistry';
import type { ActivitySession } from '../src/shared/protocol';

// Hosted Xvfb has no GPU. Use real software-rendered X11 windows, as the other
// Linux desktop fixtures do; capture/input still use production X11 paths.
app.disableHardwareAcceleration();
app.commandLine.appendSwitch('ozone-platform', 'x11');
// Closing the fixture is an acceptance step, not completion of the runner.
// Keep Electron alive until finish() has finalized the recording and results.
app.on('window-all-closed', () => {});

const fixtureMode = process.argv.includes('--linux-cua-fixture');
const output = resolve(process.env.WUU_LINUX_CUA_OUTPUT ?? 'out/e2e/linux-cua');
const profile = process.env.WUU_LINUX_CUA_PROFILE!;
const helper = resolve(process.env.WUU_CUA_LINUX_HELPER ?? join(dirname(fileURLToPath(import.meta.url)), '../../build/bin/wuu-cua-linux'));
app.setPath('userData', join(profile, fixtureMode ? 'target' : 'host'));
process.env.WUU_CUA_LINUX_HELPER = helper;
const results: Record<string, unknown> = { platform: process.platform, display: process.env.DISPLAY, checks: [] };
const checks = results.checks as string[];
const children: ChildProcess[] = [];
let coordinator: ObservationCoordinator | undefined;
let recorder: ChildProcess | undefined;
let fixture: ChildProcess;
let nextID = 0;
const fixtureReplies = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void }>();
const fixtureSize = { width: 620, height: 680 };
const fixtureHTML = `<!doctype html><html><style>
*{box-sizing:border-box}body{margin:0;font:18px system-ui;background:#f4f7fa;color:#18334f}header{height:120px;background:#123456;color:white;padding:24px}h1{margin:0;font-size:24px}p{margin:8px 0}#marker{position:absolute;right:24px;top:24px;width:80px;height:70px;background:#ffff00}main{padding:30px 40px}button{width:190px;height:60px;font:inherit;background:#dcecff;border:1px solid #5b80aa;border-radius:12px}label{display:block;margin-top:24px}input[type=text]{display:block;width:540px;height:44px;font:inherit;padding:8px;border:1px solid #5b80aa}input[type=range]{position:absolute;left:60px;top:335px;width:480px;height:30px}#scroll{position:absolute;left:40px;top:390px;width:540px;height:180px;overflow:auto;border:1px solid #8195aa;padding:12px}#scroll article{height:650px;background:linear-gradient(#dcecff,#c9f0df)}footer{position:absolute;left:40px;bottom:25px;color:#48705b}body.dark{background:#172334;color:#eff4ff}
</style><body><header><h1>Linux CUA acceptance</h1><p>Disposable app · native X11 input</p><span id="marker"></span></header><main><button id="click">Click count: 0</button><label>Native text entry<input id="text" type="text"></label><input id="range" type="range" min="0" max="100" value="0"><div id="scroll"><article>Scroll this panel<br><br>Every input is delivered by the production Linux helper.<br><br>No external sites or accounts.</article></div><footer id="status">Ready for native input</footer></main><script>
window.state={clicks:0,text:'',range:0,scroll:0,keys:[]};
const button=document.getElementById('click'),field=document.getElementById('text'),slider=document.getElementById('range'),panel=document.getElementById('scroll');
button.onclick=()=>{state.clicks++;button.textContent='Click count: '+state.clicks;button.style.background=state.clicks%2?'#cc66ff':'#22cc66';document.getElementById('status').textContent='Click received';};
field.oninput=()=>{state.text=field.value;};slider.oninput=()=>{state.range=Number(slider.value);};panel.onscroll=()=>{state.scroll=panel.scrollTop;};
document.onkeydown=e=>{state.keys.push(e.key);};
</script></body></html>`;

async function waitFor(label: string, check: () => Promise<unknown> | unknown, timeout = 12_000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!(await check())) {
    if (Date.now() >= deadline) throw new Error(`Timed out: ${label}`);
    await new Promise(resolve => setTimeout(resolve, 40));
  }
}
function fixtureRequest(command: string, value?: unknown): Promise<any> {
  const id = ++nextID;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { fixtureReplies.delete(id); reject(new Error(`Fixture timed out: ${command}`)); }, 5000);
    fixtureReplies.set(id, { resolve: value => { clearTimeout(timer); resolve(value); }, reject });
    fixture.send!({ id, command, value });
  });
}
function startHelper(env = process.env) {
  const child = spawn(helper, [], { env, stdio: ['pipe', 'pipe', 'pipe'] });
  children.push(child);
  let sequence = 0, buffer = '';
  const pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void }>();
  child.stderr.on('data', chunk => process.stderr.write(`[linux-helper] ${chunk}`));
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', chunk => {
    buffer += chunk;
    for (;;) {
      const newline = buffer.indexOf('\n');
      if (newline < 0) break;
      const message = JSON.parse(buffer.slice(0, newline));
      buffer = buffer.slice(newline + 1);
      if (message.id != null) {
        const request = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) request?.reject(new Error(JSON.stringify(message.error)));
        else request?.resolve(message.result);
      }
    }
  });
  child.on('exit', (code, signal) => {
    for (const request of pending.values()) request.reject(new Error(`Linux helper exited ${code}/${signal}`));
    pending.clear();
  });
  return { child, request(method: string, params: unknown = {}) {
    const id = ++sequence;
    return new Promise<any>((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Helper timed out: ${method}`)); }, 15_000);
      pending.set(id, { resolve: value => { clearTimeout(timer); resolve(value); }, reject: error => { clearTimeout(timer); reject(error); } });
      child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`);
    });
  } };
}
async function capture(name: string): Promise<void> {
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'x11grab', '-video_size', '1440x900', '-i', process.env.DISPLAY!, '-frames:v', '1', join(output, name)], { timeout: 15_000 });
}
async function fixtureMain(): Promise<void> {
  const target = new BrowserWindow({ x: 40, y: 40, ...fixtureSize, frame: false, show: false, webPreferences: { sandbox: true, contextIsolation: true } });
  await target.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(fixtureHTML)}`);
  const shown = once(target, 'show', { signal: AbortSignal.timeout(5000) });
  target.show();
  await shown;
  await target.webContents.capturePage();
  await target.webContents.executeJavaScript('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  const windowID = target.getNativeWindowHandle().readUInt32LE(0);
  process.send?.({ ready: true, processID: process.pid, windowID });
  process.on('message', async ({ id, command, value }: any) => {
    try {
      let result: unknown = true;
      if (command === 'state') result = await target.webContents.executeJavaScript('window.state');
      else if (command === 'marker') await target.webContents.executeJavaScript(`document.getElementById('marker').style.background=${JSON.stringify(value)}`);
      else if (command === 'animate') await target.webContents.executeJavaScript(`(() => {
        clearInterval(window.animation);
        if (${Boolean(value)}) { let frame = 0; window.animation = setInterval(() => {
          // A long color cycle cannot alias the 100 ms settling sampler.
          document.getElementById('marker').style.background = 'rgb('+((++frame*37)%256)+',255,128)';
        }, 50); }
      })()`);
      else if (command === 'theme') await target.webContents.executeJavaScript(`document.body.classList.toggle('dark',${JSON.stringify(value === 'dark')})`);
      else if (command === 'move') target.setPosition(Number(value), 40);
      else if (command === 'size') { target.setSize(value.width, value.height); result = target.getSize(); }
      else if (command === 'hide') target.hide();
      else if (command === 'minimize') target.minimize();
      else if (command === 'restore') { target.restore(); target.showInactive(); }
      else if (command === 'show') target.showInactive();
      else if (command === 'close') target.close();
      else throw new Error(`Unknown fixture command ${command}`);
      process.send?.({ id, result });
    } catch (error) { process.send?.({ id, error: String(error) }); }
  });
  process.on('disconnect', () => app.exit(0));
}

async function acceptance(): Promise<void> {
  mkdirSync(output, { recursive: true });
  const extensionList = execFileSync('xdpyinfo', ['-queryExtensions'], { encoding: 'utf8' });
  for (const extension of ['Composite', 'DAMAGE', 'XTEST']) assert.ok(extensionList.includes(extension), `${extension} is required for real X11 capture/input`);
  const desktopSize = screen.getPrimaryDisplay().size;
  assert.ok(desktopSize.width >= 1440 && desktopSize.height >= 900, 'Acceptance display must be at least 1440×900');
  results.extensions = ['Composite', 'DAMAGE', 'XTEST'];
  recorder = spawn('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'x11grab', '-framerate', '10', '-video_size', '1440x900', '-i', process.env.DISPLAY!, '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', join(output, 'linux-cua-pip.mp4')], { stdio: ['pipe', 'ignore', 'inherit'] });
  fixture = spawn(process.execPath, [...(app.commandLine.hasSwitch('no-sandbox') ? ['--no-sandbox'] : []), fileURLToPath(import.meta.url), '--linux-cua-fixture'], { stdio: ['ignore', 'inherit', 'inherit', 'ipc'], env: process.env });
  children.push(fixture);
  let target: { processID: number; windowID: number } | undefined;
  fixture.on('message', (message: any) => {
    if (message.ready) target = message;
    else if (message.id) {
      const request = fixtureReplies.get(message.id); fixtureReplies.delete(message.id);
      if (message.error) request?.reject(new Error(message.error)); else request?.resolve(message.result);
    }
  });
  await waitFor('native fixture window', () => target);
  const identity = target!;
  results.target = identity;
  results.scope = 'Production helper MCP and production PiP factory; activity is supplied directly to ObservationCoordinator, not through a Go app-server turn.';
  let appID = `linux:${identity.processID}:${identity.windowID}`;
  const rpc = startHelper();
  await rpc.request('initialize', { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'linux-cua-e2e', version: '1' } });
  const listed = await rpc.request('tools/list');
  assert.ok(listed.tools.some((tool: any) => tool.name === 'computer'));
  const computer = (args: Record<string, unknown>) => rpc.request('tools/call', { name: 'computer', arguments: { app: appID, control_epoch: 'linux-cua-acceptance', ...args } });
  async function observe(saveImage = true) {
    const response = await computer({ action: 'observe' });
    assert.ok(!response.isError, JSON.stringify(response));
    const details = response.structuredContent;
    assert.equal(details.process_id, identity.processID);
    assert.equal(details.window_id, identity.windowID);
    assert.ok(details.snapshot_id);
    const image = response.content.find((content: any) => content.type === 'image');
    assert.ok(image?.data, 'Observation returns actual target pixels');
    if (saveImage) writeFileSync(join(output, 'target-observation.png'), Buffer.from(image.data, 'base64'));
    return details;
  }
  async function input(action: string, args: Record<string, unknown>) {
    const observation = await observe();
    const response = await computer({ action, snapshot_id: observation.snapshot_id, coordinate_space: 'screenshot', foreground_policy: 'allow', ...args });
    assert.ok(!response.isError, JSON.stringify(response));
    return observation;
  }
  const inventory = await computer({ action: 'list_apps', app: '' });
  assert.ok(!inventory.isError, JSON.stringify(inventory));
  const inventoryText = inventory.content.filter((part: any) => part.type === 'text').map((part: any) => part.text).join('\n');
  for (const window of inventory.structuredContent.apps) assert.ok(inventoryText.includes(window.id), 'Every discovered window ID must reach the model-facing content');
  const initialObservation = await observe();
  results.screenshot = initialObservation.screenshot;
  const canonicalTarget = initialObservation.app;
  assert.match(canonicalTarget, /^linux:\d+:\d+:\d+$/, 'Resolved target includes the process lifetime');
  appID = canonicalTarget;
  const resolved = await computer({ action: 'list_apps' });
  assert.ok(!resolved.isError, JSON.stringify(resolved));
  assert.equal(resolved.structuredContent.resolved_app.id, canonicalTarget);
  assert.deepEqual(resolved.structuredContent.apps.map((item: any) => item.id), [canonicalTarget]);
  const wrongPID = await computer({ action: 'list_apps', app: canonicalTarget.replace(`linux:${identity.processID}:`, `linux:${process.pid}:`) });
  assert.equal(wrongPID.isError, true, 'Filtered target resolution rejects a mismatched process');
  const wrongLifetime = await computer({ action: 'list_apps', app: canonicalTarget.replace(/:\d+$/, ':1') });
  assert.equal(wrongLifetime.isError, true, 'Filtered target resolution rejects a stale process lifetime');
  checks.push('MCP initialize, tool discovery, target-bound screenshot');
  const refusalSnapshot = await observe();
  const refused = await computer({ action: 'click', snapshot_id: refusalSnapshot.snapshot_id, coordinate_space: 'screenshot', x: 90, y: 180, foreground_policy: 'avoid' });
  assert.equal(refused.isError, true, 'Unsupported background control is rejected');
  assert.equal((await fixtureRequest('state')).clicks, 0);
  const mismatchedTarget = await computer({ action: 'observe', app: `linux:${process.pid}:${identity.windowID}` });
  assert.equal(mismatchedTarget.isError, true, 'A valid XID with the wrong process cannot be observed');
  const nativeOrigin = () => {
    const info = execFileSync('xwininfo', ['-id', String(identity.windowID)], { encoding: 'utf8' });
    const match = /Absolute upper-left X:\s+(-?\d+)/.exec(info);
    assert.ok(match, info);
    return Number(match[1]);
  };
  const originalOrigin = nativeOrigin();
  const beforeMove = await observe();
  await fixtureRequest('move', 60);
  await waitFor('fixture moved on X11', () => nativeOrigin() !== originalOrigin);
  results.nativeWindowOrigins = { original: originalOrigin, moved: nativeOrigin() };
  const movedSnapshot = await computer({ action: 'click', snapshot_id: beforeMove.snapshot_id, coordinate_space: 'screenshot', x: 130, y: 180, foreground_policy: 'allow' });
  assert.equal(movedSnapshot.structuredContent.error_code, 'stale_snapshot', 'Moving a target invalidates old coordinates');
  assert.equal((await fixtureRequest('state')).clicks, 0);
  await fixtureRequest('move', 40);
  await waitFor('fixture position restored on X11', () => nativeOrigin() === originalOrigin);
  const consumed = await input('click', { x: 130, y: 180 });
  await waitFor('native click', async () => (await fixtureRequest('state')).clicks === 1);
  const replay = await computer({ action: 'click', snapshot_id: consumed.snapshot_id, coordinate_space: 'screenshot', x: 130, y: 180, foreground_policy: 'allow' });
  assert.equal(replay.structuredContent.error_code, 'stale_snapshot', 'Pointer snapshots cannot be reused');
  assert.equal((await fixtureRequest('state')).clicks, 1);
  await input('click', { x: 250, y: 270 });
  await input('type_text', { text: 'Linux CUA works' });
  await waitFor('native text', async () => (await fixtureRequest('state')).text === 'Linux CUA works');
  await input('press_key', { key: 'BackSpace' });
  await waitFor('native keyboard', async () => (await fixtureRequest('state')).text === 'Linux CUA work');
  await input('drag', { from_x: 70, from_y: 350, to_x: 500, to_y: 350, duration: 0.4 });
  await waitFor('native drag', async () => (await fixtureRequest('state')).range > 70);
  await input('scroll', { x: 300, y: 470, direction: 'down', pages: 2 });
  await waitFor('native scroll', async () => (await fixtureRequest('state')).scroll > 0);
  results.inputState = await fixtureRequest('state'); checks.push('foreground click, text, keyboard, drag, scroll; background mutation refused');

  const main = new BrowserWindow({ x: 720, y: 40, width: 660, height: 800, frame: false, show: false, webPreferences: { sandbox: true } });
  await main.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent("<body style='margin:0;padding:34px;background:#eef2f7;color:#18334f;font:18px system-ui'><h1>Wuu · Linux live preview</h1><p>Native app input and continuous window capture</p><p>The preview stays bound to the selected window.<br>Occlusion must not replace it with other screen pixels.</p><input autofocus placeholder='Focus sentinel' style='font:inherit;padding:12px;width:90%'></body>")}`);
  main.show(); main.focus();
  await waitFor('host focus', () => main.isFocused());
  const activity: ActivitySession = { id: 'linux-cua', kind: 'cua', thread_id: 'linux-cua-thread', workdir: '/linux-cua-fixture', plugin_id: 'cua-linux', target: canonicalTarget, process_id: identity.processID, window_id: identity.windowID, state: 'foreground_controlled', controller: 'agent', execution_id: 'linux-acceptance', created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
  const factory = createObservationPiPFactory({ browserHost: null as unknown as BrowserHostCoordinator, isPackaged: false, parent: () => main });
  coordinator = new ObservationCoordinator({ mainWindow: () => main } as unknown as WindowRegistry, undefined, factory);
  coordinator.setBrowserPiPHostLayout(main.webContents.id, main, { host: { x: 0, y: 0, width: 660, height: 800 }, obstacles: [] });
  coordinator.setActiveThread(activity.thread_id);
  coordinator.update(activity);
  await waitFor('PiP created', () => BrowserWindow.getAllWindows().some(win => win !== main));
  const pip = BrowserWindow.getAllWindows().find(win => win !== main)!;
  const getView = async (selector: string) => {
    for (const child of pip.contentView.children) if (child instanceof WebContentsView && await child.webContents.executeJavaScript(`!!document.querySelector(${JSON.stringify(selector)})`).catch(() => false)) return child.webContents;
    return undefined;
  };
  await waitFor('live preview video', async () => {
    const view = await getView('video');
    return view && await view.executeJavaScript("(()=>{const v=document.querySelector('video');return v.readyState>=2&&v.videoWidth>0&&v.currentTime>0})()");
  }, 20_000);
  await waitFor('live preview visible', () => pip.isVisible());
  assert.ok(main.isFocused(), 'Starting observation must not activate the target or steal focus');
  const video = (await getView('video'))!;
  const overlay = (await getView('#root'))!;
  const settlePreview = async () => {
    await waitFor('preview appearance settled', async () => pip.getOpacity() === 1 && await overlay.executeJavaScript("document.getElementById('appear').hidden"));
    await overlay.executeJavaScript('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  };
  const videoSize = await video.executeJavaScript("(()=>{const v=document.querySelector('video');return {width:v.videoWidth,height:v.videoHeight}})()");
  results.videoSize = videoSize;
  assert.deepEqual(videoSize, { width: initialObservation.screenshot.width, height: initialObservation.screenshot.height }, 'Native helper and live PiP capture the same selected window geometry');
  assert.equal(await video.executeJavaScript("document.querySelector('video').srcObject.getVideoTracks().filter(t=>t.readyState==='live').length"), 1);
  assert.equal(await video.executeJavaScript("document.querySelector('video').srcObject.getAudioTracks().length"), 0);
  const pixel = (x: number, y: number) => video.executeJavaScript(`(()=>{const v=document.querySelector('video'),c=document.createElement('canvas');if(v.readyState<2||!v.videoWidth||v.hidden)return [];c.width=v.videoWidth;c.height=v.videoHeight;const ctx=c.getContext('2d');ctx.drawImage(v,0,0);return Array.from(ctx.getImageData(Math.round(${x}*c.width),Math.round(${y}*c.height),1,1).data)})()`);
  const isColor = (actual: number[], expected: number[]) => expected.every((value, index) => Math.abs(actual[index] - value) < 16);
  await waitFor('real target pixels in PiP', async () => isColor(await pixel(.02, .02), [18, 52, 86]));
  const firstTime = await video.executeJavaScript("document.querySelector('video').currentTime");
  await waitFor('continuous captured frames', async () => await video.executeJavaScript("document.querySelector('video').currentTime") > firstTime + .15);
  await settlePreview();
  await capture('01-linux-cua-live.png'); checks.push('production PiP factory, target pixels, continuous frames, no focus activation');

  const cover = new BrowserWindow({ x: 40, y: 40, width: 620, height: 680, frame: false, show: false, webPreferences: { sandbox: true } });
  await cover.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent("<body style='margin:0;background:#ff00ff;font:26px system-ui;padding:30px;color:#220022'><h1>Occluding window</h1><p>The live preview must still show the target app.</p></body>")}`);
  cover.showInactive();
  main.focus();
  await fixtureRequest('marker', '#00ffff');
  await waitFor('occluded target continues updating', async () => isColor(await pixel(560 / initialObservation.screenshot.width, 59 / initialObservation.screenshot.height), [0, 255, 255]));
  assert.ok(isColor(await pixel(.02, .02), [18, 52, 86]), 'Preview contains target pixels rather than occluder');
  assert.ok(main.isFocused(), 'Capture of an occluded window must not activate it');
  await capture('02-occluded-target-live.png');
  const coverPixel = nativeImage.createFromPath(join(output, '02-occluded-target-live.png')).crop({ x: 50, y: 50, width: 1, height: 1 }).toBitmap();
  assert.ok(isColor([coverPixel[2], coverPixel[1], coverPixel[0]], [255, 0, 255]), 'Native desktop pixels prove the target is actually occluded');
  checks.push('XComposite occlusion capture updates without activation or screen fallback');
  cover.destroy();
  coordinator.setAppearance(true, { reducedMotion: false, fontSize: 20, fontFamily: 'system-ui', locale: 'en-US', surface: '#172334', text: '#eff4ff', muted: '#b4c2d4' });
  await fixtureRequest('theme', 'dark');
  const beforeResize = pip.getBounds();
  const pipWindowID = pip.getNativeWindowHandle().readUInt32LE(0);
  results.previewNativeGeometry = {
    window: execFileSync('xwininfo', ['-id', String(pipWindowID)], { encoding: 'utf8' }),
    frame: execFileSync('xprop', ['-id', String(pipWindowID), '_GTK_FRAME_EXTENTS', '_NET_FRAME_EXTENTS'], { encoding: 'utf8' }),
  };
  const resizeCommands: string[] = [];
  const captureResize = (_event: unknown, url: string) => { if (url.startsWith('wuu-pip://resize')) resizeCommands.push(url); };
  overlay.on('will-navigate', captureResize);
  results.resize = { before: beforeResize, minimum: pip.getMinimumSize(), maximum: pip.getMaximumSize(), commands: resizeCommands };
  overlay.sendInputEvent({ type: 'mouseMove', x: beforeResize.width - 9, y: beforeResize.height - 9, globalX: beforeResize.x + beforeResize.width - 9, globalY: beforeResize.y + beforeResize.height - 9 });
  overlay.sendInputEvent({ type: 'mouseDown', x: beforeResize.width - 9, y: beforeResize.height - 9, globalX: beforeResize.x + beforeResize.width - 9, globalY: beforeResize.y + beforeResize.height - 9, button: 'left', clickCount: 1 });
  await waitFor('preview resize gesture started', () => overlay.executeJavaScript("document.getElementById('root').classList.contains('resizing')"));
  overlay.sendInputEvent({ type: 'mouseMove', x: 251, y: 231, globalX: beforeResize.x + 251, globalY: beforeResize.y + 231, modifiers: ['leftbuttondown'] });
  await waitFor('dark preview chrome', () => overlay.executeJavaScript("document.documentElement.classList.contains('dark')"));
  await waitFor('small preview bounds', () => { const bounds = pip.getBounds(); return bounds.width === 260 && bounds.height === 240; });
  overlay.sendInputEvent({ type: 'mouseUp', x: 251, y: 231, globalX: beforeResize.x + 251, globalY: beforeResize.y + 231, button: 'left', clickCount: 1 });
  await waitFor('preview resize gesture ended', () => overlay.executeJavaScript("!document.getElementById('root').classList.contains('resizing')"));
  results.smallPreviewBounds = pip.getBounds();
  overlay.removeListener('will-navigate', captureResize);
  await waitFor('dark target pixels', async () => isColor(await pixel(.02, .30), [23, 35, 52]));
  await settlePreview();
  await capture('03-dark-small-large-text.png'); checks.push('dark appearance, small preview, large UI font');

  await fixtureRequest('minimize');
  await waitFor('minimized target stops capture', () => video.executeJavaScript("document.querySelector('video').srcObject===null"));
  await fixtureRequest('restore');
  await waitFor('restored target resumes capture', () => video.executeJavaScript("document.querySelector('video').srcObject?.getVideoTracks().some(t=>t.readyState==='live')"));
  await waitFor('restored target pixels', async () => isColor(await pixel(.02, .02), [18, 52, 86]));
  // The explicit native restore can itself activate the fixture asynchronously.
  // Establish host focus after restoration, then verify ongoing capture leaves it.
  main.focus();
  await waitFor('host focus after explicit target restore', () => main.isFocused());
  const restoredFrameTime = await video.executeJavaScript("document.querySelector('video').currentTime");
  await waitFor('restored capture advances without activation', async () => await video.executeJavaScript("document.querySelector('video').currentTime") > restoredFrameTime + .15);
  assert.ok(main.isFocused(), 'Restored observation does not take host focus');
  await fixtureRequest('hide');
  await waitFor('hidden target stops capture', () => video.executeJavaScript("document.querySelector('video').srcObject===null"));
  assert.equal(await video.executeJavaScript("(()=>{const c=document.querySelector('canvas');return c.getContext('2d').getImageData(0,0,1,1).data[3]})()"), 0, 'Hidden target clears stale pixels');
  await capture('04-target-hidden.png');
  await fixtureRequest('show'); main.focus();
  await waitFor('shown target resumes exact pixels', async () => {
    if (!await video.executeJavaScript("document.querySelector('video').srcObject?.getVideoTracks().some(t=>t.readyState==='live') && document.querySelector('video').readyState>=2")) return false;
    return isColor(await pixel(.02, .02), [18, 52, 86]);
  });
  checks.push('target minimize/hide release tracks and clear stale pixels; restore/show resumes exact target without focus change');

  coordinator.setActiveThread('other-thread');
  await waitFor('conversation switch hides PiP', () => !pip.isVisible());
  await waitFor('hidden preview releases tracks', () => video.executeJavaScript("document.querySelector('video').srcObject===null"));
  await input('click', { x: 130 / (initialObservation.screenshot.width - 1) * 1000, y: 180 / (initialObservation.screenshot.height - 1) * 1000, coordinate_space: 'normalized' });
  await waitFor('native control continues while preview hidden', async () => (await fixtureRequest('state')).clicks === 2);
  coordinator.update({ ...activity, updated_at: new Date().toISOString() });
  assert.equal(pip.isVisible(), false, 'Background activity updates cannot show another conversation preview');
  main.focus();
  coordinator.setActiveThread(activity.thread_id);
  await waitFor('conversation return restores PiP', () => pip.isVisible());
  await waitFor('restored preview frames', async () => (await getView('video'))?.executeJavaScript("document.querySelector('video').readyState>=2"));
  await overlay.executeJavaScript("document.getElementById('close').click()");
  await waitFor('close dismisses PiP', () => !pip.isVisible() || pip.isDestroyed());
  await input('click', { x: 130, y: 180 });
  await waitFor('native control continues after PiP close', async () => (await fixtureRequest('state')).clicks === 3);
  coordinator.update({ ...activity, updated_at: new Date().toISOString() });
  assert.ok(pip.isDestroyed() || !pip.isVisible(), 'Further activity does not undo explicit preview dismissal');
  main.focus();
  coordinator.showForThread(activity.thread_id);
  await waitFor('explicit reopen', () => BrowserWindow.getAllWindows().some(win => win !== main && win.isVisible()));
  await waitFor('reopened preview delivers new frames', () => video.executeJavaScript("document.querySelector('video').srcObject?.getVideoTracks().some(t=>t.readyState==='live') && document.querySelector('video').readyState>=2"));
  await settlePreview();
  await capture('05-reopened-preview.png'); checks.push('conversation visibility, capture track teardown, explicit dismiss/reopen; native helper actions continue while preview hidden or dismissed');

  // A permanently animated target cannot become quiet. Post-input capture must
  // still finish within its bounded settling window and return explicit status.
  await fixtureRequest('animate', true);
  const settlingStarted = performance.now();
  const animatedAfter = await computer({ action: 'press_key', key: 'Escape', foreground_policy: 'allow', after: 'vision' });
  const settlingElapsedMs = performance.now() - settlingStarted;
  assert.ok(!animatedAfter.isError, JSON.stringify(animatedAfter));
  assert.equal(animatedAfter.structuredContent.settling, 'timed_out');
  assert.ok(settlingElapsedMs >= 1000 && settlingElapsedMs < 2500, `Animated post-input capture remains bounded: ${settlingElapsedMs} ms`);
  results.animatedPostInput = { settling: animatedAfter.structuredContent.settling, elapsedMs: settlingElapsedMs };
  await fixtureRequest('animate', false);
  checks.push('animated post-input observation reports bounded settling timeout without stalling the application');

  // Measure the shipped capture path under sustained changing pixels and native
  // input. Screenshots, synchronous encoders and process startup are outside the
  // timed phases; the independent evidence recorder remains running throughout.
  const tickRate = Number(execFileSync('getconf', ['CLK_TCK'], { encoding: 'utf8' }).trim());
  assert.ok(Number.isFinite(tickRate) && tickRate > 0);
  const directChildren = (await readFile(`/proc/${process.pid}/task/${process.pid}/children`, 'utf8')).trim().split(/\s+/).filter(Boolean).map(Number);
  const helperPIDs: number[] = [];
  for (const pid of directChildren) {
    const command = await readFile(`/proc/${pid}/cmdline`, 'utf8').catch(() => '');
    if (command.split('\0')[0] === helper) helperPIDs.push(pid);
  }
  assert.equal(helperPIDs.length, 2, 'Exactly one input helper and one target watcher are running');
  const percentile = (values: number[], fraction: number) => {
    assert.ok(values.length > 0, 'Performance sample must not be empty');
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * fraction) - 1)]!;
  };
  const summarize = (values: number[]) => ({ count: values.length, mean: values.reduce((sum, value) => sum + value, 0) / values.length, p50: percentile(values, .5), p95: percentile(values, .95), max: Math.max(...values) });
  const resourceSample = async () => {
    const electron = app.getAppMetrics().map(metric => ({ pid: metric.pid, type: metric.type,
      rssMiB: metric.memory.workingSetSize / 1024, cpuSeconds: Number(metric.cpu.cumulativeCPUUsage) }));
    const helpers = await Promise.all(helperPIDs.map(async pid => {
      const stat = await readFile(`/proc/${pid}/stat`, 'utf8');
      const fields = stat.slice(stat.lastIndexOf(')') + 2).trim().split(/\s+/);
      const status = await readFile(`/proc/${pid}/status`, 'utf8');
      return { pid, rssMiB: Number(/VmRSS:\s+(\d+)/.exec(status)?.[1]) / 1024,
        cpuSeconds: (Number(fields[11]) + Number(fields[12])) / tickRate };
    }));
    for (const value of [...electron, ...helpers]) assert.ok(Number.isFinite(value.rssMiB) && Number.isFinite(value.cpuSeconds), 'Process resource counters must be available');
    return { atMs: performance.now(), electron, helpers,
      electronRssMiB: electron.reduce((sum, value) => sum + value.rssMiB, 0),
      helperRssMiB: helpers.reduce((sum, value) => sum + value.rssMiB, 0) };
  };
  const startRendererProbe = `(() => {
    const video = document.querySelector('video');
    const state = { frames: [], ui: [], started: performance.now(), lastFrame: performance.now(), lastUI: performance.now(), frameID: 0, uiID: 0 };
    const frame = now => { state.frames.push(now - state.lastFrame); state.lastFrame = now; state.frameID = video.requestVideoFrameCallback(frame); };
    const ui = now => { state.ui.push(now - state.lastUI); state.lastUI = now; state.uiID = requestAnimationFrame(ui); };
    state.frameID = video.requestVideoFrameCallback(frame); state.uiID = requestAnimationFrame(ui);
    window.linuxCuaPerformance = state;
  })()`;
  const stopRendererProbe = `(() => {
    const video = document.querySelector('video'), state = window.linuxCuaPerformance;
    video.cancelVideoFrameCallback(state.frameID); cancelAnimationFrame(state.uiID);
    const tracks = video.srcObject?.getVideoTracks() ?? [];
    return { elapsedMs: performance.now() - state.started, frames: state.frames, ui: state.ui,
      finalFrameGapMs: performance.now() - state.lastFrame,
      liveTracks: tracks.filter(track => track.readyState === 'live').length,
      settings: tracks.map(track => track.getSettings()) };
  })()`;
  const performanceReport: Record<string, any> = {
    scope: 'Isolated production PiP factory and native helper, not the full Wuu app or a Mac hardware comparison.',
    environment: { os: release(), arch: process.arch, electron: process.versions.electron, chrome: process.versions.chrome,
      cpuModel: cpus()[0]?.model, logicalCPUs: cpus().length, totalMemoryMiB: totalmem() / 1024 ** 2,
      display: desktopSize, sessionType: process.env.XDG_SESSION_TYPE,
      virtualDisplay: process.env.WUU_LINUX_CUA_VIRTUAL_DISPLAY === '1', hardwareAcceleration: false,
      gpuFeatures: app.getGPUFeatureStatus(), evidenceRecorder: 'ffmpeg x11grab 10 fps, concurrent',
      processScope: 'Electron host and its Chromium children; separate native input helper and watcher. Excludes fixture, compositor, X server, and recorder.' },
    budgets: { mainEventLoopP95Ms: 100, mainEventLoopMaxMs: 1000, previewUIFrameP95Ms: 100,
      previewUIFrameMaxMs: 1000, minimumVideoFPS: 5, maximumVideoFrameGapMs: 1000,
      nativeInputP95Ms: 500, nativeInputMaxMs: 1500, observeAndInputP95Ms: 1000,
      electronRssGrowthMiB: 96, helperRssGrowthMiB: 48 },
    phases: [], cycles: [],
  };
  results.performance = performanceReport;
  const measure = async (name: string, durationMs: number, visible: boolean) => {
    await video.executeJavaScript(startRendererProbe);
    const lag = monitorEventLoopDelay({ resolution: 10 }); lag.enable();
    const samples = [await resourceSample()];
    const nativeInputMs: number[] = [], observeAndInputMs: number[] = [], hostRendererReplyMs: number[] = [];
    performanceReport.inProgress = { name, requestedDurationMs: durationMs, samples, nativeInputMs, observeAndInputMs, hostRendererReplyMs };
    let expectedClicks = (await fixtureRequest('state')).clicks;
    const started = performance.now();
    let nextInput = started, nextSample = started + 1000;
    try {
      while (performance.now() - started < durationMs) {
        if (performance.now() >= nextInput) {
          const begin = performance.now();
          const resolved = await computer({ action: 'list_apps' });
          assert.ok(!resolved.isError && resolved.structuredContent.resolved_app.id === canonicalTarget, 'Host pre-dispatch target resolution');
          const observation = await observe(false);
          const inputResolution = await computer({ action: 'list_apps' });
          assert.ok(!inputResolution.isError && inputResolution.structuredContent.resolved_app.id === canonicalTarget);
          const inputStarted = performance.now();
          const response = await computer({ action: 'click', snapshot_id: observation.snapshot_id,
            coordinate_space: 'screenshot', x: 130 * observation.screenshot.width / observation.screenshot.window_width,
            y: 180 * observation.screenshot.height / observation.screenshot.window_height, foreground_policy: 'allow' });
          assert.ok(!response.isError, JSON.stringify(response));
          expectedClicks++;
          await waitFor('native click while sampling preview', async () => (await fixtureRequest('state')).clicks === expectedClicks, 1500);
          nativeInputMs.push(performance.now() - inputStarted);
          observeAndInputMs.push(performance.now() - begin);
          nextInput += 3000;
        }
        await new Promise(resolve => setTimeout(resolve, Math.max(1, nextSample - performance.now())));
        const rendererStarted = performance.now();
        await main.webContents.executeJavaScript('performance.now()');
        hostRendererReplyMs.push(performance.now() - rendererStarted);
        samples.push(await resourceSample());
        nextSample += 1000;
      }
    } finally { lag.disable(); }
    const renderer = await video.executeJavaScript(stopRendererProbe);
    const cpu = (key: 'electron' | 'helpers') => samples.slice(1).map((sample, index) => {
      const previous = samples[index]!;
      return sample[key].reduce((total, entry) => {
        const before = previous[key].find(value => value.pid === entry.pid);
        assert.ok(before, `${name}: process population must remain stable during sampling`);
        return total + 100_000 * (entry.cpuSeconds - before.cpuSeconds) / (sample.atMs - previous.atMs);
      }, 0);
    });
    const rssGrowth = (key: 'electronRssMiB' | 'helperRssMiB') =>
      percentile(samples.slice(-5).map(value => value[key]), .5) - percentile(samples.slice(1, 6).map(value => value[key]), .5);
    const phase = { name, requestedDurationMs: durationMs, elapsedMs: performance.now() - started,
      eventLoopMs: { p95: lag.percentile(95) / 1e6, max: lag.max / 1e6 },
      video: { fps: renderer.frames.length * 1000 / renderer.elapsedMs,
        frameGapMs: renderer.frames.length ? summarize([...renderer.frames, renderer.finalFrameGapMs]) : null,
        liveTracks: renderer.liveTracks, settings: renderer.settings },
      previewUIFrameMs: renderer.ui.length ? summarize(renderer.ui) : null,
      nativeInputMs: summarize(nativeInputMs), observeAndInputMs: summarize(observeAndInputMs), hostRendererReplyMs: summarize(hostRendererReplyMs),
      cpuPercent: { electron: summarize(cpu('electron')), helpers: summarize(cpu('helpers')), unit: '100% = one logical CPU' },
      rssGrowthMiB: { electron: rssGrowth('electronRssMiB'), helpers: rssGrowth('helperRssMiB') }, samples };
    performanceReport.phases.push(phase);
    delete performanceReport.inProgress;
    writeFileSync(join(output, 'performance.json'), JSON.stringify(performanceReport, null, 2));
    const budget = performanceReport.budgets;
    assert.ok(phase.eventLoopMs.p95 < budget.mainEventLoopP95Ms && phase.eventLoopMs.max < budget.mainEventLoopMaxMs, `${name}: Electron main loop stalled`);
    assert.ok(phase.nativeInputMs.p95 < budget.nativeInputP95Ms && phase.nativeInputMs.max < budget.nativeInputMaxMs, `${name}: native input was unresponsive`);
    assert.ok(phase.hostRendererReplyMs.p95 < 100 && phase.hostRendererReplyMs.max < 1000, `${name}: host renderer became unresponsive`);
    assert.ok(phase.observeAndInputMs.p95 < budget.observeAndInputP95Ms, `${name}: observation/input round-trip exceeded budget`);
    assert.ok(phase.rssGrowthMiB.electron < budget.electronRssGrowthMiB && phase.rssGrowthMiB.helpers < budget.helperRssGrowthMiB, `${name}: retained memory grew beyond budget`);
    if (visible) {
      assert.equal(renderer.liveTracks, 1, `${name}: capture pipeline must have exactly one live video track`);
      assert.ok(renderer.settings[0].width <= 1280 && renderer.settings[0].height <= 1280 && renderer.settings[0].frameRate <= 15.5, `${name}: bounded capture dimensions/rate`);
      assert.ok(phase.video.fps >= budget.minimumVideoFPS && phase.video.frameGapMs!.max < budget.maximumVideoFrameGapMs, `${name}: live video stalled`);
      assert.ok(phase.previewUIFrameMs && phase.previewUIFrameMs.p95 < budget.previewUIFrameP95Ms && phase.previewUIFrameMs.max < budget.previewUIFrameMaxMs, `${name}: preview renderer stalled`);
    } else {
      assert.equal(renderer.liveTracks, 0, 'Hidden preview must not retain a capture track');
      assert.equal(renderer.frames.length, 0, 'Hidden preview must not continue delivering frames');
    }
  };
  await fixtureRequest('animate', true);
  await measure('live-changing-target', 30_000, true);
  const largeSize = { width: 2560, height: 1600 };
  assert.deepEqual(await fixtureRequest('size', largeSize), [largeSize.width, largeSize.height]);
  // Electron's X11 client-side shadows can extend beyond BrowserWindow's
  // logical size. Preserve the measured margins instead of assuming no frame.
  const largeNativeSize = {
    width: largeSize.width + initialObservation.screenshot.window_width - fixtureSize.width,
    height: largeSize.height + initialObservation.screenshot.window_height - fixtureSize.height,
  };
  await waitFor('large target observation uses new geometry', async () => {
    const snapshot = await observe(false);
    performanceReport.largeTarget = { requested: largeSize, expectedNative: largeNativeSize, observed: snapshot.screenshot };
    return snapshot.screenshot.window_width === largeNativeSize.width && snapshot.screenshot.window_height === largeNativeSize.height;
  });
  await waitFor('large target preview remains bounded', () => video.executeJavaScript(`(() => {
    const video = document.querySelector('video');
    return video.videoWidth===1280 && Math.abs(video.videoWidth/video.videoHeight-${largeNativeSize.width / largeNativeSize.height})<.01;
  })()`));
  await measure('large-changing-target', 15_000, true);
  const beforeCycles = await resourceSample();
  const liveProcessIDs = app.getAppMetrics().map(metric => metric.pid).sort((a, b) => a - b);
  for (let cycle = 0; cycle < 3; cycle++) {
    await video.executeJavaScript("window.previousCaptureTracks = document.querySelector('video').srcObject.getTracks()");
    const hideStarted = performance.now();
    coordinator.setActiveThread('other-thread');
    await waitFor('hidden preview has no retained live tracks', () => video.executeJavaScript("document.querySelector('video').srcObject===null && window.previousCaptureTracks.every(track=>track.readyState==='ended')"));
    const hideMs = performance.now() - hideStarted;
    if (cycle === 0) await measure('preview-hidden-target-active', 8_000, false);
    const resumeStarted = performance.now();
    coordinator.setActiveThread(activity.thread_id);
    await waitFor('preview capture resumes after repeated hide', () => video.executeJavaScript("document.querySelector('video').srcObject?.getVideoTracks().filter(track=>track.readyState==='live').length===1 && document.querySelector('video').readyState>=2"));
    const resumeMs = performance.now() - resumeStarted;
    performanceReport.cycles.push({ hideMs, resumeMs });
    assert.ok(hideMs < 1500 && resumeMs < 3000, 'Preview track cleanup/recovery must stay responsive');
    assert.deepEqual(app.getAppMetrics().map(metric => metric.pid).sort((a, b) => a - b), liveProcessIDs, 'Repeated preview visibility changes must not accumulate Electron processes');
  }
  await measure('resumed-changing-target', 10_000, true);
  await fixtureRequest('animate', false);
  const afterCycles = await resourceSample();
  performanceReport.retainedGrowthAfterCyclesMiB = { electron: afterCycles.electronRssMiB - beforeCycles.electronRssMiB, helpers: afterCycles.helperRssMiB - beforeCycles.helperRssMiB };
  assert.ok(performanceReport.retainedGrowthAfterCyclesMiB.electron < performanceReport.budgets.electronRssGrowthMiB
    && performanceReport.retainedGrowthAfterCyclesMiB.helpers < performanceReport.budgets.helperRssGrowthMiB, 'Repeated visibility changes must not accumulate retained memory');
  writeFileSync(join(output, 'performance.json'), JSON.stringify(performanceReport, null, 2));
  checks.push('sustained live/hidden/resumed performance budgets: main/renderer responsiveness, frame cadence, native input latency, CPU/RSS trends, bounded capture and repeated track cleanup');

  const wayland = startHelper({ ...process.env, XDG_SESSION_TYPE: 'wayland', WAYLAND_DISPLAY: 'wayland-acceptance' });
  const unsupported = await wayland.request('tools/call', { name: 'computer', arguments: { action: 'permission_status' } });
  assert.equal(unsupported.structuredContent.supported, false, 'Wayland does not silently use XWayland/global input');
  wayland.child.stdin.end();
  rpc.child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method: 'notifications/wuu/control_revoked', params: { control_epoch: 'linux-cua-acceptance' } })}\n`);
  const revoked = await computer({ action: 'type_text', text: 'revoked input', foreground_policy: 'allow' });
  assert.equal(revoked.isError, true, 'Revoked control cannot send further native input');
  assert.equal(revoked.structuredContent.error_code, 'cancelled');
  assert.equal((await fixtureRequest('state')).text, 'Linux CUA work');
  checks.push('moved-target and consumed snapshots, mismatched PID, revoked control and Wayland are rejected; normalized coordinates work');

  await fixtureRequest('close');
  await waitFor('target exit cleans up preview', () => BrowserWindow.getAllWindows().filter(win => win !== main).every(win => !win.isVisible()), 15_000);
  checks.push('target closure stops capture and dismisses PiP');
  await coordinator.shutdown(); coordinator = undefined;
  const watcherPID = helperPIDs.find(pid => pid !== rpc.child.pid)!;
  await waitFor('preview shutdown reaps the native watcher', async () => {
    try { await readFile(`/proc/${watcherPID}/stat`, 'utf8'); return false; }
    catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return true; throw error; }
  });
  main.destroy();
  results.status = 'passed';
}

async function finish(error?: unknown): Promise<void> {
  if (error) { results.status = 'failed'; results.error = String(error); console.error(error);
    try {
      results.windows = await Promise.all(BrowserWindow.getAllWindows().map(async win => ({
        title: win.getTitle(), visible: win.isVisible(), focused: win.isFocused(), bounds: win.getBounds(),
        views: await Promise.all(win.contentView.children.filter(view => view instanceof WebContentsView).map(async view => {
          const contents = (view as WebContentsView).webContents;
          return { url: contents.getURL(), state: await contents.executeJavaScript("({text:document.body.innerText,video:(()=>{const v=document.querySelector('video');return v&&{readyState:v.readyState,width:v.videoWidth,height:v.videoHeight,currentTime:v.currentTime,hidden:v.hidden,tracks:v.srcObject?.getTracks().map(t=>({kind:t.kind,state:t.readyState}))}})()})").catch(String) };
        })),
      })));
      if (process.env.DISPLAY) await capture('failure.png');
    } catch { /* Preserve the original failure when display capture is unavailable. */ }
  }
  await coordinator?.shutdown().catch(() => undefined);
  for (const child of children) if (child.exitCode == null) child.kill('SIGTERM');
  if (recorder && recorder.exitCode == null) {
    recorder.stdin?.write('q\n');
    await Promise.race([once(recorder, 'exit'), new Promise(resolve => setTimeout(resolve, 5000))]);
    if (recorder.exitCode == null) recorder.kill('SIGTERM');
  }
  mkdirSync(output, { recursive: true });
  try { execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-i', join(output, 'linux-cua-pip.mp4'), '-filter_complex', '[0:v]fps=6,scale=1008:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse', join(output, 'linux-cua-pip.gif')], { timeout: 60_000 }); } catch (captureError) { console.error('GIF conversion:', captureError); if (!error) error = captureError; }
  if (error) { results.status = 'failed'; results.error = String(error); }
  if (results.performance) writeFileSync(join(output, 'performance.json'), JSON.stringify(results.performance, null, 2));
  writeFileSync(join(output, 'results.json'), JSON.stringify(results, null, 2));
  console.log(`linux-cua-e2e: ${error ? 'FAIL' : 'PASS'} (${checks.length} checks; ${output})`);
  app.exit(error ? 1 : 0);
}
app.whenReady().then(fixtureMode ? fixtureMain : acceptance).then(() => { if (!fixtureMode) return finish(); }).catch(error => { if (fixtureMode) { console.error(error); app.exit(1); } else return finish(error); });
