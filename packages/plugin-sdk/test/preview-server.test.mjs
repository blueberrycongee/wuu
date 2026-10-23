import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm, realpath } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { get } from 'node:http';
import { startPreview } from '../preview/server.mjs';

// Failure cases: a bad save must keep the served last-good revision; errors
// locate the author's file; atomic replacement and repair must publish again;
// the server must never expose arbitrary local paths or accept foreign origins.
test('single TSX author loop retains the last good build and repairs edits', async () => {
  const root = await mkdtemp(join(tmpdir(), 'wuu-ui-author-'));
  const file = join(root, 'panel.tsx');
  const code = (title) => `import type { PluginGenerationApi } from '@wuu/plugin-sdk';
export function activate(api: PluginGenerationApi) {
 const React = api.react;
 api.registerViewType({id:'panel',title:'Panel',render:()=> <h1>${title}</h1>});
}`;
  await writeFile(file, code('First'));
  const server = await startPreview({ sources: [file], port: 0 });
  const stop = new AbortController();
  try {
    const status = await (await fetch(`${server.url}/status`)).json();
    assert.equal(status.diagnostics.length, 0);
    assert.ok(status.revision);
    const first = await (await fetch(`${server.url}/module/${status.revision}.mjs`)).text();
    assert.match(first, /First/);
    const stream = await fetch(`${server.url}/events`, { signal: stop.signal });
    const reader = stream.body.getReader();
    let buffered = '';
    const next = async (predicate) => {
      const timeout = setTimeout(() => stop.abort(), 15000);
      try {
        for (;;) {
          let boundary;
          while ((boundary = buffered.indexOf('\n\n')) !== -1) {
            const frame = buffered.slice(0, boundary);
            buffered = buffered.slice(boundary + 2);
            const data = frame.split('\n').find((line) => line.startsWith('data: '));
            if (data) { const value = JSON.parse(data.slice(6)); if (predicate(value)) return value; }
          }
          const chunk = await reader.read();
          assert.equal(chunk.done, false);
          buffered += new TextDecoder().decode(chunk.value);
        }
      } finally { clearTimeout(timeout); }
    };
    await next((event) => event.revision === status.revision);
    await writeFile(file, 'export function activate(api) { const broken = <');
    const failure = await next((event) => event.diagnostics.length > 0);
    assert.equal(failure.revision, status.revision);
    assert.equal(failure.diagnostics[0].file, await realpath(file));
    assert.ok(failure.diagnostics[0].line > 0);
    assert.equal(await (await fetch(`${server.url}/module/${status.revision}.mjs`)).text(), first);
    const replacement = join(root, 'panel-saving.tsx');
    await writeFile(replacement, code('Repaired'));
    const { rename } = await import('node:fs/promises');
    await rename(replacement, file);
    const repaired = await next((event) => event.diagnostics.length === 0 && event.revision !== status.revision);
    assert.match(await (await fetch(`${server.url}/module/${repaired.revision}.mjs`)).text(), /Repaired/);
    assert.equal((await fetch(`${server.url}/anything-else`)).status, 404);
    assert.equal((await fetch(`${server.url}/status`, { headers: { Origin: 'https://example.com' } })).status, 403);
    assert.equal(await new Promise((accept) => { get(`${server.url}/status`, { headers: { Host: 'example.com' } }, (response) => { response.resume(); accept(response.statusCode); }); }), 403);
  } finally {
    stop.abort();
    await server.close();
    await rm(root, { recursive: true, force: true });
  }
});
