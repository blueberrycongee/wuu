const { execFileSync } = require('node:child_process');
const { existsSync, mkdirSync, mkdtempSync, copyFileSync, rmSync } = require('node:fs');
const { join } = require('node:path');

function restoreNotarization(stage, {
  repository = process.env.GITHUB_REPOSITORY, runId = process.env.GITHUB_RUN_ID,
  beforeAttempt = Number(process.env.GITHUB_RUN_ATTEMPT), root = 'release', run = execFileSync,
} = {}) {
  if (!['app', 'dmg'].includes(stage)) throw new Error('Expected app or dmg receipt stage');
  if (!(Number.isInteger(beforeAttempt) && beforeAttempt > 0) && beforeAttempt !== Infinity) throw new Error('Missing release run attempt');
  const api = endpoint => JSON.parse(run('gh', ['api', '--paginate', '--slurp',
    `repos/${repository}/actions/runs/${runId}/${endpoint}`], { encoding: 'utf8', timeout: 60_000 }));
  const pattern = new RegExp(`^${stage}-(receipts|submission)-(\\d+)$`);
  const previous = api('artifacts').flatMap(page => page.artifacts)
    .map(item => ({ ...item, match: item.name.match(pattern) }))
    .filter(item => item.match && Number(item.match[2]) < beforeAttempt)
    .sort((a, b) => Number(b.match[2]) - Number(a.match[2]) || Number(b.match[1] === 'receipts') - Number(a.match[1] === 'receipts'))[0];
  const directory = join(root, 'notarization');
  const receipt = join(directory, `${stage}.json`);
  if (previous) {
    if (previous.expired) throw new Error('Previous notarization receipt expired; reconcile Apple history before resubmitting');
    mkdirSync(directory, { recursive: true });
    // A newer current-run receipt supersedes any imported checkpoint receipt.
    const download = mkdtempSync(join(root, 'receipt-'));
    try {
      run('gh', ['run', 'download', String(runId), '--repo', repository, '--name', previous.name, '--dir', download], { stdio: 'inherit', timeout: 5 * 60_000 });
      const restored = join(download, `${stage}.json`);
      if (!existsSync(restored)) throw new Error(`Missing ${stage} receipt in ${previous.name}; reconcile Apple history before resubmitting`);
      copyFileSync(restored, receipt);
    } finally { rmSync(download, { recursive: true, force: true }); }
    console.log(`Restored ${previous.name}; the notarization helper verifies the archive hash before reuse.`);
  } else if (!existsSync(receipt)) {
    // Cancellation can kill the runner before its always() receipt upload. Absence
    // is safe only when no earlier attempt reached the Apple submission step.
    const submissionSteps = stage === 'app'
      ? ['Submit saved app for notarization', 'Notarize the saved app and build distribution archives']
      : ['Submit saved DMG for notarization', 'Notarize and verify the saved distribution'];
    const started = api('jobs?filter=all').flatMap(page => page.jobs).some(job => {
      if (!job.steps.some(step => submissionSteps.includes(step.name) && step.status !== 'queued' && step.conclusion !== 'skipped')) return false;
      if (!Number.isInteger(job.run_attempt)) throw new Error('Missing Apple job attempt; cannot safely recover without a receipt');
      return job.run_attempt < beforeAttempt;
    });
    if (started) throw new Error(`Missing ${stage} notarization receipt after an earlier Apple step started; reconcile Apple history before resubmitting`);
  }
}

if (require.main === module) restoreNotarization(process.argv[2]);
module.exports = { restoreNotarization };
