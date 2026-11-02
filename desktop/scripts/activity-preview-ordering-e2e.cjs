// macOS native ordering diagnostic: baseline and candidate presentation policies.
// Run: electron scripts/activity-preview-ordering-e2e.cjs
// This checks native policy behavior, not the production ActivityPiPSurface.
// No user profile, network, screenshots, or screen-recording permission needed.
// Failure cases: show steals key focus; host z-order crosses the external app;
// reattachment lifts the host; repeated presentation loses native ownership.
const { app, BrowserWindow } = require('electron');
const { execFileSync, spawn } = require('node:child_process');
const { mkdtempSync, writeFileSync, rmSync, mkdirSync, readFileSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const delay = ms => new Promise(r => setTimeout(r, ms));
const assert = require('node:assert/strict');
const reportDirectory = process.env.WUU_ACTIVITY_ORDERING_REPORT_DIR || resolve(__dirname, '../artifacts/activity-preview-ordering');
mkdirSync(reportDirectory, { recursive: true });
const report = { electron: process.versions.electron, platform: process.platform, productionSurfaceTested: false, samples: [], status: 'running' };
const saveReport = () => writeFileSync(join(reportDirectory, 'report.json'), JSON.stringify(report, null, 2));
const directory = mkdtempSync(join(tmpdir(), 'wuu-focus-probe-'));
mkdirSync(join(directory, 'profile'));
app.setPath('userData', join(directory, 'profile'));
const timeout = setTimeout(() => { report.status = 'failed'; report.error = 'Probe timed out'; saveReport(); cleanup(); app.exit(1); }, 90000);
function cleanup() { clearTimeout(timeout); if (coverProcess) coverProcess.kill(); rmSync(directory, { recursive: true, force: true }); }
let coverProcess;
app.whenReady().then(async () => {
  if (process.platform !== 'darwin') throw new Error('macOS acceptance probe only');
  const source = join(directory, 'observe.swift');
  const helper = join(directory, 'observe');
  writeFileSync(source, readFileSync(join(__dirname, "fixtures/activity-ordering.swift")));
  execFileSync('/usr/bin/xcrun', ['swiftc', source, '-o', helper], { stdio: 'inherit' });
  const main = new BrowserWindow({ width: 700, height: 480, title: 'Synthetic Wuu host', show: false });
  let panel;
  await main.loadURL('data:text/html,<body style="background:%23dcecff;font:24px sans-serif">Synthetic Wuu host. The synthetic green external-app window should remain over this blue host.</body>');
  const observe = label => {
    const native = JSON.parse(execFileSync(helper, [String(process.pid), String(coverProcess.pid)], { encoding: 'utf8' }));
    const sample = { label, hostID: Number(main.getMediaSourceId().split(":")[1]), panelID: Number(panel.getMediaSourceId().split(":")[1]), nativeParentIsHost: panel.getParentWindow() === main, coverPID: coverProcess?.pid, time: new Date().toISOString(), mainFocused: main.isFocused(), panelFocused: panel.isFocused(), mainVisible: main.isVisible(), panelVisible: panel.isVisible(), ...native };
    report.samples.push(sample); saveReport(); console.log(JSON.stringify(sample));
    assert.ok(sample.probeWindows.some(window => window.id === Number(main.getMediaSourceId().split(':')[1])), 'Host native window identity must be observable');
    if (sample.panelVisible) assert.ok(sample.probeWindows.some(window => window.id === Number(panel.getMediaSourceId().split(':')[1])), 'Visible preview native window identity must be observable');
    return sample;
  };
  const assertBackground = sample => {
    assert.equal(sample.frontPID, coverProcess.pid, 'Preview must leave the external app foreground');
    assert.equal(sample.mainFocused, false, 'Preview must not focus its host');
    assert.equal(sample.panelFocused, false, 'Passive preview must not become key');
    assert.ok(sample.coverNormalWindowZIndex >= 0, 'External test app must have an on-screen normal window');
    const hostID = Number(main.getMediaSourceId().split(':')[1]);
    const host = sample.probeWindows.find(window => window.id === hostID);
    assert.ok(host, 'Synthetic host must remain present in CGWindowList');
    assert.ok(host.zIndex > sample.coverNormalWindowZIndex, 'Host must remain behind the external test app');
  };
  for (const mode of ['native-child', 'independent-panel', 'detach-show-reattach', 'detach-show-order-reattach', 'detach-show-level-order-reattach']) {
    const attached = mode !== 'independent-panel';
    const detached = mode.startsWith('detach-');
    const candidate = mode === 'detach-show-level-order-reattach';
    const relative = mode.includes('-order-');
    panel = new BrowserWindow({ width: 260, height: 170, x: 850, y: 550, frame: false,
      title: 'Synthetic activity preview', type: 'panel', show: false, acceptFirstMouse: true,
      resizable: false, minimizable: false, maximizable: false, fullscreenable: false });
    await panel.loadURL('data:text/html,<body style="background:%23fee4b0;font:20px sans-serif">Synthetic live preview</body>');

    panel.hide();
    panel.setParentWindow(attached ? main : null);
    main.show(); main.focus();
    if (coverProcess) { coverProcess.kill(); await delay(300); }
    coverProcess = spawn(helper, ['--cover'], { stdio: 'ignore' });
    console.log(`MODE ${mode}: synthetic green app foreground. Do not interact during the probe.`);
    const deadline = Date.now() + 10000;
    while (JSON.parse(execFileSync(helper, [String(process.pid), String(coverProcess.pid)], { encoding: 'utf8' })).frontPID !== coverProcess.pid) {
      if (Date.now() >= deadline) throw new Error('Synthetic external app did not become foreground; result would be invalid');
      await delay(100);
    }
    assertBackground(observe('baseline-' + mode));
    if (detached) panel.setParentWindow(null);
    panel.showInactive();
    await delay(1000); observe('after-showInactive-' + mode);
    if (detached) {
      if (relative) {
        if (candidate) panel.setAlwaysOnTop(false);
        panel.moveAbove(main.getMediaSourceId());
        await delay(1000); const ordered = observe('after-relative-order-' + mode);
        if (candidate) assertBackground(ordered);
      }
      panel.setParentWindow(main);
      await delay(1000); const reattached = observe('after-reattach-' + mode);
      if (candidate) assertBackground(reattached);
      assert.equal(panel.getParentWindow(), main, 'Restore native ownership after presentation');
    }
    panel.moveTop();
    await delay(1000); observe('after-moveTop-' + mode);
    for (let i = 0; i < 4; i++) {
      await panel.webContents.executeJavaScript(`document.body.textContent='Frame update ${i}'`);
      await delay(300);
    }
    observe('after-passive-updates-' + mode);
    panel.hide(); await delay(300);
    if (detached) panel.setParentWindow(null);
    panel.showInactive();
    if (candidate) panel.setAlwaysOnTop(false);
    if (relative) panel.moveAbove(main.getMediaSourceId());
    if (detached) panel.setParentWindow(main);
    await delay(1000); observe('after-reshow-' + mode);
    if (candidate) {
      for (const sample of report.samples.filter(sample => sample.label.endsWith(mode))) assertBackground(sample);
      assert.equal(panel.getParentWindow(), main, 'Repeated presentation preserves native ownership');
      assert.equal(panel.isVisible(), true, 'An unfocused visible host still presents its preview');
      const final = report.samples.at(-1);
      assert.equal(final.probeWindows.find(window => window.id === final.panelID).layer, final.probeWindows.find(window => window.id === final.hostID).layer, 'Native child preserves the host window level');
      main.show(); main.focus();
      const focusedDeadline = Date.now() + 5000;
      while (!main.isFocused()) {
        if (Date.now() >= focusedDeadline) throw new Error('Explicit host focus did not complete');
        await delay(50);
      }
      panel.moveTop();
      const foreground = observe('explicit-host-focus-' + mode);
      assert.equal(foreground.frontPID, process.pid, 'Explicit host focus still activates Wuu');
      assert.equal(panel.isVisible(), true, 'Preview remains visible with the foreground host');
    }
    if (mode === 'native-child') {
      report.baselineReproduced = report.samples.filter(sample => sample.label.endsWith(mode)).some(sample =>
        sample.frontPID !== sample.coverPID || sample.mainFocused || sample.panelFocused
        || sample.probeWindows.find(window => window.id === sample.hostID).zIndex < sample.coverNormalWindowZIndex);
      saveReport();
    }
    panel.destroy();
    await delay(500);
  }
  console.log('DONE. Compare frontPID and host normal-layer zIndex before/after each operation. A changed visual z-order can occur without a focus change.');
  report.status = 'passed'; saveReport();
  main.destroy(); app.quit();
}).catch(error => { console.error(error); report.status = 'failed'; report.error = String(error.stack || error); saveReport(); cleanup(); app.exit(1); });
app.on('will-quit', cleanup);
