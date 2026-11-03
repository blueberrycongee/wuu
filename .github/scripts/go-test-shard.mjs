import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';

const [pkg, index, count, tags] = process.argv.slice(2);
const shard = Number(index);
const shards = Number(count);
if (!pkg || !Number.isInteger(shard) || !Number.isInteger(shards) || shards < 1 || shard < 0 || shard >= shards) {
  throw new Error('Usage: go-test-shard.mjs <package> <zero-based shard> <shard count> [build tags]');
}
const flags = tags ? [`-tags=${tags}`] : [];
if (process.env.WUU_GO_TEST_RACE === 'true') flags.push('-race', '-p', '2');
const pattern = process.env.WUU_GO_TEST_PATTERN || '.';
// Enumerate the compiled package so build constraints, new tests, examples and
// fuzz seed tests follow the same Go toolchain as the subsequent execution.
const listing = execFileSync('go', ['test', ...flags, `-list=${pattern}`, pkg], { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 });
const names = listing.split(/\r?\n/).filter(name => /^(Test|Example|Fuzz)\S*$/.test(name));
if (!names.length || new Set(names).size !== names.length) throw new Error('Expected a nonempty, unique Go test inventory');
const weights = JSON.parse(process.env.WUU_GO_SHARD_WEIGHTS || '{}');
if (!weights || Array.isArray(weights) || typeof weights !== 'object'
  || Object.values(weights).some(value => !Number.isFinite(value) || value <= 0)) throw new Error('Invalid Go shard weights');
let tests;
if (Object.keys(weights).length) {
  // Coarse weights isolate measured expensive process/contention guards. New
  // tests still enter the complete inventory with unit weight, never disappear.
  const bins = Array.from({ length: shards }, () => ({ weight: 0, tests: [] }));
  for (const name of [...names].sort((a, b) => (weights[b] || 1) - (weights[a] || 1) || a.localeCompare(b))) {
    const bin = bins.reduce((best, candidate) => candidate.weight < best.weight ? candidate : best);
    bin.tests.push(name); bin.weight += weights[name] || 1;
  }
  tests = bins[shard].tests;
} else {
  tests = names.filter(name => createHash('sha256').update(name).digest().readUInt32BE(0) % shards === shard);
}
if (!tests.length) throw new Error(`Shard ${shard}/${shards} selected no tests`);
const manifest = { package: pkg, tags: tags || '', race: flags.includes('-race'), pattern, weights, shard, shards, total: names.length, tests };
console.log(JSON.stringify(manifest, null, 2));
if (process.env.WUU_GO_SHARD_EVIDENCE) {
  mkdirSync(dirname(process.env.WUU_GO_SHARD_EVIDENCE), { recursive: true });
  writeFileSync(process.env.WUU_GO_SHARD_EVIDENCE, JSON.stringify(manifest, null, 2) + '\n');
}
const escaped = tests.map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
const result = spawnSync('go', ['test', ...flags, '-count=1', `-run=^(${escaped.join('|')})$`, pkg], { stdio: 'inherit' });
if (result.error) throw result.error;
process.exit(result.status ?? 1);
