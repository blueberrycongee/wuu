// Exercise the generated full-stack fixture in Chromium, not Node's unrestricted
// module loader. Called by TestPrepareSingleFileDesktopAndFullStack when
// CHROMIUM_BIN is set; no npm dependencies or desktop build are required.
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');

async function main() {
  assert.ok(process.env.CHROMIUM_BIN, 'Set CHROMIUM_BIN to a Chromium executable');
  assert.ok(process.argv[2], 'Pass the generated desktop.mjs path');
  const source = await fs.readFile(process.argv[2], 'utf8');
  const shell = await fs.readFile(path.join(__dirname, '../index.html'), 'utf8');
  const policy = shell.match(/http-equiv="Content-Security-Policy"\s+content="([^"]+)"/)[1];
  const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'wuu-plugin-csp-'));
  const probe = `
    const violations = [];
    document.addEventListener('securitypolicyviolation', event => {
      violations.push({ directive: event.effectiveDirective, uri: event.blockedURI });
    });
    try {
      const mod = await import('/desktop.mjs');
      const activated = await mod.activate({ pluginId: 'user:fullstack' });
      if (activated !== 'ui-ready:user:fullstack:call') throw new Error('Unexpected activation: ' + activated);
      let stubError = '';
      try { mod.default.executeTool({ arguments: { file: 'ignored' } }); }
      catch (error) { stubError = error.message; }
      if (!stubError.includes('only available in the plugin runtime')) throw new Error('Node builtin did not reject renderer execution');
      // A passing import is meaningful only while data: scripts stay forbidden.
      let blocked = false;
      try { await import('data:text/javascript,globalThis.cspBypassed=true'); }
      catch { blocked = true; }
      if (!blocked || globalThis.cspBypassed) throw new Error('CSP was weakened');
      await fetch('/result', { method: 'POST', body: JSON.stringify({ ok: true, activated, stubError, dataImportBlocked: blocked, violations }) });
    } catch (error) {
      await fetch('/result', { method: 'POST', body: JSON.stringify({ ok: false, error: String(error), violations }) });
    }
  `;
  let finish;
  const result = new Promise(resolve => { finish = resolve; });
  const server = http.createServer(async (req, res) => {
    if (req.url === '/result' && req.method === 'POST') {
      let body = '';
      for await (const chunk of req) body += chunk;
      res.end('ok');
      finish(JSON.parse(body));
      return;
    }
    res.setHeader('Content-Security-Policy', policy);
    res.setHeader('Content-Type', req.url.endsWith('.mjs') ? 'text/javascript' : 'text/html');
    res.end(req.url === '/desktop.mjs' ? source : req.url === '/probe.mjs' ? probe : '<!doctype html><script type="module" src="/probe.mjs"></script>');
  });
  let browser;
  let timer;
  let stderr = '';
  try {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    browser = spawn(process.env.CHROMIUM_BIN, [
      '--headless', '--no-sandbox', '--disable-gpu', '--no-first-run',
      '--disable-background-networking', `--user-data-dir=${profile}`,
      `http://127.0.0.1:${server.address().port}/`,
    ], { stdio: ['ignore', 'ignore', 'pipe'] });
    browser.stderr.on('data', chunk => { stderr += chunk; });
    const outcome = await Promise.race([
      result,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error('Chromium CSP probe timed out\n' + stderr)), 20000);
        browser.once('error', reject);
        browser.once('exit', code => reject(new Error('Chromium exited before result: ' + code + '\n' + stderr)));
      }),
    ]);
    console.log(JSON.stringify(outcome));
    assert.equal(outcome.ok, true, JSON.stringify(outcome));
  } finally {
    clearTimeout(timer);
    if (browser?.pid && browser.exitCode === null && browser.signalCode === null) {
      const exited = new Promise(resolve => browser.once('exit', resolve));
      browser.kill('SIGKILL');
      await exited;
    }
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    await fs.rm(profile, { recursive: true, force: true, maxRetries: 3 });
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
