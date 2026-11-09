import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { cp, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import { preparePublicSDK } from '../../../packages/plugin-sdk/test/public-package.mjs';

const exec = promisify(execFile), here = fileURLToPath(new URL('.', import.meta.url)), root = resolve(here, '../../..');
const output = resolve(process.env.WUU_FEATURE_UI_OUTPUT ?? join(root, '.tmp/e2e/feature-ui'));
const binary = resolve(process.env.WUU_E2E_CORE ?? join(root, 'bin/wuu-architecture'));
await mkdir(output, { recursive: true });
const temporary = await mkdtemp(join(tmpdir(), 'wuu-feature-ui-'));
process.env.npm_config_cache ??= join(temporary, 'npm-cache');
const { tsc, project, packed } = await preparePublicSDK({ sdk: join(root, 'packages/plugin-sdk'), temporary });
for (const [name, source, services, requirements] of [
  ['acceptance-surfaces', 'surfaces', ['ui.shell', 'ui.composer', 'ui.navigation'], []],
  ['acceptance-goal-ui', 'goal-consumer', [], [{ name: 'goal.control', major_version: 1, required: false }]],
]) {
  const directory = join(project, name); await mkdir(directory);
  await cp(join(here, `${source}.tsx`), join(directory, 'desktop.tsx'));
  await exec(process.execPath, [tsc, 'desktop.tsx', '--strict', '--jsx', 'react', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2022', '--outDir', 'dist'], { cwd: directory });
  await cp(join(directory, 'dist/desktop.js'), join(directory, 'desktop.js'));
  await writeFile(join(directory, 'plugin.json'), JSON.stringify({ schema_version: 1, id: name, name, version: '1.0.0', desktop: { entry: 'desktop.js',
    services: services.map(name => ({ name, version: '1.0.0', methods: [{ name: 'describe', input_schema: `${name}.describe.input.v1`, output_schema: `${name}.describe.output.v1` }] })), required_services: requirements } }, null, 2));
}
const hash = data => createHash('sha256').update(data).digest('hex');
await writeFile(join(output, 'inputs.json'), JSON.stringify({ independent: project, binary, binarySHA256: hash(await readFile(binary)), sdkSHA256: hash(await readFile(packed)) }, null, 2));
try {
  const result = await exec(process.execPath, [join(root, 'desktop/node_modules/vitest/vitest.mjs'), 'run', 'src/renderer/plugins/FeatureUIComposition.integration.test.tsx', '--maxWorkers=1', '--minWorkers=1'], {
    cwd: join(root, 'desktop'), env: { ...process.env, WUU_FEATURE_UI_BINARY: binary, WUU_FEATURE_UI_PROJECT: project, WUU_FEATURE_UI_OUTPUT: output }, maxBuffer: 8 * 1024 * 1024,
  });
  await writeFile(join(output, 'renderer.log'), result.stdout + result.stderr); console.log(result.stdout);
} catch (error) { await writeFile(join(output, 'renderer.log'), `${error.stdout ?? ''}\n${error.stderr ?? ''}`); throw error; }
console.log(`Feature UI evidence: ${output}`);
