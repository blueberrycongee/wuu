import { spawn } from 'node:child_process';
import { mkdirSync, openSync, closeSync, writeSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { StringDecoder } from 'node:string_decoder';
import { fileURLToPath } from 'node:url';

// Keep Makefile package discovery/order as the sole inventory authority. -json
// changes reporting only: no -count, -run, parallelism or timeout overrides.
export async function runTimedCommand({ command, args, directory, stdout = process.stdout, stderr = process.stderr }) {
  mkdirSync(directory, { recursive: true });
  const files = Object.fromEntries(['stdout.log', 'stderr.log', 'events.jsonl'].map(name => [name, openSync(join(directory, name), 'w')]));
  const tests = [], packages = [], cached = new Set();
  const decoder = new StringDecoder('utf8');
  let pending = '', eventCount = 0, spawnError = null, receivedSignal = null;
  const started = Date.now();
  function line(raw) {
    let event;
    try { event = JSON.parse(raw); } catch { stdout.write(raw); return; }
    if (!event || typeof event.Action !== 'string' || (typeof event.Package !== 'string' && typeof event.ImportPath !== 'string')) { stdout.write(raw); return; }
    eventCount++;
    writeSync(files['events.jsonl'], raw.endsWith('\n') ? raw : raw + '\n');
    if (typeof event.Output === 'string') {
      stdout.write(event.Output);
      if (/^ok\s+.*\(cached\)\s*$/.test(event.Output)) cached.add(event.Package);
    }
    if (['pass', 'fail', 'skip'].includes(event.Action)) {
      const row = { package: event.Package, action: event.Action, reportedElapsedSeconds: event.Elapsed ?? null,
        elapsedSeconds: event.Elapsed ?? null, ...(event.Test ? { test: event.Test } : {}) };
      (event.Test ? tests : packages).push(row);
    }
  }
  const child = spawn(command, args, { stdio: ['inherit', 'pipe', 'pipe'] });
  const handlers = new Map(['SIGINT', 'SIGTERM'].map(signal => [signal, () => { receivedSignal = signal; child.kill(signal); }]));
  for (const [signal, handler] of handlers) process.on(signal, handler);
  child.stdout.on('data', chunk => {
    writeSync(files['stdout.log'], chunk);
    pending += decoder.write(chunk);
    let index;
    while ((index = pending.indexOf('\n')) >= 0) { line(pending.slice(0, index + 1)); pending = pending.slice(index + 1); }
  });
  child.stderr.on('data', chunk => { writeSync(files['stderr.log'], chunk); stderr.write(chunk); });
  child.on('error', error => { spawnError = String(error); stderr.write(spawnError + '\n'); writeSync(files['stderr.log'], spawnError + '\n'); });
  const result = await new Promise(resolve => child.on('close', (code, signal) => resolve({ code: receivedSignal ? 1 : (code ?? 1), signal: signal ?? receivedSignal })));
  pending += decoder.end();
  if (pending) line(pending);
  for (const [signal, handler] of handlers) process.removeListener(signal, handler);
  for (const fd of Object.values(files)) closeSync(fd);
  for (const row of [...tests, ...packages]) {
    row.cached = cached.has(row.package);
    if (row.cached) row.elapsedSeconds = null; // Replayed timings are not this run's measurements.
  }
  writeFileSync(join(directory, 'summary.json'), JSON.stringify({ schemaVersion: 1, command, args, ...result,
    spawnError, wallSeconds: (Date.now() - started) / 1000, eventCount, tests, packages }, null, 2) + '\n');
  return result;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const directory = process.env.WUU_GO_TIMING_EVIDENCE;
  if (!directory) throw new Error('WUU_GO_TIMING_EVIDENCE is required');
  const result = await runTimedCommand({ command: 'make', args: ['test-go-packages', 'GO_TEST_OUTPUT_FLAGS=-json'], directory });
  process.exitCode = result.code;
}
