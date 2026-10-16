const { spawnSync } = require('node:child_process');
const { mkdirSync } = require('node:fs');
const path = require('node:path');
const desktop = path.resolve(__dirname, '..');
const root = path.resolve(desktop, '..');
const core = process.env.WUU_TOUR_CORE || path.join(desktop, 'build/bin', process.platform === 'win32' ? 'wuu-corpus-core.exe' : 'wuu-corpus-core');
function run(command, args, cwd = root, env = process.env) {
  const result = spawnSync(command, args, { cwd, env, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
if (process.env.WUU_CORPUS_SKIP_BUILD !== '1') {
  mkdirSync(path.dirname(core), { recursive: true });
  run('go', ['build', '-o', core, './cmd/wuu']);
  if (process.platform === 'darwin') run(process.execPath, [path.join(__dirname, 'build-cua-mac.cjs')], desktop);
  run(process.execPath, [path.join(desktop, 'node_modules/electron-vite/bin/electron-vite.js'), 'build'], desktop);
}
let electron;
if (process.platform === 'darwin') {
  const { prepareDevElectronApp } = require('./prepare-dev-electron-app.cjs');
  const { ensureDevSigningIdentity } = require('./dev-signing.cjs');
  electron = path.join(prepareDevElectronApp(ensureDevSigningIdentity()), 'Contents/MacOS/Electron');
} else {
  electron = require('electron');
}
run(electron, [path.join(__dirname, 'ui-tour-e2e.cjs'), ...process.argv.slice(2)], desktop, {
  ...process.env, WUU_TOUR_CORPUS: '1', WUU_TOUR_CORE: core, WUU_E2E_HIDDEN: 'true',
  WUU_TOUR_OUTPUT: process.env.WUU_TOUR_OUTPUT || path.join(root, 'artifacts/conversation-corpus', new Date().toISOString().replace(/[:.]/g, '-')),
});
