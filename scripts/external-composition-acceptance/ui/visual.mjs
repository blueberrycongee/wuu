import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { extname, join, resolve, sep } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = fileURLToPath(new URL('.', import.meta.url));
const root = resolve(here, '../../..');
const output = resolve(process.env.WUU_FEATURE_UI_VISUAL_OUTPUT ?? join(root, '.tmp/e2e/feature-ui-rendering'));
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const temporary = await mkdtemp(join(tmpdir(), 'wuu-feature-ui-pixels-'));
await mkdir(output, { recursive: true });
const report = { passed: false, sourceRevision: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
  sourceDirty: !!execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim(),
  scope: 'Actual Chromium pixels, production PluginHost / goal-ui / automation-ui / styles, independent surface bindings, deterministic synthetic services. Not real-core or packaged desktop acceptance.',
  inputs: {}, build: [], browser: null, cases: [], screenshots: [], runtimeErrors: [],
};
let browser, browserExit, server, cdp, browserSession;
let browserLog = '';
async function files(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return (await Promise.all(entries.map(entry => entry.isDirectory() ? files(join(directory, entry.name)) : [join(directory, entry.name)]))).flat().sort();
}
try {
  for (const file of ['visual-preview.tsx', 'visual.html', 'visual.mjs']) report.inputs[file] = hash(await readFile(join(here, file)));
  for (const file of ['goal-ui', 'automation-ui']) report.inputs[`${file}/desktop.js`] = hash(await readFile(join(root, `internal/plugin/bundled/${file}/desktop.js`)));
  const { build } = await import(pathToFileURL(join(root, 'desktop/node_modules/vite/dist/node/index.js')).href);
  const built = join(temporary, 'build');
  await build({ configFile: false, root: here, base: './', logLevel: 'warn',
    resolve: { alias: { react: join(root, 'desktop/node_modules/react'), 'react-dom': join(root, 'desktop/node_modules/react-dom') } },
    esbuild: { jsx: 'automatic' },
    build: { target: 'esnext', outDir: built, emptyOutDir: true, rollupOptions: { input: join(here, 'visual.html') } },
  });
  for (const file of await files(built)) report.build.push({ file: file.slice(built.length + 1), sha256: hash(await readFile(file)) });
  if (process.argv.includes('--build-only')) {
    await writeFile(join(output, 'build-only.json'), JSON.stringify({ ...report, buildPassed: true, pixelAcceptance: 'not run' }, null, 2));
    console.log('Feature UI browser bundle built; pixel acceptance was not run.');
  } else {
    const executable = process.env.WUU_CHROMIUM;
    assert.ok(executable, 'WUU_CHROMIUM must name an installed Chromium/Google Chrome executable; missing browser is a failure, not a skip');
    server = createServer(async (request, response) => {
      const pathname = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
      const file = resolve(built, `.${pathname === '/' ? '/visual.html' : pathname}`);
      if (!file.startsWith(built + sep)) { response.writeHead(403).end(); return; }
      try {
        const content = await readFile(file);
        const mime = { '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.woff2': 'font/woff2' }[extname(file)] ?? 'application/octet-stream';
        response.writeHead(200, { 'content-type': mime, 'cache-control': 'no-store' }); response.end(content);
      } catch { response.writeHead(404).end(); }
    });
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    const url = `http://127.0.0.1:${server.address().port}/visual.html`;
    // CDP uses inherited pipes, not a public debugging listener. The profile is
    // disposable; the browser sandbox stays enabled.
    browser = spawn(executable, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
      '--remote-debugging-pipe', `--user-data-dir=${join(temporary, 'profile')}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe', 'pipe', 'pipe'] });
    browserExit = new Promise(resolve => browser.once('exit', resolve));
    browser.stderr.on('data', bytes => { browserLog += bytes.toString(); });
    const pending = new Map(); let sequence = 0, buffer = '';
    const failPending = error => { for (const entry of pending.values()) { clearTimeout(entry.timer); entry.reject(error); } pending.clear(); };
    browser.on('error', failPending);
    browser.on('exit', (code, signal) => failPending(new Error(`Chromium exited (${code ?? signal}); see browser.log`)));
    browser.stdio[3].on('error', failPending);
    browser.stdio[4].setEncoding('utf8');
    browser.stdio[4].on('data', bytes => {
      buffer += bytes.toString();
      for (;;) {
        const end = buffer.indexOf('\0'); if (end < 0) break;
        const message = JSON.parse(buffer.slice(0, end)); buffer = buffer.slice(end + 1);
        if (message.method === 'Runtime.exceptionThrown') report.runtimeErrors.push(message.params.exceptionDetails);
        const entry = pending.get(message.id); if (!entry) continue;
        pending.delete(message.id); clearTimeout(entry.timer);
        message.error ? entry.reject(new Error(`${entry.method}: ${JSON.stringify(message.error)}`)) : entry.resolve(message.result);
      }
    });
    cdp = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
      const id = ++sequence;
      const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, 20_000);
      pending.set(id, { resolve, reject, timer, method });
      browser.stdio[3].write(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }) + '\0');
    });
    report.browser = await cdp('Browser.getVersion');
    const { targetId } = await cdp('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await cdp('Target.attachToTarget', { targetId, flatten: true });
    browserSession = sessionId;
    const command = (method, params) => cdp(method, params, sessionId);
    await command('Runtime.enable'); await command('Page.enable');
    await command('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
    await command('Emulation.setTimezoneOverride', { timezoneId: 'UTC' });
    const evaluate = async expression => {
      const value = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
      if (value.exceptionDetails) throw new Error(value.exceptionDetails.exception?.description ?? value.exceptionDetails.text);
      return value.result.value;
    };
    const until = async (expression, description) => {
      const deadline = Date.now() + 15_000;
      do {
        if (await evaluate(`Boolean(${expression})`)) return;
        await new Promise(resolve => setTimeout(resolve, 50));
      } while (Date.now() < deadline);
      throw new Error(`Browser state timed out: ${description}`);
    };
    const key = async (key, code = key, virtual = key === 'Escape' ? 27 : 9) => {
      await command('Input.dispatchKeyEvent', { type: 'keyDown', key, code, windowsVirtualKeyCode: virtual });
      await command('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: virtual });
    };
    const click = async selector => {
      const point = await evaluate(`(() => {
        const element = document.querySelector(${JSON.stringify(selector)}); if (!element) throw Error('Missing click target');
        element.scrollIntoView({ block:'nearest', inline:'nearest' });
        const rect = element.getBoundingClientRect(); const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
        if (!rect.width || !rect.height || !element.contains(document.elementFromPoint(x, y))) throw Error('Click target is clipped or occluded');
        return { x, y };
      })()`);
      await command('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', clickCount: 1 });
      await command('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', clickCount: 1 });
    };
    const capture = async (name, scenario) => {
      await evaluate('document.fonts.ready.then(() => true)');
      await evaluate('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
      const metrics = await evaluate(`(() => {
        const selectors = ['html','body','[data-external-shell]','[data-external-navigation]','[data-external-composer]','[data-preview-catalog]', '.plugin-automation','.plugin-goal','.plugin-automation-main','.plugin-automation-detail-body','.plugin-automation-detail'];
        const regions = selectors.flatMap(selector => [...document.querySelectorAll(selector)].filter(el => el.getClientRects().length).map(el => {
          const r = el.getBoundingClientRect(); const style = getComputedStyle(el);
          return { selector, x:r.x, y:r.y, width:r.width, height:r.height, clientWidth:el.clientWidth, scrollWidth:el.scrollWidth, clientHeight:el.clientHeight, scrollHeight:el.scrollHeight, scrollTop:el.scrollTop, fontSize:style.fontSize, fontFamily:style.fontFamily };
        }));
        const focused = document.activeElement; const style = getComputedStyle(focused);
        return { viewport:{ width:innerWidth, height:innerHeight, devicePixelRatio }, theme:document.documentElement.dataset.theme, regions,
          focus:{ tag:focused.tagName, label:focused.getAttribute('aria-label'), className:focused.className, visible:focused.matches(':focus-visible'), outline:style.outline, boxShadow:style.boxShadow },
          diagnostics:window.featureUI.diagnostics(), requests:window.featureUI.requests };
      })()`);
      // Check inner scrolling regions too: overflow:hidden on the document must
      // not turn a clipped toolbar or editor into an apparent layout pass.
      const overflow = metrics.regions.filter(region => region.scrollWidth > region.clientWidth + 1);
      const { data } = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false, fromSurface: true });
      const png = Buffer.from(data, 'base64');
      assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
      assert.equal(png.readUInt32BE(16), scenario.width);
      assert.equal(png.readUInt32BE(20), scenario.height);
      await writeFile(join(output, `${name}.png`), png);
      await writeFile(join(output, `${name}.json`), JSON.stringify(metrics, null, 2));
      report.screenshots.push({ name, scenario, sha256: hash(png), width:png.readUInt32BE(16), height:png.readUInt32BE(20), ...metrics.viewport });
      assert.deepEqual(overflow, [], `${name}: horizontal content is clipped or overflowing`);
      assert.equal(metrics.regions.find(region => region.selector === '.plugin-automation')?.fontSize, `${scenario.font}px`, `${name}: requested UI font size must reach the production feature`);
      assert.deepEqual(metrics.diagnostics.filter(item => item.kind !== 'runtime'), [], `${name}: renderer/activation failure`);
      assert.deepEqual(report.runtimeErrors, [], `${name}: uncaught browser exception`);
      return metrics;
    };
    for (const scene of [
      { id:'light-wide', scenario:'populated', theme:'light', font:15, width:1280, height:900 },
      { id:'dark-narrow-long', scenario:'long', theme:'dark', font:20, width:480, height:960 },
      { id:'empty', scenario:'empty', theme:'light', font:15, width:1280, height:900 },
      { id:'unavailable', scenario:'populated', theme:'dark', font:20, width:480, height:960 },
    ]) {
      await command('Emulation.setDeviceMetricsOverride', { width:scene.width, height:scene.height, deviceScaleFactor:1, mobile:false });
      await command('Page.navigate', { url:`${url}?${new URLSearchParams({ scenario:scene.scenario, theme:scene.theme, font:String(scene.font) })}` });
      await until(`window.featureUI?.ready && document.querySelector('[data-external-shell]') && ${scene.scenario === 'empty' ? "document.querySelector('.plugin-automation-empty') && !document.querySelector('.plugin-goal')" : "document.querySelectorAll('.plugin-automation-item').length === window.featureUI.taskCount && document.querySelector('.plugin-goal') && document.querySelector('.plugin-automation-sidebar-item')"}`, 'production feature modules loaded');
      await capture(`${scene.id}-catalog`, scene);
      if (scene.id === 'unavailable') {
        await evaluate('window.featureUI.disableProviders()');
        await until("document.querySelector('.plugin-goal [role=alert]') && document.querySelector('.plugin-automation [role=alert]') && document.querySelector('.plugin-automation-create').disabled", 'provider errors disable writes');
        assert.ok(await evaluate("[...document.querySelectorAll('.plugin-goal .plugin-goal-icon-button, .plugin-automation-item input')].every(el => el.matches(':disabled'))"));
        await click('.plugin-goal button[aria-expanded]');
        await capture('unavailable-goal-expanded', scene);
        await click('.plugin-automation-item-main');
        await until("document.querySelector('.plugin-automation-detail input')?.matches(':disabled')", 'stale editor is read-only');
        assert.ok(await evaluate("[...document.querySelectorAll('.plugin-automation-detail input, .plugin-automation-detail textarea, .plugin-automation-detail button[type=submit]')].every(el => el.matches(':disabled'))"));
        await capture('unavailable-editor-disabled', scene);
        assert.ok(await evaluate("window.featureUI.requests.every(call => ['get_goal','automation.list','automation.run.list'].includes(call.input.method))"));
        report.cases.push(`${scene.id}: both provider errors, stale editor and goal mutations disabled, no mutation/fallback call`);
        continue;
      }
      if (scene.scenario === 'long') {
        assert.ok(await evaluate("document.querySelector('.plugin-automation-main').scrollHeight > document.querySelector('.plugin-automation-main').clientHeight"));
        await evaluate("document.querySelector('.plugin-automation-main').scrollTop = document.querySelector('.plugin-automation-main').scrollHeight; document.querySelector('[data-external-navigation]').scrollTop = document.querySelector('[data-external-navigation]').scrollHeight");
        await capture('dark-narrow-long-scrolled', scene);
        await evaluate("document.querySelector('.plugin-automation-main').scrollTop = 0");
      }
      if (scene.scenario !== 'empty') {
        await click('.plugin-goal button[aria-expanded]');
        await until("document.querySelector('.plugin-goal-details')", 'goal expanded');
        await capture(`${scene.id}-goal-expanded`, scene);
        await click('.plugin-goal button[aria-expanded]');
      }
      // Real keyboard navigation enters the editor, then Escape must dismiss it
      // and restore focus before the same trigger is used again.
      await click('.plugin-automation-create');
      await until("document.activeElement?.matches('.plugin-automation-form-name')", 'new editor takes focus');
      await key('Tab');
      await capture(`${scene.id}-editor-focused`, scene);
      await key('Escape');
      await until("!document.querySelector('.plugin-automation-detail') && document.activeElement?.matches('.plugin-automation-create')", 'Escape closes and restores focus');
      const focused = await capture(`${scene.id}-escape-focus`, scene);
      assert.ok(focused.focus.visible && !focused.focus.outline.startsWith('none') && !focused.focus.outline.includes(' 0px'), 'Restored keyboard focus must be visible');
      await key('Enter', 'Enter', 13);
      await until("document.activeElement?.matches('.plugin-automation-form-name')", 'editor reopens from restored focus');
      await capture(`${scene.id}-editor-reopened`, scene);
      await click('.plugin-automation-detail-close');
      await until("!document.querySelector('.plugin-automation-detail')", 'Close dismisses reopened editor');
      report.cases.push(`${scene.id}: production surfaces, scrolling/empty state, editor focus, Escape, reopen and Close; no horizontal overflow`);
    }
    assert.equal(report.cases.length, 4);
    assert.ok(report.screenshots.length >= 16);
    report.passed = true;
  }
} catch (error) {
  report.error = error.stack ?? String(error);
  if (browserSession && browser?.exitCode === null && browser?.signalCode === null) {
    try {
      const { data } = await cdp('Page.captureScreenshot', { format:'png', fromSurface:true }, browserSession);
      const png = Buffer.from(data, 'base64');
      await writeFile(join(output, 'failure.png'), png);
      report.failureScreenshotSHA256 = hash(png);
    } catch (captureError) { report.failureCaptureError = String(captureError); }
  }
  throw error;
} finally {
  if (browser?.pid && browser.exitCode === null && browser.signalCode === null) {
    // Chrome persists its profile on shutdown. Close through CDP before any
    // signal fallback, then wait for exit instead of racing profile writers.
    try { await cdp('Browser.close'); }
    catch (error) { browserLog += `\nBrowser.close: ${String(error)}\n`; }
    const waitForExit = milliseconds => new Promise(resolve => {
      const timer = setTimeout(() => resolve(false), milliseconds);
      browserExit.then(() => { clearTimeout(timer); resolve(true); });
    });
    if (!await waitForExit(3000)) {
      browser.kill('SIGTERM');
      if (!await waitForExit(3000)) { browser.kill('SIGKILL'); await browserExit; }
    }
  }
  if (server) await new Promise(resolve => server.close(resolve));
  await writeFile(join(output, 'browser.log'), browserLog);
  try { await rm(temporary, { recursive:true, force:true, maxRetries:8, retryDelay:100 }); }
  catch (error) {
    report.cleanupError = String(error);
    // A cleanup failure must not replace the original rendering failure.
    if (!report.error) { report.passed = false; report.error = error.stack ?? String(error); process.exitCode = 1; }
  }
  if (!process.argv.includes('--build-only') || report.error) await writeFile(join(output, 'report.json'), JSON.stringify(report, null, 2));
}
if (report.passed) console.log(`Feature UI Chromium acceptance passed: ${report.cases.length} scenarios, ${report.screenshots.length} screenshots. Evidence: ${output}`);
