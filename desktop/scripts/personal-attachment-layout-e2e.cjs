// Real rendered synthetic fixture: escaped identity overlap and hidden-chat portal.
// Build with build-personal-fixture.cjs, then run with Electron.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const output = path.resolve(__dirname, '../out/personal-attachment-layout-e2e');
async function run({ evaluate, resize, navigate, capture }) {
  fs.mkdirSync(output, { recursive: true });
  const cases = [], failures = [];
  async function until(fn) {
    const end = Date.now() + 10000;
    while (Date.now() < end) {
      if (await evaluate(fn)) return;
      await new Promise(resolve => setTimeout(resolve, 30));
    }
    throw new Error(`Timed out: ${fn}`);
  }
  async function check(name, fn) {
    try { await fn(); cases.push(name); }
    catch (error) { failures.push({ name, error: String(error) }); }
  }
  async function clearance(name) {
    await check(name, async () => {
      const geometry = await evaluate(() => ({
        identityBottom: document.querySelector('.mosaic-identity-label').getBoundingClientRect().bottom,
        scrollTop: document.querySelector('.mosaic-main .plugin-conversation-scroll').getBoundingClientRect().top,
      }));
      assert.ok(geometry.scrollTop >= geometry.identityBottom, JSON.stringify(geometry));
    });
  }
  for (const [name, width, height] of [['desktop', 1181, 757], ['zoom-equivalent', 590, 378]]) {
    await resize(width, height);
    await navigate(path.resolve(__dirname, '../out/personal-fixture/index.html'));
    await until(() => document.querySelector('.mosaic-main [data-action="open-attachment"]'));
    await clearance(`${name}: identity clears scrolling content`);
    await evaluate(() => [...document.querySelectorAll('.mosaic-main [data-action="open-attachment"]')].find(node => node.textContent.includes('合成说明.md')).click());
    await until(() => document.querySelector('.workspace-right-panel .workspace-file-preview'));
    await capture(path.join(output, `${name}-attachment.png`));
    if (width > 720) {
      await clearance('split: identity clears scrolling content');
      await evaluate(() => document.querySelector('.workspace-panel-globalize').click());
    }
    await until(() => document.querySelector('.conversation-pane').hasAttribute('inert'));
    await check(`${name}: hidden chat has no jump control`, async () => {
      await evaluate(() => {
        const scroll = document.querySelector('.mosaic-main .plugin-conversation-scroll');
        scroll.scrollTop = 0;
        scroll.dispatchEvent(new Event('scroll'));
        return new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      });
      assert.equal(await evaluate(() => !!document.querySelector('[data-wuu-component="jump-to-latest"]')), false);
    });
    await capture(path.join(output, `${name}-fullpanel.png`));
    await evaluate(() => document.querySelector('.workspace-conversation-tab button').click());
    await until(() => !document.querySelector('.conversation-pane').hasAttribute('inert'));
    await check(`${name}: visible scrolled chat regains jump`, async () => {
      await evaluate(() => {
        const scroll = document.querySelector('.mosaic-main .plugin-conversation-scroll');
        scroll.scrollTop = 0;
        scroll.dispatchEvent(new Event('scroll'));
      });
      const overflowing = await evaluate(() => {
        const scroll = document.querySelector('.mosaic-main .plugin-conversation-scroll');
        return scroll.scrollHeight - scroll.clientHeight > 80;
      });
      if (overflowing) await until(() => document.querySelector('[data-wuu-component="jump-to-latest"]'));
    });
  }
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ cases, failures }, null, 2));
  assert.equal(failures.length, 0, JSON.stringify(failures, null, 2));
  return cases;
}
module.exports = { run };
if (require.main === module) {
  const { app, BrowserWindow } = require('electron');
  fs.mkdirSync(output, { recursive: true });
  app.setPath('userData', fs.mkdtempSync(path.join(output, 'profile-')));
  app.whenReady().then(async () => {
    const win = new BrowserWindow({ width: 1181, height: 757, webPreferences: { contextIsolation: true } });
    await run({
      evaluate: fn => win.webContents.executeJavaScript(`(${fn})()`, true),
      resize: (width, height) => win.setContentSize(width, height),
      navigate: file => win.loadFile(file),
      capture: async file => fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG()),
    });
    app.exit(0);
  }).catch(error => { console.error(error); app.exit(1); });
}
