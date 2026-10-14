// Real Electron shell, browser tab, native-frame subprocess and renderer.
// Native frames are synthetic: this does not validate ScreenCaptureKit.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const desktop = path.resolve(__dirname, '..');
const output = path.join(desktop, 'out-e2e', 'activity-preview.mjs');
async function main() {
  fs.mkdirSync(path.dirname(output), { recursive: true });
  await require('esbuild').build({
    entryPoints: [path.join(__dirname, 'activity-preview-e2e-entry.ts')],
    outfile: output, bundle: true, platform: 'node', format: 'esm', target: 'node22', external: ['electron'],
    plugins: [{ name: 'svg', setup(build) {
      build.onResolve({ filter: /\.svg\?raw$/ }, args => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: 'svg' }));
      build.onLoad({ filter: /.*/, namespace: 'svg' }, args => ({ contents: fs.readFileSync(args.path, 'utf8'), loader: 'text' }));
    } }],
  });
  await require('esbuild').build({
    entryPoints: [path.join(desktop, 'src/main/nativePreviewPreload.ts')],
    outfile: path.join(path.dirname(output), 'nativePreview.cjs'), bundle: true,
    platform: 'node', format: 'cjs', external: ['electron'],
  });
  await require('esbuild').build({
    entryPoints: [path.join(desktop, 'src/nativePreview.ts')],
    outfile: path.join(path.dirname(output), 'nativePreview.js'), bundle: true, platform: 'browser', format: 'esm',
  });
  fs.writeFileSync(path.join(path.dirname(output), 'native-preview.html'),
    fs.readFileSync(path.join(desktop, 'native-preview.html'), 'utf8').replace('/src/nativePreview.ts', './nativePreview.js'));
  if (process.env.WUU_ACTIVITY_PREVIEW_BUILD_ONLY === '1') return;
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-activity-preview-'));
  try {
    const result = spawnSync(String(require('electron')), [
      ...(process.platform === 'linux' ? ['--no-sandbox', ...(!process.env.DISPLAY ? ['--disable-gpu', '--ozone-platform=headless', '--ozone-override-screen-size=1440,1000'] : [])] : []), output,
    ], { stdio: 'inherit', env: { ...process.env, WUU_ACTIVITY_PREVIEW_FIXTURE: fixture, XDG_CACHE_HOME: path.join(fixture, 'cache') } });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally {
    fs.rmSync(fixture, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch(error => { console.error(error); process.exitCode ||= 1; });
