import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
const suite = process.env.WUU_CONFORMANCE_SUITE;
assert(['controller', 'authority', 'cooperation'].includes(suite), 'Unknown conformance suite');
const run = base => {
  const runs = readdirSync(base).filter(name => name.startsWith('run-'));
  assert.equal(runs.length, 1, `${base}: expected exactly one fresh fixture`);
  return join(base, runs[0]);
};
for (const [owner, base, file, name] of [
  ['authority', '.tmp/e2e/provider-authority', 'verification.log', 'TestProviderAuthorityHTTPProcess'],
  ['authority', '.tmp/e2e/session-sharing', 'verification.log', 'TestSessionSharingExternalProcess'],
  ['authority', '.tmp/artifact-provider-acceptance', 'verification.log', 'TestThreadArtifactSelectedProvider'],
  ['cooperation', '.tmp/e2e/trusted-desktop', 'go-test.log', 'TestEncryptedDelegatedDesktopSharedCore'],
  ['authority', '.tmp/e2e/provider-delegation', 'verification.log', 'TestProviderDelegationSDKProcess'],
  ['authority', '.tmp/e2e/provider-delegation', 'verification.log', 'TestStateProviderDelegationAuthority'],
  ['authority', '.tmp/e2e/service-continuation', 'verification.log', 'TestServiceContinuationSDKReplacementAndRemoval'],
]) {
  if (owner !== suite) continue;
  const log = readFileSync(join(run(base), file), 'utf8');
  assert.match(log, new RegExp(`^--- PASS: ${name} [(]`, 'm'), `${name}: did not execute`);
  assert.doesNotMatch(log, /^\s*--- SKIP:/m, `${name}: skipped coverage is not acceptance`);
}
for (const [owner, base, file, key, expected] of [
  ['authority', '.tmp/artifact-provider-acceptance', 'report.json', 'status', 'passed'],
  ['controller', '.tmp/e2e/minimal-composition', 'result.json', 'passed', true],
  ['controller', '.tmp/e2e/resource-access', 'result.json', 'passed', true],
  ['controller', '.tmp/e2e/session-progress', 'report.json', 'passed', true],
  ['cooperation', '.tmp/e2e/session-federation', 'results.json', 'status', 'passed'],
  ['cooperation', '.tmp/e2e/trusted-desktop', 'result.json', 'passed', true],
  ['authority', '.tmp/e2e/provider-delegation', 'report.json', 'passed', true],
  ['authority', '.tmp/e2e/service-continuation', 'report.json', 'passed', true],
  ['cooperation', '.tmp/e2e/composition', 'results.json', 'status', 'passed'],
]) {
  if (owner !== suite) continue;
  const report = JSON.parse(readFileSync(join(run(base), file), 'utf8'));
  assert.equal(report[key], expected, `${base}: fixture did not pass`);
}
if (suite === 'cooperation') {
  const additive = JSON.parse(readFileSync('.tmp/e2e/additive-cooperation/result.json', 'utf8'));
  assert.equal(additive.passed, true, 'additive cooperation did not pass');
  assert(Array.isArray(additive.cases) && additive.cases.length >= 11, 'additive boundary cases did not execute');
  assert.equal(new Set(additive.cases).size, additive.cases.length, 'additive cases must be distinct');
  // The runner emits its final marker only after the real renderer batch
  // and report checks; renderer.log itself contains the Vitest output.
  assert.match(readFileSync('.tmp/e2e/architecture-conformance/additive-cooperation.log', 'utf8'), /^PASS: independent additive packages cooperate through declared services and owned UI slots\.$/m);
  const renderer = readFileSync('.tmp/e2e/additive-cooperation/renderer.log', 'utf8').replace(/\x1b\[[0-9;]*m/g, '');
  assert.match(renderer, /Tests\s+\d+ passed\b/);
  assert.doesNotMatch(renderer, /\b[1-9]\d* skipped\b/);
  const inputs = JSON.parse(readFileSync('.tmp/e2e/additive-cooperation/inputs.json', 'utf8'));
  const binaryHash = readFileSync('.tmp/e2e/architecture-conformance/binary.sha256', 'utf8').trim().split(/\s+/)[0];
  assert.equal(inputs.binarySHA256, binaryHash, 'additive fixture used a different executable');
  const isolation = JSON.parse(readFileSync(join(run('.tmp/e2e/plugin-failure-isolation'), 'result.json'), 'utf8'));
  assert.equal(isolation.status, 'passed', 'plugin failure isolation did not pass');
  assert.equal(isolation.expectBlocked, false, 'negative control is not acceptance');
  assert.equal(isolation.binary_sha256, binaryHash, 'isolation fixture used a different executable');
  assert(Array.isArray(isolation.checks) && isolation.checks.length >= 15, 'isolation boundaries did not execute');
  assert.equal(new Set(isolation.checks).size, isolation.checks.length, 'isolation cases must be distinct');
  assert(isolation.checks.includes('new send completes while real additive plugin prepare is held; activation follows explicit barrier release'), 'held additive preparation admission was not verified');
}
