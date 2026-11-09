import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const base = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const catalog = JSON.parse(await readFile(resolve(base, 'references/catalog.json'), 'utf8'));
const args = process.argv.slice(2);
const options = { kind: '', id: '', limit: 12, offset: 0 };
const terms = [];
for (let i = 0; i < args.length; i++) {
  const arg = args[i];
  if (arg === '--help') {
    console.log('Usage: node catalog.mjs [--kind design-system|template|workflow|craft|prompt] [--id <id>] [--limit 1..50] [--offset N] [search terms]');
    process.exit(0);
  }
  if (arg.startsWith('--')) {
    const key = arg.slice(2);
    if (!Object.hasOwn(options, key) || args[i + 1] === undefined || args[i + 1].startsWith('--')) {
      throw new Error(`Unknown or incomplete option: ${arg}`);
    }
    options[key] = key === 'limit' || key === 'offset' ? Number(args[++i]) : args[++i];
  } else {
    terms.push(arg.toLowerCase());
  }
}
if (options.kind && !Object.hasOwn(catalog.counts, options.kind)) throw new Error(`Unknown kind: ${options.kind}`);
if (!Number.isInteger(options.limit) || options.limit < 1 || options.limit > 50) throw new Error('--limit must be 1..50');
if (!Number.isInteger(options.offset) || options.offset < 0) throw new Error('--offset must be a nonnegative integer');
const matches = catalog.entries.filter((entry) => {
  if (options.kind && entry.kind !== options.kind) return false;
  if (options.id && entry.id !== options.id) return false;
  const searchable = [entry.id, entry.name, entry.description, entry.mode, entry.category, ...(entry.tags ?? [])].join(' ').toLowerCase();
  return terms.every((term) => searchable.includes(term));
});
if (options.id && matches.length === 0) throw new Error(`No matching preset: ${options.id}`);
const items = matches.slice(options.offset, options.offset + options.limit);
const next = options.offset + items.length;
console.log(JSON.stringify({
  base_directory: base,
  revision: catalog.upstream.revision,
  counts: catalog.counts,
  total: matches.length,
  offset: options.offset,
  next_offset: next < matches.length ? next : null,
  items,
}, null, 2));
