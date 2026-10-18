// End-to-end Linux X11 input and live PiP, using only a disposable local app.
import assert from 'node:assert/strict';
import { spawn, execFileSync, type ChildProcess } from 'node:child_process';
import { once } from 'node:events';
import { mkdirSync, writeFileSync } from 'node:fs';
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
const fixtureHTML = `<!doctype html><html><style>
*{box-sizing:border-box}body{margin:0;font:18px system-ui;background:#f4f7fa;color:#18334f}header{height:120px;background:#123456;color:white;padding:24px}h1{margin:0;font-size:24px}p{margin:8px 0}#marker{position:absolute;right:24px;top:24px;width:80px;height:70px;background:#ffff00}main{padding:30px 40px}button{width:190px;height:60px;font:inherit;background:#dcecff;border:1px solid #5b80aa;border-radius:12px}label{display:block;margin-top:24px}input[type=text]{display:block;width:540px;height:44px;font:inherit;padding:8px;border:1px solid #5b80aa}input[type=range]{position:absolute;left:60px;top:335px;width:480px;height:30px}#scroll{position:absolute;left:40px;top:390px;width:540px;height:180px;overflow:auto;border:1px solid #8195aa;padding:12px}#scroll article{height:650px;background:linear-gradient(#dcecff,#c9f0df)}footer{position:absolute;left:40px;bottom:25px;color:#48705b}body.dark{background:#172334;color:#eff4ff}
</style><body><header><h1>Linux CUA acceptance</h1><p>Disposable app · native X11 input</p><span id="marker"></span></header><main><button id="click">Click count: 0</button><label>Native text entry<input id="text" type="text"></label><input id="range" type="range" min="0" max="100" value="0"><div id="scroll"><article>Scroll this panel<br><br>Every input is delivered by the production Linux helper.<br><br>No external sites or accounts.</article></div><footer id="status">Ready for native input</footer></main><script>
window.state={clicks:0,text:'',range:0,scroll:0,keys:[]};
const button=document.getElementById('click'),field=document.getElementById('text'),slider=document.getElementById('range'),panel=document.getElementById('scroll');
button.onclick=()=>{state.clicks++;button.textContent='Click count: '+state.clicks;document.getElementById('status').textContent='Click received';};
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
  const target = new BrowserWindow({ x: 40, y: 40, width: 620, height: 680, frame: false, show: false, webPreferences: { sandbox: true, contextIsolation: true } });
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
      else if (command === 'theme') await target.webContents.executeJavaScript(`document.body.classList.toggle('dark',${JSON.stringify(value === 'dark')})`);
      else if (command === 'move') target.setPosition(Number(value), 40);
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
  async function observe() {
    const response = await computer({ action: 'observe' });
    assert.ok(!response.isError, JSON.stringify(response));
    const details = response.structuredContent;
    assert.equal(details.process_id, identity.processID);
    assert.equal(details.window_id, identity.windowID);
    assert.ok(details.snapshot_id);
    const image = response.content.find((content: any) => content.type === 'image');
    assert.ok(image?.data, 'Observation returns actual target pixels');
    writeFileSync(join(output, 'target-observation.png'), Buffer.from(image.data, 'base64'));
    return details;
  }
  async function input(action: string, args: Record<string, unknown>) {
    const observation = await observe();
    const response = await computer({ action, snapshot_id: observation.snapshot_id, coordinate_space: 'screenshot', foreground_policy: 'allow', ...args });
    assert.ok(!response.isError, JSON.stringify(response));
    return observation;
  }
  const initialObservation = await observe();
  results.screenshot = initialObservation.screenshot;
  const canonicalTarget = initialObservation.app;
  assert.match(canonicalTarget, /^linux:\d+:\d+:\d+$/, 'Resolved target includes the process lifetime');
  appID = canonicalTarget;
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
  coordinator = new ObservationCoordinator({ mainWindow: () => main } as unknown as WindowRegistry, undefined, (next, key, sink) => factory(next, key, sink, () => ({ x: 830, y: 370, width: 480, height: 390 })));
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
  pip.setSize(260, 240);
  await waitFor('dark preview chrome', () => overlay.executeJavaScript("document.documentElement.classList.contains('dark')"));
  await capture('03-dark-small-large-text.png'); checks.push('dark appearance, small preview, large UI font');

  await fixtureRequest('minimize');
  await waitFor('minimized target stops capture', () => video.executeJavaScript("document.querySelector('video').srcObject===null"));
  await fixtureRequest('restore'); main.focus();
  await waitFor('restored target resumes capture', () => video.executeJavaScript("document.querySelector('video').srcObject?.getVideoTracks().some(t=>t.readyState==='live')"));
  await waitFor('restored target pixels', async () => isColor(await pixel(.02, .02), [18, 52, 86]));
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
  await capture('05-reopened-preview.png'); checks.push('conversation visibility, capture track teardown, explicit dismiss/reopen; native helper actions continue while preview hidden or dismissed');

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
  writeFileSync(join(output, 'results.json'), JSON.stringify(results, null, 2));
  console.log(`linux-cua-e2e: ${error ? 'FAIL' : 'PASS'} (${checks.length} checks; ${output})`);
  app.exit(error ? 1 : 0);
}
app.whenReady().then(fixtureMode ? fixtureMain : acceptance).then(() => { if (!fixtureMode) return finish(); }).catch(error => { if (fixtureMode) { console.error(error); app.exit(1); } else return finish(error); });
