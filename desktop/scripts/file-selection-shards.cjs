// CI only: each Electron process owns its Xvfb display, profile and evidence.
// The ordinary file-selection-e2e.cjs entry point still runs all four variants.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { variants, caseKinds, selectVariants } = require('./file-selection-variants.cjs');

function launch(child) {
  const log = fs.openSync(child.log, 'w');
  let childProcess;
  try {
    childProcess = spawn('xvfb-run', ['--auto-servernum', path.resolve(__dirname, '../node_modules/.bin/electron'),
      '--no-sandbox', path.join(__dirname, 'file-selection-e2e.cjs')], {
      env: { ...process.env, WUU_E2E_OUTPUT: child.output, WUU_FILE_SELECTION_SHARD: child.shard },
      stdio: ['ignore', log, log],
    });
  } finally { fs.closeSync(log); }
  return new Promise(resolve => {
    childProcess.once('error', error => resolve({ code: null, error: error.message }));
    childProcess.once('close', (code, signal) => resolve({ code, signal }));
  });
}

async function runShards({ output, launch: start = launch }) {
  output = path.resolve(output);
  fs.mkdirSync(output, { recursive: true });
  const privateRoot = path.join(output, '.shards');
  fs.mkdirSync(privateRoot, { recursive: true });
  // Fresh child directories prevent stale reports from satisfying a new run.
  const children = [1, 2].map(index => ({ shard: `${index}/2`,
    output: fs.mkdtempSync(path.join(privateRoot, `${index}-`)), log: path.join(output, `shard-${index}.log`) }));
  const outcomes = await Promise.allSettled(children.map(child => {
    try { return start(child); } catch (error) { return Promise.reject(error); }
  }));
  const merged = { boundary: '', renderer: '', output, cases: [], screenshots: [], errors: [], layoutObservations: [], completed: false };
  const identities = new Set();
  const screenshots = new Set();
  for (const [index, child] of children.entries()) {
    const outcome = outcomes[index];
    if (outcome.status !== 'fulfilled' || outcome.value?.code !== 0) {
      merged.errors.push({ phase: child.shard, message: `Child failed: ${outcome.status === 'rejected' ? String(outcome.reason) : JSON.stringify(outcome.value)}` });
    }
    try {
      const raw = fs.readFileSync(path.join(child.output, 'report.json'), 'utf8');
      fs.writeFileSync(path.join(output, `shard-${index + 1}.json`), raw);
      const report = JSON.parse(raw);
      // Preserve failure images too: child profiles stay private and are not uploaded.
      assert.ok(Array.isArray(report.screenshots));
      for (const source of report.screenshots) {
        // Only flat declared PNGs are artifacts; never copy a child profile.
        assert.equal(path.dirname(source), child.output, 'Screenshot is outside child artifact root');
        assert.equal(path.extname(source), '.png');
        const name = path.basename(source) === 'failure.png' ? `shard-${index + 1}-failure.png` : path.basename(source);
        assert.ok(!screenshots.has(name), `Duplicate screenshot: ${name}`);
        screenshots.add(name);
        const target = path.join(output, name);
        fs.copyFileSync(source, target);
        merged.screenshots.push(target);
      }
      assert.equal(report.completed, true, 'Child report is incomplete');
      assert.deepEqual(report.errors, [], 'Child reported runtime errors');
      assert.equal(report.output, child.output, 'Child report belongs to another run');
      assert.equal(typeof report.boundary, 'string');
      assert.equal(typeof report.renderer, 'string');
      assert.ok(report.renderer.length > 0);
      if (!merged.renderer) { merged.boundary = report.boundary; merged.renderer = report.renderer; }
      else { assert.equal(report.boundary, merged.boundary); assert.equal(report.renderer, merged.renderer); }
      const selected = selectVariants(child.shard);
      const expected = new Set(selected.flatMap(variant => caseKinds.map(kind => `${variant.name}:${kind}`)));
      for (const item of report.cases) {
        assert.deepEqual(item.variant, selected.find(variant => variant.name === item.variant?.name), 'Foreign variant');
        assert.equal(Boolean(item.file) !== Boolean(item.kind), true, 'Case must have one identity');
        const key = `${item.variant.name}:${item.file || item.kind}`;
        assert.ok(expected.delete(key), `Unexpected or duplicate case: ${key}`);
        assert.ok(!identities.has(key), `Overlapping child case: ${key}`);
        identities.add(key);
        merged.cases.push(item);
      }
      assert.equal(expected.size, 0, `Missing cases: ${[...expected].join(', ')}`);
      assert.ok(Array.isArray(report.layoutObservations));
      for (const observation of report.layoutObservations) {
        assert.equal(typeof (observation.label ?? observation.name), 'string', 'Missing layout observation identity');
        merged.layoutObservations.push(observation);
      }
    } catch (error) { merged.errors.push({ phase: child.shard, message: error.stack || String(error) }); }
  }
  if (identities.size !== variants.length * caseKinds.length) merged.errors.push({ phase: 'join', message: 'Incomplete 24-case coverage' });
  const rank = name => variants.findIndex(variant => name.startsWith(variant.name));
  merged.cases.sort((a, b) => rank(a.variant.name) - rank(b.variant.name) || caseKinds.indexOf(a.file || a.kind) - caseKinds.indexOf(b.file || b.kind));
  merged.screenshots.sort((a, b) => rank(path.basename(a)) - rank(path.basename(b)));
  merged.layoutObservations.sort((a, b) => rank(a.label ?? a.name) - rank(b.label ?? b.name));
  merged.completed = merged.errors.length === 0;
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(merged, null, 2));
  if (!merged.completed) throw new Error(`File-selection shards failed; inspect ${path.join(output, 'report.json')}`);
  return merged;
}

if (require.main === module) {
  const output = process.env.WUU_E2E_OUTPUT || fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-file-selection-shards-'));
  runShards({ output }).then(report => console.log(`PASS ${report.cases.length} file selection cases. Report and screenshots: ${report.output}`))
    .catch(error => { console.error(error); process.exitCode = 1; });
}
module.exports = { runShards };
