#!/usr/bin/env node
import { startPreview } from '../preview/server.mjs';
import { checkSources } from '../preview/check.mjs';
import { readFile, realpath } from 'node:fs/promises';

const sources = [];
let port = 0;
let state;
let check = false;
for (let i = 2; i < process.argv.length; i++) {
  const argument = process.argv[i];
  if (argument === '--check') check = true;
  else if (argument === '--port') port = Number(process.argv[++i]);
  else if (argument === '--state') state = process.argv[++i];
  else if (argument === '--help') {
    console.log('Usage: wuu-plugin-preview [--check] [--port 0] [--state snapshot.json] file.tsx [child.tsx ...]\nServes a local browser preview with source reload and diagnostics. No plugin manifest, build or installation.');
    process.exit(0);
  } else if (argument.startsWith('--')) throw new Error(`Unknown option: ${argument}`);
  else sources.push(argument);
}
if (!Number.isInteger(port) || port < 0 || port > 65535) throw new Error('Port must be an integer from 0 to 65535.');
try {
  if (check) {
    if (!sources.length) throw new Error('Provide at least one source file.');
    const files = await Promise.all(sources.map((source) => realpath(source)));
    const diagnostics = checkSources(files, await Promise.all(files.map((file) => readFile(file, 'utf8'))));
    for (const diagnostic of diagnostics) console.error(`${diagnostic.file ?? 'Preview'}:${diagnostic.line ?? 0}:${diagnostic.column ?? 0}: ${diagnostic.message}`);
    process.exit(diagnostics.length ? 1 : 0);
  }
  const server = await startPreview({ sources, port, state });
  console.log(`Wuu component preview: ${server.url}\nWatching ${sources.length} source file(s). Browser data is synthetic; no model calls or persistent writes.`);
  const stop = async () => { await server.close(); process.exit(0); };
  process.once('SIGINT', stop); process.once('SIGTERM', stop);
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
