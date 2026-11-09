import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const workflow = readFileSync('.github/workflows/ci.yml', 'utf8');
const race = workflow.split('\n  go-race:\n')[1].split('\n  architecture-conformance:\n')[0];

test('race lanes retain the complete admission, runtime and supporting package guards', () => {
  assert.match(race, /suite: admission/);
  assert.equal((race.match(/suite: runtime/g) || []).length, 2);
  assert.match(race, /matrix\.suite == 'admission'/);
  assert.match(race, /matrix\.suite == 'runtime'/);
  assert.match(race, /go-test-shard\.mjs \.\/internal\/runtime/);
  for (const pkg of ['session', 'providers/...', 'modelrequest', 'pluginhost', 'agent', 'agentcontrol', 'subagent']) {
    assert(race.includes(`./internal/${pkg}`), pkg);
  }
  assert.match(race, /WUU_GO_TEST_RACE: 'true'/);
  assert.match(race, /WUU_GO_TEST_PATTERN:/);
});

test('shared Desktop outputs are same-run artifacts and verified against the checkout', () => {
  const build = workflow.split('\n  repository-check:\n')[1].split('\n  go-validation:\n')[0];
  const consumers = workflow.split('\n  desktop-suite:\n')[1].split('\n  desktop-macos-controls:\n')[0];
  // This producer must start without the selector's extra queue hop, while
  // retaining identical immutable comparison refs and fail-closed consumers.
  assert.doesNotMatch(build.split('    steps:')[0], /needs:/);
  assert(build.indexOf('make repository-check') < build.indexOf("- name: Build this run's renderer and core once"));
  const selection = workflow.split('      - name: Select jobs from the complete change\n')[1].split('\n  repository-check:')[0];
  const repeated = build.split('      - name: Select shared Desktop build from the complete change\n')[1].split('      - uses: actions/setup-node@v4')[0];
  assert.equal(repeated.replace('id: select-build', 'id: select').trim(), selection.trim());
  const required = `steps.select-build.outputs.desktop == 'true' && steps.select-build.outputs.desktop_suites != '["unit","unit-extra"]'`;
  const heavySteps = build.split('id: select-build')[1].split(/\n      - /).slice(1);
  assert.equal(heavySteps.length, 5);
  for (const step of heavySteps.filter(step => !step.startsWith('name: Check repository and prepare selected Desktop build'))) assert(step.includes(`if: ${required}`), step);
  assert.match(build, /BUILD_REQUIRED: \$\{\{ steps\.select-build\.outputs\.desktop/);
  assert.match(consumers, /needs\.repository-check\.result == 'success'/);
  assert.doesNotMatch(consumers, /needs\.repository-check\.result == 'skipped'/);
  assert.match(build, /git rev-parse HEAD > .*source-commit.txt/);
  assert.match(build, /go build .*wuu-session-switch-core/);
  assert.match(build, /electron-vite build/);
  assert.match(build, /go build -o .*wuu-session-fixture \.\/scripts\/session-fixture/);
  assert.match(build, /sha256sum .*wuu-session-fixture/);
  assert.match(consumers, /WUU_SESSION_FIXTURE_BINARY:/);
  const lifecycle = consumers.split('      - name: Exercise bounded session-lifecycle journeys\n')[1].split('        run: |')[0];
  for (const name of ['WUU_SESSION_FIXTURE', 'WUU_SESSION_FIXTURE_BINARY']) {
    assert(lifecycle.includes(name + ': ${{ github.workspace }}/.tmp/desktop-ci-build/wuu-session-fixture'), name);
  }
  assert.match(consumers, /needs: \[changes, repository-check\]/);
  assert.match(consumers, /actions\/download-artifact@v4/);
  assert.match(consumers, /source-commit.txt\)" = "\$\(git rev-parse HEAD\)"/);
  assert.match(consumers, /sha256sum --check .*bundle.sha256/);
  assert.doesNotMatch(consumers, /go build .*wuu-session-switch-core/);
});

test('parallel client work joins every required lane before SSH acceptance', () => {
  const clients = workflow.split('\n  clients-check:\n')[1].split('\n  macos-check:\n')[0];
  assert.match(clients, /make check-clients check-plugins/);
  assert.match(clients, /make test-clients &/);
  assert.match(clients, /make build-clients/);
  assert.match(clients, /cp -R clients\/mobile-web\/dist\/\. desktop\/build\/mobile-web\//);
  assert.match(clients, /npm --prefix desktop run build:core/);
  assert.match(clients, /vite\.js build --config remote\.vite\.config\.ts/);
  assert.match(clients, /for pid in "\$test_pid" "\$build_pid" "\$deps_pid"; do wait "\$pid" \|\| status=1; done/);
  assert.match(clients, /exit "\$status"/);
  assert.match(clients, /npm --prefix clients\/mobile-web run test:headless/);
});

test('overlapped macOS acceptance joins every native check and fails on either lane or changed inputs', async () => {
  const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { spawnSync } = await import('node:child_process');
  const step = workflow.split('      - name: Exercise native controls and headless Personal approval\n')[1].split('\n      - name:')[0];
  const script = step.split('        run: |\n')[1].split('\n').map(line => line.slice(10)).join('\n');
  const root = mkdtempSync(join(tmpdir(), 'wuu-ci-acceptance-'));
  try {
    const bin = join(root, 'bin');
    const evidence = join(root, 'desktop/out/personal-desktop-acceptance');
    mkdirSync(bin); mkdirSync(evidence, { recursive: true });
    mkdirSync(join(root, 'desktop/node_modules/.bin'), { recursive: true });
    const command = (path, body) => writeFileSync(path, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
    command(join(bin, 'git'), 'echo fixture-commit');
    command(join(bin, 'node'), 'echo approval >> "$RECORD"; test "$FAIL_APPROVAL" != 1');
    command(join(root, 'desktop/node_modules/.bin/electron'), 'echo "native:$1" >> "$RECORD"; if [ "$FAIL_HEADER" = 1 ] && [ "$1" = desktop/scripts/settings-sidebar-hit-e2e.cjs ]; then exit 1; fi; if [ "$FAIL_MESSAGE_FLOW" = 1 ] && [ "$1" = desktop/scripts/personal-message-flow-e2e.cjs ]; then exit 1; fi');
    command(join(bin, 'shasum'), 'test "$(grep -c "^native:" "$RECORD")" -eq 5 || exit 7; echo hash >> "$RECORD"; test "$FAIL_HASH" != 1');
    for (const failure of [{}, { FAIL_HEADER: '1' }, { FAIL_MESSAGE_FLOW: '1' }, { FAIL_APPROVAL: '1' }, { FAIL_HEADER: '1', FAIL_APPROVAL: '1' }, { FAIL_HASH: '1' }]) {
      const record = join(root, 'record'); writeFileSync(record, '');
      const result = spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
        cwd: root, encoding: 'utf8', env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, RECORD: record,
          WUU_TITLEBAR_OUTPUT: join(root, 'header'), WUU_ERROR_FEEDBACK_OUTPUT: join(root, 'error'),
          WUU_SETTINGS_OVERLAY_OUTPUT: join(root, 'overlay'), FAIL_HEADER: '0', FAIL_MESSAGE_FLOW: '0', FAIL_APPROVAL: '0', FAIL_HASH: '0', ...failure },
      });
      assert.equal(result.status, Object.keys(failure).length ? 1 : 0, result.stdout + result.stderr);
      const events = readFileSync(record, 'utf8').trim().split('\n');
      assert.equal(events.filter(event => event.startsWith('native:')).length, 5);
      assert(events.includes('approval'));
      assert.equal(events.at(-1), 'hash');
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('Desktop workers preserve per-journey failure, timeout, and join behavior', async () => {
  const { mkdtempSync, writeFileSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const { spawnSync } = await import('node:child_process');
  const root = mkdtempSync(join(tmpdir(), 'wuu-ci-journeys-'));
  try {
    for (const name of ['Exercise bounded session journeys', 'Exercise bounded session-lifecycle journeys', 'Exercise bounded shell journeys']) {
      const step = workflow.split(`      - name: ${name}\n`)[1].split('\n      - name:')[0];
      const original = step.split('        run: |\n')[1].split('\n').map(line => line.slice(10)).join('\n');
      const shell = name === 'Exercise bounded shell journeys';
      for (const mode of ['success', 'failure', 'timeout', ...(shell ? ['hash'] : [])]) {
        let count = 0;
        let script = original.replace(/(<<'(WUU_JOURNEY_\d_\d)'\n)[\s\S]*?(\n\2\n)/g, (_, begin, label, end) => {
          const index = ++count;
          const fail = index === 1 && ['failure', 'timeout'].includes(mode);
          return `${begin}printf '${index}\\n' >> "$MARKERS"\n${fail ? (mode === 'timeout' ? 'sleep 30\n' : 'false\n') + 'echo unreachable >> "$MARKERS"\n' : ''}${end}`;
        }).replace(/\$\{\{.*?\}\}/g, 'fixture');
        assert(count > 1, 'the real workflow must contain journey blocks');
        if (shell) script = script.replace('sha256sum --quiet --check "fixture/wuu-initial-renderer.sha256"',
          `( test "$(wc -l < "$MARKERS")" -eq ${count} && echo hash >> "$MARKERS" && ${mode === 'hash' ? 'false' : 'true'} )`);
        if (mode === 'timeout') script = script.replace(/(run_journey '[^']+' )\S+/, (_, prefix) => `${prefix}0.05s`);
        const markers = join(root, 'markers'); writeFileSync(markers, '');
        const result = spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
          cwd: root, encoding: 'utf8', timeout: 5000, env: { ...process.env, MARKERS: markers },
        });
        assert.equal(result.status, mode === 'success' ? 0 : 1, result.stdout + result.stderr);
        const events = readFileSync(markers, 'utf8').trim().split('\n');
        assert.equal(events.length, count + (shell ? 1 : 0));
        if (shell) assert.equal(events.at(-1), 'hash');
        assert.equal((result.stdout.match(/: exit /g) || []).length, count);
        if (mode === 'timeout') assert.match(result.stdout, /: exit 124/);
      }
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
