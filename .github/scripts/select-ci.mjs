import { execFileSync } from 'node:child_process';
import { appendFileSync, readFileSync } from 'node:fs';

const { suites, entries } = JSON.parse(readFileSync(new URL('./desktop-ci-suites.json', import.meta.url)));
const desktopSuites = new Set();
let fullDesktop = false;
let relatedEligible = true;
const relatedSources = [];
const selected = { go: false, desktop: false, clients: false, native: false };
const all = () => {
  Object.keys(selected).forEach(key => { selected[key] = true; });
  fullDesktop = true;
  relatedEligible = false;
};
const event = process.env.GITHUB_EVENT_NAME;
if (!['pull_request', 'push', 'workflow_dispatch'].includes(event)) {
  throw new Error(`Unsupported CI event: ${event}`);
}

// Manual runs cover everything. A newly created branch has no before commit.
if (event === 'workflow_dispatch' || /^0+$/.test(process.env.CI_BASE || '')) {
  all();
} else {
  if (!process.env.CI_BASE || !process.env.CI_HEAD) throw new Error('Missing CI comparison refs');
  // Include both sides of renames and deletions, without API pagination limits.
  // A missing commit or failed diff must fail selection, never skip validation.
  const paths = execFileSync('git', [
    'diff', '--name-only', '--no-renames', '-z', process.env.CI_BASE, process.env.CI_HEAD, '--',
  ], { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }).split('\0').filter(Boolean);
  for (const path of paths) {
    // Only known non-runtime documentation is exempt. Markdown under prompts,
    // plugins, or fixtures can be executable input and is not ignored globally.
    if (/^(docs\/|docs-site\/|landing\/|\.github\/(ISSUE_TEMPLATE\/|PULL_REQUEST_TEMPLATE\.md$))/.test(path)
      || /^(README(?:\.[^/]+)?\.md|README\.md|CHANGELOG\.md|CONTRIBUTING\.md|CODE_OF_CONDUCT\.md|AGENTS\.md)$/.test(path)) continue;
    // Only existing renderer modules use parsed import dependencies. Global inputs,
    // plugin loading, and additions/deletions retain the full suite.
    if (/^desktop\/src\/renderer\/(?!test\/|plugins\/).*\.(ts|tsx)$/.test(path) && !path.endsWith('.d.ts')) {
      const status = execFileSync('git', ['diff', '--name-status', '--no-renames', process.env.CI_BASE, process.env.CI_HEAD, '--', path], { encoding: 'utf8' });
      if (status.startsWith('M\t')) relatedSources.push(path.slice('desktop/'.length));
      else relatedEligible = false;
    } else {
      relatedEligible = false;
    }
    // Native apps have their own workflow; shared client packages do not.
    if (/^clients\/native\//.test(path)) continue;
    if (Object.hasOwn(entries, path)) {
      selected.desktop = true;
      desktopSuites.add(entries[path]);
    } else if (/^desktop\/src\/renderer\//.test(path)) {
      fullDesktop = true;
      // mobile-web imports production renderer modules, styles and assets.
      selected.desktop = selected.clients = true;
    } else if (/^desktop\//.test(path)) {
      fullDesktop = true;
      // Config, native helpers, scripts, vendored modules and dependencies can
      // affect both Web builds and native packaging.
      selected.desktop = selected.clients = selected.native = true;
    } else if (/^clients\/(mobile|mobile-app)\//.test(path)) {
      selected.clients = true;
    } else {
      // Shared protocol, core, Go, plugins, dependency locks, build scripts and
      // unknown/new roots retain full coverage until their consumers are known.
      all();
    }
  }
  console.log(`Compared ${paths.length} changed paths`);
}

// Main pushes only check native packaging. PRs own validation and E2E suites.
const outputs = {
  go: event !== 'push' && selected.go,
  desktop: event !== 'push' && selected.desktop,
  clients: event !== 'push' && selected.clients,
  native: selected.native,
  desktop_related: JSON.stringify(event === 'pull_request' && relatedEligible ? relatedSources : []),
  desktop_suites: JSON.stringify(event !== 'push' && selected.desktop
    ? suites.filter(suite => fullDesktop || desktopSuites.has(suite)) : []),
};
for (const [key, value] of Object.entries(outputs)) {
  const line = `${key}=${value}\n`;
  process.stdout.write(line);
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, line);
}
