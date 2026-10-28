// Run with Electron while the isolated desktop/dev/settings fixture is served.
// WUU_CONNECTION_DETAIL_URL overrides http://127.0.0.1:5206/dev/settings/.
// Real renderer geometry and pointer input; no product preload or credentials.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { app, BrowserWindow } = require('electron');

const output = path.resolve(__dirname, '../../artifacts/connection-detail-layout-e2e');
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-connection-layout-'));
const baseURL = process.env.WUU_CONNECTION_DETAIL_URL || 'http://127.0.0.1:5206/dev/settings/';
const evidence = { scenarios: [], screenshots: [] };
let win;
let scenario;
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', profile);
app.on('quit', () => fs.rmSync(profile, { recursive: true, force: true }));

const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(fn, arg) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, arg)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out: ${fn}`);
}
const settle = () => evaluate(async () => {
  await document.fonts.ready;
  await Promise.all(document.getAnimations().filter(animation =>
    animation.playState === 'running' && Number.isFinite(animation.effect.getComputedTiming().endTime)
  ).map(animation => animation.finished.catch(() => {})));
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
});
const save = () => fs.writeFileSync(path.join(output, 'evidence.json'), JSON.stringify(evidence, null, 2));
async function capture(suffix) {
  const filename = `${scenario.name}-${suffix}.png`;
  fs.writeFileSync(path.join(output, filename), (await win.webContents.capturePage()).toPNG());
  evidence.screenshots.push(filename);
  save();
}
async function target(selector) {
  await evaluate(selector => {
    const node = document.querySelector(selector);
    if (!node) throw new Error(`Missing control: ${selector}`);
    node.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' });
  }, selector);
  await settle();
  return evaluate(selector => {
    const node = document.querySelector(selector);
    const rect = node.getBoundingClientRect();
    const x = Math.round(rect.x + rect.width / 2), y = Math.round(rect.y + rect.height / 2);
    return { x, y, reachable: !node.disabled && node.contains(document.elementFromPoint(x, y)) };
  }, selector);
}
async function click(selector) {
  const point = await target(selector);
  assert.ok(point.reachable, `Control must remain reachable: ${selector} ${JSON.stringify(point)}`);
  win.webContents.sendInputEvent({ type: 'mouseMove', x: point.x, y: point.y });
  win.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, x: point.x, y: point.y });
  win.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, x: point.x, y: point.y });
  await settle();
}

// Measure the rendered contract, including both scrollport and viewport bounds.
const geometry = () => evaluate(() => {
  const rect = node => {
    const { left, right, top, bottom, width, height } = node.getBoundingClientRect();
    return { left, right, top, bottom, width, height, centerY: top + height / 2 };
  };
  const scroll = document.querySelector('.settings-scroll');
  const page = document.querySelector('.connection-page');
  const mark = page.querySelector('.connection-page-watermark');
  const style = getComputedStyle(mark);
  const panel = page.querySelector('.connection-credential-panel, .connection-credential-entry > .connection-credential-menu');
  const enabledHeading = page.querySelector('.connection-model-table-heading > :nth-child(3), .connection-model-head > :nth-child(3)');
  // Text ranges reveal glyphs overflowing a squeezed label's own layout box.
  const textRects = node => {
    if (!node || !panel || !panel.getClientRects().length) return [];
    const bounds = panel.getBoundingClientRect();
    const ranges = [];
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      const text = walker.currentNode;
      if (!text.textContent.trim() || getComputedStyle(text.parentElement).visibility !== 'visible') continue;
      const range = document.createRange();
      range.selectNodeContents(text);
      ranges.push(...range.getClientRects());
    }
    // Input values have no DOM text nodes; their visible control is the boundary.
    for (const input of node.querySelectorAll('input, textarea, select')) {
      if ((input.value || input.placeholder) && getComputedStyle(input).visibility === 'visible') ranges.push(input.getBoundingClientRect());
    }
    return ranges.map(box => ({
      left: Math.max(box.left, bounds.left, 0), right: Math.min(box.right, bounds.right, innerWidth),
      top: Math.max(box.top, bounds.top, 0), bottom: Math.min(box.bottom, bounds.bottom, innerHeight),
    })).filter(box => box.right > box.left && box.bottom > box.top);
  };
  return {
    viewport: { width: innerWidth, height: innerHeight },
    scroll: { ...rect(scroll), clientWidth: scroll.clientWidth, scrollWidth: scroll.scrollWidth, scrollLeft: scroll.scrollLeft },
    document: { clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, scrollLeft: document.scrollingElement.scrollLeft },
    page: rect(page),
    watermark: { ...rect(mark), opacity: Number(style.opacity), visible: style.display !== 'none' && style.visibility !== 'hidden' },
    enabledColumn: enabledHeading ? rect(enabledHeading) : null,
    rows: [...page.querySelectorAll('.connection-model-row')].filter(row => row.querySelector('[role="switch"]')).map(row => ({
      id: row.querySelector('.connection-model-id').textContent,
      name: rect(row.querySelector('.model-choice-main, .connection-model-name')),
      modelID: rect(row.querySelector('.connection-model-id')),
      enabled: rect(row.querySelector('[role="switch"]')),
      order: rect(row.querySelector('.connection-model-order')),
      orderButtons: [...row.querySelectorAll('.connection-model-order button')].map(rect),
    })),
    panel: panel && panel.getClientRects().length ? { ...rect(panel), clientWidth: panel.clientWidth, scrollWidth: panel.scrollWidth } : null,
    credentialRows: [...(panel?.querySelectorAll('.settings-row') || [])].map(row => ({
      label: row.querySelector('.settings-row-label-title')?.textContent.trim(),
      labelText: textRects(row.querySelector('.settings-row-label-title')),
      valueText: textRects(row.querySelector('.settings-row-control, .settings-row-control-block')),
    })).filter(row => row.labelText.length && row.valueText.length),
  };
});
function assertLayout(measured) {
  const context = `${scenario.name}: ${JSON.stringify(measured)}`;
  assert.ok(measured.scroll.scrollWidth <= measured.scroll.clientWidth, `Settings must not scroll horizontally: ${context}`);
  assert.ok(measured.document.scrollWidth <= measured.document.clientWidth, `Document must not scroll horizontally: ${context}`);
  assert.ok(measured.page.left >= measured.scroll.left - 1 && measured.page.right <= measured.scroll.right + 1,
    `Connection page must fit its settings scrollport: ${context}`);
  assert.ok(measured.page.left >= -1 && measured.page.right <= measured.viewport.width + 1,
    `Connection page must fit the viewport: ${context}`);
  const mark = measured.watermark;
  assert.ok(mark.visible && mark.opacity > 0 && mark.width >= 300 && mark.height >= 300,
    `Large decorative watermark must remain rendered: ${context}`);
  assert.ok(measured.rows.length > 0, `Expected model rows: ${context}`);
  for (const row of measured.rows) {
    assert.ok(Math.abs(row.enabled.centerY - row.name.centerY) <= 2 && Math.abs(row.enabled.centerY - row.modelID.centerY) <= 2,
      `Model switch must share its name/ID row: ${JSON.stringify(row)}`);
    assert.ok(row.modelID.right <= row.enabled.left + 1 && row.enabled.right <= row.order.left + 1,
      `Model switch must sit between ID and ordering controls: ${JSON.stringify(row)}`);
    if (measured.enabledColumn) {
      const centerX = (row.enabled.left + row.enabled.right) / 2;
      assert.ok(centerX >= measured.enabledColumn.left - 1 && centerX <= measured.enabledColumn.right + 1,
        `Model switch must align with the enabled header: ${JSON.stringify(row)}`);
    }
    assert.ok(row.orderButtons.length > 0 && row.orderButtons.every(button =>
      button.left >= row.order.left - 1 && button.right <= measured.page.right + 1 &&
      Math.abs(button.centerY - row.enabled.centerY) <= 2),
    `Ordering controls must remain on the model row and inside the page: ${JSON.stringify(row)}`);
  }
  if (measured.panel) {
    assert.ok(measured.panel.left >= Math.max(0, measured.scroll.left) - 1 &&
      measured.panel.right <= Math.min(measured.viewport.width, measured.scroll.right) + 1,
    `Credentials panel must fit the visible settings area: ${context}`);
    assert.ok(measured.panel.scrollWidth <= measured.panel.clientWidth, `Credentials panel must not overflow horizontally: ${context}`);
    for (const row of measured.credentialRows) {
      assert.ok(!row.labelText.some(label => row.valueText.some(value =>
        Math.min(label.right, value.right) - Math.max(label.left, value.left) > 1 &&
        Math.min(label.bottom, value.bottom) - Math.max(label.top, value.top) > 1)),
      `Credentials label text must not overlap its value/control: ${JSON.stringify(row)}`);
    }
  }
}

app.whenReady().then(async () => {
  win = new BrowserWindow({ width: 1280, height: 1000, show: false, webPreferences: {
    sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false,
  } });
  win.showInactive();
  for (const detail of ['provider', 'engine']) for (const theme of ['light', 'dark']) for (const width of [1280, 760]) {
    scenario = { name: `${detail}-${theme}-${width}-20px`, detail, theme, width };
    evidence.scenarios.push(scenario);
    try {
      const url = new URL(baseURL);
      for (const [key, value] of Object.entries({ page: 'providers', connections: 'quota', detail, theme, size: '20' })) url.searchParams.set(key, value);
      scenario.url = url.href;
      win.setContentSize(width, 1000);
      await win.loadURL(url.href);
      await waitFor(width => innerWidth === width && document.querySelector('.connection-model-row [role="switch"]'), width);
      await settle();
      scenario.initial = await geometry();
      await capture('detail');
      assertLayout(scenario.initial);

      // Exercise the actual scrollport rather than hiding overflow in the test.
      await evaluate(() => {
        document.querySelector('.settings-scroll').scrollLeft = 10000;
        document.scrollingElement.scrollLeft = 10000;
      });
      await settle();
      scenario.afterHorizontalScroll = await geometry();
      assertLayout(scenario.afterHorizontalScroll);
      assert.equal(scenario.afterHorizontalScroll.scroll.scrollLeft, 0, 'Horizontal scrolling must not strand the page');
      assert.equal(scenario.afterHorizontalScroll.document.scrollLeft, 0, 'Horizontal scrolling must not shift the document');
      assert.ok(Math.abs(scenario.initial.page.left - scenario.afterHorizontalScroll.page.left) <= 1, 'Horizontal scrolling must not move the page');
      assert.ok((await target('.connection-page-back')).reachable, 'Back must stay reachable after horizontal scroll');

      const toggle = '.connection-model-row [role="switch"]:not(:disabled)';
      scenario.toggleBefore = await evaluate(selector => document.querySelector(selector).getAttribute('aria-checked'), toggle);
      await click(toggle);
      await waitFor(({ selector, before }) => document.querySelector(selector)?.getAttribute('aria-checked') !== before,
        { selector: toggle, before: scenario.toggleBefore });
      await capture('model-toggle');
      await click(toggle);
      await waitFor(({ selector, before }) => document.querySelector(selector)?.getAttribute('aria-checked') === before,
        { selector: toggle, before: scenario.toggleBefore });

      const entry = '.connection-credential-menu > summary, [data-testid="engine-auth-entry"]';
      scenario.hasCredentials = await evaluate(selector => Boolean(document.querySelector(selector)), entry);
      assert.ok(scenario.hasCredentials, 'Provider and engine fixtures must expose their credentials entry');
      if (scenario.hasCredentials) {
        await click(entry);
        await waitFor(() => Boolean(document.querySelector('details.connection-credential-menu[open], [data-testid="engine-auth-entry"][aria-expanded="true"]')));
        scenario.credentials = await geometry();
        await capture('credentials');
        assert.ok(scenario.credentials.panel, 'Opening credentials must reveal a panel');
        assertLayout(scenario.credentials);
        const control = '.connection-credential-panel button:not(:disabled), .connection-credential-entry > .connection-credential-menu button:not(:disabled)';
        assert.ok((await target(control)).reachable, 'Credentials controls must remain reachable');
        assert.equal(await evaluate(selector => {
          const node = document.querySelector(selector);
          node.focus({ preventScroll: true });
          return document.activeElement === node;
        }, control), true, 'Credentials controls must accept focus');
        await click(entry);
        await waitFor(() => !document.querySelector('details.connection-credential-menu[open], [data-testid="engine-auth-entry"][aria-expanded="true"]'));
      }
      await click('.connection-page-back');
      await waitFor(() => !document.querySelector('.connection-page'));
      scenario.passed = true;
    } catch (error) {
      scenario.passed = false;
      scenario.error = error.stack || String(error);
      await capture('failure').catch(() => {});
      console.error(`FAIL: ${scenario.name}: ${error.message}`);
    }
    save();
  }
  const failed = evidence.scenarios.filter(result => !result.passed);
  assert.equal(failed.length, 0, `${failed.length}/${evidence.scenarios.length} layouts failed: ${failed.map(result => result.name).join(', ')}. Evidence: ${output}`);
  console.log(`PASS: ${evidence.scenarios.length} connection detail layouts. Evidence: ${output}`);
  win.close();
  app.quit();
}).catch(async error => {
  evidence.error = error.stack || String(error);
  save();
  console.error(error);
  app.exit(1);
});
