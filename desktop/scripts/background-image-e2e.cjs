// Start Vite in desktop/, then run: electron scripts/background-image-e2e.cjs
// Uses an isolated profile, synthetic images, and real IndexedDB/worker rendering.
const { app, BrowserWindow } = require("electron");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const output = path.resolve(__dirname, "../../artifacts/background-image");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5189";
const fixture = `${origin}/dev/three-pane/?background`;
const preferences = `await import('/src/renderer/background/preferences.ts')`;
const windows = [];
function createWindow() {
  const win = new BrowserWindow({ show: false, width: 1440, height: 960,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  windows.push(win);
  return win;
}
function evaluate(win, body) { return win.webContents.executeJavaScript(`(async () => { ${body} })()`); }
function waitFor(win, expression) {
  return evaluate(win, `return new Promise((resolve, reject) => {
    const deadline = performance.now() + 15000;
    function check() {
      if (${expression}) return resolve(true);
      if (performance.now() > deadline) return reject(new Error('Timed out: ' + ${JSON.stringify(expression)}));
      requestAnimationFrame(check);
    }
    check();
  });`);
}
async function installSynthetic(win, corrupt = false) {
  await waitFor(win, `document.querySelector('input[type=file]') && !document.querySelector('input[type=file]').disabled`);
  await evaluate(win, `
    const canvas = document.createElement('canvas'); canvas.width = 1200; canvas.height = 800;
    const ctx = canvas.getContext('2d');
    const gradient = ctx.createLinearGradient(0, 0, 1200, 800);
    gradient.addColorStop(0, '#e52e71'); gradient.addColorStop(0.5, '#2979ff'); gradient.addColorStop(1, '#00b894');
    ctx.fillStyle = gradient; ctx.fillRect(0, 0, 1200, 800);
    const blob = ${corrupt ? "new Blob(['not an image'])" : "await new Promise(resolve => canvas.toBlob(resolve))"};
    const transfer = new DataTransfer(); transfer.items.add(new File([blob], 'synthetic.png', { type: 'image/png' }));
    const input = document.querySelector('input[type=file]'); input.files = transfer.files;
    input.dispatchEvent(new Event('change', { bubbles: true }));
  `);
}
async function settle(win) {
  await evaluate(win, `await Promise.all(document.getAnimations().filter(a => a.effect?.target?.classList?.contains('app-background')).map(a => a.finished.catch(() => {})));
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));`);
}
function average(image, rect, viewportWidth) {
  // capturePage may return physical pixels even when its scale factor is 1.
  const scale = image.getSize().width / viewportWidth;
  const pixels = image.crop(Object.fromEntries(Object.entries(rect).map(([key, value]) => [key, Math.round(value * scale)]))).toBitmap();
  const result = [0, 0, 0];
  for (let i = 0; i < pixels.length; i += 4) for (let c = 0; c < 3; c++) result[c] += pixels[i + c];
  return result.map(value => value / (pixels.length / 4));
}

app.whenReady().then(async () => {
  // Exercise the bundled worker under the product's file origin and CSP too.
  const built = path.resolve(__dirname, "../out/renderer");
  const production = createWindow();
  await production.loadFile(path.join(built, "index.html"));
  const workerFile = fs.readdirSync(path.join(built, "assets")).find(name => /^image\.worker-.*\.js$/.test(name));
  assert(workerFile, "Build must emit the image worker");
  const workerURL = pathToFileURL(path.join(built, "assets", workerFile)).href;
  const processed = await evaluate(production, `
    const canvas = document.createElement('canvas'); canvas.width = 4096; canvas.height = 512;
    const source = canvas.getContext('2d');
    source.fillStyle = '#f4d4a0'; source.fillRect(0, 0, 2048, 512);
    source.fillStyle = '#006d77'; source.fillRect(2048, 0, 2048, 512);
    const image = await new Promise(resolve => canvas.toBlob(resolve));
    const worker = new Worker(${JSON.stringify(workerURL)}, { type: 'module' });
    try {
      return await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Bundled worker timed out')), 10000);
        worker.onerror = event => { clearTimeout(timeout); reject(new Error(event.message)); };
        worker.onmessage = async event => {
          clearTimeout(timeout);
          if (!event.data.image) return reject(new Error('Worker did not return an image'));
          try {
            const bitmap = await createImageBitmap(event.data.image);
            const output = document.createElement('canvas'); output.width = bitmap.width; output.height = bitmap.height;
            const ctx = output.getContext('2d'); ctx.drawImage(bitmap, 0, 0);
            const pixels = ctx.getImageData(0, 0, bitmap.width, bitmap.height).data;
            const sample = x => [...ctx.getImageData(x, 128, 1, 1).data];
            let minLuminance = 1, opaque = true;
            for (let i = 0; i < pixels.length; i += 4) {
              const rgb = [...pixels.slice(i, i + 3)].map(v => v / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4);
              minLuminance = Math.min(minLuminance, rgb[0] * .2126 + rgb[1] * .7152 + rgb[2] * .0722);
              opaque &&= pixels[i + 3] === 255;
            }
            resolve({ width: bitmap.width, height: bitmap.height, left: sample(100), middle: sample(1024), right: sample(1900), opaque, minLuminance }); bitmap.close();
          } catch (error) { reject(error); }
        };
        worker.postMessage({ image, purpose: 'light' });
      });
    } finally { worker.terminate(); }
  `);
  assert.equal(processed.width, 2048); assert.equal(processed.height, 256);
  assert(processed.opaque, "Gaussian padding must keep image edges opaque");
  assert.notDeepEqual(processed.middle, processed.left);
  assert.notDeepEqual(processed.middle, processed.right, "Gaussian blur must soften a hard boundary");
  assert(processed.minLuminance >= 0.235, "Local shell treatment must support readable dark text");
  const win = createWindow();
  await win.loadURL(`${fixture}&theme=light&size=14`);
  await waitFor(win, `document.querySelector('input[type=file]') && !document.querySelector('input[type=file]').disabled`);
  const baseline = await win.webContents.capturePage();
  await installSynthetic(win);
  await waitFor(win, `document.documentElement.hasAttribute('data-shell-skin')`);
  await settle(win);
  const rendered = await win.webContents.capturePage();
  const regions = await evaluate(win, `return ['.sidebar', '.conversation-pane', '.workspace-right-panel'].map(selector => {
    const rect = document.querySelector(selector).getBoundingClientRect();
    return { x: Math.round(rect.x + 10), y: Math.round(rect.bottom - 80), width: 12, height: 12 };
  });`);
  for (const [i, region] of regions.entries()) {
    const before = average(baseline, region, win.getContentSize()[0]), after = average(rendered, region, win.getContentSize()[0]);
    assert(before.every((value, c) => Math.abs(value - after[c]) < 1), `Content pane ${i} must retain its own surface`);
  }
  const saved = await evaluate(win, `const { readBackground } = ${preferences}; const p = await readBackground(); return { id: p.imageID, size: p.image.size };`);
  assert(saved.size > 0);
  await installSynthetic(win, true);
  await waitFor(win, `document.querySelector('[role=alert]')`);
  assert.equal(await evaluate(win, `const { readBackground } = ${preferences}; return (await readBackground()).imageID;`), saved.id);

  // Aborting the write must not retire the last good image or send notifications.
  const rollback = await evaluate(win, `
    const { readBackground, updateBackground } = ${preferences};
    const original = IDBObjectStore.prototype.put; let events = 0;
    const listener = () => events++; window.addEventListener('wuu-background-change', listener);
    IDBObjectStore.prototype.put = function() { this.transaction.abort(); };
    let rejected = false;
    try { await updateBackground(p => ({ ...p, imageID: 'failed-replacement' })); } catch { rejected = true; }
    finally { IDBObjectStore.prototype.put = original; window.removeEventListener('wuu-background-change', listener); }
    return { rejected, events, id: (await readBackground()).imageID };
  `);
  assert.deepEqual(rollback, { rejected: true, events: 0, id: saved.id });

  const second = createWindow();
  await second.loadURL(`${fixture}&theme=light`);
  await waitFor(second, `document.documentElement.hasAttribute('data-shell-skin')`);
  await win.loadURL(`${fixture}&theme=dark&size=20`);
  await waitFor(win, `document.documentElement.hasAttribute('data-shell-skin')`);

  for (const theme of ["light", "dark"]) for (const size of [14, 20]) for (const width of [1440, 900]) {
    win.setContentSize(width, 960);
    await win.loadURL(`${origin}/dev/three-pane/?theme=${theme}&size=${size}`);
    await waitFor(win, `document.documentElement.hasAttribute('data-shell-skin')`);
    await settle(win);
    const bounds = await evaluate(win, `const r = document.querySelector('.app-background').getBoundingClientRect();
      return { width: r.width, height: r.height, viewport: innerWidth, hit: document.elementFromPoint(20, 200)?.className };`);
    assert.equal(bounds.width, bounds.viewport);
    assert.equal(bounds.height, 960);
    assert.notEqual(bounds.hit, "app-background");
    fs.writeFileSync(path.join(output, `${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
    // Removing only the shell artwork must not repaint reading surfaces.
    await win.loadURL(`${origin}/dev/three-pane/?empty&theme=${theme}&size=${size}`);
    await waitFor(win, `document.querySelector('.empty-home') && document.documentElement.hasAttribute('data-shell-skin')`);
    await settle(win);
    const samples = await evaluate(win, `const empty = document.querySelector('.empty-home').getBoundingClientRect();
      const dock = document.querySelector('.dock-composer-wrap').getBoundingClientRect();
      return [{ name: 'empty conversation', x: Math.round(empty.left + 16), y: Math.round(empty.top + 16), width: 8, height: 8 },
        { name: 'dock cover', x: Math.round(dock.left + 12), y: Math.round(dock.bottom - 10), width: 6, height: 6 }];`);
    const visible = await win.webContents.capturePage();
    await evaluate(win, `document.querySelector('.app-background').style.visibility = 'hidden';`);
    await settle(win);
    const hidden = await win.webContents.capturePage();
    for (const sample of samples) {
      const before = average(hidden, sample, width), after = average(visible, sample, width);
      const difference = Math.max(...before.map((value, c) => Math.abs(value - after[c])));
      assert(difference < 1, `The ${sample.name} must retain its content surface (${theme}, ${size}, ${width}); pixel difference: ${difference}`);
    }
    await evaluate(win, `document.querySelector('.app-background').style.visibility = '';`);
    await settle(win);
    fs.writeFileSync(path.join(output, `${theme}-${size}-${width}-empty.png`), (await win.webContents.capturePage()).toPNG());
  }
  // Render the real plugin through both workbench portals and embedded views.
  // Hide only the shell image to verify that nested content stays independent.
  for (const theme of ["light", "dark"]) for (const region of ["primary", "workspace", "settings", "overlay", "auxiliary"]) {
    await win.loadURL(`${origin}/dev/automation/?theme=${theme}&font=20&region=${region}`);
    await waitFor(win, `document.querySelectorAll('.plugin-automation-list .plugin-automation-item').length === 3 && document.documentElement.hasAttribute('data-shell-skin')`);
    await settle(win);
    const sample = await evaluate(win, `const r = document.querySelector('.plugin-automation-main').getBoundingClientRect();
      return { x: Math.round(r.left + 16), y: Math.round(r.bottom - 24), width: 8, height: 8 };`);
    const visible = await win.webContents.capturePage();
    await evaluate(win, `document.querySelector('.app-background').style.visibility = 'hidden';`);
    await settle(win);
    const hidden = await win.webContents.capturePage();
    const before = average(hidden, sample, win.getContentSize()[0]), after = average(visible, sample, win.getContentSize()[0]);
    const difference = Math.max(...before.map((value, c) => Math.abs(value - after[c])));
    assert(difference < 1, `${region} plugin content must retain its own surface (${theme}); pixel difference: ${difference}`);
    await evaluate(win, `document.querySelector('.app-background').style.visibility = '';`);
    await settle(win);
    fs.writeFileSync(path.join(output, `${theme}-plugin-${region}.png`), (await win.webContents.capturePage()).toPNG());
  }
  await evaluate(win, `const { updateBackground } = ${preferences}; await updateBackground(() => null);`);
  await waitFor(second, `!document.documentElement.hasAttribute('data-shell-skin')`);
  await win.loadURL(fixture);
  await waitFor(win, `document.querySelector('input[type=file]') && !document.querySelector('input[type=file]').disabled`);
  assert.equal(await evaluate(win, `const { readBackground } = ${preferences}; return await readBackground();`), null);
  console.log("PASS: bundled worker/CSP/resizing/Gaussian edges/contrast, import, invalid input, write rollback, fixed wallpaper treatment, reload, multi-window sync/removal, independent three-pane/empty-conversation/dock/plugin pixels, opaque content, 26 render captures");
  windows.forEach(window => window.destroy());
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
