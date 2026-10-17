const { execFileSync } = require('node:child_process');
const { mkdirSync, copyFileSync, readdirSync, existsSync } = require('node:fs');
const { join, resolve } = require('node:path');
const { saveCheckpoint, verifyCheckpoint } = require('./release-checkpoint.cjs');
const { releaseCredentials, checkReleaseSigning, releaseSigningIdentity } = require('./check-release-signing.cjs');
const { notarizeArchive, stapleArtifact, validateArtifact } = require('./notarization.cjs');

async function main() {
  releaseCredentials();
  if (process.env.WUU_RELEASE !== '1') throw new Error('Release stages require WUU_RELEASE=1');
  const run = (command, args) => execFileSync(command, args, { stdio: 'inherit' });
  const script = name => join(__dirname, name);
  const root = resolve('release');
  const app = join(root, 'mac-arm64', 'wuu.app');
  const version = require(resolve('package.json')).version;
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  if (process.env.WUU_RELEASE_SOURCE_COMMIT && process.env.WUU_RELEASE_SOURCE_COMMIT !== commit) throw new Error('Source commit mismatch');
  const identity = stage => ({ commit, version, stage });
  const builder = args => run('node_modules/.bin/electron-builder', ['--config', script('release-builder.cjs'), '--mac', ...args, '--arm64', '--publish=never', '--config.electronDist=node_modules/electron/dist']);
  mkdirSync(root, { recursive: true });
  switch (process.argv[2]) {
    case 'submit-app':
    case 'submit-dmg': {
      const isApp = process.argv[2] === 'submit-app';
      verifyCheckpoint(root, identity(isApp ? 'signed-app' : 'distribution'));
      await notarizeArchive(join(root, isApp ? 'signed-app.zip' : 'signed-dmg.dmg'),
        join(root, `notarization/${isApp ? 'app' : 'dmg'}.json`), { submitOnly: true });
      break;
    }
    case 'sign-app': {
      checkReleaseSigning();
      builder(['dir']);
      run(process.execPath, [script('verify-mac-release.cjs'), app]);
      const actual = execFileSync(join(app, 'Contents/Resources/bin/wuu-core'), ['--version'], { encoding: 'utf8' });
      if (existsSync(join(app, 'Contents/Resources/bin/wuu'))) throw new Error('Packaged core must remain private as wuu-core');
      if (actual.includes('dirty')) throw new Error('Packaged core was built from a dirty worktree');
      const info = execFileSync('/usr/libexec/PlistBuddy', ['-c', 'Print :CFBundleShortVersionString', join(app, 'Contents/Info.plist')], { encoding: 'utf8' }).trim();
      if (info !== version) throw new Error('Signed app version mismatch');
      run('ditto', ['-c', '-k', '--sequesterRsrc', '--keepParent', app, join(root, 'signed-app.zip')]);
      saveCheckpoint(root, identity('signed-app'), ['signed-app.zip']);
      break;
    }
    case 'package': {
      verifyCheckpoint(root, identity('signed-app'));
      // Extract the same signed bytes on every retry; never rebuild or re-sign the app.
      run('ditto', ['-x', '-k', join(root, 'signed-app.zip'), join(root, 'mac-arm64')]);
      run(process.execPath, [script('verify-mac-release.cjs'), app]);
      await notarizeArchive(join(root, 'signed-app.zip'), join(root, 'notarization/app.json'));
      await stapleArtifact(app);
      await validateArtifact(app);
      run('spctl', ['--assess', '--type', 'execute', '--verbose=4', app]);
      builder(['dmg', 'zip', '--prepackaged', app]);
      const dmg = join(root, 'wuu-mac-arm64.dmg');
      run('codesign', ['--force', '--sign', releaseSigningIdentity(), ...(process.env.WUU_RELEASE_KEYCHAIN ? ['--keychain', process.env.WUU_RELEASE_KEYCHAIN] : []), '--timestamp', dmg]);
      copyFileSync(dmg, join(root, 'signed-dmg.dmg'));
      const files = readdirSync(root).filter(name => /^(wuu-mac-arm64\.(dmg|zip|zip\.blockmap)|signed-dmg\.dmg|.+-mac\.yml)$/.test(name));
      saveCheckpoint(root, identity('distribution'), files);
      break;
    }
    case 'verify':
      verifyCheckpoint(root, identity('distribution'));
      run(process.execPath, [script('verify-mac-artifacts.cjs')]);
      run('unzip', ['-t', join(root, 'wuu-mac-arm64.zip')]);
      break;
    default: throw new Error('Expected sign-app, submit-app, package, submit-dmg or verify');
  }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
