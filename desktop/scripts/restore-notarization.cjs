const { execFileSync } = require('node:child_process');
const { mkdirSync } = require('node:fs');

const stage = process.argv[2];
if (!['app', 'dmg'].includes(stage)) throw new Error('Expected app or dmg receipt stage');
const prefix = `${stage}-receipts-`;
const pages = JSON.parse(execFileSync('gh', ['api', '--paginate', '--slurp',
  `repos/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}/artifacts`], { encoding: 'utf8' }));
const previous = pages.flatMap(page => page.artifacts)
  .filter(item => item.name.startsWith(prefix) && /^\d+$/.test(item.name.slice(prefix.length)) && Number(item.name.slice(prefix.length)) < Number(process.env.GITHUB_RUN_ATTEMPT))
  .sort((a, b) => Number(b.name.slice(prefix.length)) - Number(a.name.slice(prefix.length)))[0];
if (previous) {
  if (previous.expired) throw new Error('Previous notarization receipt expired; reconcile Apple history before resubmitting');
  mkdirSync('release/notarization', { recursive: true });
  execFileSync('gh', ['run', 'download', process.env.GITHUB_RUN_ID, '--name', previous.name, '--dir', 'release/notarization'], { stdio: 'inherit' });
  console.log(`Restored ${previous.name}; the notarization helper verifies the archive hash before reuse.`);
}
