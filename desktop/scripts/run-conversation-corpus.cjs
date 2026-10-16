const { execFileSync, spawnSync } = require('node:child_process');
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
let signing;
if (process.platform === 'darwin') {
  const { DEFAULT_DEV_SIGNING_ID, matchingIdentity, parseCodeSigningIdentities } = require('./dev-signing.cjs');
  const requested = process.env.WUU_DEV_SIGN_ID?.trim() || DEFAULT_DEV_SIGNING_ID;
  if (requested === '-') {
    signing = { identity: '-', fingerprint: 'adhoc', name: 'ad-hoc' };
  } else {
    // Acceptance must never create a key, import a certificate or change trust.
    const identities = execFileSync('security', ['find-identity', '-v', '-p', 'codesigning'], { encoding: 'utf8' });
    const match = matchingIdentity(parseCodeSigningIdentities(identities), requested);
    if (!match) throw new Error('No existing macOS signing identity found. Set WUU_DEV_SIGN_ID to an existing identity, or WUU_DEV_SIGN_ID=- for ad-hoc acceptance.');
    signing = { identity: match.sha1, fingerprint: match.sha1, name: match.name };
  }
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
  electron = path.join(prepareDevElectronApp(signing), 'Contents/MacOS/Electron');
} else {
  electron = require('electron');
}
run(electron, [path.join(__dirname, 'ui-tour-e2e.cjs'), ...process.argv.slice(2)], desktop, {
  ...process.env, WUU_TOUR_CORPUS: '1', WUU_TOUR_CORE: core, WUU_E2E_HIDDEN: 'true',
  WUU_TOUR_OUTPUT: process.env.WUU_TOUR_OUTPUT || path.join(root, 'artifacts/conversation-corpus', new Date().toISOString().replace(/[:.]/g, '-')),
});
