const assert = require('node:assert/strict');
const { test } = require('node:test');
const { createErrorStyleProvider } = require('./error-message-style-provider.cjs');

// Failure cases: background title consumes the next turn's 503; a title
// arriving during its retry overwrites the pending main-turn response.
for (const titleFirst of [true, false]) {
  test(`title traffic cannot consume or replace main retry (title first: ${titleFirst})`, { timeout: 5000 }, async () => {
    const requests = [];
    const fixture = createErrorStyleProvider(requests, 'Synthetic authentication failure.');
    await new Promise(resolve => fixture.server.listen(0, '127.0.0.1', resolve));
    const url = `http://127.0.0.1:${fixture.server.address().port}/v1/chat/completions`;
    const active = [];
    const send = model => { const request = fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ model, stream: true, messages: [{ role: 'system', content: model === 'fixture-title' ? 'You are a title generator.' : 'Synthetic agent' }, { role: 'user', content: 'Synthetic request' }] }) }); active.push(request); request.catch(() => {}); return request; };
    fixture.mode = 'retry';
    try {
      if (titleFirst) {
        const title = await send('fixture-title');
        assert.equal(title.status, 200, 'title must not consume the main-turn 503');
        await title.text();
      }
      const failed = await send('fixture');
      assert.equal(failed.status, 503);
      await failed.text();
      const mainArrived = new Promise(resolve => fixture.server.once('request', request => request.once('end', resolve)));
      const pending = send('fixture');
      await mainArrived;
      const mainResponse = fixture.blockedRetry;
      // Observe receipt deterministically without waiting for a blocked HTTP body.
      const arrived = new Promise(resolve => fixture.server.once('request', request => request.once('end', resolve)));
      const title = send('fixture-title');
      await arrived;
      assert.equal(fixture.blockedRetry, mainResponse, 'title must not replace the pending main response');
      fixture.reply(mainResponse);
      assert.equal((await pending).status, 200);
      assert.equal((await title).status, 200);
      assert.equal(requests.filter(item => item.role === 'turn' && item.mode === 'retry').length, 1);
    } finally { fixture.server.closeAllConnections(); await new Promise(resolve => fixture.server.close(resolve)); await Promise.allSettled(active); }
  });
}
