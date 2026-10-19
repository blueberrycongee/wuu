const { createHash } = require('node:crypto');
const { readFileSync, writeFileSync, readdirSync } = require('node:fs');
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
  const required = identity.stage === 'signed-app' ? ['signed-app.zip']
    : identity.stage === 'distribution' ? ['wuu-mac-arm64.dmg', 'wuu-mac-arm64.zip', 'wuu-mac-arm64.zip.blockmap', 'signed-dmg.dmg']
      : null;
  if (!required) throw new Error('Unknown checkpoint stage');
  if (required.some(name => !Object.hasOwn(manifest.sha256, name))) throw new Error('Incomplete checkpoint: missing required archive');
  if (identity.stage === 'distribution') {
    const manifests = readdirSync(root).filter(name => name.endsWith('-mac.yml'));
    if (!manifests.length || !Object.keys(manifest.sha256).some(name => name.endsWith('-mac.yml'))) throw new Error('Incomplete checkpoint: missing update metadata');
    if (manifests.some(name => !Object.hasOwn(manifest.sha256, name))) throw new Error('Unbound update metadata in checkpoint');
    if (manifest.sha256['wuu-mac-arm64.dmg'] !== manifest.sha256['signed-dmg.dmg']) throw new Error('Checkpoint DMG does not match notarization archive');
  }
  for (const [name, expected] of Object.entries(manifest.sha256)) {
    if (fileHash(root, name) !== expected) throw new Error(`Checkpoint hash mismatch: ${name}`);
  }
  return manifest;
}

module.exports = { saveCheckpoint, verifyCheckpoint };
