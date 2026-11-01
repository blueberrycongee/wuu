const { spawnSync } = require('node:child_process');
const { resolve } = require('node:path');

function runWindowsBuild(eventName, { spawn = spawnSync } = {}) {
  const validation = eventName === 'pull_request';
  const args = [require.resolve('electron-builder/cli.js'), '--win', 'nsis', 'zip', '--publish=never'];
  // PR artifacts validate package layout and runtime behavior, not release compression.
  // Pushes (including tags), manual runs and unknown events retain builder defaults.
  if (validation) args.push('--config.compression=store');
  // The builder's differential NSIS archive retains its format/dictionary settings,
  // but its supported level override also accelerates that inner archive.
  const env = validation ? { ...process.env, ELECTRON_BUILDER_COMPRESSION_LEVEL: '1' } : process.env;
  console.log(`Windows packaging mode: ${validation ? 'pull-request-validation (store config; archive level 1)' : 'release-fidelity (default config; inherited environment unchanged)'}`);
  const result = spawn(process.execPath, args, { cwd: resolve(__dirname, '..'), env, stdio: 'inherit' });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

module.exports = { runWindowsBuild };
if (require.main === module) process.exitCode = runWindowsBuild(process.env.GITHUB_EVENT_NAME);
