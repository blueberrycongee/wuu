import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const workflow = readFileSync('.github/workflows/ci.yml', 'utf8');
const section = workflow.split('\n  go-check:\n')[1]?.split('\n  desktop-suite:\n')[0];
assert.ok(section, 'Go check must remain a distinct merge gate');
const script = section.match(/^        run: \|\n((?: {10}.*\n)+)/m)?.[1]
  .split('\n').map(line => line.slice(10)).join('\n');
assert.ok(script, 'Go check must have an executable gate');

const selected = {
  SELECTION_RESULT: 'success',
  GO_REQUIRED: 'true',
  GO_VALIDATION_RESULT: 'success',
  GO_RACE_RESULT: 'success',
  PROJECT_AGENT_RESULT: 'success',
  ARCHITECTURE_REQUIRED: 'true',
  ARCHITECTURE_RESULT: 'success',
};
function passes(overrides = {}) {
  return spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
    env: { ...process.env, ...selected, ...overrides },
  }).status === 0;
}

test('the Go gate waits for the independent race job', () => {
  assert.match(section, /needs: \[[^\]]*\bgo-race\b[^\]]*\]/);
  assert.match(section, /GO_RACE_RESULT: \$\{\{ needs\.go-race\.result \}\}/);
});

test('selected Go and architecture checks must all succeed', () => {
  assert.equal(passes(), true);
  for (const result of ['failure', 'cancelled', 'skipped', '']) {
    for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'PROJECT_AGENT_RESULT', 'ARCHITECTURE_RESULT']) {
      assert.equal(passes({ [name]: result }), false, `${name}=${result}`);
    }
  }
  assert.equal(passes({ SELECTION_RESULT: 'failure' }), false);
});

test('unselected checks must be skipped, not failed or run unexpectedly', () => {
  const unselected = {
    GO_REQUIRED: 'false', GO_VALIDATION_RESULT: 'skipped', GO_RACE_RESULT: 'skipped',
    PROJECT_AGENT_RESULT: 'skipped', ARCHITECTURE_REQUIRED: 'false', ARCHITECTURE_RESULT: 'skipped',
  };
  assert.equal(passes(unselected), true);
  for (const name of ['GO_VALIDATION_RESULT', 'GO_RACE_RESULT', 'PROJECT_AGENT_RESULT', 'ARCHITECTURE_RESULT']) {
    assert.equal(passes({ ...unselected, [name]: 'failure' }), false, name);
    assert.equal(passes({ ...unselected, [name]: 'success' }), false, name);
  }
});

// A successful `go test` process may contain only skipped gated fixtures. The
// required bridge gate must prove both real-bundle cases actually executed.
test('the Personal desktop bridge merge gate rejects missing or skipped real-bundle evidence', async () => {
  const conformance = workflow.split('\n  architecture-conformance:\n')[1]?.split('\n  project-agent-check:\n')[0];
  const bridge = conformance?.split('      - name: Guard Personal desktop handoff with the real bundled runtime\n')[1]?.split('      - name: Verify independent additive packages')[0];
  assert.ok(bridge, 'real Personal handoff must run inside required public conformance, including desktop-only boundary edits');
  assert.match(bridge, /WUU_SESSION_HANDOFF_E2E: '1'/);
  assert.match(bridge, /go test -race -p 1 -count=1 -timeout 5m -json/);
  assert.match(bridge, /internal\/version\.Version=\$\(make -s print-version\)/);
  assert.match(bridge, /git rev-parse HEAD/);
  const reporter = bridge.match(/node --input-type=module - .* <<'NODE'\n([\s\S]*?)\n          NODE/)?.[1]
    .split('\n').map(line => line.slice(10)).join('\n');
  assert.ok(reporter, 'bridge gate must reject skipped or absent cases, not just exit zero');
  const {mkdtempSync, writeFileSync, rmSync} = await import('node:fs');
  const {tmpdir} = await import('node:os');
  const {join} = await import('node:path');
  const temporary = mkdtempSync(join(tmpdir(), 'personal-bridge-gate-'));
  try {
    const file = join(temporary, 'go-test.jsonl');
    const Package = 'github.com/blueberrycongee/wuu/internal/appserver';
    const root = 'TestPersonalRealBundleSessionHandoffAcrossTwoHosts';
    const evidence = ['caption-and-file', 'attachment-only'].map(name => ({Package, Test:`${root}/${name}`, Action:'pass'}));
    evidence.push({Package, Test:root, Action:'pass'}, {Package, Action:'pass'});
    const accepts = events => {
      writeFileSync(file, events.map(event => JSON.stringify(event)).join('\n')+'\n');
      return spawnSync(process.execPath, ['--input-type=module', '-', file], {input: reporter, encoding:'utf8'}).status === 0;
    };
    assert.equal(accepts(evidence), true);
    assert.equal(accepts(evidence.filter(event => event.Test !== `${root}/attachment-only`)), false);
    assert.equal(accepts(evidence.map(event => event.Test === `${root}/attachment-only` ? {...event, Action:'skip'} : event)), false);
    assert.equal(accepts(evidence.map(event => event.Test === `${root}/caption-and-file` ? {...event, Action:'fail'} : event)), false);
    assert.equal(accepts(evidence.filter(event => event.Test)), false);
  } finally { rmSync(temporary, {recursive:true, force:true}); }
});
