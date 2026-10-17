const { createHash } = require('node:crypto');
const { readFileSync, writeFileSync } = require('node:fs');
const { join, basename } = require('node:path');

function fileHash(root, name) {
  if (basename(name) !== name || !/^[\w.-]+$/.test(name) || name === '.' || name === '..') throw new Error('Invalid checkpoint filename');
  return createHash('sha256').update(readFileSync(join(root, name))).digest('hex');
}

function saveCheckpoint(root, identity, files) {
  const manifest = { ...identity, sha256: Object.fromEntries(files.map(name => [name, fileHash(root, name)])) };
  writeFileSync(join(root, 'checkpoint.json'), JSON.stringify(manifest, null, 2) + '\n');
}

function verifyCheckpoint(root, identity) {
  const manifest = JSON.parse(readFileSync(join(root, 'checkpoint.json'), 'utf8'));
  for (const key of ['commit', 'version', 'stage']) {
    if (manifest[key] !== identity[key]) throw new Error(`Checkpoint identity mismatch: ${key}`);
  }
  if (!manifest.sha256 || !Object.keys(manifest.sha256).length) throw new Error('Empty checkpoint');
  for (const [name, expected] of Object.entries(manifest.sha256)) {
    if (fileHash(root, name) !== expected) throw new Error(`Checkpoint hash mismatch: ${name}`);
  }
  return manifest;
}

module.exports = { saveCheckpoint, verifyCheckpoint };
