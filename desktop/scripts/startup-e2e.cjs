// Launch a fresh Electron process after separately preparing synthetic fixture data.
// Usage: node scripts/startup-e2e.cjs <prepared-fixture> <output-directory>
// WUU_SWITCH_MAIN, WUU_DESKTOP_CORE and their BUILD_COMMIT variables select artifacts.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { createHash } = require('node:crypto');
const [fixtureArg, outputArg] = process.argv.slice(2);
assert.ok(fixtureArg && outputArg, 'Pass a fresh prepared synthetic fixture and output directory');
const fixture = path.resolve(fixtureArg);
const output = path.resolve(outputArg);
const marker = JSON.parse(fs.readFileSync(path.join(fixture, 'startup-fixture.json'), 'utf8'));
assert.equal(marker.synthetic, true, 'Only generated synthetic fixtures are accepted');
assert.equal(fs.existsSync(path.join(fixture, 'profile')), false, 'Use a fresh prepared fixture for each launch');
assert.ok(process.env.WUU_SWITCH_BUILD_COMMIT && process.env.WUU_SWITCH_CORE_BUILD_COMMIT, 'Record exact UI and core build source commits');
fs.mkdirSync(output, { recursive: true });
const executable = require('electron');
const harness = path.join(__dirname, 'session-switch-e2e.cjs');
const args = process.platform === 'linux' ? ['--no-sandbox', '--ozone-platform=headless', '--ozone-override-screen-size=1440,1000', harness] : [harness];
const env = { ...process.env, WUU_STARTUP_ONLY: '1', WUU_STARTUP_PREPARED: fixture,
  WUU_SWITCH_OUTPUT: output, WUU_SWITCH_TURNS: String(marker.turns),
  WUU_SWITCH_SIDEBAR_THREADS: String(marker.sidebarThreads), WUU_STARTUP_SELECTED: String(marker.selectedIndex) };
delete env.WUU_STARTUP_PREPARE_ONLY;
const launchNs = process.hrtime.bigint();
env.WUU_STARTUP_LAUNCH_NS = String(launchNs);
const child = spawn(executable, args, { env, stdio: ['ignore', 'pipe', 'pipe'] });
const elapsed = () => Number(process.hrtime.bigint() - launchNs) / 1e6;
const milestones = [{ stage: 'spawn-called', ms: 0 }];
child.once('spawn', () => milestones.push({ stage: 'process-spawned', ms: elapsed(), pid: child.pid }));
const log = fs.createWriteStream(path.join(output, 'run.log'));
child.stdout.on('data', chunk => log.write(chunk));
child.stderr.on('data', chunk => log.write(chunk));
child.once('error', error => { fs.writeFileSync(path.join(output, 'launcher-error.txt'), String(error.stack)); process.exitCode = 1; });
child.once('exit', (code, signal) => {
  milestones.push({ stage: 'process-exited', ms: elapsed(), code, signal });
  fs.writeFileSync(path.join(output, 'launcher.json'), JSON.stringify({ executable, args, fixture, driverSha256: createHash('sha256').update(fs.readFileSync(__filename)).digest('hex'), launchNs: String(launchNs), milestones }, null, 2));
  log.end();
  process.exitCode = code || (signal ? 1 : 0);
});
