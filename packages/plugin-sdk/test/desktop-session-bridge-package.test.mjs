import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {copyFile, mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';
import {test} from 'node:test';
import {preparePublicSDK} from './public-package.mjs';

const exec = promisify(execFile);
test('external desktop session provider compiles and forwards host tickets through only the packed public SDK', {timeout: 60_000}, async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'wuu-desktop-session-provider-'));
  try {
    const sdk = fileURLToPath(new URL('../', import.meta.url));
    const {tsc, project} = await preparePublicSDK({sdk, temporary});
    await copyFile(new URL('./fixtures/desktop-session-provider.ts', import.meta.url), join(project, 'provider.ts'));
    await exec(process.execPath, [tsc, 'provider.ts', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2022', '--strict'], {cwd: project})
      .catch(error => { throw new Error(error.stdout || error.message); });
    const {externalDesktopProvider} = await import(pathToFileURL(join(project, 'provider.js')).href);
    const issued = {version: 1, operation: 'inspect', operation_id: 'host-intent', locator: {version: 1, execution_host: 'desktop-project:registered', session_id: 'existing'}, params: {session_id: 'existing'}, transport_ticket: {id: 'host-issued-ticket', source_host: 'source-generation'}};
    const receipt = {version: 1, operation_id: issued.operation_id, locator: issued.locator, status: 'completed', result: {session: {session_id: 'existing'}}};
    const calls = [];
    const provider = externalDesktopProvider({supports: () => true, call: async (method, params) => { calls.push({method, params}); return receipt; }});
    assert.deepEqual(await provider.dispatch(issued), receipt);
    assert.equal(provider.provided[0].name, 'session.transport');
    assert.deepEqual(provider.required, [{name: 'host.desktop.session', major_version: 1, required: true}]);
    assert.deepEqual(calls, [{method: 'host.service.call', params: {service: 'host.desktop.session', method: 'dispatch', params: {ticket_id: issued.transport_ticket.id}}}]);
    assert.equal(calls[0].params.params.request, undefined, 'provider cannot replace host-authenticated original material');
  } finally { await rm(temporary, {recursive: true, force: true}); }
});
