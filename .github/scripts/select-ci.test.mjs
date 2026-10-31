import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';

const script = resolve('.github/scripts/select-ci.mjs');
const suites = ['unit', 'selection', 'corpus', 'layout', 'session', 'session-lifecycle', 'shell'];
function select(paths, event = 'pull_request', remove = false) {
  const cwd = mkdtempSync(join(tmpdir(), 'wuu-ci-selection-'));
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  try {
    git('init', '-q');
    git('config', 'user.email', 'ci@example.invalid');
    git('config', 'user.name', 'CI fixture');
    for (const path of paths) {
      mkdirSync(join(cwd, path, '..'), { recursive: true });
      writeFileSync(join(cwd, path), 'before\n');
    }
    git('add', '.'); git('commit', '-qm', 'baseline');
    const base = git('rev-parse', 'HEAD');
    for (const path of paths) {
      if (remove) rmSync(join(cwd, path));
      else writeFileSync(join(cwd, path), 'after\n');
    }
    git('add', '-A'); git('commit', '-qm', 'change');
    const stdout = execFileSync(process.execPath, [script], { cwd, encoding: 'utf8', env: {
      ...process.env, GITHUB_OUTPUT: '', GITHUB_EVENT_NAME: event, CI_BASE: base, CI_HEAD: 'HEAD',
    } });
    return Object.fromEntries(stdout.split('\n').filter(line => line.includes('=')).map(line => {
      const i = line.indexOf('='); return [line.slice(0, i), line.slice(i + 1)];
    }));
  } finally { rmSync(cwd, { recursive: true, force: true }); }
}
test('isolated image test selects layout without unrelated platform jobs', () => {
  const result = select(['desktop/scripts/image-layout-e2e.cjs']);
  assert.deepEqual(JSON.parse(result.desktop_suites), ['layout']);
  assert.equal(result.desktop, 'true');
  for (const job of ['go', 'clients', 'native']) assert.equal(result[job], 'false');
});
test('Personal desktop fixture changes select the required hosted controls without platform packaging', () => {
  for (const path of ['desktop/scripts/build-personal-fixture.cjs', 'desktop/scripts/personal-settings-e2e.cjs', 'desktop/scripts/personal-approval-ui-e2e.cjs', 'desktop/dev/personal/index.tsx', 'desktop/dev/personal/shell.ts']) {
    const result = select([path]);
    assert.deepEqual(JSON.parse(result.desktop_suites), ['shell'], path);
    assert.equal(result.desktop, 'true', path);
    for (const job of ['go', 'clients', 'native']) assert.equal(result[job], 'false', `${path}: ${job}`);
  }
});
test('multiple test changes union suites, including deleted entries', () => {
  assert.deepEqual(JSON.parse(select(['desktop/scripts/image-layout-e2e.cjs', 'desktop/scripts/file-selection-e2e.cjs'], 'pull_request', true).desktop_suites), ['selection', 'layout']);
});
test('session-specific changes retain all session checks across both shards', () => {
  for (const path of ['desktop/scripts/session-message-e2e.cjs', 'desktop/scripts/composer-footer-e2e.cjs', 'desktop/scripts/session-switch-e2e.cjs']) {
    assert.deepEqual(JSON.parse(select([path]).desktop_suites), ['session', 'session-lifecycle'], path);
  }
});
for (const path of ['desktop/src/renderer/RichContent.tsx', 'desktop/scripts/new-helper.cjs', 'desktop/scripts/image-layout-e2e-preload.cjs', 'desktop/package-lock.json', '.github/workflows/ci.yml', 'internal/session/run.go']) {
  test(`shared or unknown input retains every Desktop suite: ${path}`, () => {
    assert.deepEqual(JSON.parse(select([path]).desktop_suites), suites);
  });
}
test('shared changes override narrow test selection', () => {
  assert.deepEqual(JSON.parse(select(['desktop/scripts/image-layout-e2e.cjs', 'desktop/src/renderer/styles.css']).desktop_suites), suites);
});
test('documentation excludes Desktop; manual runs restore full coverage', () => {
  assert.deepEqual(JSON.parse(select(['docs/en/project/development.md']).desktop_suites), []);
  assert.deepEqual(JSON.parse(select(['docs/en/project/development.md'], 'workflow_dispatch').desktop_suites), suites);
});
test('main push retains existing no-duplicate-test policy', () => {
  assert.deepEqual(JSON.parse(select(['desktop/scripts/image-layout-e2e.cjs'], 'push').desktop_suites), []);
});

