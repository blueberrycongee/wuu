// Electron-rendered production components, synthetic services; not full-App or
// physical-display acceptance. Run after npm ci in desktop. --build-only checks
// fixture bundling without starting a browser; it is not an E2E pass.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const esbuild = require("esbuild");
const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_ERROR_FEEDBACK_OUTPUT || path.join(desktop, "out/e2e/error-feedback"));
const temp = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-error-feedback-"));
fs.mkdirSync(output, { recursive: true });
esbuild.buildSync({ entryPoints: [path.join(__dirname, "e2e/error-feedback-fixture.tsx")],
  outfile: path.join(temp, "fixture.js"), bundle: true, platform: "browser", format: "esm",
  jsx: "automatic", define: { "process.env.NODE_ENV": '"production"', "import.meta.env": '{"DEV":false,"MODE":"production"}' },
  loader: { ".ttf": "file", ".woff": "file", ".woff2": "file", ".svg": "file", ".png": "file" },
});
fs.writeFileSync(path.join(temp, "index.html"), '<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="fixture.css"><div id="root"></div><script type="module" src="fixture.js"></script>');
if (process.argv.includes("--build-only")) {
  console.log("PASS fixture bundle only; no rendered verification");
  fs.rmSync(temp, { recursive: true, force: true });
  process.exit(0);
}
const { app, BrowserWindow } = require("electron");
app.setPath("userData", path.join(temp, "profile"));
const report = { status: "running", kind: "production-component fixture", platform: process.platform,
  versions: process.versions, checks: [], samples: [], screenshots: [], errors: [], recoveryInteractions: [],
  limitations: ["Synthetic services; no live model, account, Go core or full App composition.", "Electron capture is not physical-display approval."] };
