import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

const checker = resolve('.github/scripts/check-mcp-auth-evidence.mjs');
function verify(change = () => {}, missing = false, wrongSource = false) {
  const output = mkdtempSync(join(tmpdir(), 'wuu-auth-evidence-'));
  const binary = Buffer.from('synthetic fixture executable');
  const source = 'a'.repeat(40);
  const report = {
    status: 'passed', executable_sha256: createHash('sha256').update(binary).digest('hex'),
    checks: Array.from({ length: 11 }, (_, index) => `executed boundary ${index}`),
    wire: [{ method: 'initialize', authenticated: true }, { method: 'tools/call', authenticated: true }],
  };
  change(report);
  try {
    writeFileSync(join(output, 'fixture'), binary);
    writeFileSync(join(output, 'source-commit.txt'), `${source}\n`);
    if (!missing) writeFileSync(join(output, 'results.json'), JSON.stringify(report));
    return spawnSync(process.execPath, [checker, output, join(output, 'fixture'), wrongSource ? 'b'.repeat(40) : source], { encoding: 'utf8' }).status === 0;
  } finally { rmSync(output, { recursive: true, force: true }); }
}

test('MCP reload evidence accepts a complete passing run of the exact executable', () => {
  assert.equal(verify(), true);
});

test('MCP reload evidence rejects failure, missing execution and mismatched executable', () => {
  for (const change of [
    report => { report.status = 'failed'; },
    report => { report.executable_sha256 = 'b'.repeat(64); },
    report => { report.checks = []; },
    report => { report.checks = Array(11).fill('duplicate'); },
    report => { report.wire = []; },
    report => { report.wire[0].authenticated = false; },
    report => { report.wire = [{ method: 'initialize', authenticated: true }]; },
  ]) assert.equal(verify(change), false);
  assert.equal(verify(undefined, true), false);
  assert.equal(verify(undefined, false, true), false);
});