test('ordinary renderer edits pass source paths to dependency-based unit selection', () => {
  const result = select(['desktop/src/renderer/LanguagePreferenceSection.tsx']);
  assert.deepEqual(JSON.parse(result.desktop_related), ['src/renderer/LanguagePreferenceSection.tsx']);
  assert.deepEqual(JSON.parse(result.desktop_suites), suites);
});
for (const path of ['desktop/src/renderer/styles.css', 'desktop/src/renderer/test/setup.ts', 'desktop/vitest.config.ts', 'desktop/package-lock.json']) {
  test(`global or non-source input disables related mode: ${path}`, () => {
    assert.deepEqual(JSON.parse(select(['desktop/src/renderer/LanguagePreferenceSection.tsx', path]).desktop_related), []);
  });
}
test('deleted UI sources and manual dispatch use full unit coverage', () => {
  const paths = ['desktop/src/renderer/LanguagePreferenceSection.tsx'];
  assert.deepEqual(JSON.parse(select(paths, 'pull_request', true).desktop_related), []);
  assert.deepEqual(JSON.parse(select(paths, 'workflow_dispatch').desktop_related), []);
});
test('documentation does not broaden a renderer-only change', () => {
  assert.deepEqual(JSON.parse(select(['docs/en/project/development.md', 'desktop/src/renderer/LanguagePreferenceSection.tsx']).desktop_related), ['src/renderer/LanguagePreferenceSection.tsx']);
});

test('architecture conformance covers desktop public boundaries without selecting unrelated Go suites', () => {
  for (const path of ['desktop/src/main/trustedRemoteTransport.ts', 'desktop/src/shared/protocol.ts', 'desktop/scripts/trusted-desktop-e2e.mjs', 'desktop/src/renderer/plugins/PluginHost.ts', 'desktop/src/renderer/test/setup.ts', 'desktop/vitest.config.ts']) {
    const result = select([path]);
    assert.equal(result.architecture, 'true', path);
    assert.equal(result.go, 'false', path);
  }
});

test('architecture conformance follows shared changes and existing event policy, not renderer-only edits', () => {
  for (const path of ['internal/runtime/session.go', 'packages/plugin-sdk/test/minimal-package.mjs', 'scripts/session-federation-e2e.mjs']) {
    assert.equal(select([path]).architecture, 'true', path);
  }
  assert.equal(select(['desktop/src/renderer/LanguagePreferenceSection.tsx']).architecture, 'false');
  assert.equal(select(['docs/en/project/development.md']).architecture, 'false');
  assert.equal(select(['docs/en/project/development.md'], 'workflow_dispatch').architecture, 'true');
  assert.equal(select(['desktop/src/main/trustedRemoteTransport.ts'], 'push').architecture, 'false');
});

test('agent skill instructions are non-runtime documentation, not executable skill resources', () => {
  const instruction = '.agents/skills/change-validation/SKILL.md';
  for (const remove of [false, true]) {
    const result = select([instruction], 'pull_request', remove);
    for (const job of ['go', 'architecture', 'desktop', 'clients', 'native']) assert.equal(result[job], 'false', job);
    assert.deepEqual(JSON.parse(result.desktop_suites), []);
  }
  assert.deepEqual(JSON.parse(select([instruction, 'desktop/src/renderer/LanguagePreferenceSection.tsx']).desktop_related), ['src/renderer/LanguagePreferenceSection.tsx']);
  for (const path of ['.agents/skills/change-validation/scripts/check.mjs', '.agents/skills/change-validation/assets/SKILL.md', 'plugins/example/SKILL.md']) {
    assert.equal(select([path]).go, 'true', path);
    assert.deepEqual(JSON.parse(select([path]).desktop_suites), suites, path);
  }
  assert.deepEqual(JSON.parse(select([instruction], 'workflow_dispatch').desktop_suites), suites);
});
