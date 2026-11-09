const { execFileSync } = require('node:child_process');
const { appendFileSync, mkdirSync } = require('node:fs');
const { join } = require('node:path');
const { verifyCheckpoint } = require('./release-checkpoint.cjs');
const { restoreNotarization } = require('./restore-notarization.cjs');

function restoreReleaseCheckpoint(runId, identity, {
  repository = process.env.GITHUB_REPOSITORY, currentRunId = process.env.GITHUB_RUN_ID,
  root = join(process.env.RUNNER_TEMP || '.', 'wuu-release-checkpoint'), run = execFileSync,
} = {}) {
  if (!/^[1-9]\d*$/.test(runId) || runId === String(currentRunId)) throw new Error('Expected a different completed release source run ID');
  const metadata = JSON.parse(run('gh', ['api', `repos/${repository}/actions/runs/${runId}`], { encoding: 'utf8', timeout: 60_000 }));
  if (metadata.repository.full_name !== repository || metadata.head_repository.full_name !== repository ||
      metadata.path !== '.github/workflows/release.yml' || !['push', 'workflow_dispatch'].includes(metadata.event) || metadata.status !== 'completed') {
    throw new Error('Checkpoint source must be a completed Release workflow in this repository');
  }
  if (!/^[0-9a-f]{40}$/.test(metadata.head_sha)) throw new Error('Invalid release tooling source commit');
  // A manual release run's head SHA identifies tooling, not the tagged product.
  run('git', ['merge-base', '--is-ancestor', metadata.head_sha, 'origin/main'], { stdio: 'inherit', timeout: 60_000 });
  const pages = JSON.parse(run('gh', ['api', '--paginate', '--slurp',
    `repos/${repository}/actions/runs/${runId}/artifacts`], { encoding: 'utf8', timeout: 60_000 }));
  const candidates = pages.flatMap(page => page.artifacts).flatMap(artifact => {
    const imported = artifact.name.match(/^resume-(\d+)-(signed-app|distribution)$/);
    const stage = imported?.[2] || ({ 'signed-app': 'signed-app', 'signed-distribution': 'distribution' })[artifact.name];
    return stage ? [{ ...artifact, stage, attempt: Number(imported?.[1] || 0) }] : [];
  }).sort((a, b) => Number(b.stage === 'distribution') - Number(a.stage === 'distribution') || b.attempt - a.attempt);
  const checkpoint = candidates[0];
  if (!checkpoint || checkpoint.expired) throw new Error('Missing or expired signed release checkpoint; do not reuse a receipt with a new build');
  mkdirSync(root, { recursive: true });
  run('gh', ['run', 'download', runId, '--repo', repository, '--name', checkpoint.name, '--dir', root], { stdio: 'inherit', timeout: 5 * 60_000 });
  verifyCheckpoint(root, { ...identity, stage: checkpoint.stage });
  restoreNotarization(checkpoint.stage === 'signed-app' ? 'app' : 'dmg', { repository, runId, beforeAttempt: Infinity, root, run });
  console.log(`Recovered ${checkpoint.name} from release run ${runId} for ${identity.commit}.`);
  return checkpoint.stage;
}

if (require.main === module) {
  const [runId, commit, version] = process.argv.slice(2);
  if (runId) {
    const stage = restoreReleaseCheckpoint(runId, { commit, version });
    appendFileSync(process.env.GITHUB_OUTPUT, `checkpoint=${stage}\nartifact=resume-${process.env.GITHUB_RUN_ATTEMPT}-${stage}\n`);
  }
}
module.exports = { restoreReleaseCheckpoint };
