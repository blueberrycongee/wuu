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

// Protect the external author boundary: missing exports/packed files, leaked
// private types, incompatible callback generics or sync/async factory results,
// and accepting an unsupported version or inline credential in typed config.
test('desktop connector compiles independently against only the packed public SDK', {timeout: 60_000}, async () => {
  const temporary = await mkdtemp(join(tmpdir(), 'wuu-desktop-connector-'));
  try {
    const sdk = fileURLToPath(new URL('../', import.meta.url));
    const {tsc, project} = await preparePublicSDK({sdk, temporary});
    await copyFile(new URL('./fixtures/desktop-session-connector.ts', import.meta.url), join(project, 'connector.ts'));
    await exec(process.execPath, [tsc, 'connector.ts', '--module', 'NodeNext', '--moduleResolution', 'NodeNext', '--target', 'ES2022', '--strict'], {cwd: project})
      .catch(error => { throw new Error(error.stdout || error.message); });
    const {connector, asyncConnector, routes, context} = await import(pathToFileURL(join(project, 'connector.js')).href);
    const {isDesktopSessionConnectionV1} = await import(pathToFileURL(join(project, 'node_modules/@wuu/plugin-sdk/dist/index.js')).href);
    const events = [];
    const hooks = {
      onAttach: event => events.push(event), onDetach: () => events.push('detach'),
      onState: state => events.push(state), onNotification: () => events.push('notification'),
      onServerRequest: async () => ({error: {code: 'permission_denied', message: 'synthetic fixture'}}),
    };
    const options = {version: 1, route: routes.select(context), hooks};
    const connection = connector.connectDesktopSession(options);
    assert.ok(isDesktopSessionConnectionV1(connection));
    assert.ok(isDesktopSessionConnectionV1(await asyncConnector(options)));
    for (const invalid of [null, [], {}, {...connection, start: 1}]) assert.equal(isDesktopSessionConnectionV1(invalid), false);
    connection.start();
    await connection.waitAttached(100);
    const rpc = connection.rpc();
    assert.equal(rpc.isClosed(), false);
    const result = await rpc.call('thread/read', {thread_id: 'session'}, 100, options.route.workdir, () => events.push('response'));
    assert.equal(result.workdir, '/remote/workspace');
    assert.deepEqual(events.slice(-2), ['response', 'notification']);
    await assert.rejects(rpc.call('not-supported'), {kind: 'app-server-response', code: 'method_not_found'});
    await connection.stop();
    assert.equal(rpc.isClosed(), true);
    assert.equal(connection.rpc(), null);
  } finally {
    await rm(temporary, {recursive: true, force: true});
  }
});
