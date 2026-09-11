// Real browser layout and focus with production pickers/forms; only account
// transport and model inventory are fixtures. Native IME still needs device tests.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { createServer } from 'vite';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const server = await createServer({ root, server: { host: '127.0.0.1', port: 0, strictPort: false } });
let browser;
try {
  await server.listen();
  const url = `http://127.0.0.1:${server.httpServer.address().port}/test/phone-interactions.html`;
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto(url);
  const draft = page.getByRole('textbox', { name: 'Draft' });
  await draft.fill('Keep my draft');
  await page.setViewportSize({ width: 390, height: 420 });
  await page.locator('.codex-runtime-trigger').tap();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => document.querySelector('dialog[data-ready="true"]'));
  assert.equal(await draft.inputValue(), 'Keep my draft');
  assert.equal(await draft.evaluate(node => document.activeElement === node), false);
  assert.equal(await page.locator('dialog').evaluate(node => node.matches(':modal')), true);
  await page.locator('.runtime-panel-context button').last().tap();
  await page.getByRole('menuitemradio', { name: 'second', exact: true }).tap();
  await page.locator('.runtime-panel-model').tap();
  const last = page.getByRole('menuitemradio', { name: 'Model 29', exact: true });
  await last.scrollIntoViewIfNeeded();
  await last.tap();
  assert.equal(await page.getByTestId('selection').textContent(), 'second/model-29');
  // Escape is also the event Android routes to the innermost menu.
  await page.locator('[role="menu"]').evaluate(node => node.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })));
  await page.waitForFunction(() => !document.querySelector('dialog'));
  assert.equal(await draft.evaluate(node => document.activeElement === node), false);
  assert.equal(await draft.inputValue(), 'Keep my draft');
  await page.locator('.codex-runtime-trigger').tap();
  await page.locator('dialog').tap({ position: { x: 10, y: 10 } });
  await page.waitForFunction(() => !document.querySelector('dialog'));
  for (const size of [{ width: 390, height: 340 }, { width: 800, height: 122 }]) {
    await page.locator('.codex-runtime-trigger').tap();
    await page.locator('.runtime-panel-model').tap();
    const search = page.getByRole('searchbox');
    await search.fill('Model 29');
    await page.setViewportSize(size);
    await last.scrollIntoViewIfNeeded();
    const bounds = await last.boundingBox();
    assert.ok(bounds.height >= 44 && bounds.y >= 0 && bounds.y + bounds.height <= size.height);
    await last.tap();
    await page.locator('.composer-mobile-sheet-panel > header button').tap();
    await page.waitForFunction(() => !document.querySelector('dialog'));
    assert.equal(await draft.evaluate(node => document.activeElement === node), false);
    await page.setViewportSize({ width: 390, height: 844 });
  }

  // Resize alone moves the trigger after the shell writes height on the next
  // frame. The floating menu must follow without another scroll/resize event.
  await page.goto(`${url}?anchored`);
  await page.getByRole('button', { name: 'Anchored menu' }).tap();
  await page.setViewportSize({ width: 390, height: 420 });
  await page.waitForFunction(() => {
    const anchor = document.querySelector('main > div').getBoundingClientRect();
    const menu = document.querySelector('[data-floating-menu-owner]').getBoundingClientRect();
    return Math.abs(anchor.top - menu.bottom - 8) < 2;
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => {
    const anchor = document.querySelector('main > div').getBoundingClientRect();
    const menu = document.querySelector('[data-floating-menu-owner]').getBoundingClientRect();
    return Math.abs(anchor.top - menu.bottom - 8) < 2;
  });

  await page.goto(`${url}?login`);
  const password = page.locator('input[type="password"]');
  await password.waitFor();
  const before = await page.locator('.account-page-header').boundingBox();
  await password.fill('fixture-only-password');
  for (const height of [420, 340, 500]) {
    await page.setViewportSize({ width: 390, height });
    await page.waitForFunction(() => {
      const field = document.querySelector('input[type="password"]').getBoundingClientRect();
      return field.top >= 0 && field.bottom <= visualViewport.height;
    });
    const after = await page.locator('.account-page-header').boundingBox();
    assert.equal(after.height, before.height);
    assert.equal(await password.inputValue(), 'fixture-only-password');
  }
  assert.deepEqual(errors, []);
  console.log('Phone picker focus, modality, scrolling, viewport anchoring and login resize passed');
} finally {
  await browser?.close();
  await server.close();
}
