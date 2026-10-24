import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, cp, readFile, writeFile, symlink, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const exec = promisify(execFile);
const authorSDK = fileURLToPath(new URL('../', import.meta.url));
const sdk = resolve(process.env.WUU_PARITY_SDK_ROOT ?? authorSDK);
const repo = resolve(authorSDK, '../..');
const output = resolve(process.env.WUU_PARITY_OUTPUT ?? join(repo, 'artifacts/parity-author'));
await mkdir(output, {recursive: true});
const temporary = await mkdtemp(join(tmpdir(), 'wuu-parity-author-'));
try {
  const stage = join(temporary, 'package');
  await mkdir(stage);
  await cp(join(sdk, 'package.json'), join(stage, 'package.json'));
  const tsc = join(sdk, 'node_modules/typescript/bin/tsc');
  await exec(process.execPath, [tsc, '--project', join(sdk, 'tsconfig.json'), '--outDir', join(stage, 'dist')]);
  const packed = JSON.parse((await exec('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temporary], {cwd: stage})).stdout)[0].filename;
  const project = join(temporary, 'author');
  const installed = join(project, 'node_modules/@wuu/plugin-sdk');
  await mkdir(installed, {recursive: true});
  await exec('tar', ['-xzf', join(temporary, packed), '--strip-components=1', '-C', installed]);
  // Reuse declared dependencies offline without mutating their shared install.
  const manifest = JSON.parse(await readFile(join(sdk, 'package.json'), 'utf8'));
  for (const dependency of Object.keys({...manifest.dependencies, ...manifest.devDependencies})) {
    const destination = join(project, 'node_modules', dependency);
    await mkdir(resolve(destination, '..'), {recursive: true});
    await symlink(join(sdk, 'node_modules', dependency), destination);
  }
  await writeFile(join(project, 'package.json'), '{"type":"module","private":true}');
  for (const name of ['tools', 'sessions', 'journal', 'turn-state', 'prompt', 'compaction']) await cp(join(repo, `examples/plugins/independent-providers/${name}.ts`), join(project, `${name}.ts`));
  for (const name of ['shell', 'navigation']) await cp(join(repo, `examples/plugins/independent-providers/${name}.tsx`), join(project, `${name}.tsx`));
  await exec(process.execPath, [tsc, 'tools.ts', 'sessions.ts', 'prompt.ts', 'compaction.ts', 'shell.tsx', 'navigation.tsx', '--jsx', 'react', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2022', '--strict', '--skipLibCheck'], {cwd: project});
  const checked = await exec(process.execPath, ['--test', join(authorSDK, 'test/parity-tools.test.mjs'), join(authorSDK, 'test/parity-sessions.test.mjs'), join(authorSDK, 'test/parity-policies.test.mjs')], {
    env: {...process.env, WUU_PARITY_TOOLS_ENTRY: join(project, 'tools.js'), WUU_PARITY_SESSIONS_ENTRY: join(project, 'sessions.js'), WUU_PARITY_PROMPT_ENTRY: join(project, 'prompt.js'), WUU_PARITY_COMPACTION_ENTRY: join(project, 'compaction.js')},
  });
  await writeFile(join(output, 'public-sdk-check.log'), checked.stdout);
  const tarball = await readFile(join(temporary, packed));
  await cp(join(temporary, packed), join(output, 'sdk.tgz'));
  await writeFile(join(output, 'public-sdk-check.json'), JSON.stringify({passed: true, sdkRoot: sdk, packageSHA256: createHash('sha256').update(tarball).digest('hex'), package: manifest.name, version: manifest.version, sourceFiles: ['tools.ts', 'sessions.ts', 'journal.ts', 'turn-state.ts', 'prompt.ts', 'compaction.ts', 'shell.tsx', 'navigation.tsx']}, null, 2));
  console.log(checked.stdout);
  console.log('PASS: independent session metadata and tool providers compiled against packed public SDK; no dependency installation.');
} catch (error) {
  console.error(error.stdout ?? '', error.stderr ?? '');
  throw error;
} finally {
  await rm(temporary, {recursive: true, force: true});
}
