import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {cp, mkdir, readFile, symlink, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';

const exec = promisify(execFile);

// Both author acceptance suites compile outside the checkout, against packed
// public exports. Dependencies are reused offline; source internals are absent.
export async function preparePublicSDK({sdk, temporary, tarball}) {
  const tsc = join(sdk, 'node_modules/typescript/bin/tsc');
  let packed;
  if (tarball) {
    packed = 'verified-sdk.tgz';
    await cp(resolve(tarball), join(temporary, packed));
  } else {
    const stage = join(temporary, 'package');
    await mkdir(stage);
    await cp(join(sdk, 'package.json'), join(stage, 'package.json'));
    await exec(process.execPath, [tsc, '--project', join(sdk, 'tsconfig.json'), '--outDir', join(stage, 'dist')]);
    packed = JSON.parse((await exec('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temporary], {cwd: stage})).stdout)[0].filename;
  }
  const project = join(temporary, 'author');
  const installed = join(project, 'node_modules/@wuu/plugin-sdk');
  await mkdir(installed, {recursive: true});
  await exec('tar', ['-xzf', join(temporary, packed), '--strip-components=1', '-C', installed]);
  const manifest = JSON.parse(await readFile(join(installed, 'package.json'), 'utf8'));
  for (const dependency of Object.keys({...manifest.dependencies, ...manifest.devDependencies})) {
    const destination = join(project, 'node_modules', dependency);
    await mkdir(resolve(destination, '..'), {recursive: true});
    await symlink(join(sdk, 'node_modules', dependency), destination);
  }
  await writeFile(join(project, 'package.json'), '{"type":"module","private":true}');
  return {tsc, project, manifest, packed: join(temporary, packed)};
}
