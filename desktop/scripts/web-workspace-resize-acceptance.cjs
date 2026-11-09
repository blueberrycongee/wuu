// Behavioral and composed-pixel evidence. Video/input sampling makes this a
// separate run from the performance comparison, not another FPS measurement.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

module.exports = async function accept(h) {
  const { win, output, report, evaluate, until, settle, click, setSidebars, nativeInput, geometry, capture, delay, origin, fixtureTabs } = h;
  const checks = report.acceptance = [];
  await setSidebars(true, true);
  const draft = 'Retained conversation draft wraps at the current width after returning from the website. '.repeat(6);
  const setDraft = value => evaluate(value => {
    const input = document.querySelector('[data-main-conversation-composer] textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }, value);
  await setDraft(draft);
  await click(`.sidebar-ribbon-main button[data-ribbon-id="${fixtureTabs.animated.id}"]`);
  let view;
  await until(() => {
    view = win.contentView.children.find(v => v.webContents?.getURL() === origin + '/animated' && v.getVisible());
    return view && !view.webContents.isLoading();
  }, 'animated native page');
  const wc = view.webContents;
  if (process.env.WUU_RESIZE_CHROME_ONLY === '1') return require('./web-workspace-chrome-acceptance.cjs')(h, view);
  // Switching already loaded ribbon tabs must not hide the newly selected
  // page again while React updates the shared browser chrome.
  const switchPages = new Map();
  for (const kind of ['static', 'animated']) {
    await click(`.sidebar-ribbon-main button[data-ribbon-id="${fixtureTabs[kind].id}"]`);
    await until(() => {
      const page = win.contentView.children.find(v => v.webContents?.getURL() === origin + '/' + kind && v.getVisible());
      if (!page || page.webContents.isLoading()) return false;
      switchPages.set(kind, page); return true;
    }, 'warm switch page ' + kind);
  }
  const switches = report.tabSwitches = [];
  let switching;
  const restores = [];
  for (const [kind, page] of switchPages) {
    await page.webContents.executeJavaScript(`document.querySelector('#draft').value='retained-${kind}';void 0;`);
    const setVisible = page.setVisible;
    page.setVisible = function (visible) {
      const result = setVisible.call(this, visible);
      switching?.events.push({ kind, visible, at: performance.now(), ...(visible ? {} : { stack: new Error().stack }) });
      return result;
    };
    restores.push(() => { page.setVisible = setVisible; });
  }
  const recording = spawn('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-framerate', '60', '-video_size', '1920x1200', '-i', process.env.DISPLAY, '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '18', '-progress', 'pipe:1', '-y', path.join(output, 'ribbon-tab-switch.mp4')], { stdio: ['ignore', 'pipe', 'pipe'] });
  let recordingError = '';
  recording.stderr.on('data', bytes => { recordingError += bytes; });
  const recordingDone = new Promise((resolve, reject) => {
    recording.once('error', reject);
    recording.once('close', code => code === 0 || code === 255 ? resolve() : reject(new Error(recordingError)));
  });
  try {
    await new Promise((resolve, reject) => { recording.stdout.once('data', resolve); recording.once('error', reject); });
    for (const [width, height, zoom, theme] of [[1440, 900, 1, 'light'], [600, 700, 1, 'light'], [1280, 800, 1.25, 'dark']]) {
      win.setBounds({ x: 30, y: 30, width, height }); win.webContents.setZoomFactor(zoom);
      await evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
      await settle();
      for (let cycle = 0; cycle < 8; cycle++) {
        const kind = cycle % 2 ? 'animated' : 'static', target = switchPages.get(kind);
        switching = { kind, cycle, width, height, zoom, theme, events: [] };
        const point = await evaluate(id => {
          const r = document.querySelector(`[data-ribbon-id="${id}"]`).getBoundingClientRect();
          return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
        }, fixtureTabs[kind].id);
        const outer = win.getContentBounds();
        await nativeInput(['mousemove', String(Math.round(outer.x + point.x * zoom)), String(Math.round(outer.y + point.y * zoom)), 'click', '1']);
        await until(() => evaluate(id => document.querySelector(`[data-ribbon-id="${id}"]`)?.getAttribute('aria-current') === 'page', fixtureTabs[kind].id), 'ribbon switch selects ' + kind);
        switching.railExpanded = await evaluate(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded === 'true');
        if (switching.railExpanded) {
          await until(() => evaluate(() => { const image = document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-backdrop'); return image?.complete && image.naturalWidth > 100; }), 'expanded rail preserves a decoded page backdrop');
          switching.hoverBounds = target.getBounds();
          assert.equal(target.getVisible(), false, 'Expanded rail occludes the native child beneath its page snapshot');
        }
        switching.leftRailAt = performance.now();
        await nativeInput(['mousemove', String(Math.round(outer.x + width - 40)), String(Math.round(outer.y + 90 * zoom))]);
        await until(() => target.getVisible(), 'leaving the rail restores selected ' + kind);
        if (switching.hoverBounds) assert.deepEqual(target.getBounds(), switching.hoverBounds, 'Rail collapse does not resize the page');
        await settle();
        switching.targetVisibility = switching.events.filter(event => event.kind === kind).map(event => event.visible);
        switching.contentsID = target.webContents.id;
        switching.draft = await target.webContents.executeJavaScript('document.querySelector("#draft").value');
        assert.equal(switching.draft, 'retained-' + kind);
        switches.push(switching); switching = undefined;
      }
    }

  } finally {
    switching = undefined;
    for (const restore of restores) restore();
    recording.kill('SIGINT'); await recordingDone;
    report.artifacts.push('ribbon-tab-switch.mp4');
    fs.writeFileSync(path.join(output, 'tab-switches.json'), JSON.stringify(switches, null, 2));
    win.webContents.setZoomFactor(1);
    await evaluate(() => { document.documentElement.dataset.theme = 'light'; });
    await settle();
  }
  for (const sample of switches) {
    const settled = sample.events.filter(event => event.kind === sample.kind && (!sample.railExpanded || event.at >= sample.leftRailAt));
    assert.ok(!settled.some(event => !event.visible), `Selected ${sample.kind} stays visible after intentional rail occlusion ends: ${JSON.stringify(settled)}`);
  }
  checks.push({ name: 'warm-ribbon-switch-keeps-selected-page-visible', switches: switches.length });
  if (process.env.WUU_RESIZE_SWITCH_ONLY === '1') return;
  // A busy shell must not leave the live guest at the previous window size.
  // Pause only the shell's JavaScript to make the missing IPC round trip
  // deterministic; the independent webpage must keep painting and resizing.
  for (const zoom of [1, 1.25]) {
    win.setBounds({ x: 30, y: 30, width: 1280, height: 800 });
    win.webContents.setZoomFactor(zoom); await settle();
    const before = view.getBounds(), [width, height] = win.getContentSize();
    const dom = await evaluate(() => {
      const r = document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-host').getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    });
    for (const key of ['x', 'y', 'width', 'height']) assert.ok(Math.abs(before[key] - Math.round(dom[key] * zoom)) <= 2, `Zoomed native ${key} matches the visible host`);
    const right = width - before.x - before.width, bottom = height - before.y - before.height;
    await win.webContents.debugger.sendCommand('Debugger.enable');
    const paused = new Promise(resolve => {
      const listener = (_event, method) => {
        if (method !== 'Debugger.paused') return;
        win.webContents.debugger.removeListener('message', listener); resolve();
      };
      win.webContents.debugger.on('message', listener);
    });
    await win.webContents.debugger.sendCommand('Debugger.pause'); await paused;
    try {
      for (const [nextWidth, nextHeight] of [[1440, 900], [900, 700], [1280, 800]]) {
        const ticks = await wc.executeJavaScript('window.ticks');
        win.setBounds({ x: 30 + 1440 - nextWidth, y: 30 + 900 - nextHeight, width: nextWidth, height: nextHeight });
        await new Promise(resolve => setImmediate(resolve));
        const [contentWidth, contentHeight] = win.getContentSize();
        const expected = { x: before.x, y: before.y, width: contentWidth - before.x - right, height: contentHeight - before.y - bottom };
        const actual = view.getBounds();
        checks.push({ name: 'resize-with-paused-shell', zoom, expected, actual });
        fs.writeFileSync(path.join(output, 'paused-shell.json'), JSON.stringify(checks, null, 2));
        for (const key of ['x', 'y', 'width', 'height']) assert.ok(Math.abs(actual[key] - expected[key]) <= 2, `Paused shell still fills native ${key}: ${JSON.stringify({ zoom, expected, actual })}`);
        assert.equal(view.getVisible(), true);
        await until(async () => await wc.executeJavaScript('window.ticks') > ticks, 'Guest remains live while shell is paused');
      }
    } finally {
      await win.webContents.debugger.sendCommand('Debugger.resume');
      await win.webContents.debugger.sendCommand('Debugger.disable');
    }
    await settle();
  }
  win.webContents.setZoomFactor(1); await settle();
  if (process.env.WUU_RESIZE_GEOMETRY_ONLY === '1') return;
  // Local extension surfaces keep absolute geometry rather than stretching
  // with the owner. Exercise the shared host API without a renderer observer.
  await evaluate(origin => window.wuu.webWorkspace.open({ id: 'resize-local-rectangle', url: origin + '/static' }), origin);
  let local;
  const rectangle = { x: 100, y: 200, width: 320, height: 240 };
  try {
    await evaluate(bounds => window.wuu.webWorkspace.reportBounds('resize-local-rectangle', bounds), rectangle);
    await until(() => {
      local = win.contentView.children.find(v => v.webContents?.getURL() === origin + '/static');
      return local && !local.webContents.isLoading();
    }, 'local surface navigation');
    win.setSize(1440, 900);
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(local.getBounds(), rectangle, 'A local surface keeps its own rectangle');
    checks.push({ name: 'local-surface-rectangle', actual: local.getBounds() });
  } finally { await evaluate(() => window.wuu.webWorkspace.close('resize-local-rectangle')); }
  await settle();
  await click('.web-workspace-actions button[aria-haspopup="dialog"]');
  await until(() => !view.getVisible(), 'Options menu occludes the native page');
  win.setSize(960, 700); await settle();
  assert.equal(view.getVisible(), false, 'Native resize must not reveal an occluded page');
  await click('.web-workspace-actions button[aria-haspopup="dialog"]');
  await until(() => view.getVisible(), 'Closing the menu restores the same page');
  checks.push({ name: 'overlay-resize-restoration', geometry: await geometry(view) });
  await wc.executeJavaScript(`document.querySelector('#draft').value='retained page state';scrollTo(0,200);window.resizeInputs=0;addEventListener('keydown',e=>{if(e.key==='F8'){window.resizeInputs++;document.querySelector('#click').textContent='Live input: '+window.resizeInputs}})`);
  const video = spawn('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-framerate', '20', '-video_size', '1920x1200', '-i', process.env.DISPLAY, '-c:v', 'libx264', '-preset', 'ultrafast', '-crf', '28', '-y', path.join(output, 'native-x11-resize.mp4')], { stdio: ['ignore', 'ignore', 'pipe'] });
  let videoError = '';
  video.stderr.on('data', bytes => { videoError += bytes; });
  const videoDone = new Promise((resolve, reject) => {
    video.once('error', reject);
    video.once('close', code => code === 0 || code === 255 ? resolve() : reject(new Error(videoError)));
  });
  try {
    for (const [name, horizontal, vertical] of [
      ['right', 1, 0], ['left', -1, 0], ['bottom', 0, 1], ['top', 0, -1],
      ['bottom-right', 1, 1], ['bottom-left', -1, 1], ['top-right', 1, -1], ['top-left', -1, -1],
    ]) {
      win.setBounds({ x: 30, y: 30, width: 1440, height: 900 });
      await settle();
      const before = await wc.executeJavaScript('({ticks:window.ticks,inputs:window.resizeInputs,scroll:scrollY})');
      const bounds = [];
      const resized = () => bounds.push(win.getBounds());
      win.on('resize', resized);
      // Start Openbox's Alt-resize inside the live guest, away from Electron's
      // draggable title strip, which can consume the top-edge pointer grab.
      const outer = win.getContentBounds(), guest = view.getBounds();
      const start = {
        x: Math.round(outer.x + (horizontal < 0 ? guest.x + 20 : horizontal > 0 ? guest.x + guest.width - 20 : outer.width / 2)),
        y: Math.round(outer.y + (vertical < 0 ? guest.y + 20 : vertical > 0 ? guest.y + guest.height - 20 : outer.height / 2)),
      };
      const args = ['mousemove', '--sync', String(start.x), String(start.y), 'keydown', 'Alt_L', 'mousedown', '3'];
      for (let i = 1; i <= 96; i++) {
        const fraction = i <= 48 ? i / 48 : (96 - i) / 48;
        args.push('mousemove', String(Math.round(start.x - horizontal * 1000 * fraction)), String(Math.round(start.y - vertical * 340 * fraction)), 'sleep', '0.016');
      }
      args.push('mouseup', '3', 'keyup', 'Alt_L');
      let hiddenSamples = 0;
      const visibility = setInterval(() => { if (!view.getVisible()) hiddenSamples++; }, 25);
      const input = setInterval(() => {
        wc.sendInputEvent({ type: 'keyDown', keyCode: 'F8' });
        wc.sendInputEvent({ type: 'keyUp', keyCode: 'F8' });
      }, 250);
      try { await nativeInput(args); }
      finally {
        clearInterval(input); clearInterval(visibility); win.removeListener('resize', resized);
        await nativeInput(['mouseup', '3', 'keyup', 'Alt_L']);
      }
      await settle();
      const after = await wc.executeJavaScript('({ticks:window.ticks,inputs:window.resizeInputs,draft:document.querySelector("#draft").value,scroll:scrollY})');
      const check = { name, input: 'X11/Openbox native pointer Alt+button3, not macOS', start, bounds, before, after, hiddenSamples };
      checks.push(check);
      const range = key => Math.max(...bounds.map(b => b[key])) - Math.min(...bounds.map(b => b[key]));
      assert.ok(bounds.length > 10, name + ': native WM delivered resize events');
      if (horizontal) assert.ok(range('width') > 850, name + ': full horizontal range');
      if (vertical) assert.ok(range('height') > 280, name + ': full vertical range');
      assert.equal(hiddenSamples, 0, name + ': page stayed visible');
      assert.equal(after.draft, 'retained page state');
      assert.ok(after.inputs > before.inputs, name + ': input handled during drag');
      assert.ok(after.ticks > before.ticks + 10, name + ': animation continued');
      assert.ok(after.scroll > 0, name + ': reading position retained');
      check.geometry = await geometry(view);
      await capture('native-' + name);
    }
  } finally { video.kill('SIGINT'); await videoDone; }
  report.artifacts.push('native-x11-resize.mp4');

  for (const [theme, size] of [['light', 15], ['dark', 20]]) {
    await evaluate(({theme, size}) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty('--conversation-message-font-size', size + 'px');
    }, {theme, size});
    for (const [width, height] of [[1440, 900], [440, 560]]) {
      win.setBounds({ x: 30, y: 30, width, height }); await settle();
      checks.push({ name: `${theme}-${size}-${width}x${height}`, geometry: await geometry(view) });
      await capture(`layout-${theme}-${size}-${width}x${height}`);
    }
  }
  // Main has no application minimum. Exercise the actual WM floor separately;
  // a viewport smaller than its chrome cannot be a usable-page performance case.
  win.setSize(1, 1); await delay(200);
  const minimum = { name: 'operating-system-size-floor', size: win.getSize(), content: win.getContentSize() };
  win.setBounds({ x: 30, y: 30, width: 440, height: 560 }); await settle();
  minimum.restored = await geometry(view);
  assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'), 'retained page state');
  checks.push(minimum);
  await click('[data-wuu-component="conversations-section"]');
  await settle();
  const composerGeometry = () => evaluate(() => {
    const input = document.querySelector('[data-main-conversation-composer] textarea');
    return { value: input.value, height: input.offsetHeight, scrollHeight: input.scrollHeight };
  });
  const revealed = await composerGeometry();
  assert.equal(revealed.value, draft, 'Conversation draft survives native website resizing');
  await setDraft(''); await settle(); await setDraft(draft); await settle();
  const edited = await composerGeometry();
  assert.ok(Math.abs(revealed.height - edited.height) <= 2, 'Revealed draft fits current width/font before editing');
  checks.push({ name: 'retained-conversation-draft', revealed, edited });
  await capture('restored-conversation');

  // Public fixed-content website supplements the local fixtures; no account is used.
  win.setBounds({ x: 30, y: 30, width: 1280, height: 800 });
  await click(`.sidebar-ribbon-main button[data-ribbon-id="${fixtureTabs.static.id}"]`);
  await until(() => {
    view = win.contentView.children.find(v => v.webContents?.getURL() === origin + '/static' && v.getVisible());
    return view && !view.webContents.isLoading();
  }, 'public navigation target');
  await evaluate(group => window.wuu.webWorkspace.command(group, 'navigate', 'https://example.com/'), fixtureTabs.static.group);
  const publicResult = { name: 'public-example.com', status: 'unavailable' };
  try {
    await until(() => win.contentView.children.some(v => v.webContents?.getURL() === 'https://example.com/' && v.getVisible() && !v.webContents.isLoading()), 'public page', 15000);
    view = win.contentView.children.find(v => v.webContents?.getURL() === 'https://example.com/' && v.getVisible());
    for (const width of [960, 600, 1280]) { win.setSize(width, 800); await settle(); await geometry(view); }
    publicResult.status = 'passed'; publicResult.title = view.webContents.getTitle();
    await capture('public-example');
  } catch (error) { publicResult.error = String(error); }
  checks.push(publicResult);
  if (process.env.WUU_RESIZE_LOADING === '1') {
    const loading = await require('./web-workspace-loading-probe.cjs')({ view, output, capture, delay,
      navigate: url => evaluate(({ group, url }) => window.wuu.webWorkspace.command(group, 'navigate', url), { group: fixtureTabs.static.group, url }),
    });
    checks.push({ name: 'notion-loading-diagnostic', samples: loading.samples.length, error: loading.error });
  }
  await setSidebars(true, true);
  await delay(500);
  fs.writeFileSync(path.join(output, 'acceptance.json'), JSON.stringify(checks, null, 2));
};
