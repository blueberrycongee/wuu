// Production renderer, DesktopPluginRuntime and external plugin bytes; synthetic
// backend only. Run after electron-vite build. No Go process or inference.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const crypto = require("node:crypto");
const { app, BrowserWindow, protocol } = require("electron");
const desktop = path.resolve(__dirname, "..");
const renderer = process.env.WUU_E2E_RENDERER || path.join(desktop, "out/renderer/index.html");
const output = process.env.WUU_CONVERSATION_ADDONS_OUTPUT || path.join(desktop, "out/e2e/conversation-addons");
const example = path.join(desktop, "../examples/plugins/conversation-metrics/desktop.js");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(os.tmpdir(), "wuu-conversation-addons-")));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(desktop);
process.env.WUU_RESIZE_E2E_LOCALE = "en";
process.env.WUU_RENDERER_E2E_EXTENSION = path.join(__dirname, "conversation-addons-e2e-preload.cjs");
protocol.registerSchemesAsPrivileged([{ scheme: "wuu-plugin", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
const peer = `export function activatePresentation(api) {
  for (const owner of ['wuu.default-conversation', 'wuu.default-preview']) {
    for (const target of ['turn', 'tool-activity']) api.registerUIContribution({ id: owner + target,
      target: owner + '/conversation.' + target + '.after', order: 10, render({ context }) {
        if (!Object.isFrozen(context) || !Object.isFrozen(context.snapshot)) throw new Error('Mutable public slot context');
        return api.react.createElement('span', { 'data-peer-addon': target, 'data-owner': owner,
          'data-address': JSON.stringify([context.threadId, context.turnId, context.itemId]) }, 'Independent annotation');
      } });
  }
}`;
const broken = `export function activatePresentation(api) {
  api.registerUIContribution({ id: 'broken', target: 'wuu.default-conversation/conversation.turn.after',
    render() { throw new Error('Expected conversation addon failure'); } });
}`;
const report = { fixture: "production renderer with synthetic backend", checks: [], captures: [], errors: [],
  exampleSha256: crypto.createHash("sha256").update(fs.readFileSync(example)).digest("hex") };
let win, watchdog;
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
const frames = () => evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function until(fn, value) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, value)) return;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`UI condition timed out: ${fn}`);
}
async function inventory(names) { win.webContents.send("test:conversation-addons", names); await frames(); }
async function check(name, body) { report.phase = name; save(); await body(); report.checks.push(name); save(); console.log(`PASS ${name}`); }
async function preserved() {
  assert.deepEqual(await evaluate(() => {
    const before = window.__addonNativeNodes;
    return { turns: before.turns.every(node => node.isConnected),
      details: before.details.every(node => node.isConnected),
      folds: before.folds.every(node => node.isConnected && node.open),
      focus: document.activeElement === before.focus,
      composer: before.composer === document.querySelector('[data-main-conversation-composer] textarea') };
  }), { turns: true, details: true, folds: true, focus: true, composer: true }, "native DOM, disclosures and focus survive inventory changes");
}
async function fail(error) {
  clearTimeout(watchdog); report.passed = false; report.failure = String(error.stack || error); save(); console.error(error);
  if (win && !win.isDestroyed()) {
    try { fs.writeFileSync(path.join(output, "failure.png"), (await win.webContents.capturePage()).toPNG()); } catch {}
  }
  app.exit(1);
}
app.whenReady().then(async () => {
  watchdog = setTimeout(() => fail(new Error(`Timed out during ${report.phase}`)), 120000);
  protocol.handle("wuu-plugin", request => {
    const name = new URL(request.url).hostname;
    const source = name === "metrics" ? fs.readFileSync(example) : name === "peer" ? peer : name === "broken" ? broken : undefined;
    return source === undefined ? new Response("Unknown fixture", { status: 404 })
      : new Response(source, { headers: { "Content-Type": "text/javascript" } });
  });
  win = new BrowserWindow({ width: 1280, height: 900, show: false, webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error" && !message.includes("Expected conversation addon failure")) report.errors.push(message);
  });
  await win.loadFile(renderer); win.showInactive();
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await until(() => [...document.querySelectorAll('.thread-row')].some(row => row.textContent.includes('Conversation addon acceptance')));
  await evaluate(() => [...document.querySelectorAll('.thread-row')].find(row => row.textContent.includes('Conversation addon acceptance')).click());
  await until(() => document.querySelectorAll('.turn').length === 2);
  await check("single and grouped native tool details before addons", async () => {
    await evaluate(() => document.querySelectorAll('.turn-process-toggle').forEach(button => { if (button.getAttribute('aria-expanded') === 'false') button.click(); }));
    await until(() => document.querySelectorAll('.process-surface-fold').length === 2);
    await evaluate(() => document.querySelectorAll('.process-surface-fold').forEach(node => { node.open = true; node.dispatchEvent(new Event('toggle')); }));
    await until(() => document.querySelectorAll('.program-tool-record').length === 2);
    await evaluate(() => document.querySelectorAll('.program-tool-record').forEach(node => { node.open = true; node.dispatchEvent(new Event('toggle')); }));
    await until(() => document.querySelectorAll('.program-tool-details').length === 3);
    await evaluate(() => {
      const focus = document.querySelector('.process-surface-fold > summary'); focus.focus();
      window.__addonNativeNodes = { turns: [...document.querySelectorAll('.turn')], details: [...document.querySelectorAll('.program-tool-details')],
        folds: [...document.querySelectorAll('.process-surface-fold, .program-tool-record')], focus,
        composer: document.querySelector('[data-main-conversation-composer] textarea') };
    });
  });
  await check("independent addons render in order with frozen addressed payloads", async () => {
    await inventory(["metrics", "peer"]);
    await until(() => document.querySelectorAll('[data-conversation-metrics-turn]').length === 2 && document.querySelectorAll('[data-conversation-metrics-tool]').length === 3);
    assert.equal(await evaluate(() => [...document.querySelectorAll('[data-conversation-metrics-turn]')].every(node => node.textContent === '1200 ms · 8 input tokens · 0 output tokens')), true);
    assert.equal(await evaluate(() => [...document.querySelectorAll('[data-peer-addon]')].every(node => {
      const following = node.nextElementSibling;
      return following?.hasAttribute(node.dataset.peerAddon === 'turn' ? 'data-conversation-metrics-turn' : 'data-conversation-metrics-tool')
        && JSON.parse(node.dataset.address)[0] === 'resize-thread';
    })), true);
    await preserved();
  });
  await check("tool action is local and a failed peer cannot remove working content", async () => {
    await evaluate(() => document.querySelector('[data-conversation-metrics-tool] button').click());
    await until(() => document.querySelector('[data-conversation-metrics-tool] pre')?.textContent === 'Reported result 0');
    await evaluate(() => window.__addonNativeNodes.focus.focus());
    await inventory(["metrics", "peer", "broken"]);
    await until(async () => (await window.wuu.testDesktopActivations()).some(report => report.id === 'user:conversation-addon-broken' && report.status === 'failed'));
    await preserved();
    assert.equal(await evaluate(() => document.querySelector('[data-conversation-metrics-tool] button').getAttribute('aria-expanded')), 'true');
  });
  await check("disable removes only metrics and re-enable starts only its local state", async () => {
    await evaluate(() => { window.__addonPeerNodes = [...document.querySelectorAll('[data-peer-addon]')]; });
    await inventory(["peer"]);
    await until(() => !document.querySelector('[data-conversation-metrics-turn], [data-conversation-metrics-tool]'));
    await preserved();
    assert.equal(await evaluate(() => window.__addonPeerNodes.length === 5 && window.__addonPeerNodes.every(node => node.isConnected)), true);
    await inventory(["metrics", "peer"]);
    await until(() => document.querySelectorAll('[data-conversation-metrics-tool]').length === 3);
    await preserved();
    assert.equal(await evaluate(() => [...document.querySelectorAll('[data-conversation-metrics-tool] button')].every(button => button.getAttribute('aria-expanded') === 'false')), true);
  });
  await check("bubble and IM layouts retain usable addons in light/dark wide/narrow views", async () => {
    for (const [style, theme, width, size] of [["bubble", "light", 1280, 15], ["im", "light", 1280, 15], ["bubble", "dark", 760, 20], ["im", "dark", 760, 20]]) {
      win.setContentSize(width, 900);
      await evaluate(({ style, theme, size }) => {
        document.documentElement.dataset.messageStyle = style; document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty('--wuu-font-size-ui', `${size}px`);
        document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
        document.querySelector('[data-conversation-metrics-turn]').scrollIntoView({ block: 'center' });
      }, { style, theme, size });
      await frames();
      assert.equal(await evaluate(() => [...document.querySelectorAll('[data-conversation-metrics-turn], [data-conversation-metrics-tool]')].every(node => node.scrollWidth <= node.clientWidth + 1)), true, 'addon contents do not clip horizontally');
      const name = `${style}-${theme}-${width}-${size}.png`;
      fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG()); report.captures.push(name);
    }
  });
  await check("disabling all addons leaves no hidden UI", async () => {
    await inventory([]);
    await until(() => !document.querySelector('[data-peer-addon], [data-conversation-metrics-turn], [data-conversation-metrics-tool]'));
    await preserved();
  });
  assert.deepEqual(report.errors, []); clearTimeout(watchdog); report.passed = true; save(); app.exit(0);
}).catch(fail);
