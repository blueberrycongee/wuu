// Real X11 acceptance. Run after electron-vite build and build:cua-linux.
// CI supplies an isolated Xvfb display with a window manager and compositor.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out', 'main', 'linux-cua-e2e.mjs');
async function main() {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  await require('esbuild').build({
    entryPoints: [path.join(__dirname, 'linux-cua-e2e-entry.ts')], outfile: output,
    bundle: true, platform: 'node', format: 'esm', target: 'node22', external: ['electron'],
    plugins: [{ name: 'svg', setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: 'svg' }));
      build.onLoad({ filter: /.*/, namespace: 'svg' }, args => ({ contents: fs.readFileSync(args.path, 'utf8'), loader: 'text' }));
    } }],
  });
  if (process.env.WUU_LINUX_CUA_BUILD_ONLY === '1') return;
  if (process.platform !== 'linux' || !process.env.DISPLAY) throw new Error('Linux CUA acceptance requires a real X11 DISPLAY; headless simulation is not acceptance.');
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-linux-cua-e2e-'));
  try {
    const result = spawnSync(String(require('electron')), [...process.argv.slice(2), output], {
      stdio: 'inherit', env: { ...process.env, WUU_LINUX_CUA_PROFILE: profile },
    });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally {
    fs.rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
