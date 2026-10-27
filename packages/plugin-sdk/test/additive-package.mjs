import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp, mkdir, cp, readFile, writeFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {preparePublicSDK} from './public-package.mjs';
const exec = promisify(execFile), sdk = fileURLToPath(new URL('../', import.meta.url)), repo = resolve(sdk, '../..');
const binary = process.env.WUU_ADDITIVE_BINARY;
if (!binary) throw new Error('WUU_ADDITIVE_BINARY must identify the built Wuu executable');
const output = resolve(process.env.WUU_ADDITIVE_OUTPUT ?? join(repo, 'artifacts/additive-cooperation'));
await mkdir(output, {recursive: true});
for (const name of ['result.json', 'failure.log', 'renderer.log', 'transcript.jsonl']) await rm(join(output, name), {force: true});
const temporary = await mkdtemp(join(tmpdir(), 'wuu-additive-author-'));
try {
  const {tsc, project, packed} = await preparePublicSDK({sdk, temporary});
  for (const name of ['additive-counter', 'additive-counter-controls']) await cp(join(repo, 'examples/plugins', name), join(project, name), {recursive: true});
  // A publishes its contract as an ordinary package export. B consumes that
  // public package, not A's source path or the host's internal implementations.
  await exec(process.execPath, [tsc, '-p', 'tsconfig.json'], {cwd: join(project, 'additive-counter')});
  const counterTarball = JSON.parse((await exec('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temporary], {cwd: join(project, 'additive-counter')})).stdout)[0].filename;
  const contractPackage = join(project, 'node_modules/@wuu-example/additive-counter');
  await mkdir(contractPackage, {recursive: true});
  await exec('tar', ['-xzf', join(temporary, counterTarball), '--strip-components=1', '-C', contractPackage]);
  await exec(process.execPath, [tsc, '-p', 'tsconfig.json'], {cwd: join(project, 'additive-counter-controls')});
  for (const name of ['additive-counter', 'additive-counter-controls']) {
    const source = join(project, name);
    const manifest = JSON.parse(await readFile(join(source, 'plugin.json'), 'utf8'));
    if (manifest.runtime) {
      await writeFile(join(source, 'entry.mjs'), `import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createCounter} from './dist/runtime.js'; await runJSONLRuntime(createCounter(), {input:process.stdin,output:process.stdout});`);
      await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(source, 'entry.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(source, 'runtime.mjs')}`]);
      manifest.runtime.command = process.execPath;
      await writeFile(join(source, 'major-two.mjs'), `import {runJSONLRuntime} from '@wuu/plugin-sdk'; import {createCounter} from './dist/runtime.js'; import {counterDescriptor} from './dist/contract.js'; await runJSONLRuntime({...createCounter(), initialize() {return {provided_services:[{...counterDescriptor,version:'2.0.0'}]};}}, {input:process.stdin,output:process.stdout});`);
      await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(source, 'major-two.mjs'), '--bundle', '--platform=node', '--format=esm', `--outfile=${join(source, 'runtime-v2.mjs')}`]);
    }
    await exec(join(sdk, 'node_modules/esbuild/bin/esbuild'), [join(source, 'dist/desktop.js'), '--bundle', '--platform=browser', '--format=esm', `--outfile=${join(source, 'desktop.js')}`]);
    await writeFile(join(source, 'plugin.json'), JSON.stringify(manifest));
  }
  const peer = join(project, 'additive-counter-controls-peer');
  await cp(join(project, 'additive-counter-controls'), peer, {recursive: true});
  const peerManifest = JSON.parse(await readFile(join(peer, 'plugin.json'), 'utf8')); peerManifest.id = 'additive-counter-controls-peer';
  await writeFile(join(peer, 'plugin.json'), JSON.stringify(peerManifest));
  await cp(packed, join(output, 'sdk.tgz'));
  await writeFile(join(output, 'inputs.json'), JSON.stringify({sdkSHA256:createHash('sha256').update(await readFile(packed)).digest('hex'),binary:resolve(binary),binarySHA256:createHash('sha256').update(await readFile(binary)).digest('hex')}, null, 2));
  const checked = await exec(process.execPath, [join(repo, 'desktop/node_modules/vitest/vitest.mjs'), 'run', 'src/renderer/plugins/AdditiveCooperation.integration.test.tsx', 'src/renderer/plugins/ApplicationShell.test.tsx', 'src/renderer/plugins/DesktopPluginRuntime.test.ts', '--maxWorkers=1'], {
    cwd:join(repo, 'desktop'), env:{...process.env, WUU_ADDITIVE_BINARY:resolve(binary), WUU_ADDITIVE_PROJECT:project, WUU_ADDITIVE_OUTPUT:output}, maxBuffer:8*1024*1024,
  });
  await writeFile(join(output, 'renderer.log'), checked.stdout + checked.stderr); console.log(checked.stdout);
  const report = JSON.parse(await readFile(join(output, 'result.json'), 'utf8'));
  if (report.passed !== true || report.cases.length < 11) throw new Error('Additive boundary cases did not complete');
  console.log('PASS: independent additive packages cooperate through declared services and owned UI slots.');
} catch (error) { await writeFile(join(output, 'failure.log'), `${error.stdout ?? ''}\n${error.stderr ?? ''}\n${error.stack}`); console.error(error.stdout ?? '', error.stderr ?? ''); throw error; }
finally { await rm(temporary, {recursive:true, force:true}); }
