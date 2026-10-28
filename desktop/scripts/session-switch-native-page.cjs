const assert = require('node:assert/strict');

// The shell reveals a newly attached view before its asynchronous loadURL has
// committed. Native lifecycle events, not React paint, establish page readiness.
function waitForNativeBlankPage(owner, existingContents, timeout = 30000) {
  const candidates = owner.contentView.children.map(view => view.webContents)
    .filter(contents => contents && !existingContents.has(contents.id));
  return new Promise((resolve, reject) => {
    const events = ['did-navigate', 'did-finish-load', 'did-stop-loading', 'destroyed'];
    const cleanup = () => {
      clearTimeout(timer);
      owner.removeListener('closed', closed);
      for (const contents of candidates) for (const event of events) contents.removeListener(event, check);
    };
    const fail = error => { cleanup(); reject(error); };
    const closed = () => fail(new Error('Native browser page owner closed before readiness'));
    const check = () => {
      try {
        if (owner.isDestroyed()) return closed();
        const attached = new Set(owner.contentView.children.map(view => view.webContents));
        const pages = candidates.filter(contents => !contents.isDestroyed() && attached.has(contents)
          && contents.getURL() === 'about:blank');
        assert(pages.length <= 1, 'Opening a browser tab must create exactly one native blank page');
        if (pages.length === 1 && !pages[0].isLoadingMainFrame()) { cleanup(); resolve(pages[0]); }
      } catch (error) { fail(error); }
    };
    const timer = setTimeout(() => fail(new Error(`Timed out waiting for the native blank page: ${JSON.stringify(candidates.map(contents => ({
      id: contents.id, url: contents.isDestroyed() ? '<destroyed>' : contents.getURL(),
    })))}`)), timeout);
    owner.on('closed', closed);
    for (const contents of candidates) for (const event of events) contents.on(event, check);
    check();
  });
}

module.exports = { waitForNativeBlankPage };
