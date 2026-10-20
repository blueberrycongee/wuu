import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, symlinkSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { build } from 'vite';
import { bundledDependencyLicenses } from './bundled-dependency-licenses.ts';

// Release contract: notices survive dependency pruning, including transitive,
// CSS-only and linked vendored packages; missing license text stops the build.
function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'wuu-bundled-licenses-'));
  function write(path, content) {
    mkdirSync(join(root, path, '..'), { recursive: true });
    writeFileSync(join(root, path), content);
  }
  write('package.json', JSON.stringify({ name: 'fixture', type: 'module' }));
  write('entry.js', 'import { value } from "direct"; import "styles/style.css"; import "blobatar"; console.log(value);');
  for (const name of ['direct', '@scope/transitive', 'styles', 'unused']) {
    write(`node_modules/${name}/package.json`, JSON.stringify({ name, version: '1.0.0', main: 'index.js' }));
    write(`node_modules/${name}/LICENSE`, `${name} license text`);
  }
  write('node_modules/direct/index.js', 'export { value } from "@scope/transitive";');
  write('node_modules/@scope/transitive/index.js', 'export const value = 42;');
  write('node_modules/@scope/transitive/NOTICE.txt', 'Required transitive attribution');
  write('node_modules/@scope/transitive/ThirdPartyNotices.txt', 'Embedded library notices');
  write('node_modules/styles/style.css', 'body { color: red; }');
  write('vendor/blobatar/package.json', JSON.stringify({ name: 'blobatar', version: '1.0.0', main: 'index.js' }));
  write('vendor/blobatar/index.js', 'console.log("blobatar");');
  write('vendor/blobatar/LICENSE', 'Blobatar license text');
  write('vendor/zeron/LICENSE', 'Zeron license text');
  symlinkSync(join(root, 'vendor/blobatar'), join(root, 'node_modules/blobatar'), 'junction');
  return { root, write };
}

async function bundle(root) {
  await build({
    root, configFile: false, logLevel: 'silent', plugins: [bundledDependencyLicenses(root)],
    build: { minify: false, rollupOptions: { input: join(root, 'entry.js') } },
  });
  return readFileSync(join(root, 'dist/THIRD-PARTY-LICENSES.txt'), 'utf8');
}

test('emitted release notices preserve transitive, asset and vendored licenses', async () => {
  const { root } = fixture();
  try {
    const notices = await bundle(root);
    for (const expected of ['direct license text', '@scope/transitive license text', 'styles license text', 'Required transitive attribution', 'Embedded library notices', 'Blobatar license text', 'Zeron license text']) {
      assert.ok(notices.includes(expected), `Missing ${expected}`);
    }
    assert.ok(!notices.includes('unused license text'));
    assert.equal(notices.match(/Blobatar license text/g).length, 1);
    assert.equal(await bundle(root), notices, 'Notice ordering must be deterministic');
    assert.ok(!notices.includes(root), 'Release notices must not leak build paths');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('build fails with package identity when required license text is missing', async () => {
  const { root } = fixture();
  try {
    rmSync(join(root, 'node_modules/@scope/transitive/LICENSE'));
    await assert.rejects(bundle(root), /Missing license text for @scope\/transitive@1\.0\.0/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('known npm omissions use pinned upstream notices and reject version changes', async () => {
  const { root, write } = fixture();
  try {
    rmSync(join(root, 'node_modules/direct/LICENSE'));
    write('node_modules/direct/package.json', JSON.stringify({ name: 'remark-math', version: '6.0.0', main: 'index.js' }));
    write('vendor/licenses/remark-math-MIT.txt', readFileSync(new URL('../vendor/licenses/remark-math-MIT.txt', import.meta.url), 'utf8'));
    assert.match(await bundle(root), /Copyright \(c\) 2017 Junyoung Choi/);
    write('node_modules/direct/package.json', JSON.stringify({ name: 'remark-math', version: '6.0.1', main: 'index.js' }));
    await assert.rejects(bundle(root), /Missing license text for remark-math@6\.0\.1/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
