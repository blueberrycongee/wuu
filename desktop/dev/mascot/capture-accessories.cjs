// Optional rendered check, with npm run lab:mascot running.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../../artifacts/mascot-accessories");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-data"));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1100, height: 900, webPreferences: { backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") { errors.push(event.message); console.error(event.message); } });
  await win.loadURL(`${process.env.MASCOT_PREVIEW_URL || "http://127.0.0.1:5177"}/?study=accessories`);
  const evaluate = fn => win.webContents.executeJavaScript(`(${fn})()`);
  const settle = () => evaluate(() => new Promise(resolve => setTimeout(resolve, 500)));
  await settle();
  const reports = [];
  const choices = await evaluate(() => [...document.querySelectorAll('.accessory-collection button')].map(button => button.getAttribute('aria-label')).slice(1));
  const selectAccessory = async label => {
    await win.webContents.executeJavaScript(`[...document.querySelectorAll('.accessory-collection button')].find(button => button.getAttribute('aria-label') === ${JSON.stringify(label)}).click()`);
    await settle();
  };
  const inspect = () => evaluate(() => {
    const avatars = [...document.querySelectorAll('svg[data-wuu-mascot-accessory]:not([data-wuu-mascot-accessory="none"])')];
    const collisions = [];
    const covered = avatars.filter(svg => {
      const art = [...svg.querySelectorAll('.wuu-mascot-layer-front .wuu-mascot-accessory path, .wuu-mascot-layer-front .wuu-mascot-accessory rect')];
      return [...svg.querySelectorAll('.mo-eye path')].some(eye => {
        const r = eye.getBoundingClientRect();
        for (let x = 1; x < 10; x++) for (let y = 1; y < 10; y++) {
          const p = new DOMPoint(r.left + r.width * x / 10, r.top + r.height * y / 10);
          if (!eye.isPointInFill(p.matrixTransform(eye.getScreenCTM().inverse()))) continue;
          if (art.some(a => {
            const local = p.matrixTransform(a.getScreenCTM().inverse());
            const style = getComputedStyle(a);
            const hit = (style.fill !== 'none' && a.isPointInFill(local)) || (style.stroke !== 'none' && a.isPointInStroke(local));
            if (hit) collisions.push(a.outerHTML);
            return hit;
          })) return true;
        }
        return false;
      });
    });
    const colors = [...document.querySelectorAll('.beanie-colors .wuu-mascot-layer-front .wuu-mascot-accessory')].map(art => getComputedStyle(art).getPropertyValue('--wuu-accessory-color'));
    const detached = avatars.filter(svg => {
      const body = [...svg.querySelectorAll('.mo-bob > g:not(.mo-eyes) > path, .mo-bob > g:not(.mo-eyes) > circle')];
      const art = [...svg.querySelectorAll('.wuu-mascot-layer-front .wuu-mascot-accessory path, .wuu-mascot-layer-front .wuu-mascot-accessory rect')];
      return !art.some(a => {
        const box = a.getBBox();
        const style = getComputedStyle(a);
        for (let x = 0; x <= 24; x++) for (let y = 0; y <= 24; y++) {
          const p = new DOMPoint(box.x + box.width * x / 24, box.y + box.height * y / 24);
          if (!(style.fill !== 'none' && a.isPointInFill(p)) && !(style.stroke !== 'none' && a.isPointInStroke(p))) continue;
          const screen = p.matrixTransform(a.getScreenCTM());
          if (body.some(b => b.isPointInFill(screen.matrixTransform(b.getScreenCTM().inverse())))) return true;
        }
        return false;
      });
    }).map(svg => ({ accessory: svg.dataset.wuuMascotAccessory, shape: svg.closest('figure')?.querySelector('figcaption')?.textContent }));
    return { width: innerWidth, scrollWidth: document.documentElement.scrollWidth, count: avatars.length,
      detached,
      covered: covered.map(svg => ({ accessory: svg.dataset.wuuMascotAccessory, section: svg.closest('section')?.getAttribute('aria-label'), shape: svg.closest('figure')?.querySelector('figcaption')?.textContent, index: [...(svg.closest('figure')?.querySelectorAll('svg[data-wuu-mascot-accessory]') || [])].indexOf(svg), collisions })), colors: [...new Set(colors)] };
  });
  for (const width of [1100, 390]) {
    win.setContentSize(width, 900);
    for (const dark of [false, true]) {
      await win.webContents.executeJavaScript(`(() => {
        const checkbox = document.querySelector('header input');
        if (checkbox.checked !== ${dark}) checkbox.click();
      })()`);
      await settle();
      for (const label of choices) {
      await selectAccessory(label);
      const metrics = await inspect();
      assert.ok(metrics.count > 0);
      assert.ok(metrics.scrollWidth <= metrics.width, JSON.stringify(metrics));
      assert.deepEqual(metrics.covered, [], `Accessory covers eyes: ${label}`);
      assert.deepEqual(metrics.detached, [], `Accessory floats off body: ${label}`);
      assert.equal(metrics.colors.length, 2, `Expected both curated palettes: ${label}`);
      reports.push({ label, dark, ...metrics });
      await evaluate(() => window.scrollTo(0, 0));
      fs.writeFileSync(path.join(output, `${width}-${dark ? "dark" : "light"}-${label}.png`), (await win.webContents.capturePage()).toPNG());
      await evaluate(() => document.querySelector('.beanie-sizes').scrollIntoView());
      fs.writeFileSync(path.join(output, `${width}-${dark ? "dark" : "light"}-${label}-sizes.png`), (await win.webContents.capturePage()).toPNG());
      }
    }
  }
  win.setContentSize(1100, 900);
  const states = await evaluate(() => [...document.querySelector('.beanie-controls select').options].map(o => o.value));
  for (const state of states) {
    await win.webContents.executeJavaScript(`(() => { const input = document.querySelector('.beanie-controls select'); input.value = ${JSON.stringify(state)}; input.dispatchEvent(new Event('change', { bubbles: true })); })()`);
    for (const label of choices) {
      await selectAccessory(label);
      const metrics = await inspect();
      assert.deepEqual(metrics.covered, [], `Eye coverage: ${state}/${label}`);
      assert.deepEqual(metrics.detached, [], `Accessory floats off body: ${state}/${label}`);
    }
  }
  await evaluate(() => { const input = document.querySelector('.beanie-controls select'); input.value = 'idle'; input.dispatchEvent(new Event('change', { bubbles: true })); window.scrollTo(0, 0); });
  await selectAccessory(choices[0]);
  const hatTransform = () => evaluate(() => getComputedStyle(document.querySelector('.beanie-hero .wuu-accessory-motion')).transform);
  const before = await hatTransform();
  win.webContents.sendInputEvent({ type: "mouseMove", x: 370, y: 100 });
  await settle();
  assert.notEqual(await hatTransform(), before, "Hat should respond to the shared pointer camera");
  await evaluate(() => document.querySelector('.beanie-controls input[type="checkbox"]').click());
  await settle();
  assert.equal(await hatTransform(), before, "Disabling pointer following should reset the hat");
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await settle();
  assert.equal(await hatTransform(), "none");
  win.webContents.debugger.detach();
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(reports, null, 2));
  console.log(`Accessory rendered checks passed: ${output}`);
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
