import { spawn } from 'node:child_process';
import { readFileSync, mkdirSync, writeFileSync, createWriteStream } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Each replay has a private Electron profile, WUU_HOME, X display and evidence
// directory. Share only immutable binaries; never build inside the workers.
export function corpusGroups(samples) {
  if (!samples.length || new Set(samples.map(s => s.id)).size !== samples.length) throw new Error('Invalid corpus inventory');
  const groups = Array.from({ length: 3 }, () => ({ weight: 0, samples: [] }));
  for (const sample of [...samples].sort((a, b) => b.steps.length - a.steps.length || a.id.localeCompare(b.id))) {
    const group = groups.reduce((least, candidate) => candidate.weight < least.weight ? candidate : least);
    group.samples.push(sample.id);
    group.weight += sample.steps.length;
  }
  return groups.map(group => group.samples.sort());
}

async function main() {
  if (process.env.WUU_CORPUS_SKIP_BUILD !== '1') throw new Error('CI corpus workers require the already-built revision');
  const desktop = fileURLToPath(new URL('..', import.meta.url));
  const corpus = JSON.parse(readFileSync(join(desktop, 'scripts/fixtures/conversation-corpus/corpus.json')));
  const groups = corpusGroups(corpus.samples);
  const output = resolve(process.env.WUU_TOUR_OUTPUT);
  mkdirSync(output, { recursive: true });
  writeFileSync(join(output, 'inventory.json'), JSON.stringify({ groups, textOnly: ['sample-02'] }, null, 2));
  const replay = (name, samples, textOnly = false) => new Promise(resolveReplay => {
    const directory = join(output, name);
    mkdirSync(directory, { recursive: true });
    const log = createWriteStream(join(directory, 'run.log'));
    const child = spawn('xvfb-run', ['--auto-servernum', process.execPath, 'scripts/run-conversation-corpus.cjs', '--no-sandbox'], {
      cwd: desktop, env: { ...process.env, WUU_TOUR_OUTPUT: directory, WUU_TOUR_ONLY: samples.join(','), WUU_CORPUS_TEXT_ONLY: textOnly ? '1' : '0' },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    child.stdout.pipe(log, { end: false }); child.stderr.pipe(log, { end: false });
    child.once('error', error => { log.end(String(error)); resolveReplay(false); });
    child.once('close', code => { log.end(); console.log(`${name}: ${code === 0 ? 'passed' : 'failed'} (${directory})`); resolveReplay(code === 0); });
  });
  const passed = await Promise.all(groups.map((samples, index) => replay(`replay-${index}`, samples)));
  const textOnly = await replay('text-only', ['sample-02'], true);
  if (!passed.every(Boolean) || !textOnly) process.exitCode = 1;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
