// Vitest shards its complete discovered inventory, including related-mode files.
// Keep the Git-process fixtures serialized and run the launcher contract once.
export function unitTestPlan(files = [], shard = '') {
  if (shard && !/^[12]\/2$/.test(shard)) throw new Error('Invalid CI unit shard');
  const first = !shard || shard === '1/2';
  const serialized = ['src/main/gitService.test.ts', 'src/main/remoteGit.test.ts'];
  return [
    ...(first ? [{ tool: 'npm', args: ['run', 'test:dev-launcher'] }] : []),
    { tool: 'vitest', args: ['run', ...files.filter(file => !serialized.includes(file)),
      ...serialized.map(file => `--exclude=${file}`), ...(shard ? [`--shard=${shard}`] : [])] },
    ...(first ? [{ tool: 'vitest', args: ['run', ...serialized,
      '--maxWorkers=1', '--minWorkers=1', '--testTimeout=15000'] }] : []),
  ];
}
