// Optional, synthetic-only startup diagnostics. Never imported by product code.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const { createHash } = require('node:crypto');
const { pathToFileURL } = require('node:url');
const { StringDecoder } = require('node:string_decoder');

module.exports = function createStartupProbe(entryNs) {
  const { app, ipcMain, screen } = require('electron');
  const stages = [];
  const cores = [];
  const syncIPC = [];
  const initialization = [];
  const launchNs = BigInt(process.env.WUU_STARTUP_LAUNCH_NS);
  const launchMs = () => Number(process.hrtime.bigint() - launchNs) / 1e6;
  const mark = (stage, fields = {}) => stages.push({ stage, launchMs: launchMs(), ...fields });
  stages.push({ stage: 'harness-entry', launchMs: Number(entryNs - launchNs) / 1e6 });
  app.once('ready', () => mark('electron-app-ready'));
  const hash = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  function linuxProcess(pid) {
    if (process.platform !== 'linux') return { pid, unsupported: true };
    try {
      const stat = fs.readFileSync(`/proc/${pid}/stat`, 'utf8');
      const fields = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
      const io = Object.fromEntries(fs.readFileSync(`/proc/${pid}/io`, 'utf8').trim().split('\n').map(line => { const [key, value] = line.split(':'); return [key, Number(value.trim())]; }));
      let children = [];
      let childrenUnavailable;
      try { children = fs.readFileSync(`/proc/${pid}/task/${pid}/children`, 'utf8').trim().split(/\s+/).filter(Boolean).map(Number); } catch (error) { childrenUnavailable = error.code; }
      return { pid, childrenUnavailable, userTicks: Number(fields[11]), systemTicks: Number(fields[12]), rssPages: Number(fields[21]), io, children };
    } catch (error) { return { pid, unavailable: error.code }; }
  }
  function observeCore(child, args, options) {
    const workdirArg = args.indexOf('--workdir');
    const workdir = workdirArg < 0 ? options?.cwd : args[workdirArg + 1];
    const record = { pid: child.pid, workdir, environment: Object.fromEntries(Object.entries(options?.env || {}).filter(([key]) => key.endsWith('_PLUGIN_HELPER') || ['WUU_NODE_EXECUTABLE', 'WUU_SOURCE_ROOT', 'WUU_SAFE_MODE', 'ELECTRON_RUN_AS_NODE'].includes(key))), spawnMs: launchMs(), firstStdoutMs: null, bytes: 0, requests: [], before: linuxProcess(child.pid) };
    cores.push(record);
    mark('core-spawn', { pid: child.pid, workdir });
    const write = child.stdin.write.bind(child.stdin);
    child.stdin.write = (chunk, ...rest) => {
      try {
        const request = JSON.parse(String(chunk));
        if (request.method && request.id !== undefined) record.requests.push({ id: String(request.id), method: request.method, sentMs: launchMs(), bytes: Buffer.byteLength(chunk), responseMs: null });
      } catch {}
      return write(chunk, ...rest);
    };
    // Keep only the response envelope prefix; never duplicate large history JSON.
    let prefix = '';
    let lineBytes = 0;
    const decoder = new StringDecoder('utf8');
    child.stdout.on('data', chunk => {
      const data = typeof chunk === 'string' ? chunk : decoder.write(chunk);
      record.bytes += typeof chunk === 'string' ? Buffer.byteLength(chunk) : chunk.length;
      if (record.firstStdoutMs === null) { record.firstStdoutMs = launchMs(); mark('core-first-stdout', { pid: child.pid }); }
      let offset = 0;
      while (offset < data.length) {
        const newline = data.indexOf('\n', offset);
        const end = newline < 0 ? data.length : newline;
        prefix += data.slice(offset, Math.min(end, offset + Math.max(0, 512 - prefix.length)));
        lineBytes += Buffer.byteLength(data.slice(offset, end));
        if (newline < 0) break;
        const boundary = prefix.search(/"(?:result|error)"\s*:/);
        const match = boundary < 0 ? null : prefix.slice(0, boundary).match(/"id"\s*:\s*("[^"]*"|[0-9]+)/);
        if (match) {
          const request = record.requests.find(request => request.id === String(JSON.parse(match[1])) && request.responseMs === null);
          if (request) { request.responseMs = launchMs(); request.responseBytes = lineBytes; }
        }
        prefix = ''; lineBytes = 0; offset = newline + 1;
      }
    });
  }

  const channels = new Set(['wuu:theme-preference-get-sync', 'wuu:language-preference-get-sync', 'wuu:onboarding-complete-get-sync', 'wuu:message-flow-font-size-get-sync', 'wuu:pop-out-init']);
  const on = ipcMain.on.bind(ipcMain);
  ipcMain.on = (channel, listener) => {
    if (!channels.has(channel)) return on(channel, listener);
    return on(channel, (...args) => {
      const start = performance.now();
      const timing = { channel, launchMs: launchMs(), ms: null };
      syncIPC.push(timing);
      try { return listener(...args); } finally { timing.ms = performance.now() - start; }
    });
  };
  return {
    mark, launchMs, observeCore,
    initialized(result) {
      initialization.push({ workspace: result.workspace_root, status: result.status,
        extensions: result.extension_inventory?.map(({ id, state, enabled, kind }) => ({ id, state, enabled, kind })) });
    },
    observeWindow(win) {
      mark('window-created', { windowId: win.id });
      for (const name of ['show', 'ready-to-show', 'focus']) win.on(name, () => mark('window-' + name, { windowId: win.id }));
      for (const name of ['did-start-loading', 'dom-ready', 'did-finish-load']) win.webContents.on(name, () => mark(name, { windowId: win.id }));
      win.webContents.debugger.attach('1.3');
      // Attachment does not suspend initial navigation. Early parse/evaluation is
      // uncovered; retrospective PaintTiming uses the renderer navigation clock.
      void win.webContents.debugger.sendCommand('Performance.enable').then(() => mark('renderer-metrics-enabled'));
    },
    async verifyDisplay(win, evaluate) {
      const viewport = await evaluate(win, () => ({ width: innerWidth, height: innerHeight, scale: devicePixelRatio, visibility: document.visibilityState }));
      assert.ok(screen.getPrimaryDisplay().bounds.width > 0 && viewport.width > 0 && viewport.height > 0,
        'Nonzero display/viewport is a startup rig precondition, not a latency sample');
      mark('viewport-verified', { viewport });
    },
    async finish({ win, evaluate, output, fixture, projects, selectedIndex, turns, sidebarThreads, safeMode, mainBundle, harness, timings, startup }) {
      mark('typed-composer-ready');
      assert.equal(win.webContents.getURL(), pathToFileURL(path.resolve(path.dirname(mainBundle), '../renderer/index.html')).href,
        'Loaded renderer must match the selected application artifact');
      const inputProof = await evaluate(win, () => window.__startupInput);
      assert.ok(inputProof?.trusted && inputProof.inputType === 'insertText' && !inputProof.disabled && !inputProof.readOnly && !inputProof.inert,
        'Startup input was not accepted by the editable composer');
      const renderer = await evaluate(win, () => ({ width: innerWidth, height: innerHeight, devicePixelRatio,
        timeOrigin: performance.timeOrigin, now: performance.now(), visibility: document.visibilityState,
        navigation: performance.getEntriesByType('navigation').map(entry => entry.toJSON()),
        paints: performance.getEntriesByType('paint').map(entry => entry.toJSON()) }));
      const rendererMetrics = await win.webContents.debugger.sendCommand('Performance.getMetrics');
      const coreProcesses = cores.map(core => ({ ...core, role: core.workdir === projects[selectedIndex]?.path ? 'active' : 'background', after: linuxProcess(core.pid) }));
      const assets = path.resolve(path.dirname(mainBundle), '../renderer/assets');
      const evidence = {
        scope: 'Fresh Electron process/profile and prepared synthetic WUU_HOME; no OS cache flushing. Main/core launchMs starts before parent spawn. Renderer paint entries use their own navigation clock. Main RPC and core wire envelopes overlap. Two visible animation frames do not prove physical presentation.',
        coverage: 'Renderer CDP counters begin at renderer-metrics-enabled; earlier parse/evaluation and first React commit are not measured. Sync IPC records main handler time only, excluding cross-process roundtrip. Helper CPU/IO unavailable when childrenUnavailable is set; absent helpers are not zero work.',
        buildKind: process.env.WUU_SWITCH_BUILD_KIND || 'production-vite',
        sourceCommit: process.env.WUU_SWITCH_BUILD_COMMIT, coreSourceCommit: process.env.WUU_SWITCH_CORE_BUILD_COMMIT, coreSourcePatchSha256: process.env.WUU_SWITCH_CORE_PATCH_SHA256 || null,
        mainSha256: hash(mainBundle), coreSha256: hash(process.env.WUU_DESKTOP_CORE), harnessSha256: hash(harness), probeSha256: hash(__filename),
        preloadSha256: hash(path.resolve(path.dirname(mainBundle), '../preload/index.cjs')),
        rendererAssets: Object.fromEntries(fs.readdirSync(assets).sort().map(name => [name, hash(path.join(assets, name))])),
        helpers: Object.fromEntries(Object.entries(process.env).filter(([key]) => key.endsWith('_PLUGIN_HELPER')).map(([key, file]) => [key, { file, sha256: hash(file) }])),
        selectedIndex, turns, sidebarThreads, safeMode, fixture, inputProof, startup, stages, ipc: timings, syncIPC, initialization,
        displays: screen.getAllDisplays().map(({ bounds, workArea, scaleFactor }) => ({ bounds, workArea, scaleFactor })),
        gpuFeatureStatus: app.getGPUFeatureStatus(), windowVisible: win.isVisible(), windowSize: win.getSize(), zoomFactor: win.webContents.getZoomFactor(),
        renderer, rendererMetrics, coreProcesses,
        mainProcess: { ...linuxProcess(process.pid), cpu: process.cpuUsage(), resources: process.resourceUsage() },
        electronProcesses: app.getAppMetrics(), versions: process.versions,
        environment: { platform: process.platform, arch: process.arch, osRelease: os.release(), cpu: os.cpus()[0].model,
          cpuCount: os.cpus().length, hostLoadAverage: os.loadavg(), totalMemoryBytes: os.totalmem(), freeMemoryBytes: os.freemem() },
      };
      fs.writeFileSync(path.join(output, 'startup-results.json'), JSON.stringify(evidence, null, 2));
      fs.writeFileSync(path.join(output, 'startup.png'), (await win.webContents.capturePage()).toPNG());
      console.log('STARTUP_COMPLETE', JSON.stringify({ launchToTypeableMs: stages.find(stage => stage.stage === 'typed-composer-ready').launchMs }));
    },
  };
};