const save = () => fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2));
let win;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`, true);
async function until(check, label) {
  const end = Date.now() + 15000;
  while (Date.now() < end) { const result = await check(); if (result) return result; await delay(25); }
  throw new Error(`Timed out: ${label}`);
}
async function target(selector) {
  return until(() => evaluate(selector => {
    const el = document.querySelector(selector); if (!el || el.disabled) return null;
    // The App uses a settings scrollport, not body scrolling. Bring a control
    // into view as a user would, then still require native hit testing.
    el.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "instant" });
    const r = el.getBoundingClientRect(), x = r.x + r.width / 2, y = r.y + r.height / 2;
    return r.width && r.height && el.contains(document.elementFromPoint(x, y)) ? { x: Math.round(x), y: Math.round(y) } : null;
  }, selector), selector);
}
async function click(selector) {
  const point = await target(selector);
  for (const type of ["mouseMove", "mouseDown", "mouseUp"]) win.webContents.sendInputEvent({ type, ...point, button: "left", clickCount: 1 });
  return point;
}
async function capture(name) {
  await evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
  fs.writeFileSync(path.join(output, name + ".png"), (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(name + ".png");
}
const present = selector => evaluate(selector => Boolean(document.querySelector(selector)), selector);
async function run() {
  await app.whenReady();
  win = new BrowserWindow({ width: 1280, height: 900, show: true, webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true } });
  win.webContents.on("console-message", event => { if (event.level === "error") report.errors.push(event.message); });
  await win.loadFile(path.join(temp, "index.html"));
  win.webContents.setBackgroundThrottling(false);
  await until(() => evaluate(() => Boolean(window.feedback)), "fixture ready");
  for (const [theme, font, width] of [["light", 15, 1280], ["dark", 20, 640]]) {
    win.setContentSize(width, 900);
    await evaluate(({ theme, font }) => { window.feedback.applyAppearance(theme, font); window.feedback.notice(); }, { theme, font });
    await until(() => present('[data-wuu-component="notice"]'), "long notice");
    await evaluate(() => document.fonts.ready);
    const sample = await evaluate(() => {
      const el = document.querySelector('[data-wuu-component="notice"]'), message = el.querySelector('.archive-tip-message');
      return { viewport: innerWidth, viewportHeight: innerHeight, pageWidth: document.documentElement.scrollWidth, notice: el.getBoundingClientRect().toJSON(),
        messageOverflow: message.scrollWidth - message.clientWidth, textLength: message.textContent.length,
        controls: [...el.querySelectorAll('button')].map(button => { const r = button.getBoundingClientRect(); return { bounds: r.toJSON(), hit: button.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)) }; }) };
    });
    assert.ok(sample.pageWidth <= sample.viewport && sample.messageOverflow <= 1, "long reason remains readable without horizontal clipping");
    assert.ok(sample.notice.width >= Math.min(640, sample.viewport - 48) - 1, "long notice uses available width rather than squeezing text into half the viewport");
    assert.ok(sample.controls.every(control => control.hit), "notice controls remain hit-testable");
    report.samples.push({ theme, font, width, ...sample });
    await capture(`notice-${theme}-${font}-${width}`);
  }
  const notice = '[data-wuu-component="notice"]';
  win.webContents.sendInputEvent({ type: "mouseMove", ...await target('.archive-tip-message') });
  await delay(6500); // Deliberately cross the product's six-second expiry.
  assert.ok(await present(notice), "hover preserves a notice past expiry");
  await evaluate(() => document.querySelector('.archive-tip-action').focus());
  win.webContents.sendInputEvent({ type: "mouseMove", x: 5, y: 850 });
  await delay(6500);
  assert.ok(await present(notice), "keyboard focus preserves a notice past expiry");
  await click('.archive-tip-dismiss');
  await until(async () => !await present(notice), "dismissal");
  for (let attempt = 0; attempt < 2; attempt++) {
    await evaluate(() => window.feedback.actionFailure());
    await until(() => present(notice), "explicit action feedback");
    await click('.archive-tip-dismiss');
    await until(async () => !await present(notice), "action dismissal");
  }
  await evaluate(() => window.feedback.shortNotice());
  await until(() => present(notice), "short notice");
  const shortWidth = await evaluate(() => document.querySelector('[data-wuu-component="notice"]').getBoundingClientRect().width);
  assert.ok(shortWidth < report.samples.at(-1).notice.width / 2, "short notice remains content-sized, not a full-width banner");
  await capture("notice-short");
  await click('.archive-tip-dismiss');
  await until(async () => !await present(notice), "short notice dismissal");
  report.checks.push("long notices use available width while short notices remain compact", "long notices and controls at light 15px/1280 and dark 20px/640", "hover and keyboard focus survive real six-second expiry", "dismissed identical explicit failures remain visible on retry");
  await evaluate(() => window.feedback.render('settings'));
  await until(() => present('[data-testid="settings-error-retry"]'), "initial settings failure");
  for (let attempt = 1; attempt <= 2; attempt++) {
    await click('[data-testid="settings-error-retry"]');
    await until(() => evaluate(expected => window.feedback.reads() === expected, attempt + 1), "settings attempt");
    if (attempt === 1) {
      await until(() => present('[data-testid="settings-error-retry"]'), "second failed read remains retryable");
      await click('.settings-mcp-details-toggle');
      await until(() => present('.settings-mcp-diagnostic-text'), "diagnostic disclosure");
      await capture('settings-repeated-failure');
    }
  }
  await until(() => evaluate(() => !document.querySelector('[data-testid="settings-provider-connect"]').disabled), "settings recovered without saving");
  assert.equal(await present('.settings-failure'), false);
  await capture('settings-recovered');
  report.checks.push("actual connection dialog failure → retry → failure → retry → success, diagnostic disclosure and no submission");
  for (const [theme, font, width] of [["light", 15, 1280], ["dark", 20, 640]]) {
    win.setContentSize(width, 900);
    await evaluate(({theme, font}) => { window.feedback.render("notice"); window.feedback.applyAppearance(theme, font); window.feedback.remoteFailure(true); }, {theme, font});
    await until(async () => !await present('[data-testid="settings-remote-page"]'), "remote unmount before new sample");
    await evaluate(() => window.feedback.render("remote"));
    await until(() => present('[data-testid="settings-error-retry"]'), "remote initial failure");
    assert.equal(await present('[role="switch"]'), false, "unknown state never claims access is disabled");
    assert.equal(await present('[data-testid="settings-remote-page"]'), false, "unknown state never claims an empty devices list");
    await target('[data-testid="settings-error-retry"]');
    await capture(`remote-initial-failure-${theme}-${font}-${width}`);
    await evaluate(() => window.feedback.remoteFailure(false));
    await click('[data-testid="settings-error-retry"]');
    await until(() => present('[role="switch"][aria-checked="true"]'), "remote recovered enabled state");
    assert.ok(await evaluate(() => document.querySelector('[data-testid="settings-remote-page"]').textContent.includes("Retained phone")));
    await evaluate(() => window.feedback.remoteFailure(true, true));
    await until(() => present('[data-testid="settings-error-retry"]'), "cached refresh failure");
    assert.ok(await present('[role="switch"][aria-checked="true"]'), "refresh failure retains access state");
    assert.ok(await evaluate(() => document.querySelector('[data-testid="settings-remote-page"]').textContent.includes("Retained phone")), "refresh failure retains devices");
    await target('[data-testid="settings-error-retry"]');
    await capture(`remote-cached-failure-${theme}-${font}-${width}`);
    await evaluate(() => window.feedback.remoteFailure(false));
    await click('[data-testid="settings-error-retry"]');
    await until(async () => !await present('[data-testid="settings-error-retry"]'), "remote refresh recovery");
    await capture(`remote-recovered-${theme}-${font}-${width}`);
  }
  report.checks.push("remote first-read failure shows Retry rather than disabled access or empty devices; cached refresh failure preserves access and devices; retry recovers at light 15px/1280 and dark 20px/640");
  for (const [theme, font, width] of [["light", 15, 1280], ["dark", 20, 640]]) {
    win.setContentSize(width, 900);
    await evaluate(({theme, font}) => { window.feedback.applyAppearance(theme, font); window.feedback.render("notice"); }, {theme, font});
    await until(async () => !await present('[data-wuu-component="draft-recovery"]'), "recovery fixture unmounted");
    await evaluate(() => window.feedback.render("owned-notices"));
    await until(() => present('[data-wuu-component="draft-recovery"]'), "durable recovery in composer");
    const before = await evaluate(() => document.querySelector('[data-testid="retained-draft"]').value);
    assert.equal(before, "My newer draft remains editable");
    assert.ok(await target('[data-testid="retained-draft"]'), "recovery does not cover the editor");
    for (const expected of ["Global feedback first", "Composer feedback second", "Settings feedback third"]) {
      await until(() => evaluate(expected => document.querySelector('[data-wuu-component="notice"]')?.textContent.includes(expected), expected), expected);
      assert.equal(await evaluate(() => document.querySelectorAll('[data-wuu-component="notice"]').length), 1, "all transient surfaces share one notification position");
      await capture(`owned-${expected.split(" ")[0].toLowerCase()}-${theme}-${font}-${width}`);
      await click('.archive-tip-dismiss');
    }
    await until(async () => !await present(notice), "all transient notices dismissed");
    assert.ok(await present('[data-wuu-component="draft-recovery"]'), "dismissal preserves durable recovery");
    assert.equal(await evaluate(() => document.querySelector('[data-testid="retained-draft"]').value), before);
    const previousOpens = await evaluate(() => window.feedback.recoveryOpened());
    const interaction = { theme, font, width, previousOpens, point: await click('[data-wuu-component="draft-recovery"] button') };
    report.recoveryInteractions.push(interaction);
    // sendInputEvent queues native input; executeJavaScript is not an input ACK.
    // Require a new activation in each theme instead of borrowing the first click.
    try {
      await until(() => evaluate(expected => window.feedback.recoveryOpened() === expected, previousOpens + 1), "recovery remains actionable after native click");
    } finally {
      interaction.observed = await evaluate(() => ({ opened: window.feedback.recoveryOpened(), events: window.feedback.recoveryInputEvents() }));
    }
    await capture(`draft-recovery-${theme}-${font}-${width}`);
  }
  report.checks.push("global and two surface notices render sequentially at one anchor; dismissals preserve editable draft and persistent recovery; recovery remains actionable at light 15px and dark 20px (surrounding editor is a fixture; public tab.select and no tab.close covered by integration tests)");
  await evaluate(() => window.feedback.render('rich'));
  await until(() => present('#fixture-prose a'), "rich link");
  const prose = await evaluate(() => document.querySelector('#fixture-prose').textContent);
  await click('#fixture-prose a');
  await until(() => present(notice), "link operation failure");
  assert.equal(await evaluate(() => document.querySelector('#fixture-prose').textContent), prose, "operation failure does not insert text into the answer");
  assert.equal(await present('#fixture-prose [role="alert"]'), false);
  await capture('rich-link-failure');
  report.checks.push("actual RichContent failed link reports centrally without changing answer prose");
  assert.deepEqual(report.errors, []);
  report.status = "passed";
}
const watchdog = setTimeout(() => { report.status = "failed"; report.error = "watchdog expired"; save(); app.exit(1); }, 120000);
run().then(() => { clearTimeout(watchdog); save(); console.log(`PASS: ${output}`); app.exit(0); }, async error => {
  clearTimeout(watchdog); report.status = "failed"; report.error = String(error.stack || error);
  if (win && !win.isDestroyed()) await capture("failure").catch(() => {});
  save(); console.error(error); app.exit(1);
});
