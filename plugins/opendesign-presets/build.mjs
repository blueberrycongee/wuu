import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import yaml from 'js-yaml';

const root = dirname(fileURLToPath(import.meta.url));
const upstream = JSON.parse(await readFile(join(root, 'upstream.json'), 'utf8'));
const args = process.argv.slice(2);
if (args.length && (args.length !== 2 || args[0] !== '--source')) {
  throw new Error('Usage: node build.mjs [--source <OpenDesign checkout>]');
}
if (!/^[a-f0-9]{40}$/.test(upstream.revision)) throw new Error('Pin a full upstream commit hash');
const scratch = await mkdtemp(join(tmpdir(), 'wuu-opendesign-'));
const output = join(root, 'dist');
const staged = join(scratch, 'package');
const skill = join(staged, 'skills/opendesign');
const assets = join(skill, 'assets/upstream');

try {
  const source = args[1] ? resolve(args[1]) : join(scratch, 'checkout');
  await cp(join(root, 'package'), staged, { recursive: true });
  await mkdir(assets, { recursive: true });
  if (!args[1]) {
    execFileSync('git', ['clone', '--depth=1', '--filter=blob:none', '--no-checkout', upstream.repository, source], { stdio: 'inherit' });
    execFileSync('git', ['-C', source, 'fetch', '--depth=1', 'origin', upstream.revision], { stdio: 'inherit' });
    // Checkout batches blob downloads; archive on a partial clone can fetch unrelated attributes one by one.
    execFileSync('git', ['-C', source, 'sparse-checkout', 'set', '--cone', ...upstream.directories], { stdio: 'inherit' });
    execFileSync('git', ['-C', source, 'checkout', '--detach', upstream.revision], { stdio: 'inherit' });
    for (const path of ['LICENSE', 'README.md', ...upstream.directories]) {
      await cp(join(source, path), join(assets, path), { recursive: true });
    }
  } else {
    // Archive committed objects, so local edits and untracked files cannot enter the package.
    const archive = execFileSync('git', ['-C', source, 'archive', '--format=tar', upstream.revision,
      'LICENSE', 'README.md', ...upstream.directories], { maxBuffer: 256 * 1024 * 1024 });
    const archivePath = join(scratch, 'upstream.tar');
    await writeFile(archivePath, archive);
    execFileSync('tar', ['-xf', archivePath, '-C', assets]);
  }
  // Repository-only metadata is not a preset and is omitted by Wuu's zip packer.
  for (const path of upstream.excludedFiles) await rm(join(assets, path));
  await cp(join(root, '../../LICENSE'), join(staged, 'LICENSE'));

  const entries = [];
  for (const dir of await readdir(join(assets, 'design-systems'), { withFileTypes: true })) {
    if (!dir.isDirectory() || dir.name.startsWith('_')) continue;
    const base = `design-systems/${dir.name}`;
    const manifest = JSON.parse(await readFile(join(assets, base, 'manifest.json'), 'utf8'));
    const files = [manifest.files.design, manifest.files.tokens, manifest.usage,
      manifest.files.components, manifest.componentsManifest].filter(Boolean);
    for (const file of files) await stat(join(assets, base, file));
    entries.push({
      id: `design-system/${dir.name}`, kind: 'design-system', name: manifest.name,
      description: manifest.description, category: manifest.category,
      directory: `assets/upstream/${base}`,
      files: files.map((file) => `assets/upstream/${base}/${file}`),
      craft: [...new Set([...(manifest.craft?.applies ?? []), ...(manifest.craft?.suggested ?? [])])],
    });
  }
  for (const [directory, kind] of [['design-templates', 'template'], ['skills', 'workflow']]) {
    for (const dir of await readdir(join(assets, directory), { withFileTypes: true })) {
      if (!dir.isDirectory() || dir.name.startsWith('_')) continue;
      const base = `${directory}/${dir.name}`;
      const content = await readFile(join(assets, base, 'SKILL.md'), 'utf8');
      const match = content.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
      if (!match) throw new Error(`Missing preset frontmatter: ${base}`);
      const meta = yaml.load(match[1], { schema: yaml.JSON_SCHEMA });
      entries.push({
        id: `${kind}/${dir.name}`, kind, name: meta.name ?? dir.name,
        description: String(meta.description ?? '').trim(), mode: meta.od?.mode ?? 'unspecified',
        directory: `assets/upstream/${base}`, files: [`assets/upstream/${base}/SKILL.md`],
        craft: meta.od?.craft?.requires ?? [],
        requirements: meta.od?.capabilities_required ?? [],
      });
    }
  }
  for (const file of await readdir(join(assets, 'craft'))) {
    if (!/^[a-z][a-z-]+\.md$/.test(file)) continue;
    const content = await readFile(join(assets, 'craft', file), 'utf8');
    entries.push({
      id: `craft/${file.slice(0, -3)}`, kind: 'craft',
      name: content.match(/^#\s+(.+)$/m)?.[1] ?? file.slice(0, -3),
      files: [`assets/upstream/craft/${file}`],
    });
  }
  for (const file of await filesUnder(join(assets, 'prompt-templates'))) {
    if (!file.endsWith('.json')) continue;
    const prompt = JSON.parse(await readFile(join(assets, 'prompt-templates', file), 'utf8'));
    if (!prompt.id || typeof prompt.prompt !== 'string') throw new Error(`Invalid media prompt: ${file}`);
    entries.push({
      id: `prompt/${prompt.surface}/${prompt.id}`, kind: 'prompt', name: prompt.title,
      description: prompt.summary, mode: prompt.surface, tags: prompt.tags ?? [],
      model: prompt.model, files: [`assets/upstream/prompt-templates/${file}`],
    });
  }
  entries.sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  if (new Set(entries.map((entry) => entry.id)).size !== entries.length) throw new Error('Duplicate catalog IDs');
  const craft = new Set(entries.filter((entry) => entry.kind === 'craft').map((entry) => entry.id.slice(6)));
  for (const entry of entries) {
    entry.craftFiles = (entry.craft ?? []).filter((name) => craft.has(name)).map((name) => `assets/upstream/craft/${name}.md`);
    const missing = (entry.craft ?? []).filter((name) => !craft.has(name));
    if (missing.length) entry.unavailableCraft = missing;
  }

  const files = [];
  for (const path of await filesUnder(assets)) {
    const bytes = await readFile(join(assets, path));
    files.push({ path, bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') });
  }
  const counts = {};
  for (const entry of entries) counts[entry.kind] = (counts[entry.kind] ?? 0) + 1;
  await mkdir(join(skill, 'references'), { recursive: true });
  await writeFile(join(skill, 'references/catalog.json'), JSON.stringify({
    schemaVersion: 1, upstream, counts, entries,
  }, null, 2) + '\n');
  await writeFile(join(staged, 'UPSTREAM.json'), JSON.stringify({
    ...upstream, counts, fileCount: files.length,
    bytes: files.reduce((sum, file) => sum + file.bytes, 0), files,
  }, null, 2) + '\n');

  // Only the generated dist tree is replaced; author-maintained package files stay intact.
  await rm(output, { recursive: true, force: true });
  await mkdir(output, { recursive: true });
  // Copy also works when the system temporary directory and checkout use different volumes.
  await cp(staged, output, { recursive: true });
  console.log(JSON.stringify({ output, revision: upstream.revision, counts, files: files.length }, null, 2));
} finally {
  await rm(scratch, { recursive: true, force: true });
}

async function filesUnder(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      for (const file of await filesUnder(join(directory, entry.name))) files.push(`${entry.name}/${file}`);
    } else if (entry.isFile()) {
      files.push(entry.name);
    } else {
      throw new Error(`Unsupported upstream resource: ${join(directory, entry.name)}`);
    }
  }
  return files.sort();
}
