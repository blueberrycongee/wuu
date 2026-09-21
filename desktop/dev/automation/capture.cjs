// Run from desktop/: electron dev/automation/capture.cjs
// Uses only synthetic preview data; screenshots are not a visual-acceptance gate.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");

const output = path.resolve(__dirname, "../../../artifacts/automation-review");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5189";
const scenarios = [
  { name: "light", width: 1200, height: 900, query: "shell" },
  { name: "dark-large", width: 1000, height: 900, query: "shell&theme=dark&font=20&rows=30&long" },
  { name: "narrow-large", width: 420, height: 800, query: "shell&font=20&rows=30&long" },
  { name: "empty", width: 720, height: 750, query: "shell&empty" },
];

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 1200, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const js = code => win.webContents.executeJavaScript(code);
  const frame = () => js("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  const ready = () => js(`new Promise((resolve, reject) => {
    const deadline = performance.now() + 10000;
    function check() {
      if (document.querySelector('.plugin-automation-template')) resolve();
      else if (performance.now() > deadline) reject(new Error('Automation preview did not mount'));
      else requestAnimationFrame(check);
    }
    check();
  })`);
  const capture = async name => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  const report = [];
  for (const scenario of scenarios) {
    win.setContentSize(scenario.width, scenario.height);
    await win.loadURL(`${origin}/dev/automation/?${scenario.query}`);
    await ready();
    await frame();
    const geometry = await js(`(() => {
      const rect = selector => { const r = document.querySelector(selector).getBoundingClientRect(); return { x:r.x, y:r.y, width:r.width, height:r.height, right:r.right, bottom:r.bottom }; };
      const results = document.querySelector('.plugin-automation-results');
      const controls = rect('.plugin-automation-controls');
      results.scrollTop = results.scrollHeight;
      const after = rect('.plugin-automation-controls');
      return { controls, after, results:rect('.plugin-automation-results'), search:rect('.plugin-automation-search'),
        overflow:document.documentElement.scrollWidth > innerWidth || results.scrollWidth > results.clientWidth,
        scrolled:results.scrollTop, font:getComputedStyle(document.querySelector('.plugin-automation')).fontSize };
    })()`);
    assert.deepEqual(geometry.controls, geometry.after, `${scenario.name}: scrolling moved the controls`);
    assert(geometry.results.height > 100, `${scenario.name}: list has no usable reading area`);
    assert(geometry.results.y >= geometry.controls.bottom - 1, `${scenario.name}: controls overlap the list`);
    assert(!geometry.overflow, `${scenario.name}: horizontal overflow`);
    if (scenario.query.includes("rows=")) assert(geometry.scrolled > 0, `${scenario.name}: list did not scroll`);
    await js("document.querySelector('.plugin-automation-results').scrollTop = 0");
    await frame();
    await capture(scenario.name);

    await js("document.querySelector('.primary-view-actions button').click()");
    await frame();
    const menu = await js(`(() => { const menu = document.querySelector('[role=menu]'); const r = menu.getBoundingClientRect(); return { visible:r.width > 0 && r.x >= 0 && r.right <= innerWidth && r.bottom <= innerHeight, focused:menu.contains(document.activeElement), active:document.activeElement.tagName, x:r.x, y:r.y, width:r.width, height:r.height }; })()`);
    await capture(`${scenario.name}-menu`);
    assert(menu.visible && menu.focused, `${scenario.name}: menu clipped or unfocused: ${JSON.stringify(menu)}`);
    await js("document.activeElement.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true}))");
    assert(await js("document.activeElement === document.querySelector('.primary-view-actions button')"));

    await js("document.querySelector('.plugin-automation-results').scrollTop = document.querySelector('.plugin-automation-results').scrollHeight; document.querySelector('.plugin-automation-template').click()");
    await frame();
    assert(await js("!!document.querySelector('.plugin-automation-detail textarea')"), `${scenario.name}: template did not open a draft`);
    await capture(`${scenario.name}-editor`);
    report.push({ name: scenario.name, ...geometry, menu });
  }
  assert.deepEqual(errors, [], "Renderer logged errors");
  fs.writeFileSync(path.join(output, "geometry.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
