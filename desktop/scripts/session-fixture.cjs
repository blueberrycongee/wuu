const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
// Capture build caches before the E2E replaces HOME with its disposable profile.
const buildEnvironment = { ...process.env };

// Call only with the disposable home and project created by the E2E fixture.
module.exports = function createSessionFixture(home, workdir, workspaceID) {
  home = fs.realpathSync(home);
  workdir = fs.realpathSync(workdir);
  fs.writeFileSync(path.join(home, '.wuu-session-fixture'), 'wuu-session-fixture-v1\n');
  return request => {
    const binary = process.env.WUU_SESSION_FIXTURE_BINARY;
    return spawnSync(binary || 'go', binary ? [] : ['run', './scripts/session-fixture'], {
      cwd: path.resolve(__dirname, '../..'), env: binary ? process.env : buildEnvironment,
      input: JSON.stringify({ ...request, home, workdir, workspace_id: workspaceID }), encoding: 'utf8',
      timeout: 120000, maxBuffer: 16 * 1024 * 1024,
    });
  };
};
