const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { test } = require('node:test');
const { waitForNativeBlankPage } = require('./session-switch-native-page.cjs');

function contents(id, url = '', loading = true) {
  return Object.assign(new EventEmitter(), {
    id, url, loading, destroyed: false,
    getURL() { return this.url; },
    isLoadingMainFrame() { return this.loading; },
    isDestroyed() { return this.destroyed; },
  });
}
function owner(...pages) {
  return Object.assign(new EventEmitter(), {
    isDestroyed: () => false,
    contentView: { children: pages.map(webContents => ({ webContents })) },
  });
}

test('native readiness waits for delayed URL commitment and load completion after shell reveal', async () => {
  const shell = contents(1, 'file:///index.html', false);
  const overlay = contents(2, 'wuu-chrome://ui/chrome-overlay.html', false);
  const page = contents(3);
  let settled = false;
  const ready = waitForNativeBlankPage(owner(shell, overlay, page), new Set([shell.id]));
  ready.then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false, 'React shell visibility is not native URL readiness');
  page.url = 'about:blank';
  page.emit('did-navigate');
  await Promise.resolve();
  assert.equal(settled, false, 'Committed URL still needs its native document to finish');
  page.loading = false;
  page.emit('did-finish-load');
  assert.equal(await ready, page);
  assert.equal(page.listenerCount('did-finish-load'), 0);
  assert.equal(overlay.listenerCount('did-finish-load'), 0);
});

test('an already loaded page is selected only from the current owner and new contents', async () => {
  const oldPage = contents(1, 'about:blank', false);
  const page = contents(2, 'about:blank', false);
  assert.equal(await waitForNativeBlankPage(owner(oldPage, page), new Set([oldPage.id])), page);
});

test('multiple native blank pages fail rather than choosing an arbitrary guest', async () => {
  await assert.rejects(waitForNativeBlankPage(owner(contents(1, 'about:blank', false), contents(2, 'about:blank', false)), new Set()), /exactly one/);
});

test('uncommitted native navigation fails within its deadline and removes listeners', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const page = contents(1);
  const window = owner(page);
  const ready = waitForNativeBlankPage(window, new Set(), 5000);
  const failed = assert.rejects(ready, /Timed out.*native blank page/);
  t.mock.timers.tick(5000);
  await failed;
  assert.equal(page.listenerCount('did-finish-load'), 0);
  assert.equal(window.listenerCount('closed'), 0);
});

test('closing the owner cannot substitute a detached or foreign native page', async () => {
  const page = contents(1);
  const window = owner(page);
  const ready = waitForNativeBlankPage(window, new Set());
  const failed = assert.rejects(ready, /owner closed/);
  let settled = false;
  ready.then(() => { settled = true; }, () => { settled = true; });
  window.contentView.children = [];
  page.url = 'about:blank';
  page.loading = false;
  page.emit('did-finish-load');
  await Promise.resolve();
  assert.equal(settled, false, 'A detached page no longer belongs to this navigation');
  window.emit('closed');
  await failed;
  assert.equal(page.listenerCount('did-finish-load'), 0);
});
