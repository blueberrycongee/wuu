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
  const build = workflow.split('\n  desktop-build:\n')[1].split('\n  desktop-suite:\n')[0];
  const consumers = workflow.split('\n  desktop-suite:\n')[1].split('\n  desktop-macos-controls:\n')[0];
  assert.match(build, /git rev-parse HEAD > .*source-commit.txt/);
  assert.match(build, /go build .*wuu-session-switch-core/);
  assert.match(build, /electron-vite build/);
  assert.match(consumers, /needs: \[changes, desktop-build\]/);
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
