import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve, relative } from 'node:path';
import { createVitest } from 'vitest/node';
import { selectRelatedTests } from './ci-unit-dependencies.mjs';

const desktop = fileURLToPath(new URL('..', import.meta.url));
const related = JSON.parse(process.env.WUU_CI_RELATED || '[]');
if (!Array.isArray(related) || related.some(path => typeof path !== 'string' || !/^src\/renderer\/(?!.*\.\.).*\.tsx?$/.test(path))) {
  throw new Error('Invalid renderer source selection');
}
const run = (command, args) => {
  const result = spawnSync(command, args, { cwd: desktop, stdio: 'inherit', env: process.env });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status || 1);
};
if (!related.length) {
  run('npm', ['test']);
} else {
  // Ask Vitest for its configured test inventory, without transforming/executing
  // mocked browser modules such as Monaco during dependency discovery.
  const ctx = await createVitest('test', { root: desktop, watch: false, run: true });
  let tests, aliases, globalRoots;
  try {
    tests = [...new Set((await ctx.globTestSpecifications()).map(spec => spec.moduleId))];
    aliases = ctx.projects.flatMap(project => project.vite.config.resolve.alias);
    globalRoots = ctx.projects.flatMap(project => [...project.config.setupFiles, ...project.config.globalSetup]);
  }
  finally { await ctx.close(); }
  const selection = selectRelatedTests({ tests, aliases, globalRoots, changed: related.map(path => resolve(desktop, path)) });
  const serialized = ['src/main/gitService.test.ts', 'src/main/remoteGit.test.ts'];
  const files = selection.selected.map(path => relative(desktop, path)).filter(path => !serialized.includes(path));
  mkdirSync(resolve(desktop, 'out/ci'), { recursive: true });
  writeFileSync(resolve(desktop, 'out/ci/unit-related.json'), JSON.stringify({
    changed: related, totalFiles: tests.length, selected: files,
    uncertain: selection.uncertain.map(path => relative(desktop, path)), serialized,
    affected: selection.affected.map(path => relative(desktop, path)),
    fullFallback: selection.affected.length === 0,
  }, null, 2));
  console.log(`Unit dependencies: ${files.length}/${tests.length} files selected, ${selection.uncertain.length} with opaque dependencies retained`);
  if (!selection.affected.length) {
    console.log('No related tests found; running the complete unit gate');
    run('npm', ['test']);
  } else {
    run('npm', ['run', 'test:dev-launcher']);
    run(process.execPath, ['node_modules/vitest/vitest.mjs', 'run', ...files,
      '--exclude=src/main/gitService.test.ts', '--exclude=src/main/remoteGit.test.ts']);
    run(process.execPath, ['node_modules/vitest/vitest.mjs', 'run', ...serialized,
      '--maxWorkers=1', '--minWorkers=1', '--testTimeout=15000']);
  }
}
