import test from 'node:test';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {copyFile, mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {preparePublicSDK} from './public-package.mjs';

const exec = promisify(execFile);
const sdk = fileURLToPath(new URL('../', import.meta.url));
test('conversation addon compiles outside the checkout with only the packed public SDK', async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'wuu-conversation-addon-'));
  try {
    const {project, tsc} = await preparePublicSDK({sdk, temporary});
    await copyFile(new URL('../../../examples/plugins/conversation-metrics/desktop.js', import.meta.url), join(project, 'desktop.js'));
    await exec(process.execPath, [tsc, '--allowJs', '--checkJs', '--noEmit', '--strict', '--skipLibCheck',
      '--target', 'es2022', '--module', 'nodenext', '--moduleResolution', 'nodenext', 'desktop.js'], {cwd: project});
  } finally { await rm(temporary, {recursive: true, force: true}); }
});
