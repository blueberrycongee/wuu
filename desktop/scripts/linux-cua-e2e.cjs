// Real X11 acceptance. Run after electron-vite build and build:cua-linux.
// CI supplies an isolated Xvfb display with a window manager and compositor.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync, execFileSync } = require('node:child_process');
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
    const started = Date.now();
    const result = spawnSync(String(require('electron')), [...process.argv.slice(2), output], {
      stdio: 'inherit', env: { ...process.env, WUU_LINUX_CUA_PROFILE: profile },
    });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
    if (process.exitCode === 0) {
      const evidence = path.resolve(process.env.WUU_LINUX_CUA_OUTPUT ?? 'out/e2e/linux-cua');
      const resultPath = path.join(evidence, 'results.json');
      const gifPath = path.join(evidence, 'linux-cua-pip.gif');
      const screenshots = ['01-linux-cua-live.png', '02-occluded-target-live.png', '03-dark-small-large-text.png', '04-target-hidden.png', '05-reopened-preview.png', 'target-observation.png'];
      for (const file of [resultPath, path.join(evidence, 'performance.json'), gifPath, ...screenshots.map(name => path.join(evidence, name))]) {
        const info = fs.statSync(file);
        if (!info.size || info.mtimeMs < started) throw new Error(`Acceptance did not finalize current evidence: ${file}`);
      }
      if (JSON.parse(fs.readFileSync(resultPath, 'utf8')).status !== 'passed') throw new Error('Acceptance results did not pass');
      const frames = Number(execFileSync('ffprobe', ['-v', 'error', '-select_streams', 'v:0', '-count_frames', '-show_entries', 'stream=nb_read_frames', '-of', 'default=noprint_wrappers=1:nokey=1', gifPath], { encoding: 'utf8', timeout: 15_000 }).trim());
      if (!Number.isFinite(frames) || frames < 2) throw new Error('Acceptance GIF does not contain multiple recorded frames');
      const fullApp = spawnSync(String(require('electron')), [...process.argv.slice(2), path.join(__dirname, 'linux-cua-e2e-app.mjs')], {
        stdio: 'inherit', env: { ...process.env, WUU_LINUX_CUA_PROFILE: profile }, timeout: 180_000,
      });
      if (fullApp.error) throw fullApp.error;
      if (fullApp.status !== 0) throw new Error(`Full application acceptance exited ${fullApp.status}`);
      const fullAppResult = JSON.parse(fs.readFileSync(path.join(evidence, 'full-app.json'), 'utf8'));
      if (fullAppResult.status !== 'passed') throw new Error(`Full application acceptance failed: ${fullAppResult.error}`);
      for (const name of ['full-app.json', 'full-app-live.png', 'full-app-desktop.png', 'full-app-after-vision.png']) {
        const info = fs.statSync(path.join(evidence, name));
        if (!info.size || info.mtimeMs < started) throw new Error(`Missing current full-app evidence: ${name}`);
      }
    }
  } finally {
    fs.rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
