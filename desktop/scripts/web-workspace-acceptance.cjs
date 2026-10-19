// Real native website journeys, run by workbench-acceptance-e2e.cjs under Xvfb.
// Local contract fixtures and anonymous public-page observations use the real app.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, session, webContents } = require('electron');
const { prepareMediaFixture, serveMediaFixture, runBrowserMediaAcceptance } = require('./browser-media-acceptance.cjs');

async function createWebsiteFixture(home) {
  const requests = [];
  const controls = { offline: true };
  const media = prepareMediaFixture(home);
  const downloadBytes = Buffer.from('Synthetic Wuu browser download.\n'.repeat(128));
  const server = http.createServer((request, response) => {
    let body = '';
    request.on('data', chunk => { body += chunk; });
    request.on('end', () => {
      const url = new URL(request.url, 'http://fixture.test');
      requests.push({ method: request.method, path: url.pathname, body, cookie: request.headers.cookie || '' });
      if (serveMediaFixture(request, response, url, media)) return;
      if (url.pathname === '/offline' && controls.offline) { response.destroy(); return; }
      if (url.pathname === '/loading-hold') {
        response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
        response.write('<!doctype html><title>Controlled loading fixture</title><body style="font:24px system-ui;padding:48px;background:#edf4fa;color:#17354a"><h1>Loading fixture</h1><p>This local response stays open until Stop is clicked.</p>');
        controls.finishLoading = () => response.end('</body>');
        const timer = setTimeout(controls.finishLoading, 20000);
        response.once('close', () => { clearTimeout(timer); controls.finishLoading = null; });
        return;
      }
      if (url.pathname === '/download') {
        response.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="fixture.txt"', 'Content-Length': downloadBytes.length });
        response.end(downloadBytes); return;
      }
      if (url.pathname === '/slow-download') {
        const chunk = Buffer.alloc(65536, 'w'); let count = 0;
        response.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="slow-fixture.bin"', 'Content-Length': chunk.length * 160 });
        const timer = setInterval(() => { response.write(chunk); if (++count === 160) { clearInterval(timer); response.end(); } }, 100);
        response.once('close', () => clearInterval(timer)); return;
      }
      if (url.pathname === '/cookie') {
        response.writeHead(302, { Location: '/alpha', 'Set-Cookie': 'wuu_acceptance=synthetic-session; Path=/; Max-Age=86400; SameSite=Lax' });
        response.end(); return;
      }
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
      response.end(`<!doctype html><meta charset="utf-8"><title>Local fixture ${url.pathname}</title>
<style>body{font:18px system-ui;margin:0;background:#edf4fa;color:#17354a}main{padding:42px;max-width:850px}h1{font-weight:500}input,button,a{font:inherit;margin:8px 12px 8px 0;padding:10px}a{display:inline-block}input{width:330px}pre{white-space:pre-wrap;padding:18px;background:white;border-radius:12px}button{cursor:pointer}#live-marker{display:inline-block;width:8px;height:8px;margin-right:10px;background:#328875;animation:fixture-pulse 1s linear infinite alternate}@keyframes fixture-pulse{to{transform:translateX(6px);opacity:.35}}</style>
<main><p><span id="live-marker"></span>Wuu · isolated local website fixture</p><h1>${url.pathname}</h1><p>No external service, account, or model is contacted.</p><p>Find marker: fixture one, fixture two, fixture three.</p>
<label>Retained page draft <input id="draft" placeholder="Type a retained website draft"></label>
<p><a id="next" href="/next">Next page</a><a id="cookie" href="/cookie">Set synthetic session cookie</a></p>
<button id="blank">Open blank popup + postMessage</button>
<form action="/posted" method="post" target="_blank"><input type="hidden" name="fixture" value="post-preserved"><button id="post">Open POST popup</button></form>
<p><a id="download" href="/download">Download synthetic text</a><a id="slow-download" href="/slow-download">Slow synthetic download</a></p>
<button id="geo">Request location (deny)</button><button id="camera">Request synthetic camera (deny)</button>
<pre id="state"></pre><p id="popup-result"></p><p id="permission-result"></p>
<script>
window.fixtureFrames=0;const animate=()=>{window.fixtureFrames++;requestAnimationFrame(animate)};requestAnimationFrame(animate);
const state=()=>document.querySelector('#state').textContent=JSON.stringify({cookie:document.cookie,path:location.pathname,visibility:document.visibilityState,wuu:typeof window.wuu,require:typeof require,process:typeof process},null,2);state();document.addEventListener('visibilitychange',state);
document.querySelector('#blank').onclick=()=>{const popup=window.open('about:blank','fixture-blank','width=700,height=460'); if(popup){popup.document.write('<title>Synthetic popup</title><h1>Fixture popup</h1><p>Opener messaging retained.</p>');popup.opener.postMessage('fixture-opener-roundtrip',location.origin);}};
window.addEventListener('message',event=>{if(event.data==='fixture-opener-roundtrip')document.querySelector('#popup-result').textContent=event.data;});
document.querySelector('#geo').onclick=()=>{document.querySelector('#permission-result').textContent='pending';navigator.geolocation.getCurrentPosition(()=>document.querySelector('#permission-result').textContent='unexpected-grant',()=>document.querySelector('#permission-result').textContent='denied');};
document.querySelector('#camera').onclick=async()=>{document.querySelector('#permission-result').textContent='pending';try{const stream=await navigator.mediaDevices.getUserMedia({video:true});stream.getTracks().forEach(track=>track.stop());document.querySelector('#permission-result').textContent='unexpected-grant';}catch{document.querySelector('#permission-result').textContent='denied';}};
</script></main>`);
    });
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const plugin = path.join(home, 'plugins/acceptance-webpage');
  fs.mkdirSync(plugin, { recursive: true });
  fs.writeFileSync(path.join(plugin, 'plugin.json'), JSON.stringify({ schemaVersion: 1, id: 'acceptance-webpage', name: 'Acceptance Webpage', version: '0.1.0',
    description: 'Authored local HTTP fixture, no external service or permission.', desktop: { entry: 'desktop.js' },
    contributes: { navigation: [{ id: 'website-entry', view: 'website', title: 'Fixture Website' }] } }));
  fs.writeFileSync(path.join(plugin, 'desktop.js'), `export function activate(api) { api.registerViewType({ id: 'website', title: 'Fixture Website', defaultRegion: 'primary', persistence: 'durable', render: () => api.react.createElement(api.ui.WebPage, { url: ${JSON.stringify(origin + '/extension')}, title: 'Fixture Website' }) }); }\n`);
  return { origin, requests, controls, downloadBytes, media, close: () => server.close() };
}

async function runWebsiteAcceptance(h, fixture) {
  const { win, report, output, started, save, evaluate, waitFor, settle, delay, point, fill: fillShell, key, pick, test, goConversations, goSettings, appearance, plugins, conversations } = h;
  const origin = fixture.origin;
  const addTabButton = '.sidebar-ribbon-add';
  const webTabs = '[data-wuu-component="website-navigation-item"]';
  const tabButton = id => `${webTabs}[data-ribbon-id="${id}"]`;
  const siteIDs = new Map();
  const ribbonGroup = 'ribbon-browser';
  const readSite = () => evaluate(() => window.wuu.webWorkspace.initializeTabs([]));
  let independentFixtures = false;
  const webRoot = '[data-wuu-component="web-workspace"]';
  const address = `${webRoot} .web-workspace-toolbar .workspace-browser-url-input`;
  const startInput = '.web-workspace-stage:not([hidden]) .web-workspace-start-search input';
  const toolbar = name => `${webRoot} button[aria-label="${name}"]`;
  const ribbon = name => `.sidebar-ribbon-main button[aria-label="${name}"]`;
  const prefs = () => evaluate(() => JSON.parse(localStorage.getItem('wuu.navigation-ribbon.v1')));
  const views = () => win.contentView.children.filter(view => view.webContents && !view.webContents.isDestroyed() && view.webContents.getURL().startsWith(origin));
  const contents = pathname => webContents.getAllWebContents().find(wc => !wc.isDestroyed() && wc.getURL() === origin + pathname);
  const nativeView = pathname => views().find(view => view.getVisible() && view.webContents.getURL() === origin + pathname) || views().find(view => view.webContents.getURL() === origin + pathname);
  const chromeView = kind => win.contentView.children.find(view => view.webContents && !view.webContents.isDestroyed() && view.webContents.getURL() === `wuu-chrome://ui/chrome-overlay.html?surface=${kind}`);
  const evaluateContents = async (wc, fn, value, stage = 'native chrome evaluation') => {
    const result = await wc.executeJavaScript(`(async () => {
      try { return { value: await (${fn})(${JSON.stringify(value)}) }; }
      catch (error) { return { error: String(error.stack || error) }; }
    })()`);
    if (result.error) throw new Error(`${stage} (contents ${wc.id}): ${result.error}`);
    return result.value;
  };
  const evaluateRibbon = (fn, value) => chromeView('ribbon')?.getVisible() ? evaluateContents(chromeView('ribbon').webContents, fn, value) : evaluate(fn, value);
  // Toolbar popovers reuse React in a clipped native child below a View.
  // Walk the native hierarchy rather than querying its shell placeholder.
  function nativeLayers(parent = win.contentView, x = 0, y = 0, top) {
    return parent.children.flatMap(view => {
      if (!view.getVisible()) return [];
      const b = view.getBounds(), layer = { view, x: x + b.x, y: y + b.y, top: top || view };
      return [...(view.webContents && !view.webContents.isDestroyed() ? [layer] : []), ...nativeLayers(view, layer.x, layer.y, layer.top)];
    });
  }
  async function nativePopover() {
    for (const layer of nativeLayers()) {
      const wc = layer.view.webContents;
      if (wc.getURL() !== 'about:blank') continue;
      if (await wc.executeJavaScript('document.body?.hasAttribute("data-browser-popover")').catch(() => false)) return layer;
    }
  }
  async function evaluatePopover(fn, value) {
    const layer = await nativePopover();
    return layer ? evaluateContents(layer.view.webContents, fn, value) : undefined;
  }
  const waitPopover = (fn, value, label = String(fn)) => until(() => evaluatePopover(`async argument => Boolean(await (${fn})(argument))`, value), label);
  async function selectNativeInput(wc, selector) {
    const selected = () => evaluateContents(wc, selector => {
      const input = document.activeElement;
      return document.hasFocus() && input?.matches(selector)
        && input.selectionStart === 0 && input.selectionEnd === input.value.length;
    }, selector);
    // Empty and already-selected fields satisfy the selection predicate before
    // Ctrl+A arrives. Do not queue a redundant shortcut that can select newly
    // typed characters; otherwise acknowledge its release and selection first.
    if (await selected()) return;
    let released = false;
    const observe = (_event, input) => { if (input.type === 'keyUp' && input.code === 'KeyA') released = true; };
    wc.on('before-input-event', observe);
    try {
      xinput(['key', '--clearmodifiers', 'ctrl+a']);
      await until(() => released && selected(), 'Native select-all is delivered and selects the focused input');
    } finally { wc.removeListener('before-input-event', observe); }
  }
  async function fill(target, value) {
    const query = typeof target === 'string' ? target : target.selector;
    if (!await evaluatePopover(selector => Boolean(document.querySelector(selector)), query)) return fillShell(target, value);
    const wc = (await nativePopover()).view.webContents;
    await click(target, 'focus-native-popover-input', false);
    await until(() => wc.isFocused(), 'Native popover owns keyboard focus before typing');
    await waitPopover(selector => document.hasFocus() && document.activeElement?.matches(selector), query);
    const nativeInputEvents = [];
    let inputPhase = 'before-select-all';
    const originalSelectAll = wc.selectAll;
    wc.selectAll = function (...args) {
      nativeInputEvents.push({ at: Date.now() - started, method: 'selectAll', inputPhase });
      return Reflect.apply(originalSelectAll, this, args);
    };
    const observeNativeInput = (_event, input) => { if (nativeInputEvents.length < 100) nativeInputEvents.push({ at: Date.now() - started, ...input }); };
    wc.on('before-input-event', observeNativeInput);
    await evaluateContents(wc, selector => {
      const events = [], input = document.querySelector(selector);
      const snapshot = (type, event) => {
        if (events.length >= 160) return;
        events.push({ type, at: performance.now(), trusted: event?.isTrusted, key: event?.key, data: event?.data,
          inputType: event?.inputType, ctrl: event?.ctrlKey, alt: event?.altKey, meta: event?.metaKey, shift: event?.shiftKey, defaultPrevented: event?.defaultPrevented,
          focused: document.hasFocus(), active: document.activeElement === input,
          connected: input?.isConnected, value: input?.value, selection: [input?.selectionStart, input?.selectionEnd] });
      };
      const types = ['keydown', 'keyup', 'beforeinput', 'input', 'change', 'selectionchange', 'focusin', 'focusout'];
      const observe = event => {
        snapshot(event.type, event);
        if (event.type === 'input') requestAnimationFrame(() => snapshot('input-next-frame'));
      };
      types.forEach(type => document.addEventListener(type, observe, true));
      window.__wuuAcceptancePopoverInput = { events, snapshot, stop: () => types.forEach(type => document.removeEventListener(type, observe, true)) };
      snapshot('before-native-select-all');
    }, query);
    try {
      await selectNativeInput(wc, query);
      // Let each real key reach the controlled cross-document input before
      // sending the next. No dropped character is retried or silently repaired.
      let entered = '';
      for (const character of value) {
        inputPhase = `typing:${character}`;
        await evaluateContents(wc, character => window.__wuuAcceptancePopoverInput.snapshot(`before-native-character:${character}`), character);
        xinput(['type', '--clearmodifiers', '--delay', '8', character]);
        entered += character;
        await waitPopover(({ selector, value }) => document.querySelector(selector)?.value === value, { selector: query, value: entered });
        inputPhase = `acknowledged:${entered}`;
        await evaluateContents(wc, () => window.__wuuAcceptancePopoverInput.snapshot('native-character-acknowledged'));
      }
      if (!value) { xinput(['key', 'BackSpace']); await waitPopover(selector => document.querySelector(selector)?.value === '', query); }
    } catch (error) {
      report.nativePopoverInputFailure = { selector: query, expected: value, focusedContentsID: webContents.getFocusedWebContents()?.id,
        input: await evaluateContents(wc, selector => { const input = document.querySelector(selector); return { focused: document.hasFocus(), active: document.activeElement?.outerHTML.slice(0, 300), value: input?.value, selection: [input?.selectionStart, input?.selectionEnd] }; }, query) };
      save(); throw error;
    } finally {
      wc.selectAll = originalSelectAll;
      wc.removeListener('before-input-event', observeNativeInput);
      report.nativePopoverInputTraces ||= [];
      report.nativePopoverInputTraces.push({ selector: query, expected: value, contentsID: wc.id, nativeInputEvents,
        events: await evaluateContents(wc, () => {
          const trace = window.__wuuAcceptancePopoverInput;
          trace?.snapshot('finished'); trace?.stop(); delete window.__wuuAcceptancePopoverInput;
          return trace?.events;
        }).catch(error => ({ error: String(error) })) });
      save();
    }
  }
  async function chromePoint(target) {
    const query = typeof target === 'string' ? { selector: target } : target;
    const popover = await nativePopover();
    if (popover) {
      const p = await evaluateContents(popover.view.webContents, ({ selector, text }) => {
        const node = [...document.querySelectorAll(selector)].find(node => (text === undefined || (node.getAttribute('aria-label') || node.textContent || '').trim() === text) && node.getBoundingClientRect().width > 0);
        if (!node) return null;
        const r = node.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
        const hit = document.elementFromPoint(x, y);
        if (!node.contains(hit) || node.disabled) throw new Error('Native popover target is occluded or disabled: ' + JSON.stringify({
          selector, rect: r.toJSON(), x, y, disabled: node.disabled, viewport: { width: innerWidth, height: innerHeight },
          hit: hit && { tag: hit.tagName, className: hit.getAttribute('class'), component: hit.getAttribute('data-wuu-component') } }));
        return { x, y };
      }, query, 'chromePoint native popover');
      if (p) {
        const z = popover.view.webContents.getZoomFactor(), ownerZoom = win.webContents.getZoomFactor();
        const screen = { x: popover.x + p.x * z, y: popover.y + p.y * z }, clip = popover.top.getBounds();
        assert.ok(screen.x >= clip.x && screen.x < clip.x + clip.width && screen.y >= clip.y && screen.y < clip.y + clip.height, 'Native popover control is inside its clipping viewport');
        return { x: screen.x / ownerZoom, y: screen.y / ownerZoom, nativeChrome: 'popover' };
      }
    }
    for (const kind of ['account', 'ribbon']) {
      const view = chromeView(kind);
      if (!view?.getVisible()) continue;
      const p = await evaluateContents(view.webContents, ({ selector, text }) => {
        const node = [...document.querySelectorAll(selector)].find(node => (text === undefined || (node.getAttribute('aria-label') || node.textContent || '').trim() === text) && node.getBoundingClientRect().width > 0);
        if (!node) return null;
        const r = node.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
        const hit = document.elementFromPoint(x, y);
        if (!node.contains(hit) || node.disabled) throw new Error('Native chrome target is occluded or disabled: ' + JSON.stringify({
          selector, rect: r.toJSON(), x, y, disabled: node.disabled, viewport: { width: innerWidth, height: innerHeight },
          hit: hit && { tag: hit.tagName, className: hit.getAttribute('class'), component: hit.getAttribute('data-wuu-component') } }));
        return { x, y };
      }, query, `chromePoint native ${kind}`);
      if (p) { const b = view.getBounds(), z = view.webContents.getZoomFactor(), ownerZoom = win.webContents.getZoomFactor(); return { x: (b.x + p.x * z) / ownerZoom, y: (b.y + p.y * z) / ownerZoom, nativeChrome: kind }; }
    }
    return point(target);
  }
  async function liveChrome(kind, wc, label) {
    await until(() => chromeView(kind)?.getVisible(), `${label}: trusted native ${kind} painted`);
    const overlay = chromeView(kind), view = win.contentView.children.find(view => view.webContents === wc);
    assert.ok(view?.getVisible(), `${label}: the real website stays visible`);
    const bounds = view.getBounds(), ticks = await wc.executeJavaScript('window.fixtureFrames'), animation = await wc.executeJavaScript("getComputedStyle(document.querySelector('#live-marker')).transform");
    await delay(160);
    assert.ok(view.getVisible() && await wc.executeJavaScript('window.fixtureFrames') > ticks, `${label}: guest frames continue under native chrome`);
    assert.notEqual(await wc.executeJavaScript("getComputedStyle(document.querySelector('#live-marker')).transform"), animation, `${label}: visible fixture animation advances`);
    assert.deepEqual(view.getBounds(), bounds, `${label}: native chrome does not resize the page`);
    assert.ok(win.contentView.children.indexOf(overlay) > win.contentView.children.indexOf(view), `${label}: native chrome is stacked above the live website`);
    report.actions.push({ label: `${label}-live-native-chrome`, kind, contentsID: wc.id, bounds, overlayBounds: overlay.getBounds(), atMs: Date.now() - started }); save();
  }
  async function until(predicate, label, timeout = 12000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) { if (await predicate()) return; await delay(40); }
    throw new Error(`Timed out: ${label}`);
  }
  // Shell capturePage cannot see WebContentsView children. X11 captures the
  // actual composed native window, including website pixels and native dialogs.
  async function capture(label) {
    await settle();
    const bounds = win.getBounds();
    const taken = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-video_size', `${bounds.width}x${bounds.height}`, '-i', `${process.env.DISPLAY}+${bounds.x},${bounds.y}`, '-frames:v', '1', '-f', 'image2pipe', '-vcodec', 'png', 'pipe:1'], { timeout: 12000, maxBuffer: 24 * 1024 * 1024 });
    assert.equal(taken.status, 0, String(taken.stderr || taken.error));
    const image = nativeImage.createFromBuffer(taken.stdout);
    assert.ok(!image.isEmpty(), 'Actual native-window PNG is readable');
    const name = `${String(report.screenshots.length + 1).padStart(3, '0')}-web-${label.replace(/[^a-zA-Z0-9_-]+/g, '-').slice(0, 85)}.png`;
    fs.writeFileSync(path.join(output, name), taken.stdout);
    report.screenshots.push({ file: name, source: 'x11-whole-native-window', sha256: createHash('sha256').update(taken.stdout).digest('hex'), atMs: Date.now() - started, image: image.getSize(), window: bounds,
      nativeFocus: { ownerFocused: win.isFocused(), ownerFullscreen: win.isFullScreen(), focusedContentsID: webContents.getFocusedWebContents()?.id ?? null },
      nativeViews: win.contentView.children.filter(view => view.webContents && !view.webContents.isDestroyed()).map(view => ({ id: view.webContents.id, url: view.webContents.getURL(), visible: view.getVisible(), focused: view.webContents.isFocused(), bounds: view.getBounds(), zoom: view.webContents.getZoomFactor() })),
      visibleNativeLayers: nativeLayers().map(({ view, x, y, top }) => ({ id: view.webContents.id, url: view.webContents.getURL(), focused: view.webContents.isFocused(), x, y, bounds: view.getBounds(), clip: top.getBounds() })),
      activeElement: await evaluate(() => document.activeElement?.outerHTML.slice(0, 240)) });
    save(); await delay(160);
  }
  function xinput(args) {
    const result = spawnSync('xdotool', args, { encoding: 'utf8', timeout: 5000 });
    assert.equal(result.status, 0, result.stderr || String(result.error));
  }
  async function leaveRail(waitForCollapse = true) {
    const outer = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    xinput(['mousemove', String(Math.round(outer.x + Math.min(300, outer.width / zoom - 20) * zoom)), String(Math.round(outer.y + 90 * zoom))]);
    await settle();
    // Native chrome has a 120ms pointer-exit grace. Shell rAFs alone do not
    // acknowledge its dismissal; never replace this with Escape or forced blur.
    if (waitForCollapse) await until(async () => !chromeView('ribbon')?.getVisible()
      && await evaluate(() => {
        const ribbon = document.querySelector('.sidebar-ribbon');
        if (!ribbon || ribbon.dataset.expanded === 'true') return false;
        // The marker clears before the closing width transition. A shell-only
        // hit test cannot see the live native page covering a moving target.
        ribbon.getBoundingClientRect();
        return ribbon.getAnimations().every(animation => !animation.pending && animation.playState !== 'running');
      }), 'Native hover rail acknowledges pointer leave and finishes motion', 4000);
  }
  async function hoverTab(id) {
    const p = await chromePoint(tabButton(id)), outer = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    xinput(['mousemove', String(Math.round(outer.x + 26 * zoom)), String(Math.round(outer.y + p.y * zoom))]);
    await waitFor(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded === 'true');
    await until(() => chromeView('ribbon')?.getVisible(), 'Native expanded ribbon paints');
    await until(async () => {
      const shell = await evaluate(() => {
        const ribbon = document.querySelector('.sidebar-ribbon');
        const width = ribbon.getBoundingClientRect().width;
        return { width, expanded: ribbon.dataset.expanded === 'true',
          settled: ribbon.getAnimations().every(animation => !animation.pending && animation.playState !== 'running') };
      });
      const view = chromeView('ribbon');
      if (!shell.expanded || !shell.settled || !view?.getVisible() || Math.abs(view.getBounds().width - shell.width * zoom) > 1) return false;
      // Native bounds can acknowledge reveal before its renderer processes the
      // resize. Require the same viewport and target hit ownership before use.
      return evaluateContents(view.webContents, ({ selector, width }) => {
        const node = document.querySelector(selector);
        if (!node || node.disabled || Math.abs(innerWidth - width) > 1) return false;
        const rect = node.getBoundingClientRect();
        return rect.width > 0 && rect.height > 0 && node.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
      }, { selector: tabButton(id), width: view.getBounds().width / view.webContents.getZoomFactor() }, 'hoverTab native reveal readiness');
    }, 'Native hover reveal reaches the click targets');
  }
  async function finishRailInteraction(wc, label) {
    await leaveRail(false);
    if (await evaluate(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded === 'true')) {
      // Escape/Tab can correctly retain keyboard focus in the expanded rail.
      // A real click back into browser chrome ends that interaction explicitly.
      await focusAddress(`${label}-return-to-browser-chrome`);
    }
    await waitFor(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded !== 'true');
    if (wc) await until(() => !wc.isDestroyed() && win.contentView.children.some(view => view.webContents === wc && view.getVisible()), `${label}: leaving rail restores the same native page`);
  }
  async function focusAddress(label) {
    const p = await evaluate(selector => {
      // Retained plugin pages keep hidden toolbars in the shell DOM. The real
      // native click must target the currently presented address, not the first.
      const node = [...document.querySelectorAll(selector)].find(node => {
        const r = node.getBoundingClientRect();
        return r.width > 0 && r.height > 0 && getComputedStyle(node).visibility === 'visible' && !node.closest('[hidden],[inert]');
      });
      if (!node) throw new Error('No visible address field is mounted');
      const r = node.getBoundingClientRect();
      // The expanded rail can cover the center of a narrow address field.
      // Click only its genuinely exposed pixels, keeping strict hit testing.
      for (const x of [r.x + r.width / 2, r.right - 2, r.left + 2]) {
        const y = r.y + r.height / 2;
        if (node.contains(document.elementFromPoint(x, y))) return { x, y };
      }
      throw new Error('No exposed address field point is clickable');
    }, address);
    const bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    xinput(['mousemove', String(Math.round(bounds.x + p.x * zoom)), String(Math.round(bounds.y + p.y * zoom)), 'click', '1']);
    await waitFor(selector => document.activeElement?.matches(selector), address);
    await until(() => win.webContents.isFocused(), 'Real address click transfers native keyboard focus to shell');
    report.actions.push({ label, input: 'native-x11', point: p, atMs: Date.now() - started }); save();
  }
  async function typeAddress(value, label) {
    // Observe only this runner's synthetic local URL entry. Keep native typing
    // and the exact-value assertion unchanged; never repair dropped characters.
    const diagnostic = value.startsWith(origin + '/');
    const nativeEvents = [];
    const observeInput = (_event, input) => {
      if (nativeEvents.length < 160) nativeEvents.push({ type: 'before-input', at: Date.now() - started,
        key: input.key, inputType: input.type, control: input.control, alt: input.alt, meta: input.meta, shift: input.shift });
    };
    const observeFocus = () => {
      if (nativeEvents.length < 160) nativeEvents.push({ type: 'shell-focus', at: Date.now() - started,
        focused: win.webContents.isFocused(), focusedContentsID: webContents.getFocusedWebContents()?.id });
    };
    try {
      if (diagnostic) {
        win.webContents.on('before-input-event', observeInput);
        win.webContents.on('focus', observeFocus);
        win.webContents.on('blur', observeFocus);
        await evaluate(({ address, startInput }) => {
          const events = [], cleanup = [];
          let stopped = false;
          const field = selector => {
            const input = document.activeElement?.matches(selector) ? document.activeElement
              : [...document.querySelectorAll(selector)].find(node => {
                const r = node.getBoundingClientRect();
                return r.width > 0 && r.height > 0 && getComputedStyle(node).visibility === 'visible'
                  && !node.closest('[hidden],[inert]');
              });
            return { value: input?.value, selection: [input?.selectionStart, input?.selectionEnd], active: document.activeElement === input };
          };
          const snapshot = (type, event, details) => {
            if (stopped || events.length >= 240) return;
            events.push({ type, at: performance.now(), key: event?.key, data: event?.data, inputType: event?.inputType,
              trusted: event?.isTrusted, focused: document.hasFocus(),
              active: document.activeElement?.tagName + '.' + document.activeElement?.className,
              address: field(address), search: field(startInput), details });
          };
          const observe = event => {
            if (!['selectionchange', 'focusin', 'focusout'].includes(event.type)
              && !event.target?.matches?.(address + ',' + startInput)) return;
            snapshot(event.type, event);
            if (event.type === 'input') queueMicrotask(() => snapshot('after-input-microtask'));
          };
          const types = ['keydown', 'keyup', 'beforeinput', 'input', 'selectionchange', 'focusin', 'focusout'];
          types.forEach(type => document.addEventListener(type, observe, true));
          cleanup.push(() => types.forEach(type => document.removeEventListener(type, observe, true)));
          window.__wuuAcceptanceAddressInput = { snapshot, stop: () => {
            snapshot('finished'); stopped = true; cleanup.forEach(stop => stop()); return events;
          } };
          const api = window.wuu.webWorkspace;
          cleanup.push(api.onState(state => snapshot('tab-state', undefined,
            { id: state.id, tab: state.activeTabId, loading: state.loading })));
          cleanup.push(api.onFocusAddress((id, newTabId) => snapshot('address-focus-request', undefined, { id, newTabId })));
          cleanup.push(api.onPopoverDismiss(token => snapshot('popover-dismiss', undefined, { token })));
          snapshot('before-address-focus');
        }, { address, startInput });
      }
      await focusAddress(label);
      await selectNativeInput(win.webContents, address);
      if (diagnostic) await evaluate(() => window.__wuuAcceptanceAddressInput.snapshot('native-selection-ready'));
      xinput(['type', '--clearmodifiers', '--delay', '8', value]);
      await waitFor(({ selector, value }) => document.activeElement?.matches(selector) && document.activeElement.value === value, { selector: address, value });
    } finally {
      if (diagnostic) {
        win.webContents.removeListener('before-input-event', observeInput);
        win.webContents.removeListener('focus', observeFocus);
        win.webContents.removeListener('blur', observeFocus);
        const events = await evaluate(() => {
          const trace = window.__wuuAcceptanceAddressInput;
          delete window.__wuuAcceptanceAddressInput;
          return trace?.stop();
        }).catch(error => ({ error: String(error) }));
        report.nativeAddressInputTraces ||= [];
        report.nativeAddressInputTraces.push({ label, expected: value, nativeEvents, events });
        save();
      }
    }
  }
  async function click(target, label, evidence = true, retainHover = false) {
    if (evidence) await capture(`before-${label}`);
    // Bundled navigation can finish mounting after a renderer reload. Measure
    // immediately before the real click, not before the slower X11 capture.
    const p = await chromePoint(target);
    const inRail = p.nativeChrome === 'ribbon' || await evaluate(p => Boolean(document.elementFromPoint(p.x, p.y)?.closest('.sidebar-ribbon')), p);
    const bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    if (inRail && !p.nativeChrome) {
      const input = { x: Math.round(bounds.x + p.x * zoom) - bounds.x, y: Math.round(bounds.y + p.y * zoom) - bounds.y };
      const coveringPages = views().filter(view => {
        const rect = view.getBounds();
        return view.getVisible() && input.x >= rect.x && input.x < rect.x + rect.width
          && input.y >= rect.y && input.y < rect.y + rect.height;
      }).map(view => ({ contentsID: view.webContents.id, bounds: view.getBounds() }));
      if (coveringPages.length) {
        report.actions.push({ label: `web-${label}-occluded-shell-target`, point: p, input, coveringPages });
        save();
      }
      assert.equal(coveringPages.length, 0, `Shell ribbon target must not be covered by a live native page: ${JSON.stringify({ point: p, input, coveringPages })}`);
    }
    xinput(['mousemove', String(Math.round(bounds.x + p.x * zoom)), String(Math.round(bounds.y + p.y * zoom)), 'click', '1']);
    report.actions.push({ label: `web-${label}`, input: 'native-x11', atMs: Date.now() - started, point: p });
    await settle();
    if (evidence) await capture(`after-${label}`);
    if (inRail && !retainHover) {
      // An open account menu intentionally retains the rail until dismissal.
      const menuRetains = chromeView('account')?.getVisible() || await evaluate(() => [...document.querySelectorAll('.sidebar-account-menu[role=menu]')].some(node => node.getBoundingClientRect().width > 0 && getComputedStyle(node).visibility !== 'hidden'));
      await leaveRail(!menuRetains);
    }
  }
  async function mascotGaze() {
    const selector = '.web-workspace-stage:not([hidden]) .web-workspace-start-mascot';
    await waitFor(selector => document.querySelector(selector)?.querySelector('.web-workspace-start-mascot-eyes'), selector);
    await evaluate(selector => {
      const svg = document.querySelector(selector), eyes = svg.querySelector('.web-workspace-start-mascot-eyes');
      const rect = svg.getBoundingClientRect, computed = window.getComputedStyle;
      const probe = { svg, eyes, reads: 0, mutations: 0 };
      svg.getBoundingClientRect = function (...args) { probe.reads++; return rect.apply(this, args); };
      window.getComputedStyle = function (element, ...args) { if (element === svg || svg.contains(element)) probe.reads++; return computed.call(this, element, ...args); };
      const observer = new MutationObserver(records => { probe.mutations += records.length; });
      observer.observe(svg, { subtree: true, attributes: true, attributeFilter: ['transform', 'd', 'style'] });
      probe.cleanup = () => { observer.disconnect(); delete svg.getBoundingClientRect; window.getComputedStyle = computed; delete window.__wuuMascotAcceptance; };
      window.__wuuMascotAcceptance = probe;
    }, selector);
    const states = [];
    for (const direction of [-1, 1]) {
      const target = await evaluate(({ selector, direction }) => {
        const r = document.querySelector(selector).getBoundingClientRect();
        return { x: Math.max(64, Math.min(innerWidth - 24, r.x + r.width / 2 + direction * 180)), y: r.y + r.height / 2 };
      }, { selector, direction });
      const bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
      xinput(['mousemove', String(Math.round(bounds.x + target.x * zoom)), String(Math.round(bounds.y + target.y * zoom))]);
      await waitFor(direction => {
        const eyes = window.__wuuMascotAcceptance.eyes;
        return eyes.transform.baseVal.consolidate()?.matrix.e * direction > 0;
      }, direction);
      await capture(direction < 0 ? 'mascot-pointer-left' : 'mascot-pointer-right');
      states.push(await evaluate(() => {
        const { eyes } = window.__wuuMascotAcceptance, r = eyes.getBoundingClientRect();
        return { transform: eyes.getAttribute('transform'), centerX: r.x + r.width / 2 };
      }));
    }
    assert.ok(states[1].centerX > states[0].centerX + 0.5, 'Actual eye geometry follows left and right native pointer input');
    await evaluate(() => { const p = window.__wuuMascotAcceptance; p.reads = p.mutations = 0; });
    await delay(400);
    const idle = await evaluate(() => ({ reads: window.__wuuMascotAcceptance.reads, mutations: window.__wuuMascotAcceptance.mutations }));
    assert.deepEqual(idle, { reads: 0, mutations: 0 }, 'Settled mascot does not poll geometry/styles or mutate eye artwork while idle');
    return { states, idle, idleMs: 400 };
  }
  async function mascotUnmount() {
    await waitFor(() => !window.__wuuMascotAcceptance.svg.isConnected);
    await delay(80);
    await evaluate(() => { const p = window.__wuuMascotAcceptance; p.reads = p.mutations = 0; });
    const bounds = win.getContentBounds();
    xinput(['mousemove', String(bounds.x + 100), String(bounds.y + 60)]);
    await delay(120);
    const result = await evaluate(() => {
      const p = window.__wuuMascotAcceptance;
      return { detached: !p.svg.isConnected, reads: p.reads, mutations: p.mutations };
    });
    assert.deepEqual(result, { detached: true, reads: 0, mutations: 0 }, 'Navigating away releases mascot pointer work');
    await evaluate(() => window.__wuuMascotAcceptance.cleanup());
    return result;
  }
  async function pageClick(wc, selector, label) {
    const view = win.contentView.children.find(view => view.webContents === wc);
    await until(() => view?.getVisible() && !wc.isDestroyed(), 'Native website is actually visible before page input');
    const p = await wc.executeJavaScript(`(async () => { const node=document.querySelector(${JSON.stringify(selector)}); node.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'}); await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))); const r=node.getBoundingClientRect(); return {x:r.x+r.width/2,y:r.y+r.height/2}; })()`);
    const outer = win.getContentBounds(), inner = view.getBounds(), zoom = wc.getZoomFactor();
    await capture(`before-${label}`);
    xinput(['mousemove', String(Math.round(outer.x + inner.x + p.x * zoom)), String(Math.round(outer.y + inner.y + p.y * zoom)), 'click', '1']);
    report.actions.push({ label, input: 'native-x11-website', atMs: Date.now() - started });
    await delay(80);
  }
  async function hidden(label) {
    await until(() => views().every(view => !view.getVisible()), `${label}: all native surfaces hidden`);
    return Promise.all(views().map(async view => ({ url: view.webContents.getURL(), visible: view.getVisible(), visibility: await view.webContents.executeJavaScript('document.visibilityState') })));
  }
  async function verticalTabGeometry(expanded = false) {
    await waitFor(expanded => {
      const bar = document.querySelector('.sidebar-ribbon');
      return (bar?.dataset.expanded === 'true') === expanded
        && bar.getAnimations({ subtree: true }).every(animation => animation.playState !== 'running');
    }, expanded, 'Ribbon reaches its requested interaction state');
    const expectedWidth = await evaluate(() => document.querySelector('.sidebar-ribbon').getBoundingClientRect().width);
    await until(() => expanded
      ? chromeView('ribbon')?.getVisible() && Math.abs(chromeView('ribbon').getBounds().width - expectedWidth * win.webContents.getZoomFactor()) <= 1
      : !chromeView('ribbon')?.getVisible(), 'Native ribbon finishes its reveal or dismissal');
    const geometry = await evaluateRibbon(() => {
      const bar = document.querySelector('.sidebar-ribbon'), r = bar.getBoundingClientRect();
      const rows = [...bar.querySelectorAll('.sidebar-ribbon-tab-row')].map(row => {
        const button = row.querySelector('[data-ribbon-id]'), title = button.querySelector('.sidebar-ribbon-label');
        const close = row.querySelector('.sidebar-ribbon-close'), tr = title.getBoundingClientRect();
        return { id: button.dataset.ribbonId, web: button.dataset.wuuComponent === 'website-navigation-item',
          title: title.textContent, titleWidth: tr.width, titleVisible: tr.width > 0 && getComputedStyle(title).visibility === 'visible',
          close: Boolean(close), active: button.getAttribute('aria-current') === 'page',
          closeVisible: close ? close.getBoundingClientRect().width > 0 && getComputedStyle(close).visibility === 'visible' && Number(getComputedStyle(close).opacity) > 0.95 : false };
      });
      return { width: r.width, expanded: bar.dataset.expanded === 'true', viewportWidth: innerWidth, rows };
    });
    assert.equal(geometry.expanded, expanded, `Rail expansion follows the current interaction: ${JSON.stringify(geometry)}`);
    assert.ok(Math.abs(geometry.width - expectedWidth) <= 1, `Native ribbon matches the shell interaction bounds: ${JSON.stringify(geometry)}`);
    assert.ok(geometry.rows.every(row => row.title && (expanded ? row.titleVisible && row.titleWidth >= 20 : !row.titleVisible)), 'Destination labels appear only in the expanded overlay');
    assert.ok(geometry.rows.some(row => row.id === 'conversations' && !row.close), 'Built-in Chat has no close button');
    assert.ok(geometry.rows.every(row => row.close === row.web), 'Only real website tabs have direct close controls');
    assert.ok(geometry.rows.filter(row => row.web && row.active).every(row => row.closeVisible === expanded), 'Active web tab exposes its direct close control only in the expanded rail');
    return geometry;
  }
  async function traceLoadedTabSwitches() {
    const traces = [], originalBounds = win.getBounds();
    let current = await openSite('Fixture Alpha', '/alpha');
    try {
      for (const [pathname, width, mode] of [['/beta', 1280, 'keyboard'], ['/alpha', 1280, 'keyboard'], ['/beta', 960, 'keyboard'], ['/alpha', 960, 'pointer']]) {
        if (win.getContentSize()[0] !== width) { win.setContentSize(width, 900); await settle(); }
        await pageClick(current, '#draft', `focus-before-${mode}-tab-switch`);
        const incoming = views().find(view => view.webContents.getURL() === origin + pathname);
        assert.ok(incoming && !incoming.getVisible(), 'Switch target is an existing hidden native page');
        const trace = { pathname, width, mode, incomingID: incoming.webContents.id, calls: [], boundsIPC: [], frames: [], captures: [] };
        const capturePage = incoming.webContents.capturePage;
        incoming.webContents.capturePage = async function (...args) {
          const image = await capturePage.apply(this, args);
          trace.captures.push({ sha256: createHash('sha256').update(image.toPNG()).digest('hex'), pixels: image.getSize() });
          return image;
        };
        const restore = [() => { incoming.webContents.capturePage = capturePage; }], pending = [];
        for (const view of views()) for (const method of ['setVisible', 'setBounds']) {
          const original = view[method];
          view[method] = function (...args) {
            const result = original.apply(this, args);
            trace.calls.push({ contentsID: view.webContents.id, method, value: args[0], atMs: Date.now() - started, bounds: view.getBounds() });
            if (view === incoming && method === 'setVisible' && args[0] === true) pending.push(view.webContents.executeJavaScript(`new Promise(resolve => {
              const frames = []; const sample = () => { const r = document.querySelector('#draft').getBoundingClientRect();
                frames.push({ width: innerWidth, height: innerHeight, inputX: r.x, inputWidth: r.width });
                if (frames.length < 6) requestAnimationFrame(sample); else resolve(frames); }; requestAnimationFrame(sample);
            })`).then(frames => trace.frames.push(...frames)));
            return result;
          };
          restore.push(() => { view[method] = original; });
        }
        report.activeTabSwitchTrace = trace;
        try {
          if (mode === 'keyboard') xinput(['key', '--clearmodifiers', 'ctrl+Tab']);
          else {
            const before = win.contentView.children.find(view => view.webContents === current).getBounds();
            await click(tabButton(alphaID), 'pointer-switch-loaded-tab', false, true);
            await verticalTabGeometry(true);
            await liveChrome('ribbon', incoming.webContents, 'hovered incoming tab');
            assert.equal(incoming.webContents.getURL(), origin + pathname, 'Hovered selection presents the actual incoming page');
            assert.deepEqual(incoming.getBounds(), before, 'Expanding the rail does not resize the incoming native viewport');
            await capture('hover-expanded-incoming-native-tab');
            await leaveRail();
          }
          await until(() => incoming.getVisible(), 'Incoming native tab becomes visible');
          await Promise.race([Promise.all(pending), delay(3000).then(() => { throw new Error('Native switch frame sampling timed out'); })]);
          await delay(120);
          trace.settled = incoming.getBounds();
          const visibility = trace.calls.filter(call => call.contentsID === incoming.webContents.id && call.method === 'setVisible').map(call => call.value);
          if (mode === 'keyboard') {
            assert.deepEqual(visibility, [true], 'No-overlay native keyboard switch reveals the incoming view once without a hide/show flash');
            assert.ok(!trace.boundsIPC.some(event => event.id === ribbonGroup && (event.bounds === null || event.bounds?.occluded)), 'Tab identity change does not tear down unchanged host bounds');
          }
          assert.ok(trace.frames.length >= 6 && trace.frames.every(frame => Math.abs(frame.width - trace.settled.width / incoming.webContents.getZoomFactor()) <= 2),
            `Incoming page has current viewport width from its first sampled frames: ${JSON.stringify(trace)}`);
          assert.ok(trace.frames.every(frame => Math.abs(frame.inputX - trace.frames[0].inputX) <= 1), 'Page content does not shift horizontally across reveal frames');
          current = incoming.webContents;
        } finally {
          restore.forEach(restore => restore()); delete report.activeTabSwitchTrace;
          traces.push(trace); (report.tabSwitchTraces ||= []).push(trace); save();
        }
      }
    } finally { win.setBounds(originalBounds); await settle(); }
    return traces;
  }
  async function chromeGeometry() {
    const geometry = await evaluate(() => {
      const root = document.querySelector('.web-workspace-stage:not([hidden]) [data-wuu-component="web-workspace"]');
      const rect = selector => {
        const r = root.querySelector(selector).getBoundingClientRect();
        return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, centerY: r.top + r.height / 2 };
      };
      return {
        toolbar: rect('.web-workspace-toolbar'), navigation: rect('.workspace-browser-nav-cluster'),
        address: rect('.web-workspace-address'), input: rect('.workspace-browser-url-input'), actions: rect('.web-workspace-actions'),
        horizontalTabs: Boolean(root.querySelector('.web-workspace-tabstrip')),
        leftTabs: document.querySelectorAll('[data-wuu-component="website-navigation-item"]').length,
      };
    });
    const { toolbar, navigation, address: field, input, actions } = geometry;
    assert.ok(Math.max(navigation.centerY, field.centerY, actions.centerY) - Math.min(navigation.centerY, field.centerY, actions.centerY) <= 1,
      `Browser navigation, address and actions stay on one centerline: ${JSON.stringify(geometry)}`);
    assert.ok(navigation.left >= toolbar.left && navigation.right <= field.left && field.right <= actions.left && actions.right <= toolbar.right,
      `Browser controls remain ordered and inside the toolbar: ${JSON.stringify(geometry)}`);
    assert.ok(input.width >= 48, `The address remains editable at the supported narrow width: ${JSON.stringify(geometry)}`);
    assert.equal(geometry.horizontalTabs, false, 'Web pages have one left tab and no nested horizontal tab strip');
    assert.ok(geometry.leftTabs > 0, 'Live website tabs remain reachable from the ribbon');
    return geometry;
  }
  async function dragBrowserToolbar() {
    const before = win.getBounds(), outer = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    const drag = await evaluate(() => {
      const bar = document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-toolbar');
      const r = bar.getBoundingClientRect(), actions = bar.querySelector('.web-workspace-actions').getBoundingClientRect();
      const x = (actions.right + r.right) / 2, y = r.top + r.height / 2, hit = document.elementFromPoint(x, y);
      return { x, y, gap: r.right - actions.right, region: getComputedStyle(hit).getPropertyValue('-webkit-app-region'),
        safe: bar.contains(hit) && !hit.closest('button,input,form,a,[role="button"]') };
    });
    assert.ok(drag.safe && drag.gap >= 4 && drag.region === 'drag', `Toolbar padding is a safe native drag target: ${JSON.stringify(drag)}`);
    const x = Math.round(outer.x + drag.x * zoom), y = Math.round(outer.y + drag.y * zoom);
    try {
      await capture('before-toolbar-padding-drag');
      report.actions.push({ label: 'native-toolbar-drag-start', input: 'native-x11', before, point: { x, y }, drag, atMs: Date.now() - started }); save();
      xinput(['mousemove', String(x), String(y), 'mousedown', '1']); await delay(100);
      for (let step = 1; step <= 5; step++) {
        xinput(['mousemove', String(x + step * 10), String(y + step * 8)]); await delay(40);
      }
      await until(() => Math.abs(win.getBounds().x - before.x) >= 20 || Math.abs(win.getBounds().y - before.y) >= 20,
        'Dragging empty browser toolbar padding moves the actual native window', 4000);
      xinput(['mouseup', '1']); await settle();
      report.actions.push({ label: 'native-toolbar-drag', input: 'native-x11', before, after: win.getBounds(), atMs: Date.now() - started }); save();
      await capture('after-toolbar-padding-drag');
    } finally {
      xinput(['mouseup', '1']); await delay(40);
      xinput(['key', '--clearmodifiers', 'Escape']); await settle();
      win.setBounds(before);
      await until(() => ['x', 'y', 'width', 'height'].every(key => Math.abs(win.getBounds()[key] - before[key]) <= 1),
        'Native toolbar drag cleanup restores original window bounds');
    }
    await click(toolbar('Browser options'), 'options-remains-clickable-after-drag', false);
    await waitFor(() => document.querySelector('button[aria-label="Browser options"]')?.getAttribute('aria-expanded') === 'true');
    xinput(['key', '--clearmodifiers', 'Escape']);
    await waitFor(() => document.querySelector('button[aria-label="Browser options"]')?.getAttribute('aria-expanded') === 'false');
    await click(address, 'address-remains-editable-after-drag', false);
    await until(() => win.webContents.isFocused(), 'Real address click retains shell keyboard focus');
    xinput(['key', '--clearmodifiers', 'ctrl+a']);
    assert.ok(await evaluate(selector => { const input = document.querySelector(selector); return document.activeElement === input && input.selectionStart === 0 && input.selectionEnd === input.value.length; }, address), 'URL text remains selectable inside draggable toolbar');
    xinput(['type', '--clearmodifiers', '--delay', '0', origin + '/alpha']);
    await waitFor(({ selector, url }) => document.querySelector(selector).value === url, { selector: address, url: origin + '/alpha' });
    xinput(['key', '--clearmodifiers', 'Return']);
    await until(() => nativeView('/alpha')?.getVisible() && !nativeView('/alpha').webContents.isLoading(), 'Editable URL submits after toolbar drag');
  }
  async function assertLivePopover(label) {
    await until(() => nativePopover(), `${label}: native popover is visible`);
    const layer = await nativePopover(), view = views().find(view => view.getVisible());
    assert.ok(view, `${label}: the underlying native website remains visible`);
    const wc = view.webContents, bounds = view.getBounds();
    const before = await wc.executeJavaScript('({frames:window.fixtureFrames,animation:getComputedStyle(document.querySelector("#live-marker")).transform})');
    await delay(160);
    const after = await wc.executeJavaScript('({frames:window.fixtureFrames,animation:getComputedStyle(document.querySelector("#live-marker")).transform})');
    assert.ok(view.getVisible() && after.frames > before.frames, `${label}: live guest frames continue`);
    assert.notEqual(after.animation, before.animation, `${label}: visible page animation continues`);
    assert.deepEqual(view.getBounds(), bounds, `${label}: native page viewport is unchanged`);
    assert.ok(win.contentView.children.indexOf(layer.top) > win.contentView.children.indexOf(view), `${label}: native popover is above the page`);
    report.actions.push({ label: `${label}-live-popover`, contentsID: layer.view.webContents.id, pageID: wc.id, bounds, clip: layer.top.getBounds(), atMs: Date.now() - started }); save();
  }
  async function profileMenu(label, wc) {
    if (label === 'narrow-new-tab') await hoverTab((await readSite()).activeTabId);
    if (label === 'narrow-new-tab') {
      const alignment = await evaluateRibbon(() => {
        const center = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return r.x + r.width / 2; };
        return { rows: { upper: center('.sidebar-ribbon-main [data-ribbon-id="conversations"]'),
          settings: center('.sidebar-ribbon-footer .sidebar-ribbon-button'), account: center('.sidebar-ribbon-footer .sidebar-account-trigger') },
        icons: { upper: center('.sidebar-ribbon-main [data-ribbon-id="conversations"] > svg'),
          settings: center('.sidebar-ribbon-footer .sidebar-ribbon-button > svg'), account: center('.sidebar-ribbon-footer .sidebar-account-avatar') } };
      });
      assert.ok([alignment.rows, alignment.icons].every(group => Object.values(group).every(x => Math.abs(x - group.upper) <= 0.5))
        && alignment.icons.upper < alignment.rows.upper - 16,
      `Narrow large-type footer rows align with upper tabs, with icons on their shared left axis: ${JSON.stringify(alignment)}`);
      report.actions.push({ label: 'narrow-ribbon-footer-alignment', atMs: Date.now() - started, alignment }); save();
    }
    await click('.sidebar-account-trigger', `open-profile-${label}`);
    await until(() => chromeView('account')?.getVisible(), 'Native account panel paints');
    const account = chromeView('account');
    if (wc) await liveChrome('account', wc, `profile-${label}`);
    const geometry = await evaluateContents(account.webContents, () => {
      const menu = document.querySelector('.sidebar-account-menu'), r = menu.getBoundingClientRect();
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, viewport: { width: innerWidth, height: innerHeight },
        hit: [r.left + 8, r.right - 8].every(x => menu.contains(document.elementFromPoint(x, r.top + r.height / 2))),
        items: [...menu.querySelectorAll('[role="menuitem"]')].map(item => {
          const b = item.getBoundingClientRect();
          return { text: item.textContent, hit: item.contains(document.elementFromPoint(b.right - 8, b.top + b.height / 2)) };
        }) };
    });
    const nativeBounds = account.getBounds(), [windowWidth, windowHeight] = win.getContentSize();
    assert.ok(geometry.left >= 0 && geometry.top >= 0 && geometry.right <= geometry.viewport.width + 1
      && geometry.bottom <= geometry.viewport.height + 1 && geometry.hit && geometry.items.every(item => item.hit)
      && nativeBounds.x >= 0 && nativeBounds.y >= 0 && nativeBounds.x + nativeBounds.width <= windowWidth && nativeBounds.y + nativeBounds.height <= windowHeight,
    `Full native account panel is readable and hit-testable: ${JSON.stringify({geometry,nativeBounds})}`);
    await capture(`profile-over-${label}`);
    xinput(['key', '--clearmodifiers', 'Escape']);
    await until(() => !account.getVisible(), 'Escape dismisses native account panel');
    await finishRailInteraction(wc, 'profile-dismissal');
    if (wc) {
      geometry.keyboardDepartures = [];
      for (const chord of ['Tab', 'shift+Tab']) {
        await click('.sidebar-account-trigger', `profile-${chord}-departure`, false);
        await until(() => account.getVisible() && account.webContents.isFocused(), 'Native account panel owns keyboard focus');
        xinput(['key', '--clearmodifiers', chord]);
        await until(() => !account.getVisible(), `${chord} dismisses native account`);
        await waitFor(() => document.activeElement?.matches('.sidebar-account-trigger'));
        assert.ok(win.webContents.isFocused(), `${chord} returns keyboard ownership to the shell account trigger`);
        assert.ok(win.contentView.children.find(view => view.webContents === wc)?.getVisible(), 'Keyboard dismissal never hides the live website');
        await finishRailInteraction(wc, `profile-${chord}`);
        geometry.keyboardDepartures.push({ chord, nativeContentsID: wc.id });
      }
      await click('.sidebar-account-trigger', 'profile-first-outside-click', false);
      await until(() => account.getVisible(), 'Account shown before outside input');
      await pageClick(wc, '#draft', 'first-click-outside-native-account');
      await until(() => !account.getVisible(), 'Outside page click dismisses account');
      assert.ok(wc.isFocused() && await wc.executeJavaScript('document.activeElement.id === "draft"'), 'The first click outside the bounded overlay reaches the website');
      await capture('profile-keyboard-departures');
    }
    return geometry;
  }
  async function openSite(name, pathname) {
    await click(siteIDs.has(name) ? tabButton(siteIDs.get(name)) : ribbon(name), `open-${name}`);
    await waitFor(() => [...document.querySelectorAll('[data-wuu-component="web-workspace"]')].some(node => node.getBoundingClientRect().width > 0));
    const id = await evaluate(() => [...document.querySelectorAll('[data-wuu-component="web-workspace"]')].find(node => node.getBoundingClientRect().width > 0).dataset.websiteId);
    const state = id === ribbonGroup ? await readSite() : await evaluate(({ id, url }) => window.wuu.webWorkspace.open({ id, url }), { id, url: origin + pathname });
    if (state.url !== origin + pathname || state.error) {
      // Independent journey setup only. Address-bar behavior has its own real
      // native Return assertion and a failed navigation is never erased.
      report.actions.push({ label: 'reset-independent-website-fixture', setupOnly: true, from: state.url, to: origin + pathname, atMs: Date.now() - started });
      await evaluate(({ id, url }) => window.wuu.webWorkspace.command(id, 'navigate', url), { id, url: origin + pathname });
    }
    await until(() => nativeView(pathname)?.getVisible() && !nativeView(pathname).webContents.isLoading(), `visible fixture ${pathname}`);
    return nativeView(pathname).webContents;
  }
  async function navigateAddress(pathname, label) {
    await typeAddress(pathname.startsWith('http') ? pathname : origin + pathname, `type-address-${label}`);
    await capture(`before-address-submit-${label}`);
    xinput(['key', '--clearmodifiers', 'Return']);
    report.actions.push({ label: `native-address-submit-${label}`, input: 'native-x11-Return', atMs: Date.now() - started });
    await until(() => nativeView(pathname)?.getVisible() && !nativeView(pathname).webContents.isLoading(), `native address navigation ${pathname}`);
  }
  async function tabMenu(id, label) {
    await hoverTab(id);
    const p = await chromePoint(tabButton(id)), bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    const wc = chromeView('ribbon').webContents, trace = { label, id, point: p, commands: [] };
    const observe = (event, message) => {
      if (event.sender === wc && trace.commands.length < 40) trace.commands.push({ token: message?.token, generation: message?.generation, command: message?.command, atMs: Date.now() - started });
    };
    ipcMain.on('wuu:chrome-overlay-command', observe);
    const popup = Menu.prototype.popup; let items;
    Menu.prototype.popup = function (...args) { items = this.items.map(item => ({ label: item.label, enabled: item.enabled })); return popup.apply(this, args); };
    try {
      xinput(['mousemove', String(Math.round(bounds.x + p.x * zoom)), String(Math.round(bounds.y + p.y * zoom)), 'click', '3']);
      await until(() => items, 'Actual native tab context menu opens');
      await capture(`tab-context-${label}`);
      return items.map(item => item.label);
    } finally {
      Menu.prototype.popup = popup;
      ipcMain.removeListener('wuu:chrome-overlay-command', observe);
      trace.items = items;
      trace.current = await evaluateContents(wc, async id => {
        const request = await window.wuuChromeOverlay.ready();
        return { token: request?.token, generation: request?.generation, item: request?.model.items?.find(item => item.id === id),
          focused: document.hasFocus(), active: document.activeElement?.outerHTML.slice(0, 240) };
      }, id).catch(error => ({ unavailable: String(error) }));
      (report.tabMenuTraces ||= []).push(trace); save();
    }
  }
  async function closeActiveTab(label) {
    const before = await readSite(), id = before.activeTabId;
    const index = before.tabs.findIndex(tab => tab.id === id);
    const remaining = before.tabs.filter(tab => tab.id !== id);
    const expected = remaining[Math.min(index, remaining.length - 1)]?.id ?? '';
    await hoverTab(id);
    await click(`.sidebar-ribbon-close[data-ribbon-close-id="${id}"]`, label);
    await until(async () => !(await readSite()).tabs.some(tab => tab.id === id), 'Direct close button removes the web tab from the ribbon');
    await until(async () => (await readSite()).activeTabId === expected, 'Closing the active left tab selects its adjacent surviving tab');
    const adjacent = remaining.find(tab => tab.id === expected);
    if (adjacent?.url.startsWith(origin)) await until(() => views().some(view => view.getVisible() && view.webContents.getURL() === adjacent.url), 'Adjacent native page is actually visible after close');
    else if (adjacent?.url === 'about:blank') await waitFor(selector => document.querySelector(selector)?.getBoundingClientRect().width > 0, startInput);
    return readSite();
  }
  async function blankTab(label) {
    const before = (await readSite()).tabs.length;
    await click(addTabButton, label);
    await until(async () => (await readSite()).tabs.length === before + 1, 'Left plus creates one real tab');
    await waitFor(selector => document.activeElement?.matches(selector), startInput);
    const state = await readSite();
    assert.equal(state.url, 'about:blank');
    await hidden('blank mascot page');
    return state.activeTabId;
  }
  async function addSite(name, pathname) {
    const id = await blankTab(`new-${name}`);
    siteIDs.set(name, id);
    await fill(startInput, pathname.startsWith('http') ? pathname : origin + pathname);
    xinput(['key', '--clearmodifiers', 'Return']);
    if (!pathname.startsWith('http')) await until(() => nativeView(pathname)?.getVisible() && !nativeView(pathname).webContents.isLoading(), `New left tab loads ${pathname}`);
    return id;
  }
  function security(wc) {
    const p = wc.getLastWebPreferences();
    assert.equal(p.sandbox, true); assert.equal(p.contextIsolation, true); assert.equal(p.nodeIntegration, false);
    assert.ok(!p.preload, 'Untrusted page has no privileged preload');
    assert.equal(wc.session, session.fromPartition('persist:wuu-browser'), 'Uses the existing persistent browser profile');
    return { sandbox: p.sandbox, contextIsolation: p.contextIsolation, nodeIntegration: p.nodeIntegration, preload: p.preload || null };
  }
  async function ensureIndependentFixtures(journey) {
    // The first journey owns real +/address/Pin acceptance. Once its result is
    // recorded, missing prerequisites must not turn every later journey into
    // the same missing-selector failure. API calls here are setup, not passes.
    for (const [name, pathname] of [['Fixture Alpha', '/alpha'], ['Fixture Beta', '/beta']]) {
      const state = await readSite();
      if (state.tabs.some(tab => tab.id === siteIDs.get(name))) continue;
      const restored = await evaluate(({ id, url }) => window.wuu.webWorkspace.createTab(id, url), { id: ribbonGroup, url: origin + pathname });
      siteIDs.set(name, restored.activeTabId);
      if (name === 'Fixture Alpha') alphaID = restored.activeTabId;
      report.actions.push({ label: 'restore-missing-independent-fixture', setupOnly: true, journey, name, id: restored.activeTabId, url: origin + pathname, atMs: Date.now() - started }); save();
      await waitFor(id => document.querySelector(`[data-ribbon-id="${id}"]`), restored.activeTabId);
    }
  }
  async function ensureIndependentCookie(journey) {
    const cookieCheck = report.checks.find(check => check.name === 'native website security cookies history reload and retained input');
    if (cookieCheck?.status === 'passed') return;
    const cookies = session.fromPartition('persist:wuu-browser').cookies;
    if ((await cookies.get({ url: origin })).some(cookie => cookie.name === 'wuu_acceptance' && cookie.value === 'synthetic-session')) return;
    await cookies.set({ url: origin, name: 'wuu_acceptance', value: 'synthetic-session', path: '/', expirationDate: Date.now() / 1000 + 86400, sameSite: 'lax' });
    report.actions.push({ label: 'restore-independent-synthetic-cookie', setupOnly: true, journey, reason: 'The earlier cookie journey did not pass', atMs: Date.now() - started }); save();
  }
  const testNative = (name, action) => test(name, async () => {
    try {
      if (independentFixtures) await ensureIndependentFixtures(name);
      return await action();
    }
    catch (error) {
      try { await capture(`failure-${name}`); } catch {}
      // Keep the original failure, then dismiss only this fixture's popups.
      for (const popup of BrowserWindow.getAllWindows()) {
        if (popup !== win && (popup.webContents.getURL().startsWith(origin) || popup.webContents.getURL() === 'about:blank')) popup.close();
      }
      win.focus(); throw error;
    } finally { await evaluate(() => window.__wuuMascotAcceptance?.cleanup()); }
  });
  let alphaID, savedOrder;

  await testNative('bundled Notion ribbon resolves its setting and creates the native website', async () => {
    const diagnostic = report.notionPlugin = {
      entry: 'notion-workspace:website', requestedURL: 'https://www.notion.so/',
      externalAccount: false, samples: [], events: [], networkStatus: 'pending',
    };
    const created = [];
    const observe = (_event, wc) => {
      // Trusted ribbon/account/capsule views are separate WebContents and
      // must not be counted as repeated Notion website creation.
      if (wc === win.webContents || wc.session !== session.fromPartition('persist:wuu-browser')) return;
      created.push(wc);
      const record = (event, detail = {}) => {
        diagnostic.events.push({ atMs: Date.now() - started, id: wc.id, event, ...detail }); save();
      };
      record('created');
      for (const event of ['did-start-loading', 'did-stop-loading', 'dom-ready', 'did-finish-load']) {
        wc.on(event, () => record(event, { url: wc.getURL(), loading: wc.isLoading() }));
      }
      wc.on('did-start-navigation', (_event, url, inPlace, mainFrame) => {
        if (mainFrame) record('did-start-navigation', { url, inPlace });
      });
      wc.on('did-redirect-navigation', (_event, url, inPlace, mainFrame) => {
        if (mainFrame) record('did-redirect-navigation', { url, inPlace });
      });
      wc.on('did-navigate', (_event, url, statusCode, statusText) => record('did-navigate', { url, statusCode, statusText }));
      wc.on('did-fail-load', (_event, code, description, url, mainFrame) => {
        if (mainFrame) record('did-fail-load', { code, description, url });
      });
      wc.on('console-message', event => {
        if (event.level === 'error' && diagnostic.events.filter(item => item.event === 'console-error').length < 20) {
          record('console-error', { message: event.message.slice(0, 1800), source: event.sourceId, line: event.lineNumber });
        }
      });
      wc.on('render-process-gone', (_event, detail) => record('render-process-gone', detail));
      wc.on('destroyed', () => record('destroyed'));
    };
    app.on('web-contents-created', observe);
    const pluginRow = '.plugin-row[data-plugin="notion-workspace"]';
    try {
      await click(plugins, 'notion-open-extension-inventory');
      await click(pick('.sidebar-section-item', 'Plugins'), 'notion-plugin-list');
      await waitFor(selector => document.querySelector(selector), pluginRow);
      await click(`${pluginRow} [role="switch"]`, 'enable-bundled-notion');
      await waitFor(() => document.querySelector('.sidebar-ribbon-main button[aria-label="Notion"]'), undefined, 'bundled Notion ribbon activation', 20000);
      diagnostic.ribbonID = await evaluate(() => document.querySelector('.sidebar-ribbon-main button[aria-label="Notion"]').dataset.ribbonId);
      assert.ok(diagnostic.ribbonID.startsWith('plugin:'), 'Notion is a registered plugin destination, not a website shortcut');
      const [registeredPluginID] = JSON.parse(diagnostic.ribbonID.slice('plugin:'.length));
      diagnostic.registeredPluginID = registeredPluginID;
      const selectedAt = Date.now();
      await click(ribbon('Notion'), 'actual-bundled-notion-ribbon');
      for (const elapsed of [0, 3000, 10000, 20000]) {
        await delay(Math.max(0, selectedAt + elapsed - Date.now()));
        const shell = await evaluate(registeredPluginID => {
          const root = [...document.querySelectorAll('.plugin-workbench-view, [data-wuu-component="plugin-view-content"]')]
            .find(node => (node.dataset.pluginId || node.dataset.wuuPlugin) === registeredPluginID
              && node.getBoundingClientRect().width > 0 && node.getBoundingClientRect().height > 0);
          return { mounted: Boolean(root), text: root?.innerText.slice(0, 1600) || '',
            nativeHost: root?.querySelector('[data-wuu-component="web-workspace"]')?.getAttribute('data-website-id') || null,
            alert: root?.querySelector('[role="alert"]')?.textContent || null };
        }, registeredPluginID);
        const native = [];
        for (const view of win.contentView.children) {
          const wc = view.webContents;
          if (!wc || wc.isDestroyed() || !created.includes(wc)) continue;
          const state = { id: wc.id, url: wc.getURL(), visible: view.getVisible(), loading: wc.isLoading(), bounds: view.getBounds() };
          if (!wc.isLoadingMainFrame() && /^https:\/\//.test(state.url)) {
            state.document = await Promise.race([
              wc.executeJavaScript('({title:document.title,ready:document.readyState,userAgent:navigator.userAgent,text:document.body?.innerText.slice(0,1800)||"",visibleInputs:Array.from(document.querySelectorAll("input")).filter(e=>e.getBoundingClientRect().width>0).map(e=>({type:e.type,placeholder:e.placeholder}))})').catch(error => ({ error: String(error) })),
              delay(1500).then(() => ({ unavailable: 'document observation timed out' })),
            ]);
          }
          native.push(state);
        }
        diagnostic.samples.push({ elapsedMs: Date.now() - selectedAt, shell, native });
        save(); await capture(`notion-plugin-${elapsed}ms`);
      }
      const last = diagnostic.samples.at(-1);
      diagnostic.networkStatus = last.native.some(page => !page.loading && page.document?.text)
        ? 'document-rendered' : diagnostic.events.some(event => event.event === 'did-fail-load') ? 'navigation-failed' : 'unsettled-after-20s';
      diagnostic.createdContents = created.length;
      save();
      assert.ok(last.shell.nativeHost, `Bundled Notion must leave its plugin loading state: ${JSON.stringify(last.shell)}`);
      assert.ok(diagnostic.events.some(event => event.event === 'did-start-navigation' && event.url === diagnostic.requestedURL), 'The actual plugin requests its default notion.so URL');
      assert.ok(last.native.some(page => page.visible), 'The Notion plugin presents its native page, independent of external network availability');
      assert.ok(created.length <= 2, `Plugin renders must not recreate native pages repeatedly: ${created.length}`);
      // Anonymous notion.so can redirect to marketing. Follow its real public
      // login link to observe the application, without entering any account data.
      const page = created.find(wc => !wc.isDestroyed() && /^https:\/\/(?:www\.)?notion\.(?:com|so)\//.test(wc.getURL()));
      const existingLogin = last.native.find(page => /^https:\/\/(?:app\.|www\.)?notion\.(?:com|so)\/login/.test(page.url) && page.document?.visibleInputs?.length);
      diagnostic.publicLogin = existingLogin ? { status: 'login-form-visible', ...existingLogin } : { status: 'not-offered' };
      if (existingLogin) { save(); await capture('notion-public-application-login'); }
      else if (page && !page.isLoadingMainFrame()) {
        const href = await Promise.race([
          page.executeJavaScript(`Array.from(document.querySelectorAll('a[href]')).find(a => /^(log in|登录)$/i.test(a.textContent.trim()))?.getAttribute('href') || ''`),
          delay(1500).then(() => ''),
        ]);
        const destination = href ? new URL(href, page.getURL()) : null;
        if (destination?.protocol === 'https:' && /(^|\.)notion\.(com|so)$/.test(destination.hostname) && destination.pathname.startsWith('/login')) {
          diagnostic.publicLogin = { requestedURL: destination.href, status: 'observing' }; save();
          await pageClick(page, `a[href=${JSON.stringify(href)}]`, 'notion-public-login-link');
          await delay(10000);
          const live = created.filter(wc => !wc.isDestroyed()).map(wc => ({ id: wc.id, url: wc.getURL(), loading: wc.isLoading() }));
          diagnostic.publicLogin.pages = live;
          const login = created.find(wc => !wc.isDestroyed() && /^https:\/\/(?:app\.|www\.)?notion\.(?:com|so)\/login/.test(wc.getURL()));
          if (login && !login.isLoadingMainFrame()) {
            diagnostic.publicLogin.document = await Promise.race([
              login.executeJavaScript('({title:document.title,ready:document.readyState,userAgent:navigator.userAgent,text:document.body?.innerText.slice(0,2400)||"",visibleInputs:Array.from(document.querySelectorAll("input")).filter(e=>e.getBoundingClientRect().width>0).map(e=>({type:e.type,placeholder:e.placeholder}))})'),
              delay(1500).then(() => ({ unavailable: 'document observation timed out' })),
            ]);
          }
          diagnostic.publicLogin.status = diagnostic.publicLogin.document?.visibleInputs?.length ? 'login-form-visible' : 'unsettled-after-10s';
          save(); await capture('notion-public-application-login');
        }
      }
      return { requestedURL: diagnostic.requestedURL, networkStatus: diagnostic.networkStatus, createdContents: created.length, final: last };
    } finally {
      app.removeListener('web-contents-created', observe);
      // Return the isolated profile to its original optional-plugin state so
      // external activity cannot contaminate unrelated local fixture journeys.
      await click(plugins, 'notion-diagnostic-return-to-inventory');
      await click(pick('.sidebar-section-item', 'Plugins'), 'notion-diagnostic-plugin-list');
      if (await evaluate(selector => document.querySelector(`${selector} [role="switch"]`)?.getAttribute('aria-checked') === 'true', pluginRow)) {
        await click(`${pluginRow} [role="switch"]`, 'disable-diagnostic-notion');
      }
      await goConversations();
    }
  });

  await testNative('left plus opens one blank mascot tab and Pin retains the website', async () => {
    const probeContents = [win.webContents, chromeView('ribbon')?.webContents].filter(Boolean);
    for (const wc of probeContents) await evaluateContents(wc, () => {
      const events = [], types = ['pointerover', 'pointerout', 'pointermove', 'focusin', 'focusout', 'focus', 'blur'];
      const observe = event => {
        const nav = document.querySelector('.sidebar-ribbon');
        events.push({ type: event.type, at: performance.now(), x: event.clientX, y: event.clientY,
          target: event.target instanceof Element ? event.target.outerHTML.slice(0, 160) : String(event.target),
          related: event.relatedTarget instanceof Element ? event.relatedTarget.outerHTML.slice(0, 160) : String(event.relatedTarget),
          expanded: nav?.dataset.expanded, hovered: nav?.matches(':hover'), focusedWithin: nav?.contains(document.activeElement) });
        if (events.length > 160) events.shift();
      };
      types.forEach(type => window.addEventListener(type, observe, true));
      window.__wuuRailAcceptance = { events, stop: () => types.forEach(type => window.removeEventListener(type, observe, true)) };
    });
    try {
      assert.equal(await evaluate(selector => document.querySelectorAll(selector).length, webTabs), 0, 'No website tabs are added by default');
      await blankTab('first-left-web-tab');
      assert.equal(await evaluate(() => Boolean(document.querySelector('.web-workspace-tabstrip'))), false);
      await fill(startInput, 'javascript:alert(1)'); xinput(['key', '--clearmodifiers', 'Return']);
      await waitFor(selector => document.querySelector(selector)?.getAttribute('aria-invalid') === 'true', startInput);
      assert.equal((await readSite()).url, 'about:blank', 'Unsafe scheme never navigates');
      await capture('unsafe-address-rejected');
      await closeActiveTab('close-final-blank-tab');
      assert.equal((await readSite()).tabs.length, 0, 'Closing the final tab leaves no artificial replacement');
      await waitFor(() => !document.querySelector('.web-workspace-stage:not([hidden])'));
      alphaID = await blankTab('new-alpha-left-tab'); siteIDs.set('Fixture Alpha', alphaID);
      await fill(startInput, origin + '/alpha'); xinput(['key', '--clearmodifiers', 'Return']);
      await until(() => nativeView('/alpha')?.getVisible() && !nativeView('/alpha').webContents.isLoading(), 'First left tab loads fixture');
      const options = await tabMenu(alphaID, 'pin-alpha');
      assert.deepEqual(options, ['Pin tab', 'Close tab'], 'Website context menu stays minimal');
      xinput(['key', 'Home', 'key', 'Return']);
      await waitFor(id => document.querySelector(`[data-ribbon-id="${id}"]`)?.dataset.pinned === 'true', alphaID);
      await addSite('Fixture Beta', '/beta');
      await openSite('Fixture Alpha', '/alpha');
      const vertical = { collapsed: await verticalTabGeometry() }, hostBefore = nativeView('/alpha').getBounds();
      const viewportBefore = await nativeView('/alpha').webContents.executeJavaScript('({width:innerWidth,height:innerHeight})');
      const focusBeforeHover = nativeView('/alpha').webContents.isFocused();
      await hoverTab(siteIDs.get('Fixture Beta'));
      vertical.expanded = await verticalTabGeometry(true);
      await liveChrome('ribbon', nativeView('/alpha').webContents, 'hover rail');
      assert.equal(nativeView('/alpha').webContents.isFocused(), focusBeforeHover, 'Pointer hover preserves existing native keyboard focus');
      assert.deepEqual(nativeView('/alpha').getBounds(), hostBefore, 'Hover expansion does not resize the native website');
      assert.deepEqual(await nativeView('/alpha').webContents.executeJavaScript('({width:innerWidth,height:innerHeight})'), viewportBefore, 'Hover expansion leaves the page viewport unchanged');
      assert.ok(await evaluateRibbon(id => { const close = document.querySelector(`[data-ribbon-close-id="${id}"]`); return close && getComputedStyle(close).visibility === 'visible' && Number(getComputedStyle(close).opacity) > 0.95; }, siteIDs.get('Fixture Beta')), 'Hovered native tab exposes its close control');
      await capture('vertical-left-tabs-with-hover-close');
      await leaveRail();
      await until(() => nativeView('/alpha')?.getVisible() && !chromeView('ribbon')?.getVisible(), 'Pointer leave dismisses chrome while retaining the same live page');
      vertical.restored = await verticalTabGeometry();
      await capture('collapsed-left-tabs-unchanged-web-viewport');
      return { alphaID, betaID: siteIDs.get('Fixture Beta'), options, tabs: (await readSite()).tabs, vertical };
    } finally {
      report.firstTabRailInteraction = await Promise.all(probeContents.map(async wc => {
        if (wc.isDestroyed()) return { id: wc.id, destroyed: true };
        const state = await evaluateContents(wc, () => {
          const nav = document.querySelector('.sidebar-ribbon'), probe = window.__wuuRailAcceptance;
          const result = { events: probe.events, expanded: nav?.dataset.expanded, hovered: nav?.matches(':hover'),
            focusedWithin: nav?.contains(document.activeElement), documentFocused: document.hasFocus(), activeElement: document.activeElement?.outerHTML.slice(0, 300) };
          probe.stop(); delete window.__wuuRailAcceptance; return result;
        });
        return { id: wc.id, url: wc.getURL(), focused: wc.isFocused(), ...state };
      })).catch(error => [{ unavailable: String(error) }]);
      save();
    }
  });

  independentFixtures = true;
  await testNative('ribbon reorders Chat away from first with real keyboard and pointer', async () => {
    await openSite('Fixture Alpha', '/alpha');
    const before = await evaluate(() => [...document.querySelectorAll('.sidebar-ribbon-main [data-ribbon-id]')].map(node => node.dataset.ribbonId));
    await click('.sidebar-account-trigger', 'anchor-ribbon-keyboard'); xinput(['key', 'Escape']);
    await waitFor(() => document.activeElement?.matches('.sidebar-account-trigger'));
    for (let step = 0; step < before.length * 2 + 4; step++) {
      xinput(['key', '--clearmodifiers', 'shift+Tab']); await delay(60);
      if (await evaluateRibbon(id => document.activeElement?.dataset.ribbonId === id, alphaID)) break;
    }
    assert.ok(await evaluateRibbon(id => document.activeElement?.dataset.ribbonId === id, alphaID), 'Real keyboard traversal reaches the left web tab');
    await capture('ribbon-keyboard-focus-before-reorder');
    for (let index = before.indexOf(alphaID); index > 0; index--) {
      xinput(['key', '--clearmodifiers', 'alt+Up']);
      await waitFor(({ id, index }) => document.querySelectorAll('.sidebar-ribbon-main [data-ribbon-id]')[index - 1]?.dataset.ribbonId === id, { id: alphaID, index });
      assert.ok(await evaluateRibbon(id => document.activeElement?.dataset.ribbonId === id, alphaID), 'Keyboard reorder retains ribbon focus');
    }
    const betaID = siteIDs.get('Fixture Beta');
    const from = await chromePoint(tabButton(betaID)), to = await chromePoint(tabButton(alphaID));
    const bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    const sx = bounds.x + from.x * zoom, sy = bounds.y + from.y * zoom, ex = bounds.x + to.x * zoom, ey = bounds.y + to.y * zoom;
    try {
      xinput(['mousemove', String(Math.round(sx)), String(Math.round(sy)), 'mousedown', '1']); await delay(80);
      for (let step = 1; step <= 8; step++) {
        xinput(['mousemove', String(Math.round(sx + (ex - sx) * step / 8)), String(Math.round(sy + (ey - sy) * step / 8))]); await delay(40);
      }
      xinput(['mouseup', '1']);
      await waitFor(id => document.querySelector('.sidebar-ribbon-main [data-ribbon-id]')?.dataset.ribbonId === id, betaID);
    } finally { xinput(['mouseup', '1']); }
    savedOrder = await evaluate(() => [...document.querySelectorAll('.sidebar-ribbon-main [data-ribbon-id]')].map(node => node.dataset.ribbonId));
    assert.ok(savedOrder.indexOf(await evaluate(selector => document.querySelector(selector).dataset.ribbonId, conversations)) > 0, 'Chat is not pinned first');
    assert.deepEqual((await prefs()).order.filter(id => savedOrder.includes(id)), savedOrder);
    await capture('reordered-left-tabs'); return { before, after: savedOrder };
  });

  await testNative('native website security cookies history reload and retained input', async () => {
    let wc = await openSite('Fixture Alpha', '/alpha');
    const preferences = security(wc);
    assert.deepEqual(await wc.executeJavaScript('({wuu:typeof window.wuu,require:typeof require,process:typeof process})'), { wuu: 'undefined', require: 'undefined', process: 'undefined' });
    await pageClick(wc, '#cookie', 'set-synthetic-cookie');
    await until(() => !wc.isLoading() && wc.getURL() === origin + '/alpha', 'cookie redirect completed');
    assert.ok((await wc.executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'));
    assert.ok((await session.fromPartition('persist:wuu-browser').cookies.get({ url: origin })).some(cookie => cookie.name === 'wuu_acceptance'));
    await pageClick(wc, '#draft', 'focus-native-draft');
    await wc.insertText('Retained native website draft');
    await until(() => wc.executeJavaScript('document.querySelector("#draft").value === "Retained native website draft"'), 'native draft input');
    await click(conversations, 'website-to-chat'); await hidden('Chat');
    await openSite('Fixture Alpha', '/alpha');
    assert.equal(await wc.executeJavaScript('document.querySelector("#draft").value'), 'Retained native website draft');
    await pageClick(wc, '#next', 'navigate-native-link');
    await until(() => wc.getURL() === origin + '/next' && !wc.isLoading(), 'next page loaded');
    await click(toolbar('Back'), 'native-back');
    await until(() => wc.getURL() === origin + '/alpha' && !wc.isLoading(), 'back returned');
    await click(toolbar('Forward'), 'native-forward');
    await until(() => wc.getURL() === origin + '/next' && !wc.isLoading(), 'forward returned');
    const requestsBeforeReload = fixture.requests.filter(request => request.path === '/next').length;
    await click(toolbar('Reload'), 'native-reload');
    await until(() => fixture.requests.filter(request => request.path === '/next').length > requestsBeforeReload && !wc.isLoading(), 'reload requested a fresh page');
    assert.ok((await wc.executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'));
    await pageClick(wc, '#draft', 'focus-before-native-address-shortcut');
    xinput(['key', 'ctrl+l']);
    await waitFor(selector => document.activeElement?.matches(selector), address, 'native Ctrl+L focuses shell address');
    try {
      await fill(address, origin + '/loading-hold'); xinput(['key', '--clearmodifiers', 'Return']);
      await until(() => wc.getURL() === origin + '/loading-hold' && wc.isLoading(), 'Controlled local document remains loading');
      await waitFor(() => document.querySelector('.web-workspace-toolbar button[aria-label="Stop loading"]')
        && document.querySelector('[data-wuu-component="website-navigation-item"][aria-current="page"] .sidebar-ribbon-tab-loading'));
      assert.equal(await evaluate(() => Boolean(document.querySelector('.web-workspace-toolbar .workspace-browser-loading-bar'))), false, 'Loading has no redundant full-width blue progress strip');
      await capture('controlled-loading-ribbon-spinner-and-stop');
      await click(toolbar('Stop loading'), 'stop-controlled-loading');
      await until(() => !wc.isLoading(), 'Stop cancels the controlled loading document');
      await waitFor(() => document.querySelector('.web-workspace-toolbar button[aria-label="Reload"]'));
    } finally { fixture.controls.finishLoading?.(); }
    await navigateAddress('/alpha', 'return-from-history');
    assert.equal(wc.getURL(), origin + '/alpha');
    await capture('native-page-loaded'); return { preferences, cookie: 'synthetic-session', contentsId: wc.id };
  });

  await testNative('native websites yield to sections dialogs and restore real focus', async () => {
    const transitions = await traceLoadedTabSwitches();
    const wc = await openSite('Fixture Alpha', '/alpha');
    await tabMenu(alphaID, 'over-live-page');
    await liveChrome('ribbon', nativeView('/alpha').webContents, 'native tab context menu'); const managerHidden = false;
    await capture('context-menu-over-native-page');
    xinput(['key', 'Escape']);
    await settle();
    await finishRailInteraction(wc, 'tab-context-dismissal');
    await goSettings('Appearance'); await hidden('Settings'); await capture('settings-with-native-hidden');
    await openSite('Fixture Alpha', '/alpha');
    await click(plugins, 'native-to-extensions'); await hidden('Extensions');
    await click(conversations, 'native-to-chat'); await hidden('Conversations');
    key('P', ['control']);
    await waitFor(() => document.querySelector('[role="dialog"] input'));
    await hidden('search'); await capture('search-with-native-hidden'); key('Escape');
    for (let i = 0; i < 3; i++) {
      await openSite('Fixture Alpha', '/alpha');
      await click(plugins, 'rapid-extensions', false); await hidden('rapid Extensions');
      await click(conversations, 'rapid-chat', false); await hidden('rapid Chat');
    }
    await openSite('Fixture Alpha', '/alpha');
    await pageClick(wc, '#draft', 'focus-after-section-return');
    await wc.insertText(' restored');
    await until(() => wc.executeJavaScript('document.querySelector("#draft").value.endsWith(" restored")'), 'native focus accepts input after restore');
    return { managerHidden, retainedContentsId: wc.id, transitions };
  });

  await testNative('native website bounds follow narrow large-type and zoomed workbench', async () => {
    const samples = [];
    let visualTabID;
    try {
      for (const [theme, size, width, zoom] of [['light', 15, 700, 1], ['light', 20, 440, 1], ['light', 20, 960, 1], ['dark', 20, 1440, 1.25]]) {
        await appearance(theme, size, width); win.webContents.setZoomFactor(zoom); await settle();
        await openSite('Fixture Alpha', '/alpha');
        const view = nativeView('/alpha');
        await until(async () => {
          const host = await evaluate(() => { const r=document.querySelector('.web-workspace-host').getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height}; });
          const actual = view.getBounds();
          return ['x','y','width','height'].every(key => Math.abs(actual[key] - Math.round(host[key] * zoom)) <= 2);
        }, 'native view matches rendered host bounds');
        const bounds = view.getBounds(), [w, h] = win.getContentSize();
        assert.ok(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= w && bounds.y + bounds.height <= h);
        const chrome = await chromeGeometry();
        chrome.verticalTabs = await verticalTabGeometry();
        if (width === 700) await dragBrowserToolbar();
        await capture(`bounds-${theme}-${width}-zoom-${zoom}`);
        if (width === 440) {
          for (const [name, trigger, panel] of [['library', '[data-browser-library-trigger]', '[data-browser-library]'], ['downloads', '[data-web-workspace-downloads-trigger]', '[data-web-workspace-downloads]']]) {
            await click(`.web-workspace-toolbar ${trigger}`, `narrow-toolbar-${name}`, false);
            await waitPopover(selector => document.querySelector(selector), panel);
            await assertLivePopover(`narrow-toolbar-${name}`);
            await capture(`narrow-toolbar-${name}`);
            xinput(['key', '--clearmodifiers', 'Escape']);
            await until(async () => !await nativePopover(), `Native ${name} closes on Escape`);
            await until(() => view.getVisible(), 'Closing narrow toolbar menus restores the same native webpage');
          }
        }
        if (width === 440 || theme === 'dark') {
          visualTabID = await blankTab(`new-tab-${theme}-${width}`);
          await waitFor(selector => document.activeElement?.matches(selector), startInput);
          await hidden('new-tab start page');
          await capture(`new-tab-${theme}-${width}-font-${size}`);
          if (width === 440) await profileMenu('narrow-new-tab');
          await closeActiveTab('return-from-visual-new-tab');
          visualTabID = undefined;
          await openSite('Fixture Alpha', '/alpha');
        }
        samples.push({ theme, size, width, zoom, bounds, chrome });
      }
    } finally {
      if (visualTabID && (await readSite()).tabs.some(tab => tab.id === visualTabID)) {
        // A failed visual assertion must not leave this journey's blank tab as
        // an unrelated later journey's adjacent close destination.
        await evaluate(({ group, id }) => window.wuu.webWorkspace.closeTab(group, id), { group: ribbonGroup, id: visualTabID });
        report.actions.push({ label: 'cleanup-owned-visual-tab-after-failure', setupOnly: true, id: visualTabID, atMs: Date.now() - started }); save();
      }
      win.webContents.setZoomFactor(1); await appearance('light', 15, 1280);
    }
    return samples;
  });

  await testNative('website popups preserve opener and POST with no privileged bridge', async () => {
    const wc = await openSite('Fixture Alpha', '/alpha');
    const initial = new Set(BrowserWindow.getAllWindows().map(window => window.id));
    await pageClick(wc, '#blank', 'open-blank-popup');
    await until(() => BrowserWindow.getAllWindows().some(window => !initial.has(window.id)), 'blank popup created');
    const blank = BrowserWindow.getAllWindows().find(window => !initial.has(window.id));
    await until(() => wc.executeJavaScript('document.querySelector("#popup-result").textContent === "fixture-opener-roundtrip"'), 'popup opener message');
    security(blank.webContents);
    assert.deepEqual(await blank.webContents.executeJavaScript('({wuu:typeof window.wuu,require:typeof require,process:typeof process})'), { wuu: 'undefined', require: 'undefined', process: 'undefined' });
    await capture('blank-popup-opener'); blank.close(); win.focus();
    const tabsBefore = (await readSite()).tabs.length;
    await pageClick(wc, '#post', 'open-post-popup');
    await until(() => fixture.requests.some(request => request.path === '/posted' && request.method === 'POST' && request.body === 'fixture=post-preserved'), 'native POST body preserved');
    await until(() => contents('/posted') && !contents('/posted').isLoading(), 'POST left tab loaded');
    const post = contents('/posted'); security(post); await capture('post-left-tab');
    assert.ok(nativeView('/posted')?.getVisible(), 'Ordinary target=_blank POST opens a left tab');
    assert.equal((await readSite()).tabs.length, tabsBefore + 1);
    assert.equal(await evaluate(selector => document.querySelectorAll(selector).length, webTabs), tabsBefore + 1);
    await closeActiveTab('close-post-tab');
    win.focus();
    return { openerRoundtrip: true, ordinaryPostMode: 'left-tab', post: fixture.requests.find(request => request.path === '/posted') };
  });

  await testNative('website camera and location never receive silent grants', async () => {
    const wc = await openSite('Fixture Alpha', '/alpha');
    const observed = [];
    const original = dialog.showMessageBox;
    dialog.showMessageBox = function(...args) {
      const options = args.at(-1);
      observed.push({ title: options.title, message: options.message, buttons: options.buttons });
      return original.apply(this, args);
    };
    try {
      for (const selector of ['#geo', '#camera']) {
        const before = observed.length;
        await pageClick(wc, selector, `request-${selector.slice(1)}-permission`);
        await until(() => observed.length > before, 'real native permission dialog opened');
        assert.equal(observed.at(-1).title, 'Website permission');
        await capture(`${selector.slice(1)}-permission-prompt`);
        xinput(['key', 'Escape']);
        await until(() => wc.executeJavaScript('document.querySelector("#permission-result").textContent === "denied"'), 'native permission denial returned');
      }
    } finally { xinput(['key', 'Escape']); dialog.showMessageBox = original; }
    return observed;
  });

  const liveStage = '.web-workspace-stage:not([hidden])';
  let clearedHistoryURL;

  await testNative('left web tabs native shortcuts find and per-page zoom', async () => {
    let wc = await openSite('Fixture Alpha', '/alpha');
    const before = (await readSite()).tabs.length;
    await blankTab('click-left-add-tab');
    await closeActiveTab('click-close-left-tab');
    wc = await openSite('Fixture Alpha', '/alpha');
    await pageClick(wc, '#draft', 'native-tab-shortcut-focus'); xinput(['key', '--clearmodifiers', 'ctrl+t']);
    await waitFor(({ count, input }) => document.querySelectorAll('[data-wuu-component="website-navigation-item"]').length === count && document.activeElement?.matches(input), { count: before + 1, input: startInput });
    await hidden('new blank tab');
    await fill(startInput, origin + '/tab-two');
    await capture('new-tab-central-address'); xinput(['key', '--clearmodifiers', 'Return']);
    await until(() => nativeView('/tab-two')?.getVisible() && !nativeView('/tab-two').webContents.isLoading(), 'Central new-tab input navigates on native Return');
    wc = nativeView('/tab-two').webContents;
    report.nativeFindEvents ||= [];
    wc.on('found-in-page', (_event, result) => { report.nativeFindEvents.push({ contentsID: wc.id, result, atMs: Date.now() - started }); save(); });
    await pageClick(wc, '#draft', 'native-find-shortcut-focus'); xinput(['key', '--clearmodifiers', 'ctrl+f']);
    const findInput = `${liveStage} .web-workspace-findbar input`;
    await waitFor(selector => document.activeElement?.matches(selector), findInput);
    await fill(findInput, 'fixture');
    await until(async () => (await readSite()).findResult?.matches > 1, 'native find returns multiple matches');
    const found = (await readSite()).findResult;
    await click(`${liveStage} button[aria-label="Next match"]`, 'next-native-find-match');
    await until(async () => (await readSite()).findResult?.activeMatchOrdinal !== found.activeMatchOrdinal, 'next find result selected');
    await chromeGeometry();
    await capture('tabs-and-native-find');
    await click(`${liveStage} button[aria-label="Close find"]`, 'close-native-find');
    await click(`${liveStage} button[aria-label="Browser options"]`, 'browser-options');
    xinput(['key', 'Home', 'key', 'Down', 'key', 'Down', 'key', 'Down', 'key', 'Return']);
    await until(() => wc.getZoomFactor() > 1, 'actual native website zoom increased');
    const zoom = wc.getZoomFactor();
    await click(`${liveStage} button[aria-label="Browser options"]`, 'reopen-browser-options');
    xinput(['key', 'Home', 'key', 'Down', 'key', 'Down', 'key', 'Return']);
    await until(() => wc.getZoomFactor() === 1, 'actual native zoom reset'); xinput(['key', 'Escape']);
    await pageClick(wc, '#draft', 'native-close-tab-focus'); xinput(['key', '--clearmodifiers', 'ctrl+w']);
    await until(async () => !win.isDestroyed() && (await readSite()).tabs.length === before, 'native Ctrl+W closes one tab only');
    wc = await openSite('Fixture Alpha', '/alpha');
    await pageClick(wc, '#draft', 'native-reopen-tab-focus'); xinput(['key', '--clearmodifiers', 'ctrl+shift+t']);
    await until(async () => (await readSite()).tabs.length === before + 1 && nativeView('/tab-two')?.getVisible(), 'native reopen restores closed tab');
    await click(address, 'address-focused-close-tab'); xinput(['key', '--clearmodifiers', 'ctrl+w']);
    await until(async () => !win.isDestroyed() && (await readSite()).tabs.length === before, 'address-focused Ctrl+W closes only the active tab');
    return { originalTabs: before, find: found, zoom, windowSurvived: !win.isDestroyed() };
  });

  await testNative('native downloads complete pause resume and cancel safely', async () => {
    const wc = await openSite('Fixture Alpha', '/alpha');
    const partition = session.fromPartition('persist:wuu-browser');
    const directory = path.join(output, 'synthetic-downloads'); fs.mkdirSync(directory, { recursive: true });
    const saveFixture = (_event, item) => {
      if ([origin + '/download', origin + '/slow-download'].includes(item.getURL())) item.setSavePath(path.join(directory, item.getFilename()));
    };
    // Only the fixture's OS save-picker destination is predetermined. Native
    // transfer bytes/events, UI controls and ownership remain production code.
    partition.on('will-download', saveFixture);
    const downloads = () => evaluate(() => window.wuu.webWorkspace.listDownloads());
    const menu = '[data-web-workspace-downloads-trigger]';
    try {
      await pageClick(wc, '#download', 'download-synthetic-file');
      await until(async () => (await downloads()).some(item => item.filename === 'fixture.txt' && item.state === 'completed'), 'download completed');
      const completed = (await downloads()).find(item => item.filename === 'fixture.txt');
      assert.deepEqual(fs.readFileSync(completed.savePath), fixture.downloadBytes, 'Downloaded bytes match the synthetic response');
      await click(menu, 'open-downloads'); await assertLivePopover('downloads panel');
      await waitPopover(id => document.querySelector(`[data-download-id="${id}"] [data-download-command="reveal"]`), completed.id);
      await capture('completed-download'); xinput(['key', 'Escape']);
      await until(() => nativeView('/alpha')?.getVisible(), 'restore after downloads');
      await pageClick(wc, '#slow-download', 'start-cancellable-download');
      await until(async () => (await downloads()).some(item => item.filename === 'slow-fixture.bin' && item.receivedBytes > 0 && !item.done), 'streaming download made progress');
      const transfer = (await downloads()).find(item => item.filename === 'slow-fixture.bin');
      const action = command => `[data-download-id="${transfer.id}"] [data-download-command="${command}"]`;
      await click(menu, 'manage-live-download');
      await click(action('pause'), 'pause-download');
      await until(async () => (await downloads()).find(item => item.id === transfer.id)?.paused, 'download paused');
      await click(action('resume'), 'resume-download');
      await until(async () => !(await downloads()).find(item => item.id === transfer.id)?.paused, 'download resumed');
      await click(action('cancel'), 'cancel-download');
      await until(async () => (await downloads()).find(item => item.id === transfer.id)?.state === 'cancelled', 'download cancellation completed');
      await capture('cancelled-download'); xinput(['key', 'Escape']);
      return { completed: { filename: completed.filename, bytes: completed.receivedBytes, sha256: createHash('sha256').update(fixture.downloadBytes).digest('hex') }, cancelled: transfer.id, nativeSavePicker: 'fixture path preselected, not exercised' };
    } finally { partition.removeListener('will-download', saveFixture); }
  });

  await testNative('bookmarks history search and clearing preserve private saved state', async () => {
    await openSite('Fixture Alpha', '/alpha');
    await click('[data-browser-bookmark-current]', 'bookmark-local-page');
    await waitFor(() => document.querySelector('[data-browser-bookmark-current]')?.getAttribute('aria-pressed') === 'true');
    await navigateAddress('/history-target', 'history-page');
    const tabsBeforeStart = (await readSite()).tabs.length;
    await blankTab('saved-history-new-tab');
    await waitFor(selector => document.activeElement?.matches(selector), startInput);
    await waitFor(() => {
      const mascot = document.querySelector('.web-workspace-start-mascot');
      return mascot?.getBoundingClientRect().width > 0 && (!(mascot instanceof HTMLImageElement) || mascot.complete && mascot.naturalWidth > 0);
    });
    await capture('mascot-new-tab-ready');
    const gaze = await mascotGaze();
    await fill(startInput, 'fixture');
    const suggestion = url => `[data-address-suggestion-url="${url}"]`;
    await waitPopover(({ alpha, history }) => document.querySelector(`[data-address-suggestion-url="${alpha}"]`)?.dataset.addressSuggestionSource === 'bookmark'
      && document.querySelector(`[data-address-suggestion-url="${history}"]`)?.dataset.addressSuggestionSource === 'history', { alpha: origin + '/alpha', history: origin + '/history-target' });
    await capture('central-history-and-bookmark-suggestions');
    xinput(['key', '--clearmodifiers', 'Escape']);
    await until(async () => !await nativePopover(), 'Central suggestions close on Escape');
    await waitFor(selector => document.activeElement?.matches(selector) && document.activeElement.value === 'fixture', startInput);
    await fill(startInput, 'alpha');
    await waitPopover(selector => document.querySelector(selector), suggestion(origin + '/alpha'));
    xinput(['key', '--clearmodifiers', 'Down']);
    await waitPopover(url => document.querySelector(`[data-address-suggestion-url="${url}"]`)?.getAttribute('aria-selected') === 'true', origin + '/alpha');
    xinput(['key', '--clearmodifiers', 'Return']);
    await until(() => nativeView('/alpha')?.getVisible() && !nativeView('/alpha').webContents.isLoading(), 'Keyboard selection opens the saved bookmark');
    gaze.unmount = await mascotUnmount();
    // The page now owns native focus. A renderer-only sendInputEvent can edit
    // its DOM address field without moving OS focus from the WebContentsView.
    // Exercise the real user's click and typing before judging autocomplete.
    await typeAddress('history-target', 'focus-toolbar-history-suggestions');
    await waitFor(selector => document.activeElement?.matches(selector) && document.activeElement.value === 'history-target', address);
    await waitPopover(selector => document.querySelector(selector), suggestion(origin + '/history-target'));
    await assertLivePopover('toolbar-address-suggestions');
    await capture('toolbar-history-suggestion-over-page');
    xinput(['key', '--clearmodifiers', 'Escape']);
    await until(async () => !await nativePopover(), 'Toolbar suggestions close on Escape');
    await waitFor(selector => document.activeElement?.matches(selector) && document.activeElement.value === 'history-target', address);
    await until(() => nativeView('/alpha')?.getVisible(), 'Escape restores native page while retaining address draft');
    xinput(['key', '--clearmodifiers', 'Down']);
    await waitPopover(selector => document.querySelector(selector), suggestion(origin + '/history-target'));
    await click(suggestion(origin + '/history-target'), 'choose-history-with-pointer');
    await until(() => nativeView('/history-target')?.getVisible() && !nativeView('/history-target').webContents.isLoading(), 'Pointer selection opens stored history URL');
    const selectedPage = nativeView('/history-target').webContents;
    const profile = await profileMenu('live-website', selectedPage);
    assert.equal(selectedPage.getURL(), origin + '/history-target');
    await capture('profile-dismissed-native-page-restored');
    await closeActiveTab('close-suggestion-journey-tab');
    assert.equal((await readSite()).tabs.length, tabsBeforeStart, 'Closing suggestion journey removes exactly its one tab');
    await openSite('Fixture Alpha', '/history-target');
    await click('[data-browser-library-trigger]', 'open-browser-library'); await assertLivePopover('browser library');
    await fill('[data-browser-library] input[type=search]', 'history-target');
    await waitPopover(url => document.querySelector(`[data-library-url="${url}"]`), origin + '/history-target');
    await capture('searched-browser-history');
    await click('[data-library-section="bookmarks"]', 'show-bookmarks');
    await fill('[data-browser-library] input[type=search]', 'alpha');
    await click(`[data-library-url="${origin}/alpha"]`, 'open-saved-bookmark');
    await until(() => nativeView('/alpha')?.getVisible() && !nativeView('/alpha').webContents.isLoading(), 'bookmark navigated');
    await click('[data-browser-library-trigger]', 'clear-fixture-history');
    await click('[data-library-section="history"]', 'history-section');
    await fill('[data-browser-library] input[type=search]', '');
    await click('[data-library-clear-history]', 'review-clear-history');
    await click('[data-library-confirm-clear]', 'confirm-clear-synthetic-history');
    await until(async () => (await evaluate(() => window.wuu.webWorkspace.library.getState())).history.length === 0, 'history cleared');
    const state = await evaluate(() => window.wuu.webWorkspace.library.getState());
    assert.ok(state.bookmarks.some(bookmark => bookmark.url === origin + '/alpha'), 'Clearing history retains bookmarks');
    clearedHistoryURL = origin + '/history-target';
    await capture('cleared-history-retained-bookmark'); xinput(['key', 'Escape']);
    return { history: state.history.length, bookmarks: state.bookmarks.length, keyboardAndPointerSuggestions: true, profile, gaze };
  });

  await testNative('failed website navigation retries requested destination', async () => {
    await openSite('Fixture Alpha', '/alpha'); fixture.controls.offline = true;
    await typeAddress(origin + '/offline', 'type-failing-local-address'); xinput(['key', '--clearmodifiers', 'Return']);
    await waitFor(() => document.querySelector('.web-workspace-stage:not([hidden]) .web-workspace-message[role=alert]'));
    await capture('failed-requested-destination');
    fixture.controls.offline = false;
    await click(pick(`${liveStage} .web-workspace-message button`, 'Try again'), 'retry-requested-destination');
    await until(() => nativeView('/offline')?.getVisible() && !nativeView('/offline').webContents.isLoading(), 'retry loads requested URL, not previous page');
    await capture('retried-requested-destination'); return { url: nativeView('/offline').webContents.getURL() };
  });

  await runBrowserMediaAcceptance({ win, report, output, started, save, testNative, openSite, pageClick, navigateAddress, readSite, nativeView, until, evaluate, fill, address, capture, xinput, delay }, fixture);

  await testNative('left tab close recreation and renderer restart preserve order pin and cookies', async () => {
    await ensureIndependentCookie('tab close and renderer restart');
    const wc = await openSite('Fixture Alpha', '/alpha');
    assert.ok((await wc.executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'));
    const backgroundOwner = await openSite('Fixture Beta', '/beta'), beforeClose = win.getBounds();
    await hoverTab(alphaID);
    try {
      await until(() => evaluateRibbon(id => { const close = document.querySelector(`[data-ribbon-close-id="${id}"]`); return close && getComputedStyle(close).visibility === 'visible' && Number(getComputedStyle(close).opacity) > 0.95; }, alphaID), 'Native background close control settles before clicking', 4000);
    } catch (error) {
      const pointer = spawnSync('xdotool', ['getmouselocation', '--shell'], { encoding: 'utf8', timeout: 5000 });
      report.backgroundCloseFailure = { pointer: pointer.stdout, overlayBounds: chromeView('ribbon')?.getBounds(),
        native: await evaluateRibbon(id => ({ hovered: [...document.querySelectorAll(':hover')].map(node => node.tagName + '.' + node.className),
          activeElement: document.activeElement?.outerHTML.slice(0, 300),
          close: document.querySelector(`[data-ribbon-close-id="${id}"]`)?.outerHTML }), alphaID) }; save(); throw error;
    }
    await click(`.sidebar-ribbon-close[data-ribbon-close-id="${alphaID}"]`, 'close-background-alpha');
    await until(() => wc.isDestroyed(), 'Closed left tab releases native contents');
    assert.equal((await readSite()).activeTabId, siteIDs.get('Fixture Beta'), 'Closing a background tab does not select it or change the active tab');
    await until(() => !backgroundOwner.isDestroyed() && nativeView('/beta')?.getVisible(), 'Background close keeps the active native page visible after leaving the rail');
    assert.deepEqual(win.getBounds(), beforeClose, 'Close control does not begin a native window drag');
    assert.ok(!(await readSite()).tabs.some(tab => tab.id === alphaID));
    alphaID = await addSite('Fixture Alpha Restored', '/alpha'); siteIDs.set('Fixture Alpha', alphaID);
    await tabMenu(alphaID, 'pin-restored-alpha');
    xinput(['key', 'Home', 'key', 'Return']);
    await waitFor(id => document.querySelector(`[data-ribbon-id="${id}"]`)?.dataset.pinned === 'true', alphaID);
    assert.ok((await contents('/alpha').executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'), 'Closing a tab never clears shared login/session data');
    const before = await prefs(), stateBefore = await readSite();
    const nativeBefore = views().map(view => view.webContents);
    const reloaded = new Promise(resolve => win.webContents.once('did-finish-load', resolve));
    win.webContents.reload();
    await Promise.race([reloaded, delay(30000).then(() => { throw new Error('Renderer reload did not finish'); })]);
    await waitFor(() => window.wuu && document.querySelector('.sidebar-ribbon-main'), undefined, 'reloaded workbench ready', 30000);
    await waitFor(count => document.querySelectorAll('[data-wuu-component="website-navigation-item"]').length === count, stateBefore.tabs.length);
    assert.deepEqual(await prefs(), before, 'Saved ribbon order survives renderer restart');
    const restored = await readSite();
    assert.deepEqual(restored.tabs.map(tab => ({ id: tab.id, url: tab.url, pinned: Boolean(tab.pinned) })), stateBefore.tabs.map(tab => ({ id: tab.id, url: tab.url, pinned: Boolean(tab.pinned) })), 'Left tabs and pins survive renderer restart');
    assert.ok(nativeBefore.every(wc => wc.isDestroyed()), 'Shell reload releases old native pages before lazy recreation');
    await evaluate(() => {
      const events = [], types = ['pointerover', 'pointerout', 'pointermove', 'focusin', 'focusout'];
      const observe = event => {
        const nav = document.querySelector('.sidebar-ribbon');
        events.push({ type: event.type, at: performance.now(), x: event.clientX, y: event.clientY,
          target: event.target instanceof Element ? event.target.outerHTML.slice(0, 200) : String(event.target),
          related: event.relatedTarget instanceof Element ? event.relatedTarget.outerHTML.slice(0, 160) : String(event.relatedTarget),
          expanded: nav?.dataset.expanded, hovered: nav?.matches(':hover'), focusedWithin: nav?.contains(document.activeElement) });
        if (events.length > 100) events.shift();
      };
      types.forEach(type => document.addEventListener(type, observe, true));
      window.__wuuRestartInputProbe = { events, stop: () => types.forEach(type => document.removeEventListener(type, observe, true)) };
    });
    try {
      await openSite('Fixture Alpha Restored', '/alpha');
      assert.ok((await contents('/alpha').executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'));
    } finally {
      report.restartInteraction = await evaluate(() => {
        const nav = document.querySelector('.sidebar-ribbon'), probe = window.__wuuRestartInputProbe;
        const result = { events: probe.events, expanded: nav?.dataset.expanded, hovered: nav?.matches(':hover'),
          focusedWithin: nav?.contains(document.activeElement), activeElement: document.activeElement?.outerHTML.slice(0, 300),
          layers: [...document.querySelectorAll('[role="tooltip"],.sidebar-account-menu,.thread-row-context-menu')].map(node => node.outerHTML.slice(0, 240)) };
        probe.stop(); delete window.__wuuRestartInputProbe; return result;
      }).catch(error => ({ unavailable: String(error) }));
      report.restartInteraction.nativeViews = views().map(view => ({ id: view.webContents.id, url: view.webContents.getURL(), visible: view.getVisible(), focused: view.webContents.isFocused(), bounds: view.getBounds() }));
      report.restartInteraction.nativeChildren = win.getChildWindows().map(child => ({ title: child.getTitle(), visible: child.isVisible(), focusable: child.isFocusable(), url: child.webContents.getURL() }));
      save();
    }
    const library = await evaluate(() => window.wuu.webWorkspace.library.getState());
    if (clearedHistoryURL) {
      assert.ok(!library.history.some(entry => entry.url === clearedHistoryURL), 'Renderer restart does not restore cleared history');
      assert.ok(library.bookmarks.some(entry => entry.url === origin + '/alpha'), 'Bookmark survives renderer restart');
    }
    await capture('left-tabs-after-restart'); return { before, restoredTabs: restored.tabs, sharedCookieRetained: true };
  });

  await testNative('authored extension uses shared native website and unloads cleanly', async () => {
    await ensureIndependentCookie('authored extension shared browser profile');
    await click(plugins, 'open-extension-inventory'); await hidden('extension inventory');
    await click(pick('.sidebar-section-item', 'Plugins'), 'select-plugin-inventory');
    await waitFor(() => document.querySelector('.catalog-row-open[aria-label*="Acceptance Webpage"]'));
    await click('.catalog-row-open[aria-label*="Acceptance Webpage"]', 'review-authored-extension');
    await click(pick('.settings-detail-actions button', 'Approve and enable'), 'approve-authored-local-fixture');
    await waitFor(() => document.querySelector('.sidebar-ribbon-main button[aria-label="Fixture Website"]'), undefined, 'production extension activation', 20000);
    const wc = await openSite('Fixture Website', '/extension');
    security(wc);
    assert.ok((await wc.executeJavaScript('document.cookie')).includes('wuu_acceptance=synthetic-session'));
    await pageClick(wc, '#draft', 'type-retained-extension-draft'); await wc.insertText('Retain this extension draft across Chat');
    await capture('extension-native-website');
    await click(conversations, 'extension-to-chat'); await hidden('extension off-stage');
    assert.ok(!wc.isDestroyed(), 'Hidden plugin page remains alive');
    const restored = await openSite('Fixture Website', '/extension');
    assert.equal(restored.id, wc.id, 'Returning activates the exact existing plugin page');
    assert.equal(await restored.executeJavaScript('document.querySelector("#draft").value'), 'Retain this extension draft across Chat');
    await click(plugins, 'disable-extension-inventory');
    await click(pick('.sidebar-section-item', 'Plugins'), 'plugin-details-for-disable');
    if (await evaluate(() => !!document.querySelector('.catalog-row-open[aria-label*="Acceptance Webpage"]'))) await click('.catalog-row-open[aria-label*="Acceptance Webpage"]', 'open-enabled-fixture');
    await click('button[role="switch"][aria-label="Turn off Acceptance Webpage"]', 'disable-authored-extension');
    await waitFor(() => !document.querySelector('.sidebar-ribbon-main button[aria-label="Fixture Website"]'));
    await until(() => !contents('/extension'), 'disabled extension native contents destroyed');
    await capture('extension-disabled-cleanly');
    assert.ok(wc.isDestroyed());
    await click('button[role="switch"][aria-label="Turn on Acceptance Webpage"]', 're-enable-authored-extension');
    await waitFor(() => document.querySelector('.sidebar-ribbon-main button[aria-label="Fixture Website"]'), undefined, 'fixture re-enabled', 20000);
    const replacement = await openSite('Fixture Website', '/extension');
    assert.notEqual(replacement.id, wc.id, 'Re-enabled generation gets a new page');
    assert.equal(views().filter(view => view.webContents.getURL() === origin + '/extension').length, 1, 'Exactly one replacement plugin native page');
    return { sharedCookieRetained: true, hiddenPageRetained: true, retainedDraft: true, oldGenerationDestroyed: true, singleReplacementPage: true };
  });
  // Public availability is diagnostic; actual tab ownership, chrome and return
  // behavior remain product gates even when a service blocks anonymous access.
  const publicSites = [
    ['Bilibili', 'https://www.bilibili.com/'], ['Notion', 'https://www.notion.so/'],
    ['Feishu', 'https://www.feishu.cn/'], ['X-Twitter', 'https://x.com/'],
  ].map(([name, requestedURL]) => ({ name, requestedURL, required: false, status: 'not-observed', accountUsed: false, events: [], visits: [] }));
  report.optionalEvidence = publicSites;
  const publicPages = new Map(), releasePublicObservers = [];
  async function publicSurface(site, wc) {
    const state = await readSite();
    if (state.activeTabId !== site.tabID || wc.isDestroyed()) return null;
    if (state.error) {
      const visibleError = await evaluate(error => [...document.querySelectorAll('.web-workspace-stage:not([hidden]) [role="alert"]')]
        .some(node => node.getBoundingClientRect().width > 0 && node.textContent.includes(error)), state.error);
      return visibleError ? { kind: 'wuu-navigation-error', url: state.url, error: state.error } : null;
    }
    return win.contentView.children.some(view => view.webContents === wc && view.getVisible()) ? { kind: 'native-public-page', url: state.url } : null;
  }
  async function observePublicDocument(site, wc) {
    site.document = await Promise.race([
      wc.executeJavaScript(`(() => {
        const visible = e => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(e).visibility !== 'hidden'; };
        return { title: document.title, ready: document.readyState, text: document.body?.innerText.slice(0, 1600) || '',
          loginFormVisible: [...document.querySelectorAll('input[type=email],input[type=password]')].some(visible) };
      })()`).catch(error => ({ unavailable: String(error) })),
      delay(800).then(() => ({ unavailable: 'Public document observation exceeded 800 ms' })),
    ]);
    site.finalURL = wc.getURL(); site.loading = wc.isLoadingMainFrame();
    site.surface = await publicSurface(site, wc);
    const terminal = site.events.findLast(event => event.event === 'navigate' || event.event === 'failed' && event.code !== -3);
    site.status = site.surface?.error || terminal?.event === 'failed' ? 'network-failed'
      : terminal?.code >= 400 || /captcha|verify (?:you are|your identity)|access denied|unusual traffic|人机验证|安全验证/i.test(site.document.text || '') ? 'blocked-or-challenge'
      : site.document.loginFormVisible || /\/(?:login|signin|i\/flow\/login)(?:[/?#]|$)/.test(site.finalURL) ? 'public-login-visible'
      : site.document.text ? 'public-document-visible' : site.loading ? 'loading-at-observation-limit' : 'document-unavailable';
    save();
  }
  try {
    for (const site of publicSites) await testNative(`public ${site.name} opens through the real left plus and address`, async () => {
      const created = [];
      const observe = (_event, wc) => {
        if (wc === win.webContents || wc.session !== session.fromPartition('persist:wuu-browser')) return;
        created.push(wc);
        const add = detail => { if (site.events.length < 80) site.events.push({ ...detail, atMs: Date.now() - started }); };
        const navigated = (_event, url, code) => add({ event: 'navigate', id: wc.id, url, code });
        const failed = (_event, code, description, url, mainFrame) => { if (mainFrame) add({ event: 'failed', id: wc.id, code, description, url }); };
        wc.on('did-navigate', navigated); wc.on('did-fail-load', failed);
        releasePublicObservers.push(() => { wc.removeListener('did-navigate', navigated); wc.removeListener('did-fail-load', failed); });
      };
      app.on('web-contents-created', observe);
      try {
        site.tabID = await addSite(`Public ${site.name}`, site.requestedURL);
        await until(() => created.some(wc => !wc.isDestroyed() && win.contentView.children.some(view => view.webContents === wc)), `${site.name}: actual native page is attached`, 4000);
        const wc = created.find(wc => !wc.isDestroyed() && win.contentView.children.some(view => view.webContents === wc));
        publicPages.set(site, wc); site.nativeContentsID = wc.id;
        const preferences = wc.getLastWebPreferences();
        assert.ok(preferences.sandbox && preferences.contextIsolation && !preferences.nodeIntegration, 'Public pages retain the shipping browser isolation');
        const deadline = Date.now() + 6000;
        // A new native tab exists briefly at about:blank before its requested
        // navigation starts; that provisional idle state is not a loaded site.
        while ((wc.getURL() === 'about:blank' || wc.isLoadingMainFrame()) && Date.now() < deadline) await delay(100);
        await until(() => publicSurface(site, wc), `${site.name}: page or matching Wuu navigation error is presented`, 4000);
        await observePublicDocument(site, wc);
        await capture(`public-${site.name}-entry-${site.status}`);
        return { tabID: site.tabID, nativeContentsID: wc.id, requestedURL: site.requestedURL, externalStatus: site.status };
      } finally { app.removeListener('web-contents-created', observe); }
    });
    for (const round of [1, 2]) await testNative(`public website switch circuit ${round} preserves native pages and usable chrome`, async () => {
      await click(conversations, `public-circuit-${round}-return-chat`, false);
      await until(() => [...publicPages.values()].every(wc => !wc.isDestroyed() && !win.contentView.children.find(view => view.webContents === wc)?.getVisible()), 'Chat hides public native pages without destroying them');
      const circuit = round === 1 ? publicSites : [...publicSites].reverse();
      for (const site of circuit) {
        const wc = publicPages.get(site);
        if (!wc) continue; // Its independent real-entry check already records the failure.
        const view = win.contentView.children.find(view => view.webContents === wc);
        assert.ok(view && !wc.isDestroyed(), `${site.name}: returning retains the original native page`);
        const visit = { round, nativeContentsID: wc.id, visibility: [], atMs: Date.now() - started };
        site.visits.push(visit);
        const originalVisible = view.setVisible;
        view.setVisible = function (visible) { visit.visibility.push({ visible, atMs: Date.now() - started }); return originalVisible.call(this, visible); };
        try {
          await click(tabButton(site.tabID), `public-${site.name}-visit-${round}`, false);
          await until(() => publicSurface(site, wc), `${site.name}: real tab click selects its original page or navigation error`);
          assert.ok([...publicPages.values()].filter(other => other !== wc).every(other => !win.contentView.children.find(candidate => candidate.webContents === other)?.getVisible()), 'No previous public page remains above the selected page');
          const bounds = view.getBounds();
          await focusAddress(`public-${site.name}-address-focus-${round}`);
          if (round === 1) {
            await hoverTab(site.tabID);
            assert.ok(await publicSurface(site, wc), 'Rail hover preserves the public page or navigation error');
            assert.deepEqual(view.getBounds(), bounds, 'Rail hover does not resize the public page');
            await capture(`public-${site.name}-hover-rail`);
            await finishRailInteraction(null, `public-${site.name}-hover`);
            const popup = Menu.prototype.popup;
            Menu.prototype.popup = function (...args) { visit.menu = this.items.map(item => ({ label: item.label, enabled: item.enabled })); return popup.apply(this, args); };
            try {
              await click(toolbar('Browser options'), `public-${site.name}-options`, false);
              await until(() => visit.menu, 'Real native browser options menu opens');
              assert.ok(await publicSurface(site, wc), 'Browser menu preserves the public page or navigation error');
              assert.deepEqual(view.getBounds(), bounds, 'Browser menu does not resize the public page');
              await capture(`public-${site.name}-browser-options`);
              xinput(['key', '--clearmodifiers', 'Escape']);
            } finally { Menu.prototype.popup = popup; }
            await waitFor(id => document.querySelector(`[data-website-id="${id}"] button[aria-label="Browser options"]`)?.getAttribute('aria-expanded') === 'false', ribbonGroup);
            await focusAddress(`public-${site.name}-focus-after-dismissal`);
          }
          await observePublicDocument(site, wc);
          visit.finalURL = wc.getURL(); visit.externalStatus = site.status; visit.bounds = view.getBounds();
          await capture(`public-${site.name}-return-${round}-${site.status}`);
          const revealed = visit.visibility.findIndex(event => event.visible);
          if (site.surface?.kind === 'native-public-page') assert.ok(revealed >= 0 && visit.visibility.slice(revealed).every(event => event.visible), 'Returning and using chrome never hide/reveal the selected public page again');
          else visit.flickerObservation = 'External navigation failed; native page visibility is not asserted for the Wuu error surface';
        } finally { view.setVisible = originalVisible; save(); }
      }
      return { direction: round === 1 ? 'forward' : 'reverse', sites: publicSites.map(site => ({ name: site.name, status: site.status, visits: site.visits.length })), accountsUsed: false };
    });
  } finally { releasePublicObservers.forEach(release => release()); }
  await goConversations();
  report.websiteFixture = { origin, requestCount: fixture.requests.length, externalAccounts: false, authoredExtension: true };
}
module.exports = { createWebsiteFixture, runWebsiteAcceptance };
