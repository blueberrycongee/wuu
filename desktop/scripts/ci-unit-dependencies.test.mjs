import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { selectRelatedTests } from './ci-unit-dependencies.mjs';

test('dependency selection keeps direct, transitive, re-export, dynamic and opaque consumers', () => {
  const root = mkdtempSync(join(tmpdir(), 'wuu-unit-graph-'));
  const sources = {
    'setup.ts': 'import "./src/global";',
    'src/global.ts': 'export const theme = 1;',
    'src/ui.tsx': 'export const value = 1;',
    'src/barrel.ts': 'export { value } from "./ui";',
    'src/cycle.ts': 'export * from "./barrel"; import "./loop";',
    'src/loop.ts': 'import "./cycle";',
    'direct.test.ts': 'import "./src/ui";',
    'indirect.test.ts': 'import "./src/cycle";',
    'regex.test.ts': 'import "~ui/ui";',
    'alias.test.ts': 'import "@/ui";',
    'dynamic.test.ts': 'const load = () => import("./src/ui");',
    'mock.test.ts': 'vi.mock("./src/ui", () => ({}));',
    'opaque.test.ts': 'const load = (name) => import(name);',
    'filesystem.test.ts': 'import { readFileSync as read } from "node:fs"; read("anything");',
    'unresolved.test.ts': 'import "./missing";',
    'unrelated.test.ts': 'import "vitest"; const value = 2;',
  };
  try {
    for (const [name, text] of Object.entries(sources)) {
      mkdirSync(join(root, name, '..'), { recursive: true }); writeFileSync(join(root, name), text);
    }
    const tests = Object.keys(sources).filter(name => name.endsWith('.test.ts')).map(name => join(root, name));
    const result = selectRelatedTests({ tests, aliases: [{ find: '@', replacement: join(root, 'src') }, { find: /^~ui\//, replacement: join(root, 'src') + '/' }], changed: [join(root, 'src/ui.tsx')] });
    assert.deepEqual(result.selected.map(path => path.slice(root.length + 1)).sort(), [
      'alias.test.ts', 'direct.test.ts', 'dynamic.test.ts', 'filesystem.test.ts', 'indirect.test.ts', 'mock.test.ts', 'opaque.test.ts', 'regex.test.ts', 'unresolved.test.ts',
    ]);
    assert.deepEqual(selectRelatedTests({ tests, changed: [join(root, 'src/global.ts')], globalRoots: [join(root, 'setup.ts')] }).selected, tests);
    assert.ok(result.uncertain.includes(join(root, 'opaque.test.ts')));
    assert.deepEqual(selectRelatedTests({ tests: [join(root, 'unrelated.test.ts')], changed: [join(root, 'unrelated.test.ts')] }).selected, [join(root, 'unrelated.test.ts')]);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
