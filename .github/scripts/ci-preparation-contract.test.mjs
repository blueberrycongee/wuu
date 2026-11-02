import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

function stepScript(path, name) {
  const step = readFileSync(path, 'utf8').split(`      - name: ${name}\n`)[1]?.split(/\n      - /)[0];
  const body = step?.split('        run: |\n')[1];
  assert.ok(body, `Missing executable step: ${name}`);
  return body.split('\n').map(line => line.slice(10)).join('\n');
}

function fixture(run) {
  const root = mkdtempSync(join(tmpdir(), 'wuu-ci-preparation-'));
  const bin = join(root, 'bin');
  mkdirSync(bin);
  const command = (name, body) => writeFileSync(join(bin, name), `#!/bin/bash\nset -eu\n${body}\n`, { mode: 0o755 });
  // Check completion at the shell boundary, not after spawn waits for inherited pipes.
  const execute = (script, env = {}, completed = []) => spawnSync('bash', ['--noprofile', '--norc', '-eo', 'pipefail', '-c', `
set +e
(
set -e
${script}
)
result=$?
${completed.map(name => `test -e "$FIXTURE/${name}-done" || exit 93`).join('\n')}
exit "$result"
`], {
    cwd: root, encoding: 'utf8', timeout: 10000,
    env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, RECORD: join(root, 'calls'), FIXTURE: root, ...env },
  });
  try { run({ root, command, execute }); }
  finally { rmSync(root, { recursive: true, force: true }); }
}

const annotation = stepScript('.github/workflows/annotation-acceptance.yml', 'Install capture dependencies');
for (const failure of ['', 'npm-desktop', 'npm-core', 'electron', 'apt-update', 'apt-install']) {
  test(`Annotation preparation joins both installs: ${failure || 'success'}`, () => fixture(({ root, command, execute }) => {
    // Rendezvous makes a serial implementation fail, without timing assertions.
    assert.equal(spawnSync('mkfifo', [join(root, 'npm-ready'), join(root, 'apt-ready')]).status, 0);
    const mock = `
case "$(basename "$0") $*" in
  "npm ci --prefix desktop") key=npm-desktop; echo ready > "$FIXTURE/npm-ready"; read -r ready < "$FIXTURE/apt-ready" ;;
  "npm ci --prefix clients/core") key=npm-core ;;
  "node desktop/node_modules/electron/install.js") key=electron ;;
  "sudo apt-get update") key=apt-update; read -r ready < "$FIXTURE/npm-ready"; echo ready > "$FIXTURE/apt-ready" ;;
  "sudo apt-get install -y xvfb ffmpeg fonts-noto-cjk fonts-noto-color-emoji") key=apt-install ;;
  "sudo sed "*) exit 0 ;;
  *) echo "Unexpected mock command: $0 $*" >&2; exit 92 ;;
esac
printf '%s\\n' "$key" >> "$RECORD"
[[ "$FAIL" != "$key" ]] || exit 42
if [[ "$key" == electron || "$key" == apt-install ]]; then sleep 0.05; touch "$FIXTURE/$key-done"; fi`;
    for (const name of ['npm', 'node', 'sudo']) command(name, mock);
    const completed = [];
    if (!failure || failure.startsWith('npm-') || failure === 'electron') completed.push('apt-install');
    if (!failure || failure.startsWith('apt-')) completed.push('electron');
    const result = execute(annotation, { FAIL: failure }, completed);
    assert.ifError(result.error);
    assert.equal(result.status, failure ? 1 : 0, result.stdout + result.stderr);
    const calls = readFileSync(join(root, 'calls'), 'utf8').trim().split('\n');
    if (!failure || failure.startsWith('npm-') || failure === 'electron') {
      assert.ok(existsSync(join(root, 'apt-install-done')), 'apt must finish even if npm fails');
    }
    if (!failure || failure.startsWith('apt-')) {
      assert.ok(existsSync(join(root, 'electron-done')), 'npm must finish even if apt fails');
    }
    if (failure === 'npm-desktop') assert.ok(!calls.includes('npm-core'));
    if (['npm-desktop', 'npm-core'].includes(failure)) assert.ok(!calls.includes('electron'));
    if (failure === 'apt-update') assert.ok(!calls.includes('apt-install'));
  }));
}

const native = stepScript('.github/workflows/ci.yml', 'Validate app snapshot and native helpers');
for (const failure of ['', 'snapshot', 'native', 'snapshot,native', 'status', 'bridge']) {
  test(`Native helper preparation joins both validations: ${failure || 'success'}`, () => fixture(({ root, command, execute }) => {
    assert.equal(spawnSync('mkfifo', [join(root, 'snapshot-ready'), join(root, 'native-ready')]).status, 0);
    const mock = `
case "$(basename "$0") $*" in
  "npm --prefix desktop run build:app-context-mac")
    key=snapshot; echo ready > "$FIXTURE/snapshot-ready"; read -r ready < "$FIXTURE/native-ready" ;;
  "make test-native")
    key=native; read -r ready < "$FIXTURE/snapshot-ready"; echo ready > "$FIXTURE/native-ready" ;;
  "wuu-app-context --status") key=status ;;
  "Electron -e "*) key=bridge; [[ "$ELECTRON_RUN_AS_NODE" == 1 ]] ;;
  *) echo "Unexpected mock command: $0 $*" >&2; exit 92 ;;
esac
printf '%s\\n' "$key" >> "$RECORD"
case ",$FAIL," in *",$key,"*) exit 42 ;; esac
if [[ "$key" == bridge || "$key" == native ]]; then sleep 0.05; touch "$FIXTURE/$key-done"; fi`;
    for (const name of ['npm', 'make']) command(name, mock);
    for (const path of ['desktop/build/app-context/Wuu App Snapshot.app/Contents/MacOS/wuu-app-context',
      'desktop/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron']) {
      const executable = join(root, path);
      mkdirSync(join(executable, '..'), { recursive: true });
      writeFileSync(executable, `#!/bin/bash\nset -eu\n${mock}\n`, { mode: 0o755 });
    }
    const completed = [];
    if (!failure.includes('native')) completed.push('native');
    if (!failure || failure === 'native') completed.push('bridge');
    const result = execute(native, { FAIL: failure }, completed);
    assert.ifError(result.error);
    assert.equal(result.status, failure ? 1 : 0, result.stdout + result.stderr);
    const calls = readFileSync(join(root, 'calls'), 'utf8').trim().split('\n');
    assert.ok(calls.includes('snapshot') && calls.includes('native'), 'both lanes must run');
    if (!failure.includes('native')) assert.ok(existsSync(join(root, 'native-done')), 'native tests must finish');
    if (!failure || failure === 'native') assert.ok(existsSync(join(root, 'bridge-done')), 'snapshot validation must finish');
    if (failure.includes('snapshot')) assert.ok(!calls.includes('status') && !calls.includes('bridge'));
    if (failure === 'status') assert.ok(!calls.includes('bridge'));
  }));
}
