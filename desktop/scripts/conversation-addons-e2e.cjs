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
const messageAddons = Object.fromEntries(["slot", "command"].map(kind => [kind,
  fs.readFileSync(path.join(desktop, `../packages/plugin-sdk/test/fixtures/message-${kind}-addon.js`), "utf8")]));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(os.tmpdir(), "wuu-conversation-addons-")));
process.env.WUU_RESIZE_E2E_CWD = path.dirname(desktop);
process.env.WUU_RESIZE_E2E_LOCALE = "en-US";
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
  for (const target of ['turn.after', 'message.actions']) api.registerUIContribution({ id: target,
    target: 'wuu.default-conversation/conversation.' + target,
    render() { throw new Error('Expected conversation addon failure'); } });
}`;
const report = { fixture: "production renderer with synthetic backend", phase: "startup", checks: [], geometry: [], captures: [], errors: [], console: [],
  exampleSha256: crypto.createHash("sha256").update(fs.readFileSync(example)).digest("hex"),
  messageAddonSha256: Object.fromEntries(Object.entries(messageAddons).map(([name, source]) => [name,
    crypto.createHash("sha256").update(source).digest("hex")])) };
let win, watchdog, failing = false;
const save = () => fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(report, null, 2));
const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
const frames = (count = 2) => evaluate(count => new Promise(resolve => {
  const tick = () => --count ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}), count);
async function until(fn, value) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (report.errors.length) throw new Error(`Renderer failed during ${report.phase}: ${report.errors.join("\n")}`);
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
  if (failing) return;
  failing = true;
  clearTimeout(watchdog); report.passed = false; report.failure = String(error.stack || error); save(); console.error(error);
  if (win && !win.isDestroyed()) {
    // A crashed preload or renderer must leave actionable evidence, not just a
    // later missing-selector timeout. Bound diagnostics when the process died.
    const bounded = async promise => {
      let timeout;
      try { return await Promise.race([promise, new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error('Failure diagnostics timed out')), 2000);
      })]); } finally { clearTimeout(timeout); }
    };
    try {
      report.dom = await bounded(evaluate(async () => ({ readyState: document.readyState,
        bridgeAvailable: Boolean(window.wuu), language: window.wuu?.initialLanguagePreference,
        bodyText: document.body?.innerText.slice(0, 8000), html: document.body?.innerHTML.slice(0, 16000),
        threads: [...document.querySelectorAll('.thread-row-main')].map(node => ({ label: node.getAttribute('aria-label'), text: node.textContent })),
        activations: await window.wuu?.testDesktopActivations?.(),
      })));
    } catch (diagnosticError) { report.diagnosticError = String(diagnosticError); }
    try { fs.writeFileSync(path.join(output, "failure.png"), (await bounded(win.webContents.capturePage())).toPNG()); }
    catch (captureError) { report.captureError = String(captureError); }
  }
  save();
  app.exit(1);
}
app.whenReady().then(async () => {
  watchdog = setTimeout(() => fail(new Error(`Timed out during ${report.phase}`)), 120000);
  protocol.handle("wuu-plugin", request => {
    const name = new URL(request.url).hostname;
    const source = name === "metrics" ? fs.readFileSync(example) : name === "peer" ? peer : name === "broken" ? broken : messageAddons[name];
    return source === undefined ? new Response("Unknown fixture", { status: 404 })
      : new Response(source, { headers: { "Content-Type": "text/javascript" } });
  });
  win = new BrowserWindow({ width: 1280, height: 900, show: false, webPreferences: {
    preload: path.join(__dirname, "resize-e2e-preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
  } });
  win.webContents.on("console-message", ({ level, message, lineNumber, sourceId }) => {
    if (report.console.length < 50) report.console.push({ level, message, lineNumber, sourceId });
    if (level === "error" && !message.includes("Expected conversation addon failure")) report.errors.push(message);
  });
  win.webContents.on("preload-error", (_event, preload, error) => report.errors.push(`Preload ${preload}: ${error.stack || error}`));
  win.webContents.on("render-process-gone", (_event, details) => report.errors.push(`Renderer exited: ${JSON.stringify(details)}`));
  win.webContents.on("did-fail-load", (_event, code, description, url) => report.errors.push(`Load ${url}: ${code} ${description}`));
  await win.loadFile(renderer); win.showInactive();
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await until(() => [...document.querySelectorAll('.thread-row-main')].some(button => button.getAttribute('aria-label')?.includes('Conversation addon acceptance')));
  await evaluate(() => [...document.querySelectorAll('.thread-row-main')].find(button => button.getAttribute('aria-label')?.includes('Conversation addon acceptance')).click());
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
  await check("independent message commands and raw actions preserve peers, native controls and focus in bubble and IM", async () => {
    for (const style of ["bubble", "im"]) {
      await evaluate(style => {
        document.documentElement.dataset.messageStyle = style;
        window.__messageNativeNodes = { messages: [...document.querySelectorAll('[data-wuu-component="message"]')],
          rows: [...document.querySelectorAll('.message-actions')], buttons: [...document.querySelectorAll('.message-actions button')] };
      }, style);
      const baseline = await evaluate(async () => {
        const requests = await window.wuu.testMessageActionRequests();
        return { mounted: requests.filter(row => row.method === 'mounted').length,
          inspected: requests.filter(row => row.id === 'user:conversation-addon-slot' && row.method === 'inspect').length };
      });
      await inventory(["slot"]);
      await until(() => document.querySelectorAll('button[aria-label="Inspect raw message"]').length === 4);
      await until(async before => (await window.wuu.testMessageActionRequests()).filter(row => row.method === 'mounted').length === before + 4, baseline.mounted);
      await evaluate(() => {
        const raw = [...document.querySelectorAll('button[aria-label="Inspect raw message"]')];
        raw.forEach(button => button.click()); raw[0].focus();
        window.__messageAddonNodes = { raw, rows: raw.map(node => node.closest('.message-actions')),
          native: [...document.querySelectorAll('.message-actions button')].filter(node => !raw.includes(node)) };
      });
      assert.equal(await evaluate(() => {
        const before = window.__messageNativeNodes;
        return before.messages.length === 4 && before.buttons.length >= 4
          && [...before.messages, ...before.rows, ...before.buttons].every(node => node.isConnected);
      }), true, 'adding raw actions preserves the native messages, rows, copy and edit/fork controls');
      await until(async before => (await window.wuu.testMessageActionRequests()).filter(row => row.id === 'user:conversation-addon-slot' && row.method === 'inspect').length === before + 4, baseline.inspected);
      const mounted = baseline.mounted + 4;
      assert.equal(await evaluate(() => window.__messageAddonNodes.raw[0].textContent), "Raw 1");
      await inventory(["slot", "command"]);
      await until(() => document.querySelectorAll('button[aria-label="Inspect command message"]').length === 4);
      assert.equal(await evaluate(() => {
        const before = window.__messageAddonNodes;
        before.commands = [...document.querySelectorAll('button[aria-label="Inspect command message"]')];
        return before.raw.every((node, index) => node.isConnected && node.closest('.message-actions') === before.rows[index])
          && before.raw[0].textContent === 'Raw 1' && document.activeElement === before.raw[0]
          && before.native.every(node => node.isConnected)
          && before.rows.every(row => row.querySelectorAll('button').length === before.native.filter(node => row.contains(node)).length + 2);
      }), true, `${style}: adding commands must retain the raw nodes, state, focus and native controls`);
      const commandCalls = await evaluate(async () => (await window.wuu.testMessageActionRequests()).filter(row => row.id === 'user:conversation-addon-command' && row.method === 'inspect').length);
      await evaluate(() => window.__messageAddonNodes.commands.forEach(button => button.click()));
      await until(async before => (await window.wuu.testMessageActionRequests()).filter(row => row.id === 'user:conversation-addon-command' && row.method === 'inspect').length === before + 4, commandCalls);
      const requests = await evaluate(() => window.wuu.testMessageActionRequests());
      assert.equal(requests.filter(row => row.method === 'mounted').length, mounted, 'adding a command cannot remount raw actions');
      const expectedMessages = ['single', 'multiple'].flatMap(turnId => ['user', 'answer'].map(kind => ({
        threadId: 'resize-thread', turnId, itemId: `${turnId}-${kind}`,
      })));
      const rawCalls = requests.filter(row => row.id === 'user:conversation-addon-slot' && row.method === 'inspect').slice(-4);
      assert.ok(rawCalls.every(row => row.input.snapshot.id === row.input.itemId));
      assert.deepEqual(rawCalls.map(row => ({ threadId: row.input.threadId, turnId: row.input.turnId, itemId: row.input.itemId })), expectedMessages,
        'raw user and assistant actions route the displayed message snapshot');
      assert.deepEqual(requests.filter(row => row.id === 'user:conversation-addon-command' && row.method === 'inspect').slice(-4).map(row => ({
        threadId: row.input.threadId, turnId: row.input.turnId, itemId: row.input.item.id,
      })), expectedMessages, 'command user and assistant actions route the displayed message snapshot');
      await evaluate(() => window.__messageAddonNodes.commands[0].focus());
      await inventory(["command"]);
      await until(() => !document.querySelector('button[aria-label="Inspect raw message"]'));
      assert.equal(await evaluate(() => window.__messageAddonNodes.commands.every(node => node.isConnected)
        && document.activeElement === window.__messageAddonNodes.commands[0]), true,
      `${style}: removing raw actions must retain command nodes and focus`);
      await inventory(["slot", "command"]);
      await until(() => document.querySelectorAll('button[aria-label="Inspect raw message"]').length === 4);
      assert.equal(await evaluate(() => window.__messageAddonNodes.commands.every(node => node.isConnected)
        && document.activeElement === window.__messageAddonNodes.commands[0]
        && [...document.querySelectorAll('button[aria-label="Inspect raw message"]')].every(node => node.textContent === 'Raw 0')), true,
      're-enabling raw actions restarts only their own local state');
      await evaluate(() => {
        window.__messageAddonNodes.raw = [...document.querySelectorAll('button[aria-label="Inspect raw message"]')];
        window.__messageAddonNodes.raw[0].click(); window.__messageAddonNodes.raw[0].focus();
      });
      const failures = await evaluate(async () => (await window.wuu.testDesktopActivations()).filter(row => row.id === 'user:conversation-addon-broken' && row.status === 'failed').length);
      await inventory(["slot", "command", "broken"]);
      await until(async before => (await window.wuu.testDesktopActivations()).filter(row => row.id === 'user:conversation-addon-broken' && row.status === 'failed').length > before, failures);
      assert.equal(await evaluate(() => {
        const before = window.__messageAddonNodes;
        return before.raw.every(node => node.isConnected) && before.commands.every(node => node.isConnected)
          && before.native.every(node => node.isConnected) && before.raw[0].textContent === 'Raw 1'
          && document.activeElement === before.raw[0];
      }), true, 'a failed raw contribution cannot discard either working addon or native controls');
      await inventory(["slot"]);
      await until(() => !document.querySelector('button[aria-label="Inspect command message"]'));
      assert.equal(await evaluate(() => window.__messageAddonNodes.raw.every(node => node.isConnected)
        && window.__messageAddonNodes.raw[0].textContent === 'Raw 1'
        && document.activeElement === window.__messageAddonNodes.raw[0]), true,
      `${style}: removing commands must retain raw nodes, state and focus`);
      await inventory([]);
      await until(() => !document.querySelector('button[aria-label="Inspect raw message"]'));
      assert.equal(await evaluate(() => {
        const before = window.__messageNativeNodes;
        return [...before.messages, ...before.rows, ...before.buttons].every(node => node.isConnected)
          && document.querySelectorAll('.message-actions button').length === before.buttons.length;
      }), true, 'removing both addons restores exactly the original native controls without remounting messages');
      await evaluate(() => window.__addonNativeNodes.focus.focus());
      await preserved();
    }
    report.messageActionRequests = await evaluate(() => window.wuu.testMessageActionRequests());
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
    const failures = await evaluate(async () => (await window.wuu.testDesktopActivations()).filter(row => row.id === 'user:conversation-addon-broken' && row.status === 'failed').length);
    await inventory(["metrics", "peer", "broken"]);
    await until(async before => (await window.wuu.testDesktopActivations()).filter(row => row.id === 'user:conversation-addon-broken' && row.status === 'failed').length > before, failures);
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
  await check("addons belong to native turn bounds and preserve a paused reader in bubble and IM", async () => {
    for (const style of ["bubble", "im"]) {
      await inventory([]);
      await until(() => !document.querySelector('[data-peer-addon], [data-conversation-metrics-turn], [data-conversation-metrics-tool]'));
      await evaluate(style => { document.documentElement.dataset.messageStyle = style; }, style);
      await frames(20);
      const bounds = await evaluate(() => {
        const viewport = document.querySelector('.conversation-pane > .scroll-region');
        const rect = viewport.getBoundingClientRect();
        return { x: Math.round(rect.left + rect.width * 0.6), y: Math.round(rect.top + rect.height * 0.4) };
      });
      // Use real upward wheel input to relinquish following before positioning
      // the reading point, rather than mutating a private scroll controller.
      win.webContents.sendInputEvent({ type: "mouseWheel", ...bounds, deltaX: 0, deltaY: 120, canScroll: true });
      await frames(12);
      await evaluate(() => {
        const viewport = document.querySelector('.conversation-pane > .scroll-region');
        const answer = document.querySelector('[data-turn-id="multiple"] .agent-text p');
        viewport.scrollTop += answer.getBoundingClientRect().top - viewport.getBoundingClientRect().top - 140;
        viewport.dispatchEvent(new Event('scroll', { bubbles: true }));
      });
      await until(() => Boolean(document.querySelector('.jump-to-latest-pill')));
      await frames(20);
      const before = await evaluate(() => {
        const viewport = document.querySelector('.conversation-pane > .scroll-region');
        const first = document.querySelector('[data-turn-id="single"]');
        const answer = document.querySelector('[data-turn-id="multiple"] .agent-text p');
        window.__addonReadingPoint = { answer, first };
        return { y: answer.getBoundingClientRect().top, firstHeight: first.getBoundingClientRect().height,
          viewportTop: viewport.getBoundingClientRect().top, viewportHeight: viewport.clientHeight,
          distanceFromLatest: viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop };
      });
      assert.ok(before.firstHeight > before.viewportHeight * 1.5, 'the preceding native turn is genuinely tall');
      assert.ok(before.y > before.viewportTop && before.y < before.viewportTop + before.viewportHeight, 'the saved answer is visible');
      assert.ok(before.distanceFromLatest > before.viewportHeight, 'the reader is not clamped to latest');
      for (const enabled of [true, false]) {
        await inventory(enabled ? ["metrics", "peer"] : []);
        await until(enabled => document.querySelectorAll('[data-conversation-metrics-turn]').length === (enabled ? 2 : 0)
          && document.querySelectorAll('[data-peer-addon="turn"]').length === (enabled ? 2 : 0), enabled);
        await frames(12);
        const after = await evaluate(() => {
          const { first, answer } = window.__addonReadingPoint;
          return { y: answer.getBoundingClientRect().top, firstHeight: first.getBoundingClientRect().height,
            sameNode: answer.isConnected && answer === document.querySelector('[data-turn-id="multiple"] .agent-text p')
              && first === document.querySelector('[data-turn-id="single"]'),
            paused: Boolean(document.querySelector('.jump-to-latest-pill')),
            addons: [...document.querySelectorAll('[data-conversation-metrics-turn], [data-peer-addon="turn"]')].map(node => {
              const expectedID = node.dataset.conversationMetricsTurn ?? JSON.parse(node.dataset.address)[1];
              const turn = node.closest('[data-turn-id]');
              const rect = node.getBoundingClientRect(), turnRect = turn?.getBoundingClientRect();
              return { expectedID, nativeID: turn?.dataset.turnId, height: rect.height,
                nativeMatch: turn === window.__addonNativeNodes.turns.find(node => node.dataset.turnId === expectedID),
                inside: Boolean(turnRect && rect.top >= turnRect.top - 1 && rect.bottom <= turnRect.bottom + 1) };
            }) };
        });
        report.geometry.push({ style, enabled, before, after }); save();
        assert.equal(after.sameNode, true, 'the saved native answer and preceding turn are not remounted');
        assert.equal(after.paused, true, 'inventory changes do not resume following');
        assert.ok(Math.abs(after.y - before.y) <= 1, `${style}: inventory changed reading Y: ${before.y} -> ${after.y}`);
        if (enabled) {
          assert.equal(after.addons.length, 4);
          for (const addon of after.addons) {
            assert.equal(addon.nativeID, addon.expectedID, 'addon is inside its addressed native turn');
            assert.equal(addon.nativeMatch, true, 'the containing turn is the original native element');
            assert.equal(addon.inside, true, 'addon bottom is included in native turn bounds');
          }
          const footerHeight = after.addons.filter(addon => addon.expectedID === 'single').reduce((sum, addon) => sum + addon.height, 0);
          assert.ok(footerHeight > 0 && after.firstHeight - before.firstHeight >= footerHeight - 1,
            'native turn height accounts for enabled addon content');
        } else {
          assert.ok(Math.abs(after.firstHeight - before.firstHeight) <= 1, 'disabling addons releases their native turn height');
        }
      }
    }
    await inventory(["metrics", "peer"]);
    await until(() => document.querySelectorAll('[data-conversation-metrics-turn]').length === 2);
    await preserved();
  });
  await check("bubble and IM layouts retain usable addons in light/dark wide/narrow views", async () => {
    await inventory(["metrics", "peer", "slot", "command"]);
    await until(() => document.querySelectorAll('button[aria-label="Inspect command message"]').length === 4);
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
      for (const variant of ["user", "agent"]) {
        await evaluate(variant => {
          const button = document.querySelector(`[data-wuu-variant="${variant}"] button[aria-label="Inspect raw message"]`);
          button.scrollIntoView({ block: 'center' }); button.focus();
        }, variant);
        await frames();
        const actionGeometry = await evaluate(variant => {
          const raw = document.querySelector(`[data-wuu-variant="${variant}"] button[aria-label="Inspect raw message"]`);
          const row = raw.closest('.message-actions');
          const rectangles = [...row.querySelectorAll('button')].map(button => {
            const rect = button.getBoundingClientRect();
            return { label: button.getAttribute('aria-label'), x: rect.x, y: rect.y, width: rect.width, height: rect.height,
              inside: rect.left >= 0 && rect.right <= innerWidth && rect.top >= 0 && rect.bottom <= innerHeight };
          });
          return { rectangles, visible: getComputedStyle(row).display !== 'none' && Number(getComputedStyle(row).opacity) > 0 };
        }, variant);
        report.geometry.push({ style, theme, width, size, variant, messageActions: actionGeometry });
        assert.equal(actionGeometry.visible, true, 'focused action row is visible');
        assert.ok(actionGeometry.rectangles.every(rect => rect.inside && rect.width > 0 && rect.height > 0), 'native and addon controls are in view');
        for (let index = 1; index < actionGeometry.rectangles.length; index++) {
          const previous = actionGeometry.rectangles[index - 1], current = actionGeometry.rectangles[index];
          assert.ok(current.x >= previous.x + previous.width - 1, 'peer message actions do not overlap');
        }
        const messageName = `message-actions-${variant}-${name}`;
        fs.writeFileSync(path.join(output, messageName), (await win.webContents.capturePage()).toPNG()); report.captures.push(messageName);
      }
    }
    await evaluate(() => window.__addonNativeNodes.focus.focus());
  });
  await check("disabling all addons leaves no hidden UI", async () => {
    await inventory([]);
    await until(() => !document.querySelector('[data-peer-addon], [data-conversation-metrics-turn], [data-conversation-metrics-tool], button[aria-label="Inspect raw message"], button[aria-label="Inspect command message"]'));
    await preserved();
  });
  await check("the real side command routes addressed turn and tool addons through default-preview", async () => {
    win.setContentSize(1280, 900);
    await inventory(["metrics", "peer"]);
    await evaluate(() => {
      const input = document.querySelector('[data-main-conversation-composer] textarea');
      input.focus();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, '/side');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await until(() => Boolean(document.querySelector('.slash-command-item[data-command-name="side"]')));
    await evaluate(() => document.querySelector('.slash-command-item[data-command-name="side"]').click());
    await until(() => Boolean(document.querySelector('.side-thread-panel [data-peer-addon="turn"]')));
    await evaluate(() => {
      const toggle = document.querySelector('.side-thread-panel .turn-process-toggle');
      if (toggle.getAttribute('aria-expanded') === 'false') toggle.click();
    });
    await until(() => Boolean(document.querySelector('.side-thread-panel .process-surface-fold')));
    await evaluate(() => {
      const details = document.querySelector('.side-thread-panel .process-surface-fold');
      details.open = true; details.dispatchEvent(new Event('toggle'));
      window.__addonSideNative = { turn: document.querySelector('.side-thread-panel .turn'), details };
    });
    await until(() => Boolean(document.querySelector('.side-thread-panel [data-conversation-metrics-tool]')));
    assert.deepEqual(await evaluate(() => [...document.querySelectorAll('.side-thread-panel [data-peer-addon]')].map(node => ({
      target: node.dataset.peerAddon, owner: node.dataset.owner, address: JSON.parse(node.dataset.address),
      nativeTurn: node.closest('[data-turn-id]')?.dataset.turnId,
    }))), [
      { target: 'tool-activity', owner: 'wuu.default-preview', address: ['addon-side-thread', 'side-turn-addon-side-answer', 'addon-side-tool'], nativeTurn: 'side-turn-addon-side-answer' },
      { target: 'turn', owner: 'wuu.default-preview', address: ['addon-side-thread', 'side-turn-addon-side-answer', null], nativeTurn: 'side-turn-addon-side-answer' },
    ]);
    await evaluate(() => document.querySelector('.side-thread-panel [data-conversation-metrics-tool] button').click());
    await until(() => document.querySelector('.side-thread-panel [data-conversation-metrics-tool] pre')?.textContent === 'Reported preview result');
    await frames(12);
    const name = 'default-preview-side-addons.png';
    fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG()); report.captures.push(name);
    await inventory([]);
    await until(() => !document.querySelector('[data-peer-addon], [data-conversation-metrics-turn], [data-conversation-metrics-tool]'));
    assert.equal(await evaluate(() => window.__addonSideNative.turn.isConnected
      && window.__addonSideNative.turn === document.querySelector('.side-thread-panel .turn')
      && window.__addonSideNative.details.isConnected && window.__addonSideNative.details.open), true,
    'disabling preview addons preserves its native turn and disclosure');
  });
  assert.deepEqual(report.errors, []); clearTimeout(watchdog); report.passed = true; save(); app.exit(0);
}).catch(fail);
