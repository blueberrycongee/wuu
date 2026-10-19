const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out', 'main', 'external-capabilities-e2e.mjs');

async function main() {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  await require('esbuild').build({
    entryPoints: [path.join(__dirname, 'external-capabilities-e2e-entry.ts')], outfile: output,
    bundle: true, platform: 'node', format: 'esm', target: 'node22', external: ['electron'],
    plugins: [{ name: 'svg', setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: 'svg' }));
      build.onLoad({ filter: /.*/, namespace: 'svg' }, args => ({ contents: fs.readFileSync(args.path, 'utf8'), loader: 'text' }));
    } }],
  });
  if (process.env.WUU_EXTERNAL_CAPABILITIES_BUILD_ONLY === '1') return;
  const fixture = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-external-capabilities-')));
  try {
    const result = spawnSync(String(require('electron')), [
      ...(process.platform === 'linux' ? ['--no-sandbox', ...(!process.env.DISPLAY ? ['--ozone-platform=headless', '--ozone-override-screen-size=1440,1000'] : [])] : []), output,
    ], { stdio: 'inherit', env: { ...process.env, WUU_EXTERNAL_CAPABILITIES_FIXTURE: fixture,
      WUU_EXTERNAL_CAPABILITIES_NODE: process.execPath }, timeout: 180_000 });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally {
    fs.rmSync(fixture, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
