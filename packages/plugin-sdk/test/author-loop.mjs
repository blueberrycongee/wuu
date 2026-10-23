import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, copyFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { once } from 'node:events';

const exec = promisify(execFile);
const repo = fileURLToPath(new URL('../../../', import.meta.url));
const output = resolve(process.env.WUU_AUTHOR_OUTPUT ?? join(repo, 'artifacts/author-ui'));
await mkdir(output, { recursive: true });
const fixture = await mkdtemp(join(tmpdir(), 'wuu-packed-author-'));
const log = [];
let preview;
try {
  await exec('npm', ['--prefix', join(repo, 'packages/plugin-sdk'), 'run', 'build']);
  await exec('npm', ['pack', join(repo, 'packages/plugin-sdk'), '--ignore-scripts', '--pack-destination', fixture], { cwd: fixture });
  const tarball = join(fixture, 'wuu-plugin-sdk-0.1.0.tgz');
  await writeFile(join(fixture, 'package.json'), JSON.stringify({ private: true, type: 'module', dependencies: { '@wuu/plugin-sdk': `file:${tarball}` } }, null, 2));
  log.push((await exec('npm', ['install', '--no-audit', '--no-fund'], { cwd: fixture })).stdout);
  await copyFile(join(repo, 'examples/plugins/research-workbench/shell.tsx'), join(fixture, 'shell.tsx'));
  await copyFile(join(repo, 'examples/plugins/research-evidence/evidence.tsx'), join(fixture, 'evidence.tsx'));
  const cli = join(fixture, 'node_modules/.bin/wuu-plugin-preview');
  log.push((await exec(cli, ['--check', 'shell.tsx', 'evidence.tsx'], { cwd: fixture })).stdout);
  const inventory = JSON.parse((await exec('npm', ['ls', '--depth=0', '--json'], { cwd: fixture })).stdout);
  if (Object.keys(inventory.dependencies).join(',') !== '@wuu/plugin-sdk') throw new Error('Author fixture has dependencies other than the packed public SDK.');
  await writeFile(join(output, 'package-boundary.json'), JSON.stringify({ fixture, inventory, sourceFiles: ['shell.tsx', 'evidence.tsx'] }, null, 2));
  preview = spawn(cli, ['shell.tsx', 'evidence.tsx'], { cwd: fixture, stdio: ['ignore', 'pipe', 'pipe'] });
  preview.stderr.on('data', (data) => log.push(String(data)));
  const url = await new Promise((accept, reject) => {
    let data = '';
    const timeout = setTimeout(() => reject(new Error('Preview did not start.')), 30000);
    preview.once('exit', (code) => { clearTimeout(timeout); reject(new Error(`Preview exited: ${code}`)); });
    preview.stdout.on('data', (chunk) => { data += chunk; log.push(String(chunk)); const match = data.match(/http:\/\/127\.0\.0\.1:\d+/); if (match) { clearTimeout(timeout); accept(match[0]); } });
  });
  const require = createRequire(join(repo, 'desktop/package.json'));
  const electron = require('electron');
  const result = await exec(electron, ['--no-sandbox', join(repo, 'packages/plugin-sdk/test/author-ui.cjs'), String(url), fixture, output], {
    cwd: fixture, env: { ...process.env, ELECTRON_DISABLE_SECURITY_WARNINGS: 'true' }, maxBuffer: 4 * 1024 * 1024,
  });
  log.push(result.stdout, result.stderr);
  console.log(result.stdout);
} catch (error) {
  log.push(error.stack ?? String(error), error.stdout ?? '', error.stderr ?? '');
  throw error;
} finally {
  if (preview) { const closed = once(preview, 'exit'); preview.kill('SIGTERM'); await closed; }
  await writeFile(join(output, 'author-loop.log'), log.join('\n'));
  await rm(fixture, { recursive: true, force: true });
}
