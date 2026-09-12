// Optional browser check: start npm run lab:mascot, then run
// node_modules/.bin/electron dev/mascot/capture.cjs from desktop.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../../artifacts/mascot-system");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-data"));
app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1280, height: 1000, show: false, webPreferences: { backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  await win.loadURL("http://127.0.0.1:5177");
  const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(a => JSON.stringify(a)).join(",")})`);
  const settle = () => evaluate(() => new Promise(resolve => setTimeout(resolve, 500)));
  await settle();
  assert.ok(await evaluate(() => document.querySelector(".runtime-preview [data-wuu-mascot-activity]")), "Preview did not mount");
  const select = async (index, value) => {
    await evaluate((i, v) => {
      const input = document.querySelectorAll("select")[i];
      input.value = v;
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }, index, value);
    await settle();
  };
  const inspect = () => evaluate(() => {
    const hero = document.querySelector(".runtime-preview [data-wuu-mascot-activity]");
    return { colour: getComputedStyle(hero).getPropertyValue("--mo-head"), accessory: hero.dataset.wuuMascotAccessory, state: hero.dataset.wuuMascotActivity };
  });
  const first = await inspect();
  await select(0, "anthropic");
  const providerChanged = await inspect();
  assert.notEqual(first.colour, providerChanged.colour);
  assert.equal(first.accessory, providerChanged.accessory);
  await select(1, "claude-sonnet-4");
  assert.notEqual((await inspect()).accessory, first.accessory);
  const states = await evaluate(() => [...document.querySelectorAll("select")[2].options].map(o => o.value));
  const faceCoverage = [];
  for (const state of states) {
    await select(2, state);
    assert.equal((await inspect()).state, state);
    assert.equal(await evaluate(() => [...document.querySelectorAll(".mo-eye path")].some(p => /NaN|Infinity/.test(p.getAttribute("d")))), false);
    const covered = await evaluate(() => [...document.querySelectorAll(".samples > svg")].flatMap(svg => {
      const art = [...svg.querySelectorAll(".wuu-mascot-layer-front .wuu-mascot-accessory path, .wuu-mascot-layer-front .wuu-mascot-accessory rect, .wuu-mascot-layer-front .wuu-mascot-accessory circle, .wuu-mascot-layer-front .wuu-mascot-accessory ellipse")].filter(p => getComputedStyle(p).fill !== "none");
      let points = 0, hidden = 0;
      for (const eye of svg.querySelectorAll(".mo-eye path")) {
        const r = eye.getBoundingClientRect();
        for (let x = 1; x < 10; x++) for (let y = 1; y < 10; y++) {
          const point = new DOMPoint(r.left + r.width * x / 10, r.top + r.height * y / 10);
          if (!eye.isPointInFill(point.matrixTransform(eye.getScreenCTM().inverse()))) continue;
          points++;
          if (art.some(p => p.isPointInFill(point.matrixTransform(p.getScreenCTM().inverse())))) hidden++;
        }
      }
      return hidden > points * .08 ? [{ accessory: svg.dataset.wuuMascotAccessory, fraction: hidden / points }] : [];
    }));
    if (covered.length) faceCoverage.push({ state, covered });
  }
  await select(2, "idle");
  const geometry = [];
  for (const width of [1280, 390]) {
    win.setContentSize(width, 1000);
    for (const dark of [false, true]) {
      await evaluate(d => {
        const checkbox = document.querySelectorAll('input[type="checkbox"]')[1];
        if (checkbox.checked !== d) checkbox.click();
      }, dark);
      await settle();
      await evaluate(() => window.scrollTo(0, 0));
      const metrics = await evaluate(() => ({
        viewport: innerWidth,
        content: document.documentElement.scrollWidth,
        samples: [...document.querySelectorAll(".samples")].map(sample => {
          const bounds = sample.getBoundingClientRect();
          const captions = sample.parentElement.querySelector("figcaption").getBoundingClientRect();
          const art = [...sample.querySelectorAll(".wuu-mascot-accessory")].map(a => a.getBoundingClientRect()).filter(r => r.width > 0 && r.height > 0);
          return { accessory: sample.parentElement.textContent, fits: art.every(r => r.left >= bounds.left - 12 && r.right <= bounds.right + 12 && r.bottom <= captions.top) };
        }),
      }));
      assert.ok(metrics.content <= metrics.viewport, `Horizontal overflow at ${width}`);
      assert.ok(metrics.samples.every(s => s.fits), `Accessory overlaps label or neighbor: ${JSON.stringify(metrics.samples.filter(s => !s.fits))}`);
      geometry.push({ width, dark, ...metrics });
      fs.writeFileSync(path.join(output, `${width}-${dark ? "dark" : "light"}.png`), (await win.webContents.capturePage()).toPNG());
      await evaluate(() => document.querySelector('[aria-label="Onboarding equipment"]').scrollIntoView());
      await settle();
      const equipment = await evaluate(() => {
        const stage = document.querySelector(".onboarding-plugin-mascot-stage");
        const svg = stage.querySelector("svg");
        const art = svg.querySelector(".onboarding-mascot-equipment");
        const bounds = art.getBoundingClientRect();
        return {
          followsBody: art.closest(".mo-bob") === svg.querySelector(".mo-eye").closest(".mo-bob"),
          width: bounds.width, height: bounds.height,
          fitsViewport: bounds.left >= 0 && bounds.right <= innerWidth && bounds.top >= 0 && bounds.bottom <= innerHeight,
        };
      });
      assert.ok(equipment.followsBody && equipment.width > 0 && equipment.height > 0 && equipment.fitsViewport, JSON.stringify(equipment));
      fs.writeFileSync(path.join(output, `onboarding-${width}-${dark ? "dark" : "light"}.png`), (await win.webContents.capturePage()).toPNG());
    }
  }
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await select(2, "compact");
  assert.equal(await evaluate(() => [...document.querySelectorAll('[data-wuu-mascot-activity="compact"] .mo-bob > g')].some(el => getComputedStyle(el).animationName !== "none")), false);
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, "checks.json"), JSON.stringify({ states, geometry, faceCoverage, errors, reducedMotion: "passed" }, null, 2));
  assert.deepEqual(faceCoverage, [], "Accessories obscure the eyes");
  console.log(`Mascot renderer checks passed; screenshots: ${output}`);
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
