// Repro: switching a session across projects -> left ribbon flicker.
// Seeds two workspaces with one session each, then clicks the other
// workspace's session row while sampling the ribbon strip per frame and
// composited pixels at ribbon coordinates. Reports every anomalous frame.
const { app, BrowserWindow } = require('electron');
const assert = require('node:assert/strict');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const fs = require('node:fs');
const { pathToFileURL } = require('node:url');

const desktop = path.resolve(__dirname, '..');
const run = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-ribbon-flicker-'));
const output = process.env.WUU_FLICKER_OUTPUT || path.join(run, 'artifacts');
const home = path.join(run, 'home');
const workspaceA = path.join(run, 'workspace-a');
const workspaceB = path.join(run, 'workspace-b');
const core = path.resolve(process.env.WUU_SHELL_SKIN_CORE || path.join(desktop, 'build/bin/wuu-core'));
for (const dir of [output, home, workspaceA, workspaceB]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(run, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: core,
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;

const WIDTH = 1440, HEIGHT = 960;
const REPLY = 'Session flicker fixture reply.';
const report = { status: 'running', output, fixture: run, checks: [], errors: [], transitions: [] };
let phase = 'setup', win;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const bounded = (p, label, ms = 15000) => Promise.race([p, sleep(ms).then(() => { throw new Error(`Timed out: ${label}`); })]);
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await bounded(Promise.resolve().then(check), label, 8000)) return; await sleep(50); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, arg) => bounded(win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`), 'renderer');
const save = () => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ ...report, phase }, null, 2));

const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', part => { raw += part; }); req.on('end', () => {
    if (req.method === 'GET' || req.url.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ data: [{ id: 'fixture', object: 'model' }] })); return;
    }
    const body = JSON.parse(raw || '{}');
    const content = (body.messages || []).some(m => (m.content || '').includes?.('Generate a title')) ? 'Flicker fixture conversation' : REPLY;
    if (!body.stream) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ choices: [{ message: { role: 'assistant', content }, finish_reason: 'stop' }] })); return;
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write(`data: ${JSON.stringify({ id: 'f', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta: { role: 'assistant', content }, finish_reason: null }] })}\n\n`);
    res.write(`data: ${JSON.stringify({ id: 'f', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`);
    res.end('data: [DONE]\n\n');
  });
});

app.on('browser-window-created', (_e, created) => {
  if (win) return;
  win = created; created.hide();
  created.webContents.setBackgroundThrottling(false);
  created.webContents.on('console-message', event => {
    if (event.level === 'error') report.errors.push({ phase, message: event.message });
  });
});

async function shellReady() {
  await until(() => evaluate(() => Boolean(document.querySelector('[data-ribbon-id]') && document.querySelector('.primary-nav .nav-item'))), 'navigation shell', 60000);
}
const composer = '[data-main-conversation-composer] textarea';
async function sendTurn(text) {
  await until(() => evaluate(sel => { const i = document.querySelector(sel); return Boolean(i && !i.disabled && !i.readOnly && !i.closest('[inert]')); }, composer), 'editable composer', 60000);
  await evaluate(({ selector, text }) => { const i = document.querySelector(selector);
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(i, text);
    i.dispatchEvent(new Event('input', { bubbles: true })); }, { selector: composer, text });
  await until(() => evaluate(() => { const b = document.querySelector('[data-main-conversation-composer] .composer-send-button'); return Boolean(b && !b.disabled); }), 'send enabled');
  await evaluate(sel => document.querySelector(sel).dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })), composer);
}
// Wait until the shell has settled onto the given workspace's active context.
async function waitActiveWorkspace(root) {
  await until(async () => {
    const init = await evaluate(() => window.wuu.initialize());
    return typeof init?.workspace_root === 'string' && init.workspace_root.endsWith(path.basename(root));
  }, `runtime context ${root}`, 60000);
  await until(() => evaluate(() =>
    Boolean(document.querySelector('.sidebar-ribbon'))
    && !document.querySelector('.view-switch-loading-conversation')
    && !document.querySelector('.runtime-loading, .surface-unavailable')), 'shell settled', 60000);
}

// Per-frame instrumentation inside the renderer, focused on the ribbon.
async function installSampler() {
  return evaluate(() => {
    const frames = window.__frames = [];
    const mutations = window.__mutations = [];
    let raf = 0, tick = 0;
    const ribbonOf = () => document.querySelector('.sidebar-ribbon');
    const observer = new MutationObserver(records => {
      for (const r of records) {
        const el = r.target instanceof Element ? r.target : r.target.parentElement;
        const inRibbon = el?.closest?.('.sidebar-ribbon') ? 'ribbon' : (el === document.documentElement ? 'html' : 'other');
        mutations.push({ t: performance.now(), zone: inRibbon, type: r.type,
          attr: r.attributeName,
          added: r.addedNodes?.length ?? 0, removed: r.removedNodes?.length ?? 0,
          target: el === document.documentElement ? '<html>'
            : el?.className && typeof el.className === 'string' ? '.' + el.className.split(' ')[0] : el?.tagName });
      }
      if (mutations.length > 6000) mutations.splice(0, mutations.length - 6000);
    });
    observer.observe(document.documentElement, { attributes: true, childList: true, subtree: true });
    window.__stopObserver = () => observer.disconnect();
    const sample = () => {
      tick++;
      const ribbon = ribbonOf();
      const rect = ribbon?.getBoundingClientRect();
      const shell = document.querySelector('.app-shell');
      const h = document.documentElement;
      frames.push({ tick, t: performance.now(),
        ribbon: Boolean(ribbon), hidden: ribbon?.hasAttribute('hidden') ?? null,
        rect: rect ? [Math.round(rect.x), Math.round(rect.y), Math.round(rect.width), Math.round(rect.height)] : null,
        ribbonKids: ribbon?.querySelector('.sidebar-ribbon-main')?.childElementCount ?? null,
        websiteRows: ribbon?.querySelectorAll('[data-ribbon-id]').length ?? null,
        ribbonIds: [...(ribbon?.querySelectorAll('[data-ribbon-id]') ?? [])].map(el => el.dataset.ribbonId),
        addBtnDisabled: ribbon?.querySelector('.sidebar-ribbon-add')?.disabled ?? null,
        ribbonW: shell?.style.getPropertyValue('--sidebar-ribbon-width') ?? getComputedStyle(h).getPropertyValue('--sidebar-ribbon-width'),
        vt: h.getAttribute('data-wuu-compositor-frame'),
        clip: h.style.getPropertyValue('--wuu-presentation-clip')?.slice(0, 40) ?? null,
        overlay: document.querySelector('[data-wuu-presentation-frame]')?.style.clipPath?.slice(0, 40) ?? null,
        inert: Boolean(shell?.closest('[inert]')) || Boolean(ribbon?.closest('[inert]')),
        bg: Boolean(document.querySelector('.app-background')),
        skin: h.hasAttribute('data-shell-skin') });
      raf = requestAnimationFrame(sample);
    };
    raf = requestAnimationFrame(sample);
    window.__stopSampler = () => cancelAnimationFrame(raf);
    return true;
  });
}
async function stopSampler() {
  return evaluate(() => {
    window.__stopSampler?.();
    window.__stopObserver?.();
    return { frames: window.__frames, mutations: window.__mutations };
  });
}
async function sampleFrames(points, ms) {
  const samples = [];
  const t0 = Date.now();
  const handler = (image) => {
    const bmp = image.toBitmap(), w = image.getSize().width;
    if (!w || !bmp.length) return;
    const scale = w / WIDTH;
    const px = (x, y) => { const i = (Math.round(y * scale) * w + Math.round(x * scale)) * 4; return [bmp[i + 2], bmp[i + 1], bmp[i]]; };
    samples.push({ t: Date.now() - t0, p: points.map(([x, y]) => px(x, y)) });
  };
  win.webContents.beginFrameSubscription(false, handler);
  await new Promise(r => setTimeout(r, ms));
  win.webContents.endFrameSubscription();
  return samples;
}
// Probe the ribbon column (x≈26 center of the 52px rail) plus controls.
const PROBE_POINTS = [
  [26, 60],   // brand / header of ribbon
  [26, 200],  // ribbon destinations
  [26, 400],  // ribbon middle
  [26, 700],  // ribbon lower
  [26, 920],  // ribbon footer
  [200, 480], // sidebar content
  [900, 480], // conversation pane
];
async function steadyPixels() {
  const img = await win.webContents.capturePage();
  fs.writeFileSync(path.join(output, `${phase}-steady.png`), img.toPNG());
  const bmp = img.toBitmap(), w = img.getSize().width, scale = w / WIDTH;
  const px = (x, y) => { const i = (Math.round(y * scale) * w + Math.round(x * scale)) * 4; return [bmp[i + 2], bmp[i + 1], bmp[i]]; };
  return PROBE_POINTS.map(([x, y]) => px(x, y));
}

function analyze(label, frames, mutations, samples, baselinePixel) {
  const t = { label, frames: frames.length, mutations: mutations.length, anomalies: [], mutationHistogram: {}, events: [] };
  for (const m of mutations) {
    const key = `${m.zone}:${m.target}@${m.attr ?? `${m.type}+${m.added}-${m.removed}`}`;
    t.mutationHistogram[key] = (t.mutationHistogram[key] || 0) + 1;
  }
  t.mutationHistogram = Object.fromEntries(Object.entries(t.mutationHistogram).sort((a, b) => b[1] - a[1]).slice(0, 20));
  const seq = (pick) => {
    const out = []; let prev;
    for (const f of frames) { const v = pick(f); const k = JSON.stringify(v); if (k !== prev) { out.push({ tick: f.tick, v }); prev = k; } }
    return out;
  };
  t.ribbonPresence = seq(f => [f.ribbon, f.hidden]);
  t.ribbonKidsSeq = seq(f => f.ribbonKids).slice(0, 15);
  t.websiteRowsSeq = seq(f => f.websiteRows).slice(0, 15);
  t.ribbonIdsSeq = seq(f => f.ribbonIds).slice(0, 8);
  t.addBtnSeq = seq(f => f.addBtnDisabled).slice(0, 15);
  t.ribbonWidthSeq = seq(f => f.ribbonW).slice(0, 10);
  t.vtSeq = seq(f => [f.vt, f.clip]).slice(0, 15);
  t.overlaySeq = seq(f => f.overlay).slice(0, 15);
  t.inertSeq = seq(f => f.inert).slice(0, 10);
  t.bgSeq = seq(f => f.bg).slice(0, 10);
  t.skinSeq = seq(f => f.skin).slice(0, 10);
  t.rectSeq = seq(f => f.rect).slice(0, 10);
  const ribbonGone = frames.filter(f => !f.ribbon || f.hidden).length;
  t.ribbonGoneFrames = ribbonGone;
  t.sampleCount = samples.length;
  if (baselinePixel && samples.length) {
    const scored = samples.map((s, i) => {
      let worst = 0, wi = -1;
      s.p.forEach((p, pi) => {
        const b = baselinePixel[pi];
        if (!b) return;
        const d = Math.max(...p.map((v, j) => Math.abs(v - b[j])));
        if (d > worst) { worst = d; wi = pi; }
      });
      return { i, t: s.t, dev: worst, point: wi >= 0 ? PROBE_POINTS[wi] : null,
        pixel: wi >= 0 ? s.p[wi] : null, baseline: wi >= 0 ? baselinePixel[wi] : null };
    }).filter(s => s.dev > 40).sort((a, b) => b.dev - a.dev);
    t.anomalies = scored.slice(0, 8);
    t.maxDev = scored[0]?.dev ?? 0;
    t.devFrames = scored.length;
  }
  report.transitions.push(t);
  return t;
}

// Click the session row inside a specific workspace section. When the target
// workspace is already active, prefer a row that is not the current session so
// the click is a real session switch.
async function clickWorkspaceSession(sectionId) {
  return evaluate(id => {
    const group = document.querySelector(`.project-group[data-section-id="${id}"]`);
    if (!group) return { ok: false, reason: 'no workspace group', ids: [...document.querySelectorAll('.project-group')].map(g => g.dataset.sectionId) };
    const rows = [...group.querySelectorAll('.sidebar-session-row')];
    if (!rows.length) {
      const header = group.querySelector('button.sidebar-section-row');
      if (header && header.getAttribute('aria-expanded') !== 'true') header.click();
      return { ok: false, expanded: true };
    }
    const target = rows.find(row => !row.classList.contains('active')) ?? rows[0];
    (target.querySelector('.thread-row-main') || target).click();
    return { ok: true, rowCount: rows.length };
  }, sectionId);
}

async function runSwitchCycles(label, targets, cycles = 3) {
  const out = { label, switches: [] };
  out.baselinePixel = await steadyPixels();
  for (let c = 0; c < cycles; c++) {
    for (const target of targets) {
      const phaseName = `${label}-c${c}-${target}`;
      phase = phaseName;
      await installSampler();
      const sampling = sampleFrames(PROBE_POINTS, 1200);
      const clickInfo = await clickWorkspaceSession(target);
      report.checks.push({ name: `${phaseName}-click`, detail: clickInfo });
      if (!clickInfo.ok) { await sampling; const { frames, mutations } = await stopSampler(); continue; }
      const samples = await sampling;
      const { frames, mutations } = await stopSampler();
      const a = analyze(phaseName, frames, mutations, samples, out.baselinePixel);
      if (a.anomalies.length) {
        const img = await win.webContents.capturePage();
        fs.writeFileSync(path.join(output, `${phaseName}-anomaly.png`), img.toPNG());
      }
      out.switches.push(a);
      save();
      await sleep(300);
    }
  }
  return out;
}

async function main() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', agent: { permission_mode: 'unconfined' },
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
    engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false } }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [
    { id: 'alpha', name: 'Alpha', path: workspaceA, created_at: stamp, updated_at: stamp },
    { id: 'beta', name: 'Beta', path: workspaceB, created_at: stamp, updated_at: stamp }],
    active_context: { kind: 'project', project_id: 'alpha', cwd: workspaceA } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));

  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu window');
  win.setContentSize(WIDTH, HEIGHT);
  await shellReady();

  // One session in Alpha.
  await sendTurn('Alpha conversation');
  await until(() => evaluate(r => document.body.innerText.includes(r), REPLY), 'alpha reply', 60000);
  await until(() => evaluate(() => document.querySelectorAll('.sidebar-session-row').length >= 1), 'alpha session row', 30000);

  // Switch the runtime to Beta through the same entry the renderer uses,
  // then create a Beta session.
  await evaluate(() => window.wuu.selectProject('beta'));
  await waitActiveWorkspace(workspaceB);
  await sleep(800);
  await sendTurn('Beta conversation');
  await until(() => evaluate(r => document.body.innerText.includes(r), REPLY), 'beta reply', 60000);
  await until(() => evaluate(() => document.querySelectorAll('.sidebar-session-row').length >= 1), 'beta session row', 30000);

  // Back to Alpha so both workspace sections have a live session.
  await evaluate(() => window.wuu.selectProject('alpha'));
  await waitActiveWorkspace(workspaceA);
  await sleep(800);
  // A second Alpha session makes same-project switching a real control.
  await sendTurn('Alpha second conversation');
  await until(() => evaluate(r => document.body.innerText.includes(r), REPLY), 'alpha reply 2', 60000);

  // Make sure both workspace sections are expanded and each has a session row.
  for (const id of ['beta', 'alpha']) {
    await evaluate(sid => {
      const group = document.querySelector(`.project-group[data-section-id="${sid}"]`);
      const header = group?.querySelector('button.sidebar-section-row');
      if (group && !group.querySelectorAll('.sidebar-session-row').length && header
        && header.getAttribute('aria-expanded') !== 'true') header.click();
    }, id);
    await until(() => evaluate(gid =>
      Boolean(document.querySelector(`.project-group[data-section-id="${gid}"] .sidebar-session-row`))
    , id), `${id} section session row`, 30000);
  }

  report.results = {
    // Same-project session switches must not reload plugin generations:
    // graph_generation stays identical, so this is the negative control.
    same: await runSwitchCycles('same', ['alpha', 'alpha'], 2),
    cross: await runSwitchCycles('cross', ['beta', 'alpha'], 3),
  };
  report.status = 'done';
}

main().then(() => { save(); console.log('DONE', output); app.exit(0); })
  .catch(async error => {
    report.status = 'failed'; report.error = String(error?.stack || error);
    save();
    if (win && !win.isDestroyed()) { try { fs.writeFileSync(path.join(output, 'failure.png'), (await win.webContents.capturePage()).toPNG()); } catch {} }
    console.error(error); app.exit(1);
  });
