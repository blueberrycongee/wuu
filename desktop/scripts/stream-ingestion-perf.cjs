// Synthetic text only; no app-server or user profile. Run with the checkout's
// Wuu Dev host on macOS, or `electron scripts/stream-ingestion-perf.cjs` elsewhere.
// Measures the ingestion hot path, not model latency or end-to-end rendering.
// Timing is diagnostic; text, visibility transitions, delivery, and cleanup are
// asserted independently of hardware. Writes out/stream-ingestion-perf/report.json.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { app, BrowserWindow } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_STREAM_INGESTION_OUTPUT || path.join(desktop, 'out/stream-ingestion-perf'));
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-stream-ingestion-'));
app.setPath('userData', profile);
app.on('window-all-closed', () => {});
const timeout = setTimeout(() => { console.error('Stream ingestion probe timed out'); app.exit(1); }, 120_000);
let server;
let win;

app.whenReady().then(async () => {
  const { createServer } = await import('vite');
  const { default: react } = await import('@vitejs/plugin-react');
  server = await createServer({
    configFile: false, root: desktop,
    cacheDir: path.join(desktop, 'node_modules/.vite/stream-ingestion-perf'),
    plugins: [react(), { name: 'stream-ingestion-page', configureServer(vite) {
      vite.middlewares.use('/__stream-ingestion', async (_request, response, next) => {
        response.setHeader('Content-Type', 'text/html; charset=utf-8');
        try {
          response.end(await vite.transformIndexHtml('/__stream-ingestion', '<!doctype html><html><head><meta charset="utf-8"><title>Wuu stream ingestion probe</title></head><body></body></html>'));
        } catch (error) { next(error); }
      });
    } }],
    optimizeDeps: { entries: ['src/renderer/AppState.ts'] },
    resolve: { dedupe: ['react', 'react-dom'] },
    server: { host: '127.0.0.1', port: 0, watch: { ignored: ['**/out/**', '**/build/**'] }, fs: { allow: [path.resolve(desktop, '..')] } },
  });
  await server.listen();
  win = new BrowserWindow({ show: false, webPreferences: { backgroundThrottling: false, contextIsolation: true, nodeIntegration: false, sandbox: true } });
  win.webContents.on('console-message', event => { if (event.level === 'error') console.error(event.message); });
  await win.loadURL(`http://127.0.0.1:${server.httpServer.address().port}/__stream-ingestion`);
  const samples = await win.webContents.executeJavaScript(`(${measure.toString()})()`);
  assert.equal(samples.length, 24);
  const report = {
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: desktop, encoding: 'utf8' }).trim(),
    modified: execFileSync('git', ['status', '--porcelain'], { cwd: desktop, encoding: 'utf8' }).trim().length > 0,
    executable: app.getPath('exe'), electron: process.versions.electron, chromium: process.versions.chrome,
    scope: 'Synthetic renderer notification ingestion; excludes model, IPC, and rendering time.', samples,
  };
  fs.mkdirSync(output, { recursive: true });
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(report));
  await finish(0);
}).catch(async error => { console.error(error); await finish(1); });

async function measure() {
  const { handleStreamingNotification, initialState } = await import('/src/renderer/AppState.ts');
  const { streamTextKey, streamTextStore } = await import('/src/renderer/StreamText.tsx');
  const samples = [];
  async function sample(count, whitespace, subscribed, method) {
    const turnID = 'synthetic-ingestion-turn';
    const key = streamTextKey(turnID, 'message', 'text');
    const chunk = whitespace ? ' '.repeat(64) : 'Streaming text with enough content to stress retained buffers.\n';
    let release;
    const delivered = subscribed ? new Promise(resolve => { release = streamTextStore.subscribe(key, resolve); }) : undefined;
    try {
      let transitions = 0;
      const started = performance.now();
      for (let index = 0; index < count; index++) {
        const handling = handleStreamingNotification({ kind: 'notification', workdir: '/synthetic', message: {
          method, params: { turn_id: turnID, item_id: 'message', delta: chunk },
        } }, initialState);
        if (handling === 'stream-state') transitions++;
      }
      const ingestionMs = performance.now() - started;
      const expected = chunk.repeat(count);
      if (streamTextStore.get(key) !== expected) throw new Error('Stream text changed');
      if (transitions !== (whitespace ? 0 : 1)) throw new Error('Incorrect first-visible-text transitions');
      if (delivered && await delivered !== expected) throw new Error('Subscriber did not receive the complete stream');
      return { count, whitespace, subscribed, method, characters: expected.length, ingestionMs, transitions };
    } finally {
      release?.();
      streamTextStore.clearTurn(turnID);
      const stats = streamTextStore.stats();
      if (stats.entryCount || stats.listenerCount || stats.totalValueLength) throw new Error('Stream resources remained after cleanup');
    }
  }
  await sample(100, false, false, 'item/agentMessage/delta');
  for (const count of [5000, 10000, 20000]) for (const whitespace of [false, true]) {
    for (let repeat = 0; repeat < 3; repeat++) samples.push(await sample(count, whitespace, false, 'item/agentMessage/delta'));
  }
  for (const method of ['item/agentMessage/delta', 'item/reasoning/delta']) {
    for (let repeat = 0; repeat < 3; repeat++) samples.push(await sample(20000, false, true, method));
  }
  return samples;
}

async function finish(code) {
  clearTimeout(timeout);
  win?.destroy();
  await server?.close();
  app.exit(code);
}
