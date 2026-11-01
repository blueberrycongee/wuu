const { execFileSync } = require('node:child_process');

function runPhase(name, action, { log = console.log, now = () => new Date() } = {}) {
  const report = state => log(`${now().toISOString()} Linux packaging phase ${name}: ${state}`);
  report('started');
  try {
    const result = action();
    report('completed');
    return result;
  } catch (error) {
    report('failed');
    throw error;
  }
}

function runLinuxBuild(eventName, { exec = execFileSync, env = process.env, log = console.log } = {}) {
  const validation = eventName === 'pull_request';
  // FPM supplies XZ_OPT=-3. Bound its independent thread setting only for PR
  // validation; retain xz format/preset and all inherited release settings otherwise.
  const childEnv = validation ? { ...env, XZ_DEFAULTS: '-T4' } : env;
  log(`Linux packaging mode: ${validation ? 'pull-request-validation (xz preset unchanged; at most 4 threads)' : 'release-fidelity (inherited environment unchanged)'}`);
  return exec('node_modules/.bin/electron-builder', [
    '--linux', 'AppImage', 'deb', '--x64',
    '--publish=never', '--config.electronDist=node_modules/electron/dist',
  ], { stdio: 'inherit', env: childEnv });
}

module.exports = { runLinuxBuild, runPhase };
