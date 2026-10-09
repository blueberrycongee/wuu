// Real Electron/main/preload/Go + HTTP provider, with separate model contexts.
// Build core and desktop first. All inference and workspace data are synthetic.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');
const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_FUSION_OUTPUT || path.join(desktop, 'out/e2e/fusion');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-fusion-'));
const home = path.join(fixture, 'home'), project = path.join(fixture, 'project');
fs.mkdirSync(output, { recursive: true }); fs.mkdirSync(home); fs.mkdirSync(project);
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE ||= path.join(desktop, 'build/bin/wuu-core');
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
delete process.env.ELECTRON_RENDERER_URL;
let main, releaseSide;
const requests = [], layouts = [];
app.on('browser-window-created', (_event, window) => { main ||= window; });
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`, true);
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(win, fn, arg) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) { if (await evaluate(win, fn, arg)) return; await delay(30); }
  throw new Error(`Timed out: ${fn}`);
}
function reply(res, model, content, tool) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' });
  const delta = tool ? { role: 'assistant', tool_calls: [{ index: 0, id: tool.id, type: 'function', function: { name: tool.name, arguments: JSON.stringify(tool.args) } }] } : { role: 'assistant', content };
  res.write(`data: ${JSON.stringify({ id: 'fusion-fixture', model, choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
  res.end(`data: ${JSON.stringify({ id: 'fusion-fixture', model, choices: [{ index: 0, delta: {}, finish_reason: tool ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120 } })}\n\ndata: [DONE]\n\n`);
}
const server = http.createServer((req, res) => {
  let raw = ''; req.on('data', data => { raw += data; });
  req.on('end', () => {
    const input = JSON.parse(raw); requests.push({ path: req.url, body: input });
    assert.notEqual(input.model, 'wuu/fusion');
    const messages = input.messages || [];
    const tools = input.tools || [];
    const lastUser = messages.findLast(message => message.role === 'user' && message.name !== 'wuu_context_window');
    const human = messages.findLast(message => message.role === 'user' && message.name !== 'wuu_context_window' && !String(message.content).startsWith('Fusion task '));
    const text = JSON.stringify(human?.content || lastUser?.content || '');
    const followup = text.includes('followup');
    const background = text.includes('background-stop') || text.includes('background-report') || text.includes('background-update') || text.includes('background-ptc');
    const failure = text.includes('provider-failure');
    const correction = text.includes('review-cycle'), readonly = text.includes('readonly-check');
    const last = messages.at(-1);
    if (input.model === 'fixture-side') {
      assert.ok(!tools.some(tool => tool.function?.name === 'fusion_delegate'), 'Side cannot recursively delegate');
      if (failure) {
        res.writeHead(403, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'Sidekick fixture spending limit reached', code: 'spending_limit' } }));
        return;
      }
      if (last?.role === 'tool') return reply(res, input.model, readonly ? 'Side observed read-only denial.' : followup ? 'Side followup verified.' : 'Side implementation verified.');
      const writeArgs = { path: readonly ? 'readonly-blocked.txt' : 'fusion-e2e.txt', content: followup ? 'followup' : 'implemented' };
      const ptc = tools.some(tool => tool.function?.name === 'run_code');
      const write = () => reply(res, input.model, '', { id: followup ? 'side-write-followup' : 'side-write', name: ptc ? 'run_code' : 'write_file', args: ptc ? { input: `const result = await tools.write_file(${JSON.stringify(writeArgs)}); text(result);` } : writeArgs });
      if (!followup) releaseSide = write; else write();
      return;
    }
    const reviewed = failure ? 'Lead explained Sidekick provider failure.' : correction ? 'Lead reviewed correction.' : readonly ? 'Lead verified read-only work.' : background ? 'Lead reviewed background result.' : followup ? 'Lead reviewed followup.' : 'Lead reviewed implementation.';
    if (last?.role === 'tool') {
      let task; try { task = JSON.parse(last.content); } catch {}
      if ((task?.state === 'running' || task?.state === 'queued') && text.includes('background-ptc')) return reply(res, input.model, '', { id: 'lead-nested-conflict', name: 'run_code', args: { input: 'const result = await tools.write_file({path: "lead-conflict.txt", content: "must not write"}); text(result);' } });
      if (task?.state === 'awaiting_review' && correction && task.revision === 1) return reply(res, input.model, '', { id: 'request-correction', name: 'fusion_delegate', args: { action: 'review', task_id: task.task_id, report_id: task.report.report_id, revision: task.revision, verdict: 'request_changes', message: 'Implement fusion-followup to fix the observed output and verify it.' } });
      if (task?.state === 'awaiting_review') return reply(res, input.model, '', { id: `accept-${task.report.report_id}`, name: 'fusion_delegate', args: { action: 'review', task_id: task.task_id, report_id: task.report.report_id, revision: task.revision, verdict: 'accept', message: 'Checked actual implementation and verification evidence.' } });
      return reply(res, input.model, text.includes('active-update') ? 'Lead sent changed requirements.' : task?.state === 'running' || task?.state === 'queued' || text.includes('background-ptc') && !task ? 'Lead is waiting for background work.' : reviewed);
    }
    if (text.includes('active-update') && !String(lastUser?.content).startsWith('Fusion task ')) {
      const active = messages.filter(message => message.role === 'tool').map(message => { try { return JSON.parse(message.content); } catch { return null; } }).findLast(task => task?.state === 'running' || task?.state === 'queued');
      assert.ok(active?.task_id, 'Lead must update the admitted task');
      return reply(res, input.model, '', { id: 'update-active', name: 'fusion_delegate', args: { action: 'update', task_id: active.task_id, message: 'Apply fusion-followup, then wrap up and report the verified result.' } });
    }
    const report = String(lastUser?.content).match(/^Fusion task (\S+) report (\S+), requirements revision (\d+)/);
    if (report) return reply(res, input.model, '', { id: `accept-${report[2]}`, name: 'fusion_delegate', args: { action: 'review', task_id: report[1], report_id: report[2], revision: Number(report[3]), verdict: 'accept', message: 'Checked the actual background work and evidence.' } });
    if (!tools.some(tool => tool.function?.name === 'fusion_delegate')) return reply(res, input.model, 'Fusion test');
    reply(res, input.model, '', { id: 'delegate-' + String(human?.content || '').replace(/[^a-z0-9]+/gi, '-'), name: 'fusion_delegate', args: { message: correction ? 'Implement review-cycle and verify it.' : readonly ? 'Inspect readonly-check without changing files.' : failure ? 'Check provider-failure.' : background ? `Implement ${text.includes('background-ptc') ? 'background-ptc' : text.includes('background-update') ? 'background-update' : text.includes('background-report') ? 'background-report' : 'background-stop'}.` : followup ? 'Implement fusion-followup and verify it.' : 'Implement fusion-e2e and verify it.', ...(background ? { block: false } : {}), ...(readonly ? { read_only: true } : {}) } });
  });
});
async function capture(name) {
  console.log(`Capture ${name}`);
  await evaluate(main, () => new Promise(resolve => {
    setTimeout(resolve, 1000);
    let stable = 0, previous = '';
    const check = () => {
      const bounds = `${window.innerWidth}:${window.innerHeight}:${document.body.getBoundingClientRect().width}`;
      stable = bounds === previous ? stable + 1 : 0; previous = bounds;
      if (stable >= 4 && !document.getAnimations().some(animation => animation.playState === 'running' && animation.effect.getTiming().iterations !== Infinity)) resolve();
      else requestAnimationFrame(check);
    }; requestAnimationFrame(check);
  }));
  const layout = await evaluate(main, () => {
    const selectors = ['.codex-runtime-trigger', '.runtime-panel', '.runtime-panel-fusion-model', '.runtime-panel-fusion-role', '.fusion-status-summary', '.fusion-failure', '.fusion-selections', '.fusion-selection', '.fusion-selection .settings-select-trigger'];
    const measurements = selectors.flatMap(selector => [...document.querySelectorAll(selector)].map(node => {
      const rect = node.getBoundingClientRect(), style = getComputedStyle(node);
      return { selector, text: node.textContent, x: rect.x, y: rect.y, width: rect.width, height: rect.height, clientWidth: node.clientWidth, scrollWidth: node.scrollWidth, fontSize: style.fontSize, visible: rect.width > 0 && rect.height > 0 };
    }));
    return { viewport: { width: window.innerWidth, height: window.innerHeight, devicePixelRatio: window.devicePixelRatio }, theme: document.documentElement.dataset.theme, measurements };
  });
  const png = (await main.webContents.capturePage()).toPNG();
  fs.writeFileSync(path.join(output, `${name}.png`), png);
  layouts.push({ name, ...layout, zoomFactor: main.webContents.getZoomFactor(), image: { width: png.readUInt32BE(16), height: png.readUInt32BE(20) } });
}
const timeout = setTimeout(() => { console.error('Fusion E2E timeout', fixture); app.exit(1); }, 240000);
server.listen(0, '127.0.0.1', async () => {
  try {
    const pair = { lead: { provider: 'fixture', model: 'fixture-lead' }, sidekick: { provider: 'fixture', model: 'fixture-side' } };
    fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'fixture', providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'synthetic', model: 'fixture-lead', models: { 'fixture-lead': { name: 'Lead model with a deliberately long display name' }, 'fixture-side': { name: 'Sidekick model with a deliberately long display name' } } } }, agent: { permission_mode: 'unconfined', fusion: { enabled: true, default: true, ...pair } }, ptc: { enabled: false } }));
    fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fusion-fixture', name: 'Fusion acceptance', path: project, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }], active_context: { kind: 'project', project_id: 'fusion-fixture', cwd: project } }));
    fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en-US', theme: 'light' }));
    const boot = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', project], { env: process.env, input: '{"id":1,"method":"thread/start","params":{"fusion":true,"permission_mode":"unconfined"}}\n', encoding: 'utf8', timeout: 30000 });
    assert.equal(boot.status, 0, boot.stderr);
    const response = boot.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(message => message.id === 1);
    assert.ok(response?.result?.thread?.fusion, boot.stdout);
    const leadID = response.result.thread.id;
    await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
    while (!main) await delay(25);
    main.show(); main.focus(); main.setMinimumSize(480, 480); main.setContentSize(1180, 860);
    await waitFor(main, () => document.querySelector('.composer textarea'));
    main.webContents.setZoomFactor(1);
    await evaluate(main, async id => { await window.wuu.resumeThread(id); }, leadID);
    await waitFor(main, () => document.querySelector('.codex-runtime-trigger')?.textContent.includes('Fusion'));
    await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
    await waitFor(main, () => document.querySelector('.runtime-panel'));
    assert.deepEqual(await evaluate(main, () => [...document.querySelectorAll('.runtime-panel-fusion-model .runtime-panel-model-name')].map(node => node.textContent)), [pair.lead.model, pair.sidekick.model]);
    const inspectPairLayout = async () => {
      const layout = await evaluate(main, () => {
        const panel = document.querySelector('.runtime-panel').getBoundingClientRect();
        return [...document.querySelectorAll('.runtime-panel-fusion-model')].map(row => {
          const bounds = row.getBoundingClientRect(), label = row.querySelector('.runtime-panel-fusion-role');
          return { inside: bounds.top >= panel.top && bounds.bottom <= panel.bottom, labelFits: label.scrollWidth <= label.clientWidth + 1 };
        });
      });
      assert.ok(layout.length === 2 && layout.every(row => row.inside && row.labelFits), 'Both roles must fit inside the actual rendered menu');
    };
    await capture('runtime-menu');
    await inspectPairLayout();
    main.setContentSize(640, 860);
    await evaluate(main, () => { document.documentElement.dataset.theme = 'dark'; document.documentElement.style.setProperty('--conversation-message-font-size', '18px'); window.dispatchEvent(new Event('wuu-content-size-change')); });
    await capture('runtime-menu-dark-18-640');
    await inspectPairLayout();
    main.setContentSize(1180, 860);
    await evaluate(main, () => { document.documentElement.dataset.theme = 'light'; document.documentElement.style.setProperty('--conversation-message-font-size', '14px'); window.dispatchEvent(new Event('wuu-content-size-change')); });
    await evaluate(main, () => document.querySelector('.codex-runtime-trigger').click());
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build fusion-e2e'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Sidekick · Working'));
    assert.ok(releaseSide);
    const running = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    const workingTask = running.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1);
    assert.equal(workingTask.state, 'running');
    await waitFor(main, () => document.querySelector('.turn-process-topline .session-message-source'));
    await evaluate(main, () => document.querySelector('.turn-process-topline .session-message-source').click());
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"]`), workingTask.side_id);
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"] .session-message-name`), workingTask.side_id);
    const sourceIdentity = await evaluate(main, async ({sideID, leadID}) => {
      const side = (await window.wuu.resumeThread(sideID)).thread;
      const input = side.turns.flatMap(turn => turn.items).find(item => item.cause === 'fusion');
      return { sourceID: input.related_session_id, persistedName: input.name || '',
        leadTitle: (await window.wuu.resumeThread(leadID)).thread.title || '',
        displayedRole: document.querySelector(`.conversation-split-pane[data-thread-id="${sideID}"] .session-message-name`).textContent };
    }, {sideID: workingTask.side_id, leadID});
    assert.equal(sourceIdentity.leadTitle, '', 'Reproduce delegation before automatic title generation');
    assert.equal(sourceIdentity.persistedName, '', 'Reproduce untitled source history');
    assert.equal(sourceIdentity.sourceID, leadID);
    assert.equal(sourceIdentity.displayedRole, 'Lead', 'Fusion sources use their role even before a title exists');
    const directInputs = await evaluate(main, async ({id, turnID}) => {
      const actions = [() => window.wuu.startTurn(id, 'unversioned input'), () => window.wuu.queueTurn(id, 'unversioned queue'), () => window.wuu.steerTurn(id, turnID, 'unversioned steer')];
      return Promise.all(actions.map(async action => { try { await action(); return false; } catch { return true; } }));
    }, {id: workingTask.side_id, turnID: workingTask.progress?.turn_id || (await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, workingTask.side_id)).turns.at(-1).id});
    assert.deepEqual(directInputs, [true, true, true], 'Direct Side inputs cannot bypass requirements revisions');
    await capture('side-working-conversation');
    await evaluate(main, () => document.querySelector('.conversation-split-close').click());
    await waitFor(main, () => document.querySelectorAll('.conversation-split-pane').length < 2);
    await capture('side-working'); releaseSide();
    await waitFor(main, () => document.body.textContent.includes('Lead reviewed implementation.'));
    assert.equal(fs.readFileSync(path.join(project, 'fusion-e2e.txt'), 'utf8'), 'implemented');
    const accepted = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    assert.equal(accepted.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1).state, 'completed');
    await capture('task-reviewed-complete');
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build fusion-followup'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Lead reviewed followup.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    assert.equal(fs.readFileSync(path.join(project, 'fusion-e2e.txt'), 'utf8'), 'followup');
    const thread = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    const sideIDs = new Set(thread.turns.flatMap(turn => turn.fusion?.delegations?.map(dispatch => dispatch.session_id) || []));
    assert.equal(sideIDs.size, 1, 'Corrections reuse the same ordinary Side session');
    assert.notEqual([...sideIDs][0], leadID);
    const sideID = [...sideIDs][0];
    // A completed background dispatch is presented as a source entry; opening
    // it must resume the actual persistent Sidekick through main and preload.
    releaseSide = undefined;
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build background-report'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Lead is waiting for background work.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    assert.ok(releaseSide); releaseSide();
    await waitFor(main, () => document.body.textContent.includes('Lead reviewed background result.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    const reportThread = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    const reports = reportThread.turns.flatMap(turn => turn.items).filter(item => item.type === 'user_message' && item.related_session_id === sideID);
    assert.equal(reports.length, 1, 'An asynchronous Sidekick result is delivered exactly once');
    await waitFor(main, () => document.querySelector('.session-message-source'));
    const compact = await evaluate(main, () => {
      const entry = document.querySelector('.session-message-source');
      entry.scrollIntoView({ block: 'center' });
      return { label: entry.textContent, hiddenPayload: !document.body.innerText.includes('Fusion Sidekick result for dispatch') };
    });
    assert.match(compact.label, /Message from/); assert.equal(compact.hiddenPayload, true);
    await capture('message-source-compact');
    await evaluate(main, () => document.querySelector('.session-message-source').click());
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"]`)?.innerText.includes('Side implementation verified.'), sideID);
    assert.equal(await evaluate(main, () => document.querySelectorAll('.conversation-split-pane').length), 2);
    await capture('message-source-conversation');
    await evaluate(main, () => document.querySelector('.conversation-split-close').click());
    await waitFor(main, () => document.querySelectorAll('.conversation-split-pane').length < 2);
    const hiddenSideTitle = 'Hidden Fusion Sidekick sentinel';
    await evaluate(main, async ({ id, title }) => { await window.wuu.renameThread(id, title); }, { id: sideID, title: hiddenSideTitle });
    await waitFor(main, async ({ id, title }) => (await window.wuu.resumeThread(id)).thread.title === title, { id: sideID, title: hiddenSideTitle });
    const sidebarTitles = await evaluate(main, () => [...document.querySelectorAll('.sidebar .thread-row-title')].map(node => node.textContent));
    assert.ok(!sidebarTitles.includes(hiddenSideTitle), 'Fusion Sidekick stays out of the left sidebar');
    await capture('sidebar-side-hidden');
    const sideRequests = requests.filter(request => request.body.model === 'fixture-side');
    assert.ok(sideRequests.some(request => JSON.stringify(request.body.messages).includes('Side implementation verified.') && JSON.stringify(request.body.messages).includes('fusion-followup')), 'Side keeps its own prior context');
    assert.ok(thread.turns.some(turn => turn.fusion?.delegations?.some(dispatch => dispatch.input_tokens > 0)), 'Structured usage survives reload');
    // Consolidated review feedback revises the same task and persistent Side.
    releaseSide = undefined;
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build review-cycle'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Sidekick · Working'));
    assert.ok(releaseSide); releaseSide();
    await waitFor(main, () => document.body.textContent.includes('Lead reviewed correction.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    const corrected = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    const correctedTask = corrected.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1);
    assert.equal(correctedTask.state, 'completed'); assert.equal(correctedTask.revision, 2);
    assert.equal(correctedTask.review_rounds, 1); assert.equal(correctedTask.side_id, sideID);
    assert.equal(fs.readFileSync(path.join(project, 'fusion-e2e.txt'), 'utf8'), 'followup');
    await evaluate(main, () => [...document.querySelectorAll('.turn-process-toggle')].filter(node => !node.closest('[inert]')).at(-1).click());
    await evaluate(main, () => { const node = [...document.querySelectorAll('.turn-process-topline')].filter(node => !node.closest('[inert]')).at(-1); node.scrollIntoView({block: 'center'}); });
    await capture('task-review-correction');
    // Accepted requirements survive a completion racing the next Side step.
    releaseSide = undefined;
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build background-update'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Lead is waiting for background work.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    assert.ok(releaseSide);
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Refine active-update and wrap up'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Lead sent changed requirements.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    const updated = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    assert.equal(updated.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1).revision, 2);
    releaseSide();
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1).state === 'completed', leadID);
    assert.equal(fs.readFileSync(path.join(project, 'fusion-e2e.txt'), 'utf8'), 'followup');
    await evaluate(main, () => document.querySelector('.jump-to-latest-pill')?.click());
    await capture('task-active-update');
    // Task-level read-only is enforced at actual file execution.
    releaseSide = undefined;
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build readonly-check'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Sidekick · Working'));
    assert.ok(releaseSide); releaseSide();
    await waitFor(main, () => document.body.textContent.includes('Lead verified read-only work.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    assert.equal(fs.existsSync(path.join(project, 'readonly-blocked.txt')), false);
    const readonlyTask = (await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID)).turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1);
    assert.equal(readonlyTask.state, 'completed'); assert.equal(readonlyTask.read_only, true);
    // A settled Lead turn must retain the failed Side's structured cause.
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build provider-failure'); }, leadID);
    await waitFor(main, () => document.body.textContent.includes('Lead explained Sidekick provider failure.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', leadID);
    const failureThread = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID);
    assert.equal(failureThread.turns.at(-1).fusion.delegations.at(-1).error.status_code, 403);
    await evaluate(main, () => [...document.querySelectorAll('.turn-process-toggle')].at(-1).click());
    await waitFor(main, () => document.querySelector('.fusion-failure summary'));
    await evaluate(main, () => document.querySelector('.fusion-failure summary').click());
    await waitFor(main, () => document.body.textContent.includes('Sidekick fixture spending limit reached'));
    await capture('side-provider-failure');
    // Inspect both themes, normal/large UI text, wide/narrow windows and long labels.
    for (const theme of ['light', 'dark']) for (const font of [14, 18]) for (const width of [1180, 640]) {
      main.setContentSize(width, 860);
      await evaluate(main, ({ theme, font }) => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--conversation-message-font-size', `${font}px`); window.dispatchEvent(new Event('wuu-content-size-change')); }, { theme, font });
      await capture(`conversation-${theme}-${font}-${width}`);
    }
    main.setContentSize(1180, 860);
    await evaluate(main, () => document.querySelector('.sidebar-account-trigger')?.click());
    await waitFor(main, () => document.querySelector('[data-settings-page="providers"]'));
    await evaluate(main, () => document.querySelector('[data-settings-page="providers"]').click());
    await waitFor(main, () => document.querySelector('.settings-nav-item'));
    await evaluate(main, () => [...document.querySelectorAll('.settings-nav-item')].find(button => button.textContent.trim() === 'Built-in agent')?.click());
    await waitFor(main, () => document.querySelector('.fusion-selections'));
    await evaluate(main, () => { const button = document.querySelector('.settings-sidebar-toggle'); if (button.getAttribute('aria-pressed') === 'true') button.click(); });
    for (const theme of ['light', 'dark']) for (const font of [14, 18]) for (const width of [1180, 640]) {
      main.setContentSize(width, 860);
      await evaluate(main, ({ theme, font }) => { document.documentElement.dataset.theme = theme; document.documentElement.style.setProperty('--conversation-message-font-size', `${font}px`); window.dispatchEvent(new Event('wuu-content-size-change')); }, { theme, font });
      await capture(`settings-${theme}-${font}-${width}`);
    }
    // Explicit mode API works through preload; selecting a model leaves Fusion.
    await evaluate(main, async id => { await window.wuu.updateRuntimeSettings('fixture', 'fixture-lead', undefined, undefined, undefined, undefined, id, undefined, undefined, false); }, leadID);
    assert.equal((await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID)).fusion, null);
    await evaluate(main, async id => { await window.wuu.updateRuntimeSettings(undefined, undefined, undefined, undefined, undefined, undefined, id, undefined, undefined, true); }, leadID);
    assert.deepEqual((await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID)).fusion, pair);
    // A normal UI first send must create Fusion from the saved default.
    main.setContentSize(1180, 860);
    await evaluate(main, () => document.querySelector('.settings-back-button').click());
    await waitFor(main, () => document.querySelector('.composer textarea'));
    await evaluate(main, () => [...document.querySelectorAll('button')].find(button => button.textContent.trim() === 'New conversation').click());
    await waitFor(main, () => document.querySelector('.codex-runtime-trigger')?.textContent.includes('Fusion'));
    await evaluate(main, () => document.querySelector('.composer textarea').focus());
    main.webContents.insertText('UI fusion-followup');
    await waitFor(main, () => document.querySelector('.composer button[aria-label="Send"]')?.disabled === false);
    await evaluate(main, () => document.querySelector('.composer button[aria-label="Send"]').click());
    await waitFor(main, () => document.body.textContent.includes('Lead reviewed followup.'));
    const uiLead = await evaluate(main, async original => (await window.wuu.listThreads()).threads.find(thread => thread.id !== original && thread.fusion), leadID);
    assert.ok(uiLead?.fusion, 'First send carries the draft Fusion selection');
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', uiLead.id);
    // Side can outlive Lead. Its Stop remains reachable in the main composer.
    releaseSide = undefined;
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build background-stop'); }, uiLead.id);
    await waitFor(main, () => document.body.textContent.includes('Lead is waiting for background work.'));
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', uiLead.id);
    assert.ok(releaseSide);
    await waitFor(main, () => document.querySelector('.composer .composer-stop-button'));
    await capture('background-side-stop-control');
    await evaluate(main, () => document.querySelector('.composer .composer-stop-button').click());
    const stoppedThread = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, uiLead.id);
    const uiSideID = stoppedThread.turns.flatMap(turn => turn.fusion?.delegations ?? []).at(-1).session_id;
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', uiSideID);
    await waitFor(main, () => document.body.textContent.includes('Fusion · Cancelled'));
    await capture('background-side-stopped');
    assert.equal(stoppedThread.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1).state, 'cancelled');
    await evaluate(main, () => [...document.querySelectorAll('.turn-process-topline .session-message-source')].filter(node => !node.closest('[inert], [hidden], [aria-hidden="true"]')).at(-1).click());
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"]`), uiSideID);
    await capture('cancelled-side-conversation');
    await evaluate(main, () => document.querySelector('.conversation-split-close').click());
    // Nested Code Mode tools enforce the same shared-writer boundary.
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', uiLead.id);
    await evaluate(main, async () => { await window.wuu.updateGeneralSettings({ptc: {enabled: true}}); });
    const ptcLead = await evaluate(main, async () => (await window.wuu.startThread({fusion: true, permission_mode: 'unconfined'})).thread);
    releaseSide = undefined;
    await evaluate(main, async id => { await window.wuu.startTurn(id, 'Build background-ptc'); }, ptcLead.id);
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', ptcLead.id);
    assert.ok(releaseSide); assert.equal(fs.existsSync(path.join(project, 'lead-conflict.txt')), false);
    releaseSide();
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.turns.flatMap(turn => turn.fusion?.tasks ?? []).at(-1)?.state === 'completed', ptcLead.id);
    assert.equal(fs.readFileSync(path.join(project, 'fusion-e2e.txt'), 'utf8'), 'implemented');
    const nestedThread = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, ptcLead.id);
    assert.ok(nestedThread.turns.flatMap(turn => turn.items).some(item => item.name === 'run_code' && JSON.stringify(item).includes('Sidekick owns')), 'Nested Lead write is actually denied');
    await waitFor(main, async id => (await window.wuu.resumeThread(id)).thread.status === 'idle', ptcLead.id);
    await evaluate(main, async () => { await window.wuu.updateGeneralSettings({ptc: {enabled: false}}); });
    // Global pair changes apply to new conversations, never existing histories.
    const changed = { lead: pair.lead, sidekick: pair.lead };
    await evaluate(main, async changed => { await window.wuu.updateAdvancedSettings({ fusion: { enabled: true, default: true, ...changed } }); }, changed);
    assert.deepEqual((await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, leadID)).fusion, pair);
    const newThread = await evaluate(main, async () => (await window.wuu.startThread({ fusion: true })).thread);
    assert.deepEqual(newThread.fusion, changed);
    const cold = spawnSync(process.env.WUU_DESKTOP_CORE, ['app-server', '--safe-mode', '--workdir', project], { env: process.env, input: JSON.stringify({id: 900, method: 'thread/resume', params: {session_id: leadID}}) + '\n', encoding: 'utf8', timeout: 30000 });
    assert.equal(cold.status, 0, cold.stderr);
    const recovered = cold.stdout.split('\n').filter(Boolean).map(line => JSON.parse(line)).find(message => message.id === 900)?.result?.thread;
    assert.ok(recovered, cold.stdout);
    const recoveredTasks = recovered.turns.flatMap(turn => turn.fusion?.tasks ?? []);
    assert.ok(recoveredTasks.some(task => task.review_rounds === 1 && task.state === 'completed'));
    assert.ok(recoveredTasks.every(task => task.usage.lead_input_tokens > 0 && task.usage.side_input_tokens > 0), 'Both model totals survive a fresh host');
    // Lifecycle operations must close both panes and expose one archive entry.
    main.setContentSize(1180, 860);
    await evaluate(main, () => {
      document.documentElement.dataset.theme = 'light';
      document.documentElement.style.setProperty('--conversation-message-font-size', '14px');
      window.dispatchEvent(new Event('wuu-content-size-change'));
      document.querySelector('.sidebar-collapse-toggle[aria-label="Expand left sidebar"]')?.click();
      const projectRow = [...document.querySelectorAll('.project-row')].find(node => node.textContent === 'Fusion acceptance');
      if (!projectRow.classList.contains('expanded')) projectRow.click();
    });
    const lifecycleTitle = 'Fusion lifecycle acceptance';
    await evaluate(main, async ({ id, title }) => { await window.wuu.renameThread(id, title); }, { id: leadID, title: lifecycleTitle });
    await waitFor(main, title => [...document.querySelectorAll('.thread-row-main')].some(node => node.textContent === title), lifecycleTitle);
    await evaluate(main, title => [...document.querySelectorAll('.thread-row-main')].find(node => node.textContent === title).click(), lifecycleTitle);
    await waitFor(main, title => document.querySelector('.conversation-title-heading h1')?.textContent === title, lifecycleTitle);
    await waitFor(main, () => document.querySelector('.session-message-source'));
    await evaluate(main, () => document.querySelector('.session-message-source').click());
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"]`), sideID);
    const retainedSide = await evaluate(main, async id => (await window.wuu.resumeThread(id)).thread, sideID);
    await capture('lifecycle-before-archive');
    await evaluate(main, title => [...document.querySelectorAll('.thread-row-main')].find(node => node.textContent === title).closest('.thread-row').querySelector('.archive').click(), lifecycleTitle);
    await waitFor(main, () => document.querySelectorAll('.conversation-split-pane').length === 0);
    const archivedPair = await evaluate(main, async ids => Promise.all(ids.map(async id => (await window.wuu.resumeThread(id)).thread)), [leadID, sideID]);
    assert.ok(archivedPair.every(member => member.archived));
    assert.equal(archivedPair[1].turns.length, retainedSide.turns.length);
    const archiveCatalog = await evaluate(main, async () => (await window.wuu.listArchivedThreads()).threads);
    assert.deepEqual(archiveCatalog.map(member => member.id), [leadID]);
    await evaluate(main, () => document.querySelector('.sidebar-account-trigger').click());
    await waitFor(main, () => document.querySelector('[data-settings-page="providers"]'));
    await evaluate(main, () => document.querySelector('[data-settings-page="providers"]').click());
    await waitFor(main, () => document.querySelector('.settings-nav-item'));
    await evaluate(main, () => [...document.querySelectorAll('.settings-nav-item')].find(node => node.textContent.trim() === 'Archive').click());
    await waitFor(main, () => document.querySelector('.settings-archive-row'));
    assert.equal(await evaluate(main, () => document.querySelectorAll('.settings-archive-row').length), 1);
    await capture('lifecycle-archive-one-entry');
    await evaluate(main, () => document.querySelector('.settings-archive-restore').click());
    await waitFor(main, () => document.querySelectorAll('.settings-archive-row').length === 0);
    const restoredPair = await evaluate(main, async ids => Promise.all(ids.map(async id => (await window.wuu.resumeThread(id)).thread)), [leadID, sideID]);
    assert.ok(restoredPair.every(member => !member.archived));
    assert.equal(restoredPair[1].turns.length, retainedSide.turns.length);
    await evaluate(main, () => document.querySelector('.settings-back-button').click());
    await waitFor(main, title => [...document.querySelectorAll('.thread-row-main')].some(node => node.textContent === title), lifecycleTitle);
    await evaluate(main, title => [...document.querySelectorAll('.thread-row-main')].find(node => node.textContent === title).click(), lifecycleTitle);
    await waitFor(main, title => document.querySelector('.conversation-title-heading h1')?.textContent === title, lifecycleTitle);
    await waitFor(main, () => document.querySelector('.session-message-source'));
    await evaluate(main, () => document.querySelector('.session-message-source').click());
    await waitFor(main, id => document.querySelector(`.conversation-split-pane[data-thread-id="${id}"]`), sideID);
    await capture('lifecycle-restored-pair');
    await evaluate(main, title => [...document.querySelectorAll('.thread-row-main')].find(node => node.textContent === title).dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 140, clientY: 220 })), lifecycleTitle);
    await waitFor(main, () => [...document.querySelectorAll('[role="menuitem"]')].some(node => node.textContent.trim() === 'Delete conversation…'));
    await evaluate(main, () => [...document.querySelectorAll('[role="menuitem"]')].find(node => node.textContent.trim() === 'Delete conversation…').click());
    await waitFor(main, () => document.querySelector('[data-confirm-action="confirm"]'));
    await evaluate(main, () => document.querySelector('[data-confirm-action="confirm"]').click());
    await waitFor(main, () => document.querySelectorAll('.conversation-split-pane').length === 0);
    const deletedPair = await evaluate(main, async ids => Promise.all(ids.map(async id => { try { await window.wuu.resumeThread(id); return false; } catch { return true; } })), [leadID, sideID]);
    assert.deepEqual(deletedPair, [true, true]);
    await capture('lifecycle-deleted-pair');
    fs.writeFileSync(path.join(output, 'lifecycle.json'), JSON.stringify({ leadID, sideID, archivedPair, archiveCatalog, restoredPair, deletedPair }, null, 2));
    fs.writeFileSync(path.join(output, 'cold-recovery.json'), JSON.stringify(recovered, null, 2));
    fs.writeFileSync(path.join(output, 'results.json'), JSON.stringify({ fixture, leadID, sideIDs: [...sideIDs], sourceIdentity, pair, thread, failureThread, correctedTask, readonlyTask, nestedThread, recovered, layouts, requests }, null, 2));
    for (const name of ['failure.json', 'failure.png']) fs.rmSync(path.join(output, name), { force: true });
    console.log(`Fusion acceptance passed. Evidence: ${output}`);
    clearTimeout(timeout); server.close(); app.quit();
  } catch (error) {
    const controls = main && await evaluate(main, () => [...document.querySelectorAll('button')].map(button => ({ label: button.getAttribute('aria-label'), title: button.getAttribute('title'), className: button.className, text: button.textContent }))).catch(() => []);
    fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify({ error: error.stack, fixture, layouts, requests, controls }, null, 2));
    if (main) await capture('failure').catch(() => {});
    console.error(error); server.close(); app.exit(1);
  }
});
