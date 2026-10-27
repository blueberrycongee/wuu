// Run with Node. Exercises real Chromium downloads and a killed Electron process.
// Contracts: finished files survive restart, in-flight work becomes honestly
// interrupted, another window cannot control live work, and missing files fail.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_DOWNLOAD_RECOVERY_OUTPUT || path.join(root, 'out/e2e/browser-download-recovery'));
const electron = process.versions.electron ? require('electron') : process.env.WUU_DOWNLOAD_RECOVERY_ELECTRON || require('electron');
if (typeof electron === 'string') {
  fs.mkdirSync(output, { recursive: true });
  fs.rmSync(path.join(output, 'report.json'), { force: true });
  fs.rmSync(path.join(output, 'failure.txt'), { force: true });
  require('esbuild').buildSync({ entryPoints: [path.join(root, 'src/main/webWorkspaceDownloads.ts')], outfile: path.join(output, 'downloads.cjs'), bundle: true, platform: 'node', format: 'cjs', external: ['electron'] });
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-download-recovery-'));
  try {
    for (const phase of ['download', 'recover']) {
      const result = spawnSync(process.env.WUU_DOWNLOAD_RECOVERY_ELECTRON || electron, [...(process.platform === 'linux' ? ['--no-sandbox'] : []), __filename], { encoding: 'utf8', env: { ...process.env, WUU_DOWNLOAD_RECOVERY_OUTPUT: output, WUU_DOWNLOAD_RECOVERY_FIXTURE: fixture, WUU_DOWNLOAD_RECOVERY_PHASE: phase } });
      fs.writeFileSync(path.join(output, `${phase}.log`), result.stdout + result.stderr);
      process.stdout.write(result.stdout); process.stderr.write(result.stderr);
      if (phase === 'download') assert.equal(result.signal, 'SIGKILL', 'fixture must end through an actual abrupt termination');
      else { assert.equal(result.status, 0, 'recovery phase must pass'); assert.equal(JSON.parse(fs.readFileSync(path.join(output, 'report.json'), 'utf8')).status, 'passed'); }
    }
  } finally { fs.rmSync(fixture, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }); }
} else {
  const { app, BrowserWindow } = electron;
  const fixture = process.env.WUU_DOWNLOAD_RECOVERY_FIXTURE;
  app.on('window-all-closed', () => {});
  app.setPath('userData', path.join(fixture, 'profile'));
  const deadline = setTimeout(() => { console.error('Download recovery E2E timed out'); app.exit(1); }, 30000);
  const until = async (predicate, message) => {
    for (let i = 0; i < 200; i++) { if (await predicate()) return; await new Promise(resolve => setTimeout(resolve, 25)); }
    throw new Error(message);
  };
  (async () => {
    await app.whenReady();
    const { WebWorkspaceDownloads } = require(path.join(output, 'downloads.cjs'));
    const historyPath = path.join(fixture, 'downloads.json');
    const downloads = new WebWorkspaceDownloads(() => {}, historyPath);
    const owner = new BrowserWindow({ show: false, webPreferences: { sandbox: true } });
    if (process.env.WUU_DOWNLOAD_RECOVERY_PHASE === 'download') {
      const http = require('node:http');
      const payload = Buffer.from('Wuu real browser download recovery fixture\n');
      const server = http.createServer((req, res) => {
        res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Disposition': `attachment; filename="${req.url === '/complete' ? 'completed.txt' : 'pending.bin'}"`, 'Content-Length': req.url === '/complete' ? payload.length : 1024 * 1024 });
        if (req.url === '/complete') res.end(payload);
        else { res.write(Buffer.alloc(1024)); }
      });
      await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
      const origin = `http://127.0.0.1:${server.address().port}`;
      owner.webContents.session.on('will-download', (_event, item) => {
        downloads.start(owner, 'ribbon-browser', item);
        item.setSavePath(path.join(fixture, item.getFilename()));
      });
      owner.webContents.downloadURL(origin + '/complete');
      await until(() => downloads.list(owner).some(item => item.state === 'completed'), 'completed download was not observable');
      assert.equal(fs.readFileSync(path.join(fixture, 'completed.txt'), 'utf8'), payload.toString());
      const closing = new BrowserWindow({ show: false });
      owner.destroy();
      assert.equal(downloads.list(closing).filter(item => item.state === 'completed').length, 1, 'finished records survive closing the originating window');
      // The actual pending transfer belongs to a fresh window after closure.
      closing.webContents.session.removeAllListeners('will-download');
      closing.webContents.session.on('will-download', (_event, item) => {
        downloads.start(closing, 'ribbon-browser', item);
        item.setSavePath(path.join(fixture, item.getFilename()));
      });
      closing.webContents.downloadURL(origin + '/pending');
      await until(() => downloads.list(closing).some(item => !item.done && item.receivedBytes > 0), 'pending download did not transfer bytes');
      const pending = downloads.list(closing).find(item => !item.done);
      const stranger = new BrowserWindow({ show: false });
      assert.equal(downloads.list(stranger).some(item => item.id === pending.id), false);
      await assert.rejects(downloads.command(stranger, pending.id, 'cancel'), /no longer available/);
      await downloads.command(closing, pending.id, 'pause');
      await until(() => {
        if (!fs.existsSync(historyPath)) return false;
        return JSON.parse(fs.readFileSync(historyPath, 'utf8')).downloads.some(item => item.id === pending.id && item.receivedBytes > 0);
      }, 'active download checkpoint was not persisted');
      console.log('PASS: real completed file, live window isolation, paused progress checkpoint');
      process.kill(process.pid, 'SIGKILL');
      return;
    }
    const originalHistory = fs.readFileSync(historyPath, 'utf8');
    new WebWorkspaceDownloads(() => {}, historyPath).destroyAll();
    assert.equal(fs.readFileSync(historyPath, 'utf8'), originalHistory, 'destroying an unopened manager must not overwrite history');
    const recovered = downloads.list(owner);
    assert.equal(recovered.length, 2);
    const completed = recovered.find(item => item.state === 'completed');
    const interrupted = recovered.find(item => item.state === 'interrupted');
    assert.ok(completed?.done && completed.savePath.endsWith('completed.txt'));
    assert.ok(interrupted?.done && interrupted.receivedBytes > 0 && !interrupted.paused && !interrupted.canResume);
    await assert.rejects(downloads.command(owner, interrupted.id, 'resume'), /cannot resume/);
    fs.unlinkSync(completed.savePath);
    await assert.rejects(downloads.command(owner, completed.id, 'open'), /moved or removed/);
    owner.destroy();
    const replacement = new BrowserWindow({ show: false });
    assert.equal(downloads.list(replacement).length, 2, 'closing a window retains download history');
    const corruptPath = path.join(fixture, 'corrupt.json');
    fs.writeFileSync(corruptPath, '{unreadable');
    const corrupt = new WebWorkspaceDownloads(() => {}, corruptPath);
    assert.throws(() => corrupt.list(replacement), /download history/i);
    assert.equal(fs.readFileSync(corruptPath, 'utf8'), '{unreadable');
    const server = require('node:http').createServer((_req, res) => {
      res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Length': 1024 * 1024 });
      res.write(Buffer.alloc(1024));
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    let manager = corrupt;
    replacement.webContents.session.on('will-download', (_event, item) => {
      manager.start(replacement, 'ribbon-browser', item);
      item.setSavePath(path.join(fixture, 'new-download.bin'));
    });
    replacement.webContents.downloadURL(origin + '/corrupt');
    await until(() => {
      try { return corrupt.list(replacement).some(item => item.receivedBytes > 0); } catch { return false; }
    }, 'new download cannot be listed with corrupt history');
    const fresh = corrupt.list(replacement)[0];
    assert.equal(fresh.persistenceError, 'read');
    await corrupt.command(replacement, fresh.id, 'pause');
    assert.equal(corrupt.list(replacement)[0].paused, true);
    await corrupt.command(replacement, fresh.id, 'cancel');
    assert.equal(corrupt.list(replacement)[0].state, 'cancelled');
    assert.equal(fs.readFileSync(corruptPath, 'utf8'), '{unreadable');
    const blockedDirectory = path.join(fixture, 'blocked');
    fs.mkdirSync(blockedDirectory);
    manager = new WebWorkspaceDownloads(() => {}, path.join(blockedDirectory, 'downloads.json'));
    assert.deepEqual(manager.list(replacement), []);
    fs.rmdirSync(blockedDirectory);
    fs.writeFileSync(blockedDirectory, 'temporary obstruction');
    replacement.webContents.downloadURL(origin + '/write-error');
    await until(() => manager.list(replacement).some(item => item.receivedBytes > 0), 'download cannot continue during history write error');
    const unsaved = manager.list(replacement)[0];
    assert.equal(unsaved.persistenceError, 'write');
    await manager.command(replacement, unsaved.id, 'pause');
    assert.equal(manager.list(replacement)[0].paused, true);
    fs.unlinkSync(blockedDirectory); fs.mkdirSync(blockedDirectory);
    await manager.command(replacement, unsaved.id, 'resume');
    assert.equal(manager.list(replacement)[0].persistenceError, undefined);
    await manager.command(replacement, unsaved.id, 'cancel');
    assert.equal(JSON.parse(fs.readFileSync(path.join(blockedDirectory, 'downloads.json'), 'utf8')).downloads[0].state, 'cancelled');
    corrupt.destroyAll(); manager.destroyAll(); server.close();
    fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ status: 'passed', platform: process.platform, electron: process.versions.electron, checks: ['actual Chromium download', 'live window ownership', 'actual process kill and restart', 'completed history restored', 'in-flight download marked interrupted', 'resume unavailable after restart', 'deleted file visible error', 'history survives window closure', 'corrupt history preserved and surfaced', 'new downloads manageable with corrupt history', 'write failure visible without cancelling transfers', 'write error clears after storage recovery', 'unopened manager shutdown preserves history'] }, null, 2));
    console.log('PASS: restart recovery, honest interrupted state, missing-file error, window closure, corrupt history');
    downloads.destroyAll(); replacement.destroy(); clearTimeout(deadline); app.exit(0);
  })().catch(error => { console.error(error); fs.writeFileSync(path.join(output, 'failure.txt'), String(error.stack)); app.exit(1); });
}
