const { spawnSync } = require('node:child_process');
const { resolve } = require('node:path');

function runWindowsBuild(eventName, { spawn = spawnSync } = {}) {
  const validation = eventName === 'pull_request';
  const args = [require.resolve('electron-builder/cli.js'), '--win', 'nsis', 'zip', '--publish=never'];
  // PR artifacts validate package layout and runtime behavior, not release compression.
  // Pushes (including tags), manual runs and unknown events retain builder defaults.
  if (validation) args.push('--config.compression=store');
  console.log(`Windows packaging mode: ${validation ? 'pull-request-validation (store)' : 'release-fidelity (default compression)'}`);
  const result = spawn(process.execPath, args, { cwd: resolve(__dirname, '..'), env: process.env, stdio: 'inherit' });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

module.exports = { runWindowsBuild };
if (require.main === module) process.exitCode = runWindowsBuild(process.env.GITHUB_EVENT_NAME);
