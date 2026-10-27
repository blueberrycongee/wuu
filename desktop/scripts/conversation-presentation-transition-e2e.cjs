// Synthetic own-window raster acceptance: no core, model, account, or user profile.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const desktop = path.resolve(__dirname, '..');
async function main() {
  const output = path.resolve(process.env.WUU_CONVERSATION_TRANSITION_OUTPUT || path.join(desktop, 'out/e2e/conversation-presentation-transition'));
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-conversation-transition-'));
  fs.mkdirSync(output, { recursive: true });
  const entry = path.join(desktop, 'out/main/conversation-presentation-transition-e2e.mjs');
  fs.mkdirSync(path.dirname(entry), { recursive: true });
  const esbuild = require('esbuild');
  await esbuild.build({ entryPoints: [path.join(__dirname, 'conversation-presentation-transition-renderer.tsx')], outfile: path.join(fixture, 'renderer.js'),
    bundle: true, platform: 'browser', format: 'iife', jsx: 'automatic', define: { 'process.env.NODE_ENV': '"production"', 'import.meta.env.DEV': 'false', 'import.meta.env.MODE': '"production"' } });
  await esbuild.build({ entryPoints: [path.join(__dirname, 'conversation-presentation-transition-preload.ts')], outfile: path.join(fixture, 'preload.cjs'), bundle: true, platform: 'node', format: 'cjs', external: ['electron'] });
  fs.writeFileSync(path.join(fixture, 'index.html'), '<!doctype html><html><body style="margin:0"><div id="root"></div><script src="renderer.js"></script></body></html>');
  await esbuild.build({ entryPoints: [path.join(__dirname, 'conversation-presentation-transition-main.ts')], outfile: entry,
    bundle: true, platform: 'node', format: 'esm', external: ['electron'] });
  if (process.env.WUU_CONVERSATION_TRANSITION_BUILD_ONLY === '1') { fs.rmSync(fixture, { recursive: true, force: true }); return; }
  try {
    const result = spawnSync(String(require('electron')), [
      ...(process.platform === 'linux' ? ['--no-sandbox', ...(!process.env.DISPLAY ? ['--ozone-platform=headless', '--ozone-override-screen-size=1440,1000'] : [])] : []), entry,
    ], { stdio: 'inherit', env: { ...process.env, WUU_CONVERSATION_TRANSITION_FIXTURE: fixture, WUU_CONVERSATION_TRANSITION_OUTPUT: output }, timeout: 60000 });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally { fs.rmSync(fixture, { recursive: true, force: true }); }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
