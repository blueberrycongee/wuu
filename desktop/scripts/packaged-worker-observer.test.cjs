// Regression: Chromium can report an empty CDP worker target URL. Passing the
// gate still requires native worker traffic and exact packaged asset provenance.
const assert = require('node:assert/strict');
const { test } = require('node:test');
const vm = require('node:vm');
const { installWorkerObserver, findPackagedWorker } = require('./packaged-runtime-smoke.cjs');
const root = 'file:///tmp/isolated%20app/wuu.app/Contents/Resources/app.asar/out/renderer/assets/';

function fixture(wrappers = {}) {
  class NativeWorker {
    static sentinel = 42;
    constructor(url, options) { this.url = url; this.options = options; this.listeners = {}; this.sent = []; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    emit(type, event = {}) { for (const fn of this.listeners[type] || []) fn(event); }
    postMessage(...args) { this.sent.push(args); }
    terminate() { this.stopped = true; }
  }
  const context = vm.createContext({ Worker: NativeWorker, URL, location: { href: root + '../index.html' },
    fetch: async url => ({ text: async () => { if (!(url in wrappers)) throw Error('missing blob'); return wrappers[url]; } }) });
  vm.runInContext(`(${installWorkerObserver.toString()})()`, context);
  return { context, NativeWorker, records: () => JSON.parse(JSON.stringify(context.__wuuPackagedWorkers)) };
}

test('empty CDP URL needs no fallback when packaged worker actually responds; native behavior preserved', () => {
  const { context, NativeWorker, records } = fixture();
  const options = { type: 'module' };
  const worker = new context.Worker(new URL(root + 'ts.worker-BMazTI8A.js'), options);
  assert.ok(worker instanceof NativeWorker && worker instanceof context.Worker);
  assert.equal(context.Worker.sentinel, 42);
  assert.equal(worker.options, options);
  const payload = { request: 1 }, transfer = [];
  worker.postMessage(payload, transfer);
  assert.equal(worker.sent[0][0], payload);
  assert.equal(worker.sent[0][1], transfer);
  assert.equal(findPackagedWorker(records(), 'ts.worker', root), null, 'construction alone cannot pass');
  worker.emit('message', { data: { response: 1 } });
  assert.equal(findPackagedWorker(records(), 'ts.worker', root).responses, 1);
});

test('PDF blob wrapper must import packaged worker and receive native response', async () => {
  const url = root + 'pdf.worker.min-wgc6bjNh.mjs';
  const { context, records } = fixture({ 'blob:null/pdf': `await import("${url}");` });
  const worker = new context.Worker('blob:null/pdf', { type: 'module' });
  worker.emit('message', { data: { action: 'ready' } });
  worker.emit('message', { data: { action: 'test', data: true } });
  await new Promise(setImmediate);
  assert.equal(findPackagedWorker(records(), 'pdf.worker', root), null, 'ready/test-only native worker may coexist with fake-worker fallback');
  worker.emit('message', { data: { action: 'GetDoc', data: { sensitive: 'do not retain' } } });
  assert.equal(findPackagedWorker(records(), 'pdf.worker', root), null, 'document handshake alone does not prove page rendering');
  worker.emit('message', { data: { action: 'StartRenderPage', data: { sensitive: 'do not retain' } } });
  assert.equal(findPackagedWorker(records(), 'pdf.worker', root).sourceURL, url);
  assert.equal(JSON.stringify(records()).includes('do not retain'), false, 'worker payloads must not be logged');
  worker.terminate();
  assert.equal(worker.stopped, true);
  assert.equal(findPackagedWorker(records(), 'pdf.worker', root), null, 'terminated worker cannot conceal fake-worker fallback');
});

// These names are from the actual electron-vite release output, not invented
// basename approximations. Vite preserves PDF.js's .min source suffix.
test('real emitted PDF worker basename and supported non-minified variant are matched exactly', () => {
  const valid = { sourceURL: root + 'pdf.worker.min-wgc6bjNh.mjs', responses: 28,
    pdfDocument: true, pdfRenderPage: true, terminated: false, errors: [] };
  for (const file of ['pdf.worker.min-wgc6bjNh.mjs', 'pdf.worker-wgc6bjNh.mjs', 'pdf.worker.min-wgc6bjNh.js']) {
    assert.ok(findPackagedWorker([{ ...valid, sourceURL: root + file }], 'pdf.worker', root), file);
  }
  for (const url of [root + 'pdf.worker.minimum-wgc6bjNh.mjs', root + 'pdf.worker.min-wgc6bjNh.mjs.extra',
    root + 'pdf.worker.min-.mjs', root + 'pdf.worker.min-wgc6bjNh.mjs%0A', root + 'nested/pdf.worker.min-wgc6bjNh.mjs',
    root + 'nested%2Fpdf.worker.min-wgc6bjNh.mjs', root + 'nested%5Cpdf.worker.min-wgc6bjNh.mjs',
    root + '../pdf.worker.min-wgc6bjNh.mjs', root + 'pdf.worker.min-wgc6bjNh.mjs?source=packaged',
    root + 'pdf.worker.min-wgc6bjNh.mjs#packaged', root.replace('/assets/', '/assets-other/') + 'pdf.worker.min-wgc6bjNh.mjs',
    'https://example.com/pdf.worker.min-wgc6bjNh.mjs']) {
    assert.equal(findPackagedWorker([{ ...valid, sourceURL: url }], 'pdf.worker', root), null, url);
  }
});

test('failed workers, absent workers, wrong roots and spoofed blob wrappers fail closed', async () => {
  assert.equal(findPackagedWorker([], 'pdf.worker', root), null);
  for (const url of ['https://example.com/ts.worker.js', 'file:///checkout/ts.worker.js', root + '../ts.worker.js', root + 'other.js']) {
    const { context, records } = fixture();
    new context.Worker(url).emit('message');
    assert.equal(findPackagedWorker(records(), 'ts.worker', root), null, url);
  }
  for (const type of ['error', 'messageerror']) {
    const { context, records } = fixture();
    const worker = new context.Worker(root + 'ts.worker-BMazTI8A.js');
    worker.emit('message'); worker.emit(type, { message: 'failed' });
    assert.equal(findPackagedWorker(records(), 'ts.worker', root), null, type);
  }
  for (const wrapper of [`// ${root}pdf.worker.js\nawait import("https://example.com/pdf.worker.js");`, `await import("${root}pdf.worker.js"); throw Error("fallback");`, 'invalid']) {
    const { context, records } = fixture({ 'blob:null/pdf': wrapper });
    const worker = new context.Worker('blob:null/pdf');
    worker.emit('message', { data: { action: 'GetDoc' } });
    worker.emit('message', { data: { action: 'StartRenderPage' } });
    await new Promise(setImmediate);
    assert.equal(findPackagedWorker(records(), 'pdf.worker', root), null);
  }
});

// This exercises our CDP orchestration contract, not a browser implementation.
// Actual Electron injection and worker execution remain the packaged smoke gate.
const { activateWorkerObserver, workerObserverStatus } = require('./packaged-runtime-smoke.cjs');
function protocolFixture(fault) {
  let enabled = false, script, loader = 'old', reloaded = false, reads = 0;
  const { context } = fixture();
  context.document = { readyState: 'complete' };
  vm.runInContext("delete globalThis.__wuuPackagedWorkerObserver", context);
  const calls = [];
  const send = async (method, params) => {
    calls.push(method);
    if (method === 'Page.enable') enabled = true;
    if (method === 'Page.addScriptToEvaluateOnNewDocument') { script = params.source; return { identifier: 'registration' }; }
    if (method === 'Page.reload') reloaded = true;
    if (method === 'Page.getFrameTree') {
      if (reloaded && reads++ > 0 && fault !== 'stale') {
        loader = 'new';
        if (script && enabled && fault !== 'absent') {
          vm.runInContext(script, context); script = null;
          if (fault === 'wrong-token') context.__wuuPackagedWorkerObserver.token = 'old-run';
          if (fault === 'replaced') context.Worker = class Replacement {};
          if (fault === 'records') context.__wuuPackagedWorkers = null;
        }
      }
      return { frameTree: { frame: { loaderId: loader } } };
    }
    return {};
  };
  const evaluate = async fn => JSON.parse(JSON.stringify(vm.runInContext(`(${fn.toString()})()`, context)));
  const until = async (read, label) => { for (let i = 0; i < 4; i++) { const value = await read(); if (value) return value; } throw Error(label); };
  return { send, evaluate, until, calls };
}

test('CDP observer setup enables Page and waits for a new document with its active proxy', async () => {
  const f = protocolFixture();
  const result = await activateWorkerObserver(f.send, f.evaluate, f.until, 'run-token');
  assert.equal(result.loaderId, 'new');
  assert.equal(result.previousLoaderId, 'old');
  assert.equal(result.token, 'run-token');
  assert.equal(result.installed, true);
  assert.equal(result.proxyActive, true);
  assert.deepEqual(f.calls.slice(0, 4), ['Page.enable', 'Page.getFrameTree', 'Page.addScriptToEvaluateOnNewDocument', 'Page.reload']);
});

test('CDP observer setup rejects stale documents, missing state and replaced proxies', async () => {
  for (const fault of ['stale', 'absent', 'wrong-token', 'replaced', 'records']) {
    const f = protocolFixture(fault);
    await assert.rejects(activateWorkerObserver(f.send, f.evaluate, f.until, 'run-token'), /worker observer installed/, fault);
  }
  const { context } = fixture();
  context.document = { readyState: 'complete' };
  vm.runInContext("delete globalThis.__wuuPackagedWorkerObserver", context);
  const missing = vm.runInContext(`(${workerObserverStatus.toString()})()`, context);
  assert.equal(missing.installed, false, 'missing instrumentation is distinct from zero workers');
});
