// Anonymous public navigation in the disposable acceptance profile. Keep this
// diagnostic separate from deterministic, offline resize regression gates.
const fs = require('node:fs');
const path = require('node:path');

module.exports = async function probeLoading({ view, output, navigate, capture, delay }) {
  const wc = view.webContents;
  if (!wc.debugger.isAttached()) wc.debugger.attach('1.3');
  await wc.debugger.sendCommand('Network.enable');
  await wc.debugger.sendCommand('Page.enable');
  await wc.debugger.sendCommand('Runtime.enable');
  const started = Date.now(), requests = new Map();
  const result = { url: 'https://www.notion.so/', scope: 'Anonymous fresh isolated Linux profile; not the existing macOS tab', samples: [], errors: [] };
  const safeURL = value => { try { const url = new URL(value); return url.origin + url.pathname; } catch { return ''; } };
  const listener = (_event, method, event) => {
    const at = Date.now() - started;
    if (method === 'Network.requestWillBeSent') {
      requests.set(event.requestId, { url: safeURL(event.request.url), type: event.type, startMs: at });
    } else if (method === 'Network.responseReceived') {
      const entry = requests.get(event.requestId);
      if (entry) Object.assign(entry, { responseMs: at, status: event.response.status, protocol: event.response.protocol, fromDiskCache: event.response.fromDiskCache, timing: event.response.timing });
    } else if (method === 'Network.loadingFinished') {
      const entry = requests.get(event.requestId); if (entry) Object.assign(entry, { endMs: at, bytes: event.encodedDataLength });
    } else if (method === 'Network.loadingFailed') {
      const entry = requests.get(event.requestId); if (entry) Object.assign(entry, { endMs: at, error: event.errorText, canceled: event.canceled });
    } else if (method === 'Runtime.exceptionThrown') {
      result.errors.push({ atMs: at, text: event.exceptionDetails.exception?.description || event.exceptionDetails.text });
    }
  };
  wc.debugger.on('message', listener);
  try {
    await navigate(result.url);
    for (let i = 0; i < 30; i++) {
      await delay(1000);
      const snapshot = await wc.debugger.sendCommand('Runtime.evaluate', {
        expression: `JSON.stringify({title:document.title,readyState:document.readyState,text:document.body?.innerText.slice(0,400),textLength:document.body?.innerText.length,viewport:[innerWidth,innerHeight],navigation:performance.getEntriesByType('navigation').map(n=>n.toJSON()),paint:performance.getEntriesByType('paint').map(p=>({name:p.name,startTime:p.startTime}))})`,
        returnByValue: true,
      });
      const state = snapshot.result.value ? JSON.parse(snapshot.result.value) : { exception: snapshot.exceptionDetails?.text };
      result.samples.push({ atMs: Date.now() - started, loading: wc.isLoading(), visible: view.getVisible(), ...state });
      if (i >= 4 && state.readyState === 'complete' && state.textLength > 500) break;
    }
    result.finalURL = safeURL(wc.getURL());
    await capture('notion-loading-probe');
  } catch (error) { result.error = String(error); }
  finally {
    wc.debugger.removeListener('message', listener);
    await wc.debugger.sendCommand('Network.disable');
    result.requests = [...requests.values()];
    fs.writeFileSync(path.join(output, 'notion-loading.json'), JSON.stringify(result, null, 2));
  }
  return result;
};
